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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
	"github.com/grafana/fleet-management-operator/internal/controller/attributes"
)

const (
	// Policy condition reasons. The Ready/Synced condition types are shared
	// with the Pipeline and Collector controllers (see pipeline_controller.go).
	policyReasonMatched    = "Matched"
	policyReasonNoMatch    = "NoMatch"
	policyReasonListFailed = "ListFailed"

	// Event reasons emitted by the Policy controller.
	policyEventReasonSynced     = "Synced"
	policyEventReasonNoMatch    = "NoMatch"
	policyEventReasonListFailed = "ListFailed"

	// Truncated condition type and reasons for RemoteAttributePolicy.
	// Set when status.matchedCollectorIDs is capped at maxMatchedIDs;
	// status.matchedCount always reflects the full count.
	policyConditionTypeTruncated      = "Truncated"
	policyConditionReasonTruncated    = "MatchedIDsTruncated"
	policyConditionReasonNotTruncated = "NotTruncated"

	// maxMatchedIDs is the cap on status.matchedCollectorIDs. At 30k
	// collectors, storing all IDs would approach ~860 KB per CR in etcd;
	// status.matchedCount provides the full count without the etcd bloat.
	maxMatchedIDs = 1000

	// policyMatcherKeyIndex is an IndexField on RemoteAttributePolicy indexed
	// by the label key names referenced in spec.selector.matchers. It lets the
	// Collector watch handler look up only the Policies whose matchers mention
	// a key present in the changed Collector, avoiding a full namespace List.
	policyMatcherKeyIndex = ".spec.selector.matcherKeys"
)

// RemoteAttributePolicyReconciler reconciles a RemoteAttributePolicy object.
//
// This controller does NOT call the Fleet Management API. Its sole
// responsibilities are:
//
//  1. Maintain status.matchedCollectorIDs by evaluating the Policy selector
//     against every Collector CR in the same namespace.
//  2. Trigger the Collector controller when the set of matched collectors
//     could change (Policy spec changes are handled via For(); Collector
//     additions/changes are handled via the Watches() wired in
//     SetupWithManager).
//
// External attribute writes happen from the Collector controller, which
// reads live Policy state on each reconcile. That keeps Phase 2 simple — no
// finalizer is needed here because there is nothing external to clean up.
//
// MaxConcurrentReconciles > 1 is safe for this controller: reconciles are
// pure K8s cache reads with no external API calls. Configurable via
// --controller-policy-max-concurrent (default 4).
type RemoteAttributePolicyReconciler struct {
	client.Client
	Scheme                  *runtime.Scheme
	Recorder                record.EventRecorder
	MaxConcurrentReconciles int
}

var _ reconcile.Reconciler = &RemoteAttributePolicyReconciler{}

func (r *RemoteAttributePolicyReconciler) emitEvent(object runtime.Object, eventtype, reason, message string) {
	if r.Recorder != nil {
		r.Recorder.Event(object, eventtype, reason, message)
	}
}

func (r *RemoteAttributePolicyReconciler) emitEventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
	if r.Recorder != nil {
		r.Recorder.Eventf(object, eventtype, reason, messageFmt, args...)
	}
}

// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=remoteattributepolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=remoteattributepolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=collectors,verbs=get;list;watch

