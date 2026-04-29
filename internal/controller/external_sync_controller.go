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
	"time"

	"github.com/robfig/cron/v3"
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
	"github.com/grafana/fleet-management-operator/pkg/sources"
)

const (
	externalSyncReasonSynced       = "Synced"
	externalSyncReasonSourceFailed = "SourceFailed"
	externalSyncReasonStalled      = "Stalled"
	externalSyncReasonScheduleErr  = "InvalidSchedule"

	externalSyncEventReasonSynced       = "Synced"
	externalSyncEventReasonStalled      = "Stalled"
	externalSyncEventReasonSourceFailed = "SourceFailed"

	externalSyncConditionStalled = "Stalled"

	// Truncated condition type and reasons for ExternalAttributeSync.
	// Set when status.ownedKeys is capped at maxOwnedKeys. Attributes for
	// collectors beyond the cap may not be removed on CR deletion.
	externalSyncConditionTruncated = "Truncated"
	externalSyncReasonTruncated    = "OwnedKeysTruncated"
	externalSyncReasonNotTruncated = "NotTruncated"

	// maxOwnedKeys caps status.ownedKeys. At 30k collectors, an uncapped
	// slice approaches several MB; the Collector reconciler reads this on
	// every reconcile, so the cap bounds both etcd writes and cache reads.
	maxOwnedKeys = 1000

	// defaultExternalSyncRequeueOnError is the requeue delay for transient
	// failures (network, secret resolution); it intentionally backs off
	// faster than the schedule itself so a misconfigured source surfaces
	// quickly without slamming the upstream.
	defaultExternalSyncRequeueOnError = 30 * time.Second

	// externalSyncMatcherKeyIndex is an IndexField on ExternalAttributeSync
	// indexed by the label key names referenced in spec.selector.matchers.
	// It lets the Collector watch handler look up only the syncs whose matchers
	// mention a key present in the changed Collector.
	externalSyncMatcherKeyIndex = ".spec.selector.matcherKeys"
)

// SourceFactory builds a sources.Source from a v1alpha1.ExternalSource spec
// and (optional) Secret. The reconciler holds one factory and dispatches on
// the spec's Kind. Phase 3 ships HTTP only; Phase 4 will register SQL.
type SourceFactory func(spec fleetmanagementv1alpha1.ExternalSource, secret *corev1.Secret) (sources.Source, error)

// ExternalAttributeSyncReconciler reconciles an ExternalAttributeSync object.
//
// On each reconcile the controller:
//
//  1. Resolves the Secret (if any) referenced by the source.
//  2. Constructs a sources.Source instance via the registered factory.
//  3. Calls Fetch and projects records through the AttributeMapping.
//  4. Filters to the collectors selected by spec.selector (evaluated against
//     in-cluster Collector status).
//  5. Writes the canonical owned-key claim into status — the Collector
//     controller reads this directly when computing merged desired state.
//  6. Requeues at the next scheduled fire time.
//
// This controller does NOT call the Fleet Management API. The Collector
// controller is the sole writer to Fleet for collector remote attributes,
// and it picks up new ExternalAttributeSync state through the watches it
// established in SetupWithManager.
//
// MaxConcurrentReconciles > 1 is safe: Fetch calls are per-source and do
// not share external state with other sync reconciles. Configurable via
// --controller-sync-max-concurrent (default 4).
type ExternalAttributeSyncReconciler struct {
	client.Client
	Scheme                  *runtime.Scheme
	Recorder                record.EventRecorder
	MaxConcurrentReconciles int

	// Factory builds a Source from the spec. Tests inject a fake; the
	// real wiring (cmd/main.go) provides a factory that dispatches to
	// pkg/sources implementations by kind.
	Factory SourceFactory

	// CronParser parses cron-format schedules. If nil, a sensible default
	// is used (5-field standard cron). Exposed for tests.
	CronParser cron.Parser
}

var _ reconcile.Reconciler = &ExternalAttributeSyncReconciler{}

// defaultCronParser is the 5-field standard cron parser shared with the
// webhook so a schedule that the webhook accepted will also parse here.
var defaultCronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

