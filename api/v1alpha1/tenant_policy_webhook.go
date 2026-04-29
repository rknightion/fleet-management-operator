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

package v1alpha1

import (
	"context"
	"fmt"
	"strings"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var tenantpolicylog = logf.Log.WithName("tenantpolicy-resource")

// tenantPolicyMaxMatcherLength mirrors Pipeline/Policy webhooks. Keeping
// the cap consistent across the suite means a `requiredMatcher` is always
// applicable as a Pipeline matcher.
const tenantPolicyMaxMatcherLength = 200

// SetupTenantPolicyWebhookWithManager registers the TenantPolicy
// validating webhook with the manager.
func SetupTenantPolicyWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &TenantPolicy{}).
		WithValidator(&TenantPolicy{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-fleetmanagement-grafana-com-v1alpha1-tenantpolicy,mutating=false,failurePolicy=fail,sideEffects=None,groups=fleetmanagement.grafana.com,resources=tenantpolicies,verbs=create;update,versions=v1alpha1,name=vtenantpolicy.kb.io,admissionReviewVersions=v1,timeoutSeconds=5

// ValidateCreate implements admission.Validator.
func (r *TenantPolicy) ValidateCreate(ctx context.Context, obj *TenantPolicy) (admission.Warnings, error) {
	tenantpolicylog.Info("validate create", "name", obj.Name)
	if err := obj.validateTenantPolicy(); err != nil {
		return nil, err
	}
	return obj.tenantPolicyWarnings(), nil
}

// ValidateUpdate implements admission.Validator.
func (r *TenantPolicy) ValidateUpdate(ctx context.Context, oldObj, newObj *TenantPolicy) (admission.Warnings, error) {
	tenantpolicylog.Info("validate update", "name", newObj.Name)
	if err := newObj.validateTenantPolicy(); err != nil {
		return nil, err
	}
	return newObj.tenantPolicyWarnings(), nil
}

// ValidateDelete implements admission.Validator.
func (r *TenantPolicy) ValidateDelete(ctx context.Context, obj *TenantPolicy) (admission.Warnings, error) {
	tenantpolicylog.Info("validate delete", "name", obj.Name)
	return nil, nil
}

// validateTenantPolicy enforces:
//   - spec.subjects non-empty; each entry has a recognized Kind and the
//     Kind-specific Namespace requirement (ServiceAccount needs Namespace).
//   - spec.requiredMatchers non-empty; each matcher is within the 200-char
//     cap and parses via the existing Prometheus matcher syntax check.
//   - spec.namespaceSelector, when set, parses as a valid LabelSelector.
func (r *TenantPolicy) validateTenantPolicy() error {
	if err := r.validateSubjects(); err != nil {
		return err
	}
	if err := r.validateRequiredMatchers(); err != nil {
		return err
	}
	if err := r.validateNamespaceSelector(); err != nil {
		return err
	}
	return nil
}

func (r *TenantPolicy) validateSubjects() error {
	if len(r.Spec.Subjects) == 0 {
		return fmt.Errorf("spec.subjects must contain at least one entry; a TenantPolicy with no subjects matches no requests")
	}

	for i, s := range r.Spec.Subjects {
		switch s.Kind {
		case rbacv1.UserKind, rbacv1.GroupKind:
			if strings.TrimSpace(s.Name) == "" {
				return fmt.Errorf("spec.subjects[%d].name must be set for Kind=%s", i, s.Kind)
			}
			if s.Namespace != "" {
				return fmt.Errorf(
					"spec.subjects[%d].namespace must be empty for Kind=%s; namespaces are only meaningful for ServiceAccount subjects",
					i, s.Kind)
			}

		case rbacv1.ServiceAccountKind:
			if strings.TrimSpace(s.Name) == "" {
				return fmt.Errorf("spec.subjects[%d].name must be set for Kind=ServiceAccount", i)
			}
			if strings.TrimSpace(s.Namespace) == "" {
				return fmt.Errorf("spec.subjects[%d].namespace is required for Kind=ServiceAccount", i)
			}

		default:
			return fmt.Errorf(
				"spec.subjects[%d].kind %q is invalid; must be one of %s, %s, %s",
				i, s.Kind, rbacv1.UserKind, rbacv1.GroupKind, rbacv1.ServiceAccountKind)
		}
	}

	return nil
}

func (r *TenantPolicy) validateRequiredMatchers() error {
	if len(r.Spec.RequiredMatchers) == 0 {
		return fmt.Errorf("spec.requiredMatchers must contain at least one entry; a TenantPolicy with no required matchers has no effect")
	}

	for i, m := range r.Spec.RequiredMatchers {
		if len(m) > tenantPolicyMaxMatcherLength {
			return fmt.Errorf(
				"spec.requiredMatchers[%d] exceeds %d character limit (length: %d): %s",
				i, tenantPolicyMaxMatcherLength, len(m), m)
		}
		if err := validateMatcherSyntax(m); err != nil {
			return fmt.Errorf("spec.requiredMatchers[%d] has invalid syntax: %w", i, err)
		}
	}

	return nil
}

func (r *TenantPolicy) validateNamespaceSelector() error {
	if r.Spec.NamespaceSelector == nil {
		return nil
	}
	if _, err := metav1.LabelSelectorAsSelector(r.Spec.NamespaceSelector); err != nil {
		return fmt.Errorf("spec.namespaceSelector is not a valid LabelSelector: %w", err)
	}
	return nil
}

// tenantPolicyWarnings returns admission warnings that do not block
// creation but should surface in `kubectl apply` / `kubectl create` output.
//
// Currently emits one warning: when spec.namespaceSelector is set but
// empty (no MatchLabels and no MatchExpressions), the selector matches
// every namespace — which is functionally identical to omitting the
// field. The author probably intended to scope the policy and forgot to
// fill in labels; surface it so they can correct or confirm.
func (r *TenantPolicy) tenantPolicyWarnings() admission.Warnings {
	var warnings admission.Warnings
	if sel := r.Spec.NamespaceSelector; sel != nil &&
		len(sel.MatchLabels) == 0 && len(sel.MatchExpressions) == 0 {
		warnings = append(warnings,
			"spec.namespaceSelector is empty; this policy applies to every namespace "+
				"(omit the field to make this explicit)")
	}
	return warnings
}
