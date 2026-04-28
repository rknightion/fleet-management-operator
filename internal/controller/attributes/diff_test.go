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

package attributes

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/grafana/fleet-management-operator/pkg/fleetclient"
)

func TestDiff(t *testing.T) {
	tests := []struct {
		name             string
		desired          map[string]string
		observed         map[string]string
		previouslyOwned  []string
		expected         []*fleetclient.Operation
		expectedOpsCount int
	}{
		{
			name:     "empty desired and observed",
			desired:  nil,
			observed: nil,
			expected: nil,
		},
		{
			name:    "single new key produces ADD",
			desired: map[string]string{"env": "prod"},
			expected: []*fleetclient.Operation{
				{Op: fleetclient.OpAdd, Path: "/remote_attributes/env", Value: "prod"},
			},
		},
		{
			name:     "matching desired/observed produces no ops",
			desired:  map[string]string{"env": "prod"},
			observed: map[string]string{"env": "prod"},
			expected: nil,
		},
		{
			name:     "differing value produces REPLACE",
			desired:  map[string]string{"env": "staging"},
			observed: map[string]string{"env": "prod"},
			expected: []*fleetclient.Operation{
				{Op: fleetclient.OpReplace, Path: "/remote_attributes/env", Value: "staging"},
			},
		},
		{
			name:            "previously owned but no longer desired produces REMOVE",
			desired:         nil,
			observed:        map[string]string{"env": "prod"},
			previouslyOwned: []string{"env"},
			expected: []*fleetclient.Operation{
				{Op: fleetclient.OpRemove, Path: "/remote_attributes/env"},
			},
		},
		{
			name:            "previously owned and already gone server-side produces no REMOVE",
			desired:         nil,
			observed:        nil,
			previouslyOwned: []string{"env"},
			expected:        nil,
		},
		{
			name:            "key not owned and not desired is left alone",
			desired:         nil,
			observed:        map[string]string{"foreign": "value"},
			previouslyOwned: nil,
			expected:        nil,
		},
		{
			name:            "mixed ADD + REPLACE + REMOVE returned in path order",
			desired:         map[string]string{"region": "us-east", "env": "prod"},
			observed:        map[string]string{"env": "staging", "old": "x"},
			previouslyOwned: []string{"old"},
			expected: []*fleetclient.Operation{
				{Op: fleetclient.OpReplace, Path: "/remote_attributes/env", Value: "prod"},
				{Op: fleetclient.OpRemove, Path: "/remote_attributes/old"},
				{Op: fleetclient.OpAdd, Path: "/remote_attributes/region", Value: "us-east"},
			},
		},
		{
			name:    "key with slash and tilde is RFC 6901 escaped",
			desired: map[string]string{"a/b~c": "v"},
			expected: []*fleetclient.Operation{
				{Op: fleetclient.OpAdd, Path: "/remote_attributes/a~1b~0c", Value: "v"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Diff(tt.desired, tt.observed, tt.previouslyOwned)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestRemoteAttrPath(t *testing.T) {
	assert.Equal(t, "/remote_attributes/env", remoteAttrPath("env"))
	assert.Equal(t, "/remote_attributes/a~1b", remoteAttrPath("a/b"))
	assert.Equal(t, "/remote_attributes/a~0b", remoteAttrPath("a~b"))
	assert.Equal(t, "/remote_attributes/a~01b", remoteAttrPath("a~1b"))
}
