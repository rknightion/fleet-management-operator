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

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
	"github.com/grafana/fleet-management-operator/internal/controller/discovery"
	"github.com/grafana/fleet-management-operator/pkg/fleetclient"
)

const (
	discoveryReasonSynced               = "Synced"
	discoveryReasonListCollectorsFailed = "ListCollectorsFailed"
	discoveryReasonUpsertFailed         = "UpsertFailed"
	discoveryReasonInvalidConfig        = "InvalidConfig"

	discoveryEventReasonDiscovered = "Discovered"
	discoveryEventReasonPruned     = "Pruned"
	discoveryEventReasonConflict   = "Conflict"
	discoveryEventReasonSynced     = "Synced"
	discoveryEventReasonFailed     = "Failed"

	// TruncatedConflicts condition type and reasons for CollectorDiscovery.
	// Conflicts are diagnostic-only; no controller reads this slice to drive
	// behaviour, so capping at 100 is safe.
	discoveryConditionTruncatedConflicts = "TruncatedConflicts"
	discoveryReasonConflictsTruncated    = "ConflictsTruncated"
	discoveryReasonNoConflictsTruncated  = "NoConflictsTruncated"

	// maxDiscoveryConflicts caps status.conflicts. Events carry the full
	// conflict list; the status cap bounds etcd write size.
	maxDiscoveryConflicts = 100

	// defaultDiscoveryRequeueOnError is the requeue delay for transient
	// failures (Fleet API errors, k8s API errors). Intentionally faster
	// than the steady-state poll cadence so a misconfigured controller
	// surfaces quickly without slamming Fleet.
	defaultDiscoveryRequeueOnError = 30 * time.Second

	// defaultDiscoveryPollInterval mirrors the schema default. Used as a
	// fallback if the schema default ever fails to apply (e.g., very
	// old clients) — should never trigger in practice.
	defaultDiscoveryPollInterval = 5 * time.Minute
)

// FleetDiscoveryClient is the controller-side abstraction over the Fleet
// Management collector list endpoint. Defined here per the project's
// interface-on-the-consumer-side convention so tests can mock it
// independently of the existing FleetCollectorClient (whose interface
// shape is purposely narrower — Get + BulkUpdate).
type FleetDiscoveryClient interface {
	ListCollectors(ctx context.Context, matchers []string) ([]*fleetclient.Collector, error)
}

// CollectorDiscoveryReconciler reconciles a CollectorDiscovery object.
//
// MaxConcurrentReconciles should stay at 1 (the default). Discovery is
// poll-driven: concurrency > 1 would trigger multiple ListCollectors calls
// per poll cycle without benefit, consuming unnecessary Fleet API budget.
// Configurable via --controller-discovery-max-concurrent.
//
// On each reconcile (poll cadence or spec change) the controller:
//
//  1. Reads spec.pollInterval and skips if not yet due.
//  2. Calls ListCollectors with spec.selector.matchers.
//  3. Filters out inactive collectors (unless includeInactive=true) and
//     intersects with spec.selector.collectorIDs (when non-empty).
//  4. Upsert phase: for each surviving collector, computes a DNS-1123
//     name (sanitize-with-hash-fallback) and ensures a Collector CR
//     exists in the target namespace, labelled and annotated to mark it
//     as managed by this CollectorDiscovery. Existing CRs owned by
//     another discovery or by a manual user are left alone and surfaced
//     in status.conflicts.
//  5. Stale phase: for every label-matched CR whose spec.id is no
//     longer in the Fleet result, applies the policy.onCollectorRemoved
//     decision (Keep marks the CR with a stale annotation; Delete
//     removes it). Discovery is the SOLE writer to its CRs' lifecycles
//     — it never modifies spec.remoteAttributes or other user-managed
//     spec fields.
//  6. Updates status (counts, lastSyncTime, conflicts, conditions) and
//     requeues at pollInterval.
//
// This controller does NOT call BulkUpdateCollectors. The single-writer
// principle (Collector reconciler is the sole writer of remote
// attributes) is preserved.
type CollectorDiscoveryReconciler struct {
	client.Client
	Scheme                  *runtime.Scheme
	FleetClient             FleetDiscoveryClient
	Recorder                events.EventRecorder
	MaxConcurrentReconciles int

	// Now is overridable in tests. Defaults to time.Now.
	Now func() time.Time
}

