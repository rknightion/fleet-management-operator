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
	"strings"
	"testing"
)

func TestCollectorDiscovery_validatePollInterval(t *testing.T) {
	tests := []struct {
		name     string
		interval string
		wantErr  bool
		errMsg   string
	}{
		{name: "empty (uses default)", interval: "", wantErr: false},
		{name: "5m valid", interval: "5m", wantErr: false},
		{name: "1m at the floor", interval: "1m", wantErr: false},
		{name: "60s at the floor (alternate spelling)", interval: "60s", wantErr: false},
		{name: "1h valid", interval: "1h", wantErr: false},
		{name: "30s below floor", interval: "30s", wantErr: true, errMsg: "below the minimum"},
		{name: "0s rejected", interval: "0s", wantErr: true, errMsg: "below the minimum"},
		{name: "unparseable", interval: "five minutes", wantErr: true, errMsg: "not a valid Go duration"},
		{name: "negative duration", interval: "-1m", wantErr: true, errMsg: "below the minimum"},
		{name: "whitespace trimmed", interval: "  5m  ", wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cd := &CollectorDiscovery{
				Spec: CollectorDiscoverySpec{
					PollInterval: tt.interval,
				},
			}
			err := cd.validatePollInterval()
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePollInterval(%q) error = %v, wantErr %v", tt.interval, err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("validatePollInterval(%q) error %q should contain %q", tt.interval, err.Error(), tt.errMsg)
			}
		})
	}
}

func TestCollectorDiscovery_validateDiscoverySelector(t *testing.T) {
	tests := []struct {
		name     string
		selector PolicySelector
		wantErr  bool
		errMsg   string
	}{
		{
			name:     "empty selector is allowed (mirror everything)",
			selector: PolicySelector{},
			wantErr:  false,
		},
		{
			name: "valid matchers",
			selector: PolicySelector{
				Matchers: []string{"collector.os=linux", "env=prod"},
			},
			wantErr: false,
		},
		{
			name: "valid regex matchers",
			selector: PolicySelector{
				Matchers: []string{"team=~team-(a|b)"},
			},
			wantErr: false,
		},
		{
			name: "invalid matcher syntax (== instead of =)",
			selector: PolicySelector{
				Matchers: []string{"env==prod"},
			},
			wantErr: true,
			errMsg:  "invalid syntax",
		},
		{
			name: "matcher exceeding 200 char limit",
			selector: PolicySelector{
				Matchers: []string{"k=" + strings.Repeat("v", 200)},
			},
			wantErr: true,
			errMsg:  "200 character limit",
		},
		{
			name: "valid collectorIDs",
			selector: PolicySelector{
				CollectorIDs: []string{"edge-1", "edge-2"},
			},
			wantErr: false,
		},
		{
			name: "empty collectorID rejected",
			selector: PolicySelector{
				CollectorIDs: []string{"edge-1", "", "edge-3"},
			},
			wantErr: true,
			errMsg:  "is empty",
		},
		{
			name: "whitespace-only collectorID rejected",
			selector: PolicySelector{
				CollectorIDs: []string{"   "},
			},
			wantErr: true,
			errMsg:  "is empty",
		},
		{
			name: "matchers and collectorIDs combined",
			selector: PolicySelector{
				Matchers:     []string{"env=prod"},
				CollectorIDs: []string{"edge-1"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cd := &CollectorDiscovery{
				Spec: CollectorDiscoverySpec{
					Selector: tt.selector,
				},
			}
			err := cd.validateDiscoverySelector()
			if (err != nil) != tt.wantErr {
				t.Errorf("validateDiscoverySelector() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("validateDiscoverySelector() error %q should contain %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestCollectorDiscovery_validateTargetNamespace(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		wantErr   bool
	}{
		{name: "empty (default)", namespace: "", wantErr: false},
		{name: "valid", namespace: "fleet-mirror", wantErr: false},
		{name: "single char", namespace: "a", wantErr: false},
		{name: "uppercase rejected", namespace: "Fleet", wantErr: true},
		{name: "underscore rejected", namespace: "fleet_mirror", wantErr: true},
		{name: "leading hyphen rejected", namespace: "-fleet", wantErr: true},
		{name: "trailing hyphen rejected", namespace: "fleet-", wantErr: true},
		{name: "too long rejected", namespace: strings.Repeat("a", 64), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cd := &CollectorDiscovery{
				Spec: CollectorDiscoverySpec{
					TargetNamespace: tt.namespace,
				},
			}
			err := cd.validateTargetNamespace()
			if (err != nil) != tt.wantErr {
				t.Errorf("validateTargetNamespace(%q) error = %v, wantErr %v", tt.namespace, err, tt.wantErr)
			}
		})
	}
}

func TestCollectorDiscovery_validatePolicy(t *testing.T) {
	tests := []struct {
		name    string
		policy  DiscoveryPolicy
		wantErr bool
	}{
		{name: "empty defaults", policy: DiscoveryPolicy{}, wantErr: false},
		{name: "Keep + Skip", policy: DiscoveryPolicy{
			OnCollectorRemoved: DiscoveryOnRemovedKeep,
			OnConflict:         DiscoveryOnConflictSkip,
		}, wantErr: false},
		{name: "Delete + Skip", policy: DiscoveryPolicy{
			OnCollectorRemoved: DiscoveryOnRemovedDelete,
			OnConflict:         DiscoveryOnConflictSkip,
		}, wantErr: false},
		{name: "invalid OnCollectorRemoved", policy: DiscoveryPolicy{
			OnCollectorRemoved: "Wipe",
		}, wantErr: true},
		{name: "invalid OnConflict (TakeOwnership reserved)", policy: DiscoveryPolicy{
			OnConflict: "TakeOwnership",
		}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cd := &CollectorDiscovery{
				Spec: CollectorDiscoverySpec{Policy: tt.policy},
			}
			err := cd.validatePolicy()
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePolicy(%+v) error = %v, wantErr %v", tt.policy, err, tt.wantErr)
			}
		})
	}
}

func TestCollectorDiscovery_validateCollectorDiscovery_endToEnd(t *testing.T) {
	cd := &CollectorDiscovery{
		Spec: CollectorDiscoverySpec{
			PollInterval: "5m",
			Selector: PolicySelector{
				Matchers: []string{"env=prod"},
			},
			TargetNamespace: "fleet-mirror",
			Policy: DiscoveryPolicy{
				OnCollectorRemoved: DiscoveryOnRemovedKeep,
				OnConflict:         DiscoveryOnConflictSkip,
			},
		},
	}
	if _, err := cd.validateCollectorDiscovery(); err != nil {
		t.Fatalf("validateCollectorDiscovery() unexpected error: %v", err)
	}
}
