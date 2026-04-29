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

// Cache Usage Audit:
//
// This controller uses ZERO List() operations in the reconciliation path.
// The single Get() in Reconcile() reads from the informer cache, not the API server.
// All writes (Update, Status().Update()) go directly to the API server.
// The informer watch established in SetupWithManager() keeps the cache current.
// The ObservedGeneration pattern handles cache-lag gracefully (reconcile may be
// re-triggered before the cache reflects status updates, but the generation check
// prevents duplicate work).
//
// Rationale: List() operations are avoided because they either bypass the cache
// (if using a direct client) or load all resources into memory unnecessarily.
// Single-resource Get() operations are sufficient for this controller's reconciliation
// pattern where each Pipeline is reconciled independently.
//
// Reconcile Loop Audit:
//
// This controller makes exactly 5 Kubernetes API calls across all reconciliation paths:
// - 1 Get() operation: Fetch the Pipeline resource that triggered reconciliation (cached read)
// - 2 Update() operations: Add finalizer (spec change), remove finalizer (deletion complete)
// - 2 Status().Update() operations: Record success state, record error state
//
// Path breakdown:
// - Happy path (create/update): 3 calls = Get + UpsertPipeline (Fleet API) + Status().Update()
// - Finalizer addition: 2 calls = Get + Update, then returns immediately for re-reconciliation
// - Delete path: 3 calls = Get + DeletePipeline (Fleet API) + Update (finalizer removal)
// - ObservedGeneration skip: 1 call = Get only, no further processing (spec unchanged)
//
// All API calls are justified and cannot be eliminated without breaking controller semantics.
//
// Watch Pattern Audit:
//
// - Resync: Disabled (nil SyncPeriod in cmd/main.go) - appropriate for watch-driven controller
// - Rate limiter: Default controller-runtime workqueue.DefaultTypedControllerRateLimiter
//   (5ms-1000s exponential backoff + 10qps bucket rate limit)
// - Backoff: Four return patterns correctly mapped to error types (error, Requeue, RequeueAfter, nil)
// - Storm prevention: Single For() watch on Pipeline CRD, Status subresource updates, ObservedGeneration guard

package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
	"github.com/grafana/fleet-management-operator/pkg/fleetclient"
)

const (
	// pipelineFinalizer is the finalizer for Pipeline resources
	pipelineFinalizer = "pipeline.fleetmanagement.grafana.com/finalizer"

	// Condition types
	conditionTypeReady  = "Ready"
	conditionTypeSynced = "Synced"

	// Condition reasons
	reasonSynced          = "Synced"
	reasonSyncFailed      = "SyncFailed"
	reasonValidationError = "ValidationError"
	reasonDeleting        = "Deleting"
	reasonDeleteFailed    = "DeleteFailed"

	// Event reasons
	eventReasonSynced         = "Synced"
	eventReasonSyncFailed     = "SyncFailed"
	eventReasonCreated        = "Created"
	eventReasonUpdated        = "Updated"
	eventReasonDeleted        = "Deleted"
	eventReasonDeleteFailed   = "DeleteFailed"
	eventReasonValidationFail = "ValidationFailed"
	eventReasonRateLimited    = "RateLimited"
	eventReasonRecreated      = "Recreated"
)

// FleetPipelineClient defines the interface for interacting with Fleet Management API
type FleetPipelineClient interface {
	UpsertPipeline(ctx context.Context, req *fleetclient.UpsertPipelineRequest) (*fleetclient.Pipeline, error)
	DeletePipeline(ctx context.Context, id string) error
}

// PipelineReconciler reconciles a Pipeline object
type PipelineReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	FleetClient FleetPipelineClient
	Recorder    record.EventRecorder
}

// Ensure PipelineReconciler implements reconcile.Reconciler at compile time
var _ reconcile.Reconciler = &PipelineReconciler{}

// emitEvent safely emits an event, checking if Recorder is not nil
func (r *PipelineReconciler) emitEvent(object runtime.Object, eventtype, reason, message string) {
	if r.Recorder != nil {
		r.Recorder.Event(object, eventtype, reason, message)
	}
}

