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

// log is for logging in this package.
var collectordiscoverylog = logf.Log.WithName("collectordiscovery-resource")

// discoveryMinPollInterval is the hard floor on spec.pollInterval. The
// shared Fleet Management rate limiter sits at 3 req/s; we cap discovery
// polls so a misconfigured short interval cannot starve the
// BulkUpdateCollectors path that the Collector reconciler depends on.
const discoveryMinPollInterval = 1 * time.Minute

// SetupCollectorDiscoveryWebhookWithManager registers the CollectorDiscovery
// webhook with the manager.
//
// checker layers matcher-based TenantPolicy enforcement on top of spec
// validation (nil when --enable-tenant-policy-enforcement is off). reviewer
// closes the cross-namespace confused-deputy escalation (nil when
// --enforce-cross-namespace-discovery-authz is off).
func SetupCollectorDiscoveryWebhookWithManager(mgr ctrl.Manager, checker MatcherChecker, reviewer SubjectAccessReviewer) error {
	return ctrl.NewWebhookManagedBy(mgr, &CollectorDiscovery{}).
		WithValidator(&collectorDiscoveryValidator{checker: checker, reviewer: reviewer}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-fleetmanagement-grafana-com-v1alpha1-collectordiscovery,mutating=false,failurePolicy=fail,sideEffects=None,groups=fleetmanagement.grafana.com,resources=collectordiscoveries,verbs=create;update,versions=v1alpha1,name=vcollectordiscovery.kb.io,admissionReviewVersions=v1,timeoutSeconds=5

// collectorDiscoveryValidator is the production webhook validator. It runs the
// type's spec validation and, when its dependencies are non-nil, layers
// matcher-based TenantPolicy enforcement and the cross-namespace
// SubjectAccessReview on top.
type collectorDiscoveryValidator struct {
	checker  MatcherChecker
	reviewer SubjectAccessReviewer
}

var _ admission.Validator[*CollectorDiscovery] = &collectorDiscoveryValidator{}

// ValidateCreate implements admission.Validator.
func (v *collectorDiscoveryValidator) ValidateCreate(ctx context.Context, obj *CollectorDiscovery) (admission.Warnings, error) {
	collectordiscoverylog.Info("validate create", "name", obj.Name)
	warnings, err := obj.validateCollectorDiscovery()
	if err != nil {
		return warnings, err
	}
	if err := v.runDiscoveryAuthz(ctx, obj); err != nil {
		return warnings, err
	}
	return warnings, nil
}

// ValidateUpdate implements admission.Validator.
func (v *collectorDiscoveryValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *CollectorDiscovery) (admission.Warnings, error) {
	collectordiscoverylog.Info("validate update", "name", newObj.Name)
	// All fields are mutable; re-run the full validation suite against the
	// incoming newObj.
	warnings, err := newObj.validateCollectorDiscovery()
	if err != nil {
		return warnings, err
	}
	if err := v.runDiscoveryAuthz(ctx, newObj); err != nil {
		return warnings, err
	}
	return warnings, nil
}

// ValidateDelete implements admission.Validator.
func (v *collectorDiscoveryValidator) ValidateDelete(ctx context.Context, obj *CollectorDiscovery) (admission.Warnings, error) {
	collectordiscoverylog.Info("validate delete", "name", obj.Name)
	// No validation needed for delete.
	return nil, nil
}

// runDiscoveryAuthz layers the two authorization checks on top of spec
// validation:
//
//   - matcher-based TenantPolicy enforcement (when checker is non-nil) over
//     the discovery selector, reusing the shared runTenantChecks helper.
//     CollectorDiscovery's selector is a PolicySelector (matchers AND'd, OR'd
//     with collectorIDs), so the same matcher scoping that governs Pipeline /
//     RemoteAttributePolicy / ExternalAttributeSync applies here.
//   - cross-namespace SubjectAccessReview (when reviewer is non-nil) for a
//     spec.targetNamespace that differs from the CR's own namespace, closing
//     the confused-deputy escalation where a user makes the operator create
//     Collectors in a namespace they cannot write to themselves.
func (v *collectorDiscoveryValidator) runDiscoveryAuthz(ctx context.Context, obj *CollectorDiscovery) error {
	if err := runTenantChecks(ctx, v.checker, obj.Namespace, obj.Spec.Selector.Matchers, obj.Spec.Selector.CollectorIDs); err != nil {
		return err
	}

	target := strings.TrimSpace(obj.Spec.TargetNamespace)
	if target == "" || target == obj.Namespace {
		// Same-namespace (or default-to-own) writes are already governed by
		// the RBAC that allowed the user to create this CollectorDiscovery.
		return nil
	}
	return checkCrossNamespaceCreate(ctx, v.reviewer, target, "collectors")
}

// validateCollectorDiscovery performs comprehensive validation of the
// CollectorDiscovery resource.
func (r *CollectorDiscovery) validateCollectorDiscovery() (admission.Warnings, error) {
	if err := r.validatePollInterval(); err != nil {
		return nil, err
	}

	if err := r.validateDiscoverySelector(); err != nil {
		return nil, err
	}

	if err := r.validateTargetNamespace(); err != nil {
		return nil, err
	}

	if err := r.validatePolicy(); err != nil {
		return nil, err
	}

	var warnings admission.Warnings
	// Warn on empty selector (match-all). Not a hard error — it is a valid
	// configuration — but at fleets >1000 collectors a single ListCollectors
	// response can approach 30 MB and may exceed the Fleet Management API's
	// server-side timeout. Shard instead via disjoint-matcher CRs.
	if len(r.Spec.Selector.Matchers) == 0 && len(r.Spec.Selector.CollectorIDs) == 0 {
		warnings = append(warnings,
			"spec.selector is empty: this CollectorDiscovery will mirror all Fleet collectors. "+
				"For fleets with more than 1000 collectors, shard into multiple CollectorDiscovery "+
				"resources with disjoint matchers to avoid single large-response timeouts (see values.yaml).")
	}
	return warnings, nil
}

// validatePollInterval enforces:
//   - non-empty (after trim); empty falls through the schema default but
//     defense-in-depth check covers anyone bypassing it.
//   - parses as a Go duration.
//   - is at least discoveryMinPollInterval to protect the shared rate
//     limiter.
func (r *CollectorDiscovery) validatePollInterval() error {
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

	if d < discoveryMinPollInterval {
		return fmt.Errorf(
			"spec.pollInterval %q (%s) is below the minimum of %s; shorter intervals would starve the shared Fleet Management rate limiter",
			raw, d, discoveryMinPollInterval)
	}

	return nil
}

// validateDiscoverySelector enforces:
//   - matchers (when present) honor the 200-char cap and the Prometheus
//     matcher syntax;
//   - collectorIDs entries must be non-empty.
//
// Unlike RemoteAttributePolicy / ExternalAttributeSync, the selector
// MAY be empty for CollectorDiscovery — empty means "mirror every
// collector", which is a legitimate configuration (the cost is
// documented in the chart README).
func (r *CollectorDiscovery) validateDiscoverySelector() error {
	sel := r.Spec.Selector

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

// validateTargetNamespace enforces that, when set, the namespace is a
// valid DNS-1123 label (k8s namespace name format). Empty means "use
// this CollectorDiscovery's own namespace" and is always allowed.
func (r *CollectorDiscovery) validateTargetNamespace() error {
	ns := strings.TrimSpace(r.Spec.TargetNamespace)
	if ns == "" {
		return nil
	}

	if errs := validation.IsDNS1123Label(ns); len(errs) > 0 {
		return fmt.Errorf("spec.targetNamespace %q is not a valid Kubernetes namespace name: %s", ns, strings.Join(errs, "; "))
	}

	return nil
}

// validatePolicy enforces (defense-in-depth — schema enums should
// already enforce these):
//   - onCollectorRemoved is empty (defaults to Keep), Keep, or Delete;
//   - onConflict is empty (defaults to Skip) or Skip.
func (r *CollectorDiscovery) validatePolicy() error {
	switch r.Spec.Policy.OnCollectorRemoved {
	case "", DiscoveryOnRemovedKeep, DiscoveryOnRemovedDelete:
		// ok
	default:
		return fmt.Errorf(
			"spec.policy.onCollectorRemoved %q is invalid; must be one of Keep, Delete",
			r.Spec.Policy.OnCollectorRemoved)
	}

	switch r.Spec.Policy.OnConflict {
	case "", DiscoveryOnConflictSkip:
		// ok
	default:
		return fmt.Errorf(
			"spec.policy.onConflict %q is invalid; must be Skip (TakeOwnership reserved for v2)",
			r.Spec.Policy.OnConflict)
	}

	return nil
}