func (r *ExternalAttributeSyncReconciler) parser() cron.Parser {
	// cron.Parser is a value type without a useful zero value (its parsed
	// flag set is zero), so a nil-equivalent test isn't reliable; instead
	// detect via Parse on a known-good expression.
	if _, err := r.CronParser.Parse("* * * * *"); err == nil {
		return r.CronParser
	}
	return defaultCronParser
}

func (r *ExternalAttributeSyncReconciler) emitEvent(object runtime.Object, eventtype, reason, message string) {
	if r.Recorder != nil {
		r.Recorder.Event(object, eventtype, reason, message)
	}
}

func (r *ExternalAttributeSyncReconciler) emitEventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
	if r.Recorder != nil {
		r.Recorder.Eventf(object, eventtype, reason, messageFmt, args...)
	}
}

// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=externalattributesyncs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=externalattributesyncs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=collectors,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile fetches the external source and updates owned-keys status.
func (r *ExternalAttributeSyncReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("reconciling ExternalAttributeSync", "namespace", req.Namespace, "name", req.Name)

	// B5: outcome is set at every return path; the deferred increment fires
	// once per Reconcile call so PromQL rate(...) reflects every exit path
	// (NotFound, NoOp, Stalled, Synced, etc.) instead of only the success /
	// error status-update paths.
	var outcome string
	defer func() {
		if outcome != "" {
			fleetResourceSyncedTotal.WithLabelValues("ExternalAttributeSync", outcome).Inc()
		}
	}()

	sync := &fleetmanagementv1alpha1.ExternalAttributeSync{}
	if err := r.Get(ctx, req.NamespacedName, sync); err != nil {
		if apierrors.IsNotFound(err) {
			// No finalizer is registered. Collectors that previously
			// inherited keys from this sync will lose them on their
			// next Collector reconcile because they read live
			// ExternalAttributeSync state.
			//
			// D5: drop the owned-keys gauge series for this CR so deleted
			// CRs don't leave ghost time series in Prometheus forever.
			fleetExternalSyncOwnedKeys.DeleteLabelValues(req.Namespace, req.Name)
			outcome = outcomeNotFound
			return ctrl.Result{}, nil
		}
		outcome = externalSyncReasonSourceFailed
		return ctrl.Result{}, err
	}

	nextRun, scheduleErr := r.scheduleNext(sync, time.Now())
	if scheduleErr != nil {
		log.Error(scheduleErr, "invalid schedule",
			"namespace", sync.Namespace, "name", sync.Name, "schedule", sync.Spec.Schedule)
		r.emitEventf(sync, corev1.EventTypeWarning, externalSyncEventReasonSourceFailed,
			"Invalid schedule %q: %v", sync.Spec.Schedule, scheduleErr)
		outcome = externalSyncReasonScheduleErr
		return r.updateStatusError(ctx, sync, externalSyncReasonScheduleErr, scheduleErr)
	}

	secret, err := r.resolveSecret(ctx, sync)
	if err != nil {
		log.Error(err, "failed to resolve source secret",
			"namespace", sync.Namespace, "name", sync.Name)
		r.emitEventf(sync, corev1.EventTypeWarning, externalSyncEventReasonSourceFailed,
			"Failed to resolve source secret: %v", err)
		_, statusErr := r.updateStatusError(ctx, sync, externalSyncReasonSourceFailed, err)
		outcome = externalSyncReasonSourceFailed
		if statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: defaultExternalSyncRequeueOnError}, nil
	}

	if r.Factory == nil {
		err := fmt.Errorf("no SourceFactory registered for ExternalAttributeSync controller")
		outcome = externalSyncReasonSourceFailed
		return r.updateStatusError(ctx, sync, externalSyncReasonSourceFailed, err)
	}

	src, err := r.Factory(sync.Spec.Source, secret)
	if err != nil {
		log.Error(err, "failed to construct source",
			"namespace", sync.Namespace, "name", sync.Name, "kind", sync.Spec.Source.Kind)
		r.emitEventf(sync, corev1.EventTypeWarning, externalSyncEventReasonSourceFailed,
			"Failed to construct %s source: %v", sync.Spec.Source.Kind, err)
		_, statusErr := r.updateStatusError(ctx, sync, externalSyncReasonSourceFailed, err)
		outcome = externalSyncReasonSourceFailed
		if statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: defaultExternalSyncRequeueOnError}, nil
	}

	records, err := src.Fetch(ctx)
	if err != nil {
		log.Error(err, "source Fetch failed",
			"namespace", sync.Namespace, "name", sync.Name, "kind", src.Kind())
		r.emitEventf(sync, corev1.EventTypeWarning, externalSyncEventReasonSourceFailed,
			"Source Fetch failed: %v", err)
		_, statusErr := r.updateStatusError(ctx, sync, externalSyncReasonSourceFailed, err)
		outcome = externalSyncReasonSourceFailed
		if statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		// Retry on the source-error cadence, not the schedule — the
		// next reconcile event from the watch will reset to the
		// schedule once the source recovers.
		return ctrl.Result{RequeueAfter: defaultExternalSyncRequeueOnError}, nil
	}

	matchedCollectors, err := r.matchedCollectors(ctx, sync)
	if err != nil {
		outcome = externalSyncReasonSourceFailed
		return ctrl.Result{}, err
	}

	owned, recordsApplied, projectionErr := r.projectRecords(records, sync, matchedCollectors)
	if projectionErr != nil {
		outcome = externalSyncReasonSourceFailed
		return r.updateStatusError(ctx, sync, externalSyncReasonSourceFailed, projectionErr)
	}

	now := metav1.Now()
	priorRecordsApplied := sync.Status.RecordsApplied

	if !sync.Spec.AllowEmptyResults && len(records) == 0 && priorRecordsApplied > 0 {
		// Empty-result safety guard: keep the previous OwnedKeys claim,
		// surface a Stalled condition, and requeue at the schedule.
		log.Info("source returned 0 records but previous run had >0; keeping previous claim",
			"namespace", sync.Namespace, "name", sync.Name, "priorApplied", priorRecordsApplied)
		r.emitEvent(sync, corev1.EventTypeWarning, externalSyncEventReasonStalled,
			"Source returned 0 records; previous claim preserved (set spec.allowEmptyResults=true to override)")

		sync.Status.LastSyncTime = &now
		sync.Status.RecordsSeen = int32(len(records))
		sync.Status.ObservedGeneration = sync.Generation
		setStalledCondition(&sync.Status.Conditions, sync.Generation, true,
			"Source returned 0 records; OwnedKeys claim preserved")
		setReadyCondition(&sync.Status.Conditions, sync.Generation, false, externalSyncReasonStalled,
			"Source returned 0 records; previous OwnedKeys claim preserved")
		setSyncedCondition(&sync.Status.Conditions, sync.Generation, false, externalSyncReasonStalled,
			"Source returned 0 records")

		if err := r.Status().Update(ctx, sync); err != nil {
			if apierrors.IsConflict(err) {
				outcome = outcomeNoOp
				return ctrl.Result{Requeue: true}, nil
			}
			outcome = externalSyncReasonSourceFailed
			return ctrl.Result{}, err
		}
		outcome = externalSyncReasonStalled
		return ctrl.Result{RequeueAfter: durationUntil(nextRun)}, nil
	}

	ownedStatus, truncated := ownedKeysToStatus(owned)

	// D8: record owned-key count for this CR up-front, BEFORE the no-op
	// short-circuit. This ensures the gauge reflects current intent on
	// operator restart even when the status itself is steady-state and the
	// reconcile takes the no-op path.
	fleetExternalSyncOwnedKeys.WithLabelValues(sync.Namespace, sync.Name).Set(float64(len(ownedStatus)))

	// OBS-03: record sync age using the previous LastSuccessTime before overwriting it
	if sync.Status.LastSuccessTime != nil && !sync.Status.LastSuccessTime.IsZero() {
		fleetResourceSyncAge.WithLabelValues("ExternalAttributeSync").
			Observe(time.Since(sync.Status.LastSuccessTime.Time).Seconds())
	}

	// No-op check: skip the status write only when EVERY observable field
	// of the desired status already equals the current status. The previous
	// implementation compared only generation, counts and owned-keys —
	// missing the Stalled / Truncated condition transitions (B3, B4).
	// Without this check, a stalled source that recovers with the same
	// records as the last successful run would leave Stalled=True forever.
	if externalSyncStatusUnchanged(sync, len(records), recordsApplied, ownedStatus, truncated) {
		log.V(1).Info("no-op sync: source, owned-keys, and conditions unchanged, skipping status write",
			"namespace", sync.Namespace, "name", sync.Name)
		outcome = outcomeNoOp
		return ctrl.Result{RequeueAfter: durationUntil(nextRun)}, nil
	}

	sync.Status.LastSyncTime = &now
	sync.Status.LastSuccessTime = &now
	sync.Status.RecordsSeen = int32(len(records))
	sync.Status.RecordsApplied = int32(recordsApplied)
	sync.Status.OwnedKeys = ownedStatus
	sync.Status.ObservedGeneration = sync.Generation

	if truncated {
		r.emitEventf(sync, corev1.EventTypeWarning, "Truncated",
			"ownedKeys capped at %d; collectors beyond cap may retain attributes on CR deletion",
			maxOwnedKeys)
		meta.SetStatusCondition(&sync.Status.Conditions, metav1.Condition{
			Type:               externalSyncConditionTruncated,
			Status:             metav1.ConditionTrue,
			Reason:             externalSyncReasonTruncated,
			Message:            fmt.Sprintf("ownedKeys truncated to %d entries; attributes for collectors beyond the cap may not be removed on CR deletion — shard sources with >%d collectors", maxOwnedKeys, maxOwnedKeys),
			ObservedGeneration: sync.Generation,
		})
	} else {
		meta.SetStatusCondition(&sync.Status.Conditions, metav1.Condition{
			Type:               externalSyncConditionTruncated,
			Status:             metav1.ConditionFalse,
			Reason:             externalSyncReasonNotTruncated,
			ObservedGeneration: sync.Generation,
		})
	}

	setStalledCondition(&sync.Status.Conditions, sync.Generation, false, "")
	setReadyCondition(&sync.Status.Conditions, sync.Generation, true, externalSyncReasonSynced,
		fmt.Sprintf("Fetched %d records, applied %d to %d collector(s)", len(records), recordsApplied, len(owned)))
	setSyncedCondition(&sync.Status.Conditions, sync.Generation, true, externalSyncReasonSynced,
		"Source synced successfully")

	if err := r.Status().Update(ctx, sync); err != nil {
		if apierrors.IsConflict(err) {
			outcome = outcomeNoOp
			return ctrl.Result{Requeue: true}, nil
		}
		outcome = externalSyncReasonSourceFailed
		return ctrl.Result{}, err
	}

	r.emitEventf(sync, corev1.EventTypeNormal, externalSyncEventReasonSynced,
		"Synced %d records, %d applied across %d collector(s)", len(records), recordsApplied, len(owned))

	if truncated {
		outcome = externalSyncReasonTruncated
	} else {
		outcome = externalSyncReasonSynced
	}
	return ctrl.Result{RequeueAfter: durationUntil(nextRun)}, nil
}

