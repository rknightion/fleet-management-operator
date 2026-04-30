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
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
	"github.com/grafana/fleet-management-operator/internal/controller/discovery"
	"github.com/grafana/fleet-management-operator/pkg/fleetclient"
)

const (
	pipelineDiscoveryReasonSynced        = "Synced"
	pipelineDiscoveryReasonListFailed    = "ListPipelinesFailed"
	pipelineDiscoveryReasonUpsertFailed  = "UpsertFailed"
	pipelineDiscoveryReasonStaleFailed   = "StaleProcessingFailed"
	pipelineDiscoveryReasonInvalidConfig = "InvalidConfig"

	pipelineDiscoveryEventReasonDiscovered = "PipelineDiscovered"
	pipelineDiscoveryEventReasonPruned     = "PipelinePruned"
	pipelineDiscoveryEventReasonConflict   = "PipelineConflict"
	pipelineDiscoveryEventReasonSynced     = "Synced"
	pipelineDiscoveryEventReasonFailed     = "SyncFailed"

	// pipelineDiscoveryConditionTruncatedConflicts is the condition type set
	// when the conflict list exceeds maxPipelineDiscoveryConflicts.
	pipelineDiscoveryConditionTruncatedConflicts = "TruncatedConflicts"
	pipelineDiscoveryReasonConflictsTruncated    = "ConflictListTruncated"
	pipelineDiscoveryReasonNoConflictsTruncated  = "NoConflictsTruncated"

	// maxPipelineDiscoveryConflicts caps status.conflicts. Conflicts are
	// diagnostic-only; no controller reads this slice to drive behaviour, so
	// capping at 100 is safe.
	maxPipelineDiscoveryConflicts = 100

	// defaultPipelineDiscoveryPollInterval mirrors the schema default.
	defaultPipelineDiscoveryPollInterval = 5 * time.Minute
)

// PipelineDiscoveryFleetClient is the controller-side abstraction over the
// Fleet Management pipeline list endpoint. Defined here per the project's
// interface-on-the-consumer-side convention so tests can mock it
// independently of the real connect-go client.
type PipelineDiscoveryFleetClient interface {
	ListPipelines(ctx context.Context, req *fleetclient.ListPipelinesRequest) ([]*fleetclient.Pipeline, error)
}

// PipelineDiscoveryReconciler reconciles a PipelineDiscovery object.
//
// MaxConcurrentReconciles defaults to 1. Discovery is poll-driven:
// concurrency > 1 can trigger multiple ListPipelines calls per poll cycle
// without benefit, consuming unnecessary Fleet API budget.
//
// On each reconcile (poll cadence or spec change) the controller:
//
//  1. Reads spec.pollInterval and skips if not yet due.
//  2. Calls ListPipelines with spec.selector filters.
//  3. Upsert phase: for each Fleet pipeline, computes a DNS-1123 name
//     and ensures a Pipeline CR exists in the target namespace, labelled
//     and annotated to mark it as managed by this PipelineDiscovery.
//     Existing CRs owned by another discovery or by a manual user are
//     left alone and surfaced in status.conflicts.
//  4. Stale phase: for every label-matched CR whose Fleet pipeline is no
//     longer in the result, applies the policy.onPipelineRemoved decision.
//  5. Updates status and requeues at pollInterval.
type PipelineDiscoveryReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	FleetClient PipelineDiscoveryFleetClient
	Recorder    events.EventRecorder

	// MaxConcurrentReconciles controls controller-runtime worker concurrency.
	// Zero defaults to 1.
	MaxConcurrentReconciles int

	// Now is overridable in tests. Defaults to time.Now.
	Now func() time.Time
}

var _ reconcile.Reconciler = &PipelineDiscoveryReconciler{}

func (r *PipelineDiscoveryReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *PipelineDiscoveryReconciler) emitEventf(object runtime.Object, eventtype, reason, messageFmt string, args ...any) {
	if r.Recorder != nil {
		r.Recorder.Eventf(object, nil, eventtype, reason, reason, messageFmt, args...)
	}
}

// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=pipelinediscoveries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=pipelinediscoveries/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=pipelines,verbs=get;list;watch;create;update;patch;delete

// Reconcile lists pipelines from Fleet and reconciles the matching set of
// Pipeline CRs in the target namespace.
func (r *PipelineDiscoveryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("reconciling PipelineDiscovery", "namespace", req.Namespace, "name", req.Name)

	// Step 1: Fetch PipelineDiscovery CR.
	pd := &v1alpha1.PipelineDiscovery{}
	if err := r.Get(ctx, req.NamespacedName, pd); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Skip if being deleted. No finalizer; orphan-on-delete by design.
	if !pd.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Step 2: Parse poll interval and apply schedule check.
	pollInterval, err := r.parsePollInterval(pd)
	if err != nil {
		log.Error(err, "invalid pollInterval", "namespace", pd.Namespace, "name", pd.Name)
		r.emitEventf(pd, corev1.EventTypeWarning, pipelineDiscoveryEventReasonFailed,
			"Invalid pollInterval %q: %v", pd.Spec.PollInterval, err)
		result, updateErr := r.updateStatusError(ctx, pd, pipelineDiscoveryReasonInvalidConfig, err)
		if updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return result, err
	}

	now := r.now()

	if pd.Status.LastSyncTime != nil && pd.Status.ObservedGeneration == pd.Generation {
		nextDue := pd.Status.LastSyncTime.Add(pollInterval)
		if now.Before(nextDue) {
			return ctrl.Result{RequeueAfter: nextDue.Sub(now)}, nil
		}
	}

	targetNS := strings.TrimSpace(pd.Spec.TargetNamespace)
	if targetNS == "" {
		targetNS = pd.Namespace
	}

	// Step 3: Build ListPipelinesRequest from selector.
	listReq := r.buildListRequest(pd)

	// Step 4: Call ListPipelines.
	pipelines, err := r.FleetClient.ListPipelines(ctx, listReq)
	if err != nil {
		log.Error(err, "ListPipelines failed", "namespace", pd.Namespace, "name", pd.Name)
		r.emitEventf(pd, corev1.EventTypeWarning, pipelineDiscoveryEventReasonFailed,
			"ListPipelines failed: %v", err)
		if _, statusErr := r.updateStatusError(ctx, pd, pipelineDiscoveryReasonListFailed, err); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{}, err
	}

	// Step 5-6: Upsert phase.
	conflicts, currentNames, err := r.upsertPipelineCRs(ctx, pd, pipelines, targetNS)
	if err != nil {
		log.Error(err, "upsert phase failed", "namespace", pd.Namespace, "name", pd.Name)
		r.emitEventf(pd, corev1.EventTypeWarning, pipelineDiscoveryEventReasonFailed,
			"Upsert phase failed: %v", err)
		if _, statusErr := r.updateStatusError(ctx, pd, pipelineDiscoveryReasonUpsertFailed, err); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{}, err
	}

	// Step 7: Stale phase.
	stale, managed, err := r.processStaleP(ctx, pd, currentNames, targetNS)
	if err != nil {
		log.Error(err, "stale phase failed", "namespace", pd.Namespace, "name", pd.Name)
		r.emitEventf(pd, corev1.EventTypeWarning, pipelineDiscoveryEventReasonFailed,
			"Stale phase failed: %v", err)
		if _, statusErr := r.updateStatusError(ctx, pd, pipelineDiscoveryReasonStaleFailed, err); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{}, err
	}

	for _, conflict := range conflicts {
		r.emitEventf(pd, corev1.EventTypeWarning, pipelineDiscoveryEventReasonConflict,
			"Could not claim Pipeline %s/%s for pipeline %q: %s",
			targetNS, conflict.CRName, conflict.PipelineID, conflict.Reason)
	}

	// Step 8: Update status.
	updated, err := r.updateStatusSuccess(ctx, pd, len(pipelines), managed, stale, conflicts, now)
	if err != nil {
		return ctrl.Result{}, err
	}
	if updated {
		r.emitEventf(pd, corev1.EventTypeNormal, pipelineDiscoveryEventReasonSynced,
			"Discovered %d pipeline(s); managing %d, %d stale, %d conflict(s)",
			len(pipelines), managed, len(stale), len(conflicts))
	}

	// Step 9: Requeue at pollInterval.
	return ctrl.Result{RequeueAfter: pollInterval}, nil
}

