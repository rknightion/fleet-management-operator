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

import "time"

// Collector is the wire-public representation of a Fleet Management collector.
// It mirrors the Pipeline shape pattern in types.go: snake_case proto field
// names are translated to Go conventions, json tags use camelCase, and
// optional/nullable fields are modelled with pointers so callers can
// distinguish "unset" from "zero value".
type Collector struct {
	ID               string            `json:"id"`
	Name             string            `json:"name,omitempty"`
	Enabled          *bool             `json:"enabled,omitempty"`
	RemoteAttributes map[string]string `json:"remoteAttributes,omitempty"`
	LocalAttributes  map[string]string `json:"localAttributes,omitempty"`
	CollectorType    string            `json:"collectorType,omitempty"`
	CreatedAt        *time.Time        `json:"createdAt,omitempty"`
	UpdatedAt        *time.Time        `json:"updatedAt,omitempty"`
	MarkedInactiveAt *time.Time        `json:"markedInactiveAt,omitempty"`
}

// OpType identifies the kind of mutation an Operation performs against a
// collector's remote_attributes map. Values are intentionally upper-case to
// mirror the proto enum names (without the "Operation_" prefix).
type OpType string

const (
	OpAdd     OpType = "ADD"
	OpRemove  OpType = "REMOVE"
	OpReplace OpType = "REPLACE"
	OpMove    OpType = "MOVE"
	OpCopy    OpType = "COPY"
)

// Operation is a JSON-patch-style mutation applied to a collector's
// remote_attributes by BulkUpdateCollectors. See the proto comments on
// collector.v1.Operation for the per-op semantics; in particular: Path uses
// JSON-path syntax (e.g. "/remote_attributes/env"), and From is only
// meaningful for MOVE/COPY.
type Operation struct {
	Op       OpType `json:"op"`
	Path     string `json:"path"`
	Value    string `json:"value,omitempty"`
	OldValue string `json:"oldValue,omitempty"`
	From     string `json:"from,omitempty"`
}

// Wire-public CollectorType strings, matching the proto enum names. Kept as
// untyped string constants so the public Collector.CollectorType field stays a
// plain string (consistent with Pipeline.ConfigType).
const (
	collectorTypeAlloy = "COLLECTOR_TYPE_ALLOY"
	collectorTypeOTEL  = "COLLECTOR_TYPE_OTEL"
)
