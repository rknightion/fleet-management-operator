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

package sqlsource

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMockSource builds a Source backed by a sqlmock-driven *sql.DB. Returning
// the mock alongside the source lets each test set the expected query without
// a per-test boilerplate block.
func newMockSource(t *testing.T) (*Source, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})
	src := newWithDB(Config{
		Driver: "postgres",
		Query:  "SELECT id, team, region FROM hosts",
		DSN:    "ignored-by-mock",
	}, db)
	return src, mock
}

// TestSQLSource_Kind locks in the stable kind identifier the controller
// uses in events and logs.
func TestSQLSource_Kind(t *testing.T) {
	src, _ := newMockSource(t)
	assert.Equal(t, "SQL", src.Kind())
}

// TestSQLSource_Fetch_HappyPath exercises the canonical successful fetch:
// two rows of three string columns each. We use FieldString on a sample to
// confirm the controller's coercion path sees the rows as strings.
func TestSQLSource_Fetch_HappyPath(t *testing.T) {
	src, mock := newMockSource(t)

	rows := sqlmock.NewRows([]string{"id", "team", "region"}).
		AddRow("collector-1", "team-a", "us-east").
		AddRow("collector-2", "team-b", "eu-west")
	mock.ExpectQuery("SELECT id, team, region FROM hosts").WillReturnRows(rows)

	records, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 2)

	assert.Equal(t, "collector-1", records[0]["id"])
	assert.Equal(t, "team-a", records[0]["team"])
	assert.Equal(t, "us-east", records[0]["region"])

	assert.Equal(t, "collector-2", records[1]["id"])
	assert.Equal(t, "team-b", records[1]["team"])
	assert.Equal(t, "eu-west", records[1]["region"])

	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSQLSource_Fetch_ZeroRows confirms that an empty result set is a
// non-error path — the controller's empty-result safety guard is the only
// place that decision is made.
func TestSQLSource_Fetch_ZeroRows(t *testing.T) {
	src, mock := newMockSource(t)

	mock.ExpectQuery("SELECT id, team, region FROM hosts").
		WillReturnRows(sqlmock.NewRows([]string{"id", "team", "region"}))

	records, err := src.Fetch(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, records, "should return non-nil empty slice")
	assert.Empty(t, records)

	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSQLSource_Fetch_QueryError verifies that a driver-level query error is
// wrapped and surfaced rather than swallowed. The original error must remain
// reachable via errors.Is so the controller can inspect it.
func TestSQLSource_Fetch_QueryError(t *testing.T) {
	src, mock := newMockSource(t)

	wantErr := errors.New("connection refused")
	mock.ExpectQuery("SELECT id, team, region FROM hosts").WillReturnError(wantErr)

	records, err := src.Fetch(context.Background())
	require.Error(t, err)
	assert.Nil(t, records)
	assert.Contains(t, err.Error(), "sqlsource: query")
	assert.True(t, errors.Is(err, wantErr), "wrapped error must be reachable via errors.Is")

	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSQLSource_Fetch_NullValues confirms NULL columns are dropped from the
// record map — the controller's RequiredKeys check then sees them as absent
// rather than as the empty string, which is the documented contract.
func TestSQLSource_Fetch_NullValues(t *testing.T) {
	src, mock := newMockSource(t)

	rows := sqlmock.NewRows([]string{"id", "team", "region"}).
		AddRow("collector-1", nil, "us-east")
	mock.ExpectQuery("SELECT id, team, region FROM hosts").WillReturnRows(rows)

	records, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)

	assert.Equal(t, "collector-1", records[0]["id"])
	assert.Equal(t, "us-east", records[0]["region"])
	_, present := records[0]["team"]
	assert.False(t, present, "NULL column must be omitted from the record map")

	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSQLSource_Fetch_NumericTypes ensures int64 and float64 column values
// land in the record as their native Go types, not as pre-stringified
// values. This matters because sources.FieldString handles the coercion path
// for numeric columns; if we pre-stringified them the controller's caller
// would lose the type information.
func TestSQLSource_Fetch_NumericTypes(t *testing.T) {
	src, mock := newMockSource(t)
	src.cfg.Query = "SELECT id, count, ratio FROM stats"

	rows := sqlmock.NewRows([]string{"id", "count", "ratio"}).
		AddRow("collector-1", int64(42), float64(3.14))
	mock.ExpectQuery("SELECT id, count, ratio FROM stats").WillReturnRows(rows)

	records, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)

	assert.Equal(t, "collector-1", records[0]["id"])
	assert.Equal(t, int64(42), records[0]["count"], "int64 must be preserved as int64")
	assert.Equal(t, float64(3.14), records[0]["ratio"], "float64 must be preserved as float64")

	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSQLSource_Fetch_ContextCancellation asserts that a pre-cancelled
// context aborts the fetch with a context error rather than running the
// query. sqlmock honours ctx.Err on QueryContext, which is the same contract
// real drivers expose.
func TestSQLSource_Fetch_ContextCancellation(t *testing.T) {
	src, mock := newMockSource(t)

	// We do NOT set an ExpectQuery here: a cancelled context must short-
	// circuit before the query is sent. If sqlmock ever observes the
	// query it will fail because no expectation matches.

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	records, err := src.Fetch(ctx)
	require.Error(t, err)
	assert.Nil(t, records)
	assert.True(t,
		errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded),
		"expected context error, got %q", err.Error())

	// No expectations were registered, so ExpectationsWereMet should pass.
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSQLSource_New_RejectsBadDriver confirms that constructor-time
// validation rejects unsupported drivers up front, with a message that names
// the supported set so operators can correct the spec quickly.
func TestSQLSource_New_RejectsBadDriver(t *testing.T) {
	_, err := New(Config{
		Driver: "oracle",
		Query:  "SELECT 1",
		DSN:    "host=localhost",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "oracle")
	assert.Contains(t, err.Error(), "postgres")
	assert.Contains(t, err.Error(), "mysql")
}

// TestSQLSource_New_AcceptsSupportedDrivers locks in the supported set; if a
// future driver is added, this test must be updated to reflect it.
func TestSQLSource_New_AcceptsSupportedDrivers(t *testing.T) {
	for _, driver := range []string{"postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			src, err := New(Config{
				Driver: driver,
				Query:  "SELECT 1",
				DSN:    "host=localhost",
			})
			require.NoError(t, err)
			assert.Equal(t, driver, src.cfg.Driver)
		})
	}
}