// externalSyncStatusUnchanged reports whether the desired status implied by
// (recordsSeen, recordsApplied, ownedStatus, truncated) is already reflected
// in sync.Status, including the Stalled and Truncated condition states.
//
// B3/B4: comparing only generation + counts + owned-keys is insufficient;
// it lets a Stalled→same-data-recovery transition keep Stalled=True forever
// because none of those fields change but the desired Stalled value flips.
func externalSyncStatusUnchanged(
	sync *fleetmanagementv1alpha1.ExternalAttributeSync,
	recordsSeen int,
	recordsApplied int,
	ownedStatus []fleetmanagementv1alpha1.OwnedKeyEntry,
	truncated bool,
) bool {
	if sync.Status.ObservedGeneration != sync.Generation {
		return false
	}
	if sync.Status.RecordsSeen != int32(recordsSeen) {
		return false
	}
	if sync.Status.RecordsApplied != int32(recordsApplied) {
		return false
	}
	if !ownedKeyEntriesEqual(sync.Status.OwnedKeys, ownedStatus) {
		return false
	}
	if !externalSyncTruncatedConditionMatches(sync, truncated) {
		return false
	}
	// On the success path, Stalled is always False. If currently True, this
	// is a recovery and we MUST write status to clear the condition.
	if !externalSyncStalledIsFalse(sync) {
		return false
	}
	// Likewise Ready=True/Synced=True on the success path. If either is
	// not currently True, this is a recovery and we must write status.
	if !externalSyncReadyAndSyncedAreTrue(sync) {
		return false
	}
	return true
}

