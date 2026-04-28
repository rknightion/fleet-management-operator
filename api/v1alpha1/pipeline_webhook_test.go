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

func TestPipeline_ValidateCreate(t *testing.T) {
	tests := []struct {
		name     string
		pipeline *Pipeline
		wantErr  bool
		errMsg   string
	}{
		{
			name: "valid Alloy pipeline",
			pipeline: &Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pipeline",
					Namespace: "default",
				},
				Spec: PipelineSpec{
					Contents:   "prometheus.scrape \"default\" { }",
					ConfigType: ConfigTypeAlloy,
					Enabled:    true,
					Matchers:   []string{"collector.os=linux"},
				},
			},
			wantErr: false,
		},
		{
			name: "valid OTEL pipeline",
			pipeline: &Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-otel",
					Namespace: "default",
				},
				Spec: PipelineSpec{
					Contents: `receivers:
  otlp:
    protocols:
      grpc:
service:
  pipelines:
    metrics:
      receivers: [otlp]`,
					ConfigType: ConfigTypeOpenTelemetryCollector,
					Enabled:    true,
				},
			},
			wantErr: false,
		},
		{
			name: "empty contents",
			pipeline: &Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "empty",
					Namespace: "default",
				},
				Spec: PipelineSpec{
					Contents: "   ",
					Enabled:  true,
				},
			},
			wantErr: true,
			errMsg:  "contents cannot be empty",
		},
		{
			name: "configType mismatch - Alloy config marked as OTEL",
			pipeline: &Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mismatch",
					Namespace: "default",
				},
				Spec: PipelineSpec{
					Contents:   "receivers:\n  otlp: {}",
					ConfigType: ConfigTypeAlloy,
					Enabled:    true,
				},
			},
			wantErr: true,
			errMsg:  "configType is 'Alloy' but contents appear to be OpenTelemetry",
		},
		{
			name: "configType mismatch - OTEL config without service section",
			pipeline: &Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-service",
					Namespace: "default",
				},
				Spec: PipelineSpec{
					Contents: `receivers:
  otlp:
    protocols:
      grpc: {}`,
					ConfigType: ConfigTypeOpenTelemetryCollector,
					Enabled:    true,
				},
			},
			wantErr: true,
			errMsg:  "missing required 'service' section",
		},
		{
			name: "invalid matcher syntax - using ==",
			pipeline: &Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bad-matcher",
					Namespace: "default",
				},
				Spec: PipelineSpec{
					Contents:   "prometheus.scrape \"default\" { }",
					ConfigType: ConfigTypeAlloy,
					Enabled:    true,
					Matchers:   []string{"collector.os==linux"},
				},
			},
			wantErr: true,
			errMsg:  "use '=' not '=='",
		},
		{
			name: "matcher exceeds 200 characters",
			pipeline: &Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "long-matcher",
					Namespace: "default",
				},
				Spec: PipelineSpec{
					Contents:   "prometheus.scrape \"default\" { }",
					ConfigType: ConfigTypeAlloy,
					Enabled:    true,
					Matchers: []string{
						"very.long.label.name.that.exceeds.the.limit=very.long.value.that.also.contributes.to.exceeding.the.two.hundred.character.limit.for.matchers.in.the.fleet.management.api.which.is.documented.in.the.api.specification.and.must.be.enforced",
					},
				},
			},
			wantErr: true,
			errMsg:  "exceeds 200 character limit",
		},
		{
			name: "valid matchers with all operators",
			pipeline: &Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "all-operators",
					Namespace: "default",
				},
				Spec: PipelineSpec{
					Contents:   "prometheus.scrape \"default\" { }",
					ConfigType: ConfigTypeAlloy,
					Enabled:    true,
					Matchers: []string{
						"collector.os=linux",
						"environment!=development",
						"team=~team-(a|b)",
						"region!~us-.*",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid matcher - empty",
			pipeline: &Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "empty-matcher",
					Namespace: "default",
				},
				Spec: PipelineSpec{
					Contents:   "prometheus.scrape \"default\" { }",
					ConfigType: ConfigTypeAlloy,
					Enabled:    true,
					Matchers:   []string{""},
				},
			},
			wantErr: true,
			errMsg:  "matcher cannot be empty",
		},
		{
			name: "invalid matcher - no operator",
			pipeline: &Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-operator",
					Namespace: "default",
				},
				Spec: PipelineSpec{
					Contents:   "prometheus.scrape \"default\" { }",
					ConfigType: ConfigTypeAlloy,
					Enabled:    true,
					Matchers:   []string{"collector.os"},
				},
			},
			wantErr: true,
			errMsg:  "invalid Prometheus matcher syntax",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, err := (&pipelineValidator{}).ValidateCreate(ctx, tt.pipeline)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCreate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil {
				if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateCreate() error = %v, should contain %v", err, tt.errMsg)
				}
			}
		})
	}
}

