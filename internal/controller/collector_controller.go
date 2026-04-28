/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
	"github.com/grafana/fleet-management-operator/internal/controller/attributes"
	"github.com/grafana/fleet-management-operator/pkg/fleetclient"
)

const (
	collectorFinalizer = "collector.fleetmanagement.grafana.com/finalizer"

	collectorReasonSynced          = "Synced"
	collectorReasonSyncFailed      = "SyncFailed"
	collectorReasonNotRegistered   = "NotRegistered"
	collectorReasonValidationError = "ValidationError"
	collectorReasonDeleting        = "Deleting"
	collectorReasonDeleteFailed    = "DeleteFailed"

	collectorEventReasonSynced            = "Synced"
	collectorEventReasonSyncFailed        = "SyncFailed"
	collectorEventReasonNotRegistered     = "NotRegistered"
	collectorEventReasonRateLimited       = "RateLimited"
	collectorEventReasonAttributesUpdated = "AttributesUpdated"
	collectorEventReasonDeleted           = "Deleted"
	collectorEventReasonDeleteFailed      = "DeleteFailed"

	// notRegisteredRequeueAfter is the requeue delay used when the Collector
	// CR points at an ID that has not yet appeared in Fleet Management. The
	// expected fix is the collector itself calling RegisterCollector — at
	// most a few minutes after deployment — so we re-check on a coarse
	// schedule rather than thrashing.
	notRegisteredRequeueAfter = 30 * time.Second
)

// FleetCollectorClient is the controller-side abstraction over the Fleet
// Management collector service. Defined here (in the consumer package) per
// the project's interface-on-the-consumer-side convention so tests can mock
// it without depending on the real connect-go client.
type FleetCollectorClient interface {
	GetCollector(ctx context.Context, id string) (*fleetclient.Collector, error)
	BulkUpdateCollectors(ctx context.Context, ids []string, ops []*fleetclient.Operation) error
}

// CollectorReconciler reconciles a Collector object.
//
// Phase 1 scope: only spec.RemoteAttributes is reconciled to Fleet Management.
// spec.Name and spec.Enabled are accepted but not yet applied — that work
// arrives with Phase 2 once policy and external-sync layering is in place.
type CollectorReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	FleetClient FleetCollectorClient
	Recorder    record.EventRecorder
}

var _ reconcile.Reconciler = &CollectorReconciler{}

func (r *CollectorReconciler) emitEvent(object runtime.Object, eventtype, reason, message string) {
	if r.Recorder != nil {
		r.Recorder.Event(object, eventtype, reason, message)
	}
}

func (r *CollectorReconciler) emitEventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
	if r.Recorder != nil {
		r.Recorder.Eventf(object, eventtype, reason, messageFmt, args...)
	}
}

// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=collectors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=collectors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=collectors/finalizers,verbs=update
// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=remoteattributepolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=externalattributesyncs,verbs=get;list;watch

// Reconcile brings a Collector CR's remote attributes into agreement with
// Fleet Management. See the package-level docs in collector_controller.go for
// the full state machine.
func (r *CollectorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("reconciling Collector", "namespace", req.Namespace, "name", req.Name)

	collector := &fleetmanagementv1alpha1.Collector{}
	if err := r.Get(ctx, req.NamespacedName, collector); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Collector not found, likely deleted", "namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		log.Error(err, "failed to get Collector", "namespace", req.Namespace, "name", req.Name)
		return ctrl.Result{}, err
	}

	if !collector.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, collector)
	}

	if !controllerutil.ContainsFinalizer(collector, collectorFinalizer) {
		controllerutil.AddFinalizer(collector, collectorFinalizer)
		if err := r.Update(ctx, collector); err != nil {
			log.Error(err, "failed to add finalizer", "namespace", collector.Namespace, "name", collector.Name)
			return ctrl.Result{}, err
		}
		log.Info("added finalizer", "namespace", collector.Namespace, "name", collector.Name)
		return ctrl.Result{}, nil
	}

	// Note: there is intentionally no ObservedGeneration short-circuit
	// here. Cross-layer watches (RemoteAttributePolicy, ExternalAttributeSync)
	// trigger reconciles where the Collector spec generation is unchanged
	// but the upstream layers have moved; a strict generation check would
	// silently drop those updates. Idempotency is maintained inside
	// updateStatusSuccess, which suppresses the status write when nothing
	// actually changed.
	return r.reconcileNormal(ctx, collector)
}

