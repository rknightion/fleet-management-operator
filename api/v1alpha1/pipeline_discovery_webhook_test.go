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
	"github.com/stretchr/testify/require"
)

// validPipelineDiscovery is a minimum-viable spec that passes every
// validation rule. Tests that want to exercise individual failure modes
// start from this and mutate one field.
func validPipelineDiscovery() *PipelineDiscovery {
	ct := ConfigTypeAlloy
	return &PipelineDiscovery{
		Spec: PipelineDiscoverySpec{
			PollInterval: "5m",
			Selector: PipelineDiscoverySelector{
				ConfigType: &ct,
			},
			TargetNamespace: "fleet-pipelines",
			ImportMode:      PipelineDiscoveryImportModeAdopt,
			Policy: PipelineDiscoveryPolicy{
				OnPipelineRemoved: PipelineDiscoveryOnRemovedKeep,
			},
		},
	}
}

func boolPtr(b bool) *bool { return &b }

func TestPipelineDiscovery_validatePollInterval(t *testing.T) { //nolint:dupl
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
			pd := &PipelineDiscovery{
				Spec: PipelineDiscoverySpec{
					PollInterval: tt.interval,
				},
			}
			err := pd.validatePipelineDiscoveryPollInterval()
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePipelineDiscoveryPollInterval(%q) error = %v, wantErr %v", tt.interval, err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("validatePipelineDiscoveryPollInterval(%q) error %q should contain %q", tt.interval, err.Error(), tt.errMsg)
			}
		})
	}
}

func TestPipelineDiscovery_validateSelector(t *testing.T) {
	alloy := ConfigTypeAlloy
	otel := ConfigTypeOpenTelemetryCollector
	invalid := ConfigType("Invalid")

	tests := []struct {
		name     string
		selector PipelineDiscoverySelector
		wantErr  bool
		errMsg   string
	}{
		{
			name:     "empty selector is allowed (import everything)",
			selector: PipelineDiscoverySelector{},
			wantErr:  false,
		},
		{
			name:     "valid configType Alloy",
			selector: PipelineDiscoverySelector{ConfigType: &alloy},
			wantErr:  false,
		},
		{
			name:     "valid configType OpenTelemetryCollector",
			selector: PipelineDiscoverySelector{ConfigType: &otel},
			wantErr:  false,
		},
		{
			name:     "invalid configType rejected",
			selector: PipelineDiscoverySelector{ConfigType: &invalid},
			wantErr:  true,
			errMsg:   "is invalid",
		},
		{
			name:     "enabled=true is valid",
			selector: PipelineDiscoverySelector{Enabled: boolPtr(true)},
			wantErr:  false,
		},
		{
			name:     "enabled=false is valid",
			selector: PipelineDiscoverySelector{Enabled: boolPtr(false)},
			wantErr:  false,
		},
		{
			name:     "configType and enabled combined",
			selector: PipelineDiscoverySelector{ConfigType: &alloy, Enabled: boolPtr(true)},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pd := &PipelineDiscovery{
				Spec: PipelineDiscoverySpec{
					Selector: tt.selector,
				},
			}
			err := pd.validatePipelineDiscoverySelector()
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePipelineDiscoverySelector() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("validatePipelineDiscoverySelector() error %q should contain %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestPipelineDiscovery_validateTargetNamespace(t *testing.T) { //nolint:dupl
	tests := []struct {
		name      string
		namespace string
		wantErr   bool
	}{
		{name: "empty (default)", namespace: "", wantErr: false},
		{name: "valid", namespace: "fleet-pipelines", wantErr: false},
		{name: "single char", namespace: "a", wantErr: false},
		{name: "uppercase rejected", namespace: "Fleet", wantErr: true},
		{name: "underscore rejected", namespace: "fleet_pipelines", wantErr: true},
		{name: "leading hyphen rejected", namespace: "-fleet", wantErr: true},
		{name: "trailing hyphen rejected", namespace: "fleet-", wantErr: true},
		{name: "too long rejected", namespace: strings.Repeat("a", 64), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pd := &PipelineDiscovery{
				Spec: PipelineDiscoverySpec{
					TargetNamespace: tt.namespace,
				},
			}
			err := pd.validatePipelineDiscoveryTargetNamespace()
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePipelineDiscoveryTargetNamespace(%q) error = %v, wantErr %v", tt.namespace, err, tt.wantErr)
			}
		})
	}
}

func TestPipelineDiscovery_validateImportMode(t *testing.T) {
	tests := []struct {
		name    string
		mode    PipelineDiscoveryImportMode
		wantErr bool
		errMsg  string
	}{
		{name: "empty (uses default Adopt)", mode: "", wantErr: false},
		{name: "Adopt valid", mode: PipelineDiscoveryImportModeAdopt, wantErr: false},
		{name: "ReadOnly valid", mode: PipelineDiscoveryImportModeReadOnly, wantErr: false},
		{name: "invalid mode rejected", mode: "Mirror", wantErr: true, errMsg: "is invalid"},
		{name: "lowercase rejected", mode: "adopt", wantErr: true, errMsg: "is invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pd := &PipelineDiscovery{
				Spec: PipelineDiscoverySpec{
					ImportMode: tt.mode,
				},
			}
			err := pd.validatePipelineDiscoveryImportMode()
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePipelineDiscoveryImportMode(%q) error = %v, wantErr %v", tt.mode, err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("validatePipelineDiscoveryImportMode(%q) error %q should contain %q", tt.mode, err.Error(), tt.errMsg)
			}
		})
	}
}

