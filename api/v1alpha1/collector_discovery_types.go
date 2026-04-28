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

// DiscoveryOnRemovedAction selects what the CollectorDiscovery controller
// does when a previously-discovered collector no longer appears in
// ListCollectors. Keep (default) leaves the CR in place with a stale
// annotation; Delete removes it (the existing Collector finalizer then
// issues REMOVE ops to Fleet, which 404s for a vanished collector — net
// no-op).
// +kubebuilder:validation:Enum=Keep;Delete
type DiscoveryOnRemovedAction string

const (
	DiscoveryOnRemovedKeep   DiscoveryOnRemovedAction = "Keep"
	DiscoveryOnRemovedDelete DiscoveryOnRemovedAction = "Delete"
)

// DiscoveryOnConflictAction selects what the controller does when a
// Collector CR with the desired name already exists and is not labeled
// as managed by this discovery. v1 only ships Skip; TakeOwnership is
// reserved for v2 once a clear opt-in path is designed.
// +kubebuilder:validation:Enum=Skip
type DiscoveryOnConflictAction string

const (
	DiscoveryOnConflictSkip DiscoveryOnConflictAction = "Skip"
)

// Discovery-related label and annotation keys used by the
// CollectorDiscovery controller to track its mirrored Collector CRs.
// Public so the controller, webhook, and tests can refer to them.
const (
	// DiscoveryNameLabel marks a Collector CR as managed by a
	// CollectorDiscovery and identifies which one owns it. The value is
	// the CollectorDiscovery's metadata.name (already DNS-1123 by k8s
	// rules, so usable verbatim as a label value).
	DiscoveryNameLabel = "fleetmanagement.grafana.com/discovery-name"

	// DiscoveredByAnnotation records the namespaced name
	// ("<namespace>/<name>") of the CollectorDiscovery that created
	// this Collector CR. Provenance for humans and tooling.
	DiscoveredByAnnotation = "fleetmanagement.grafana.com/discovered-by"

	// FleetCollectorIDAnnotation records the original Fleet collector
	// ID (before sanitization). The CR's metadata.name may have been
	// sanitized or hash-suffixed to satisfy DNS-1123, so this
	// annotation is the authoritative reverse-lookup field.
	FleetCollectorIDAnnotation = "fleetmanagement.grafana.com/fleet-collector-id"

	// DiscoveryStaleAnnotation marks a Collector CR whose collector no
	// longer appears in ListCollectors when the owning
	// CollectorDiscovery's policy is Keep. The controller removes the
	// annotation when the collector reappears. An annotation (rather
	// than a condition) is used so the existing Collector reconciler
	// — which manages the CR's conditions — does not fight us.
	DiscoveryStaleAnnotation = "fleetmanagement.grafana.com/discovery-stale"
)

// DiscoveryConflictReason enumerates the reasons a discovered CR could
// not be created or claimed.
// +kubebuilder:validation:Enum=NotOwnedByDiscovery;OwnedByOtherDiscovery;NameSanitizationFailed
type DiscoveryConflictReason string

const (
	// DiscoveryConflictNotOwned indicates a Collector CR with the
	// desired name exists but is not labeled as managed by any
	// discovery — likely a manually-created CR. Skipped.
	DiscoveryConflictNotOwned DiscoveryConflictReason = "NotOwnedByDiscovery"

	// DiscoveryConflictOwnedByOther indicates a Collector CR with the
	// desired name exists and is labeled as managed by a different
	// CollectorDiscovery. First-write wins; the second discovery skips.
	DiscoveryConflictOwnedByOther DiscoveryConflictReason = "OwnedByOtherDiscovery"

	// DiscoveryConflictSanitizeFailed indicates the collector ID could
	// not be sanitized to a valid DNS-1123 name even with the hash
	// suffix (e.g., empty ID after sanitization).
	DiscoveryConflictSanitizeFailed DiscoveryConflictReason = "NameSanitizationFailed"
)

// CollectorDiscoverySpec configures a periodic poll-and-mirror cycle
// against Fleet Management's ListCollectors. Each Fleet collector that
// matches the selector becomes a Collector CR in the target namespace.
type CollectorDiscoverySpec struct {
	// PollInterval is how often the controller calls Fleet's
	// ListCollectors. Webhook-enforced minimum is 1 minute to protect
	// the shared 3 req/s rate limiter.
	// +optional
	// +kubebuilder:default="5m"
	PollInterval string `json:"pollInterval,omitempty"`

	// Selector is the server-side filter passed to ListCollectors.
	// Reuses the PolicySelector shape: matchers AND'd, OR'd with
	// explicit collectorIDs. An empty selector means "match every
	// collector" (server-wide ListCollectors call) — accepted but
	// expensive on large fleets.
	// +optional
	Selector PolicySelector `json:"selector,omitempty"`

	// TargetNamespace is the namespace where mirrored Collector CRs are
	// created. Defaults to this CollectorDiscovery's own namespace.
	// +optional
	TargetNamespace string `json:"targetNamespace,omitempty"`

	// IncludeInactive mirrors Fleet records with markedInactiveAt set.
	// Default false skips them — the typical case is "show me only
	// collectors that are currently expected to ping in".
	// +optional
	// +kubebuilder:default=false
	IncludeInactive bool `json:"includeInactive,omitempty"`

	// Policy controls how the controller reacts to Fleet-side changes.
	// +optional
	Policy DiscoveryPolicy `json:"policy,omitempty"`
}