// parsePollInterval reads spec.pollInterval and applies the schema default
// for empty values.
func (r *PipelineDiscoveryReconciler) parsePollInterval(pd *v1alpha1.PipelineDiscovery) (time.Duration, error) {
	raw := strings.TrimSpace(pd.Spec.PollInterval)
	if raw == "" {
		return defaultPipelineDiscoveryPollInterval, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("pollInterval %q is not a valid Go duration: %w", raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("pollInterval %q (%s) must be positive", raw, d)
	}
	return d, nil
}

// buildListRequest constructs a fleetclient.ListPipelinesRequest from the
// PipelineDiscovery selector spec.
func (r *PipelineDiscoveryReconciler) buildListRequest(pd *v1alpha1.PipelineDiscovery) *fleetclient.ListPipelinesRequest {
	req := &fleetclient.ListPipelinesRequest{}
	if pd.Spec.Selector.ConfigType != nil {
		s := pd.Spec.Selector.ConfigType.ToFleetAPI()
		req.ConfigType = &s
	}
	if pd.Spec.Selector.Enabled != nil {
		enabled := *pd.Spec.Selector.Enabled
		req.Enabled = &enabled
	}
	return req
}

// upsertPipelineCRs creates a Pipeline CR for each Fleet pipeline and records
// conflicts where a same-named CR exists but is not owned by this
// PipelineDiscovery. Returns the conflict list and the set of Fleet pipeline
// names that this discovery now claims.
func (r *PipelineDiscoveryReconciler) upsertPipelineCRs(
	ctx context.Context,
	pd *v1alpha1.PipelineDiscovery,
	pipelines []*fleetclient.Pipeline,
	targetNS string,
) ([]v1alpha1.PipelineDiscoveryConflict, map[string]struct{}, error) {
	conflicts := make([]v1alpha1.PipelineDiscoveryConflict, 0)
	// currentNames maps Fleet pipeline ID (from FleetPipelineIDAnnotation) to struct{}
	// for stale detection.
	currentNames := make(map[string]struct{}, len(pipelines))

	for _, fp := range pipelines {
		if fp == nil || strings.TrimSpace(fp.Name) == "" {
			continue
		}

		crName := choosePipelineCRName(fp.Name)
		if !discovery.IsValidDNS1123(crName) {
			conflicts = append(conflicts, v1alpha1.PipelineDiscoveryConflict{
				PipelineID: fp.Name,
				CRName:     crName,
				Reason:     v1alpha1.PipelineDiscoveryConflictSanitizeFailed,
			})
			continue
		}

		existing := &v1alpha1.Pipeline{}
		key := client.ObjectKey{Namespace: targetNS, Name: crName}
		err := r.Get(ctx, key, existing)
		if err != nil && !apierrors.IsNotFound(err) {
			return nil, nil, fmt.Errorf("get pipeline %s: %w", key, err)
		}

		if apierrors.IsNotFound(err) {
			// Create a new Pipeline CR.
			paused := pd.Spec.ImportMode == v1alpha1.PipelineDiscoveryImportModeReadOnly
			enabled := fp.Enabled
			pipeline := &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      crName,
					Namespace: targetNS,
					Labels: map[string]string{
						v1alpha1.PipelineDiscoveryNameLabel:      pd.Name,
						v1alpha1.PipelineDiscoveryNamespaceLabel: pd.Namespace,
					},
					Annotations: map[string]string{
						v1alpha1.PipelineDiscoveredByAnnotation: pipelineDiscoveryOwnerAnnotation(pd),
						v1alpha1.FleetPipelineIDAnnotation:      fp.ID,
					},
				},
				Spec: v1alpha1.PipelineSpec{
					Name:       fp.Name,
					Contents:   fp.Contents,
					Matchers:   fp.Matchers,
					Enabled:    &enabled,
					ConfigType: v1alpha1.ConfigTypeFromFleetAPI(fp.ConfigType),
					Paused:     paused,
				},
			}
			if createErr := r.Create(ctx, pipeline); createErr != nil {
				if apierrors.IsAlreadyExists(createErr) {
					// Race: another reconcile won. Treat as success; next
					// reconcile will re-evaluate ownership.
					currentNames[fp.ID] = struct{}{}
					continue
				}
				return nil, nil, fmt.Errorf("create pipeline %s: %w", key, createErr)
			}
			currentNames[fp.ID] = struct{}{}
			r.emitEventf(pd, corev1.EventTypeNormal, pipelineDiscoveryEventReasonDiscovered,
				"Created Pipeline %s for Fleet pipeline %q", key, fp.Name)
			continue
		}

		// CR already exists — check ownership.
		_, owned := existing.Labels[v1alpha1.PipelineDiscoveryNameLabel]
		switch {
		case !owned:
			// Manually-created CR with the same name. Skip.
			conflicts = append(conflicts, v1alpha1.PipelineDiscoveryConflict{
				PipelineID: fp.Name,
				CRName:     crName,
				Reason:     v1alpha1.PipelineDiscoveryConflictNotOwned,
			})
		case !pipelineIsOwnedByDiscovery(existing, pd):
			// Owned by a different PipelineDiscovery; first-write wins.
			conflicts = append(conflicts, v1alpha1.PipelineDiscoveryConflict{
				PipelineID: fp.Name,
				CRName:     crName,
				Reason:     v1alpha1.PipelineDiscoveryConflictOwnedByOther,
			})
		case existing.Annotations[v1alpha1.FleetPipelineIDAnnotation] != fp.ID:
			// Hash-suffix collision: same name, different pipeline IDs.
			conflicts = append(conflicts, v1alpha1.PipelineDiscoveryConflict{
				PipelineID: fp.Name,
				CRName:     crName,
				Reason:     v1alpha1.PipelineDiscoveryConflictSanitizeFailed,
			})
		default:
			// Ours and matches; clear any stale annotation.
			currentNames[fp.ID] = struct{}{}
			if err := r.clearPipelineStaleAnnotation(ctx, existing); err != nil {
				return nil, nil, err
			}
		}
	}

	// Sort for deterministic status output.
	sort.Slice(conflicts, func(i, j int) bool {
		return conflicts[i].PipelineID < conflicts[j].PipelineID
	})
	return conflicts, currentNames, nil
}