// reconcileNormal handles create/update reconciliation.
func (r *CollectorReconciler) reconcileNormal(ctx context.Context, collector *fleetmanagementv1alpha1.Collector) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	live, err := r.FleetClient.GetCollector(ctx, collector.Spec.ID)
	if err != nil {
		var apiErr *fleetclient.FleetAPIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			log.Info("collector not yet registered with Fleet Management",
				"namespace", collector.Namespace, "name", collector.Name, "id", collector.Spec.ID)
			r.emitEventf(collector, corev1.EventTypeWarning, collectorEventReasonNotRegistered,
				"Collector %q has not yet registered with Fleet Management; will retry", collector.Spec.ID)
			result, statusErr := r.updateStatusNotRegistered(ctx, collector)
			if statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			return result, nil
		}
		return r.handleAPIError(ctx, collector, err)
	}

	externalLayer, err := r.externalSyncLayerForCollector(ctx, collector)
	if err != nil {
		log.Error(err, "failed to gather matching ExternalAttributeSyncs",
			"namespace", collector.Namespace, "name", collector.Name)
		return r.updateStatusError(ctx, collector, collectorReasonSyncFailed, err)
	}
	policyLayer, err := r.policyLayerForCollector(ctx, collector, live.LocalAttributes)
	if err != nil {
		log.Error(err, "failed to gather matching RemoteAttributePolicies",
			"namespace", collector.Namespace, "name", collector.Name)
		return r.updateStatusError(ctx, collector, collectorReasonSyncFailed, err)
	}
	collectorLayer := attributes.Layer{
		Kind:  string(fleetmanagementv1alpha1.AttributeOwnerCollector),
		Owner: fmt.Sprintf("%s/%s", collector.Namespace, collector.Name),
		Attrs: collector.Spec.RemoteAttributes,
	}

	// Precedence (high → low): ExternalAttributeSync, Collector spec, Policy.
	desired, owners := attributes.Merge(externalLayer, collectorLayer, policyLayer)
	previouslyOwned := allOwnedKeys(collector.Status.AttributeOwners)
	observed := live.RemoteAttributes
	if observed == nil {
		observed = map[string]string{}
	}

	ops := attributes.Diff(desired, observed, previouslyOwned)
	if len(ops) > 0 {
		if err := r.FleetClient.BulkUpdateCollectors(ctx, []string{collector.Spec.ID}, ops); err != nil {
			return r.handleAPIError(ctx, collector, err)
		}
		r.emitEventf(collector, corev1.EventTypeNormal, collectorEventReasonAttributesUpdated,
			"Applied %d attribute operation(s) to collector %q", len(ops), collector.Spec.ID)
	}

	return r.updateStatusSuccess(ctx, collector, live, desired, owners)
}

// externalSyncLayerForCollector lists every ExternalAttributeSync in the
// collector's namespace and aggregates the (key, value) entries those
// syncs claim for this collector via their status.ownedKeys field. When
// multiple syncs claim the same key for the same collector, the first one
// in alphabetical order wins — Phase 3 doesn't expose a priority knob on
// ExternalAttributeSync, so deterministic ordering is the safest default.
func (r *CollectorReconciler) externalSyncLayerForCollector(
	ctx context.Context,
	collector *fleetmanagementv1alpha1.Collector,
) (attributes.Layer, error) {
	var list fleetmanagementv1alpha1.ExternalAttributeSyncList
	if err := r.List(ctx, &list, client.InNamespace(collector.Namespace)); err != nil {
		return attributes.Layer{}, fmt.Errorf("listing ExternalAttributeSync: %w", err)
	}

	syncs := make([]*fleetmanagementv1alpha1.ExternalAttributeSync, 0, len(list.Items))
	for i := range list.Items {
		syncs = append(syncs, &list.Items[i])
	}
	sort.SliceStable(syncs, func(i, j int) bool {
		return syncs[i].Namespace+"/"+syncs[i].Name < syncs[j].Namespace+"/"+syncs[j].Name
	})

	resolved := map[string]string{}
	owners := []string{}
	for _, s := range syncs {
		entry := findOwnedEntry(s.Status.OwnedKeys, collector.Spec.ID)
		if entry == nil {
			continue
		}
		owners = append(owners, s.Namespace+"/"+s.Name)
		for k, v := range entry.Attributes {
			if _, claimed := resolved[k]; claimed {
				continue
			}
			resolved[k] = v
		}
	}

	owner := ""
	if len(owners) > 0 {
		owner = owners[0]
	}
	return attributes.Layer{
		Kind:  string(fleetmanagementv1alpha1.AttributeOwnerExternalAttributeSync),
		Owner: owner,
		Attrs: resolved,
	}, nil
}

