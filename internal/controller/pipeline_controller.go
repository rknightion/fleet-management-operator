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
func (r *PipelineReconciler) emitEventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
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

	// 1. Fetch the Pipeline resource
	pipeline := &fleetmanagementv1alpha1.Pipeline{}
	if err := r.Get(ctx, req.NamespacedName, pipeline); err != nil {
		if apierrors.IsNotFound(err) {
			// Pipeline was deleted
			log.Info("Pipeline not found, likely deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "failed to get Pipeline")
		return ctrl.Result{}, err
	}

	// 2. Handle deletion
	if !pipeline.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, pipeline)
	}

	// 3. Add finalizer if not present
	if !controllerutil.ContainsFinalizer(pipeline, pipelineFinalizer) {
		controllerutil.AddFinalizer(pipeline, pipelineFinalizer)
		if err := r.Update(ctx, pipeline); err != nil {
			log.Error(err, "failed to add finalizer")
			return ctrl.Result{}, err
		}
		log.Info("added finalizer")
		return ctrl.Result{}, nil
	}

	// 4. Check if reconciliation is needed (observedGeneration pattern)
	if pipeline.Status.ObservedGeneration == pipeline.Generation {
		log.V(1).Info("pipeline already reconciled, skipping", "generation", pipeline.Generation)
		return ctrl.Result{}, nil
	}

	// 5. Reconcile normal case
	return r.reconcileNormal(ctx, pipeline)
}

// reconcileNormal handles normal reconciliation (create/update)
func (r *PipelineReconciler) reconcileNormal(ctx context.Context, pipeline *fleetmanagementv1alpha1.Pipeline) (ctrl.Result, error) {
	// Build the upsert request
	req := r.buildUpsertRequest(pipeline)

	// Call Fleet Management API
	apiPipeline, err := r.FleetClient.UpsertPipeline(ctx, req)
	if err != nil {
		return r.handleAPIError(ctx, pipeline, err)
	}

	// Update status with successful sync
	return r.updateStatusSuccess(ctx, pipeline, apiPipeline)
}