// DiscoveryPolicy bundles lifecycle decisions the controller respects.
type DiscoveryPolicy struct {
	// OnCollectorRemoved chooses the controller's response when a
	// previously-discovered collector no longer appears in
	// ListCollectors.
	// +optional
	// +kubebuilder:default=Keep
	OnCollectorRemoved DiscoveryOnRemovedAction `json:"onCollectorRemoved,omitempty"`

	// OnConflict chooses the controller's response when a Collector CR
	// with the desired name already exists and is not labeled as
	// managed by this discovery. v1 only ships Skip.
	// +optional
	// +kubebuilder:default=Skip
	OnConflict DiscoveryOnConflictAction `json:"onConflict,omitempty"`
}

// CollectorDiscoveryStatus reports the most recent poll outcome.
type CollectorDiscoveryStatus struct {
	// ObservedGeneration reflects the most recently observed spec.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastSyncTime is the timestamp of the most recent ListCollectors
	// call (success or failure).
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// LastSuccessTime is the timestamp of the most recent
	// ListCollectors call that produced a status update without error.
	// +optional
	LastSuccessTime *metav1.Time `json:"lastSuccessTime,omitempty"`

	// CollectorsObserved is the count of collectors returned by the
	// last ListCollectors call (after include-inactive filtering).
	// +optional
	CollectorsObserved int32 `json:"collectorsObserved,omitempty"`

	// CollectorsManaged is the count of Collector CRs in the target
	// namespace currently labeled as managed by this discovery.
	// +optional
	CollectorsManaged int32 `json:"collectorsManaged,omitempty"`

	// StaleCollectors lists collector IDs whose CR still exists but no
	// longer appears in ListCollectors. Only populated when
	// policy.onCollectorRemoved=Keep.
	// +optional
	StaleCollectors []string `json:"staleCollectors,omitempty"`

	// Conflicts records cases where the controller could not create or
	// claim a CR because of a name / ownership conflict.
	// +listType=map
	// +listMapKey=collectorID
	// +optional
	Conflicts []DiscoveryConflict `json:"conflicts,omitempty"`

	// Conditions represent the current state of the CollectorDiscovery.
	// Known types: Ready, Synced. Reasons: Synced, ListCollectorsFailed,
	// UpsertFailed, InvalidConfig. See docs/conditions.md for the
	// cross-CRD registry.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// DiscoveryConflict records a single conflict between the desired CR
// and an existing one with the same name.
type DiscoveryConflict struct {
	// CollectorID is the Fleet collector ID whose mirror CR could not
	// be created. Used as the list-map key.
	CollectorID string `json:"collectorID"`

	// CRName is the metadata.name the controller computed for the CR.
	CRName string `json:"crName"`

	// Reason classifies the conflict.
	Reason DiscoveryConflictReason `json:"reason"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=fmcd
// +kubebuilder:printcolumn:name="Poll",type="string",JSONPath=".spec.pollInterval"
// +kubebuilder:printcolumn:name="Observed",type="integer",JSONPath=".status.collectorsObserved"
// +kubebuilder:printcolumn:name="Managed",type="integer",JSONPath=".status.collectorsManaged"
// +kubebuilder:printcolumn:name="Last Sync",type="date",JSONPath=".status.lastSyncTime"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// CollectorDiscovery configures a periodic mirror of Fleet Management
// collectors into the cluster as Collector CRs. The Collector reconciler
// then manages remote attributes on each mirrored CR; this resource only
// owns the CR's existence (creation when a collector appears in Fleet,
// deletion or stale-marking when it disappears).
type CollectorDiscovery struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state.
	// +required
	Spec CollectorDiscoverySpec `json:"spec"`

	// status defines the observed state.
	// +optional
	Status CollectorDiscoveryStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// CollectorDiscoveryList contains a list of CollectorDiscovery.
type CollectorDiscoveryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []CollectorDiscovery `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CollectorDiscovery{}, &CollectorDiscoveryList{})
}
