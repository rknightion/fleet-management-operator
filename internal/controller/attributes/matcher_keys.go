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

// MatcherKeys parses a slice of Prometheus-style matcher strings and returns
// the deduplicated set of label key names they reference.
// Used to build an IndexField enabling selective Collector watch handlers.
func MatcherKeys(matchers []string) []string {
	seen := map[string]struct{}{}
	for _, m := range matchers {
		parts := matcherPattern.FindStringSubmatch(m)
		if parts == nil {
			continue
		}
		seen[parts[1]] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	return keys
}
