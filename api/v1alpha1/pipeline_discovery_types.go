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

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// Label and annotation keys for Pipeline CRs created by PipelineDiscovery.
const (
	// PipelineDiscoveryNameLabel marks a Pipeline CR as managed by a
	// PipelineDiscovery. Value is the PipelineDiscovery's metadata.name.
	PipelineDiscoveryNameLabel = "fleetmanagement.grafana.com/pipeline-discovery-name"

	// PipelineDiscoveryNamespaceLabel marks the namespace of the
	// PipelineDiscovery that owns a Pipeline CR. Together with
	// PipelineDiscoveryNameLabel, this makes ownership unambiguous when
	// same-named discoveries target one namespace.
	PipelineDiscoveryNamespaceLabel = "fleetmanagement.grafana.com/pipeline-discovery-namespace"

	// PipelineDiscoveredByAnnotation records "<namespace>/<name>" of the
	// owning PipelineDiscovery for human-readable provenance.
	PipelineDiscoveredByAnnotation = "fleetmanagement.grafana.com/pipeline-discovered-by"

	// FleetPipelineIDAnnotation records the Fleet Management pipeline ID
	// before any name sanitization. Authoritative reverse-lookup field.
	FleetPipelineIDAnnotation = "fleetmanagement.grafana.com/fleet-pipeline-id"

	// PipelineDiscoveryStaleAnnotation marks a Pipeline CR whose pipeline
	// no longer appears in ListPipelines when policy is Keep.
	PipelineDiscoveryStaleAnnotation = "fleetmanagement.grafana.com/pipeline-discovery-stale"

	// PipelineDiscoveryStaleAnnotationValue is the literal value the controller
	// writes when marking a Pipeline CR stale. Centralised so the reconciler,
	// webhook, and tests can compare against it.
	PipelineDiscoveryStaleAnnotationValue = "true"
)

// PipelineDiscoveryImportMode controls whether discovered Pipeline CRs are
// immediately reconciled to Fleet Management or held read-only.
// +kubebuilder:validation:Enum=Adopt;ReadOnly
type PipelineDiscoveryImportMode string

const (
	// PipelineDiscoveryImportModeAdopt creates Pipeline CRs with spec.paused=false.
	// The Pipeline controller will reconcile them to Fleet Management immediately.
	PipelineDiscoveryImportModeAdopt PipelineDiscoveryImportMode = "Adopt"

	// PipelineDiscoveryImportModeReadOnly creates Pipeline CRs with spec.paused=true.
	// The Pipeline controller skips reconciliation until the user opts in via
	// the fleetmanagement.grafana.com/import-mode=adopt annotation.
	PipelineDiscoveryImportModeReadOnly PipelineDiscoveryImportMode = "ReadOnly"
)

// PipelineDiscoveryOnRemovedAction controls the response when a discovered
// pipeline no longer appears in ListPipelines.
// +kubebuilder:validation:Enum=Keep;Delete
type PipelineDiscoveryOnRemovedAction string

const (
	// PipelineDiscoveryOnRemovedKeep leaves the Pipeline CR in place, marking
	// it with the stale annotation. Default.
	PipelineDiscoveryOnRemovedKeep PipelineDiscoveryOnRemovedAction = "Keep"

	// PipelineDiscoveryOnRemovedDelete removes the Pipeline CR. The Pipeline
	// finalizer issues a DeletePipeline call; 404 = success for vanished pipelines.
	PipelineDiscoveryOnRemovedDelete PipelineDiscoveryOnRemovedAction = "Delete"
)

// PipelineDiscoveryConflictReason classifies why a Pipeline CR could not be
// created or claimed.
// +kubebuilder:validation:Enum=NotOwnedByDiscovery;OwnedByOtherDiscovery;NameSanitizationFailed
type PipelineDiscoveryConflictReason string

const (
	// PipelineDiscoveryConflictNotOwned indicates a Pipeline CR with the desired
	// name exists but is not labeled as managed by any discovery — likely a
	// manually-created CR. Skipped.
	PipelineDiscoveryConflictNotOwned PipelineDiscoveryConflictReason = "NotOwnedByDiscovery"

	// PipelineDiscoveryConflictOwnedByOther indicates a Pipeline CR with the
	// desired name exists and is labeled as managed by a different
	// PipelineDiscovery. First-write wins; the second discovery skips.
	PipelineDiscoveryConflictOwnedByOther PipelineDiscoveryConflictReason = "OwnedByOtherDiscovery"

	// PipelineDiscoveryConflictSanitizeFailed indicates the pipeline ID could
	// not be sanitized to a valid DNS-1123 name even with the hash suffix
	// (e.g., empty ID after sanitization).
	PipelineDiscoveryConflictSanitizeFailed PipelineDiscoveryConflictReason = "NameSanitizationFailed"
)