// findOwnedEntry returns the OwnedKeyEntry matching collectorID, or nil.
func findOwnedEntry(entries []fleetmanagementv1alpha1.OwnedKeyEntry, collectorID string) *fleetmanagementv1alpha1.OwnedKeyEntry {
	for i := range entries {
		if entries[i].CollectorID == collectorID {
			return &entries[i]
		}
	}
	return nil
}

// policyLayerForCollector lists every RemoteAttributePolicy in the collector's
// namespace, filters to the ones whose selector matches, and resolves
// priority/name tie-breaks into a single attribute map.
//
// Phase 2 keeps this O(N) over policies in the namespace; Phase 3+ may add
// label-selector-driven indexing if it becomes a bottleneck.
func (r *CollectorReconciler) policyLayerForCollector(
	ctx context.Context,
	collector *fleetmanagementv1alpha1.Collector,
	localAttrs map[string]string,
) (attributes.Layer, error) {
	var list fleetmanagementv1alpha1.RemoteAttributePolicyList
	if err := r.List(ctx, &list, client.InNamespace(collector.Namespace)); err != nil {
		return attributes.Layer{}, fmt.Errorf("listing RemoteAttributePolicy: %w", err)
	}

	type matchedPolicy struct {
		ref      *fleetmanagementv1alpha1.RemoteAttributePolicy
		fullName string
	}
	var matches []matchedPolicy
	for i := range list.Items {
		p := &list.Items[i]
		sel := attributes.Selector{
			Matchers:     p.Spec.Selector.Matchers,
			CollectorIDs: p.Spec.Selector.CollectorIDs,
		}
		if !sel.Match(collector.Spec.ID, localAttrs) {
			continue
		}
		matches = append(matches, matchedPolicy{
			ref:      p,
			fullName: p.Namespace + "/" + p.Name,
		})
	}

	// Highest Priority wins; equal Priority broken alphabetically by
	// namespaced name so reconciliation is deterministic.
	sort.SliceStable(matches, func(i, j int) bool {
		pi, pj := matches[i].ref.Spec.Priority, matches[j].ref.Spec.Priority
		if pi != pj {
			return pi > pj
		}
		return matches[i].fullName < matches[j].fullName
	})

	resolved := map[string]string{}
	owners := []string{}
	for _, m := range matches {
		owners = append(owners, m.fullName)
		for k, v := range m.ref.Spec.Attributes {
			if _, claimed := resolved[k]; claimed {
				continue
			}
			resolved[k] = v
		}
	}

	owner := ""
	if len(owners) > 0 {
		owner = owners[0]
	}
	return attributes.Layer{
		Kind:  string(fleetmanagementv1alpha1.AttributeOwnerRemoteAttributePolicy),
		Owner: owner,
		Attrs: resolved,
	}, nil
}