// externalSyncTruncatedConditionMatches returns true when the existing
// Truncated condition is already in the desired state (True iff want is
// true; missing condition counts as False).
func externalSyncTruncatedConditionMatches(sync *fleetmanagementv1alpha1.ExternalAttributeSync, want bool) bool {
	cond := meta.FindStatusCondition(sync.Status.Conditions, externalSyncConditionTruncated)
	if cond == nil {
		return !want
	}
	if want {
		return cond.Status == metav1.ConditionTrue
	}
	return cond.Status == metav1.ConditionFalse
}

// externalSyncStalledIsFalse returns true when the Stalled condition is
// either absent or set to False. The success path always wants Stalled=False.
func externalSyncStalledIsFalse(sync *fleetmanagementv1alpha1.ExternalAttributeSync) bool {
	cond := meta.FindStatusCondition(sync.Status.Conditions, externalSyncConditionStalled)
	if cond == nil {
		return true
	}
	return cond.Status == metav1.ConditionFalse
}

// externalSyncReadyAndSyncedAreTrue returns true when both Ready and Synced
// conditions are currently True. The success path always wants both True.
func externalSyncReadyAndSyncedAreTrue(sync *fleetmanagementv1alpha1.ExternalAttributeSync) bool {
	ready := meta.FindStatusCondition(sync.Status.Conditions, conditionTypeReady)
	if ready == nil || ready.Status != metav1.ConditionTrue {
		return false
	}
	synced := meta.FindStatusCondition(sync.Status.Conditions, conditionTypeSynced)
	if synced == nil || synced.Status != metav1.ConditionTrue {
		return false
	}
	return true
}