// emitEventf safely emits an event with formatting, checking if Recorder is not nil
func (r *PipelineReconciler) emitEventf(object runtime.Object, eventtype, reason, messageFmt string, args ...any) {
	if r.Recorder != nil {
		r.Recorder.Eventf(object, eventtype, reason, messageFmt, args...)
	}
}

// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=pipelines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=pipelines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=pipelines/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.0/pkg/reconcile
func (r *PipelineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	log.Info("reconciling Pipeline", "namespace", req.Namespace, "name", req.Name)

	// outcome is set at every return path; the deferred increment fires once
	// per Reconcile call so PromQL rate(...) reflects every exit (including
	// NotFound / NoOp short-circuits) instead of only the success / error
	// status-update paths. See B5.
	var outcome string
	defer func() {
		if outcome != "" {
			fleetResourceSyncedTotal.WithLabelValues("Pipeline", outcome).Inc()
		}
	}()

	// 1. Fetch the Pipeline resource
	pipeline := &fleetmanagementv1alpha1.Pipeline{}
	// Cache: This Get() reads from the informer cache (not direct API server call) because
	// r.Client is set via mgr.GetClient() which returns a cached reader. The cache is populated
	// by the watch established in SetupWithManager().
	// Reconcile: Required entry point - fetch the resource that triggered reconciliation. Cannot
	// be eliminated; this is the standard controller-runtime pattern.
	if err := r.Get(ctx, req.NamespacedName, pipeline); err != nil {
		if apierrors.IsNotFound(err) {
			// Pipeline was deleted
			log.Info("Pipeline not found, likely deleted", "namespace", req.Namespace, "name", req.Name)
			outcome = outcomeNotFound
			return ctrl.Result{}, nil
		}
		log.Error(err, "failed to get Pipeline", "namespace", req.Namespace, "name", req.Name)
		outcome = reasonSyncFailed
		return ctrl.Result{}, err
	}

	// 2. Handle deletion
	if !pipeline.DeletionTimestamp.IsZero() {
		var deleteOutcome string
		result, err := r.reconcileDelete(ctx, pipeline, &deleteOutcome)
		outcome = deleteOutcome
		return result, err
	}

	// 3. Add finalizer if not present
	if !controllerutil.ContainsFinalizer(pipeline, pipelineFinalizer) {
		controllerutil.AddFinalizer(pipeline, pipelineFinalizer)
		// Cache: Update() writes directly to the API server (not cached). The subsequent reconcile
		// triggered by the watch event will see the updated object with the finalizer.
		// Reconcile: Finalizer must be persisted before any Fleet Management API call. Returns
		// immediately to let the watch re-trigger reconciliation with finalizer present.
		if err := r.Update(ctx, pipeline); err != nil {
			log.Error(err, "failed to add finalizer", "namespace", pipeline.Namespace, "name", pipeline.Name)
			outcome = reasonSyncFailed
			return ctrl.Result{}, err
		}
		log.Info("added finalizer", "namespace", pipeline.Namespace, "name", pipeline.Name)
		outcome = outcomeNoOp
		return ctrl.Result{}, nil
	}

	// 4. Check if reconciliation is paused
	if isPaused(pipeline) {
		return ctrl.Result{}, nil
	}

	// 5. Check if reconciliation is needed (observedGeneration pattern)
	if pipeline.Status.ObservedGeneration == pipeline.Generation {
		log.V(1).Info("pipeline already reconciled, skipping", "namespace", pipeline.Namespace, "name", pipeline.Name, "generation", pipeline.Generation)
		outcome = outcomeNoOp
		return ctrl.Result{}, nil
	}

	// 6. Reconcile normal case. The inner helpers write to normalOutcome via
	// pointer so the deferred counter sees the precise reason (Synced,
	// Recreated, RateLimited, ValidationError, SyncFailed) regardless of
	// which exit path was taken.
	var normalOutcome string
	result, err := r.reconcileNormal(ctx, pipeline, &normalOutcome)
	outcome = normalOutcome
	return result, err
}

