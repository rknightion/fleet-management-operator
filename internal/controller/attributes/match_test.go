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
)

func TestSelectorMatch_EmptySelectorMatchesNothing(t *testing.T) {
	s := Selector{}
	assert.False(t, s.Match("any-id", map[string]string{"k": "v"}))
}

func TestSelectorMatch_CollectorIDListWins(t *testing.T) {
	s := Selector{CollectorIDs: []string{"a", "b", "c"}}
	assert.True(t, s.Match("b", nil))
	assert.False(t, s.Match("d", nil))
}

func TestSelectorMatch_CollectorIDORedWithMatchers(t *testing.T) {
	s := Selector{
		CollectorIDs: []string{"override-id"},
		Matchers:     []string{"env=prod"}, // attrs say staging, would otherwise reject
	}
	assert.True(t, s.Match("override-id", map[string]string{"env": "staging"}),
		"explicit ID list should match even when matchers would reject")
	assert.False(t, s.Match("other-id", map[string]string{"env": "staging"}),
		"matchers reject when ID is not in the override list")
}

func TestSelectorMatch_MatchersAreANDed(t *testing.T) {
	s := Selector{Matchers: []string{"env=prod", "region=us-east"}}
	assert.True(t, s.Match("c", map[string]string{"env": "prod", "region": "us-east"}))
	assert.False(t, s.Match("c", map[string]string{"env": "prod", "region": "eu-west"}))
	assert.False(t, s.Match("c", map[string]string{"env": "staging", "region": "us-east"}))
}

func TestSelectorMatch_OperatorSemantics(t *testing.T) {
	cases := []struct {
		name    string
		matcher string
		attrs   map[string]string
		want    bool
	}{
		{"equals true", "env=prod", map[string]string{"env": "prod"}, true},
		{"equals false", "env=prod", map[string]string{"env": "staging"}, false},
		{"not-equals true", "env!=prod", map[string]string{"env": "staging"}, true},
		{"not-equals false", "env!=prod", map[string]string{"env": "prod"}, false},
		{"regex match true", "env=~prod.*", map[string]string{"env": "prod-eu-west"}, true},
		{"regex match false", "env=~prod.*", map[string]string{"env": "staging"}, false},
		{"regex not-match true", "env!~prod.*", map[string]string{"env": "staging"}, true},
		{"regex not-match false", "env!~prod.*", map[string]string{"env": "prod-eu-west"}, false},
		{"missing key equals empty string is false for value", "env=prod", map[string]string{}, false},
		{"missing key not-equals value is true", "env!=prod", map[string]string{}, true},
		{"quoted value strips quotes", `env="prod"`, map[string]string{"env": "prod"}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := Selector{Matchers: []string{tt.matcher}}
			assert.Equal(t, tt.want, s.Match("c", tt.attrs))
		})
	}
}

func TestSelectorMatch_CollectorIDSyntheticKey(t *testing.T) {
	s := Selector{Matchers: []string{"collector.id=edge-host-42"}}
	assert.True(t, s.Match("edge-host-42", nil))
	assert.False(t, s.Match("other-host", nil))
}

func TestMatcherUsesKey(t *testing.T) {
	assert.True(t, MatcherUsesKey("env=prod", "env"))
	assert.True(t, MatcherUsesKey(" team =~ platform .* ", "team"))
	assert.False(t, MatcherUsesKey("env=prod", "team"))
	assert.False(t, MatcherUsesKey("not-a-matcher", "env"))
}
