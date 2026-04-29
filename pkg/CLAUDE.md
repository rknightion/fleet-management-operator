# pkg — Fleet Client and External Source Plugins

## fleetclient/

Go client wrapping the Fleet Management gRPC-over-HTTP API.

```
client.go           # FleetClient struct; constructor with functional options
types.go            # Request/response types mirroring the protobuf API
conversions.go      # CRD spec → API proto conversion helpers
collector.go        # ListCollectors, BulkUpdateCollectors
collector_types.go  # Collector-specific types
interceptors.go     # HTTP interceptors: rate limiting, retry, tracing
metrics.go          # Per-call Prometheus metrics
tracing.go          # OpenTelemetry tracing
errors.go           # API error types (rate limit, not found, etc.)
```

### Constructing the Client

```go
client, err := fleetclient.New(baseURL, username, password,
    fleetclient.WithRateLimit(rps, burst),
)
```

- Use `fleetclient.WithRateLimit(rps, burst)` — never construct the rate limiter outside the client.
- Default burst is 50; do not lower below 10 (burst=1 causes livelock under restart waves).
- `interceptors.go` wires `limiter.Wait(ctx)` before every call.

### Key API Behaviors (enforced by client)

- `UpsertPipeline` and `UpdatePipeline` are NOT selective — unset fields are removed. Always send the full spec.
- `UpsertPipeline` returns the full pipeline object; use it for status updates to avoid a second `GetPipeline` call.
- `validate_only: true` for dry-run validation.

### Interface Definition

The `FleetPipelineClient` interface is defined in `internal/controller/` (consumer package), not here. This follows the Go idiom of defining interfaces where they are used. The client struct satisfies the interface implicitly.

## sources/

External source plugins for ExternalAttributeSync.

```
source.go       # Source interface: Fetch(ctx) ([]Record, error); Kind() string
http/           # HTTP source (bearer/basic auth; dotted records-path support)
sql/            # SQL source (postgres via lib/pq; mysql via go-sql-driver)
```

### Adding a New Source Kind

1. Create `pkg/sources/<kind>/` with a struct implementing the `Source` interface.
2. Add a case in `cmd/main.go`'s `buildExternalSourceFactory` dispatch switch.
3. Add a `spec.source.kind` constant in `api/v1alpha1/external_sync_types.go`.
4. Update the webhook validator to accept the new kind and validate `spec.source.<kind>Spec`.

### HTTP Source

- Auth: Secret key `bearer-token` (bearer) or `username`+`password` (basic auth).
- `records-path` supports dotted nesting: `data.items` traverses `response["data"]["items"]`.

### SQL Source

- `dsn` read from Secret key `dsn`.
- Supported drivers: `postgres`, `mysql`.
- Tests use `DATA-DOG/go-sqlmock` — do not add a live DB dependency to unit tests.

### Empty-Result Safety

When a Fetch returns 0 records but the previous run had > 0, and `spec.allowEmptyResults` is false, the controller preserves the previous OwnedKeys claim and sets a `Stalled` condition. Set `allowEmptyResults: true` to opt out.
