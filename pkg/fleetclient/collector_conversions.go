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
	collectorv1 "github.com/grafana/fleet-management-api/api/gen/proto/go/collector/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func collectorToProto(c *Collector) *collectorv1.Collector {
	if c == nil {
		return nil
	}
	out := &collectorv1.Collector{
		Id:               c.ID,
		Name:             c.Name,
		RemoteAttributes: c.RemoteAttributes,
		LocalAttributes:  c.LocalAttributes,
		CollectorType:    collectorTypeStringToProto(c.CollectorType),
	}
	if c.Enabled != nil {
		enabled := *c.Enabled
		out.Enabled = &enabled
	}
	if c.CreatedAt != nil {
		out.CreatedAt = timestamppb.New(*c.CreatedAt)
	}
	if c.UpdatedAt != nil {
		out.UpdatedAt = timestamppb.New(*c.UpdatedAt)
	}
	if c.MarkedInactiveAt != nil {
		out.MarkedInactiveAt = timestamppb.New(*c.MarkedInactiveAt)
	}
	return out
}

func collectorFromProto(c *collectorv1.Collector) *Collector {
	if c == nil {
		return nil
	}
	out := &Collector{
		ID:               c.GetId(),
		Name:             c.GetName(),
		RemoteAttributes: c.GetRemoteAttributes(),
		LocalAttributes:  c.GetLocalAttributes(),
		CollectorType:    collectorTypeProtoToString(c.GetCollectorType()),
	}
	// Preserve the optional/unset distinction: only populate Enabled when the
	// proto explicitly set it. The pb.Collector struct holds a *bool so we can
	// inspect the raw field without losing tri-state semantics.
	if c.Enabled != nil {
		enabled := *c.Enabled
		out.Enabled = &enabled
	}
	if c.GetCreatedAt() != nil {
		t := c.GetCreatedAt().AsTime()
		out.CreatedAt = &t
	}
	if c.GetUpdatedAt() != nil {
		t := c.GetUpdatedAt().AsTime()
		out.UpdatedAt = &t
	}
	if c.GetMarkedInactiveAt() != nil {
		t := c.GetMarkedInactiveAt().AsTime()
		out.MarkedInactiveAt = &t
	}
	return out
}

func collectorTypeStringToProto(s string) collectorv1.CollectorType {
	switch s {
	case collectorTypeAlloy:
		return collectorv1.CollectorType_COLLECTOR_TYPE_ALLOY
	case collectorTypeOTEL:
		return collectorv1.CollectorType_COLLECTOR_TYPE_OTEL
	default:
		return collectorv1.CollectorType_COLLECTOR_TYPE_UNSPECIFIED
	}
}

func collectorTypeProtoToString(c collectorv1.CollectorType) string {
	switch c {
	case collectorv1.CollectorType_COLLECTOR_TYPE_ALLOY:
		return collectorTypeAlloy
	case collectorv1.CollectorType_COLLECTOR_TYPE_OTEL:
		return collectorTypeOTEL
	default:
		return ""
	}
}

func operationToProto(o *Operation) *collectorv1.Operation {
	if o == nil {
		return nil
	}
	out := &collectorv1.Operation{
		Op:   opTypeStringToProto(o.Op),
		Path: o.Path,
	}
	// Value, OldValue and From are proto3 optional (*string). ADD and REPLACE
	// always carry Value, even when it is the explicit empty string. REMOVE
	// without Value leaves it unset, which means "remove unconditionally".
	if o.Op == OpAdd || o.Op == OpReplace || o.Value != "" {
		v := o.Value
		out.Value = &v
	}
	if o.OldValue != "" {
		v := o.OldValue
		out.OldValue = &v
	}
	if o.From != "" {
		v := o.From
		out.From = &v
	}
	return out
}

func operationsToProto(ops []*Operation) []*collectorv1.Operation {
	if ops == nil {
		return nil
	}
	out := make([]*collectorv1.Operation, 0, len(ops))
	for _, o := range ops {
		out = append(out, operationToProto(o))
	}
	return out
}

func opTypeStringToProto(o OpType) collectorv1.Operation_OpType {
	switch o {
	case OpAdd:
		return collectorv1.Operation_ADD
	case OpRemove:
		return collectorv1.Operation_REMOVE
	case OpReplace:
		return collectorv1.Operation_REPLACE
	case OpMove:
		return collectorv1.Operation_MOVE
	case OpCopy:
		return collectorv1.Operation_COPY
	default:
		return collectorv1.Operation_UNKNOWN
	}
}