// reconcileNormal handles normal reconciliation (create/update). outcome is
// set to the metric reason for this reconcile; callers (Reconcile and the
// 404-recreate recursion in handleAPIError) read it via pointer so the
// deferred counter increment sees the exact terminal reason.
func (r *PipelineReconciler) reconcileNormal(ctx context.Context, pipeline *fleetmanagementv1alpha1.Pipeline, outcome *string) (ctrl.Result, error) {
	// Build the upsert request
	req := r.buildUpsertRequest(pipeline)

	// Call Fleet Management API
	apiPipeline, err := r.FleetClient.UpsertPipeline(ctx, req)
	if err != nil {
		return r.handleAPIError(ctx, pipeline, err, outcome)
	}

	// Update status with successful sync
	return r.updateStatusSuccess(ctx, pipeline, apiPipeline, outcome)
}

// reconcileDelete handles pipeline deletion. outcome is set to the metric
// reason ("Deleted" on success, "DeleteFailed" / "SyncFailed" / status reason
// on failure paths) so the Reconcile deferred counter records every outcome.
func (r *PipelineReconciler) reconcileDelete(ctx context.Context, pipeline *fleetmanagementv1alpha1.Pipeline, outcome *string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(pipeline, pipelineFinalizer) {
		// Finalizer already removed, nothing to do
		*outcome = outcomeNoOp
		return ctrl.Result{}, nil
	}

	log.Info("deleting Pipeline from Fleet Management", "namespace", pipeline.Namespace, "name", pipeline.Name, "id", pipeline.Status.ID)

	// Delete from Fleet Management if we have an ID
	if pipeline.Status.ID != "" {
		if err := r.FleetClient.DeletePipeline(ctx, pipeline.Status.ID); err != nil {
			// Check if it's a 404 (already deleted)
			if apiErr, ok := err.(*fleetclient.FleetAPIError); ok && apiErr.StatusCode == http.StatusNotFound {
				log.Info("pipeline already deleted from Fleet Management", "namespace", pipeline.Namespace, "name", pipeline.Name)
				r.emitEvent(pipeline, corev1.EventTypeNormal, eventReasonDeleted,
					"Pipeline already deleted from Fleet Management")
			} else {
				log.Error(err, "failed to delete pipeline from Fleet Management", "namespace", pipeline.Namespace, "name", pipeline.Name)
				r.emitEventf(pipeline, corev1.EventTypeWarning, eventReasonDeleteFailed,
					"Failed to delete pipeline from Fleet Management: %v", err)
				return r.updateStatusError(ctx, pipeline, reasonDeleteFailed, err, outcome)
			}
		} else {
			log.Info("successfully deleted pipeline from Fleet Management", "namespace", pipeline.Namespace, "name", pipeline.Name)
			r.emitEventf(pipeline, corev1.EventTypeNormal, eventReasonDeleted,
				"Successfully deleted pipeline from Fleet Management (ID: %s)", pipeline.Status.ID)
		}
	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(pipeline, pipelineFinalizer)
	// Cache: Update() writes directly to the API server. Once finalizer is removed, the resource is deleted.
	// Reconcile: Finalizer removal is the final K8s API call in deletion. Once removed, the API
	// server garbage-collects the resource. No subsequent Get is needed.
	if err := r.Update(ctx, pipeline); err != nil {
		log.Error(err, "failed to remove finalizer", "namespace", pipeline.Namespace, "name", pipeline.Name)
		*outcome = reasonSyncFailed
		return ctrl.Result{}, err
	}

	*outcome = outcomeDeleted
	log.Info("removed finalizer, pipeline will be deleted", "namespace", pipeline.Namespace, "name", pipeline.Name)
	return ctrl.Result{}, nil
}

// buildUpsertRequest builds an UpsertPipelineRequest from a Pipeline CRD
func (r *PipelineReconciler) buildUpsertRequest(pipeline *fleetmanagementv1alpha1.Pipeline) *fleetclient.UpsertPipelineRequest {
	// Determine pipeline name
	pipelineName := pipeline.Spec.Name
	if pipelineName == "" {
		pipelineName = pipeline.Name
	}

	// Build the pipeline object
	fleetPipeline := &fleetclient.Pipeline{
		Name:       pipelineName,
		Contents:   pipeline.Spec.Contents,
		Matchers:   pipeline.Spec.Matchers,
		Enabled:    pipeline.Spec.GetEnabled(),
		ConfigType: pipeline.Spec.ConfigType.ToFleetAPI(),
	}

	// Add source if specified, otherwise default to Kubernetes
	if pipeline.Spec.Source != nil {
		fleetPipeline.Source = &fleetclient.Source{
			Type:      pipeline.Spec.Source.Type.ToFleetAPI(),
			Namespace: pipeline.Spec.Source.Namespace,
		}
	} else {
		// Default to Kubernetes source
		fleetPipeline.Source = &fleetclient.Source{
			Type:      fleetmanagementv1alpha1.SourceTypeKubernetes.ToFleetAPI(),
			Namespace: fmt.Sprintf("%s/%s", pipeline.Namespace, pipeline.Name),
		}
	}

	// Note: ID should NOT be included in UpsertPipeline requests.
	// The API uses pipeline name for idempotency and assigns/returns the ID.

	return &fleetclient.UpsertPipelineRequest{
		Pipeline:     fleetPipeline,
		ValidateOnly: false,
	}
}

// handleAPIError handles errors from Fleet Management API
// CRITICAL: Single-retry guard for 404 prevents infinite recursion by checking if ID is already empty
// outcome is set so the deferred Reconcile counter records the precise reason
// (RateLimited / Recreated / ValidationError / SyncFailed).
func (r *PipelineReconciler) handleAPIError(ctx context.Context, pipeline *fleetmanagementv1alpha1.Pipeline, err error, outcome *string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Check if it's a Fleet API error
	var apiErr *fleetclient.FleetAPIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusBadRequest:
			// Validation error - update status and don't retry immediately
			log.Info("validation error from Fleet Management API", "namespace", pipeline.Namespace, "name", pipeline.Name, "message", apiErr.Message)
			r.emitEventf(pipeline, corev1.EventTypeWarning, eventReasonValidationFail,
				"Fleet Management API validation failed: %s", apiErr.Message)
			return r.updateStatusError(ctx, pipeline, reasonValidationError, err, outcome)

		case http.StatusNotFound:
			// Pipeline was deleted externally
			// CRITICAL: Check if we've already tried to recreate (ID is empty) to prevent infinite recursion
			if pipeline.Status.ID == "" {
				// Already tried recreation and still getting 404
				log.Error(apiErr, "pipeline creation failed after external deletion detection", "namespace", pipeline.Namespace, "name", pipeline.Name)
				r.emitEvent(pipeline, corev1.EventTypeWarning, eventReasonSyncFailed,
					"Failed to recreate pipeline after external deletion")
				return r.updateStatusError(ctx, pipeline, reasonSyncFailed,
					fmt.Errorf("pipeline not found and recreation failed: %w", err), outcome)
			}

			// First detection - try to recreate inline (no recursion)
			log.Info("pipeline not found in Fleet Management, attempting recreation",
				"previousID", pipeline.Status.ID)
			r.emitEvent(pipeline, corev1.EventTypeWarning, eventReasonRecreated,
				"Pipeline was deleted externally, recreating in Fleet Management")

			// Clear ID and rebuild request
			pipeline.Status.ID = ""
			req := r.buildUpsertRequest(pipeline)

			// Try to create - if this fails, handleAPIError will handle it
			apiPipeline, err := r.FleetClient.UpsertPipeline(ctx, req)
			if err != nil {
				// Let handleAPIError classify the new error
				return r.handleAPIError(ctx, pipeline, err, outcome)
			}

			// Successfully recreated
			result, statusErr := r.updateStatusSuccess(ctx, pipeline, apiPipeline, outcome)
			// On a successful recreation we want the metric outcome to be
			// "Recreated" rather than the generic "Synced" so dashboards can
			// distinguish the external-deletion recovery path.
			if statusErr == nil {
				*outcome = outcomeRecreated
			}
			return result, statusErr

		case http.StatusTooManyRequests:
			// Rate limit - requeue with delay
			log.Info("rate limited by Fleet Management API, requeueing", "namespace", pipeline.Namespace, "name", pipeline.Name)
			r.emitEvent(pipeline, corev1.EventTypeWarning, eventReasonRateLimited,
				"Rate limited by Fleet Management API, will retry in 10 seconds")
			*outcome = outcomeRateLimited
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil

		default:
			// Other API errors - return for exponential backoff
			log.Error(err, "Fleet Management API error",
				"namespace", pipeline.Namespace,
				"name", pipeline.Name,
				"statusCode", apiErr.StatusCode,
				"operation", apiErr.Operation,
				"pipelineID", pipeline.Status.ID,
				"message", apiErr.Message)
			r.emitEventf(pipeline, corev1.EventTypeWarning, eventReasonSyncFailed,
				"Fleet Management API error (HTTP %d): %s", apiErr.StatusCode, apiErr.Message)
			return r.updateStatusError(ctx, pipeline, reasonSyncFailed, err, outcome)
		}
	}

	// Network or other errors - return for exponential backoff
	log.Error(err, "failed to sync with Fleet Management", "namespace", pipeline.Namespace, "name", pipeline.Name)
	r.emitEventf(pipeline, corev1.EventTypeWarning, eventReasonSyncFailed,
		"Failed to sync with Fleet Management: %v", err)
	return r.updateStatusError(ctx, pipeline, reasonSyncFailed, err, outcome)
}