// reconcileDelete removes every attribute this Collector CR caused to be
// written to Fleet Management — across all owner kinds, since the Collector
// reconciler is the sole writer to Fleet for the collectors it manages — and
// then drops the finalizer so garbage collection can complete.
func (r *CollectorReconciler) reconcileDelete(ctx context.Context, collector *fleetmanagementv1alpha1.Collector) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(collector, collectorFinalizer) {
		return ctrl.Result{}, nil
	}

	owned := allOwnedKeys(collector.Status.AttributeOwners)
	if len(owned) > 0 {
		ops := make([]*fleetclient.Operation, 0, len(owned))
		for _, key := range owned {
			ops = append(ops, &fleetclient.Operation{
				Op:   fleetclient.OpRemove,
				Path: "/remote_attributes/" + key,
			})
		}

		if err := r.FleetClient.BulkUpdateCollectors(ctx, []string{collector.Spec.ID}, ops); err != nil {
			var apiErr *fleetclient.FleetAPIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
				log.Info("collector already gone from Fleet Management on delete",
					"namespace", collector.Namespace, "name", collector.Name, "id", collector.Spec.ID)
				r.emitEvent(collector, corev1.EventTypeNormal, collectorEventReasonDeleted,
					"Collector already absent from Fleet Management")
			} else {
				log.Error(err, "failed to remove owned attributes during delete",
					"namespace", collector.Namespace, "name", collector.Name)
				r.emitEventf(collector, corev1.EventTypeWarning, collectorEventReasonDeleteFailed,
					"Failed to remove owned attributes from collector %q: %v", collector.Spec.ID, err)
				return r.updateStatusError(ctx, collector, collectorReasonDeleteFailed, err)
			}
		} else {
			log.Info("removed owned attributes from Fleet Management on delete",
				"namespace", collector.Namespace, "name", collector.Name, "removedKeys", owned)
		}
	}

	controllerutil.RemoveFinalizer(collector, collectorFinalizer)
	if err := r.Update(ctx, collector); err != nil {
		log.Error(err, "failed to remove finalizer",
			"namespace", collector.Namespace, "name", collector.Name)
		return ctrl.Result{}, err
	}

	fleetResourceSyncedTotal.WithLabelValues("Collector", "Deleted").Inc()
	log.Info("removed finalizer, collector resource will be garbage-collected",
		"namespace", collector.Namespace, "name", collector.Name)
	return ctrl.Result{}, nil
}

// handleAPIError mirrors the Pipeline controller's error classification: 4xx
// (except 408/429) is permanent; 429 requeues with a fixed delay; 5xx and
// other errors return for exponential backoff.
func (r *CollectorReconciler) handleAPIError(ctx context.Context, collector *fleetmanagementv1alpha1.Collector, err error) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var apiErr *fleetclient.FleetAPIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusBadRequest:
			log.Info("validation error from Fleet Management API",
				"namespace", collector.Namespace, "name", collector.Name, "message", apiErr.Message)
			r.emitEventf(collector, corev1.EventTypeWarning, collectorEventReasonSyncFailed,
				"Fleet Management API validation failed: %s", apiErr.Message)
			return r.updateStatusError(ctx, collector, collectorReasonValidationError, err)

		case http.StatusTooManyRequests:
			log.Info("rate limited by Fleet Management API, requeueing",
				"namespace", collector.Namespace, "name", collector.Name)
			r.emitEvent(collector, corev1.EventTypeWarning, collectorEventReasonRateLimited,
				"Rate limited by Fleet Management API, will retry in 10 seconds")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil

		default:
			log.Error(err, "Fleet Management API error",
				"namespace", collector.Namespace, "name", collector.Name,
				"statusCode", apiErr.StatusCode, "operation", apiErr.Operation,
				"message", apiErr.Message)
			r.emitEventf(collector, corev1.EventTypeWarning, collectorEventReasonSyncFailed,
				"Fleet Management API error (HTTP %d): %s", apiErr.StatusCode, apiErr.Message)
			return r.updateStatusError(ctx, collector, collectorReasonSyncFailed, err)
		}
	}

	log.Error(err, "failed to sync collector with Fleet Management",
		"namespace", collector.Namespace, "name", collector.Name)
	r.emitEventf(collector, corev1.EventTypeWarning, collectorEventReasonSyncFailed,
		"Failed to sync with Fleet Management: %v", err)
	return r.updateStatusError(ctx, collector, collectorReasonSyncFailed, err)
}