// processStaleP handles Pipeline CRs labelled as managed by this
// PipelineDiscovery whose Fleet pipeline ID is not in the current Fleet
// result. Returns the sorted list of stale pipeline names and the count of
// CRs still managed (after any deletes).
func (r *PipelineDiscoveryReconciler) processStaleP(
	ctx context.Context,
	pd *v1alpha1.PipelineDiscovery,
	currentNames map[string]struct{},
	targetNS string,
) ([]string, int, error) {
	var crs v1alpha1.PipelineList
	selector := client.MatchingLabels{
		v1alpha1.PipelineDiscoveryNameLabel: pd.Name,
	}
	if err := r.List(ctx, &crs, client.InNamespace(targetNS), selector); err != nil {
		return nil, 0, fmt.Errorf("list managed pipelines in %s: %w", targetNS, err)
	}

	managed := 0
	onRemoved := pd.Spec.Policy.OnPipelineRemoved
	if onRemoved == "" {
		onRemoved = v1alpha1.PipelineDiscoveryOnRemovedKeep
	}

	staleNames := make([]string, 0)
	for i := range crs.Items {
		cr := &crs.Items[i]
		if !pipelineIsOwnedByDiscovery(cr, pd) {
			continue
		}
		managed++
		fleetID := cr.Annotations[v1alpha1.FleetPipelineIDAnnotation]
		if _, present := currentNames[fleetID]; present {
			// Still in Fleet; clear any stale annotation.
			if err := r.clearPipelineStaleAnnotation(ctx, cr); err != nil {
				return nil, 0, err
			}
			continue
		}

		// Vanished from Fleet.
		switch onRemoved {
		case v1alpha1.PipelineDiscoveryOnRemovedDelete:
			if err := r.Delete(ctx, cr); err != nil && !apierrors.IsNotFound(err) {
				return nil, 0, fmt.Errorf("delete stale pipeline %s/%s: %w", cr.Namespace, cr.Name, err)
			}
			r.emitEventf(pd, corev1.EventTypeNormal, pipelineDiscoveryEventReasonPruned,
				"Deleted Pipeline %s/%s (pipeline %q no longer in Fleet)",
				cr.Namespace, cr.Name, cr.Spec.Name)
			managed--
		default: // Keep
			staleNames = append(staleNames, cr.Spec.Name)
			if err := r.setPipelineStaleAnnotation(ctx, cr); err != nil {
				return nil, 0, err
			}
		}
	}

	sort.Strings(staleNames)
	return staleNames, managed, nil
}