// updateStatusSuccess updates the status after successful sync. outcome is
// set to reasonSynced (or "NoOp" / "SyncFailed" on retryable conflict /
// non-conflict status update failure paths) so the deferred Reconcile
// counter records the precise reason.
func (r *PipelineReconciler) updateStatusSuccess(ctx context.Context, pipeline *fleetmanagementv1alpha1.Pipeline, apiPipeline *fleetclient.Pipeline, outcome *string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// OBS-03: record sync age using the previous UpdatedAt before overwriting it
	if pipeline.Status.UpdatedAt != nil && !pipeline.Status.UpdatedAt.IsZero() {
		fleetResourceSyncAge.WithLabelValues("Pipeline").
			Observe(time.Since(pipeline.Status.UpdatedAt.Time).Seconds())
	}

	// Determine if this was a create or update
	wasCreated := pipeline.Status.ID == ""
	isUpdate := pipeline.Status.ID != "" && pipeline.Status.ID == apiPipeline.ID

	// Update status fields
	pipeline.Status.ID = apiPipeline.ID
	pipeline.Status.ObservedGeneration = pipeline.Generation

	if apiPipeline.CreatedAt != nil {
		pipeline.Status.CreatedAt = &metav1.Time{Time: *apiPipeline.CreatedAt}
	}
	if apiPipeline.UpdatedAt != nil {
		pipeline.Status.UpdatedAt = &metav1.Time{Time: *apiPipeline.UpdatedAt}
	}

	// Capture previous Ready condition state to detect transitions
	oldCondition := meta.FindStatusCondition(pipeline.Status.Conditions, conditionTypeReady)
	wasReady := oldCondition != nil && oldCondition.Status == metav1.ConditionTrue

	// Set Ready condition
	meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
		Type:               conditionTypeReady,
		Status:             metav1.ConditionTrue,
		Reason:             reasonSynced,
		Message:            "Pipeline successfully synced to Fleet Management",
		ObservedGeneration: pipeline.Generation,
	})

	// Set Synced condition
	meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
		Type:               conditionTypeSynced,
		Status:             metav1.ConditionTrue,
		Reason:             reasonSynced,
		Message:            fmt.Sprintf("UpsertPipeline succeeded, ID: %s", apiPipeline.ID),
		ObservedGeneration: pipeline.Generation,
	})

	// Log condition state transitions for debugging timeline
	if !wasReady {
		previousReason := "None"
		if oldCondition != nil {
			previousReason = oldCondition.Reason
		}
		log.Info("pipeline condition transitioned to Ready",
			"namespace", pipeline.Namespace,
			"name", pipeline.Name,
			"previousReason", previousReason,
			"generation", pipeline.Generation)
	}

	// Update status
	// Cache: Status().Update() writes directly to the API server status subresource. The informer
	// cache is updated asynchronously via the watch. The ObservedGeneration check at the top of
	// Reconcile() handles the cache-lag case where the watch event re-triggers reconciliation
	// before the cache reflects the status update.
	// Reconcile: Status subresource update after successful Fleet Management sync. Uses
	// Status().Update() (not Update()) to avoid triggering a spec-change watch event.
	if err := r.Status().Update(ctx, pipeline); err != nil {
		if apierrors.IsConflict(err) {
			// Resource was modified, requeue to get fresh copy
			log.V(1).Info("status update conflict, requeueing", "namespace", pipeline.Namespace, "name", pipeline.Name)
			*outcome = outcomeNoOp
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "failed to update status", "namespace", pipeline.Namespace, "name", pipeline.Name)
		*outcome = reasonSyncFailed
		return ctrl.Result{}, err
	}

	// Emit appropriate event
	if wasCreated {
		r.emitEventf(pipeline, corev1.EventTypeNormal, eventReasonCreated,
			"Pipeline created in Fleet Management (ID: %s)", apiPipeline.ID)
	} else if isUpdate {
		r.emitEventf(pipeline, corev1.EventTypeNormal, eventReasonUpdated,
			"Pipeline updated in Fleet Management (ID: %s)", apiPipeline.ID)
	}

	r.emitEventf(pipeline, corev1.EventTypeNormal, eventReasonSynced,
		"Pipeline successfully synced to Fleet Management")

	*outcome = reasonSynced
	log.Info("successfully synced pipeline", "namespace", pipeline.Namespace, "name", pipeline.Name, "id", apiPipeline.ID, "generation", pipeline.Generation)
	return ctrl.Result{}, nil
}

