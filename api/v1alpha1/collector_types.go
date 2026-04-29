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

// CollectorType mirrors the Fleet Management collector type enum and is set
// by the controller from observed state — it is read-only on the spec.
// +kubebuilder:validation:Enum=Alloy;OpenTelemetryCollector;Unspecified
type CollectorType string

const (
	CollectorTypeAlloy                  CollectorType = "Alloy"
	CollectorTypeOpenTelemetryCollector CollectorType = "OpenTelemetryCollector"
	CollectorTypeUnspecified            CollectorType = "Unspecified"
)

// AttributeOwnerKind identifies which CR owns a remote-attribute key on a
// collector. Phase 1 only writes `Collector`; later phases add the others
// without breaking the schema.
// +kubebuilder:validation:Enum=Collector;RemoteAttributePolicy;ExternalAttributeSync
type AttributeOwnerKind string

const (
	AttributeOwnerCollector             AttributeOwnerKind = "Collector"
	AttributeOwnerRemoteAttributePolicy AttributeOwnerKind = "RemoteAttributePolicy"
	AttributeOwnerExternalAttributeSync AttributeOwnerKind = "ExternalAttributeSync"
)

// CollectorSpec defines the desired state of a Fleet Management collector.
//
// Note: collectors register themselves with Fleet Management via
// RegisterCollector — this CR does not create them. spec.id binds the CR to
// an already-registered collector; if that collector has not yet registered,
// reconcile will keep retrying and surface the situation in status.
type CollectorSpec struct {
	// ID is the Fleet Management collector ID. Required and immutable after
	// creation. Immutability is declared via a CEL rule so the API server
	// enforces it independently of the validating webhook (defence-in-depth
	// and discoverable to schema consumers).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec.id is immutable"
	ID string `json:"id"`

	// Name is the optional display name set on the collector in Fleet
	// Management. If empty, the existing server-side name is preserved.
	// +optional
	Name string `json:"name,omitempty"`

	// Enabled toggles the collector in Fleet Management. nil leaves the
	// existing server-side value untouched (so that the operator does not
	// fight a value set elsewhere unless the user explicitly wants to).
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// RemoteAttributes managed by this CR. Keys with prefix "collector." are
	// reserved by Fleet Management and rejected by the API server (CEL) and
	// the validating webhook. Each value is capped at 1024 characters by
	// the admission webhook — values are user-facing strings, not
	// configuration blobs, so the cap protects etcd. Removing a key from
	// this map removes it from Fleet (delete-detected via
	// status.attributeOwners).
	// +kubebuilder:validation:MaxProperties=100
	// +kubebuilder:validation:XValidation:rule="self.all(k, k.size() > 0)",message="keys must not be empty"
	// +kubebuilder:validation:XValidation:rule="self.all(k, !k.startsWith('collector.'))",message="keys must not use the reserved 'collector.' prefix"
	// +kubebuilder:validation:XValidation:rule="self.all(k, self[k].size() <= 1024)",message="values must be 1024 characters or fewer"
	// +optional
	RemoteAttributes map[string]string `json:"remoteAttributes,omitempty"`
}

// CollectorStatus reflects observed state from Fleet Management plus the
// operator's bookkeeping for delete-detection.
type CollectorStatus struct {
	// ObservedGeneration reflects the generation of the most recently
	// observed Collector spec.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Registered is true if the collector has been observed in Fleet
	// Management (i.e. it has called RegisterCollector at least once).
	// +optional
	Registered bool `json:"registered,omitempty"`

	// LastPing is the most recent ping timestamp as reported by Fleet
	// Management. May lag relative to actual collector activity.
	// +optional
	LastPing *metav1.Time `json:"lastPing,omitempty"`

	// CollectorType is the type the collector reported on registration.
	// +optional
	CollectorType CollectorType `json:"collectorType,omitempty"`

	// LocalAttributes are the attributes the collector reports about itself
	// (e.g. collector.os=linux). Read-only — set by the collector, not the
	// operator.
	// +optional
	LocalAttributes map[string]string `json:"localAttributes,omitempty"`

	// EffectiveRemoteAttributes is the merged set of remote attributes last
	// successfully written to Fleet Management for this collector. In Phase
	// 1 this is exactly spec.remoteAttributes; later phases add policy and
	// external-sync layers.
	// +optional
	EffectiveRemoteAttributes map[string]string `json:"effectiveRemoteAttributes,omitempty"`

	// AttributeOwners records which CR owns each remote-attribute key. Used
	// by the controller to detect and remove keys when their owner stops
	// claiming them.
	// +listType=map
	// +listMapKey=key
	// +optional
	AttributeOwners []AttributeOwnership `json:"attributeOwners,omitempty"`

	// Conditions represent the current state of the Collector resource.
	//
	// Standard condition types:
	// - "Ready": Collector successfully reconciled (attributes synced, status mirrored).
	// - "Synced": Last reconciliation succeeded.
	//
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// AttributeOwnership records the owner and current value of one remote
// attribute key.
type AttributeOwnership struct {
	// Key is the remote-attribute key.
	Key string `json:"key"`

	// OwnerKind identifies which kind of CR owns this key.
	OwnerKind AttributeOwnerKind `json:"ownerKind"`

	// OwnerName is the namespaced name of the owning CR (in the form
	// "namespace/name").
	OwnerName string `json:"ownerName"`

	// Value is the value last written for this key.
	Value string `json:"value"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=fmcol
// +kubebuilder:printcolumn:name="ID",type="string",JSONPath=".spec.id"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".status.collectorType"
// +kubebuilder:printcolumn:name="Registered",type="boolean",JSONPath=".status.registered"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Collector is the Schema for the collectors API.
type Collector struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of the Collector.
	// +required
	Spec CollectorSpec `json:"spec"`

	// status defines the observed state of the Collector.
	// +optional
	Status CollectorStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// CollectorList contains a list of Collector.
type CollectorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Collector `json:"items"`
}

// CollectorTypeFromFleetAPI converts a Fleet Management API collector type
// string to the CRD enum value.
func CollectorTypeFromFleetAPI(apiType string) CollectorType {
	switch apiType {
	case "COLLECTOR_TYPE_ALLOY":
		return CollectorTypeAlloy
	case "COLLECTOR_TYPE_OTEL":
		return CollectorTypeOpenTelemetryCollector
	default:
		return CollectorTypeUnspecified
	}
}

func init() {
	SchemeBuilder.Register(&Collector{}, &CollectorList{})
}
