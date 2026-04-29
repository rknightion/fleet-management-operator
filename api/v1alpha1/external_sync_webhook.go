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
	"net/url"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var externalattributesynclog = logf.Log.WithName("externalattributesync-resource")

// externalSyncCronParser is the standard 5-field cron parser
// (minute hour day-of-month month day-of-week) used to validate
// spec.schedule when it is not a Go duration.
var externalSyncCronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

// SetupExternalAttributeSyncWebhookWithManager registers the
// ExternalAttributeSync validating webhook with the manager. Pass a
// non-nil MatcherChecker to layer tenant-policy enforcement on top of the
// spec validation; pass nil to skip the tenant check.
func SetupExternalAttributeSyncWebhookWithManager(mgr ctrl.Manager, checker MatcherChecker) error {
	return ctrl.NewWebhookManagedBy(mgr, &ExternalAttributeSync{}).
		WithValidator(&externalAttributeSyncValidator{checker: checker}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-fleetmanagement-grafana-com-v1alpha1-externalattributesync,mutating=false,failurePolicy=fail,sideEffects=None,groups=fleetmanagement.grafana.com,resources=externalattributesyncs,verbs=create;update,versions=v1alpha1,name=vexternalattributesync.kb.io,admissionReviewVersions=v1,timeoutSeconds=5

// externalAttributeSyncValidator is the production webhook validator. It
// runs the type's spec validation and, when checker is non-nil, layers
// the tenant policy check on top.
type externalAttributeSyncValidator struct {
	checker MatcherChecker
}

var _ admission.Validator[*ExternalAttributeSync] = &externalAttributeSyncValidator{}

// ValidateCreate implements admission.Validator.
func (v *externalAttributeSyncValidator) ValidateCreate(ctx context.Context, obj *ExternalAttributeSync) (admission.Warnings, error) {
	externalattributesynclog.Info("validate create", "name", obj.Name)
	warnings, err := obj.validateExternalAttributeSync()
	if err != nil {
		return warnings, err
	}
	if err := runTenantChecks(ctx, v.checker, obj.Namespace, obj.Spec.Selector.Matchers, obj.Spec.Selector.CollectorIDs); err != nil {
		return warnings, err
	}
	return warnings, nil
}

// ValidateUpdate implements admission.Validator.
func (v *externalAttributeSyncValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *ExternalAttributeSync) (admission.Warnings, error) {
	externalattributesynclog.Info("validate update", "name", newObj.Name)
	// All fields are mutable; re-run the full validation suite.
	warnings, err := newObj.validateExternalAttributeSync()
	if err != nil {
		return warnings, err
	}
	if err := runTenantChecks(ctx, v.checker, newObj.Namespace, newObj.Spec.Selector.Matchers, newObj.Spec.Selector.CollectorIDs); err != nil {
		return warnings, err
	}
	return warnings, nil
}

// ValidateDelete implements admission.Validator.
func (v *externalAttributeSyncValidator) ValidateDelete(ctx context.Context, obj *ExternalAttributeSync) (admission.Warnings, error) {
	return nil, nil
}

// validateExternalAttributeSync performs comprehensive validation of the
// ExternalAttributeSync resource.
func (r *ExternalAttributeSync) validateExternalAttributeSync() (admission.Warnings, error) {
	// 1. Schedule is required and parses as duration or cron.
	if err := r.validateSchedule(); err != nil {
		return nil, err
	}

	// 2-5. Source kind / kind-spec consistency / HTTP URL / HTTP method.
	if err := r.validateSource(); err != nil {
		return nil, err
	}

	// 6-8. Mapping: collectorIDField, attributeFields non-empty, reserved-prefix.
	if err := r.validateMapping(); err != nil {
		return nil, err
	}

	// 9-11. Selector non-empty, matcher syntax+length, collectorIDs non-empty.
	if err := r.validateExternalSyncSelector(); err != nil {
		return nil, err
	}

	return nil, nil
}

// validateSchedule enforces:
//   - spec.schedule must be non-empty
//   - spec.schedule must parse as either a Go duration ("5m", "30s") or a
//     standard 5-field cron expression ("*/15 * * * *"). The error includes
//     both parse failure reasons so users can see what was tried.
func (r *ExternalAttributeSync) validateSchedule() error {
	schedule := strings.TrimSpace(r.Spec.Schedule)
	if schedule == "" {
		return fmt.Errorf("spec.schedule is required and must be either a Go duration (e.g. \"5m\") or a 5-field cron expression (e.g. \"*/15 * * * *\")")
	}

	// Try Go duration first — it is the cheaper, more common form.
	if _, durErr := time.ParseDuration(schedule); durErr == nil {
		return nil
	} else {
		// Fall through to cron parsing; capture the duration error so we can
		// report both reasons if cron also fails.
		if _, cronErr := externalSyncCronParser.Parse(schedule); cronErr == nil {
			return nil
		} else {
			return fmt.Errorf(
				"spec.schedule %q is neither a valid Go duration nor a valid 5-field cron expression: duration parse error: %v; cron parse error: %v",
				schedule, durErr, cronErr)
		}
	}
}

// validateSource enforces:
//   - kind is HTTP or SQL (defense-in-depth; enum marker should already enforce)
//   - kind=HTTP requires spec.source.http to be set and spec.source.sql to be nil
//   - kind=SQL requires spec.source.sql to be set and spec.source.http to be nil
//   - HTTP source URL must parse with scheme http or https
//   - HTTP method (when set) is GET or POST
func (r *ExternalAttributeSync) validateSource() error {
	src := r.Spec.Source

	switch src.Kind {
	case ExternalSourceKindHTTP:
		if src.HTTP == nil {
			return fmt.Errorf("spec.source.kind=HTTP requires spec.source.http to be set")
		}
		if src.SQL != nil {
			return fmt.Errorf("spec.source.kind=HTTP must not also set spec.source.sql")
		}
		if err := validateHTTPSource(src.HTTP); err != nil {
			return err
		}

	case ExternalSourceKindSQL:
		if src.SQL == nil {
			return fmt.Errorf("spec.source.kind=SQL requires spec.source.sql to be set")
		}
		if src.HTTP != nil {
			return fmt.Errorf("spec.source.kind=SQL must not also set spec.source.http")
		}

	default:
		return fmt.Errorf(
			"spec.source.kind %q is invalid; must be one of HTTP, SQL", src.Kind)
	}

	return nil
}

// validateHTTPSource enforces:
//   - URL non-empty after trim and parses successfully
//   - URL scheme is http or https
//   - method (when set) is GET or POST
func validateHTTPSource(http *HTTPSourceSpec) error {
	rawURL := strings.TrimSpace(http.URL)
	if rawURL == "" {
		return fmt.Errorf("spec.source.http.url is required and must not be empty")
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("spec.source.http.url is not a valid URL: %w", err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf(
			"spec.source.http.url scheme %q is invalid; must be http or https", parsed.Scheme)
	}

	if parsed.Host == "" {
		return fmt.Errorf("spec.source.http.url %q is missing a host component", rawURL)
	}

	switch http.Method {
	case "", "GET", "POST":
		// OK. Empty defaults to GET via the schema default; both forms accepted.
	default:
		return fmt.Errorf(
			"spec.source.http.method %q is invalid; must be GET or POST", http.Method)
	}

	return nil
}

// validateMapping enforces:
//   - spec.mapping.collectorIDField is non-empty after trim
//   - spec.mapping.attributeFields is non-empty (a mapping with no outputs is meaningless)
//   - attribute keys must not start with the reserved "collector." prefix
func (r *ExternalAttributeSync) validateMapping() error {
	mapping := r.Spec.Mapping

	if strings.TrimSpace(mapping.CollectorIDField) == "" {
		return fmt.Errorf("spec.mapping.collectorIDField is required and must not be empty or whitespace")
	}

	if len(mapping.AttributeFields) == 0 {
		return fmt.Errorf(
			"spec.mapping.attributeFields must contain at least one entry; a mapping with no attribute fields produces no output")
	}

	for key := range mapping.AttributeFields {
		if key == "" {
			return fmt.Errorf("spec.mapping.attributeFields contains an empty key")
		}

		if strings.HasPrefix(key, collectorReservedAttributePrefix) {
			return fmt.Errorf(
				"spec.mapping.attributeFields key %q uses reserved prefix %q which is reserved by Fleet Management for collector-reported attributes",
				key, collectorReservedAttributePrefix)
		}
	}

	return nil
}

// validateExternalSyncSelector enforces:
//   - selector must have at least one matcher or one collector ID
//   - matchers honor the policyMaxMatcherLength cap and the Prometheus matcher syntax
//   - collectorIDs entries must be non-empty
//
// Mirrors RemoteAttributePolicy.validateSelector — same rule set, same
// rationale: a partially-written selector silently targets nothing.
func (r *ExternalAttributeSync) validateExternalSyncSelector() error {
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
