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

// Layer represents one input to the precedence merge: a kind label, an
// identifier for the owner CR, and the key/value attributes this layer
// claims. The Kind/Owner fields flow through to KeyOwnership records on the
// Collector status so the controller can later detect when a layer drops a
// key and emit the appropriate REMOVE op.
type Layer struct {
	Kind  string
	Owner string
	Attrs map[string]string
}

// KeyOwnership records the layer that owns a single attribute key after the
// merge. The controller converts this into the v1alpha1.AttributeOwnership
// status entry; the attributes package stays free of any v1alpha1 import to
// keep the merge logic pure and reusable.
type KeyOwnership struct {
	Key   string
	Kind  string
	Owner string
	Value string
}

// Merge combines a sequence of Layers in precedence order — the first layer
// wins on key collisions. Callers pass layers in highest-to-lowest precedence:
// for Phase 2 that's `Merge(collectorLayer, policyLayer)`; Phase 3 will
// prepend an ExternalAttributeSync layer.
//
// The returned desired map and owners slice are aligned: every key in
// desired has exactly one owners entry. Owners are returned sorted by Key so
// callers and tests get deterministic output.
func Merge(layers ...Layer) (desired map[string]string, owners []KeyOwnership) {
	desired = map[string]string{}
	ownerByKey := map[string]KeyOwnership{}

	for _, layer := range layers {
		for k, v := range layer.Attrs {
			if _, claimed := desired[k]; claimed {
				continue
			}
			desired[k] = v
			ownerByKey[k] = KeyOwnership{
				Key:   k,
				Kind:  layer.Kind,
				Owner: layer.Owner,
				Value: v,
			}
		}
	}

	owners = make([]KeyOwnership, 0, len(ownerByKey))
	for _, o := range ownerByKey {
		owners = append(owners, o)
	}
	sortByKey(owners)

	return desired, owners
}

func sortByKey(owners []KeyOwnership) {
	// Tiny insertion sort — owners is bounded to ~100 entries (the per-collector
	// attribute cap), and avoiding the sort import keeps this file dependency-free.
	for i := 1; i < len(owners); i++ {
		j := i
		for j > 0 && owners[j-1].Key > owners[j].Key {
			owners[j-1], owners[j] = owners[j], owners[j-1]
			j--
		}
	}
}
