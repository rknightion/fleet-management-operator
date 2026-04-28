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
	"context"
	"fmt"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var collectorlog = logf.Log.WithName("collector-resource")

// collectorReservedAttributePrefix is the attribute-key prefix reserved by
// Fleet Management for collector-reported (local) attributes. Remote
// attributes managed via the Collector CR must not use this prefix.
const collectorReservedAttributePrefix = "collector."

// collectorMaxAttributeValueLength is the upper bound on remote-attribute
// value length. Mirrors the Fleet Management API limit and protects against
// pathologically large values.
const collectorMaxAttributeValueLength = 1024

// collectorMaxRemoteAttributes is the schema-enforced maximum number of
// remote-attribute keys per collector. Duplicated here as a defensive bound
// in case the kubebuilder MaxProperties marker is bypassed.
const collectorMaxRemoteAttributes = 100

// SetupCollectorWebhookWithManager registers the Collector webhook with the manager.
func SetupCollectorWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &Collector{}).
		WithValidator(&Collector{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-fleetmanagement-grafana-com-v1alpha1-collector,mutating=false,failurePolicy=fail,sideEffects=None,groups=fleetmanagement.grafana.com,resources=collectors,verbs=create;update,versions=v1alpha1,name=vcollector.kb.io,admissionReviewVersions=v1

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type
func (r *Collector) ValidateCreate(ctx context.Context, obj *Collector) (admission.Warnings, error) {
	collectorlog.Info("validate create", "name", r.Name)

	return r.validateCollector()
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type
func (r *Collector) ValidateUpdate(ctx context.Context, oldObj, newObj *Collector) (admission.Warnings, error) {
	collectorlog.Info("validate update", "name", r.Name)

	// spec.id is immutable. Refuse updates that change it.
	if oldObj != nil && newObj != nil && oldObj.Spec.ID != newObj.Spec.ID {
		return nil, fmt.Errorf("spec.id is immutable; create a new Collector resource instead")
	}

	return r.validateCollector()
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type
func (r *Collector) ValidateDelete(ctx context.Context, obj *Collector) (admission.Warnings, error) {
	collectorlog.Info("validate delete", "name", r.Name)

	// No validation needed for delete
	return nil, nil
}

// validateCollector performs comprehensive validation of the Collector resource.
func (r *Collector) validateCollector() (admission.Warnings, error) {
	// 1. spec.id required and non-blank after trimming. MinLength=1 in the
	//    schema catches empty strings, but not all-whitespace values.
	if err := r.validateID(); err != nil {
		return nil, err
	}

	// 2. Validate remote attributes (keys, values, count).
	if err := r.validateRemoteAttributes(); err != nil {
		return nil, err
	}

	return nil, nil
}

// validateID rejects blank or whitespace-only IDs.
func (r *Collector) validateID() error {
	if strings.TrimSpace(r.Spec.ID) == "" {
		return fmt.Errorf("spec.id cannot be empty or whitespace")
	}

	return nil
}

// validateRemoteAttributes enforces:
//   - reserved "collector." prefix is rejected
//   - empty-string keys are rejected
//   - values are at most collectorMaxAttributeValueLength characters
//   - defense-in-depth count check against collectorMaxRemoteAttributes
func (r *Collector) validateRemoteAttributes() error {
	attrs := r.Spec.RemoteAttributes
	if len(attrs) == 0 {
		return nil
	}

	// Defense-in-depth: schema MaxProperties=100 should already enforce this.
	if len(attrs) > collectorMaxRemoteAttributes {
		return fmt.Errorf(
			"spec.remoteAttributes has %d entries which exceeds the maximum of %d",
			len(attrs), collectorMaxRemoteAttributes)
	}

	for key, value := range attrs {
		if key == "" {
			return fmt.Errorf("spec.remoteAttributes contains an empty key")
		}

		if strings.HasPrefix(key, collectorReservedAttributePrefix) {
			return fmt.Errorf(
				"spec.remoteAttributes key %q uses reserved prefix %q which is reserved by Fleet Management for collector-reported attributes",
				key, collectorReservedAttributePrefix)
		}

		if len(value) > collectorMaxAttributeValueLength {
			return fmt.Errorf(
				"spec.remoteAttributes[%q] value length %d exceeds the maximum of %d characters",
				key, len(value), collectorMaxAttributeValueLength)
		}
	}

	return nil
}
