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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ConfigType represents the type of collector configuration
// +kubebuilder:validation:Enum=Alloy;OpenTelemetryCollector
type ConfigType string

const (
	// ConfigTypeAlloy represents Grafana Alloy configuration syntax
	ConfigTypeAlloy ConfigType = "Alloy"

	// ConfigTypeOpenTelemetryCollector represents OpenTelemetry Collector configuration syntax
	ConfigTypeOpenTelemetryCollector ConfigType = "OpenTelemetryCollector"
)

// Fleet Management API constants for ConfigType
const (
	fleetAPIConfigTypeAlloy = "CONFIG_TYPE_ALLOY"
	fleetAPIConfigTypeOTEL  = "CONFIG_TYPE_OTEL"
)

// SourceType represents the origin source of the pipeline
// +kubebuilder:validation:Enum=Git;Terraform;Kubernetes;Unspecified
type SourceType string

const (
	// SourceTypeGit indicates pipeline originated from Git repository
	SourceTypeGit SourceType = "Git"

	// SourceTypeTerraform indicates pipeline originated from Terraform
	SourceTypeTerraform SourceType = "Terraform"

	// SourceTypeKubernetes indicates pipeline originated from this Kubernetes operator
	SourceTypeKubernetes SourceType = "Kubernetes"

	// SourceTypeUnspecified indicates pipeline source is not specified
	SourceTypeUnspecified SourceType = "Unspecified"
)

// Fleet Management API constants for SourceType
const (
	fleetAPISourceTypeGit         = "SOURCE_TYPE_GIT"
	fleetAPISourceTypeTerraform   = "SOURCE_TYPE_TERRAFORM"
	fleetAPISourceTypeKubernetes  = "SOURCE_TYPE_KUBERNETES"
	fleetAPISourceTypeUnspecified = "SOURCE_TYPE_UNSPECIFIED"
)

// PipelineSource defines the origin source of the pipeline
type PipelineSource struct {
	// Type specifies the source type (Git, Terraform, Kubernetes, Unspecified)
	// +optional
	// +kubebuilder:default=Kubernetes
	Type SourceType `json:"type,omitempty"`

	// Namespace provides additional context about the source
	// For Git: repository name or URL
	// For Terraform: workspace or module name
	// For Kubernetes: cluster name or context
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// ToFleetAPI converts CRD ConfigType to Fleet Management API format
func (c ConfigType) ToFleetAPI() string {
	switch c {
	case ConfigTypeAlloy:
		return fleetAPIConfigTypeAlloy
	case ConfigTypeOpenTelemetryCollector:
		return fleetAPIConfigTypeOTEL
	default:
		return fleetAPIConfigTypeAlloy
	}
}

// ConfigTypeFromFleetAPI converts Fleet Management API format to CRD ConfigType
func ConfigTypeFromFleetAPI(apiType string) ConfigType {
	switch apiType {
	case fleetAPIConfigTypeOTEL:
		return ConfigTypeOpenTelemetryCollector
	case fleetAPIConfigTypeAlloy:
		return ConfigTypeAlloy
	default:
		return ConfigTypeAlloy
	}
}

// ToFleetAPI converts CRD SourceType to Fleet Management API format
func (s SourceType) ToFleetAPI() string {
	switch s {
	case SourceTypeGit:
		return fleetAPISourceTypeGit
	case SourceTypeTerraform:
		return fleetAPISourceTypeTerraform
	case SourceTypeKubernetes:
		return fleetAPISourceTypeKubernetes
	case SourceTypeUnspecified:
		return fleetAPISourceTypeUnspecified
	default:
		return fleetAPISourceTypeKubernetes
	}
}

// SourceTypeFromFleetAPI converts Fleet Management API format to CRD SourceType
func SourceTypeFromFleetAPI(apiType string) SourceType {
	switch apiType {
	case "SOURCE_TYPE_GIT":
		return SourceTypeGit
	case "SOURCE_TYPE_TERRAFORM":
		return SourceTypeTerraform
	case "SOURCE_TYPE_KUBERNETES":
		return SourceTypeKubernetes
	case "SOURCE_TYPE_UNSPECIFIED":
		return SourceTypeUnspecified
	default:
		return SourceTypeUnspecified
	}
}

// PipelineSpec defines the desired state of Pipeline
type PipelineSpec struct {
	// Name of the pipeline (unique identifier in Fleet Management)
	// If not specified, uses metadata.name
	// +optional
	Name string `json:"name,omitempty"`

	// Contents of the pipeline configuration (Alloy or OpenTelemetry Collector config)
	// +required
	// +kubebuilder:validation:MinLength=1
	Contents string `json:"contents"`

	// Matchers to assign pipeline to collectors. Uses Prometheus Alertmanager
	// syntax: key=value, key!=value, key=~regex, key!~regex. A maximum of
	// 100 matchers may be set per pipeline; the cap exists to bound
	// validation and matching cost across the fleet (Fleet Management
	// evaluates matchers on every collector poll). Each matcher is
	// independently capped at 200 characters by the API server (OpenAPI
	// maxLength) and double-checked by the validating webhook.
	// +optional
	// +kubebuilder:validation:MaxItems=100
	// +kubebuilder:validation:items:MaxLength=200
	Matchers []string `json:"matchers,omitempty"`

	// Enabled indicates whether the pipeline is enabled for collectors
	// +optional
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// ConfigType specifies the type of configuration (Alloy or OpenTelemetryCollector)
	// +optional
	// +kubebuilder:default=Alloy
	ConfigType ConfigType `json:"configType,omitempty"`

	// Source specifies the origin of the pipeline (Git, Terraform, Kubernetes, etc.)
	// Used for tracking and grouping pipelines by their source
	// +optional
	Source *PipelineSource `json:"source,omitempty"`
}

// PipelineStatus defines the observed state of Pipeline.
type PipelineStatus struct {
	// ID is the server-assigned pipeline ID from Fleet Management
	// +optional
	ID string `json:"id,omitempty"`

	// ObservedGeneration reflects the generation of the most recently observed Pipeline spec
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// CreatedAt is the timestamp when the pipeline was created in Fleet Management
	// +optional
	CreatedAt *metav1.Time `json:"createdAt,omitempty"`

	// UpdatedAt is the timestamp when the pipeline was last updated in Fleet Management
	// +optional
	UpdatedAt *metav1.Time `json:"updatedAt,omitempty"`

	// RevisionID is the current revision ID from Fleet Management
	// +optional
	RevisionID string `json:"revisionId,omitempty"`

	// Conditions represent the current state of the Pipeline resource.
	//
	// Standard condition types:
	// - "Ready": Pipeline is successfully synced to Fleet Management
	// - "Synced": Last reconciliation succeeded
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=fmp
// +kubebuilder:printcolumn:name="Enabled",type="boolean",JSONPath=".spec.enabled"
// +kubebuilder:printcolumn:name="Config Type",type="string",JSONPath=".spec.configType"
// +kubebuilder:printcolumn:name="Fleet ID",type="string",JSONPath=".status.id"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Pipeline is the Schema for the pipelines API
type Pipeline struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Pipeline
	// +required
	Spec PipelineSpec `json:"spec"`

	// status defines the observed state of Pipeline
	// +optional
	Status PipelineStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PipelineList contains a list of Pipeline
type PipelineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Pipeline `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Pipeline{}, &PipelineList{})
}