func pipelineDiscoveryOwnerAnnotation(pd *v1alpha1.PipelineDiscovery) string {
	return pd.Namespace + "/" + pd.Name
}

func pipelineIsOwnedByDiscovery(cr *v1alpha1.Pipeline, pd *v1alpha1.PipelineDiscovery) bool {
	labels := cr.GetLabels()
	if labels[v1alpha1.PipelineDiscoveryNameLabel] != pd.Name {
		return false
	}
	if ownerNS, ok := labels[v1alpha1.PipelineDiscoveryNamespaceLabel]; ok {
		return ownerNS == pd.Namespace
	}
	return cr.GetAnnotations()[v1alpha1.PipelineDiscoveredByAnnotation] == pipelineDiscoveryOwnerAnnotation(pd)
}

// setPipelineStaleAnnotation marks a Pipeline CR as stale. No-op if already set.
func (r *PipelineDiscoveryReconciler) setPipelineStaleAnnotation(ctx context.Context, cr *v1alpha1.Pipeline) error {
	if cr.Annotations[v1alpha1.PipelineDiscoveryStaleAnnotation] == v1alpha1.PipelineDiscoveryStaleAnnotationValue {
		return nil
	}
	patch := client.MergeFrom(cr.DeepCopy())
	if cr.Annotations == nil {
		cr.Annotations = make(map[string]string)
	}
	cr.Annotations[v1alpha1.PipelineDiscoveryStaleAnnotation] = v1alpha1.PipelineDiscoveryStaleAnnotationValue
	if err := r.Patch(ctx, cr, patch); err != nil {
		return fmt.Errorf("set stale annotation on %s/%s: %w", cr.Namespace, cr.Name, err)
	}
	return nil
}

// clearPipelineStaleAnnotation removes the stale annotation when the pipeline
// reappears. No-op if not present.
func (r *PipelineDiscoveryReconciler) clearPipelineStaleAnnotation(ctx context.Context, cr *v1alpha1.Pipeline) error {
	if _, ok := cr.Annotations[v1alpha1.PipelineDiscoveryStaleAnnotation]; !ok {
		return nil
	}
	patch := client.MergeFrom(cr.DeepCopy())
	delete(cr.Annotations, v1alpha1.PipelineDiscoveryStaleAnnotation)
	if err := r.Patch(ctx, cr, patch); err != nil {
		return fmt.Errorf("clear stale annotation on %s/%s: %w", cr.Namespace, cr.Name, err)
	}
	return nil
}