// reconcileDelete handles pipeline deletion
func (r *PipelineReconciler) reconcileDelete(ctx context.Context, pipeline *fleetmanagementv1alpha1.Pipeline) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(pipeline, pipelineFinalizer) {
		// Finalizer already removed, nothing to do
		return ctrl.Result{}, nil
	}

	log.Info("deleting Pipeline from Fleet Management", "id", pipeline.Status.ID)

	// Delete from Fleet Management if we have an ID
	if pipeline.Status.ID != "" {
		if err := r.FleetClient.DeletePipeline(ctx, pipeline.Status.ID); err != nil {
			// Check if it's a 404 (already deleted)
			if apiErr, ok := err.(*fleetclient.FleetAPIError); ok && apiErr.StatusCode == http.StatusNotFound {
				log.Info("pipeline already deleted from Fleet Management")
				r.emitEvent(pipeline, corev1.EventTypeNormal, eventReasonDeleted,
					"Pipeline already deleted from Fleet Management")
			} else {
				log.Error(err, "failed to delete pipeline from Fleet Management")
				r.emitEventf(pipeline, corev1.EventTypeWarning, eventReasonDeleteFailed,
					"Failed to delete pipeline from Fleet Management: %v", err)
				return r.updateStatusError(ctx, pipeline, reasonDeleteFailed, err)
			}
		} else {
			log.Info("successfully deleted pipeline from Fleet Management")
			r.emitEventf(pipeline, corev1.EventTypeNormal, eventReasonDeleted,
				"Successfully deleted pipeline from Fleet Management (ID: %s)", pipeline.Status.ID)
		}
	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(pipeline, pipelineFinalizer)
	if err := r.Update(ctx, pipeline); err != nil {
		log.Error(err, "failed to remove finalizer")
		return ctrl.Result{}, err
	}

	log.Info("removed finalizer, pipeline will be deleted")
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
		Enabled:    pipeline.Spec.Enabled,
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
func (r *PipelineReconciler) handleAPIError(ctx context.Context, pipeline *fleetmanagementv1alpha1.Pipeline, err error) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Check if it's a Fleet API error
	if apiErr, ok := err.(*fleetclient.FleetAPIError); ok {
		switch apiErr.StatusCode {
		case http.StatusBadRequest:
			// Validation error - update status and don't retry immediately
			log.Info("validation error from Fleet Management API", "message", apiErr.Message)
			r.emitEventf(pipeline, corev1.EventTypeWarning, eventReasonValidationFail,
				"Fleet Management API validation failed: %s", apiErr.Message)
			return r.updateStatusError(ctx, pipeline, reasonValidationError, err)

		case http.StatusNotFound:
			// Pipeline was deleted externally, recreate it
			log.Info("pipeline not found in Fleet Management, will recreate")
			r.emitEvent(pipeline, corev1.EventTypeWarning, eventReasonRecreated,
				"Pipeline was deleted externally, recreating in Fleet Management")
			pipeline.Status.ID = "" // Clear the ID so it's created fresh
			return r.reconcileNormal(ctx, pipeline)

		case http.StatusTooManyRequests:
			// Rate limit - requeue with delay
			log.Info("rate limited by Fleet Management API, requeueing")
			r.emitEvent(pipeline, corev1.EventTypeWarning, eventReasonRateLimited,
				"Rate limited by Fleet Management API, will retry in 10 seconds")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil

		default:
			// Other API errors - return for exponential backoff
			log.Error(err, "Fleet Management API error",
				"statusCode", apiErr.StatusCode,
				"operation", apiErr.Operation,
				"pipelineID", pipeline.Status.ID,
				"message", apiErr.Message)
			r.emitEventf(pipeline, corev1.EventTypeWarning, eventReasonSyncFailed,
				"Fleet Management API error (HTTP %d): %s", apiErr.StatusCode, apiErr.Message)
			return r.updateStatusError(ctx, pipeline, reasonSyncFailed, err)
		}
	}

	// Network or other errors - return for exponential backoff
	log.Error(err, "failed to sync with Fleet Management")
	r.emitEventf(pipeline, corev1.EventTypeWarning, eventReasonSyncFailed,
		"Failed to sync with Fleet Management: %v", err)
	return r.updateStatusError(ctx, pipeline, reasonSyncFailed, err)
}

// updateStatusSuccess updates the status after successful sync
func (r *PipelineReconciler) updateStatusSuccess(ctx context.Context, pipeline *fleetmanagementv1alpha1.Pipeline, apiPipeline *fleetclient.Pipeline) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

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

	// Update status
	if err := r.Status().Update(ctx, pipeline); err != nil {
		if apierrors.IsConflict(err) {
			// Resource was modified, requeue to get fresh copy
			log.V(1).Info("status update conflict, requeueing")
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "failed to update status")
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

	log.Info("successfully synced pipeline", "id", apiPipeline.ID, "generation", pipeline.Generation)
	return ctrl.Result{}, nil
}

// updateStatusError updates the status after an error
func (r *PipelineReconciler) updateStatusError(ctx context.Context, pipeline *fleetmanagementv1alpha1.Pipeline, reason string, err error) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Update observedGeneration to indicate we tried
	pipeline.Status.ObservedGeneration = pipeline.Generation

	// Set Ready condition to False
	meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
		Type:               conditionTypeReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            err.Error(),
		ObservedGeneration: pipeline.Generation,
	})

	// Set Synced condition to False
	meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
		Type:               conditionTypeSynced,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            err.Error(),
		ObservedGeneration: pipeline.Generation,
	})

	// Update status
	if updateErr := r.Status().Update(ctx, pipeline); updateErr != nil {
		if apierrors.IsConflict(updateErr) {
			log.V(1).Info("status update conflict, requeueing")
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{}, updateErr
	}

	// For validation errors, don't retry immediately
	if reason == reasonValidationError {
		log.Info("validation error, not requeueing", "error", err.Error())
		return ctrl.Result{}, nil
	}

	// For other errors, return error for exponential backoff
	return ctrl.Result{}, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *PipelineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleetmanagementv1alpha1.Pipeline{}).
		Named("pipeline").
		Complete(r)
}
