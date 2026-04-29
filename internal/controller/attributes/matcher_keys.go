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

const (
	// SyntheticHasNegationKey is a sentinel index entry emitted by MatcherKeys
	// for any matcher set containing a `!=` or `!~` operator. A negation
	// matcher (e.g. `team!=foo`) is intended to match Collectors that LACK
	// the referenced key — so the index, which is normally queried by the
	// Collector's existing attribute keys, would never enqueue the policy
	// for a Collector without that key. The selective Collector watch
	// handler must always query this bucket in addition to the Collector's
	// attribute keys to make negation-only policies reconcile correctly.
	SyntheticHasNegationKey = "__has_negation__"

	// SyntheticCollectorIDsOnlyKey is a sentinel index entry emitted by the
	// IndexField extractor wrapper (NOT by MatcherKeys directly, which only
	// sees matchers) when a policy/sync has a non-empty
	// `selector.collectorIDs` list and an empty `selector.matchers` set.
	// Such selectors return zero matcher keys; without this sentinel they
	// would be indexed under nothing and would never wake on Collector add.
	// The selective Collector watch handler must always query this bucket.
	SyntheticCollectorIDsOnlyKey = "__collector_ids_only__"
)

// MatcherKeys parses a slice of Prometheus-style matcher strings and returns
// the deduplicated set of label key names they reference. When any matcher
// uses a negation operator (`!=` or `!~`) it additionally emits the
// SyntheticHasNegationKey sentinel so the selective Collector watch handler
// can enqueue policies whose negation matchers should match Collectors that
// LACK the referenced key.
// Used to build an IndexField enabling selective Collector watch handlers.
func MatcherKeys(matchers []string) []string {
	seen := map[string]struct{}{}
	hasNegation := false
	for _, m := range matchers {
		parts := matcherPattern.FindStringSubmatch(m)
		if parts == nil {
			continue
		}
		seen[parts[1]] = struct{}{}
		if parts[2] == "!=" || parts[2] == "!~" {
			hasNegation = true
		}
	}
	if hasNegation {
		seen[SyntheticHasNegationKey] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	return keys
}
