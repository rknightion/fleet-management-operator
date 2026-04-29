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

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// newCollector returns a baseline-valid Collector with an optional spec
// overlay applied. Tests mutate the returned value to exercise specific
// rules without restating the full struct each time.
func newCollector(name string, spec CollectorSpec) *Collector {
	if spec.ID == "" {
		spec.ID = "collector-1"
	}
	return &Collector{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: spec,
	}
}

func TestCollector_ValidateCreate(t *testing.T) {
	tests := []struct {
		name      string
		collector *Collector
		wantErr   bool
		errMsg    string
	}{
		{
			name: "valid - minimal",
			collector: newCollector("minimal", CollectorSpec{
				ID: "collector-1",
			}),
			wantErr: false,
		},
		{
			name: "valid - with remote attributes",
			collector: newCollector("with-attrs", CollectorSpec{
				ID: "collector-1",
				RemoteAttributes: map[string]string{
					"team":        "platform",
					"environment": "production",
					"region":      "us-east-1",
				},
			}),
			wantErr: false,
		},
		{
			name: "valid - id with surrounding whitespace and content",
			collector: newCollector("trim-ok", CollectorSpec{
				ID: "  collector-1  ",
			}),
			wantErr: false,
		},
		{
			name: "invalid - id is whitespace only",
			collector: newCollector("blank-id", CollectorSpec{
				ID: "   ",
			}),
			wantErr: true,
			errMsg:  "spec.id cannot be empty",
		},
		{
			name: "invalid - reserved attribute prefix collector.os",
			collector: newCollector("reserved-os", CollectorSpec{
				ID: "collector-1",
				RemoteAttributes: map[string]string{
					"collector.os": "linux",
				},
			}),
			wantErr: true,
			errMsg:  "reserved prefix",
		},
		{
			name: "invalid - reserved attribute prefix collector.anything",
			collector: newCollector("reserved-anything", CollectorSpec{
				ID: "collector-1",
				RemoteAttributes: map[string]string{
					"collector.custom.key": "value",
				},
			}),
			wantErr: true,
			errMsg:  `"collector.custom.key"`,
		},
		{
			name: "valid - similar but not reserved prefix",
			collector: newCollector("similar-prefix", CollectorSpec{
				ID: "collector-1",
				RemoteAttributes: map[string]string{
					// Case-sensitive — "Collector." is allowed.
					"Collector.os": "linux",
					// "collectors." (plural) is also allowed.
					"collectors.team": "platform",
				},
			}),
			wantErr: false,
		},
		{
			name: "invalid - empty attribute key",
			collector: newCollector("empty-key", CollectorSpec{
				ID: "collector-1",
				RemoteAttributes: map[string]string{
					"": "value",
				},
			}),
			wantErr: true,
			errMsg:  "empty key",
		},
		{
			name: "invalid - attribute value over 1024 characters",
			collector: newCollector("long-value", CollectorSpec{
				ID: "collector-1",
				RemoteAttributes: map[string]string{
					"team": strings.Repeat("a", 1025),
				},
			}),
			wantErr: true,
			errMsg:  `"team"`,
		},
		{
			name: "valid - attribute value exactly 1024 characters",
			collector: newCollector("max-value", CollectorSpec{
				ID: "collector-1",
				RemoteAttributes: map[string]string{
					"team": strings.Repeat("a", 1024),
				},
			}),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, err := tt.collector.ValidateCreate(ctx, tt.collector)
			if tt.wantErr {
				if assert.Error(t, err) && tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestCollector_ValidateUpdate(t *testing.T) {
	baseOld := newCollector("test", CollectorSpec{
		ID: "collector-1",
		RemoteAttributes: map[string]string{
			"team": "platform",
		},
	})

	tests := []struct {
		name    string
		oldObj  *Collector
		newObj  *Collector
		wantErr bool
		errMsg  string
	}{
		{
			name:   "valid - spec.id unchanged, attributes updated",
			oldObj: baseOld,
			newObj: newCollector("test", CollectorSpec{
				ID: "collector-1",
				RemoteAttributes: map[string]string{
					"team":        "platform",
					"environment": "production",
				},
			}),
			wantErr: false,
		},
		{
			name:   "invalid - spec.id changed",
			oldObj: baseOld,
			newObj: newCollector("test", CollectorSpec{
				ID: "collector-2",
				RemoteAttributes: map[string]string{
					"team": "platform",
				},
			}),
			wantErr: true,
			errMsg:  "spec.id is immutable",
		},
		{
			name:   "invalid - update introduces reserved attribute prefix",
			oldObj: baseOld,
			newObj: newCollector("test", CollectorSpec{
				ID: "collector-1",
				RemoteAttributes: map[string]string{
					"team":         "platform",
					"collector.os": "linux",
				},
			}),
			wantErr: true,
			errMsg:  "reserved prefix",
		},
		{
			name:   "invalid - update introduces empty key",
			oldObj: baseOld,
			newObj: newCollector("test", CollectorSpec{
				ID: "collector-1",
				RemoteAttributes: map[string]string{
					"team": "platform",
					"":     "value",
				},
			}),
			wantErr: true,
			errMsg:  "empty key",
		},
		{
			name:   "invalid - update introduces overlong value",
			oldObj: baseOld,
			newObj: newCollector("test", CollectorSpec{
				ID: "collector-1",
				RemoteAttributes: map[string]string{
					"team": strings.Repeat("a", 1025),
				},
			}),
			wantErr: true,
			errMsg:  "exceeds the maximum",
		},
		{
			name:   "invalid - update blanks the id",
			oldObj: baseOld,
			newObj: newCollector("test", CollectorSpec{
				ID: "   ",
			}),
			wantErr: true,
			// id-changed check fires first because "collector-1" != "   ".
			errMsg: "spec.id is immutable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, err := tt.newObj.ValidateUpdate(ctx, tt.oldObj, tt.newObj)
			if tt.wantErr {
				if assert.Error(t, err) && tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestCollector_ValidateDelete(t *testing.T) {
	c := newCollector("test", CollectorSpec{ID: "collector-1"})
	warnings, err := c.ValidateDelete(context.Background(), c)
	assert.NoError(t, err)
	assert.Nil(t, warnings)
}

// TestCollector_validateRemoteAttributes_DefenseInDepth exercises the
// count-based safety net that guards against the schema MaxProperties
// marker being bypassed (e.g. because a controller writes directly to
// the API server without going through the OpenAPI layer).
func TestCollector_validateRemoteAttributes_DefenseInDepth(t *testing.T) {
	attrs := make(map[string]string, collectorMaxRemoteAttributes+1)
	for i := range collectorMaxRemoteAttributes + 1 {
		// Use a non-reserved key namespace so the count check is what fails.
		attrs[fmtKey(i)] = "v"
	}
	c := newCollector("too-many", CollectorSpec{
		ID:               "collector-1",
		RemoteAttributes: attrs,
	})

	_, err := c.ValidateCreate(context.Background(), c)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "exceeds the maximum")
	}
}

// fmtKey produces deterministic distinct attribute keys for the
// defense-in-depth count test.
func fmtKey(i int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	// Two-letter base-26 encoding handles up to 676 keys, well above 100.
	return string(letters[i/len(letters)%len(letters)]) + string(letters[i%len(letters)]) +
		"-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