// updateStatusError updates the status after an error
//
// Watch: (WATCH-03) Exponential backoff configuration
//
// This function is part of the controller's error handling strategy which uses four return patterns
// to correctly trigger exponential backoff via the workqueue rate limiter:
//
// 1. Returns error (this function, line 524): Triggers exponential backoff for transient API errors
//   - Used for Fleet Management API errors (network, 5xx, etc.)
//   - Controller-runtime increments the failure count and applies exponential delay (5ms -> 1000s)
//
// 2. Returns Requeue: true (line 434, 506): Requeues without failure count increment
//   - Used for status update conflicts (optimistic locking, cache is stale)
//   - Does NOT count as a failure, so no backoff penalty
//
// 3. Returns RequeueAfter (line 345): Timed requeue, bypasses workqueue rate limiter entirely
//   - Used for 429 rate limit errors (Fleet Management API)
//   - Fixed 10-second delay, no exponential increase
//
// 4. Returns nil error with empty Result (line 519): No requeue
//   - Used for validation errors (400 Bad Request)
//   - User must fix spec before retry, so no automatic requeue
//
// The combination of these four patterns correctly handles all error scenarios with appropriate
// retry strategies. The workqueue's ItemExponentialFailureRateLimiter handles pattern #1.
func (r *PipelineReconciler) updateStatusError(ctx context.Context, pipeline *fleetmanagementv1alpha1.Pipeline, reason string, originalErr error, outcome *string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	*outcome = reason

	retryable := shouldRetry(originalErr, reason)
	if !retryable {
		pipeline.Status.ObservedGeneration = pipeline.Generation
	}

	// Capture previous Ready condition state to detect transitions
	oldCondition := meta.FindStatusCondition(pipeline.Status.Conditions, conditionTypeReady)
	wasReady := oldCondition != nil && oldCondition.Status == metav1.ConditionTrue

	// CRITICAL: Format error message with actionable troubleshooting hints instead of raw error strings
	// Raw errors are not user-friendly in `kubectl describe` output
	formattedMessage := formatConditionMessage(reason, originalErr)

	// Set Ready condition to False
	meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
		Type:               conditionTypeReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            formattedMessage,
		ObservedGeneration: pipeline.Generation,
	})

	// Set Synced condition to False
	meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
		Type:               conditionTypeSynced,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            formattedMessage,
		ObservedGeneration: pipeline.Generation,
	})

	// Log condition state transitions for debugging timeline
	if wasReady {
		log.Error(originalErr, "pipeline condition transitioned to not Ready",
			"namespace", pipeline.Namespace,
			"name", pipeline.Name,
			"reason", reason,
			"generation", pipeline.Generation)
	}

	// CRITICAL: Try to update status, but preserve original error for exponential backoff
	// Cache: Status().Update() writes directly to API server. See comment in updateStatusSuccess() for cache-lag handling.
	// Reconcile: Status subresource update to record error condition. Uses Status().Update() to
	// avoid spec-change watch event. Original error is preserved for exponential backoff.
	if updateErr := r.Status().Update(ctx, pipeline); updateErr != nil {
		if apierrors.IsConflict(updateErr) {
			// Cache is stale, requeue to get fresh copy
			log.V(1).Info("status update conflict during error handling, requeueing", "namespace", pipeline.Namespace, "name", pipeline.Name)
			*outcome = outcomeNoOp
			return ctrl.Result{Requeue: true}, nil
		}
		// Log status update failure but continue to return original error
		log.Error(updateErr, "failed to update status after reconciliation error",
			"namespace", pipeline.Namespace,
			"name", pipeline.Name,
			"originalError", originalErr.Error(),
			"reason", reason)
	}

	// CRITICAL: Validation errors are permanent - user must fix spec before retry
	if !retryable {
		log.Info("validation error, not requeueing", "namespace", pipeline.Namespace, "name", pipeline.Name, "error", originalErr.Error())
		// Outcome counter (set above) is incremented by the deferred handler in Reconcile().
		return ctrl.Result{}, nil
	}

	// CRITICAL: Return original error to preserve exponential backoff
	// Controller-runtime needs the original error for proper exponential backoff calculation
	// Outcome counter (set above) is incremented by the deferred handler in Reconcile().
	return ctrl.Result{}, originalErr
}