// scheduleNext computes the next requeue time for the given schedule. Tries
// Go duration first (for small intervals), falls back to cron.
func (r *ExternalAttributeSyncReconciler) scheduleNext(sync *fleetmanagementv1alpha1.ExternalAttributeSync, now time.Time) (time.Time, error) {
	if d, err := time.ParseDuration(sync.Spec.Schedule); err == nil {
		if d <= 0 {
			return time.Time{}, fmt.Errorf("schedule duration must be positive, got %q", sync.Spec.Schedule)
		}
		return now.Add(d), nil
	}

	sched, err := r.parser().Parse(sync.Spec.Schedule)
	if err != nil {
		return time.Time{}, fmt.Errorf("schedule %q is neither a valid duration nor a valid cron expression: %w", sync.Spec.Schedule, err)
	}
	return sched.Next(now), nil
}

// resolveSecret reads the optional Secret referenced by the source. Returns
// nil if no SecretRef is set (no auth configured); returns an error only if
// the SecretRef is set but unreadable.
func (r *ExternalAttributeSyncReconciler) resolveSecret(ctx context.Context, sync *fleetmanagementv1alpha1.ExternalAttributeSync) (*corev1.Secret, error) {
	ref := sync.Spec.Source.SecretRef
	if ref == nil || ref.Name == "" {
		return nil, nil
	}
	ns := ref.Namespace
	if ns == "" {
		ns = sync.Namespace
	}
	secret := &corev1.Secret{}
	key := types.NamespacedName{Namespace: ns, Name: ref.Name}
	if err := r.Get(ctx, key, secret); err != nil {
		return nil, fmt.Errorf("get secret %s: %w", key, err)
	}
	return secret, nil
}