// updateStatusSuccess mirrors observed Fleet state and records the keys this
// reconcile claims ownership of. The status write is suppressed when every
// observable field is already up-to-date so the cross-layer watches don't
// produce a feedback loop of no-op reconciles.
func (r *CollectorReconciler) updateStatusSuccess(
	ctx context.Context,
	collector *fleetmanagementv1alpha1.Collector,
	live *fleetclient.Collector,
	desired map[string]string,
	owners []attributes.KeyOwnership,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	newOwners := ownersFromMerge(owners)
	newCollectorType := fleetmanagementv1alpha1.CollectorTypeFromFleetAPI(live.CollectorType)

	// Idempotency: skip the Status write entirely when nothing observable
	// has changed AND the Ready condition already reflects success at the
	// current generation.
	readyAtCurrentGen := false
	for _, c := range collector.Status.Conditions {
		if c.Type == conditionTypeReady && c.Status == metav1.ConditionTrue && c.ObservedGeneration == collector.Generation {
			readyAtCurrentGen = true
			break
		}
	}
	noopStatus := readyAtCurrentGen &&
		collector.Status.ObservedGeneration == collector.Generation &&
		collector.Status.Registered &&
		collector.Status.CollectorType == newCollectorType &&
		mapsEqual(collector.Status.LocalAttributes, live.LocalAttributes) &&
		mapsEqual(collector.Status.EffectiveRemoteAttributes, desired) &&
		ownerSlicesEqual(collector.Status.AttributeOwners, newOwners)
	if noopStatus {
		log.V(1).Info("collector status unchanged, skipping write",
			"namespace", collector.Namespace, "name", collector.Name)
		return ctrl.Result{}, nil
	}

	// OBS-03: record sync age using the previous LastPing before overwriting it
	if collector.Status.LastPing != nil && !collector.Status.LastPing.IsZero() {
		fleetResourceSyncAge.WithLabelValues("Collector").
			Observe(time.Since(collector.Status.LastPing.Time).Seconds())
	}

	collector.Status.ObservedGeneration = collector.Generation
	collector.Status.Registered = true
	collector.Status.CollectorType = newCollectorType
	collector.Status.LocalAttributes = live.LocalAttributes
	collector.Status.EffectiveRemoteAttributes = copyMap(desired)
	collector.Status.AttributeOwners = newOwners

	// LastPing is the closest proxy Fleet Management exposes for collector
	// activity (it does not publish a true last-ping timestamp). UpdatedAt
	// advances on every collector check-in, so we surface it here.
	if live.UpdatedAt != nil {
		collector.Status.LastPing = &metav1.Time{Time: *live.UpdatedAt}
	}

	meta.SetStatusCondition(&collector.Status.Conditions, metav1.Condition{
		Type:               conditionTypeReady,
		Status:             metav1.ConditionTrue,
		Reason:             collectorReasonSynced,
		Message:            "Collector successfully synced to Fleet Management",
		ObservedGeneration: collector.Generation,
	})
	meta.SetStatusCondition(&collector.Status.Conditions, metav1.Condition{
		Type:               conditionTypeSynced,
		Status:             metav1.ConditionTrue,
		Reason:             collectorReasonSynced,
		Message:            fmt.Sprintf("Reconciled %d remote attribute(s) on collector %q", len(desired), collector.Spec.ID),
		ObservedGeneration: collector.Generation,
	})

	if err := r.Status().Update(ctx, collector); err != nil {
		if apierrors.IsConflict(err) {
			log.V(1).Info("status update conflict, requeueing",
				"namespace", collector.Namespace, "name", collector.Name)
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "failed to update status",
			"namespace", collector.Namespace, "name", collector.Name)
		return ctrl.Result{}, err
	}

	r.emitEvent(collector, corev1.EventTypeNormal, collectorEventReasonSynced,
		"Collector successfully synced to Fleet Management")

	fleetResourceSyncedTotal.WithLabelValues("Collector", collectorReasonSynced).Inc()
	log.Info("successfully reconciled collector",
		"namespace", collector.Namespace, "name", collector.Name,
		"id", collector.Spec.ID, "generation", collector.Generation)
	return ctrl.Result{}, nil
}

