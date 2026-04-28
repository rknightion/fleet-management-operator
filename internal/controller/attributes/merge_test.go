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

func TestMerge_EmptyReturnsEmpty(t *testing.T) {
	desired, owners := Merge()
	assert.Empty(t, desired)
	assert.Empty(t, owners)
}

func TestMerge_SingleLayer(t *testing.T) {
	desired, owners := Merge(Layer{
		Kind:  "Collector",
		Owner: "ns/c1",
		Attrs: map[string]string{"env": "prod", "region": "us-east"},
	})
	assert.Equal(t, map[string]string{"env": "prod", "region": "us-east"}, desired)
	assert.Equal(t, []KeyOwnership{
		{Key: "env", Kind: "Collector", Owner: "ns/c1", Value: "prod"},
		{Key: "region", Kind: "Collector", Owner: "ns/c1", Value: "us-east"},
	}, owners)
}

func TestMerge_FirstLayerWinsOnKeyCollision(t *testing.T) {
	desired, owners := Merge(
		Layer{Kind: "Collector", Owner: "ns/c1", Attrs: map[string]string{"env": "staging"}},
		Layer{Kind: "RemoteAttributePolicy", Owner: "ns/p1", Attrs: map[string]string{"env": "prod", "team": "platform"}},
	)
	assert.Equal(t, map[string]string{"env": "staging", "team": "platform"}, desired)
	assert.Equal(t, []KeyOwnership{
		{Key: "env", Kind: "Collector", Owner: "ns/c1", Value: "staging"},
		{Key: "team", Kind: "RemoteAttributePolicy", Owner: "ns/p1", Value: "platform"},
	}, owners)
}

func TestMerge_DisjointLayersAreUnioned(t *testing.T) {
	desired, owners := Merge(
		Layer{Kind: "Collector", Owner: "ns/c1", Attrs: map[string]string{"env": "prod"}},
		Layer{Kind: "RemoteAttributePolicy", Owner: "ns/p1", Attrs: map[string]string{"team": "platform"}},
	)
	assert.Equal(t, map[string]string{"env": "prod", "team": "platform"}, desired)
	assert.Equal(t, []KeyOwnership{
		{Key: "env", Kind: "Collector", Owner: "ns/c1", Value: "prod"},
		{Key: "team", Kind: "RemoteAttributePolicy", Owner: "ns/p1", Value: "platform"},
	}, owners)
}

func TestMerge_OwnersSortedByKey(t *testing.T) {
	_, owners := Merge(Layer{
		Kind:  "Collector",
		Owner: "ns/c1",
		Attrs: map[string]string{"zeta": "z", "alpha": "a", "mid": "m"},
	})
	keys := make([]string, 0, len(owners))
	for _, o := range owners {
		keys = append(keys, o.Key)
	}
	assert.Equal(t, []string{"alpha", "mid", "zeta"}, keys)
}

func TestMerge_NilLayerAttrsAreSkipped(t *testing.T) {
	desired, owners := Merge(
		Layer{Kind: "Collector", Owner: "ns/c1", Attrs: nil},
		Layer{Kind: "RemoteAttributePolicy", Owner: "ns/p1", Attrs: map[string]string{"env": "prod"}},
	)
	assert.Equal(t, map[string]string{"env": "prod"}, desired)
	assert.Equal(t, []KeyOwnership{
		{Key: "env", Kind: "RemoteAttributePolicy", Owner: "ns/p1", Value: "prod"},
	}, owners)
}
