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
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// pipelinediscoverylog is for logging in this package.
var pipelinediscoverylog = logf.Log.WithName("pipelinediscovery-resource")

// pipelineDiscoveryMinPollInterval is the hard floor on spec.pollInterval.
// The shared Fleet Management rate limiter sits at 3 req/s; we cap discovery
// polls so a misconfigured short interval cannot starve the pipeline
// reconciliation path.
const pipelineDiscoveryMinPollInterval = 1 * time.Minute

// PipelineDiscoveryValidator is the webhook validator for PipelineDiscovery.
// It is constructed in cmd/main.go and wired via SetupWebhookWithManager.
//
// reviewer closes the cross-namespace confused-deputy escalation: when set and
// spec.targetNamespace names a foreign namespace, the requester must itself be
// authorized to create Pipelines there (nil when
// --enforce-cross-namespace-discovery-authz is off). It nil-checks before use,
// mirroring the other CRD webhooks.
//
// PipelineDiscovery carries NO MatcherChecker: its selector filters by
// configType / enabled, not matchers, so matcher-based TenantPolicy
// enforcement does not apply (see runDiscoveryAuthz and docs/tenant-policy.md).
type PipelineDiscoveryValidator struct {
	reviewer SubjectAccessReviewer
}

var _ admission.Validator[*PipelineDiscovery] = &PipelineDiscoveryValidator{}

// NewPipelineDiscoveryValidator builds a PipelineDiscoveryValidator with the
// given SubjectAccessReviewer. Pass nil to disable cross-namespace
// authorization enforcement (default-allow, back-compat).
func NewPipelineDiscoveryValidator(reviewer SubjectAccessReviewer) *PipelineDiscoveryValidator {
	return &PipelineDiscoveryValidator{reviewer: reviewer}
}

// SetupWebhookWithManager registers the PipelineDiscovery webhook with the
// manager.
func (v *PipelineDiscoveryValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &PipelineDiscovery{}).
		WithValidator(v).
		Complete()
}

// +kubebuilder:webhook:path=/validate-fleetmanagement-grafana-com-v1alpha1-pipelinediscovery,mutating=false,failurePolicy=fail,sideEffects=None,groups=fleetmanagement.grafana.com,resources=pipelinediscoveries,verbs=create;update,versions=v1alpha1,name=vpipelinediscovery.kb.io,admissionReviewVersions=v1,timeoutSeconds=5

// ValidateCreate implements admission.Validator.
func (v *PipelineDiscoveryValidator) ValidateCreate(ctx context.Context, obj *PipelineDiscovery) (admission.Warnings, error) {
	pipelinediscoverylog.Info("validate create", "name", obj.Name)
	warnings, err := obj.validatePipelineDiscovery()
	if err != nil {
		return warnings, err
	}
	if err := v.runDiscoveryAuthz(ctx, obj); err != nil {
		return warnings, err
	}
	return warnings, nil
}

// ValidateUpdate implements admission.Validator.
func (v *PipelineDiscoveryValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *PipelineDiscovery) (admission.Warnings, error) {
	pipelinediscoverylog.Info("validate update", "name", newObj.Name)
	// All fields are mutable; re-run the full validation suite against the
	// incoming newObj.
	warnings, err := newObj.validatePipelineDiscovery()
	if err != nil {
		return warnings, err
	}
	if err := v.runDiscoveryAuthz(ctx, newObj); err != nil {
		return warnings, err
	}
	return warnings, nil
}

// runDiscoveryAuthz runs the cross-namespace authorization check on top of
// spec validation.
//
// TenantPolicy coverage note: unlike Pipeline / RemoteAttributePolicy /
// ExternalAttributeSync (and unlike CollectorDiscovery), PipelineDiscovery's
// selector (PipelineDiscoverySelector) filters by configType / enabled — it
// has NO matcher or collectorID scope, so matcher-based TenantPolicy
// enforcement does not apply. Tenant protection for PipelineDiscovery reduces
// to the cross-namespace SubjectAccessReview below: a user can only mirror
// Fleet pipelines into a namespace they are themselves authorized to write
// Pipelines in. See docs/tenant-policy.md for this documented residual gap.
func (v *PipelineDiscoveryValidator) runDiscoveryAuthz(ctx context.Context, obj *PipelineDiscovery) error {
	target := strings.TrimSpace(obj.Spec.TargetNamespace)
	if target == "" || target == obj.Namespace {
		// Same-namespace (or default-to-own) writes are already governed by
		// the RBAC that allowed the user to create this PipelineDiscovery.
		return nil
	}
	return checkCrossNamespaceCreate(ctx, v.reviewer, target, "pipelines")
}

// ValidateDelete implements admission.Validator.
// No validation required on delete; PipelineDiscovery has no finalizer.
func (v *PipelineDiscoveryValidator) ValidateDelete(ctx context.Context, obj *PipelineDiscovery) (admission.Warnings, error) {
	pipelinediscoverylog.Info("validate delete", "name", obj.Name)
	return nil, nil
}

