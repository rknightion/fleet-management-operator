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

// Hub marks all v1alpha1 types as the conversion hub for the
// fleetmanagement.grafana.com API group.
//
// When a future v1beta1 is introduced, the conversion webhook translates
// through these types. To generate conversion stubs once v1beta1 exists:
//
//	kubebuilder create api --group fleetmanagement --version v1beta1 --kind <Kind>
//	make generate
//
// Until v1beta1 lands, these are no-ops satisfying the
// sigs.k8s.io/controller-runtime/pkg/conversion.Hub interface prerequisite.

func (*Pipeline) Hub()                  {}
func (*PipelineList) Hub()              {}
func (*Collector) Hub()                 {}
func (*CollectorList) Hub()             {}
func (*RemoteAttributePolicy) Hub()     {}
func (*RemoteAttributePolicyList) Hub() {}
func (*ExternalAttributeSync) Hub()     {}
func (*ExternalAttributeSyncList) Hub() {}
func (*CollectorDiscovery) Hub()        {}
func (*CollectorDiscoveryList) Hub()    {}
func (*TenantPolicy) Hub()              {}
func (*TenantPolicyList) Hub()          {}
