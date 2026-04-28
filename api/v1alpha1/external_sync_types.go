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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ExternalSourceKind enumerates the supported external attribute source
// kinds. Phase 3 ships HTTP; SQL arrives in Phase 4.
// +kubebuilder:validation:Enum=HTTP;SQL
type ExternalSourceKind string

const (
	ExternalSourceKindHTTP ExternalSourceKind = "HTTP"
	ExternalSourceKindSQL  ExternalSourceKind = "SQL"
)

// HTTPSourceSpec configures an HTTP/JSON external source.
type HTTPSourceSpec struct {
	// URL is the fully-qualified endpoint to fetch records from.
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url"`

	// Method is the HTTP verb to use. Defaults to GET.
	// +optional
	// +kubebuilder:default=GET
	// +kubebuilder:validation:Enum=GET;POST
	Method string `json:"method,omitempty"`

	// RecordsPath is a dotted path into the response JSON identifying the
	// array of records. Empty means the response root is the array itself.
	// Examples: "data", "result.items".
	// +optional
	RecordsPath string `json:"recordsPath,omitempty"`
}

// SQLSourceSpec configures a generic SQL external source. Reserved for
// Phase 4; the type is exposed now so existing CRDs remain forward-compatible.
type SQLSourceSpec struct {
	// Driver names the database/sql driver. Phase 4 will register
	// "postgres" and "mysql".
	// +optional
	Driver string `json:"driver,omitempty"`

	// Query is the SQL query to execute. Must SELECT at minimum the
	// CollectorIDField and every AttributeFields source column.
	// +optional
	Query string `json:"query,omitempty"`
}

// ExternalSource is the union-typed source configuration referenced by an
// ExternalAttributeSync. Exactly one of HTTP / SQL must be populated and
// must match Kind.
type ExternalSource struct {
	Kind      ExternalSourceKind      `json:"kind"`
	HTTP      *HTTPSourceSpec         `json:"http,omitempty"`
	SQL       *SQLSourceSpec          `json:"sql,omitempty"`
	SecretRef *corev1.SecretReference `json:"secretRef,omitempty"`
}

// AttributeMapping describes how to project a source record into a
// (collectorID, attributes) tuple.
type AttributeMapping struct {
	// CollectorIDField is the source field whose value identifies the
	// target collector.
	// +kubebuilder:validation:MinLength=1
	CollectorIDField string `json:"collectorIDField"`

	// AttributeFields maps an output attribute key to the source field
	// whose value becomes its value. Keys with the reserved "collector."
	// prefix are rejected.
	// +kubebuilder:validation:MaxProperties=100
	AttributeFields map[string]string `json:"attributeFields"`

	// RequiredKeys is the set of source fields that must be present for a
	// record to be applied. A record missing any required key is skipped
	// (counted in RecordsSeen but not RecordsApplied).
	// +optional
	RequiredKeys []string `json:"requiredKeys,omitempty"`
}

// ExternalAttributeSyncSpec defines a scheduled external-source pull whose
// output becomes remote attributes on selected collectors.
type ExternalAttributeSyncSpec struct {
	// Source identifies the kind and configuration of the external system.
	Source ExternalSource `json:"source"`

	// Schedule is either a Go duration ("5m", "30s") or a cron expression
	// ("*/15 * * * *"). Required.
	// +kubebuilder:validation:MinLength=1
	Schedule string `json:"schedule"`

	// Selector picks the collectors this sync targets. Reuses the
	// PolicySelector shape: matchers AND'd, OR'd with explicit collectorIDs.
	Selector PolicySelector `json:"selector"`

	// Mapping projects source records into collector attributes.
	Mapping AttributeMapping `json:"mapping"`

	// AllowEmptyResults gates the empty-result safety guard. When false
	// (default), a Fetch that returns zero records after a previous run
	// returned at least one is treated as a probable misconfiguration —
	// the previous owned-keys claim is preserved and a Stalled condition
	// is set.
	// +optional
	// +kubebuilder:default=false
	AllowEmptyResults bool `json:"allowEmptyResults,omitempty"`
}

// OwnedKeyEntry records the keys and values this ExternalAttributeSync
// claims for a specific collector. The Collector controller reads these
// directly when computing the merged desired state — values flow from this
// status field (set on each successful Fetch) into Fleet without re-running
// the source.
type OwnedKeyEntry struct {
	CollectorID string `json:"collectorID"`

	// Attributes maps the attribute key to the value this sync wants on
	// the named collector. Removing a key from this map drops that
	// claim — the Collector controller's diff produces a REMOVE op on
	// the next reconcile.
	// +optional
	Attributes map[string]string `json:"attributes,omitempty"`
}

// ExternalAttributeSyncStatus reflects the controller's view of the most
// recent fetch.
type ExternalAttributeSyncStatus struct {
	// ObservedGeneration reflects the most recently observed spec.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastSyncTime is the timestamp of the most recent Fetch attempt.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// LastSuccessTime is the timestamp of the most recent Fetch that
	// produced a status update. May trail LastSyncTime if the most recent
	// fetch was suppressed by the empty-result guard or failed.
	// +optional
	LastSuccessTime *metav1.Time `json:"lastSuccessTime,omitempty"`

	// RecordsSeen is the count of records returned by the last fetch.
	// +optional
	RecordsSeen int32 `json:"recordsSeen,omitempty"`

	// RecordsApplied is the count of records that produced an attribute
	// update (i.e., passed RequiredKeys and selector).
	// +optional
	RecordsApplied int32 `json:"recordsApplied,omitempty"`

	// OwnedKeys is the canonical claim list as of the last successful
	// fetch. Each entry is a collectorID and the set of keys this sync
	// owns on that collector.
	// +optional
	OwnedKeys []OwnedKeyEntry `json:"ownedKeys,omitempty"`

	// Conditions represent the current state of the ExternalAttributeSync.
	// Standard types: Ready, Synced, Stalled.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=fmeas
// +kubebuilder:printcolumn:name="Source",type="string",JSONPath=".spec.source.kind"
// +kubebuilder:printcolumn:name="Schedule",type="string",JSONPath=".spec.schedule"
// +kubebuilder:printcolumn:name="Last Sync",type="date",JSONPath=".status.lastSyncTime"
// +kubebuilder:printcolumn:name="Records",type="integer",JSONPath=".status.recordsApplied"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"

// ExternalAttributeSync pulls attributes from an external system on a
// schedule and reflects them onto matched collectors as remote attributes.
type ExternalAttributeSync struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state.
	// +required
	Spec ExternalAttributeSyncSpec `json:"spec"`

	// status defines the observed state.
	// +optional
	Status ExternalAttributeSyncStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ExternalAttributeSyncList contains a list of ExternalAttributeSync.
type ExternalAttributeSyncList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ExternalAttributeSync `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ExternalAttributeSync{}, &ExternalAttributeSyncList{})
}