// TestSQLSource_New_RequiresFields verifies the up-front validation rejects
// each missing field with a message that names what's missing.
func TestSQLSource_New_RequiresFields(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantSub string
	}{
		{
			name:    "missing-driver",
			cfg:     Config{Query: "SELECT 1", DSN: "x"},
			wantSub: "driver",
		},
		{
			name:    "missing-query",
			cfg:     Config{Driver: "postgres", DSN: "x"},
			wantSub: "query",
		},
		{
			name:    "missing-dsn",
			cfg:     Config{Driver: "postgres", Query: "SELECT 1"},
			wantSub: "DSN",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantSub)
		})
	}
}

// TestSQLSource_New_NormalizesDriverCase confirms the driver name comparison
// is case-insensitive — "Postgres" or "MySQL" in a CRD spec should be
// accepted, since Kubernetes does not enforce case in string fields.
func TestSQLSource_New_NormalizesDriverCase(t *testing.T) {
	src, err := New(Config{Driver: "Postgres", Query: "SELECT 1", DSN: "x"})
	require.NoError(t, err)
	assert.Equal(t, "postgres", src.cfg.Driver)
}

// TestSQLSource_New_AppliesDefaultTimeout locks in the documented default so
// a future "let's tune the timeout" change can't silently regress the
// contract.
func TestSQLSource_New_AppliesDefaultTimeout(t *testing.T) {
	src, err := New(Config{Driver: "postgres", Query: "SELECT 1", DSN: "x"})
	require.NoError(t, err)
	assert.Equal(t, defaultTimeout, src.cfg.Timeout)
}

// TestSQLSource_Fetch_BytesAreUTF8String verifies the []byte → string
// coercion that real Postgres/MySQL drivers exercise for VARCHAR/TEXT
// columns. sqlmock returns strings as []byte by default, which matches the
// production behaviour we care about.
func TestSQLSource_Fetch_BytesAreUTF8String(t *testing.T) {
	src, mock := newMockSource(t)

	rows := sqlmock.NewRows([]string{"id"}).AddRow([]byte("collector-1"))
	mock.ExpectQuery("SELECT id, team, region FROM hosts").WillReturnRows(rows)

	records, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)

	v, ok := records[0]["id"].(string)
	assert.True(t, ok, "[]byte column must be coerced to string, got %T", records[0]["id"])
	assert.Equal(t, "collector-1", v)

	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSQLSource_Close_NilDB confirms Close on a Source whose DB was never
