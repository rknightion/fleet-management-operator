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

// Pipeline name-scope values. NameScopeNamespace prefixes the Fleet Management
// pipeline name with "<namespace>." so that pipelines in different namespaces
// cannot collide on a shared Fleet name.
const (
	NameScopeNone      = "none"
	NameScopeNamespace = "namespace"

	// PipelineNameScopeAnnotation overrides the cluster-wide name-scope default
	// for a single Pipeline. Values: "namespace" or "none".
	PipelineNameScopeAnnotation = "fleetmanagement.grafana.com/name-scope"

	// nameScopeSeparator joins the namespace and the base name. Kubernetes
	// namespaces are DNS-1123 labels (no dots), so the first dot-segment of a
	// scoped name is unambiguously the namespace.
	nameScopeSeparator = "."
)

// IsDiscovered reports whether this Pipeline mirrors an existing Fleet
// Management pipeline created by PipelineDiscovery. Discovered pipelines carry a
// Fleet-assigned name (recorded via FleetPipelineIDAnnotation) and must never be
// renamed by name scoping.
func (p *Pipeline) IsDiscovered() bool {
	_, ok := p.GetAnnotations()[FleetPipelineIDAnnotation]
	return ok
}

// IsReadOnly reports whether the operator only observes this pipeline and never
// upserts it: Grafana-sourced pipelines and discovery read-only imports. This is
// the single source of truth for the read-only predicate; the controller
// delegates to it.
func (p *Pipeline) IsReadOnly() bool {
	if p.Spec.Source != nil && p.Spec.Source.Type == SourceTypeGrafana {
		return true
	}
	return p.GetAnnotations()[PipelineImportModeAnnotation] == PipelineImportModeAnnotationReadOnly
}

// EffectiveNameScope resolves the name scope for a Pipeline: a valid per-CR
// annotation wins, otherwise the cluster-wide default applies. An unrecognised
// annotation value falls back to the default (the webhook rejects unknown
// values separately).
func EffectiveNameScope(p *Pipeline, defaultScope string) string {
	switch p.GetAnnotations()[PipelineNameScopeAnnotation] {
	case NameScopeNamespace:
		return NameScopeNamespace
	case NameScopeNone:
		return NameScopeNone
	default:
		return defaultScope
	}
}

// baseName is the unscoped Fleet pipeline name: spec.name if set, else
// metadata.name.
func (p *Pipeline) baseName() string {
	if p.Spec.Name != "" {
		return p.Spec.Name
	}
	return p.Name
}

// DesiredFleetName computes the Fleet Management pipeline name the operator
// should use for this Pipeline given the cluster-wide default scope. Discovered
// and read-only pipelines keep their Fleet-assigned base name regardless of
// scope.
func DesiredFleetName(p *Pipeline, defaultScope string) string {
	base := p.baseName()
	if EffectiveNameScope(p, defaultScope) != NameScopeNamespace {
		return base
	}
	if p.IsDiscovered() || p.IsReadOnly() {
		return base
	}
	return p.Namespace + nameScopeSeparator + base
}