// Reconcile evaluates the Policy selector against the Collectors in the same
// namespace and records the matched IDs in status. See the package-level
// notes on this controller for why no Fleet Management calls happen here.
func (r *RemoteAttributePolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("reconciling RemoteAttributePolicy", "namespace", req.Namespace, "name", req.Name)

	policy := &fleetmanagementv1alpha1.RemoteAttributePolicy{}
	if err := r.Get(ctx, req.NamespacedName, policy); err != nil {
		if apierrors.IsNotFound(err) {
			// No finalizer is registered, so a hard delete is fine — Phase 2
			// has no external state to reclaim. Collectors that previously
			// inherited keys from this Policy will lose them on their next
			// reconcile because they read live Policy state each time.
			log.Info("RemoteAttributePolicy not found, likely deleted",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		log.Error(err, "failed to get RemoteAttributePolicy",
			"namespace", req.Namespace, "name", req.Name)
		return ctrl.Result{}, err
	}

	// We always re-evaluate the selector because reconciliation is driven
	// by both Policy spec changes (For()) and Collector changes (Watches()).
	// The latter does not bump the Policy's generation, so a strict
	// ObservedGeneration short-circuit would prevent the controller from
	// noticing a newly-matching Collector. Instead we compute the desired
	// matched-set and only skip the status write when:
	//
	//   - The Policy spec is up-to-date (ObservedGeneration == Generation), AND
	//   - The matched-set has not changed since the last reconcile.
	//
	// This preserves the no-op semantics required by the spec
	// (a follow-up reconcile of an unchanged Policy with no Collector
	// changes is free) while keeping watch-driven re-evaluation correct.
	collectors := &fleetmanagementv1alpha1.CollectorList{}
	if err := r.List(ctx, collectors, client.InNamespace(policy.Namespace)); err != nil {
		log.Error(err, "failed to list Collectors",
			"namespace", policy.Namespace, "name", policy.Name)
		r.emitEventf(policy, corev1.EventTypeWarning, policyEventReasonListFailed,
			"Failed to list Collectors in namespace %q: %v", policy.Namespace, err)
		return r.updateStatusError(ctx, policy, policyReasonListFailed, err)
	}

	matchedIDs := evaluatePolicySelector(policy, collectors.Items)

	// No-op check: skip the status write when spec and matched-set are
	// unchanged. MatchedCount is the full count; MatchedCollectorIDs stores
	// the capped sample, so compare by count first (cheap), then by IDs
	// only when the counts match to detect reordering within the cap.
	cappedLen := len(matchedIDs)
	if cappedLen > maxMatchedIDs {
		cappedLen = maxMatchedIDs
	}
	if policy.Status.ObservedGeneration == policy.Generation &&
		policy.Status.MatchedCount == int32(len(matchedIDs)) &&
		len(policy.Status.MatchedCollectorIDs) == cappedLen &&
		stringSlicesEqual(policy.Status.MatchedCollectorIDs, matchedIDs[:cappedLen]) {
		log.V(1).Info("policy already reconciled, skipping",
			"namespace", policy.Namespace, "name", policy.Name,
			"generation", policy.Generation, "matched", len(matchedIDs))
		return ctrl.Result{}, nil
	}

	return r.updateStatusMatched(ctx, policy, matchedIDs)
}

// evaluatePolicySelector returns the sorted, deduplicated list of Collector
// IDs that satisfy the Policy's selector. Each Collector's evaluation
// context is the union of its observed local attributes and the synthetic
// `collector.id` key bound to spec.id.
func evaluatePolicySelector(
	policy *fleetmanagementv1alpha1.RemoteAttributePolicy,
	collectors []fleetmanagementv1alpha1.Collector,
) []string {
	selector := attributes.Selector{
		Matchers:     policy.Spec.Selector.Matchers,
		CollectorIDs: policy.Spec.Selector.CollectorIDs,
	}

	seen := make(map[string]struct{}, len(collectors))
	matched := make([]string, 0, len(collectors))
	for i := range collectors {
		c := &collectors[i]
		attrs := buildSelectorAttrs(c)
		if !selector.Match(c.Spec.ID, attrs) {
			continue
		}
		if _, dup := seen[c.Spec.ID]; dup {
			continue
		}
		seen[c.Spec.ID] = struct{}{}
		matched = append(matched, c.Spec.ID)
	}
	sort.Strings(matched)
	return matched
}

// buildSelectorAttrs is the per-Collector attribute view exposed to the
// matcher engine: status.localAttributes (what the collector reports about
// itself) plus a synthetic `collector.id` so matchers can pin specific IDs
// without using the explicit CollectorIDs list.
func buildSelectorAttrs(c *fleetmanagementv1alpha1.Collector) map[string]string {
	attrs := make(map[string]string, len(c.Status.LocalAttributes)+1)
	for k, v := range c.Status.LocalAttributes {
		attrs[k] = v
	}
	attrs["collector.id"] = c.Spec.ID
	return attrs
}

// updateStatusMatched records the matched-collector set and the Ready /
// Synced conditions. Ready=False with reason NoMatch when nothing matched —
// that is the primary signal users see when a selector is broken or the
// targeted collectors haven't come up yet.
func (r *RemoteAttributePolicyReconciler) updateStatusMatched(
	ctx context.Context,
	policy *fleetmanagementv1alpha1.RemoteAttributePolicy,
	matchedIDs []string,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	policy.Status.ObservedGeneration = policy.Generation
	policy.Status.MatchedCount = int32(len(matchedIDs))

	capped := matchedIDs
	if len(matchedIDs) > maxMatchedIDs {
		capped = matchedIDs[:maxMatchedIDs]
		r.emitEventf(policy, corev1.EventTypeWarning, "Truncated",
			"matchedCollectorIDs capped at %d; matchedCount=%d reflects the full count",
			maxMatchedIDs, len(matchedIDs))
		meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
			Type:               policyConditionTypeTruncated,
			Status:             metav1.ConditionTrue,
			Reason:             policyConditionReasonTruncated,
			Message:            fmt.Sprintf("matchedCollectorIDs truncated to %d entries; full count in status.matchedCount", maxMatchedIDs),
			ObservedGeneration: policy.Generation,
		})
	} else {
		meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
			Type:               policyConditionTypeTruncated,
			Status:             metav1.ConditionFalse,
			Reason:             policyConditionReasonNotTruncated,
			ObservedGeneration: policy.Generation,
		})
	}
	policy.Status.MatchedCollectorIDs = capped

	if len(matchedIDs) > 0 {
		message := fmt.Sprintf("Policy matches %d collector(s)", len(matchedIDs))
		meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
			Type:               conditionTypeReady,
			Status:             metav1.ConditionTrue,
			Reason:             policyReasonMatched,
			Message:            message,
			ObservedGeneration: policy.Generation,
		})
		meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
			Type:               conditionTypeSynced,
			Status:             metav1.ConditionTrue,
			Reason:             policyReasonMatched,
			Message:            message,
			ObservedGeneration: policy.Generation,
		})
	} else {
		message := "Policy selector did not match any Collector in this namespace"
		meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
			Type:               conditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             policyReasonNoMatch,
			Message:            message,
			ObservedGeneration: policy.Generation,
		})
		meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
			Type:               conditionTypeSynced,
			Status:             metav1.ConditionTrue,
			Reason:             policyReasonNoMatch,
			Message:            "Selector evaluated successfully (no collectors matched)",
			ObservedGeneration: policy.Generation,
		})
	}

	if err := r.Status().Update(ctx, policy); err != nil {
		if apierrors.IsConflict(err) {
			log.V(1).Info("status update conflict, requeueing",
				"namespace", policy.Namespace, "name", policy.Name)
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "failed to update status",
			"namespace", policy.Namespace, "name", policy.Name)
		return ctrl.Result{}, err
	}

	if len(matchedIDs) > 0 {
		r.emitEventf(policy, corev1.EventTypeNormal, policyEventReasonSynced,
			"Policy matched %d collector(s)", len(matchedIDs))
		fleetResourceSyncedTotal.WithLabelValues("RemoteAttributePolicy", policyReasonMatched).Inc()
	} else {
		r.emitEvent(policy, corev1.EventTypeWarning, policyEventReasonNoMatch,
			"Policy selector did not match any Collector in this namespace")
		fleetResourceSyncedTotal.WithLabelValues("RemoteAttributePolicy", policyReasonNoMatch).Inc()
	}

	// Event coverage: Synced, NoMatch, ListFailed, Truncated.
	// Created/Deleted events are intentionally absent: RemoteAttributePolicy has no Fleet-side resource.

	log.Info("successfully reconciled policy",
		"namespace", policy.Namespace, "name", policy.Name,
		"generation", policy.Generation, "matched", len(matchedIDs))
	return ctrl.Result{}, nil
}

