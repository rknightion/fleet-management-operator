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
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Selector mirrors the v1alpha1.PolicySelector shape but stays in the
// attributes package so policy and collector controllers can share matcher
// evaluation without an import cycle.
type Selector struct {
	Matchers     []string
	CollectorIDs []string
}

// Match returns true if the given collector ID and attribute set match this
// selector.
//
//   - All Matchers must pass (AND-ed).
//   - CollectorIDs is OR-ed with the matchers result: a collector whose ID
//     appears in CollectorIDs always matches, regardless of attributes.
//   - An empty selector (no matchers and no IDs) matches NOTHING — a
//     partially-written Policy must not silently target every collector.
//
// Matchers may reference the synthetic key `collector.id` to compare against
// the collector's ID directly; otherwise key lookups go through the supplied
// attrs map.
func (s Selector) Match(collectorID string, attrs map[string]string) bool {
	if slices.Contains(s.CollectorIDs, collectorID) {
		return true
	}

	if len(s.Matchers) == 0 {
		// No matchers means "rely solely on CollectorIDs" — and we already
		// missed every entry in that list above, so this collector is out.
		return false
	}

	for _, m := range s.Matchers {
		ok, err := matchOne(m, collectorID, attrs)
		if err != nil || !ok {
			return false
		}
	}
	return true
}

// matcherPattern parses Prometheus-style matchers: key OP value where OP is
// one of =, !=, =~, !~. It is intentionally generous about whitespace and
// strips a single layer of double-quotes from the value so users can write
// either `env=prod` or `env="prod"`.
//
// Important: the order of operator alternatives is significant. `!=` and
// `=~` and `!~` must precede `=` so the longer prefixes win on greedy
// matching.
var matcherPattern = regexp.MustCompile(`^\s*([a-zA-Z_][a-zA-Z0-9_.]*)\s*(!=|=~|!~|=)\s*(.+?)\s*$`)

func matchOne(matcher, collectorID string, attrs map[string]string) (bool, error) {
	parts := matcherPattern.FindStringSubmatch(matcher)
	if parts == nil {
		return false, fmt.Errorf("invalid matcher syntax: %q", matcher)
	}
	key, op, raw := parts[1], parts[2], parts[3]

	value := raw
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}

	got := attrs[key]
	if key == "collector.id" {
		got = collectorID
	}

	switch op {
	case "=":
		return got == value, nil
	case "!=":
		return got != value, nil
	case "=~":
		re, err := regexp.Compile(value)
		if err != nil {
			return false, fmt.Errorf("invalid regex in matcher %q: %w", matcher, err)
		}
		return re.MatchString(got), nil
	case "!~":
		re, err := regexp.Compile(value)
		if err != nil {
			return false, fmt.Errorf("invalid regex in matcher %q: %w", matcher, err)
		}
		return !re.MatchString(got), nil
	}
	return false, fmt.Errorf("unknown operator %q in matcher %q", op, matcher)
}

// MatcherUsesKey returns true if the matcher references the given attribute
// key. Useful for narrowing reconcile fan-out: when a Collector's local
// attributes change, only re-evaluate Policies whose Matchers actually
// mention one of the changed keys.
func MatcherUsesKey(matcher, key string) bool {
	parts := matcherPattern.FindStringSubmatch(matcher)
	if parts == nil {
		return false
	}
	return parts[1] == key
}

// CompactMatchers returns the matchers with whitespace trimmed; used by tests
// and for stable status output.
func CompactMatchers(matchers []string) []string {
	out := make([]string, 0, len(matchers))
	for _, m := range matchers {
		out = append(out, strings.TrimSpace(m))
	}
	return out
}
