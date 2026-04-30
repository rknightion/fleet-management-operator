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

	"github.com/stretchr/testify/assert"
)

// These tests pin the wire-format strings sent to / accepted from the Fleet
// Management API. Any drift here silently changes API semantics — a tag
// rename in the SDK would otherwise only surface in production. CLAUDE.md
// documents the CRD↔API mapping; these tests lock it in.

func TestConfigType_ToFleetAPI(t *testing.T) {
	cases := []struct {
		name string
		in   ConfigType
		want string
	}{
		{"Alloy maps to CONFIG_TYPE_ALLOY", ConfigTypeAlloy, "CONFIG_TYPE_ALLOY"},
		{"OpenTelemetryCollector maps to CONFIG_TYPE_OTEL", ConfigTypeOpenTelemetryCollector, "CONFIG_TYPE_OTEL"},
		{"empty / zero value defaults to CONFIG_TYPE_ALLOY", ConfigType(""), "CONFIG_TYPE_ALLOY"},
		{"unknown value defaults to CONFIG_TYPE_ALLOY", ConfigType("Garbage"), "CONFIG_TYPE_ALLOY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.in.ToFleetAPI())
		})
	}
}

func TestConfigTypeFromFleetAPI(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want ConfigType
	}{
		{"CONFIG_TYPE_ALLOY maps to Alloy", "CONFIG_TYPE_ALLOY", ConfigTypeAlloy},
		{"CONFIG_TYPE_OTEL maps to OpenTelemetryCollector", "CONFIG_TYPE_OTEL", ConfigTypeOpenTelemetryCollector},
		{"empty defaults to Alloy", "", ConfigTypeAlloy},
		{"unknown defaults to Alloy", "CONFIG_TYPE_FUTURE", ConfigTypeAlloy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ConfigTypeFromFleetAPI(tc.in))
		})
	}
}

// TestConfigType_RoundTrip exercises the spec→API→CRD path. For every
// recognised CRD value, a round-trip must preserve the value (the API
// accepted what we sent and we parse back the same enum).
func TestConfigType_RoundTrip(t *testing.T) {
	for _, ct := range []ConfigType{ConfigTypeAlloy, ConfigTypeOpenTelemetryCollector} {
		t.Run(string(ct), func(t *testing.T) {
			assert.Equal(t, ct, ConfigTypeFromFleetAPI(ct.ToFleetAPI()))
		})
	}
}

func TestSourceType_ToFleetAPI(t *testing.T) {
	cases := []struct {
		name string
		in   SourceType
		want string
	}{
		{"Git", SourceTypeGit, "SOURCE_TYPE_GIT"},
		{"Terraform", SourceTypeTerraform, "SOURCE_TYPE_TERRAFORM"},
		{"Grafana", SourceTypeGrafana, "SOURCE_TYPE_GRAFANA"},
		{"legacy Kubernetes maps to Unspecified", SourceTypeKubernetes, "SOURCE_TYPE_UNSPECIFIED"},
		{"Unspecified", SourceTypeUnspecified, "SOURCE_TYPE_UNSPECIFIED"},
		{"unknown defaults to Unspecified", SourceType("Mystery"), "SOURCE_TYPE_UNSPECIFIED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.in.ToFleetAPI())
		})
	}
}

func TestSourceTypeFromFleetAPI(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want SourceType
	}{
		{"SOURCE_TYPE_GIT", "SOURCE_TYPE_GIT", SourceTypeGit},
		{"SOURCE_TYPE_TERRAFORM", "SOURCE_TYPE_TERRAFORM", SourceTypeTerraform},
		{"SOURCE_TYPE_GRAFANA", "SOURCE_TYPE_GRAFANA", SourceTypeGrafana},
		{"SOURCE_TYPE_KUBERNETES", "SOURCE_TYPE_KUBERNETES", SourceTypeKubernetes},
		{"SOURCE_TYPE_UNSPECIFIED", "SOURCE_TYPE_UNSPECIFIED", SourceTypeUnspecified},
		{"empty string defaults to Unspecified", "", SourceTypeUnspecified},
		{"unknown future kind defaults to Unspecified", "SOURCE_TYPE_GITHUB_ACTIONS", SourceTypeUnspecified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, SourceTypeFromFleetAPI(tc.in))
		})
	}
}

// TestSourceType_RoundTrip is the SourceType analogue of the ConfigType
// round-trip. Kubernetes is intentionally excluded because it is a deprecated
// CRD compatibility value with no Fleet API enum.
func TestSourceType_RoundTrip(t *testing.T) {
	for _, st := range []SourceType{SourceTypeGit, SourceTypeTerraform, SourceTypeGrafana, SourceTypeUnspecified} {
		t.Run(string(st), func(t *testing.T) {
			assert.Equal(t, st, SourceTypeFromFleetAPI(st.ToFleetAPI()))
		})
	}
}

func TestPipelineSpec_GetEnabled(t *testing.T) {
	tests := []struct {
		name string
		in   *bool
		want bool
	}{
		{name: "nil defaults true", in: nil, want: true},
		{name: "explicit true", in: boolPtr(true), want: true},
		{name: "explicit false", in: boolPtr(false), want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := PipelineSpec{Enabled: tc.in}
			assert.Equal(t, tc.want, spec.GetEnabled())
		})
	}
}

func TestCollectorTypeFromFleetAPI(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want CollectorType
	}{
		{"COLLECTOR_TYPE_ALLOY", "COLLECTOR_TYPE_ALLOY", CollectorTypeAlloy},
		{"COLLECTOR_TYPE_OTEL", "COLLECTOR_TYPE_OTEL", CollectorTypeOpenTelemetryCollector},
		{"empty defaults to Unspecified", "", CollectorTypeUnspecified},
		{"unknown future kind defaults to Unspecified", "COLLECTOR_TYPE_VECTOR", CollectorTypeUnspecified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, CollectorTypeFromFleetAPI(tc.in))
		})
	}
}