// updateStatusError records a list-or-status failure on the Policy status
// and preserves the original error so controller-runtime can drive
// exponential backoff.
func (r *RemoteAttributePolicyReconciler) updateStatusError(
	ctx context.Context,
	policy *fleetmanagementv1alpha1.RemoteAttributePolicy,
	reason string,
	originalErr error,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	policy.Status.ObservedGeneration = policy.Generation
	message := fmt.Sprintf("%s: %v", reason, originalErr)

	meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
		Type:               conditionTypeReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: policy.Generation,
	})
	meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
		Type:               conditionTypeSynced,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: policy.Generation,
	})

	if updateErr := r.Status().Update(ctx, policy); updateErr != nil {
		if apierrors.IsConflict(updateErr) {
			log.V(1).Info("status update conflict during error handling, requeueing",
				"namespace", policy.Namespace, "name", policy.Name)
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(updateErr, "failed to update status after reconciliation error",
			"namespace", policy.Namespace, "name", policy.Name,
			"originalError", originalErr.Error(), "reason", reason)
	}

	fleetResourceSyncedTotal.WithLabelValues("RemoteAttributePolicy", reason).Inc()
	return ctrl.Result{}, originalErr
}

// SetupWithManager wires the watches for this reconciler. The Policy
// controller watches:
//
//  1. RemoteAttributePolicy itself (via For()) — reconcile on spec changes.
//  2. Collector — when collectors come and go, the set of matched IDs may
//     shift. The handler uses a policyMatcherKeyIndex to enqueue only the
//     Policies whose matchers reference keys present in the Collector's
//     attributes, rather than all Policies in the namespace.
func (r *RemoteAttributePolicyReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx,
		&fleetmanagementv1alpha1.RemoteAttributePolicy{},
		policyMatcherKeyIndex,
		func(o client.Object) []string {
			p := o.(*fleetmanagementv1alpha1.RemoteAttributePolicy)
			return attributes.MatcherKeys(p.Spec.Selector.Matchers)
		},
	); err != nil {
		return fmt.Errorf("indexing RemoteAttributePolicy matcher keys: %w", err)
	}

	maxConcurrent := r.MaxConcurrentReconciles
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&fleetmanagementv1alpha1.RemoteAttributePolicy{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Watches(&fleetmanagementv1alpha1.Collector{},
			handler.EnqueueRequestsFromMapFunc(r.mapCollectorToAffectedPolicies)).
		Named("remoteattributepolicy").
		Complete(r)
}