func TestPipelineDiscovery_validatePolicy(t *testing.T) {
	tests := []struct {
		name    string
		policy  PipelineDiscoveryPolicy
		wantErr bool
		errMsg  string
	}{
		{name: "empty defaults", policy: PipelineDiscoveryPolicy{}, wantErr: false},
		{name: "Keep valid", policy: PipelineDiscoveryPolicy{OnPipelineRemoved: PipelineDiscoveryOnRemovedKeep}, wantErr: false},
		{name: "Delete valid", policy: PipelineDiscoveryPolicy{OnPipelineRemoved: PipelineDiscoveryOnRemovedDelete}, wantErr: false},
		{name: "invalid OnPipelineRemoved", policy: PipelineDiscoveryPolicy{OnPipelineRemoved: "Wipe"}, wantErr: true, errMsg: "is invalid"},
		{name: "Preserve rejected", policy: PipelineDiscoveryPolicy{OnPipelineRemoved: "Preserve"}, wantErr: true, errMsg: "is invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pd := &PipelineDiscovery{
				Spec: PipelineDiscoverySpec{Policy: tt.policy},
			}
			err := pd.validatePipelineDiscoveryPolicy()
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePipelineDiscoveryPolicy(%+v) error = %v, wantErr %v", tt.policy, err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("validatePipelineDiscoveryPolicy(%+v) error %q should contain %q", tt.policy, err.Error(), tt.errMsg)
			}
		})
	}
}

func TestPipelineDiscovery_validatePipelineDiscovery_endToEnd(t *testing.T) {
	ct := ConfigTypeAlloy
	pd := &PipelineDiscovery{
		Spec: PipelineDiscoverySpec{
			PollInterval: "5m",
			Selector: PipelineDiscoverySelector{
				ConfigType: &ct,
				Enabled:    boolPtr(true),
			},
			TargetNamespace: "fleet-pipelines",
			ImportMode:      PipelineDiscoveryImportModeAdopt,
			Policy: PipelineDiscoveryPolicy{
				OnPipelineRemoved: PipelineDiscoveryOnRemovedKeep,
			},
		},
	}
	if _, err := pd.validatePipelineDiscovery(); err != nil {
		t.Fatalf("validatePipelineDiscovery() unexpected error: %v", err)
	}
}

