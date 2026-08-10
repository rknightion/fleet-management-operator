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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func scopePipeline(specName string, annotations map[string]string) *Pipeline {
	return &Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "cr", Namespace: "team-a", Annotations: annotations},
		Spec: PipelineSpec{
			Name:       specName,
			Contents:   "prometheus.scrape \"default\" { }",
			ConfigType: ConfigTypeAlloy,
			Enabled:    new(true),
		},
	}
}

func TestPipeline_ValidateNameScope(t *testing.T) {
	tests := []struct {
		name         string
		clusterScope string
		specName     string
		annotations  map[string]string
		wantErr      bool
	}{
		{
			name:         "namespace default: opted-out name impersonating a scoped name is rejected",
			clusterScope: NameScopeNamespace, specName: "prod.x",
			annotations: map[string]string{PipelineNameScopeAnnotation: NameScopeNone}, wantErr: true,
		},
		{
			name:         "namespace default: opted-out plain name is allowed",
			clusterScope: NameScopeNamespace, specName: "plainname",
			annotations: map[string]string{PipelineNameScopeAnnotation: NameScopeNone}, wantErr: false,
		},
		{
			name:         "namespace default: scoped CR may use a dotted name (operator prepends namespace)",
			clusterScope: NameScopeNamespace, specName: "prod.x", annotations: nil, wantErr: false,
		},
		{
			name:         "unknown annotation value is rejected (namespace default)",
			clusterScope: NameScopeNamespace, specName: "x",
			annotations: map[string]string{PipelineNameScopeAnnotation: "bogus"}, wantErr: true,
		},
		{
			name:         "none default: opted-out dotted name is allowed (guard off)",
			clusterScope: NameScopeNone, specName: "prod.x",
			annotations: map[string]string{PipelineNameScopeAnnotation: NameScopeNone}, wantErr: false,
		},
		{
			name:         "unknown annotation value is rejected (none default)",
			clusterScope: NameScopeNone, specName: "x",
			annotations: map[string]string{PipelineNameScopeAnnotation: "bogus"}, wantErr: true,
		},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &pipelineValidator{nameScopeDefault: tt.clusterScope}
			_, err := v.ValidateCreate(ctx, scopePipeline(tt.specName, tt.annotations))
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCreate() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
