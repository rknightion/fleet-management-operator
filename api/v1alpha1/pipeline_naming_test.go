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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func namedPipeline(ns, crName, specName string, annotations map[string]string) *Pipeline {
	return &Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: ns, Annotations: annotations},
		Spec:       PipelineSpec{Name: specName},
	}
}

func TestNamingEffectiveNameScope(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		def         string
		want        string
	}{
		{"no annotation, default none", nil, NameScopeNone, NameScopeNone},
		{"no annotation, default namespace", nil, NameScopeNamespace, NameScopeNamespace},
		{"annotation namespace overrides none default", map[string]string{PipelineNameScopeAnnotation: NameScopeNamespace}, NameScopeNone, NameScopeNamespace},
		{"annotation none overrides namespace default", map[string]string{PipelineNameScopeAnnotation: NameScopeNone}, NameScopeNamespace, NameScopeNone},
		{"unknown annotation falls back to default", map[string]string{PipelineNameScopeAnnotation: "bogus"}, NameScopeNamespace, NameScopeNamespace},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := namedPipeline("ns", "cr", "", tt.annotations)
			if got := EffectiveNameScope(p, tt.def); got != tt.want {
				t.Errorf("EffectiveNameScope() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNamingIsDiscoveredAndReadOnly(t *testing.T) {
	if namedPipeline("ns", "cr", "", nil).IsDiscovered() {
		t.Error("plain pipeline should not be discovered")
	}
	if !namedPipeline("ns", "cr", "", map[string]string{FleetPipelineIDAnnotation: "abc"}).IsDiscovered() {
		t.Error("pipeline with fleet-pipeline-id annotation should be discovered")
	}
	if namedPipeline("ns", "cr", "", nil).IsReadOnly() {
		t.Error("plain pipeline should not be read-only")
	}
	if !namedPipeline("ns", "cr", "", map[string]string{PipelineImportModeAnnotation: PipelineImportModeAnnotationReadOnly}).IsReadOnly() {
		t.Error("read-only annotation should be read-only")
	}
	if namedPipeline("ns", "cr", "", map[string]string{PipelineImportModeAnnotation: PipelineImportModeAnnotationAdopt}).IsReadOnly() {
		t.Error("adopt annotation should not be read-only")
	}
	grafana := namedPipeline("ns", "cr", "", nil)
	grafana.Spec.Source = &PipelineSource{Type: SourceTypeGrafana}
	if !grafana.IsReadOnly() {
		t.Error("Grafana-sourced pipeline should be read-only")
	}
}

func TestNamingDesiredFleetName(t *testing.T) {
	tests := []struct {
		name        string
		ns          string
		crName      string
		specName    string
		annotations map[string]string
		def         string
		source      *PipelineSource
		want        string
	}{
		{name: "scope none uses spec.name", ns: "team-a", crName: "cr", specName: "mypipe", def: NameScopeNone, want: "mypipe"},
		{name: "scope none empty spec.name uses metadata.name", ns: "team-a", crName: "cr-meta", specName: "", def: NameScopeNone, want: "cr-meta"},
		{name: "scope namespace prefixes spec.name", ns: "team-a", crName: "cr", specName: "mypipe", def: NameScopeNamespace, want: "team-a.mypipe"},
		{name: "scope namespace prefixes metadata.name when spec.name empty", ns: "team-a", crName: "cr-meta", specName: "", def: NameScopeNamespace, want: "team-a.cr-meta"},
		{name: "discovered pipeline never prefixed", ns: "team-a", crName: "cr", specName: "fleet-name", annotations: map[string]string{FleetPipelineIDAnnotation: "id1"}, def: NameScopeNamespace, want: "fleet-name"},
		{name: "read-only annotation never prefixed", ns: "team-a", crName: "cr", specName: "fleet-name", annotations: map[string]string{PipelineImportModeAnnotation: PipelineImportModeAnnotationReadOnly}, def: NameScopeNamespace, want: "fleet-name"},
		{name: "annotation opt-out yields unprefixed name", ns: "team-a", crName: "cr", specName: "mypipe", annotations: map[string]string{PipelineNameScopeAnnotation: NameScopeNone}, def: NameScopeNamespace, want: "mypipe"},
		{name: "annotation opt-in prefixes even when default none", ns: "team-a", crName: "cr", specName: "mypipe", annotations: map[string]string{PipelineNameScopeAnnotation: NameScopeNamespace}, def: NameScopeNone, want: "team-a.mypipe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := namedPipeline(tt.ns, tt.crName, tt.specName, tt.annotations)
			p.Spec.Source = tt.source
			if got := DesiredFleetName(p, tt.def); got != tt.want {
				t.Errorf("DesiredFleetName() = %q, want %q", got, tt.want)
			}
		})
	}
}