// updateStatusSuccess writes the per-poll status update. Returns
// (wroteUpdate, error).
func (r *PipelineDiscoveryReconciler) updateStatusSuccess(
	ctx context.Context,
	pd *v1alpha1.PipelineDiscovery,
	observed, managed int,
	stale []string,
	conflicts []v1alpha1.PipelineDiscoveryConflict,
	now time.Time,
) (bool, error) {
	nowMeta := metav1.NewTime(now)

	pd.Status.ObservedGeneration = pd.Generation
	pd.Status.LastSyncTime = &nowMeta
	pd.Status.LastSuccessTime = &nowMeta
	pd.Status.PipelinesObserved = int32(observed)
	pd.Status.PipelinesManaged = int32(managed)
	pd.Status.StalePipelines = stale

	cappedConflicts := conflicts
	if len(conflicts) > maxPipelineDiscoveryConflicts {
		cappedConflicts = conflicts[:maxPipelineDiscoveryConflicts]
		r.emitEventf(pd, corev1.EventTypeWarning, "TruncatedConflicts",
			"discovery has %d conflicts; only first %d kept in status", len(conflicts), maxPipelineDiscoveryConflicts)
		meta.SetStatusCondition(&pd.Status.Conditions, metav1.Condition{
			Type:               pipelineDiscoveryConditionTruncatedConflicts,
			Status:             metav1.ConditionTrue,
			Reason:             pipelineDiscoveryReasonConflictsTruncated,
			Message:            fmt.Sprintf("conflicts list truncated to %d entries; check events for the full list", maxPipelineDiscoveryConflicts),
			ObservedGeneration: pd.Generation,
		})
	} else {
		meta.SetStatusCondition(&pd.Status.Conditions, metav1.Condition{
			Type:               pipelineDiscoveryConditionTruncatedConflicts,
			Status:             metav1.ConditionFalse,
			Reason:             pipelineDiscoveryReasonNoConflictsTruncated,
			ObservedGeneration: pd.Generation,
		})
	}
	pd.Status.Conflicts = cappedConflicts

	message := fmt.Sprintf("Observed %d, managed %d, stale %d, conflicts %d", observed, managed, len(stale), len(conflicts))
	setReadyCondition(&pd.Status.Conditions, pd.Generation, true, pipelineDiscoveryReasonSynced, message)
	setSyncedCondition(&pd.Status.Conditions, pd.Generation, true, pipelineDiscoveryReasonSynced, message)

	if err := r.Status().Update(ctx, pd); err != nil {
		if apierrors.IsConflict(err) {
			return false, nil
		}
		return false, fmt.Errorf("update status: %w", err)
	}
	return true, nil
}

// updateStatusError writes a failure condition and returns the status update
// error (if any). The original error is returned to the caller so
// controller-runtime can back off.
func (r *PipelineDiscoveryReconciler) updateStatusError(
	ctx context.Context,
	pd *v1alpha1.PipelineDiscovery,
	reason string,
	originalErr error,
) (ctrl.Result, error) {
	now := r.now()
	nowMeta := metav1.NewTime(now)
	pd.Status.LastSyncTime = &nowMeta
	pd.Status.ObservedGeneration = pd.Generation
	message := fmt.Sprintf("%s: %v", reason, originalErr)
	setReadyCondition(&pd.Status.Conditions, pd.Generation, false, reason, message)
	setSyncedCondition(&pd.Status.Conditions, pd.Generation, false, reason, message)

	if updateErr := r.Status().Update(ctx, pd); updateErr != nil {
		if apierrors.IsConflict(updateErr) {
			return ctrl.Result{Requeue: true}, nil
		}
		logf.FromContext(ctx).Error(updateErr, "failed to update status after error",
			"namespace", pd.Namespace, "name", pd.Name, "reason", reason)
	}
	return ctrl.Result{}, originalErr
}

// choosePipelineCRName picks the metadata.name for a discovered Pipeline CR.
// Lossy sanitizations always go through HashedName; lossless ones use the
// sanitized form directly.
func choosePipelineCRName(name string) string {
	sanitized, lossy := discovery.SanitizedName(name)
	if !lossy {
		return sanitized
	}
	return discovery.HashedName(name)
}

// SetupWithManager wires the reconciler. Discovery is purely poll-driven via
// RequeueAfter; no cross-resource watches are needed.
func (r *PipelineDiscoveryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	maxConcurrent := r.MaxConcurrentReconciles
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.PipelineDiscovery{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Named("pipelinediscovery").
		Complete(r)
}
