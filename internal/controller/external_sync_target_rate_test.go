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

package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
)

// TestSourceTargetKey covers the bucket-key derivation for E19's per-target
// rate limiter. Two ExternalAttributeSync CRs that resolve to the same upstream
// must produce equal keys; CRs against distinct upstreams must produce
// distinct keys.
func TestSourceTargetKey(t *testing.T) {
	httpURL := func(u string) fleetmanagementv1alpha1.ExternalSource {
		return fleetmanagementv1alpha1.ExternalSource{
			Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
			HTTP: &fleetmanagementv1alpha1.HTTPSourceSpec{URL: u},
		}
	}
	sqlSecret := func(ns, name string) fleetmanagementv1alpha1.ExternalSource {
		return fleetmanagementv1alpha1.ExternalSource{
			Kind:      fleetmanagementv1alpha1.ExternalSourceKindSQL,
			SQL:       &fleetmanagementv1alpha1.SQLSourceSpec{Driver: "postgres"},
			SecretRef: &corev1.SecretReference{Namespace: ns, Name: name},
		}
	}

	cases := []struct {
		name string
		spec fleetmanagementv1alpha1.ExternalSource
		want string
	}{
		{
			name: "http with full URL collapses to scheme+host",
			spec: httpURL("https://cmdb.example.com/v1/devices?limit=100"),
			want: "http:https://cmdb.example.com",
		},
		{
			name: "http with different path same host produces same key",
			spec: httpURL("https://cmdb.example.com/v2/other"),
			want: "http:https://cmdb.example.com",
		},
		{
			name: "http with different host produces different key",
			spec: httpURL("https://other.example.com/v1/devices"),
			want: "http:https://other.example.com",
		},
		{
			name: "http with port preserves port in key",
			spec: httpURL("http://internal:8080/devices"),
			want: "http:http://internal:8080",
		},
		{
			name: "http with malformed URL falls back to literal",
			spec: httpURL("::not-a-url::"),
			want: "http:::not-a-url::",
		},
		{
			name: "http with nil HTTPSpec produces empty-target key",
			spec: fleetmanagementv1alpha1.ExternalSource{
				Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
			},
			want: "http:",
		},
		{
			name: "sql keys on secret reference",
			spec: sqlSecret("default", "cmdb-creds"),
			want: "sql:default/cmdb-creds",
		},
		{
			name: "sql with same secret in different namespaces produces different keys",
			spec: sqlSecret("staging", "cmdb-creds"),
			want: "sql:staging/cmdb-creds",
		},
		{
			name: "sql without SecretRef falls back to driver name",
			spec: fleetmanagementv1alpha1.ExternalSource{
				Kind: fleetmanagementv1alpha1.ExternalSourceKindSQL,
				SQL:  &fleetmanagementv1alpha1.SQLSourceSpec{Driver: "mysql"},
			},
			want: "sql:driver=mysql",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, sourceTargetKey(tc.spec))
		})
	}
}

// TestLimiterForSource_DisabledByDefault confirms the per-target limiter is
// off when SourceTargetRate is zero or negative — the documented default
// posture so existing installs see no behaviour change.
func TestLimiterForSource_DisabledByDefault(t *testing.T) {
	r := &ExternalAttributeSyncReconciler{}
	spec := fleetmanagementv1alpha1.ExternalSource{
		Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
		HTTP: &fleetmanagementv1alpha1.HTTPSourceSpec{URL: "http://example/"},
	}
	assert.Nil(t, r.limiterForSource(spec), "rate=0 must disable the limiter")

	r.SourceTargetRate = -1
	assert.Nil(t, r.limiterForSource(spec), "negative rate must also disable the limiter")
}

// TestLimiterForSource_SharedAcrossSameTarget proves two reconciles against
// the same upstream get the SAME *rate.Limiter instance — that's the whole
// point of E19's bucket-sharing design, since two syncs against the same
// CMDB must cooperate on a single token bucket rather than each refilling
// independently.
func TestLimiterForSource_SharedAcrossSameTarget(t *testing.T) {
	r := &ExternalAttributeSyncReconciler{
		SourceTargetRate:  1,
		SourceTargetBurst: 4,
	}
	spec1 := fleetmanagementv1alpha1.ExternalSource{
		Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
		HTTP: &fleetmanagementv1alpha1.HTTPSourceSpec{URL: "https://cmdb.example/v1/devices"},
	}
	spec2 := fleetmanagementv1alpha1.ExternalSource{
		Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
		HTTP: &fleetmanagementv1alpha1.HTTPSourceSpec{URL: "https://cmdb.example/v2/other"},
	}

	lim1 := r.limiterForSource(spec1)
	lim2 := r.limiterForSource(spec2)
	assert.Same(t, lim1, lim2, "same host different paths must share the limiter instance")
}

// TestLimiterForSource_DistinctAcrossDifferentTargets is the inverse of the
// above: distinct upstream hosts must get distinct limiter instances.
func TestLimiterForSource_DistinctAcrossDifferentTargets(t *testing.T) {
	r := &ExternalAttributeSyncReconciler{
		SourceTargetRate:  1,
		SourceTargetBurst: 4,
	}
	spec1 := fleetmanagementv1alpha1.ExternalSource{
		Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
		HTTP: &fleetmanagementv1alpha1.HTTPSourceSpec{URL: "https://a.example/devices"},
	}
	spec2 := fleetmanagementv1alpha1.ExternalSource{
		Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
		HTTP: &fleetmanagementv1alpha1.HTTPSourceSpec{URL: "https://b.example/devices"},
	}

	lim1 := r.limiterForSource(spec1)
	lim2 := r.limiterForSource(spec2)
	assert.NotSame(t, lim1, lim2, "different hosts must not share a limiter")
}

// TestLimiterForSource_BurstFallback confirms the SourceTargetBurst zero-value
// guard lands on a sensible default (4 — matching --controller-sync-max-concurrent
// so a full concurrency generation can pass through immediately).
func TestLimiterForSource_BurstFallback(t *testing.T) {
	r := &ExternalAttributeSyncReconciler{SourceTargetRate: 1}
	spec := fleetmanagementv1alpha1.ExternalSource{
		Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
		HTTP: &fleetmanagementv1alpha1.HTTPSourceSpec{URL: "https://x/"},
	}
	lim := r.limiterForSource(spec)
	assert.NotNil(t, lim)
	assert.Equal(t, 4, lim.Burst(), "zero burst must fall back to 4")
}
