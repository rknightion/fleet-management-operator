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

// newPolicy returns a baseline-valid RemoteAttributePolicy with the supplied
// spec overlay applied. Tests mutate the returned value to exercise specific
// rules without restating the full struct each time.
func newPolicy(name string, spec RemoteAttributePolicySpec) *RemoteAttributePolicy {
	if spec.Attributes == nil {
		spec.Attributes = map[string]string{"team": "platform"}
	}
	if len(spec.Selector.Matchers) == 0 && len(spec.Selector.CollectorIDs) == 0 {
		spec.Selector.Matchers = []string{"environment=production"}
	}
	return &RemoteAttributePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: spec,
	}
}

func TestRemoteAttributePolicy_ValidateCreate(t *testing.T) {
	tests := []struct {
		name    string
		policy  *RemoteAttributePolicy
		wantErr bool
		errMsg  string
	}{
		// Rule 1: spec.attributes must be non-empty.
		{
			name: "valid - attributes populated, matcher selector",
			policy: newPolicy("ok-matcher", RemoteAttributePolicySpec{
				Attributes: map[string]string{"team": "platform"},
				Selector: PolicySelector{
					Matchers: []string{"environment=production"},
				},
			}),
			wantErr: false,
		},
		{
			name: "valid - attributes populated, collectorIDs selector",
			policy: newPolicy("ok-ids", RemoteAttributePolicySpec{
				Attributes: map[string]string{"team": "platform"},
				Selector: PolicySelector{
					CollectorIDs: []string{"collector-1", "collector-2"},
				},
			}),
			wantErr: false,
		},
		{
			name: "valid - attributes populated, both matchers and collectorIDs",
			policy: newPolicy("ok-both", RemoteAttributePolicySpec{
				Attributes: map[string]string{"team": "platform"},
				Selector: PolicySelector{
					Matchers:     []string{"environment=production"},
					CollectorIDs: []string{"collector-1"},
				},
			}),
			wantErr: false,
		},
		{
			name: "invalid - attributes empty map",
			policy: newPolicy("empty-attrs", RemoteAttributePolicySpec{
				Attributes: map[string]string{},
				Selector: PolicySelector{
					Matchers: []string{"environment=production"},
				},
			}),
			wantErr: true,
			errMsg:  "spec.attributes must contain at least one entry",
		},
		{
			name: "invalid - attributes nil",
			policy: &RemoteAttributePolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "nil-attrs", Namespace: "default"},
				Spec: RemoteAttributePolicySpec{
					Selector: PolicySelector{
						Matchers: []string{"environment=production"},
					},
				},
			},
			wantErr: true,
			errMsg:  "spec.attributes must contain at least one entry",
		},

		// Rule 2: reserved attribute prefix.
		{
			name: "invalid - reserved attribute prefix collector.os",
			policy: newPolicy("reserved-os", RemoteAttributePolicySpec{
				Attributes: map[string]string{"collector.os": "linux"},
				Selector: PolicySelector{
					Matchers: []string{"environment=production"},
				},
			}),
			wantErr: true,
			errMsg:  "reserved prefix",
		},
		{
			name: "invalid - reserved attribute prefix collector.custom.key",
			policy: newPolicy("reserved-custom", RemoteAttributePolicySpec{
				Attributes: map[string]string{"collector.custom.key": "value"},
				Selector: PolicySelector{
					Matchers: []string{"environment=production"},
				},
			}),
			wantErr: true,
			errMsg:  `"collector.custom.key"`,
		},
		{
			name: "valid - similar but not reserved prefix",
			policy: newPolicy("similar-prefix", RemoteAttributePolicySpec{
				Attributes: map[string]string{
					// Case-sensitive — "Collector." is allowed.
					"Collector.os": "linux",
					// "collectors." (plural) is allowed.
					"collectors.team": "platform",
				},
				Selector: PolicySelector{
					Matchers: []string{"environment=production"},
				},
			}),
			wantErr: false,
		},

		// Rule 3: empty attribute key.
		{
			name: "invalid - empty attribute key",
			policy: newPolicy("empty-key", RemoteAttributePolicySpec{
				Attributes: map[string]string{"": "value"},
				Selector: PolicySelector{
					Matchers: []string{"environment=production"},
				},
			}),
			wantErr: true,
			errMsg:  "empty key",
		},

		// Rule 4: attribute value length cap.
		{
			name: "invalid - attribute value over 1024 characters",
			policy: newPolicy("long-value", RemoteAttributePolicySpec{
				Attributes: map[string]string{"team": strings.Repeat("a", 1025)},
				Selector: PolicySelector{
					Matchers: []string{"environment=production"},
				},
			}),
			wantErr: true,
			errMsg:  `"team"`,
		},
		{
			name: "valid - attribute value exactly 1024 characters",
			policy: newPolicy("max-value", RemoteAttributePolicySpec{
				Attributes: map[string]string{"team": strings.Repeat("a", 1024)},
				Selector: PolicySelector{
					Matchers: []string{"environment=production"},
				},
			}),
			wantErr: false,
		},

		// Rule 6: matcher syntax.
		{
			name: "valid - matcher with regex operator",
			policy: newPolicy("matcher-regex", RemoteAttributePolicySpec{
				Attributes: map[string]string{"team": "platform"},
				Selector: PolicySelector{
					Matchers: []string{"team=~team-(a|b)"},
				},
			}),
			wantErr: false,
		},
		{
			name: "invalid - matcher uses double equals",
			policy: newPolicy("matcher-double-equals", RemoteAttributePolicySpec{
				Attributes: map[string]string{"team": "platform"},
				Selector: PolicySelector{
					Matchers: []string{"environment==production"},
				},
			}),
			wantErr: true,
			errMsg:  "spec.selector.matchers[0]",
		},
		{
			name: "invalid - matcher missing operator",
			policy: newPolicy("matcher-no-op", RemoteAttributePolicySpec{
				Attributes: map[string]string{"team": "platform"},
				Selector: PolicySelector{
					Matchers: []string{"environment"},
				},
			}),
			wantErr: true,
			errMsg:  "invalid Prometheus matcher syntax",
		},
		{
			name: "invalid - matcher exceeds 200 character cap",
			policy: newPolicy("matcher-too-long", RemoteAttributePolicySpec{
				Attributes: map[string]string{"team": "platform"},
				Selector: PolicySelector{
					Matchers: []string{"environment=" + strings.Repeat("a", 200)},
				},
			}),
			wantErr: true,
			errMsg:  "exceeds 200 character limit",
		},

		// Rule 7: selector must be non-empty.
		{
			name: "invalid - selector has neither matchers nor collectorIDs",
			policy: &RemoteAttributePolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "empty-selector", Namespace: "default"},
				Spec: RemoteAttributePolicySpec{
					Attributes: map[string]string{"team": "platform"},
					Selector:   PolicySelector{},
				},
			},
			wantErr: true,
			errMsg:  "spec.selector must specify at least one of matchers or collectorIDs",
		},

		// Rule 8: collectorIDs entries must be non-empty.
		{
			name: "invalid - collectorIDs contains empty string",
			policy: newPolicy("empty-cid", RemoteAttributePolicySpec{
				Attributes: map[string]string{"team": "platform"},
				Selector: PolicySelector{
					CollectorIDs: []string{"collector-1", ""},
				},
			}),
			wantErr: true,
			errMsg:  "spec.selector.collectorIDs[1]",
		},
		{
			name: "invalid - collectorIDs contains whitespace-only entry",
			policy: newPolicy("ws-cid", RemoteAttributePolicySpec{
				Attributes: map[string]string{"team": "platform"},
				Selector: PolicySelector{
					CollectorIDs: []string{"   "},
				},
			}),
			wantErr: true,
			errMsg:  "must be non-empty",
		},
		{
			name: "valid - collectorIDs all populated",
			policy: newPolicy("ok-cids", RemoteAttributePolicySpec{
				Attributes: map[string]string{"team": "platform"},
				Selector: PolicySelector{
					CollectorIDs: []string{"collector-1", "collector-2", "collector-3"},
				},
			}),
			wantErr: false,
		},

		// Priority - mutable, no extra validation.
		{
			name: "valid - priority set",
			policy: newPolicy("with-priority", RemoteAttributePolicySpec{
				Attributes: map[string]string{"team": "platform"},
				Selector: PolicySelector{
					Matchers: []string{"environment=production"},
				},
				Priority: 50,
			}),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, err := (&remoteAttributePolicyValidator{}).ValidateCreate(ctx, tt.policy)
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

func TestRemoteAttributePolicy_ValidateUpdate(t *testing.T) {
	baseOld := newPolicy("test", RemoteAttributePolicySpec{
		Attributes: map[string]string{"team": "platform"},
		Selector: PolicySelector{
			Matchers: []string{"environment=production"},
		},
		Priority: 0,
	})

	tests := []struct {
		name    string
		oldObj  *RemoteAttributePolicy
		newObj  *RemoteAttributePolicy
		wantErr bool
		errMsg  string
	}{
		{
			name:   "valid - attributes updated",
			oldObj: baseOld,
			newObj: newPolicy("test", RemoteAttributePolicySpec{
				Attributes: map[string]string{
					"team":        "platform",
					"environment": "staging",
				},
				Selector: PolicySelector{
					Matchers: []string{"environment=production"},
				},
			}),
			wantErr: false,
		},
		{
			name:   "valid - selector matchers updated (selector is mutable)",
			oldObj: baseOld,
			newObj: newPolicy("test", RemoteAttributePolicySpec{
				Attributes: map[string]string{"team": "platform"},
				Selector: PolicySelector{
					Matchers: []string{"environment=staging"},
				},
			}),
			wantErr: false,
		},
		{
			name:   "valid - priority updated (priority is mutable)",
			oldObj: baseOld,
			newObj: newPolicy("test", RemoteAttributePolicySpec{
				Attributes: map[string]string{"team": "platform"},
				Selector: PolicySelector{
					Matchers: []string{"environment=production"},
				},
				Priority: 100,
			}),
			wantErr: false,
		},
		{
			name:   "invalid - update empties attributes",
			oldObj: baseOld,
			newObj: &RemoteAttributePolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
				Spec: RemoteAttributePolicySpec{
					Attributes: map[string]string{},
					Selector: PolicySelector{
						Matchers: []string{"environment=production"},
					},
				},
			},
			wantErr: true,
			errMsg:  "spec.attributes must contain at least one entry",
		},
		{
			name:   "invalid - update introduces reserved attribute prefix",
			oldObj: baseOld,
			newObj: newPolicy("test", RemoteAttributePolicySpec{
				Attributes: map[string]string{
					"team":         "platform",
					"collector.os": "linux",
				},
				Selector: PolicySelector{
					Matchers: []string{"environment=production"},
				},
			}),
			wantErr: true,
			errMsg:  "reserved prefix",
		},
		{
			name:   "invalid - update introduces empty attribute key",
			oldObj: baseOld,
			newObj: newPolicy("test", RemoteAttributePolicySpec{
				Attributes: map[string]string{
					"team": "platform",
					"":     "value",
				},
				Selector: PolicySelector{
					Matchers: []string{"environment=production"},
				},
			}),
			wantErr: true,
			errMsg:  "empty key",
		},
		{
			name:   "invalid - update introduces overlong value",
			oldObj: baseOld,
			newObj: newPolicy("test", RemoteAttributePolicySpec{
				Attributes: map[string]string{"team": strings.Repeat("a", 1025)},
				Selector: PolicySelector{
					Matchers: []string{"environment=production"},
				},
			}),
			wantErr: true,
			errMsg:  "exceeds the maximum",
		},
		{
			name:   "invalid - update introduces invalid matcher syntax",
			oldObj: baseOld,
			newObj: newPolicy("test", RemoteAttributePolicySpec{
				Attributes: map[string]string{"team": "platform"},
				Selector: PolicySelector{
					Matchers: []string{"environment==production"},
				},
			}),
			wantErr: true,
			errMsg:  "spec.selector.matchers[0]",
		},
		{
			name:   "invalid - update empties the selector entirely",
			oldObj: baseOld,
			newObj: &RemoteAttributePolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
				Spec: RemoteAttributePolicySpec{
					Attributes: map[string]string{"team": "platform"},
					Selector:   PolicySelector{},
				},
			},
			wantErr: true,
			errMsg:  "spec.selector must specify at least one of matchers or collectorIDs",
		},
		{
			name:   "invalid - update introduces empty collectorID entry",
			oldObj: baseOld,
			newObj: newPolicy("test", RemoteAttributePolicySpec{
				Attributes: map[string]string{"team": "platform"},
				Selector: PolicySelector{
					CollectorIDs: []string{"collector-1", ""},
				},
			}),
			wantErr: true,
			errMsg:  "spec.selector.collectorIDs[1]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, err := (&remoteAttributePolicyValidator{}).ValidateUpdate(ctx, tt.oldObj, tt.newObj)
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

func TestRemoteAttributePolicy_ValidateDelete(t *testing.T) {
	p := newPolicy("test", RemoteAttributePolicySpec{
		Attributes: map[string]string{"team": "platform"},
		Selector: PolicySelector{
			Matchers: []string{"environment=production"},
		},
	})
	warnings, err := (&remoteAttributePolicyValidator{}).ValidateDelete(context.Background(), p)
	assert.NoError(t, err)
	assert.Nil(t, warnings)
}

// TestRemoteAttributePolicy_validateAttributes_DefenseInDepth exercises the
// count-based safety net that guards against the schema MaxProperties marker
// being bypassed (e.g. because a controller writes directly to the API server
// without going through the OpenAPI layer).
func TestRemoteAttributePolicy_validateAttributes_DefenseInDepth(t *testing.T) {
	attrs := make(map[string]string, collectorMaxRemoteAttributes+1)
	for i := range collectorMaxRemoteAttributes + 1 {
		// fmtKey/itoa are defined in collector_webhook_test.go (same package).
		attrs[fmtKey(i)] = "v"
	}
	p := newPolicy("too-many", RemoteAttributePolicySpec{
		Attributes: attrs,
		Selector: PolicySelector{
			Matchers: []string{"environment=production"},
		},
	})

	_, err := (&remoteAttributePolicyValidator{}).ValidateCreate(context.Background(), p)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "exceeds the maximum")
	}
}