// matchedCollectors returns the in-cluster Collector CRs whose IDs match the
// sync's selector. Each Collector's local attributes (from status) plus a
// synthetic "collector.id" key are exposed to the matcher engine.
func (r *ExternalAttributeSyncReconciler) matchedCollectors(ctx context.Context, sync *fleetmanagementv1alpha1.ExternalAttributeSync) (map[string]struct{}, error) {
	var collectors fleetmanagementv1alpha1.CollectorList
	if err := r.List(ctx, &collectors, client.InNamespace(sync.Namespace)); err != nil {
		return nil, fmt.Errorf("list collectors: %w", err)
	}

	sel := attributes.Selector{
		Matchers:     sync.Spec.Selector.Matchers,
		CollectorIDs: sync.Spec.Selector.CollectorIDs,
	}

	out := make(map[string]struct{}, len(collectors.Items))
	for i := range collectors.Items {
		c := &collectors.Items[i]
		attrs := make(map[string]string, len(c.Status.LocalAttributes)+1)
		for k, v := range c.Status.LocalAttributes {
			attrs[k] = v
		}
		attrs["collector.id"] = c.Spec.ID
		if sel.Match(c.Spec.ID, attrs) {
			out[c.Spec.ID] = struct{}{}
		}
	}
	return out, nil
}

// projectRecords applies the AttributeMapping to each fetched record,
// filters by the matched-collector set, and aggregates per-collector
// attribute claims. Returns:
//
//   - owned: collectorID -> (attrKey -> attrValue)
//   - recordsApplied: count of records that produced a (collector, attrs) entry
//   - error: only on configuration errors that should fail the whole sync
func (r *ExternalAttributeSyncReconciler) projectRecords(
	records []sources.Record,
	sync *fleetmanagementv1alpha1.ExternalAttributeSync,
	matched map[string]struct{},
) (map[string]map[string]string, int, error) {
	mapping := sync.Spec.Mapping
	if mapping.CollectorIDField == "" {
		return nil, 0, fmt.Errorf("spec.mapping.collectorIDField is empty (webhook should have rejected this)")
	}

	out := make(map[string]map[string]string)
	applied := 0

	for _, rec := range records {
		idVal, ok := sources.FieldString(rec, mapping.CollectorIDField)
		if !ok || idVal == "" {
			continue
		}

		// RequiredKeys gate: skip records missing any required source field.
		if !hasAllRequired(rec, mapping.RequiredKeys) {
			continue
		}

		if _, isMatched := matched[idVal]; !isMatched {
			continue
		}

		attrs := make(map[string]string, len(mapping.AttributeFields))
		for attrKey, sourceField := range mapping.AttributeFields {
			val, ok := sources.FieldString(rec, sourceField)
			if !ok {
				continue
			}
			attrs[attrKey] = val
		}
		if len(attrs) == 0 {
			continue
		}

		// Last-record-wins on duplicate collectorID. The mapping doesn't
		// promise per-record ordering, so users with multiple records
		// per collector should consolidate upstream.
		out[idVal] = attrs
		applied++
	}

	return out, applied, nil
}