// validatePipelineDiscovery performs comprehensive validation of the
// PipelineDiscovery resource.
func (r *PipelineDiscovery) validatePipelineDiscovery() (admission.Warnings, error) {
	if err := r.validatePipelineDiscoveryPollInterval(); err != nil {
		return nil, err
	}

	if err := r.validatePipelineDiscoverySelector(); err != nil {
		return nil, err
	}

	if err := r.validatePipelineDiscoveryTargetNamespace(); err != nil {
		return nil, err
	}

	if err := r.validatePipelineDiscoveryImportMode(); err != nil {
		return nil, err
	}

	if err := r.validatePipelineDiscoveryPolicy(); err != nil {
		return nil, err
	}

	var warnings admission.Warnings
	// Warn on empty selector (match-all). Not a hard error — it is a valid
	// configuration — but an unscoped ListPipelines call can be expensive on
	// large fleets and may approach the Fleet Management API's timeout.
	// Shard instead via multiple PipelineDiscovery resources with disjoint
	// configType or enabled filters.
	if r.Spec.Selector.ConfigType == nil && r.Spec.Selector.Enabled == nil {
		warnings = append(warnings,
			"spec.selector is empty: this PipelineDiscovery will import all Fleet pipelines. "+
				"For fleets with many pipelines, consider scoping via spec.selector.configType "+
				"or spec.selector.enabled to avoid single large-response timeouts.")
	}
	return warnings, nil
}

// validatePipelineDiscoveryPollInterval enforces:
//   - non-empty (after trim); empty falls through the schema default but
//     defense-in-depth check covers anyone bypassing it.
//   - parses as a Go duration.
//   - is at least pipelineDiscoveryMinPollInterval to protect the shared rate
//     limiter.
func (r *PipelineDiscovery) validatePipelineDiscoveryPollInterval() error {
	raw := strings.TrimSpace(r.Spec.PollInterval)
	if raw == "" {
		// Schema default ("5m") makes empty unusual but valid. Treat as
		// the default rather than rejecting.
		return nil
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("spec.pollInterval %q is not a valid Go duration (e.g. \"5m\"): %w", raw, err)
	}

	if d < pipelineDiscoveryMinPollInterval {
		return fmt.Errorf(
			"spec.pollInterval %q (%s) is below the minimum of %s; shorter intervals would starve the shared Fleet Management rate limiter",
			raw, d, pipelineDiscoveryMinPollInterval)
	}

	return nil
}

// validatePipelineDiscoverySelector enforces:
//   - configType (when set) must be a valid ConfigType value.
//
// An empty selector is allowed — it means "import every pipeline". This is
// a legitimate configuration; the cost is documented by the warning emitted
// in validatePipelineDiscovery.
func (r *PipelineDiscovery) validatePipelineDiscoverySelector() error {
	if r.Spec.Selector.ConfigType != nil {
		ct := *r.Spec.Selector.ConfigType
		switch ct {
		case ConfigTypeAlloy, ConfigTypeOpenTelemetryCollector:
			// valid
		default:
			return fmt.Errorf(
				"spec.selector.configType %q is invalid; must be one of Alloy, OpenTelemetryCollector",
				ct)
		}
	}
	return nil
}

// validatePipelineDiscoveryTargetNamespace enforces that, when set, the
// namespace is a valid DNS-1123 label (k8s namespace name format). Empty
// means "use this PipelineDiscovery's own namespace" and is always allowed.
func (r *PipelineDiscovery) validatePipelineDiscoveryTargetNamespace() error {
	ns := strings.TrimSpace(r.Spec.TargetNamespace)
	if ns == "" {
		return nil
	}

	if errs := validation.IsDNS1123Label(ns); len(errs) > 0 {
		return fmt.Errorf("spec.targetNamespace %q is not a valid Kubernetes namespace name: %s", ns, strings.Join(errs, "; "))
	}

	return nil
}

// validatePipelineDiscoveryImportMode enforces (defense-in-depth — schema
// enums should already enforce these) that importMode is empty (defaults to
// Adopt), Adopt, or ReadOnly.
func (r *PipelineDiscovery) validatePipelineDiscoveryImportMode() error {
	switch r.Spec.ImportMode {
	case "", PipelineDiscoveryImportModeAdopt, PipelineDiscoveryImportModeReadOnly:
		// valid
	default:
		return fmt.Errorf(
			"spec.importMode %q is invalid; must be one of Adopt, ReadOnly",
			r.Spec.ImportMode)
	}
	return nil
}

// validatePipelineDiscoveryPolicy enforces (defense-in-depth — schema enums
// should already enforce these) that onPipelineRemoved is empty (defaults to
// Keep), Keep, or Delete.
func (r *PipelineDiscovery) validatePipelineDiscoveryPolicy() error {
	switch r.Spec.Policy.OnPipelineRemoved {
	case "", PipelineDiscoveryOnRemovedKeep, PipelineDiscoveryOnRemovedDelete:
		// valid
	default:
		return fmt.Errorf(
			"spec.policy.onPipelineRemoved %q is invalid; must be one of Keep, Delete",
			r.Spec.Policy.OnPipelineRemoved)
	}
	return nil
}
