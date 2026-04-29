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

// Package attributes contains pure helpers for computing the operations a
// controller must apply to a Fleet Management collector to bring its remote
// attributes from an observed state to a desired state. Phase 2 will add a
// Merge function in this package that combines layered desired states; Phase
// 1 only needs Diff.
package attributes

import (
	"sort"
	"strings"

	"github.com/grafana/fleet-management-operator/pkg/fleetclient"
)

// Diff computes the BulkUpdateCollectors Operations needed to move from
// observed to desired for the keys this caller owns.
//
//   - For each key in desired:
//   - if not present in observed: ADD
//   - if present in observed but with a different value: REPLACE
//   - For each key in previouslyOwned that is not in desired but is still
//     present in observed: REMOVE.
//
// Keys not in desired AND not in previouslyOwned are left untouched, even if
// they appear in observed — they belong to some other writer (or to nobody).
//
// The returned slice is sorted by path so callers (and tests) can rely on
// deterministic ordering.
func Diff(desired, observed map[string]string, previouslyOwned []string) []*fleetclient.Operation {
	// Intentionally nil (not preallocated): callers and tests rely on
	// a nil return when there is no work to do (e.g. equality checks
	// against `nil`). Use `//nolint:prealloc` semantics by leaving as nil.
	var ops []*fleetclient.Operation //nolint:prealloc

	// ADD / REPLACE for keys we want to set.
	for key, want := range desired {
		got, present := observed[key]
		switch {
		case !present:
			ops = append(ops, &fleetclient.Operation{
				Op:    fleetclient.OpAdd,
				Path:  remoteAttrPath(key),
				Value: want,
			})
		case got != want:
			ops = append(ops, &fleetclient.Operation{
				Op:    fleetclient.OpReplace,
				Path:  remoteAttrPath(key),
				Value: want,
			})
		}
	}

	// REMOVE for keys we previously owned but no longer want.
	desiredKeys := make(map[string]struct{}, len(desired))
	for k := range desired {
		desiredKeys[k] = struct{}{}
	}
	for _, key := range previouslyOwned {
		if _, stillDesired := desiredKeys[key]; stillDesired {
			continue
		}
		if _, present := observed[key]; !present {
			// Already gone server-side; nothing to do.
			continue
		}
		ops = append(ops, &fleetclient.Operation{
			Op:   fleetclient.OpRemove,
			Path: remoteAttrPath(key),
		})
	}

	sort.SliceStable(ops, func(i, j int) bool {
		return ops[i].Path < ops[j].Path
	})

	return ops
}

// remoteAttrPath builds the JSON-patch-style path for a remote-attribute key,
// applying RFC 6901 escaping so keys containing "/" or "~" round-trip safely.
// In practice attribute keys are well-formed, but the API accepts JSON Patch
// paths so we match the standard.
func remoteAttrPath(key string) string {
	escaped := strings.ReplaceAll(key, "~", "~0")
	escaped = strings.ReplaceAll(escaped, "/", "~1")
	return "/remote_attributes/" + escaped
}