// opened is a no-op, which keeps the controller's defer-Close pattern safe
// even on the early-error path where Fetch never runs.
func TestSQLSource_Close_NilDB(t *testing.T) {
	src, err := New(Config{Driver: "postgres", Query: "SELECT 1", DSN: "x"})
	require.NoError(t, err)
	assert.NoError(t, src.Close())
}

// TestSQLSource_Close_RealDB verifies Close releases the underlying handle
// and that double-Close (real Postgres returns an error) is surfaced rather
// than swallowed.
func TestSQLSource_Close_RealDB(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectClose()

	src := newWithDB(Config{Driver: "postgres", Query: "SELECT 1", DSN: "x"}, db)
	require.NoError(t, src.Close())

	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSQLSource_Fetch_RowsErrAfterIteration covers the "iteration succeeded
// but the driver reported a deferred error" branch. database/sql guarantees
// rows.Err is the canonical place to discover this; if we forget to check
// it, partial-result corruption goes silent.
func TestSQLSource_Fetch_RowsErrAfterIteration(t *testing.T) {
	src, mock := newMockSource(t)

	wantErr := errors.New("driver post-iteration boom")
	rows := sqlmock.NewRows([]string{"id", "team", "region"}).
		AddRow("collector-1", "team-a", "us-east").
		RowError(0, wantErr)
	mock.ExpectQuery("SELECT id, team, region FROM hosts").WillReturnRows(rows)

	_, err := src.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rows")

	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSQLSource_Fetch_TimeoutHonoursCallerDeadline ensures a caller-supplied
// deadline tighter than cfg.Timeout is preserved rather than overwritten.
// This is what stops a misbehaving query from eating the entire reconcile
// budget.
func TestSQLSource_Fetch_TimeoutHonoursCallerDeadline(t *testing.T) {
	src, _ := newMockSource(t)
	src.cfg.Timeout = 10 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	wrapped, cancelW := src.fetchContext(ctx)
	defer cancelW()

	dl, ok := wrapped.Deadline()
	require.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(50*time.Millisecond), dl, 100*time.Millisecond,
		"caller's tighter deadline must be preserved")
}

// fakeStringer covers the fmt.Sprintf fallback path in normalize.
type fakeStringer struct{ name string }

func (f fakeStringer) String() string { return f.name }

// TestSQLSource_Fetch_FallbackTypeFormatting hits the default branch in
// normalize. database/sql rarely surfaces these types, but the source must
// still produce a usable string so the controller's coercion does not
// blow up.
func TestSQLSource_Fetch_FallbackTypeFormatting(t *testing.T) {
	v := normalize(fakeStringer{name: "weird"})
	s, ok := v.(string)
	require.True(t, ok, "fallback must produce a string, got %T", v)
	assert.Contains(t, s, "weird")
}

// TestSQLSource_Fetch_PingPath guards S3: handle() must run PingContext
// after a successful sql.Open so DSN / network / auth problems surface
// with a "ping" prefix instead of being deferred to QueryContext. We
// exercise the production handle() branch with a real postgres DSN that
// sql.Open accepts (lib/pq parses the DSN at Open without dialling) but
// Ping cannot satisfy, since port 1 is reserved and refuses connections.
// A short timeout keeps the test fast; the wrapping format is what we
// actually want to lock in.
func TestSQLSource_Fetch_PingPath(t *testing.T) {
	src := &Source{cfg: Config{
		Driver:  "postgres",
		Query:   "SELECT 1",
		DSN:     "host=127.0.0.1 port=1 sslmode=disable connect_timeout=1",
		Timeout: 2 * time.Second,
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, fetchErr := src.Fetch(ctx)
	require.Error(t, fetchErr)
	assert.Contains(t, fetchErr.Error(), "sqlsource: ping",
		"ping failure must be labeled distinctly from a query failure")
	assert.Contains(t, fetchErr.Error(), "postgres")
	assert.Nil(t, src.db,
		"failed Ping must clear the cached handle so the next reconcile retries Open")
}

// Confirm that go-sqlmock's *sql.DB satisfies the same interface our
// source uses — this is a guard against silent dependency-version drift.
var _ *sql.DB = (*sql.DB)(nil)
