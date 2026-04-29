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
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatcherKeys(t *testing.T) {
	cases := []struct {
		name     string
		matchers []string
		want     []string
	}{
		{
			name:     "empty input",
			matchers: nil,
			want:     []string{},
		},
		{
			name:     "single positive matcher",
			matchers: []string{"env=prod"},
			want:     []string{"env"},
		},
		{
			name:     "pure positive matchers (no synthetic key)",
			matchers: []string{"env=prod", "region=us-east"},
			want:     []string{"env", "region"},
		},
		{
			name:     "duplicate key deduplicated (positive only)",
			matchers: []string{"env=prod", "env=staging"},
			want:     []string{"env"},
		},
		{
			name:     "single negation matcher emits synthetic key plus LHS",
			matchers: []string{"team!=team-a"},
			want:     []string{"team", SyntheticHasNegationKey},
		},
		{
			name:     "mixed positive and negation: synthetic key plus all LHS",
			matchers: []string{"env=prod", "region=us-east", "team!=team-a"},
			want:     []string{"env", "region", "team", SyntheticHasNegationKey},
		},
		{
			name:     "duplicate key with negation collapses LHS but emits synthetic",
			matchers: []string{"env=prod", "env!=staging"},
			want:     []string{"env", SyntheticHasNegationKey},
		},
		{
			name:     "pure regex match (no synthetic key)",
			matchers: []string{"env=~prod.*"},
			want:     []string{"env"},
		},
		{
			name:     "multiple positive regex (no synthetic key)",
			matchers: []string{"env=~prod.*", "region=~us-.*"},
			want:     []string{"env", "region"},
		},
		{
			name:     "pure negation regex emits synthetic key",
			matchers: []string{"region!~eu-.*"},
			want:     []string{"region", SyntheticHasNegationKey},
		},
		{
			name:     "mixed regex operators: synthetic key only when negation present",
			matchers: []string{"env=~prod.*", "region!~eu-.*"},
			want:     []string{"env", "region", SyntheticHasNegationKey},
		},
		{
			name:     "invalid matcher skipped, valid positive remains",
			matchers: []string{"not-a-matcher", "env=prod"},
			want:     []string{"env"},
		},
		{
			name:     "all invalid matchers",
			matchers: []string{"not-a-matcher", "also-not-valid"},
			want:     []string{},
		},
		{
			name:     "dotted key name",
			matchers: []string{"collector.id=edge-host-42"},
			want:     []string{"collector.id"},
		},
		{
			name:     "whitespace around key and operator",
			matchers: []string{" env = prod "},
			want:     []string{"env"},
		},
		{
			name:     "whitespace around negation operator",
			matchers: []string{" team != team-a "},
			want:     []string{"team", SyntheticHasNegationKey},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := MatcherKeys(tt.matchers)
			sort.Strings(got)
			wantSorted := make([]string, len(tt.want))
			copy(wantSorted, tt.want)
			sort.Strings(wantSorted)
			assert.Equal(t, wantSorted, got)
		})
	}
}