// updateStatusError writes a failure condition and bubbles the original
// error so controller-runtime backs off appropriately.
func (r *ExternalAttributeSyncReconciler) updateStatusError(
	ctx context.Context,
	sync *fleetmanagementv1alpha1.ExternalAttributeSync,
	reason string,
	originalErr error,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	now := metav1.Now()
	sync.Status.LastSyncTime = &now
	sync.Status.ObservedGeneration = sync.Generation
	message := fmt.Sprintf("%s: %v", reason, originalErr)

	setReadyCondition(&sync.Status.Conditions, sync.Generation, false, reason, message)
	setSyncedCondition(&sync.Status.Conditions, sync.Generation, false, reason, message)

	if updateErr := r.Status().Update(ctx, sync); updateErr != nil {
		if apierrors.IsConflict(updateErr) {
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(updateErr, "failed to update status after error",
			"namespace", sync.Namespace, "name", sync.Name, "reason", reason)
	}

	// B5: outcome counter is incremented by the deferred handler in Reconcile.
	return ctrl.Result{}, originalErr
}

// SetupWithManager wires the reconciler. The controller watches:
//
//  1. ExternalAttributeSync itself (For).
//  2. The Secret it references — credential rotation triggers a re-fetch
//     without waiting for the schedule.
//  3. Collector — when a Collector CR appears or its localAttributes change,
//     the matched-collector set may shift, so re-fetch and re-project. The
//     handler uses externalSyncMatcherKeyIndex to enqueue only the syncs
//     whose matchers reference keys present in the Collector's attributes.
func (r *ExternalAttributeSyncReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx,
		&fleetmanagementv1alpha1.ExternalAttributeSync{},
		externalSyncMatcherKeyIndex,
		func(o client.Object) []string {
			s := o.(*fleetmanagementv1alpha1.ExternalAttributeSync)
			keys := attributes.MatcherKeys(s.Spec.Selector.Matchers)
			// B2: when a sync selects purely by collectorIDs (no matchers),
			// it produces zero matcher keys. Without a synthetic sentinel it
			// would be indexed under nothing and would never wake on
			// Collector add. The watch handler always queries this bucket.
			if len(s.Spec.Selector.Matchers) == 0 && len(s.Spec.Selector.CollectorIDs) > 0 {
				keys = append(keys, attributes.SyntheticCollectorIDsOnlyKey)
			}
			return keys
		},
	); err != nil {
		return fmt.Errorf("indexing ExternalAttributeSync matcher keys: %w", err)
	}

	maxConcurrent := r.MaxConcurrentReconciles
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&fleetmanagementv1alpha1.ExternalAttributeSync{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.syncsReferencingSecret),
		).
		Watches(
			&fleetmanagementv1alpha1.Collector{},
			handler.EnqueueRequestsFromMapFunc(r.mapCollectorToAffectedSyncs),
		).
		Named("externalattributesync").
		Complete(r)
}