var _ reconcile.Reconciler = &CollectorDiscoveryReconciler{}

func (r *CollectorDiscoveryReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *CollectorDiscoveryReconciler) emitEventf(object runtime.Object, eventtype, reason, messageFmt string, args ...any) {
	if r.Recorder != nil {
		r.Recorder.Eventf(object, nil, eventtype, reason, reason, messageFmt, args...)
	}
}

// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=collectordiscoveries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=collectordiscoveries/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=collectors,verbs=get;list;watch;create;update;patch;delete

// Reconcile lists collectors from Fleet and reconciles the matching set
// of Collector CRs in the target namespace.
func (r *CollectorDiscoveryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("reconciling CollectorDiscovery", "namespace", req.Namespace, "name", req.Name)

	// B5: outcome is set at every return path; the deferred increment fires
	// once per Reconcile call so PromQL rate(...) reflects every exit path
	// (NotFound, Deleted-spec, ScheduleSkipped, Synced, NoOp, ListFailed,
	// UpsertFailed, InvalidConfig) instead of only the success / error
	// status-update paths.
	var outcome string
	defer func() {
		if outcome != "" {
			fleetResourceSyncedTotal.WithLabelValues("CollectorDiscovery", outcome).Inc()
		}
	}()

	cd := &fleetmanagementv1alpha1.CollectorDiscovery{}
	if err := r.Get(ctx, req.NamespacedName, cd); err != nil {
		if apierrors.IsNotFound(err) {
			// No finalizer; orphan-on-delete is the design. Nothing to do.
			// D5: drop the per-CR gauge series so deleted CRs do not leave
			// stale labels behind in /metrics output.
			fleetDiscoveryListSize.DeleteLabelValues(req.Namespace, req.Name)
			outcome = outcomeNotFound
			return ctrl.Result{}, nil
		}
		outcome = discoveryReasonListCollectorsFailed
		return ctrl.Result{}, err
	}

	// Skip if being deleted. There is no finalizer; deletion proceeds
	// without any teardown action — discovered Collector CRs are
	// orphaned (annotations/labels remain, but no controller acts on
	// them).
	if !cd.DeletionTimestamp.IsZero() {
		outcome = outcomeDeleted
		return ctrl.Result{}, nil
	}

	pollInterval, err := r.parsePollInterval(cd)
	if err != nil {
		log.Error(err, "invalid pollInterval", "namespace", cd.Namespace, "name", cd.Name)
		r.emitEventf(cd, corev1.EventTypeWarning, discoveryEventReasonFailed,
			"Invalid pollInterval %q: %v", cd.Spec.PollInterval, err)
		var innerOutcome string
		result, err := r.updateStatusError(ctx, cd, discoveryReasonInvalidConfig, err, &innerOutcome)
		outcome = innerOutcome
		return result, err
	}

	now := r.now()

	// Schedule check: skip if a recent successful sync still leaves
	// pollInterval unexpired. Spec changes bump generation and bypass
	// this gate by clearing observedGeneration.
	if cd.Status.LastSyncTime != nil && cd.Status.ObservedGeneration == cd.Generation {
		nextDue := cd.Status.LastSyncTime.Add(pollInterval)
		if now.Before(nextDue) {
			outcome = outcomeScheduleSkipped
			return ctrl.Result{RequeueAfter: nextDue.Sub(now)}, nil
		}
	}

	targetNS := strings.TrimSpace(cd.Spec.TargetNamespace)
	if targetNS == "" {
		targetNS = cd.Namespace
	}

	collectors, err := r.FleetClient.ListCollectors(ctx, cd.Spec.Selector.Matchers)
	if err != nil {
		log.Error(err, "ListCollectors failed",
			"namespace", cd.Namespace, "name", cd.Name)
		r.emitEventf(cd, corev1.EventTypeWarning, discoveryEventReasonFailed,
			"ListCollectors failed: %v", err)
		var innerOutcome string
		if _, statusErr := r.updateStatusError(ctx, cd, discoveryReasonListCollectorsFailed, err, &innerOutcome); statusErr != nil {
			outcome = innerOutcome
			return ctrl.Result{}, statusErr
		}
		outcome = innerOutcome
		return ctrl.Result{RequeueAfter: defaultDiscoveryRequeueOnError}, nil
	}

	// OBS-05: record ListCollectors result size for this CR
	fleetDiscoveryListSize.WithLabelValues(cd.Namespace, cd.Name).Set(float64(len(collectors)))

	surviving := r.filterCollectors(cd, collectors)

	conflicts, currentIDs, err := r.upsertCollectorCRs(ctx, cd, surviving, targetNS)
	if err != nil {
		log.Error(err, "upsert phase failed", "namespace", cd.Namespace, "name", cd.Name)
		r.emitEventf(cd, corev1.EventTypeWarning, discoveryEventReasonFailed,
			"Upsert phase failed: %v", err)
		var innerOutcome string
		if _, statusErr := r.updateStatusError(ctx, cd, discoveryReasonUpsertFailed, err, &innerOutcome); statusErr != nil {
			outcome = innerOutcome
			return ctrl.Result{}, statusErr
		}
		outcome = innerOutcome
		return ctrl.Result{RequeueAfter: defaultDiscoveryRequeueOnError}, nil
	}

	stale, managed, err := r.processStale(ctx, cd, currentIDs, targetNS)
	if err != nil {
		log.Error(err, "stale phase failed", "namespace", cd.Namespace, "name", cd.Name)
		r.emitEventf(cd, corev1.EventTypeWarning, discoveryEventReasonFailed,
			"Stale phase failed: %v", err)
		var innerOutcome string
		if _, statusErr := r.updateStatusError(ctx, cd, discoveryReasonUpsertFailed, err, &innerOutcome); statusErr != nil {
			outcome = innerOutcome
			return ctrl.Result{}, statusErr
		}
		outcome = innerOutcome
		return ctrl.Result{RequeueAfter: defaultDiscoveryRequeueOnError}, nil
	}

	for _, conflict := range conflicts {
		r.emitEventf(cd, corev1.EventTypeWarning, discoveryEventReasonConflict,
			"Could not claim Collector %s/%s for collector %q: %s",
			targetNS, conflict.CRName, conflict.CollectorID, conflict.Reason)
	}

	updated, err := r.updateStatusSuccess(ctx, cd, len(surviving), managed, stale, conflicts, now)
	if err != nil {
		outcome = discoveryReasonUpsertFailed
		return ctrl.Result{}, err
	}
	if updated {
		r.emitEventf(cd, corev1.EventTypeNormal, discoveryEventReasonSynced,
			"Discovered %d collector(s); managing %d, %d stale, %d conflict(s)",
			len(surviving), managed, len(stale), len(conflicts))
	}
	// B5: increment Synced for every successful poll, not only when
	// updated==true. A steady-state CR whose status didn't change is still
	// a successful reconcile and PromQL rate alarms should reflect it.
	outcome = discoveryReasonSynced

	return ctrl.Result{RequeueAfter: pollInterval}, nil
}

