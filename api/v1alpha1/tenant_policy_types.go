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
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TenantPolicySpec binds K8s subjects to a set of required matchers. When
// tenant-policy enforcement is enabled on the manager, validating webhooks
// for Pipeline / RemoteAttributePolicy / ExternalAttributeSync resources
// require that the requesting user (after subject match) include at least
// one of the union of RequiredMatchers from every matching policy in their
// CR's matcher set.
type TenantPolicySpec struct {
	// Subjects this policy applies to. A subject matches the admission
	// request when its Kind+Name (and Namespace, for ServiceAccount) line up
	// with admission.Request.UserInfo. Reuses rbacv1.Subject so cluster
	// admins can copy bindings from existing RoleBindings.
	// +kubebuilder:validation:MinItems=1
	Subjects []rbacv1.Subject `json:"subjects"`

	// RequiredMatchers is the set of matchers (Prometheus Alertmanager
	// syntax: key=value, key!=value, key=~regex, key!~regex) that the CR's
	// matcher set must contain at least one of. Multiple matching policies
	// contribute to the union of allowed matchers — the CR satisfies the
	// check by including ANY one element of that union. A maximum of 100
	// required matchers may be set per policy; the cap exists to bound
	// admission cost across many concurrent CR writes. Each matcher is
	// independently capped at 200 characters by the API server (OpenAPI
	// maxLength) and double-checked by the validating webhook.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	// +kubebuilder:validation:items:MaxLength=200
	RequiredMatchers []string `json:"requiredMatchers"`

	// NamespaceSelector limits this policy to CRs in matching namespaces. If
	// nil, the policy applies in every namespace. Selectors are evaluated
	// against namespace labels via the standard metav1.LabelSelector
	// semantics.
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`
}

// TenantPolicyStatus reflects the controller's view of a TenantPolicy.
//
// Conditions written by the TenantPolicy reconciler:
//
//   - Valid:  True when every required matcher and selector parses; False
//     with reason ParseError when any required matcher or selector is
//     malformed.
//   - Ready:  True when Valid=True; otherwise False.
type TenantPolicyStatus struct {
	// ObservedGeneration tracks the last spec generation reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// BoundSubjectCount is the number of subjects (groups + users +
	// service accounts) currently declared by spec.subjects. Maintained
	// alongside the array so a typed printer column does not need to
	// coerce a slice into an integer.
	// +optional
	BoundSubjectCount int32 `json:"boundSubjectCount,omitempty"`

	// Conditions describe the policy's current state.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=fmtp
// +kubebuilder:printcolumn:name="Subjects",type="integer",JSONPath=".status.boundSubjectCount"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// TenantPolicy declares which K8s subjects are required to scope their
// Fleet Management CR matchers to a specific set of allowed matchers. It
// implements the missing per-tenant authorization layer that Fleet
// Management's API does not provide natively, by leveraging K8s RBAC group
// membership at admission time. Cluster-scoped because tenant boundaries
// are a platform-admin concern; standard K8s RBAC on this CRD itself
// controls who can create or modify policies.
type TenantPolicy struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired tenant policy.
	// +required
	Spec TenantPolicySpec `json:"spec"`

	// status defines the observed state of the policy.
	// +optional
	Status TenantPolicyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TenantPolicyList contains a list of TenantPolicy.
type TenantPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TenantPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TenantPolicy{}, &TenantPolicyList{})
}
