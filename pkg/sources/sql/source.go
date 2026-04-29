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

// Package sqlsource implements the sources.Source interface backed by a
// generic database/sql endpoint. It mirrors the design of pkg/sources/http:
// a typed Config struct constructed by the controller (so the package has no
// k8s.io/api/core/v1 dependency), and lazy *sql.DB construction so tests can
// inject a sqlmock-backed handle.
//
// The package name is sqlsource (not sql) to avoid a collision with the
// database/sql standard library import.
package sqlsource

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/grafana/fleet-management-operator/pkg/sources"
)

// defaultTimeout is the per-Fetch context timeout applied when Config.Timeout
// is zero. Mirrors httpsource.defaultTimeout so operator-wide source behavior
// stays consistent.
const defaultTimeout = 30 * time.Second

// supportedDrivers enumerates the database/sql drivers this package registers.
// The check is duplicated in New so callers get a clear error before any
// connection attempt; sql.Open itself only fails lazily on the first query.
var supportedDrivers = map[string]struct{}{
	"postgres": {},
	"mysql":    {},
}

// Config is the typed construction input for a SQL Source.
//
// The controller adapts v1alpha1.SQLSourceSpec plus secret material into a
// Config; keeping this struct decoupled means the package has no
// k8s.io/api/core/v1 dependency and can be exercised directly from tests.
type Config struct {
	// Driver names the database/sql driver to use. Required. Must be one of
	// "postgres" or "mysql".
	Driver string

	// Query is the SQL SELECT to execute on each Fetch. Required.
	Query string

	// DSN is the driver-specific connection string. Sourced from the
	// controller's referenced Secret (key: "dsn"). Required.
	DSN string

	// Timeout is the per-Fetch context timeout, applied to both Open and
	// Query. Zero means use the package default (30s).
	Timeout time.Duration
}

// Source is the database/sql sources.Source implementation. The *sql.DB
// handle is opened lazily on the first Fetch so construction does not require
// network connectivity, and tests can inject a sqlmock-backed handle via
// newWithDB.
type Source struct {
	cfg Config
	db  *sql.DB
}

// Compile-time interface check.
var _ sources.Source = (*Source)(nil)

// New validates cfg and returns a Source whose *sql.DB will be opened on the
// first Fetch. Validation is up-front so the controller surfaces config
// errors as ValidationFailed events rather than waiting for a network round
// trip.
func New(cfg Config) (*Source, error) {
	driver := strings.ToLower(strings.TrimSpace(cfg.Driver))
	if driver == "" {
		return nil, fmt.Errorf("sqlsource: driver is required")
	}
	if _, ok := supportedDrivers[driver]; !ok {
		return nil, fmt.Errorf(
			"sqlsource: unsupported driver %q (supported: postgres, mysql)",
			cfg.Driver,
		)
	}
	cfg.Driver = driver

	if strings.TrimSpace(cfg.Query) == "" {
		return nil, fmt.Errorf("sqlsource: query is required")
	}
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, fmt.Errorf("sqlsource: DSN is required")
	}

	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}

	return &Source{cfg: cfg}, nil
}

// newWithDB is the test-only constructor that bypasses validation and
// driver-name checks so tests can inject a sqlmock-backed *sql.DB. It is
// unexported on purpose: production callers should use New.
//
// Picking this over a "WithDB Option" pattern: the option-functional approach
// would still require a real driver name and DSN to satisfy New, which
// sqlmock can't supply without polluting the global driver registry. A
// dedicated test factory keeps the public API minimal and the test wiring
// obvious.
func newWithDB(cfg Config, db *sql.DB) *Source {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	return &Source{cfg: cfg, db: db}
}

// Kind returns the stable identifier matching v1alpha1.ExternalSourceKindSQL.
func (s *Source) Kind() string { return "SQL" }

// Fetch executes the configured query and returns one Record per row. Column
// names become record keys; values are coerced to the canonical types the
// controller's sources.FieldString helper understands:
//
//   - []byte (typical for VARCHAR/TEXT columns) is converted to string.
//   - string, bool, int64, float64 are stored as-is.
//   - nil values are omitted from the record map (so RequiredKeys checks
//     treat them as absent rather than as the empty string).
//   - Anything else falls back to fmt.Sprintf("%v", v); database/sql's
//     default Scan target set is small enough that this branch is rarely
//     hit in practice but keeps the source robust against driver quirks.
//
// The Fetch context is wrapped with cfg.Timeout if the caller-supplied
// context has no earlier deadline, so a slow query does not pin the
// reconciler indefinitely.
func (s *Source) Fetch(ctx context.Context) ([]sources.Record, error) {
	ctx, cancel := s.fetchContext(ctx)
	defer cancel()

	db, err := s.handle()
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, s.cfg.Query)
	if err != nil {
		return nil, fmt.Errorf("sqlsource: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("sqlsource: columns: %w", err)
	}

	out := make([]sources.Record, 0)
	for rows.Next() {
		// Allocate a fresh slice per row: rows.Scan retains pointers into
		// the values slice between calls, and reusing the buffer would
		// alias bytes across records.
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("sqlsource: scan: %w", err)
		}

		rec := make(sources.Record, len(cols))
		for i, col := range cols {
			v := normalize(raw[i])
			if v == nil {
				// Omit the key so RequiredKeys treats it as absent.
				continue
			}
			rec[col] = v
		}
		out = append(out, rec)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlsource: rows: %w", err)
	}

	return out, nil
}

// Close releases the underlying *sql.DB. Safe to call on a Source whose DB
// was never opened (e.g. when New succeeded but Fetch was never invoked).
func (s *Source) Close() error {
	if s.db == nil {
		return nil
	}
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("sqlsource: close: %w", err)
	}
	return nil
}

// handle returns the lazily-opened *sql.DB. Open is only attempted on the
// first call; subsequent Fetches reuse the same handle.
func (s *Source) handle() (*sql.DB, error) {
	if s.db != nil {
		return s.db, nil
	}
	db, err := sql.Open(s.cfg.Driver, s.cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("sqlsource: open %s: %w", s.cfg.Driver, err)
	}
	s.db = db
	return db, nil
}

// fetchContext applies cfg.Timeout as a context deadline unless the caller
// already supplied an earlier one. Returning context.WithTimeout's cancel
// func unconditionally lets callers defer it without a nil check.
func (s *Source) fetchContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) <= s.cfg.Timeout {
		// Caller's deadline is already at least as tight as ours; just
		// return a no-op cancel to keep the call site uniform.
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, s.cfg.Timeout)
}

// normalize coerces the dynamic types returned by database/sql.Scan into the
// canonical set documented in Fetch. See package doc for the full table.
func normalize(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		// database/sql delivers most string-shaped columns as []byte
		// regardless of declared type. Assume UTF-8 — the operator
		// targets attribute strings, which are required to be UTF-8 by
		// Fleet Management.
		return string(x)
	case string, bool, int64, float64:
		return x
	default:
		return fmt.Sprintf("%v", x)
	}
}