// PipelineDiscoverySpec configures a periodic poll-and-import cycle against
// Fleet Management's ListPipelines. Each Fleet pipeline that matches the
// selector becomes a Pipeline CR in the target namespace.
type PipelineDiscoverySpec struct {
	// PollInterval is how often the controller calls ListPipelines.
	// Webhook-enforced minimum is 1 minute to protect the shared rate limiter.
	// +optional
	// +kubebuilder:default="5m"
	PollInterval string `json:"pollInterval,omitempty"`

	// Selector filters which Fleet pipelines are imported.
	// An empty selector means "import every pipeline" (server-wide
	// ListPipelines call) — accepted but expensive on large fleets.
	// +optional
	Selector PipelineDiscoverySelector `json:"selector,omitempty"`

	// TargetNamespace is the namespace where discovered Pipeline CRs are created.
	// Defaults to this PipelineDiscovery's own namespace.
	// +optional
	TargetNamespace string `json:"targetNamespace,omitempty"`

	// ImportMode controls whether discovered Pipeline CRs are immediately
	// managed (Adopt) or held read-only (ReadOnly). Individual Pipeline CRs
	// can override this via the fleetmanagement.grafana.com/import-mode=adopt
	// annotation.
	// +optional
	// +kubebuilder:default=Adopt
	ImportMode PipelineDiscoveryImportMode `json:"importMode,omitempty"`

	// Policy controls lifecycle decisions.
	// +optional
	Policy PipelineDiscoveryPolicy `json:"policy,omitempty"`
}

// PipelineDiscoverySelector filters which Fleet pipelines are imported.
type PipelineDiscoverySelector struct {
	// ConfigType limits discovery to pipelines of this type.
	// +optional
	ConfigType *ConfigType `json:"configType,omitempty"`

	// Enabled limits discovery to enabled or disabled pipelines.
	// Omit to discover both.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`
}

// PipelineDiscoveryPolicy bundles lifecycle decisions.
type PipelineDiscoveryPolicy struct {
	// OnPipelineRemoved chooses the response when a previously-discovered
	// pipeline no longer appears in ListPipelines.
	// +optional
	// +kubebuilder:default=Keep
	OnPipelineRemoved PipelineDiscoveryOnRemovedAction `json:"onPipelineRemoved,omitempty"`
}

// PipelineDiscoveryStatus reports the most recent poll outcome.
type PipelineDiscoveryStatus struct {
	// ObservedGeneration reflects the most recently observed spec generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastSyncTime is the timestamp of the most recent ListPipelines call.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// LastSuccessTime is the timestamp of the most recent successful poll.
	// +optional
	LastSuccessTime *metav1.Time `json:"lastSuccessTime,omitempty"`

	// PipelinesObserved is the count returned by the last ListPipelines call.
	// +optional
	PipelinesObserved int32 `json:"pipelinesObserved,omitempty"`

	// PipelinesManaged is the count of Pipeline CRs labeled as managed by
	// this discovery.
	// +optional
	PipelinesManaged int32 `json:"pipelinesManaged,omitempty"`

	// StalePipelines lists pipeline IDs whose CR still exists but no longer
	// appears in ListPipelines. Only populated when policy.onPipelineRemoved=Keep.
	// +optional
	StalePipelines []string `json:"stalePipelines,omitempty"`

	// Conflicts records cases (up to 100) where a CR could not be created
	// due to a name/ownership conflict. When the cap is hit, a TruncatedConflicts
	// condition is set; check events for the full list.
	// +listType=map
	// +listMapKey=pipelineID
	// +optional
	// +kubebuilder:validation:MaxItems=100
	Conflicts []PipelineDiscoveryConflict `json:"conflicts,omitempty"`

	// Conditions represent the current state of the PipelineDiscovery.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// PipelineDiscoveryConflict records a single conflict between the desired CR
// and an existing one with the same name.
type PipelineDiscoveryConflict struct {
	// PipelineID is the Fleet pipeline ID that could not be mirrored. List-map key.
	PipelineID string `json:"pipelineID"`

	// CRName is the metadata.name the controller computed.
	CRName string `json:"crName"`

	// Reason classifies the conflict.
	Reason PipelineDiscoveryConflictReason `json:"reason"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=fmpd
// +kubebuilder:printcolumn:name="Poll",type="string",JSONPath=".spec.pollInterval"
// +kubebuilder:printcolumn:name="Mode",type="string",JSONPath=".spec.importMode"
// +kubebuilder:printcolumn:name="Observed",type="integer",JSONPath=".status.pipelinesObserved"
// +kubebuilder:printcolumn:name="Managed",type="integer",JSONPath=".status.pipelinesManaged"
// +kubebuilder:printcolumn:name="Last Sync",type="date",JSONPath=".status.lastSyncTime"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// PipelineDiscovery configures a periodic import of Fleet Management pipelines
// into the cluster as Pipeline CRs.
type PipelineDiscovery struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state.
	// +required
	Spec PipelineDiscoverySpec `json:"spec"`

	// status defines the observed state.
	// +optional
	Status PipelineDiscoveryStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PipelineDiscoveryList contains a list of PipelineDiscovery.
type PipelineDiscoveryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PipelineDiscovery `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PipelineDiscovery{}, &PipelineDiscoveryList{})
}
