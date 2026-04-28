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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// loadCRD reads the generated CRD manifest for the given filename under
// config/crd/bases/. Tests in this file pin the validation rules (CEL,
// immutability, structural caps) that travel with the schema so a future
// edit that drops a marker cannot ship silently.
func loadCRD(t *testing.T, filename string) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	path := filepath.Join("..", "..", "config", "crd", "bases", filename)
	data, err := os.ReadFile(path)
	require.NoError(t, err, "read CRD manifest %s", path)
	crd := &apiextensionsv1.CustomResourceDefinition{}
	require.NoError(t, yaml.Unmarshal(data, crd), "unmarshal CRD manifest %s", path)
	return crd
}

// schemaProperty walks a JSONSchemaProps property tree by dotted path
// (e.g. "spec.id"). Returns nil if any segment is missing.
func schemaProperty(schema *apiextensionsv1.JSONSchemaProps, path ...string) *apiextensionsv1.JSONSchemaProps {
	cur := schema
	for _, seg := range path {
		if cur == nil || cur.Properties == nil {
			return nil
		}
		next, ok := cur.Properties[seg]
		if !ok {
			return nil
		}
		cur = &next
	}
	return cur
}

// firstServedSchema returns the schema of the served version on the CRD,
// asserting there is exactly one (these CRDs are all v1alpha1).
func firstServedSchema(t *testing.T, crd *apiextensionsv1.CustomResourceDefinition) *apiextensionsv1.JSONSchemaProps {
	t.Helper()
	require.Len(t, crd.Spec.Versions, 1, "expected exactly one CRD version")
	v := crd.Spec.Versions[0]
	require.NotNil(t, v.Schema, "CRD version has no schema")
	require.NotNil(t, v.Schema.OpenAPIV3Schema, "CRD version has no OpenAPI schema")
	return v.Schema.OpenAPIV3Schema
}

// hasCELRule returns true if any x-kubernetes-validations entry on the
// node matches the supplied rule string.
func hasCELRule(props *apiextensionsv1.JSONSchemaProps, rule string) bool {
	if props == nil {
		return false
	}
	for _, v := range props.XValidations {
		if v.Rule == rule {
			return true
		}
	}
	return false
}

func TestCollectorCRD_SpecIDIsImmutable(t *testing.T) {
	crd := loadCRD(t, "fleetmanagement.grafana.com_collectors.yaml")
	schema := firstServedSchema(t, crd)
	idProp := schemaProperty(schema, "spec", "id")
	require.NotNil(t, idProp, "spec.id property missing from Collector CRD")
	assert.True(t, hasCELRule(idProp, "self == oldSelf"),
		"Collector spec.id must declare CEL immutability rule (self == oldSelf); "+
			"check the +kubebuilder:validation:XValidation marker on CollectorSpec.ID")
}

// reservedKeyPrefixRule is the CEL rule string used across CRDs to reject
// remote-attribute keys with the reserved "collector." prefix.
const reservedKeyPrefixRule = "self.all(k, !k.startsWith('collector.'))"

func TestRemoteAttributeMaps_RejectReservedKeyPrefix(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		path     []string
	}{
		{
			name:     "Collector spec.remoteAttributes",
			filename: "fleetmanagement.grafana.com_collectors.yaml",
			path:     []string{"spec", "remoteAttributes"},
		},
		{
			name:     "RemoteAttributePolicy spec.attributes",
			filename: "fleetmanagement.grafana.com_remoteattributepolicies.yaml",
			path:     []string{"spec", "attributes"},
		},
		{
			name:     "ExternalAttributeSync spec.mapping.attributeFields",
			filename: "fleetmanagement.grafana.com_externalattributesyncs.yaml",
			path:     []string{"spec", "mapping", "attributeFields"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			crd := loadCRD(t, tc.filename)
			schema := firstServedSchema(t, crd)
			prop := schemaProperty(schema, tc.path...)
			require.NotNil(t, prop, "property %v missing", tc.path)
			assert.True(t, hasCELRule(prop, reservedKeyPrefixRule),
				"%s must declare CEL rule %q to reject reserved 'collector.' key prefix",
				tc.name, reservedKeyPrefixRule)
		})
	}
}

// schemaItems returns the items schema for an array property.
func schemaItems(props *apiextensionsv1.JSONSchemaProps) *apiextensionsv1.JSONSchemaProps {
	if props == nil || props.Items == nil {
		return nil
	}
	return props.Items.Schema
}

func TestMatcherSlices_HaveItemMaxLength200(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		path     []string
	}{
		{
			name:     "Pipeline spec.matchers",
			filename: "fleetmanagement.grafana.com_pipelines.yaml",
			path:     []string{"spec", "matchers"},
		},
		{
			name:     "RemoteAttributePolicy spec.selector.matchers",
			filename: "fleetmanagement.grafana.com_remoteattributepolicies.yaml",
			path:     []string{"spec", "selector", "matchers"},
		},
		{
			name:     "ExternalAttributeSync spec.selector.matchers",
			filename: "fleetmanagement.grafana.com_externalattributesyncs.yaml",
			path:     []string{"spec", "selector", "matchers"},
		},
		{
			name:     "CollectorDiscovery spec.selector.matchers",
			filename: "fleetmanagement.grafana.com_collectordiscoveries.yaml",
			path:     []string{"spec", "selector", "matchers"},
		},
		{
			name:     "TenantPolicy spec.requiredMatchers",
			filename: "fleetmanagement.grafana.com_tenantpolicies.yaml",
			path:     []string{"spec", "requiredMatchers"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			crd := loadCRD(t, tc.filename)
			schema := firstServedSchema(t, crd)
			prop := schemaProperty(schema, tc.path...)
			require.NotNil(t, prop, "property %v missing", tc.path)
			items := schemaItems(prop)
			require.NotNil(t, items, "%s items schema missing", tc.name)
			require.NotNil(t, items.MaxLength, "%s items.maxLength not set", tc.name)
			assert.EqualValues(t, 200, *items.MaxLength,
				"%s items.maxLength must be 200; check +kubebuilder:validation:items:MaxLength=200 marker",
				tc.name)
		})
	}
}
