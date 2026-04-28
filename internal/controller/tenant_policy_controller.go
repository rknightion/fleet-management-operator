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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
)

const (
	// TenantPolicy condition types. Ready is shared with the other CRDs;
	// Valid is specific to TenantPolicy because validation is the only
	// thing the reconciler can verify (it does not interact with Fleet).
	tenantPolicyConditionTypeValid = "Valid"

	// TenantPolicy condition reasons.
	tenantPolicyReasonValid      = "Valid"
	tenantPolicyReasonParseError = "ParseError"
)

// TenantPolicyReconciler reconciles a TenantPolicy object.
//
// TenantPolicy enforcement runs synchronously in the validating webhooks
// for Pipeline / RemoteAttributePolicy / ExternalAttributeSync, so by the
// time this reconciler observes a TenantPolicy the API server has already
// admitted it through the TenantPolicy's own validating webhook. The
// reconciler therefore performs three jobs:
//
//  1. Re-validate spec content (matcher syntax, namespace selector parse)
//     so a policy admitted with the webhook disabled still surfaces its
//     malformed-ness via status conditions.
//  2. Maintain status.boundSubjectCount and status.observedGeneration so
//     `kubectl get tenantpolicy` shows useful columns and so external
//     dashboards can detect drift between spec.generation and the most
//     recent observation.
//  3. Set Ready / Valid conditions for generic alerting.
//
// The reconciler does NOT call the Fleet Management API. There is no
// finalizer because there is no external state to clean up.
type TenantPolicyReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

var _ reconcile.Reconciler = &TenantPolicyReconciler{}

// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=tenantpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=tenantpolicies/status,verbs=get;update;patch

func (r *TenantPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("reconciling TenantPolicy", "name", req.Name)

	policy := &fleetmanagementv1alpha1.TenantPolicy{}
	if err := r.Get(ctx, req.NamespacedName, policy); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "failed to get TenantPolicy", "name", req.Name)
		return ctrl.Result{}, err
	}

	validationErr := validateTenantPolicySpec(policy)
	boundSubjects := int32(len(policy.Spec.Subjects))

	// Idempotency: if the spec has already been observed AND the derived
	// status fields would be identical, skip the write.
	if policy.Status.ObservedGeneration == policy.Generation &&
		policy.Status.BoundSubjectCount == boundSubjects &&
		conditionsAlreadyMatch(policy, validationErr) {
		log.V(1).Info("TenantPolicy already reconciled, skipping",
			"name", policy.Name, "generation", policy.Generation)
		return ctrl.Result{}, nil
	}

	policy.Status.ObservedGeneration = policy.Generation
	policy.Status.BoundSubjectCount = boundSubjects

	if validationErr != nil {
		message := validationErr.Error()
		meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
			Type:               tenantPolicyConditionTypeValid,
			Status:             metav1.ConditionFalse,
			Reason:             tenantPolicyReasonParseError,
			Message:            message,
			ObservedGeneration: policy.Generation,
		})
		meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
			Type:               conditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             tenantPolicyReasonParseError,
			Message:            message,
			ObservedGeneration: policy.Generation,
		})
	} else {
		meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
			Type:               tenantPolicyConditionTypeValid,
			Status:             metav1.ConditionTrue,
			Reason:             tenantPolicyReasonValid,
			Message:            "Spec parses cleanly; matchers and selectors are well-formed",
			ObservedGeneration: policy.Generation,
		})
		meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
			Type:               conditionTypeReady,
			Status:             metav1.ConditionTrue,
			Reason:             tenantPolicyReasonValid,
			Message:            "TenantPolicy is enforceable",
			ObservedGeneration: policy.Generation,
		})
	}

	if err := r.Status().Update(ctx, policy); err != nil {
		if apierrors.IsConflict(err) {
			log.V(1).Info("status update conflict, requeueing", "name", policy.Name)
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "failed to update TenantPolicy status", "name", policy.Name)
		return ctrl.Result{}, err
	}

	if validationErr != nil {
		log.Info("TenantPolicy is invalid",
			"name", policy.Name, "error", validationErr.Error())
	} else {
		log.Info("TenantPolicy reconciled",
			"name", policy.Name, "subjects", boundSubjects)
	}
	return ctrl.Result{}, nil
}

// validateTenantPolicySpec re-runs the parse checks the validating
// webhook applies. The first error is returned with field context so the
// status message identifies the offending entry.
func validateTenantPolicySpec(policy *fleetmanagementv1alpha1.TenantPolicy) error {
	if len(policy.Spec.Subjects) == 0 {
		return fmt.Errorf("spec.subjects must contain at least one entry")
	}
	if len(policy.Spec.RequiredMatchers) == 0 {
		return fmt.Errorf("spec.requiredMatchers must contain at least one entry")
	}
	for i, m := range policy.Spec.RequiredMatchers {
		if err := fleetmanagementv1alpha1.ValidateMatcherSyntax(m); err != nil {
			return fmt.Errorf("spec.requiredMatchers[%d] is malformed: %w", i, err)
		}
	}
	if policy.Spec.NamespaceSelector != nil {
		if _, err := metav1.LabelSelectorAsSelector(policy.Spec.NamespaceSelector); err != nil {
			return fmt.Errorf("spec.namespaceSelector is not a valid LabelSelector: %w", err)
		}
	}
	return nil
}

// conditionsAlreadyMatch returns true when the in-memory conditions reflect
// the desired (validity, ready) state at the current generation. Used to
// avoid spurious status writes on re-reconciliation of an unchanged spec.
func conditionsAlreadyMatch(policy *fleetmanagementv1alpha1.TenantPolicy, validationErr error) bool {
	want := metav1.ConditionTrue
	if validationErr != nil {
		want = metav1.ConditionFalse
	}
	for _, t := range []string{conditionTypeReady, tenantPolicyConditionTypeValid} {
		c := meta.FindStatusCondition(policy.Status.Conditions, t)
		if c == nil || c.Status != want || c.ObservedGeneration != policy.Generation {
			return false
		}
	}
	return true
}

// SetupWithManager registers the reconciler. Only TenantPolicy itself is
// watched — no cross-resource fan-out is needed because the reconciler
// derives its status entirely from spec.
func (r *TenantPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleetmanagementv1alpha1.TenantPolicy{}).
		Named("tenantpolicy").
		Complete(r)
}
