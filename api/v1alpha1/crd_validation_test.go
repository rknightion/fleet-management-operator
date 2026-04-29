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

const (
	selectorNonEmptyRule      = "(has(self.matchers) && self.matchers.size() > 0) || (has(self.collectorIDs) && self.collectorIDs.size() > 0)"
	mapKeyNonEmptyRule        = "self.all(k, k.size() > 0)"
	mapValueMaxLength1024Rule = "self.all(k, self[k].size() <= 1024)"
	mapValueNonEmptyRule      = "self.all(k, self[k].size() > 0)"
	externalSourceOneOfRule   = "(self.kind == 'HTTP' && has(self.http) && !has(self.sql)) || (self.kind == 'SQL' && has(self.sql) && !has(self.http))"
	externalSecretRefNameRule = "!has(self.secretRef) || (has(self.secretRef.name) && self.secretRef.name.size() > 0)"
)

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

func TestRemoteAttributeMaps_HaveKeyAndValueBounds(t *testing.T) {
	cases := []struct {
		name          string
		filename      string
		path          []string
		wantMinProps  bool
		wantNonEmpty  bool
		wantValueSize bool
	}{
		{
			name:          "Collector spec.remoteAttributes",
			filename:      "fleetmanagement.grafana.com_collectors.yaml",
			path:          []string{"spec", "remoteAttributes"},
			wantValueSize: true,
		},
		{
			name:          "RemoteAttributePolicy spec.attributes",
			filename:      "fleetmanagement.grafana.com_remoteattributepolicies.yaml",
			path:          []string{"spec", "attributes"},
			wantMinProps:  true,
			wantValueSize: true,
		},
		{
			name:          "ExternalAttributeSync spec.mapping.attributeFields",
			filename:      "fleetmanagement.grafana.com_externalattributesyncs.yaml",
			path:          []string{"spec", "mapping", "attributeFields"},
			wantMinProps:  true,
			wantNonEmpty:  true,
			wantValueSize: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			crd := loadCRD(t, tc.filename)
			schema := firstServedSchema(t, crd)
			prop := schemaProperty(schema, tc.path...)
			require.NotNil(t, prop, "property %v missing", tc.path)
			assert.True(t, hasCELRule(prop, mapKeyNonEmptyRule),
				"%s must reject empty map keys with CEL rule %q", tc.name, mapKeyNonEmptyRule)
			if tc.wantMinProps {
				require.NotNil(t, prop.MinProperties, "%s minProperties not set", tc.name)
				assert.EqualValues(t, 1, *prop.MinProperties, "%s minProperties must be 1", tc.name)
			}
			if tc.wantNonEmpty {
				assert.True(t, hasCELRule(prop, mapValueNonEmptyRule),
					"%s must reject empty map values with CEL rule %q", tc.name, mapValueNonEmptyRule)
			}
			if tc.wantValueSize {
				assert.True(t, hasCELRule(prop, mapValueMaxLength1024Rule),
					"%s must cap map values with CEL rule %q", tc.name, mapValueMaxLength1024Rule)
			}
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

func TestSelectorSlices_HaveItemMinLength(t *testing.T) {
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
			name:     "RemoteAttributePolicy spec.selector.collectorIDs",
			filename: "fleetmanagement.grafana.com_remoteattributepolicies.yaml",
			path:     []string{"spec", "selector", "collectorIDs"},
		},
		{
			name:     "ExternalAttributeSync spec.selector.matchers",
			filename: "fleetmanagement.grafana.com_externalattributesyncs.yaml",
			path:     []string{"spec", "selector", "matchers"},
		},
		{
			name:     "ExternalAttributeSync spec.selector.collectorIDs",
			filename: "fleetmanagement.grafana.com_externalattributesyncs.yaml",
			path:     []string{"spec", "selector", "collectorIDs"},
		},
		{
			name:     "ExternalAttributeSync spec.mapping.requiredKeys",
			filename: "fleetmanagement.grafana.com_externalattributesyncs.yaml",
			path:     []string{"spec", "mapping", "requiredKeys"},
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
			require.NotNil(t, items.MinLength, "%s items.minLength not set", tc.name)
			assert.EqualValues(t, 1, *items.MinLength,
				"%s items.minLength must be 1; check +kubebuilder:validation:items:MinLength=1 marker",
				tc.name)
		})
	}
}

func TestRemoteAttributePolicyCRD_SelectorRequiresATarget(t *testing.T) {
	crd := loadCRD(t, "fleetmanagement.grafana.com_remoteattributepolicies.yaml")
	schema := firstServedSchema(t, crd)
	selector := schemaProperty(schema, "spec", "selector")
	require.NotNil(t, selector, "spec.selector property missing")
	assert.True(t, hasCELRule(selector, selectorNonEmptyRule),
		"RemoteAttributePolicy spec.selector must declare CEL rule %q", selectorNonEmptyRule)
}

func TestExternalAttributeSyncCRD_SelectorSourceAndSecretRules(t *testing.T) {
	crd := loadCRD(t, "fleetmanagement.grafana.com_externalattributesyncs.yaml")
	schema := firstServedSchema(t, crd)

	selector := schemaProperty(schema, "spec", "selector")
	require.NotNil(t, selector, "spec.selector property missing")
	assert.True(t, hasCELRule(selector, selectorNonEmptyRule),
		"ExternalAttributeSync spec.selector must declare CEL rule %q", selectorNonEmptyRule)

	source := schemaProperty(schema, "spec", "source")
	require.NotNil(t, source, "spec.source property missing")
	assert.True(t, hasCELRule(source, externalSourceOneOfRule),
		"ExternalAttributeSync spec.source must declare CEL rule %q", externalSourceOneOfRule)
	assert.True(t, hasCELRule(source, externalSecretRefNameRule),
		"ExternalAttributeSync spec.source must declare CEL rule %q", externalSecretRefNameRule)

	query := schemaProperty(schema, "spec", "source", "sql", "query")
	require.NotNil(t, query, "spec.source.sql.query property missing")
	require.NotNil(t, query.MinLength, "spec.source.sql.query minLength not set")
	assert.EqualValues(t, 1, *query.MinLength)
}