// parsePollInterval reads spec.pollInterval, applies the schema default
// for empty values, and rejects values below the webhook-enforced
// minimum (defense-in-depth).
func (r *CollectorDiscoveryReconciler) parsePollInterval(cd *fleetmanagementv1alpha1.CollectorDiscovery) (time.Duration, error) {
	raw := strings.TrimSpace(cd.Spec.PollInterval)
	if raw == "" {
		return defaultDiscoveryPollInterval, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("pollInterval %q is not a valid Go duration: %w", raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("pollInterval %q (%s) must be positive", raw, d)
	}
	// The 1-minute minimum is enforced by the webhook; intentionally not
	// re-applied here so tests can use sub-minute intervals when the
	// webhook is bypassed (envtest).
	return d, nil
}

// filterCollectors applies the include-inactive flag and the optional
// collectorIDs intersection.
func (r *CollectorDiscoveryReconciler) filterCollectors(
	cd *fleetmanagementv1alpha1.CollectorDiscovery,
	collectors []*fleetclient.Collector,
) []*fleetclient.Collector {
	out := make([]*fleetclient.Collector, 0, len(collectors))

	var allowed map[string]struct{}
	if len(cd.Spec.Selector.CollectorIDs) > 0 {
		allowed = make(map[string]struct{}, len(cd.Spec.Selector.CollectorIDs))
		for _, id := range cd.Spec.Selector.CollectorIDs {
			allowed[id] = struct{}{}
		}
	}

	for _, c := range collectors {
		if c == nil || strings.TrimSpace(c.ID) == "" {
			continue
		}
		if !cd.Spec.IncludeInactive && c.MarkedInactiveAt != nil {
			continue
		}
		if allowed != nil {
			if _, ok := allowed[c.ID]; !ok {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// upsertCollectorCRs creates a Collector CR for each surviving Fleet
// record and records conflicts where a same-named CR exists but is not
// owned by this CollectorDiscovery. Returns the conflict list and the
// set of collector IDs that this discovery now claims (for the stale
// phase).
func (r *CollectorDiscoveryReconciler) upsertCollectorCRs(
	ctx context.Context,
	cd *fleetmanagementv1alpha1.CollectorDiscovery,
	surviving []*fleetclient.Collector,
	targetNS string,
) ([]fleetmanagementv1alpha1.DiscoveryConflict, map[string]struct{}, error) {
	conflicts := make([]fleetmanagementv1alpha1.DiscoveryConflict, 0)
	currentIDs := make(map[string]struct{}, len(surviving))

	for _, c := range surviving {
		name := chooseCRName(c.ID)
		if !discovery.IsValidDNS1123(name) {
			conflicts = append(conflicts, fleetmanagementv1alpha1.DiscoveryConflict{
				CollectorID: c.ID,
				CRName:      name,
				Reason:      fleetmanagementv1alpha1.DiscoveryConflictSanitizeFailed,
			})
			continue
		}

		existing := &fleetmanagementv1alpha1.Collector{}
		key := client.ObjectKey{Namespace: targetNS, Name: name}
		err := r.Get(ctx, key, existing)
		if err != nil && !apierrors.IsNotFound(err) {
			return nil, nil, fmt.Errorf("get collector %s: %w", key, err)
		}

		if apierrors.IsNotFound(err) {
			cr := &fleetmanagementv1alpha1.Collector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: targetNS,
					Labels: map[string]string{
						fleetmanagementv1alpha1.DiscoveryNameLabel:      cd.Name,
						fleetmanagementv1alpha1.DiscoveryNamespaceLabel: cd.Namespace,
					},
					Annotations: map[string]string{
						fleetmanagementv1alpha1.DiscoveredByAnnotation:     collectorDiscoveryOwnerAnnotation(cd),
						fleetmanagementv1alpha1.FleetCollectorIDAnnotation: c.ID,
					},
				},
				Spec: fleetmanagementv1alpha1.CollectorSpec{
					ID: c.ID,
				},
			}
			if createErr := r.Create(ctx, cr); createErr != nil {
				if apierrors.IsAlreadyExists(createErr) {
					// Race: another reconcile (or another writer) won.
					// Treat as success for this collector — the next
					// reconcile will re-evaluate ownership.
					currentIDs[c.ID] = struct{}{}
					continue
				}
				return nil, nil, fmt.Errorf("create collector %s: %w", key, createErr)
			}
			currentIDs[c.ID] = struct{}{}
			r.emitEventf(cd, corev1.EventTypeNormal, discoveryEventReasonDiscovered,
				"Created Collector %s for collector %q", key, c.ID)
			continue
		}

		_, owned := existing.Labels[fleetmanagementv1alpha1.DiscoveryNameLabel]
		switch {
		case !owned:
			// Manually-created CR with the same name. Skip.
			conflicts = append(conflicts, fleetmanagementv1alpha1.DiscoveryConflict{
				CollectorID: c.ID,
				CRName:      name,
				Reason:      fleetmanagementv1alpha1.DiscoveryConflictNotOwned,
			})
		case !collectorIsOwnedByDiscovery(existing, cd):
			// Owned by a different CollectorDiscovery; first-write wins.
			conflicts = append(conflicts, fleetmanagementv1alpha1.DiscoveryConflict{
				CollectorID: c.ID,
				CRName:      name,
				Reason:      fleetmanagementv1alpha1.DiscoveryConflictOwnedByOther,
			})
		case existing.Spec.ID != c.ID:
			// Hash-suffix collision: same name, different IDs both
			// owned by us. Vanishingly rare (1-in-1M). Surface it so
			// the user can pick distinct IDs.
			conflicts = append(conflicts, fleetmanagementv1alpha1.DiscoveryConflict{
				CollectorID: c.ID,
				CRName:      name,
				Reason:      fleetmanagementv1alpha1.DiscoveryConflictSanitizeFailed,
			})
		default:
			// Ours and matches; clear any stale annotation.
			currentIDs[c.ID] = struct{}{}
			if err := r.clearStaleAnnotation(ctx, existing); err != nil {
				return nil, nil, err
			}
		}
	}

	// Sort for deterministic status output.
	sort.Slice(conflicts, func(i, j int) bool {
		return conflicts[i].CollectorID < conflicts[j].CollectorID
	})
	return conflicts, currentIDs, nil
}

// chooseCRName picks the metadata.name for a discovered CR. Lossy
// sanitizations always go through HashedName; lossless ones use the
// sanitized form directly. Empty sanitized base falls through to
// HashedName so we always get a valid DNS-1123 label.
func chooseCRName(id string) string {
	sanitized, lossy := discovery.SanitizedName(id)
	if sanitized == "" || lossy {
		return discovery.HashedName(id)
	}
	return sanitized
}

// processStale handles Collector CRs labelled as managed by this
// CollectorDiscovery whose collector ID is not in the current Fleet
// result. Returns the sorted list of stale collector IDs and the count
// of CRs still managed (after any deletes).
func (r *CollectorDiscoveryReconciler) processStale(
	ctx context.Context,
	cd *fleetmanagementv1alpha1.CollectorDiscovery,
	currentIDs map[string]struct{},
	targetNS string,
) ([]string, int, error) {
	var crs fleetmanagementv1alpha1.CollectorList
	selector := client.MatchingLabels{
		fleetmanagementv1alpha1.DiscoveryNameLabel: cd.Name,
	}
	if err := r.List(ctx, &crs, client.InNamespace(targetNS), selector); err != nil {
		return nil, 0, fmt.Errorf("list managed collectors in %s: %w", targetNS, err)
	}

	managed := 0
	onRemoved := cd.Spec.Policy.OnCollectorRemoved
	if onRemoved == "" {
		onRemoved = fleetmanagementv1alpha1.DiscoveryOnRemovedKeep
	}

	staleIDs := make([]string, 0)
	for i := range crs.Items {
		cr := &crs.Items[i]
		if !collectorIsOwnedByDiscovery(cr, cd) {
			continue
		}
		managed++
		if _, present := currentIDs[cr.Spec.ID]; present {
			// Still in Fleet; clear any stale annotation set by a
			// previous run.
			if err := r.clearStaleAnnotation(ctx, cr); err != nil {
				return nil, 0, err
			}
			continue
		}

		// Vanished from Fleet.
		switch onRemoved {
		case fleetmanagementv1alpha1.DiscoveryOnRemovedDelete:
			if err := r.Delete(ctx, cr); err != nil && !apierrors.IsNotFound(err) {
				return nil, 0, fmt.Errorf("delete stale collector %s/%s: %w", cr.Namespace, cr.Name, err)
			}
			r.emitEventf(cd, corev1.EventTypeNormal, discoveryEventReasonPruned,
				"Deleted Collector %s/%s (collector %q no longer in Fleet)",
				cr.Namespace, cr.Name, cr.Spec.ID)
			managed--
		default: // Keep
			staleIDs = append(staleIDs, cr.Spec.ID)
			if err := r.setStaleAnnotation(ctx, cr); err != nil {
				return nil, 0, err
			}
		}
	}

	sort.Strings(staleIDs)
	return staleIDs, managed, nil
}

func collectorDiscoveryOwnerAnnotation(cd *fleetmanagementv1alpha1.CollectorDiscovery) string {
	return cd.Namespace + "/" + cd.Name
}

func collectorIsOwnedByDiscovery(cr *fleetmanagementv1alpha1.Collector, cd *fleetmanagementv1alpha1.CollectorDiscovery) bool {
	labels := cr.GetLabels()
	if labels[fleetmanagementv1alpha1.DiscoveryNameLabel] != cd.Name {
		return false
	}
	if ownerNS, ok := labels[fleetmanagementv1alpha1.DiscoveryNamespaceLabel]; ok {
		return ownerNS == cd.Namespace
	}
	return cr.GetAnnotations()[fleetmanagementv1alpha1.DiscoveredByAnnotation] == collectorDiscoveryOwnerAnnotation(cd)
}

// setStaleAnnotation marks a Collector CR as stale via a server-side
// merge patch on metadata.annotations. No-op if already set so repeated
// reconciles are quiet.
func (r *CollectorDiscoveryReconciler) setStaleAnnotation(ctx context.Context, cr *fleetmanagementv1alpha1.Collector) error {
	if cr.Annotations[fleetmanagementv1alpha1.DiscoveryStaleAnnotation] == fleetmanagementv1alpha1.DiscoveryStaleAnnotationValue {
		return nil
	}
	patch := client.MergeFrom(cr.DeepCopy())
	if cr.Annotations == nil {
		cr.Annotations = make(map[string]string)
	}
	cr.Annotations[fleetmanagementv1alpha1.DiscoveryStaleAnnotation] = fleetmanagementv1alpha1.DiscoveryStaleAnnotationValue
	if err := r.Patch(ctx, cr, patch); err != nil {
		return fmt.Errorf("set stale annotation on %s/%s: %w", cr.Namespace, cr.Name, err)
	}
	return nil
}

// clearStaleAnnotation removes the stale annotation when the collector
// reappears in Fleet. No-op if the annotation isn't present.
func (r *CollectorDiscoveryReconciler) clearStaleAnnotation(ctx context.Context, cr *fleetmanagementv1alpha1.Collector) error {
	if _, ok := cr.Annotations[fleetmanagementv1alpha1.DiscoveryStaleAnnotation]; !ok {
		return nil
	}
	patch := client.MergeFrom(cr.DeepCopy())
	delete(cr.Annotations, fleetmanagementv1alpha1.DiscoveryStaleAnnotation)
	if err := r.Patch(ctx, cr, patch); err != nil {
		return fmt.Errorf("clear stale annotation on %s/%s: %w", cr.Namespace, cr.Name, err)
	}
	return nil
}

// updateStatusSuccess writes the per-poll status update. Returns
// (wroteUpdate, error). wroteUpdate is false when the new status equals
// the old status (besides timestamps) — callers can suppress events to
// keep steady-state reconciles quiet, though here we only suppress on
// genuine no-op writes.
func (r *CollectorDiscoveryReconciler) updateStatusSuccess(
	ctx context.Context,
	cd *fleetmanagementv1alpha1.CollectorDiscovery,
	observed, managed int,
	stale []string,
	conflicts []fleetmanagementv1alpha1.DiscoveryConflict,
	now time.Time,
) (bool, error) {
	nowMeta := metav1.NewTime(now)

	// OBS-03: record sync age using the previous LastSuccessTime before overwriting it
	if cd.Status.LastSuccessTime != nil && !cd.Status.LastSuccessTime.IsZero() {
		fleetResourceSyncAge.WithLabelValues("CollectorDiscovery").
			Observe(time.Since(cd.Status.LastSuccessTime.Time).Seconds())
	}

	cd.Status.ObservedGeneration = cd.Generation
	cd.Status.LastSyncTime = &nowMeta
	cd.Status.LastSuccessTime = &nowMeta
	cd.Status.CollectorsObserved = int32(observed)
	cd.Status.CollectorsManaged = int32(managed)
	cd.Status.StaleCollectors = stale

	cappedConflicts := conflicts
	if len(conflicts) > maxDiscoveryConflicts {
		cappedConflicts = conflicts[:maxDiscoveryConflicts]
		// E16: emit a Warning event so the truncation message's promise to
		// "check events for the full list" is actually backed by an event
		// (the per-conflict events emitted upstream cover the full list).
		r.emitEventf(cd, corev1.EventTypeWarning, "TruncatedConflicts",
			"discovery has %d conflicts; only first %d kept in status", len(conflicts), maxDiscoveryConflicts)
		meta.SetStatusCondition(&cd.Status.Conditions, metav1.Condition{
			Type:               discoveryConditionTruncatedConflicts,
			Status:             metav1.ConditionTrue,
			Reason:             discoveryReasonConflictsTruncated,
			Message:            fmt.Sprintf("conflicts list truncated to %d entries; check events for the full list", maxDiscoveryConflicts),
			ObservedGeneration: cd.Generation,
		})
	} else {
		meta.SetStatusCondition(&cd.Status.Conditions, metav1.Condition{
			Type:               discoveryConditionTruncatedConflicts,
			Status:             metav1.ConditionFalse,
			Reason:             discoveryReasonNoConflictsTruncated,
			ObservedGeneration: cd.Generation,
		})
	}
	cd.Status.Conflicts = cappedConflicts

	message := fmt.Sprintf("Observed %d, managed %d, stale %d, conflicts %d", observed, managed, len(stale), len(conflicts))
	setReadyCondition(&cd.Status.Conditions, cd.Generation, true, discoveryReasonSynced, message)
	setSyncedCondition(&cd.Status.Conditions, cd.Generation, true, discoveryReasonSynced, message)

	if err := r.Status().Update(ctx, cd); err != nil {
		if apierrors.IsConflict(err) {
			// Re-list will pick up fresh state on the next reconcile.
			return false, nil
		}
		return false, fmt.Errorf("update status: %w", err)
	}
	return true, nil
}

// updateStatusError writes a failure condition and returns the
// original error so controller-runtime backs off appropriately. outcome is
// set to the reason so the deferred Reconcile counter records the precise
// failure mode.
func (r *CollectorDiscoveryReconciler) updateStatusError(
	ctx context.Context,
	cd *fleetmanagementv1alpha1.CollectorDiscovery,
	reason string,
	originalErr error,
	outcome *string,
) (ctrl.Result, error) {
	*outcome = reason

	now := r.now()
	nowMeta := metav1.NewTime(now)
	cd.Status.LastSyncTime = &nowMeta
	cd.Status.ObservedGeneration = cd.Generation
	message := fmt.Sprintf("%s: %v", reason, originalErr)
	setReadyCondition(&cd.Status.Conditions, cd.Generation, false, reason, message)
	setSyncedCondition(&cd.Status.Conditions, cd.Generation, false, reason, message)

	if updateErr := r.Status().Update(ctx, cd); updateErr != nil {
		if apierrors.IsConflict(updateErr) {
			*outcome = outcomeNoOp
			return ctrl.Result{Requeue: true}, nil
		}
		logf.FromContext(ctx).Error(updateErr, "failed to update status after error",
			"namespace", cd.Namespace, "name", cd.Name, "reason", reason)
	}
	// Outcome counter (set above) is incremented by the deferred handler in Reconcile().
	return ctrl.Result{}, originalErr
}

// SetupWithManager wires the reconciler. Discovery is purely
// poll-driven via RequeueAfter; no cross-resource watches are needed.
func (r *CollectorDiscoveryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	maxConcurrent := r.MaxConcurrentReconciles
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleetmanagementv1alpha1.CollectorDiscovery{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Named("collectordiscovery").
		Complete(r)
}
