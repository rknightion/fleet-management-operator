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

// Package sources defines the pluggable interface for the
// ExternalAttributeSync controller. A Source produces a stream of records
// that the controller maps to (collectorID, attribute) pairs and applies to
// the in-cluster state.
//
// The package is intentionally decoupled from api/v1alpha1: each concrete
// implementation (HTTP, SQL) takes a typed config struct, and the controller
// adapts the v1alpha1 spec at construction time. This keeps source code
// unit-testable without spinning up a Kubernetes client and lets new sources
// be added without touching CRDs.
package sources

import "context"

// Record is one row of source data: a string-keyed map whose values are the
// raw types returned by the underlying source (string for HTTP/JSON
// primitives, the database/sql column type for SQL, etc.). Mapping into
// attribute key/value strings is performed by the controller using
// AttributeMapping; sources stay format-agnostic.
type Record map[string]any

// Source is the interface every external-source implementation satisfies.
// Implementations are constructed once per ExternalAttributeSync reconcile
// and used to perform a single Fetch. They are NOT expected to be
// long-lived or thread-safe — the controller does not share Source
// instances across reconciles.
type Source interface {
	// Fetch retrieves the current record set. Empty results are valid;
	// the empty-result safety guard is enforced by the controller, not
	// the source.
	Fetch(ctx context.Context) ([]Record, error)

	// Kind returns a stable identifier for the source type, matching the
	// v1alpha1.ExternalSourceKind name (e.g. "HTTP", "SQL"). Used in
	// logs and events.
	Kind() string
}

// FieldString returns the string form of a record's field, performing the
// minimal coercion every source needs:
//
//   - string → string
//   - bool, int, float64 → fmt.Sprint result
//   - everything else → empty string and ok=false
//
// This is exposed for source implementations that want to avoid each
// re-implementing the same coercion table.
func FieldString(r Record, key string) (string, bool) {
	v, ok := r[key]
	if !ok || v == nil {
		return "", false
	}
	switch s := v.(type) {
	case string:
		return s, true
	case bool:
		if s {
			return "true", true
		}
		return "false", true
	case int:
		return formatInt64(int64(s)), true
	case int32:
		return formatInt64(int64(s)), true
	case int64:
		return formatInt64(s), true
	case float64:
		return formatFloat(s), true
	case float32:
		return formatFloat(float64(s)), true
	}
	return "", false
}

// formatInt64 / formatFloat avoid pulling in strconv at the package
// boundary so importers see a tiny dependency surface.
func formatInt64(v int64) string {
	const radix = 10
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var buf [20]byte
	pos := len(buf)
	for v > 0 {
		pos--
		buf[pos] = byte('0' + v%radix)
		v /= radix
	}
	if negative {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func formatFloat(v float64) string {
	// Defer to strconv via fmt for floats — formatting them by hand is
	// not worth the code volume relative to integer optimization.
	return fmtSprint(v)
}