// stringSlicesEqual reports whether two string slices contain the same
// elements in the same order. Used to detect a no-op reconcile so we can
// skip the status write.
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// mapCollectorToAffectedPolicies returns reconcile requests for only the
// RemoteAttributePolicies whose matchers reference at least one key present
// in the changed Collector's attributes. This avoids the O(collectors *
// policies) fan-out from the naive "enqueue every policy" approach.
//
// When the Collector has no spec.remoteAttributes (e.g. a freshly-created CR)
// we fall back to the broad namespace-wide enqueue so that new Collectors are
// still evaluated by all Policies, including those that match via
// CollectorIDs or the synthetic collector.id key.
func (r *RemoteAttributePolicyReconciler) mapCollectorToAffectedPolicies(
	ctx context.Context, obj client.Object,
) []reconcile.Request {
	log := logf.FromContext(ctx)

	collector, ok := obj.(*fleetmanagementv1alpha1.Collector)
	if !ok {
		log.V(1).Info("ignoring non-Collector event in mapCollectorToAffectedPolicies",
			"objectKind", fmt.Sprintf("%T", obj))
		return nil
	}

	// Build the set of attribute keys the Collector carries. We look at both
	// spec.remoteAttributes and status.localAttributes: a Collector that was
	// just created may have only status, while one managed by the operator may
	// have spec attributes as well.
	collectorKeys := make(map[string]struct{})
	for k := range collector.Spec.RemoteAttributes {
		collectorKeys[k] = struct{}{}
	}
	for k := range collector.Status.LocalAttributes {
		collectorKeys[k] = struct{}{}
	}
	// Always include the synthetic collector.id key so Policies using it are
	// found via the index.
	collectorKeys["collector.id"] = struct{}{}

	// No attributes at all: fall back to broad enqueue. This covers brand-new
	// Collectors before any attributes have been populated.
	if len(collectorKeys) == 0 {
		return r.allPoliciesInNamespace(ctx, collector.Namespace)
	}

	seen := map[types.NamespacedName]struct{}{}
	var reqs []reconcile.Request
	for key := range collectorKeys {
		var list fleetmanagementv1alpha1.RemoteAttributePolicyList
		if err := r.List(ctx, &list,
			client.InNamespace(collector.Namespace),
			client.MatchingFields{policyMatcherKeyIndex: key},
		); err != nil {
			log.Error(err, "failed to list RemoteAttributePolicies by index for Collector watch",
				"namespace", collector.Namespace, "collector", collector.Name, "key", key)
			continue
		}
		for i := range list.Items {
			nn := types.NamespacedName{
				Namespace: list.Items[i].Namespace,
				Name:      list.Items[i].Name,
			}
			if _, dup := seen[nn]; !dup {
				seen[nn] = struct{}{}
				reqs = append(reqs, reconcile.Request{NamespacedName: nn})
			}
		}
	}
	return reqs
}

// allPoliciesInNamespace returns reconcile requests for every
// RemoteAttributePolicy in the given namespace. Used as a fallback when a
// Collector has no attributes to key on.
func (r *RemoteAttributePolicyReconciler) allPoliciesInNamespace(
	ctx context.Context, ns string,
) []reconcile.Request {
	log := logf.FromContext(ctx)

	var list fleetmanagementv1alpha1.RemoteAttributePolicyList
	if err := r.List(ctx, &list, client.InNamespace(ns)); err != nil {
		log.Error(err, "failed to list RemoteAttributePolicies for Collector watch fan-out (fallback)",
			"namespace", ns)
		return nil
	}
	reqs := make([]reconcile.Request, len(list.Items))
	for i := range list.Items {
		reqs[i] = reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: list.Items[i].Namespace,
			Name:      list.Items[i].Name,
		}}
	}
	return reqs
}