func TestPipeline_ValidateUpdate(t *testing.T) {
	oldPipeline := &Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: PipelineSpec{
			Contents:   "prometheus.scrape \"old\" { }",
			ConfigType: ConfigTypeAlloy,
			Enabled:    true,
		},
	}

	tests := []struct {
		name     string
		pipeline *Pipeline
		wantErr  bool
		errMsg   string
	}{
		{
			name: "valid update",
			pipeline: &Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Spec: PipelineSpec{
					Contents:   "prometheus.scrape \"new\" { }",
					ConfigType: ConfigTypeAlloy,
					Enabled:    true,
				},
			},
			wantErr: false,
		},
		{
			name: "update with invalid matcher",
			pipeline: &Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Spec: PipelineSpec{
					Contents:   "prometheus.scrape \"new\" { }",
					ConfigType: ConfigTypeAlloy,
					Enabled:    true,
					Matchers:   []string{"invalid==matcher"},
				},
			},
			wantErr: true,
			errMsg:  "use '=' not '=='",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, err := (&pipelineValidator{}).ValidateUpdate(ctx, oldPipeline, tt.pipeline)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateUpdate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil {
				if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateUpdate() error = %v, should contain %v", err, tt.errMsg)
				}
			}
		})
	}
}

func TestValidateMatcherSyntax(t *testing.T) {
	tests := []struct {
		name    string
		matcher string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid equals",
			matcher: "collector.os=linux",
			wantErr: false,
		},
		{
			name:    "valid not equals",
			matcher: "environment!=dev",
			wantErr: false,
		},
		{
			name:    "valid regex match",
			matcher: "team=~team-(a|b)",
			wantErr: false,
		},
		{
			name:    "valid regex not match",
			matcher: "region!~us-.*",
			wantErr: false,
		},
		{
			name:    "hierarchical label",
			matcher: "collector.host.name=prod-server-01",
			wantErr: false,
		},
		{
			name:    "invalid - double equals",
			matcher: "key==value",
			wantErr: true,
			errMsg:  "use '=' not '=='",
		},
		{
			name:    "invalid - no operator",
			matcher: "keyvalue",
			wantErr: true,
			errMsg:  "invalid Prometheus matcher syntax",
		},
		{
			name:    "invalid - starts with number",
			matcher: "1key=value",
			wantErr: true,
			errMsg:  "invalid Prometheus matcher syntax",
		},
		{
			name:    "invalid - special chars in label",
			matcher: "key-with-dash=value",
			wantErr: true,
			errMsg:  "invalid Prometheus matcher syntax",
		},
		{
			name:    "empty matcher",
			matcher: "",
			wantErr: true,
			errMsg:  "matcher cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMatcherSyntax(tt.matcher)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateMatcherSyntax() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil {
				if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("validateMatcherSyntax() error = %v, should contain %v", err, tt.errMsg)
				}
			}
		})
	}
}

// helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && stringContains(s, substr)))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