// mapCollectorToAffectedSyncs returns reconcile requests for only the
// ExternalAttributeSyncs whose matchers reference at least one key present in
// the changed Collector's attributes. This avoids the O(collectors * syncs)
// fan-out from the naive "enqueue every sync" approach.
//
// To preserve correctness for negation-only and collectorIDs-only syncs
// (which would otherwise be invisible to a key-keyed index), the handler
// also unconditionally queries two synthetic sentinel buckets that the
// IndexField extractor populates for those sync shapes:
//
//   - attributes.SyntheticHasNegationKey — set when MatcherKeys sees a
//     `!=` or `!~` operator. A negation matcher matches Collectors LACKING
//     the referenced key, so a key-keyed index would otherwise miss it.
//   - attributes.SyntheticCollectorIDsOnlyKey — set by the IndexField
//     wrapper when a sync has only spec.selector.collectorIDs and no
//     matchers. Such a sync produces zero matcher keys and would
//     otherwise be indexed under nothing.
func (r *ExternalAttributeSyncReconciler) mapCollectorToAffectedSyncs(ctx context.Context, obj client.Object) []reconcile.Request {
	log := logf.FromContext(ctx)

	collector, ok := obj.(*fleetmanagementv1alpha1.Collector)
	if !ok {
		return nil
	}

	// Build the set of attribute keys the Collector carries.
	collectorKeys := make(map[string]struct{})
	for k := range collector.Spec.RemoteAttributes {
		collectorKeys[k] = struct{}{}
	}
	for k := range collector.Status.LocalAttributes {
		collectorKeys[k] = struct{}{}
	}
	// Always include the synthetic collector.id key.
	collectorKeys["collector.id"] = struct{}{}
	// Always include both synthetic sentinels so that negation-only and
	// collectorIDs-only syncs are enqueued on every Collector event.
	// See doc comment above for the rationale.
	collectorKeys[attributes.SyntheticHasNegationKey] = struct{}{}
	collectorKeys[attributes.SyntheticCollectorIDsOnlyKey] = struct{}{}

	seen := map[types.NamespacedName]struct{}{}
	var reqs []reconcile.Request
	for key := range collectorKeys {
		var list fleetmanagementv1alpha1.ExternalAttributeSyncList
		if err := r.List(ctx, &list,
			client.InNamespace(collector.Namespace),
			client.MatchingFields{externalSyncMatcherKeyIndex: key},
		); err != nil {
			log.Error(err, "failed to list ExternalAttributeSyncs by index for Collector watch",
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

// syncsReferencingSecret returns reconcile requests for every
// ExternalAttributeSync in the same namespace whose source.SecretRef
// references the changed Secret.
func (r *ExternalAttributeSyncReconciler) syncsReferencingSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}

	var list fleetmanagementv1alpha1.ExternalAttributeSyncList
	if err := r.List(ctx, &list, client.InNamespace(secret.Namespace)); err != nil {
		logf.FromContext(ctx).Error(err, "listing syncs for secret watch fan-out",
			"secret", secret.Namespace+"/"+secret.Name)
		return nil
	}

	var out []reconcile.Request
	for i := range list.Items {
		s := &list.Items[i]
		ref := s.Spec.Source.SecretRef
		if ref == nil || ref.Name != secret.Name {
			continue
		}
		ns := ref.Namespace
		if ns == "" {
			ns = s.Namespace
		}
		if ns != secret.Namespace {
			continue
		}
		out = append(out, reconcile.Request{
			NamespacedName: client.ObjectKey{Namespace: s.Namespace, Name: s.Name},
		})
	}
	return out
}

// hasAllRequired returns true if every key in required is present in r.
func hasAllRequired(r sources.Record, required []string) bool {
	for _, k := range required {
		if _, ok := r[k]; !ok {
			return false
		}
	}
	return true
}

// ownedKeysToStatus converts the projected per-collector attribute map into
// the OwnedKeyEntry slice the CRD status exposes, sorted by collectorID for
// deterministic output. Returns the slice and a truncated flag — when
// truncated=true the caller must set the Truncated condition and emit a
// Warning event so operators know that collectors beyond the cap are affected.
func ownedKeysToStatus(owned map[string]map[string]string) ([]fleetmanagementv1alpha1.OwnedKeyEntry, bool) {
	ids := make([]string, 0, len(owned))
	for id := range owned {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	truncated := false
	if len(ids) > maxOwnedKeys {
		ids = ids[:maxOwnedKeys]
		truncated = true
	}
	out := make([]fleetmanagementv1alpha1.OwnedKeyEntry, 0, len(ids))
	for _, id := range ids {
		out = append(out, fleetmanagementv1alpha1.OwnedKeyEntry{
			CollectorID: id,
			Attributes:  owned[id],
		})
	}
	return out, truncated
}

// ownedKeyEntriesEqual reports whether two OwnedKeyEntry slices have the
// same content in the same order. Used by the no-op short-circuit.
func ownedKeyEntriesEqual(a, b []fleetmanagementv1alpha1.OwnedKeyEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].CollectorID != b[i].CollectorID {
			return false
		}
		if len(a[i].Attributes) != len(b[i].Attributes) {
			return false
		}
		for k, v := range a[i].Attributes {
			if b[i].Attributes[k] != v {
				return false
			}
		}
	}
	return true
}

func durationUntil(t time.Time) time.Duration {
	d := time.Until(t)
	if d < 0 {
		return time.Second
	}
	return d
}

// Condition helpers for the ExternalAttributeSync controller. They live in
// this file (rather than alongside other condition helpers) so the helpers
// can use the controller-specific Stalled condition without polluting the
// shared package.
func setReadyCondition(conds *[]metav1.Condition, gen int64, ready bool, reason, message string) {
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	meta.SetStatusCondition(conds, metav1.Condition{
		Type:               conditionTypeReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: gen,
	})
}

func setSyncedCondition(conds *[]metav1.Condition, gen int64, synced bool, reason, message string) {
	status := metav1.ConditionFalse
	if synced {
		status = metav1.ConditionTrue
	}
	meta.SetStatusCondition(conds, metav1.Condition{
		Type:               conditionTypeSynced,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: gen,
	})
}

func setStalledCondition(conds *[]metav1.Condition, gen int64, stalled bool, message string) {
	status := metav1.ConditionFalse
	if stalled {
		status = metav1.ConditionTrue
	}
	reason := externalSyncReasonStalled
	if !stalled {
		reason = externalSyncReasonSynced
		message = "Source not stalled"
	}
	meta.SetStatusCondition(conds, metav1.Condition{
		Type:               externalSyncConditionStalled,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: gen,
	})
}
