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
			name:     "single matcher",
			matchers: []string{"env=prod"},
			want:     []string{"env"},
		},
		{
			name:     "multiple distinct keys",
			matchers: []string{"env=prod", "region=us-east", "team!=team-a"},
			want:     []string{"env", "region", "team"},
		},
		{
			name:     "duplicate key deduplicated",
			matchers: []string{"env=prod", "env!=staging"},
			want:     []string{"env"},
		},
		{
			name:     "regex operators",
			matchers: []string{"env=~prod.*", "region!~eu-.*"},
			want:     []string{"env", "region"},
		},
		{
			name:     "invalid matcher skipped",
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
