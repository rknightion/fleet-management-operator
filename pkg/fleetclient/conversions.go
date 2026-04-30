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

package fleetclient

import (
	pipelinev1 "github.com/grafana/fleet-management-api/api/gen/proto/go/pipeline/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// String constants for the wire-public ConfigType field, matching the proto
// enum names. Kept as strings (rather than introducing a new typed alias) so
// the public Pipeline struct in types.go continues to satisfy existing callers.
const (
	configTypeAlloy = "CONFIG_TYPE_ALLOY"
	configTypeOTEL  = "CONFIG_TYPE_OTEL"
)

const (
	sourceTypeGit         = "SOURCE_TYPE_GIT"
	sourceTypeTerraform   = "SOURCE_TYPE_TERRAFORM"
	sourceTypeGrafana     = "SOURCE_TYPE_GRAFANA"
	sourceTypeUnspecified = "SOURCE_TYPE_UNSPECIFIED"
)

// The public Grafana HTTP API documents SOURCE_TYPE_GRAFANA for automatic
// Grafana/Instrumentation Hub pipelines before fleet-management-api v1.2.0
// exposes a named Go enum constant. Proto enum fields preserve unknown numeric
// values, so value 3 is intentionally used here until the SDK names it.
const sourceTypeGrafanaProto = pipelinev1.PipelineSource_SourceType(3)

func pipelineToProto(p *Pipeline) *pipelinev1.Pipeline {
	if p == nil {
		return nil
	}
	enabled := p.Enabled
	out := &pipelinev1.Pipeline{
		Name:       p.Name,
		Contents:   p.Contents,
		Matchers:   p.Matchers,
		Enabled:    &enabled,
		ConfigType: configTypeStringToProto(p.ConfigType),
	}
	if p.ID != "" {
		id := p.ID
		out.Id = &id
	}
	if p.Source != nil {
		out.Source = &pipelinev1.PipelineSource{
			Type:      sourceTypeStringToProto(p.Source.Type),
			Namespace: p.Source.Namespace,
		}
	}
	if p.CreatedAt != nil {
		out.CreatedAt = timestamppb.New(*p.CreatedAt)
	}
	if p.UpdatedAt != nil {
		out.UpdatedAt = timestamppb.New(*p.UpdatedAt)
	}
	return out
}

func pipelineFromProto(p *pipelinev1.Pipeline) *Pipeline {
	if p == nil {
		return nil
	}
	out := &Pipeline{
		Name:       p.GetName(),
		Contents:   p.GetContents(),
		Matchers:   p.GetMatchers(),
		Enabled:    p.GetEnabled(),
		ID:         p.GetId(),
		ConfigType: configTypeProtoToString(p.GetConfigType()),
	}
	if src := p.GetSource(); src != nil {
		out.Source = &Source{
			Type:      sourceTypeProtoToString(src.GetType()),
			Namespace: src.GetNamespace(),
		}
	}
	if p.GetCreatedAt() != nil {
		t := p.GetCreatedAt().AsTime()
		out.CreatedAt = &t
	}
	if p.GetUpdatedAt() != nil {
		t := p.GetUpdatedAt().AsTime()
		out.UpdatedAt = &t
	}
	return out
}

func configTypeStringToProto(s string) pipelinev1.ConfigType {
	switch s {
	case configTypeAlloy:
		return pipelinev1.ConfigType_CONFIG_TYPE_ALLOY
	case configTypeOTEL:
		return pipelinev1.ConfigType_CONFIG_TYPE_OTEL
	default:
		return pipelinev1.ConfigType_CONFIG_TYPE_UNSPECIFIED
	}
}

func configTypeProtoToString(c pipelinev1.ConfigType) string {
	switch c {
	case pipelinev1.ConfigType_CONFIG_TYPE_ALLOY:
		return configTypeAlloy
	case pipelinev1.ConfigType_CONFIG_TYPE_OTEL:
		return configTypeOTEL
	default:
		return ""
	}
}

// sourceTypeStringToProto maps the wire-public SourceType strings to the proto
// enum. Unsupported legacy strings, such as the operator's former
// "SOURCE_TYPE_KUBERNETES" compatibility value, fall through to UNSPECIFIED
// rather than inventing a Fleet source the SDK does not expose.
func sourceTypeStringToProto(s string) pipelinev1.PipelineSource_SourceType {
	switch s {
	case sourceTypeGit:
		return pipelinev1.PipelineSource_SOURCE_TYPE_GIT
	case sourceTypeTerraform:
		return pipelinev1.PipelineSource_SOURCE_TYPE_TERRAFORM
	case sourceTypeGrafana:
		return sourceTypeGrafanaProto
	default:
		return pipelinev1.PipelineSource_SOURCE_TYPE_UNSPECIFIED
	}
}

func sourceTypeProtoToString(s pipelinev1.PipelineSource_SourceType) string {
	switch s {
	case pipelinev1.PipelineSource_SOURCE_TYPE_GIT:
		return sourceTypeGit
	case pipelinev1.PipelineSource_SOURCE_TYPE_TERRAFORM:
		return sourceTypeTerraform
	case sourceTypeGrafanaProto:
		return sourceTypeGrafana
	default:
		return sourceTypeUnspecified
	}
}