// updateStatusNotRegistered records the "waiting for collector to register"
// state without flagging an error.
func (r *CollectorReconciler) updateStatusNotRegistered(ctx context.Context, collector *fleetmanagementv1alpha1.Collector) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	collector.Status.ObservedGeneration = collector.Generation
	collector.Status.Registered = false

	meta.SetStatusCondition(&collector.Status.Conditions, metav1.Condition{
		Type:               conditionTypeReady,
		Status:             metav1.ConditionFalse,
		Reason:             collectorReasonNotRegistered,
		Message:            fmt.Sprintf("Collector %q has not yet registered with Fleet Management", collector.Spec.ID),
		ObservedGeneration: collector.Generation,
	})
	meta.SetStatusCondition(&collector.Status.Conditions, metav1.Condition{
		Type:               conditionTypeSynced,
		Status:             metav1.ConditionFalse,
		Reason:             collectorReasonNotRegistered,
		Message:            "Awaiting collector registration",
		ObservedGeneration: collector.Generation,
	})

	if err := r.Status().Update(ctx, collector); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "failed to update NotRegistered status",
			"namespace", collector.Namespace, "name", collector.Name)
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: notRegisteredRequeueAfter}, nil
}

// updateStatusError records reconciliation failure with a user-friendly
// condition message, preserving the original error so controller-runtime can
// drive exponential backoff.
func (r *CollectorReconciler) updateStatusError(ctx context.Context, collector *fleetmanagementv1alpha1.Collector, reason string, originalErr error) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	collector.Status.ObservedGeneration = collector.Generation
	formatted := formatConditionMessage(reason, originalErr)

	meta.SetStatusCondition(&collector.Status.Conditions, metav1.Condition{
		Type:               conditionTypeReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            formatted,
		ObservedGeneration: collector.Generation,
	})
	meta.SetStatusCondition(&collector.Status.Conditions, metav1.Condition{
		Type:               conditionTypeSynced,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            formatted,
		ObservedGeneration: collector.Generation,
	})

	if updateErr := r.Status().Update(ctx, collector); updateErr != nil {
		if apierrors.IsConflict(updateErr) {
			log.V(1).Info("status update conflict during error handling, requeueing",
				"namespace", collector.Namespace, "name", collector.Name)
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(updateErr, "failed to update status after reconciliation error",
			"namespace", collector.Namespace, "name", collector.Name,
			"originalError", originalErr.Error(), "reason", reason)
	}

	if !shouldRetry(originalErr, reason) {
		log.Info("validation error, not requeueing",
			"namespace", collector.Namespace, "name", collector.Name, "error", originalErr.Error())
		fleetResourceSyncedTotal.WithLabelValues("Collector", reason).Inc()
		return ctrl.Result{}, nil
	}

	fleetResourceSyncedTotal.WithLabelValues("Collector", reason).Inc()
	return ctrl.Result{}, originalErr
}

// SetupWithManager wires the watches for this reconciler. The CollectorReconciler
// is the sole writer to Fleet for collector remote attributes, so it must
// re-reconcile when any input layer changes — its own CR (For), any
// RemoteAttributePolicy that may match it, and any ExternalAttributeSync
// whose status.ownedKeys touches it.
func (r *CollectorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleetmanagementv1alpha1.Collector{}).
		Watches(
			&fleetmanagementv1alpha1.RemoteAttributePolicy{},
			handler.EnqueueRequestsFromMapFunc(r.collectorsMatchedByPolicy),
		).
		Watches(
			&fleetmanagementv1alpha1.ExternalAttributeSync{},
			handler.EnqueueRequestsFromMapFunc(r.collectorsTouchedBySync),
		).
		Named("collector").
		Complete(r)
}

// collectorsTouchedBySync returns reconcile requests for every Collector CR
// in the namespace whose ID appears in the changed ExternalAttributeSync's
// status.ownedKeys. We also include the inverse — any Collector that was
// previously claimed by this sync but has since been dropped — by simply
// listing all Collectors in the namespace; the cost is bounded and the
// alternative (tracking per-sync claim history) would add notable
// complexity for marginal benefit at Phase 3 volumes.
func (r *CollectorReconciler) collectorsTouchedBySync(ctx context.Context, obj client.Object) []reconcile.Request {
	sync, ok := obj.(*fleetmanagementv1alpha1.ExternalAttributeSync)
	if !ok {
		return nil
	}

	var collectors fleetmanagementv1alpha1.CollectorList
	if err := r.List(ctx, &collectors, client.InNamespace(sync.Namespace)); err != nil {
		logf.FromContext(ctx).Error(err, "listing collectors for external-sync watch",
			"sync", sync.Namespace+"/"+sync.Name)
		return nil
	}

	var requests []reconcile.Request
	for i := range collectors.Items {
		c := &collectors.Items[i]
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKey{Namespace: c.Namespace, Name: c.Name},
		})
	}
	return requests
}