// isPaused reports whether the Pipeline's reconciliation is suspended.
// spec.paused=true is overridden by the per-pipeline adopt annotation so
// individual pipelines can be promoted from ReadOnly to managed status without
// editing spec.
func isPaused(pipeline *fleetmanagementv1alpha1.Pipeline) bool {
	if !pipeline.Spec.Paused {
		return false
	}
	annotations := pipeline.GetAnnotations()
	if annotations != nil && annotations[fleetmanagementv1alpha1.PipelineImportModeAnnotation] == fleetmanagementv1alpha1.PipelineImportModeAnnotationAdopt {
		return false
	}
	return true
}

// SetupWithManager sets up the controller with the Manager.
func (r *PipelineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Cache: For(&Pipeline{}) establishes the informer watch that populates the cache with Pipeline
	// resources. This is the read side that enables cached Get() calls in Reconcile(). The watch
	// delivers add/update/delete events that trigger reconciliation.
	//
	// Watch: (WATCH-02) Workqueue rate limiter configuration
	//
	// This controller uses the controller-runtime default rate limiter (no explicit WithOptions).
	// The default in controller-runtime v0.23.0 is workqueue.DefaultTypedControllerRateLimiter which combines:
	//
	// - ItemExponentialFailureRateLimiter: base 5ms, max 1000s (16.6 min) - handles per-item backoff
	// - BucketRateLimiter: 10 qps, burst 100 - overall throughput cap
	//
	// This is APPROPRIATE for this operator because:
	//
	// - Single Pipeline CRD type with expected low-to-moderate volume
	// - Fleet Management API has its own rate limiter (3 req/s via golang.org/x/time/rate in fleetclient)
	// - The double rate limiting (workqueue + Fleet API client) provides defense in depth
	// - MaxConcurrentReconciles defaults to 1, which is correct for serial Fleet API access
	//
	// Production note: The default rate limiter provides exponential backoff for transient errors
	// while preventing thundering herd scenarios. No custom WithOptions configuration is needed.
	//
	// Watch: (WATCH-04) Watch storm prevention
	//
	// This controller has NO watch storm risk because:
	//
	// - Only watches Pipeline CRD via For() - no Owns(), no Watches() on secondary resources
	// - Status updates use Status().Update() (not Update()), so they do NOT trigger spec-change watch events
	// - Finalizer updates DO trigger a watch event, but the ObservedGeneration guard at line 182
	//   prevents redundant reconciliation (after finalizer is added, generation remains unchanged)
	// - No external event sources (no channel watches, no generic event handlers)
	//
	// The single For() watch combined with Status subresource usage guarantees no feedback loops.
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleetmanagementv1alpha1.Pipeline{}).
		Named("pipeline").
		Complete(r)
}
