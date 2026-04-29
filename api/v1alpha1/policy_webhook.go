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

	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var remoteattributepolicylog = logf.Log.WithName("remoteattributepolicy-resource")

// policyMaxMatcherLength mirrors the Pipeline webhook's per-matcher 200
// character ceiling. Keeping it as a named constant in this file documents
// intent and makes the bound easy to find from policy-related code paths.
const policyMaxMatcherLength = 200

// SetupRemoteAttributePolicyWebhookWithManager registers the
// RemoteAttributePolicy validating webhook with the manager. Pass a
// non-nil MatcherChecker to layer tenant-policy enforcement on top of the
// spec validation; pass nil to skip the tenant check.
func SetupRemoteAttributePolicyWebhookWithManager(mgr ctrl.Manager, checker MatcherChecker) error {
	return ctrl.NewWebhookManagedBy(mgr, &RemoteAttributePolicy{}).
		WithValidator(&remoteAttributePolicyValidator{checker: checker}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-fleetmanagement-grafana-com-v1alpha1-remoteattributepolicy,mutating=false,failurePolicy=fail,sideEffects=None,groups=fleetmanagement.grafana.com,resources=remoteattributepolicies,verbs=create;update,versions=v1alpha1,name=vremoteattributepolicy.kb.io,admissionReviewVersions=v1,timeoutSeconds=5

// remoteAttributePolicyValidator is the production webhook validator. It
// runs the type's spec validation and, when checker is non-nil, layers
// the tenant policy check on top.
type remoteAttributePolicyValidator struct {
	checker MatcherChecker
}

var _ admission.Validator[*RemoteAttributePolicy] = &remoteAttributePolicyValidator{}

// ValidateCreate implements admission.Validator.
func (v *remoteAttributePolicyValidator) ValidateCreate(ctx context.Context, obj *RemoteAttributePolicy) (admission.Warnings, error) {
	remoteattributepolicylog.Info("validate create", "name", obj.Name)
	if err := obj.validateRemoteAttributePolicy(); err != nil {
		return nil, err
	}
	if err := runTenantChecks(ctx, v.checker, obj.Namespace, obj.Spec.Selector.Matchers, obj.Spec.Selector.CollectorIDs); err != nil {
		return nil, err
	}
	return nil, nil
}

// ValidateUpdate implements admission.Validator.
func (v *remoteAttributePolicyValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *RemoteAttributePolicy) (admission.Warnings, error) {
	remoteattributepolicylog.Info("validate update", "name", newObj.Name)
	// Priority and selector are mutable; re-run the full validation suite.
	if err := newObj.validateRemoteAttributePolicy(); err != nil {
		return nil, err
	}
	if err := runTenantChecks(ctx, v.checker, newObj.Namespace, newObj.Spec.Selector.Matchers, newObj.Spec.Selector.CollectorIDs); err != nil {
		return nil, err
	}
	return nil, nil
}

// ValidateDelete implements admission.Validator.
func (v *remoteAttributePolicyValidator) ValidateDelete(ctx context.Context, obj *RemoteAttributePolicy) (admission.Warnings, error) {
	return nil, nil
}

// validateRemoteAttributePolicy performs comprehensive validation of the
// RemoteAttributePolicy resource.
func (r *RemoteAttributePolicy) validateRemoteAttributePolicy() error {
	// 1. Attributes must be non-empty (a policy with no attributes is meaningless).
	// 2. Reserved-prefix, empty-key, value length, defense-in-depth count.
	if err := r.validateAttributes(); err != nil {
		return err
	}

	// 3. Selector must be non-empty (matchers OR collectorIDs must be set).
	// 4. Matchers must satisfy Prometheus syntax + 200-char cap.
	// 5. CollectorIDs entries must be non-empty.
	if err := r.validateSelector(); err != nil {
		return err
	}

	return nil
}

// validateAttributes enforces:
//   - spec.attributes must be non-empty
//   - reserved "collector." prefix is rejected
//   - empty-string keys are rejected
//   - values are at most collectorMaxAttributeValueLength characters
//   - defense-in-depth count check against collectorMaxRemoteAttributes
func (r *RemoteAttributePolicy) validateAttributes() error {
	attrs := r.Spec.Attributes
	if len(attrs) == 0 {
		return fmt.Errorf("spec.attributes must contain at least one entry; a Policy with no attributes has no effect")
	}

	// Defense-in-depth: schema MaxProperties=100 should already enforce this.
	if len(attrs) > collectorMaxRemoteAttributes {
		return fmt.Errorf(
			"spec.attributes has %d entries which exceeds the maximum of %d",
			len(attrs), collectorMaxRemoteAttributes)
	}

	for key, value := range attrs {
		if key == "" {
			return fmt.Errorf("spec.attributes contains an empty key")
		}

		if strings.HasPrefix(key, collectorReservedAttributePrefix) {
			return fmt.Errorf(
				"spec.attributes key %q uses reserved prefix %q which is reserved by Fleet Management for collector-reported attributes",
				key, collectorReservedAttributePrefix)
		}

		if len(value) > collectorMaxAttributeValueLength {
			return fmt.Errorf(
				"spec.attributes[%q] value length %d exceeds the maximum of %d characters",
				key, len(value), collectorMaxAttributeValueLength)
		}
	}

	return nil
}

// validateSelector enforces:
//   - selector must have at least one matcher or one collector ID
//   - matchers honor the 200-char cap and the Prometheus matcher syntax
//   - collectorIDs entries must be non-empty
func (r *RemoteAttributePolicy) validateSelector() error {
	sel := r.Spec.Selector

	if len(sel.Matchers) == 0 && len(sel.CollectorIDs) == 0 {
		return fmt.Errorf(
			"spec.selector must specify at least one of matchers or collectorIDs; an empty selector matches no collectors")
	}

	for i, matcher := range sel.Matchers {
		if len(matcher) > policyMaxMatcherLength {
			return fmt.Errorf(
				"spec.selector.matchers[%d] exceeds %d character limit (length: %d): %s",
				i, policyMaxMatcherLength, len(matcher), matcher)
		}

		if err := validateMatcherSyntax(matcher); err != nil {
			return fmt.Errorf("spec.selector.matchers[%d] has invalid syntax: %w", i, err)
		}
	}

	for i, id := range sel.CollectorIDs {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("spec.selector.collectorIDs[%d] is empty; collector IDs must be non-empty", i)
		}
	}

	return nil
}
