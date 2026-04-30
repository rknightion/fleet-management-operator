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
	"regexp"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/yaml"
)

// log is for logging in this package.
var pipelinelog = logf.Log.WithName("pipeline-resource")

// SetupPipelineWebhookWithManager registers the Pipeline validating
// webhook with the manager. Pass a non-nil MatcherChecker (e.g. a
// *tenant.Checker) to layer tenant-policy enforcement on top of the spec
// validation; pass nil to skip the tenant check.
func SetupPipelineWebhookWithManager(mgr ctrl.Manager, checker MatcherChecker) error {
	return ctrl.NewWebhookManagedBy(mgr, &Pipeline{}).
		WithValidator(&pipelineValidator{checker: checker}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-fleetmanagement-grafana-com-v1alpha1-pipeline,mutating=false,failurePolicy=fail,sideEffects=None,groups=fleetmanagement.grafana.com,resources=pipelines,verbs=create;update,versions=v1alpha1,name=vpipeline.kb.io,admissionReviewVersions=v1,timeoutSeconds=5

// pipelineValidator is the production webhook validator. It runs the
// type's spec validation and, when checker is non-nil, layers the tenant
// policy check on top.
type pipelineValidator struct {
	checker MatcherChecker
}

var _ admission.Validator[*Pipeline] = &pipelineValidator{}

// ValidateCreate implements admission.Validator.
func (v *pipelineValidator) ValidateCreate(ctx context.Context, obj *Pipeline) (admission.Warnings, error) {
	pipelinelog.Info("validate create", "name", obj.Name)
	warnings, err := obj.validatePipeline()
	if err != nil {
		return warnings, err
	}
	if v.checker != nil {
		if err := v.checker.Check(ctx, obj.Namespace, obj.Spec.Matchers); err != nil {
			return warnings, err
		}
	}
	return warnings, nil
}

// ValidateUpdate implements admission.Validator.
func (v *pipelineValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *Pipeline) (admission.Warnings, error) {
	pipelinelog.Info("validate update", "name", newObj.Name)
	warnings, err := newObj.validatePipeline()
	if err != nil {
		return warnings, err
	}
	if v.checker != nil {
		if err := v.checker.Check(ctx, newObj.Namespace, newObj.Spec.Matchers); err != nil {
			return warnings, err
		}
	}
	return warnings, nil
}

// ValidateDelete implements admission.Validator.
func (v *pipelineValidator) ValidateDelete(ctx context.Context, obj *Pipeline) (admission.Warnings, error) {
	return nil, nil
}

// validatePipeline performs comprehensive validation of the Pipeline resource
func (r *Pipeline) validatePipeline() (admission.Warnings, error) {
	var allWarnings admission.Warnings

	// 1. Validate contents is not empty
	if err := r.validateContents(); err != nil {
		return nil, err
	}

	// 2. Validate configType matches contents syntax
	warnings, err := r.validateConfigType()
	if err != nil {
		return nil, err
	}
	allWarnings = append(allWarnings, warnings...)

	// 3. Validate matchers syntax
	if err := r.validateMatchers(); err != nil {
		return nil, err
	}

	// 4. Validate source semantics.
	warnings, err = r.validateSource()
	if err != nil {
		return nil, err
	}
	allWarnings = append(allWarnings, warnings...)

	return allWarnings, nil
}

// validateContents ensures pipeline contents is not empty
func (r *Pipeline) validateContents() error {
	if strings.TrimSpace(r.Spec.Contents) == "" {
		return fmt.Errorf("spec.contents cannot be empty")
	}

	return nil
}

// validateConfigType ensures configType matches the contents syntax
func (r *Pipeline) validateConfigType() (admission.Warnings, error) {
	configType := r.Spec.ConfigType
	if configType == "" {
		configType = ConfigTypeAlloy // Default
	}

	contents := strings.TrimSpace(r.Spec.Contents)

	switch configType {
	case ConfigTypeAlloy:
		// Alloy configs should have component blocks, not start with YAML keys
		if strings.HasPrefix(contents, "receivers:") ||
			strings.HasPrefix(contents, "processors:") ||
			strings.HasPrefix(contents, "exporters:") ||
			strings.HasPrefix(contents, "service:") {
			return nil, fmt.Errorf(
				"configType is 'Alloy' but contents appear to be OpenTelemetry Collector YAML configuration. " +
					"Either change configType to 'OpenTelemetryCollector' or update contents to use Alloy syntax")
		}

		// Check for common Alloy component patterns
		alloyPatternFound := false
		alloyPatterns := []string{
			"prometheus.", "loki.", "otelcol.", "pyroscope.",
			"discovery.", "remote.http", "local.file",
		}
		for _, pattern := range alloyPatterns {
			if strings.Contains(contents, pattern) {
				alloyPatternFound = true
				break
			}
		}

		if !alloyPatternFound {
			// Warn but don't fail - might be a valid but uncommon Alloy config
			return admission.Warnings{
				"Contents don't contain common Alloy component patterns. " +
					"Verify this is valid Alloy configuration syntax.",
			}, nil
		}

	case ConfigTypeOpenTelemetryCollector:
		// OTEL configs must be valid YAML
		var config map[string]any
		if err := yaml.Unmarshal([]byte(contents), &config); err != nil {
			return nil, fmt.Errorf(
				"configType is 'OpenTelemetryCollector' but contents is not valid YAML: %w", err)
		}

		// Must have service section
		if _, hasService := config["service"]; !hasService {
			return nil, fmt.Errorf(
				"configType is 'OpenTelemetryCollector' but contents is missing required 'service' section. " +
					"OpenTelemetry Collector configs must include a service section defining pipelines")
		}

		// Warn if it looks like Alloy syntax
		if strings.Contains(contents, "prometheus.scrape") ||
			strings.Contains(contents, "loki.source") ||
			strings.Contains(contents, "otelcol.receiver") {
			return admission.Warnings{
				"Contents contain Alloy-specific component names but configType is 'OpenTelemetryCollector'. " +
					"Verify this configuration will work with OTEL collectors.",
			}, nil
		}

	default:
		return nil, fmt.Errorf("invalid configType: %s (must be 'Alloy' or 'OpenTelemetryCollector')", configType)
	}

	return nil, nil
}

// validateMatchers validates matcher syntax and constraints
func (r *Pipeline) validateMatchers() error {
	for i, matcher := range r.Spec.Matchers {
		// Check length constraint (200 characters per matcher)
		if len(matcher) > 200 {
			return fmt.Errorf("matcher[%d] exceeds 200 character limit (length: %d): %s",
				i, len(matcher), matcher)
		}

		// Validate Prometheus Alertmanager matcher syntax
		if err := validateMatcherSyntax(matcher); err != nil {
			return fmt.Errorf("matcher[%d] has invalid syntax: %w", i, err)
		}
	}

	return nil
}

// validateSource enforces Fleet source rules and keeps Grafana-owned
// automatic pipelines read-only from this operator's perspective.
func (r *Pipeline) validateSource() (admission.Warnings, error) {
	if r.Spec.Source == nil {
		return nil, nil
	}

	var warnings admission.Warnings
	sourceType := r.Spec.Source.Type
	namespace := strings.TrimSpace(r.Spec.Source.Namespace)

	switch sourceType {
	case "", SourceTypeUnspecified:
		if namespace != "" {
			return nil, fmt.Errorf("spec.source.namespace must be empty when spec.source.type is empty or 'Unspecified'")
		}
	case SourceTypeGit, SourceTypeTerraform, SourceTypeGrafana:
		if namespace == "" {
			return nil, fmt.Errorf("spec.source.namespace is required when spec.source.type is %q", sourceType)
		}
	case SourceTypeKubernetes:
		warnings = append(warnings,
			"spec.source.type=Kubernetes is deprecated and is not sent to Fleet Management; omit spec.source for operator-managed pipelines")
	default:
		return nil, fmt.Errorf("invalid source type: %s (must be 'Git', 'Terraform', 'Grafana', 'Kubernetes', or 'Unspecified')", sourceType)
	}

	if sourceType == SourceTypeGrafana {
		annotations := r.GetAnnotations()
		if annotations != nil && annotations[PipelineImportModeAnnotation] == PipelineImportModeAnnotationAdopt {
			return nil, fmt.Errorf("grafana-sourced pipelines are read-only and cannot use %s=%s",
				PipelineImportModeAnnotation, PipelineImportModeAnnotationAdopt)
		}
	}

	return warnings, nil
}

// validateMatcherSyntax validates Prometheus Alertmanager matcher syntax
func validateMatcherSyntax(matcher string) error {
	// Prometheus matcher syntax: label=value, label!=value, label=~regex, label!~regex
	// Pattern: <label> <operator> <value>
	// Operators: =, !=, =~, !~

	matcher = strings.TrimSpace(matcher)
	if matcher == "" {
		return fmt.Errorf("matcher cannot be empty")
	}

	// Regex pattern for valid matcher syntax
	// Allows: key=value, key!=value, key=~regex, key!~regex
	// Label names: [a-zA-Z_][a-zA-Z0-9_]*
	// Can include dots for hierarchical labels (e.g., collector.os)
	matcherPattern := regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.]*\s*(=|!=|=~|!~)\s*.+$`)

	if !matcherPattern.MatchString(matcher) {
		return fmt.Errorf(
			"invalid Prometheus matcher syntax: %s\n"+
				"Valid formats:\n"+
				"  - key=value (equals)\n"+
				"  - key!=value (not equals)\n"+
				"  - key=~regex (regex match)\n"+
				"  - key!~regex (regex not match)\n"+
				"Example: collector.os=linux, environment!=dev, team=~team-(a|b)",
			matcher)
	}

	// Additional validation: check for common mistakes
	if strings.Contains(matcher, "==") {
		return fmt.Errorf("use '=' not '==' for equality matching: %s", matcher)
	}

	if strings.Count(matcher, "=") > 1 && !strings.Contains(matcher, "!=") &&
		!strings.Contains(matcher, "=~") && !strings.Contains(matcher, "!~") {
		return fmt.Errorf("matcher contains multiple '=' operators - check syntax: %s", matcher)
	}

	return nil
}

// ValidateMatcherSyntax exposes the package-internal matcher validator so
// non-webhook callers (e.g. the TenantPolicy status reconciler) can run the
// same parse without re-implementing it.
func ValidateMatcherSyntax(matcher string) error {
	return validateMatcherSyntax(matcher)
}