// collectorsMatchedByPolicy is the watch map function for RemoteAttributePolicy
// changes. Given a policy, it returns reconcile requests for every Collector in
// the same namespace whose ID or local attributes match the policy's selector.
func (r *CollectorReconciler) collectorsMatchedByPolicy(ctx context.Context, obj client.Object) []reconcile.Request {
	policy, ok := obj.(*fleetmanagementv1alpha1.RemoteAttributePolicy)
	if !ok {
		return nil
	}

	var collectors fleetmanagementv1alpha1.CollectorList
	if err := r.List(ctx, &collectors, client.InNamespace(policy.Namespace)); err != nil {
		// Soft-fail: if the list errors, we cannot enqueue, but a subsequent
		// Collector or Policy event will retry.
		logf.FromContext(ctx).Error(err, "listing collectors for policy watch",
			"policy", policy.Namespace+"/"+policy.Name)
		return nil
	}

	sel := attributes.Selector{
		Matchers:     policy.Spec.Selector.Matchers,
		CollectorIDs: policy.Spec.Selector.CollectorIDs,
	}

	var requests []reconcile.Request
	for i := range collectors.Items {
		c := &collectors.Items[i]
		if !sel.Match(c.Spec.ID, c.Status.LocalAttributes) {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKey{Namespace: c.Namespace, Name: c.Name},
		})
	}
	return requests
}

// allOwnedKeys returns every key in status.AttributeOwners regardless of
// owner kind. The Collector controller is the sole writer to Fleet, so every
// recorded owner is something it wrote on the previous reconcile and must
// consider when computing REMOVE ops.
func allOwnedKeys(owners []fleetmanagementv1alpha1.AttributeOwnership) []string {
	keys := make([]string, 0, len(owners))
	for _, o := range owners {
		keys = append(keys, o.Key)
	}
	sort.Strings(keys)
	return keys
}

// ownersFromMerge converts the attributes-package KeyOwnership records (which
// stay decoupled from the v1alpha1 types) into the AttributeOwnership status
// entries the CRD exposes. The resulting slice is sorted by key for stable
// status output.
func ownersFromMerge(in []attributes.KeyOwnership) []fleetmanagementv1alpha1.AttributeOwnership {
	out := make([]fleetmanagementv1alpha1.AttributeOwnership, 0, len(in))
	for _, o := range in {
		out = append(out, fleetmanagementv1alpha1.AttributeOwnership{
			Key:       o.Key,
			OwnerKind: fleetmanagementv1alpha1.AttributeOwnerKind(o.Kind),
			OwnerName: o.Owner,
			Value:     o.Value,
		})
	}
	return out
}

// copyMap returns a shallow copy of m so callers can mutate either side
// without affecting the other.
func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// mapsEqual returns true if two string-to-string maps have the same key
// set and identical values. Used to short-circuit no-op status writes.
func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// ownerSlicesEqual compares two AttributeOwnership slices treating them as
// sets keyed on Key. The Collector controller writes owners in key-sorted
// order, but treating them as sets is more robust against incidental
// reordering from upstream code.
func ownerSlicesEqual(a, b []fleetmanagementv1alpha1.AttributeOwnership) bool {
	if len(a) != len(b) {
		return false
	}
	idx := make(map[string]fleetmanagementv1alpha1.AttributeOwnership, len(a))
	for _, o := range a {
		idx[o.Key] = o
	}
	for _, o := range b {
		other, ok := idx[o.Key]
		if !ok {
			return false
		}
		if other.OwnerKind != o.OwnerKind || other.OwnerName != o.OwnerName || other.Value != o.Value {
			return false
		}
	}
	return true
}