// TestPipelineDiscovery_ValidateCreate covers the webhook entry point used
// by the API server on every create. The plumbing is trivial — it forwards
// to validatePipelineDiscovery — but exercising the entry point directly
// catches regressions where someone wires a different validator into the
// registration call.
func TestPipelineDiscovery_ValidateCreate(t *testing.T) {
	ctx := context.Background()
	v := &PipelineDiscoveryValidator{}

	t.Run("valid minimal spec passes", func(t *testing.T) {
		obj := &PipelineDiscovery{
			Spec: PipelineDiscoverySpec{
				PollInterval: "5m",
			},
		}
		warnings, err := v.ValidateCreate(ctx, obj)
		require.NoError(t, err)
		// empty selector emits a warning but no error
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "spec.selector is empty")
	})

	t.Run("valid full spec passes", func(t *testing.T) {
		obj := validPipelineDiscovery()
		warnings, err := v.ValidateCreate(ctx, obj)
		require.NoError(t, err)
		assert.Empty(t, warnings, "fully-populated spec must not emit warnings")
	})

	t.Run("invalid pollInterval too short is rejected", func(t *testing.T) {
		obj := validPipelineDiscovery()
		obj.Spec.PollInterval = "30s"
		_, err := v.ValidateCreate(ctx, obj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "below the minimum")
	})

	t.Run("invalid pollInterval unparseable is rejected", func(t *testing.T) {
		obj := validPipelineDiscovery()
		obj.Spec.PollInterval = "two-hours"
		_, err := v.ValidateCreate(ctx, obj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a valid Go duration")
	})

	t.Run("zero pollInterval is rejected", func(t *testing.T) {
		obj := validPipelineDiscovery()
		obj.Spec.PollInterval = "0s"
		_, err := v.ValidateCreate(ctx, obj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "below the minimum")
	})

	t.Run("invalid configType in selector is rejected", func(t *testing.T) {
		obj := validPipelineDiscovery()
		bad := ConfigType("Unknown")
		obj.Spec.Selector.ConfigType = &bad
		_, err := v.ValidateCreate(ctx, obj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is invalid")
	})

	t.Run("invalid targetNamespace uppercase is rejected", func(t *testing.T) {
		obj := validPipelineDiscovery()
		obj.Spec.TargetNamespace = "Fleet"
		_, err := v.ValidateCreate(ctx, obj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a valid Kubernetes namespace name")
	})

	t.Run("invalid targetNamespace special chars is rejected", func(t *testing.T) {
		obj := validPipelineDiscovery()
		obj.Spec.TargetNamespace = "fleet_pipelines"
		_, err := v.ValidateCreate(ctx, obj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a valid Kubernetes namespace name")
	})

	t.Run("invalid targetNamespace too long is rejected", func(t *testing.T) {
		obj := validPipelineDiscovery()
		obj.Spec.TargetNamespace = strings.Repeat("a", 64)
		_, err := v.ValidateCreate(ctx, obj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a valid Kubernetes namespace name")
	})

	t.Run("invalid importMode is rejected", func(t *testing.T) {
		obj := validPipelineDiscovery()
		obj.Spec.ImportMode = "Mirror"
		_, err := v.ValidateCreate(ctx, obj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is invalid")
	})

	t.Run("invalid onPipelineRemoved is rejected", func(t *testing.T) {
		obj := validPipelineDiscovery()
		obj.Spec.Policy.OnPipelineRemoved = "Wipe"
		_, err := v.ValidateCreate(ctx, obj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is invalid")
	})

	t.Run("empty selector emits warning but is allowed", func(t *testing.T) {
		obj := validPipelineDiscovery()
		obj.Spec.Selector = PipelineDiscoverySelector{}
		warnings, err := v.ValidateCreate(ctx, obj)
		require.NoError(t, err, "empty selector is a legitimate (if unscoped) configuration")
		require.Len(t, warnings, 1, "empty selector must surface the large-fleet warning")
		assert.Contains(t, warnings[0], "spec.selector is empty")
	})
}

// TestPipelineDiscovery_ValidateUpdate is the update-path mirror. CRD fields
// are all mutable, so the update path runs the same validation as create (and
// gets the same warnings).
func TestPipelineDiscovery_ValidateUpdate(t *testing.T) {
	ctx := context.Background()
	v := &PipelineDiscoveryValidator{}

	t.Run("valid pollInterval change passes", func(t *testing.T) {
		oldObj := validPipelineDiscovery()
		newObj := validPipelineDiscovery()
		newObj.Spec.PollInterval = "10m"
		warnings, err := v.ValidateUpdate(ctx, oldObj, newObj)
		require.NoError(t, err)
		assert.Empty(t, warnings)
	})

	t.Run("update with invalid targetNamespace is rejected", func(t *testing.T) {
		oldObj := validPipelineDiscovery()
		newObj := validPipelineDiscovery()
		newObj.Spec.TargetNamespace = "Invalid_Namespace"
		_, err := v.ValidateUpdate(ctx, oldObj, newObj)
		require.Error(t, err, "DNS-1123 violation in targetNamespace must reject the update")
	})

	t.Run("update switching importMode is valid", func(t *testing.T) {
		oldObj := validPipelineDiscovery()
		newObj := validPipelineDiscovery()
		newObj.Spec.ImportMode = PipelineDiscoveryImportModeReadOnly
		warnings, err := v.ValidateUpdate(ctx, oldObj, newObj)
		require.NoError(t, err)
		assert.Empty(t, warnings)
	})

	t.Run("update with invalid importMode is rejected", func(t *testing.T) {
		oldObj := validPipelineDiscovery()
		newObj := validPipelineDiscovery()
		newObj.Spec.ImportMode = "SyncOnly"
		_, err := v.ValidateUpdate(ctx, oldObj, newObj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is invalid")
	})

	t.Run("update to empty selector emits warning but is allowed", func(t *testing.T) {
		oldObj := validPipelineDiscovery()
		newObj := validPipelineDiscovery()
		newObj.Spec.Selector = PipelineDiscoverySelector{}
		warnings, err := v.ValidateUpdate(ctx, oldObj, newObj)
		require.NoError(t, err)
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "spec.selector is empty")
	})
}

// TestPipelineDiscovery_ValidateDelete is a sanity check on the no-op delete
// path. The method must not produce errors or warnings.
func TestPipelineDiscovery_ValidateDelete(t *testing.T) {
	ctx := context.Background()
	v := &PipelineDiscoveryValidator{}
	warnings, err := v.ValidateDelete(ctx, validPipelineDiscovery())
	require.NoError(t, err)
	assert.Empty(t, warnings)
}
