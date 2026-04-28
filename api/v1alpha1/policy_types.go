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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PolicySelector picks the Collectors a RemoteAttributePolicy applies to.
//
// A Collector matches the selector if it satisfies all Matchers (AND-ed
// together) OR its ID appears in CollectorIDs. An empty selector matches
// nothing — this is intentional defensive behavior so a partially-written
// Policy never accidentally targets every collector.
type PolicySelector struct {
	// Matchers in Prometheus Alertmanager syntax (=, !=, =~, !~), evaluated
	// against the matched Collector's local attributes plus its ID under
	// the synthetic key "collector.id".
	// +optional
	// +kubebuilder:validation:MaxItems=100
	Matchers []string `json:"matchers,omitempty"`

	// CollectorIDs is an explicit list of collector IDs this policy targets.
	// OR'd with Matchers — a Collector matches if its ID appears here, even
	// if the Matchers would otherwise reject it.
	// +optional
	// +kubebuilder:validation:MaxItems=1000
	CollectorIDs []string `json:"collectorIDs,omitempty"`
}

// RemoteAttributePolicySpec defines a bulk attribute assignment to all
// collectors matched by a selector. Within a single Collector, this layer's
// values are overridden by the Collector CR's own spec.RemoteAttributes —
// the Policy is a default, the Collector CR is an override.
type RemoteAttributePolicySpec struct {
	Selector PolicySelector `json:"selector"`

	// Attributes applied to every matched collector. Reserved-prefix keys
	// ("collector.") are rejected by the webhook.
	// +kubebuilder:validation:MaxProperties=100
	Attributes map[string]string `json:"attributes"`

	// Priority breaks ties when multiple policies match the same collector
	// and set the same key — higher Priority wins. Equal-priority ties are
	// broken alphabetically by namespaced name to keep behavior
	// deterministic across reconciliations.
	// +optional
	// +kubebuilder:default=0
	Priority int32 `json:"priority,omitempty"`
}

// RemoteAttributePolicyStatus reflects the controller's view of which
// collectors this policy is currently applied to.
type RemoteAttributePolicyStatus struct {
	// ObservedGeneration reflects the generation of the most recently
	// observed Policy spec.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// MatchedCollectorIDs is the sorted set of collector IDs (from
	// Collector CRs in the same namespace) currently matched by this
	// policy's selector.
	// +optional
	MatchedCollectorIDs []string `json:"matchedCollectorIDs,omitempty"`

	// Conditions represent the current state of the Policy.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=fmrap
// +kubebuilder:printcolumn:name="Priority",type="integer",JSONPath=".spec.priority"
// +kubebuilder:printcolumn:name="Matched",type="integer",JSONPath=".status.matchedCollectorIDs"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// RemoteAttributePolicy applies a bulk set of remote attributes to every
// Collector matched by its selector.
type RemoteAttributePolicy struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of the Policy.
	// +required
	Spec RemoteAttributePolicySpec `json:"spec"`

	// status defines the observed state of the Policy.
	// +optional
	Status RemoteAttributePolicyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// RemoteAttributePolicyList contains a list of RemoteAttributePolicy.
type RemoteAttributePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []RemoteAttributePolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RemoteAttributePolicy{}, &RemoteAttributePolicyList{})
}
