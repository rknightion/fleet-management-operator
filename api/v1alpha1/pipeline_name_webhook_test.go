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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// pipelineWithName builds an otherwise-valid Alloy Pipeline with the given
// spec.name so that only spec.name validation can fail.
func pipelineWithName(name string) *Pipeline {
	return &Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "cr-name", Namespace: "default"},
		Spec: PipelineSpec{
			Name:       name,
			Contents:   "prometheus.scrape \"default\" { }",
			ConfigType: ConfigTypeAlloy,
			Enabled:    new(true),
		},
	}
}

func TestPipeline_ValidateName(t *testing.T) {
	tests := []struct {
		name     string
		specName string
		wantErr  bool
	}{
		{name: "empty name allowed (uses metadata.name)", specName: "", wantErr: false},
		{name: "normal name", specName: "my-pipeline", wantErr: false},
		{name: "dotted name allowed in phase 1", specName: "a.b", wantErr: false},
		{name: "underscores and mixed case allowed", specName: "My_Pipeline", wantErr: false},
		{name: "max length 253 allowed", specName: strings.Repeat("a", 253), wantErr: false},
		{name: "over max length rejected", specName: strings.Repeat("a", 254), wantErr: true},
		{name: "leading whitespace rejected", specName: " x", wantErr: true},
		{name: "trailing whitespace rejected", specName: "x ", wantErr: true},
		{name: "embedded space rejected", specName: "a b", wantErr: true},
		{name: "tab rejected", specName: "a\tb", wantErr: true},
		{name: "newline rejected", specName: "a\nb", wantErr: true},
		{name: "control char rejected", specName: "a\x01b", wantErr: true},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := (&pipelineValidator{}).ValidateCreate(ctx, pipelineWithName(tt.specName))
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCreate() with name %q error = %v, wantErr %v", tt.specName, err, tt.wantErr)
			}
		})
	}
}
