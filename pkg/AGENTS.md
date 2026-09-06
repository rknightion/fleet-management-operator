# pkg

The Fleet Management API client, the external source plugins for ExternalAttributeSync, and the
SSRF guard both of those depend on.

## fleetclient

- The constructor is `NewClient(baseURL, username, password, opts ...ClientOption) *Client`. It
  returns no error; a bad base URL surfaces on the first call.
- `baseURL` may be the service-suffixed form ending `/pipeline.v1.PipelineService/`, which is what
  the operator's Secret stores, or the bare server root. `normalizeBaseURL` strips the suffix, so
  both work and neither is a bug to fix.
- `WithRateLimit(rps, burst)` silently substitutes the package defaults 3 rps and burst 50 for a
  zero or negative argument, so a misconfigured flag presents as a working default rather than an
  error.
- `Limiter()` is exposed for instrumentation and tests. Calling `SetLimit` or `SetBurst` on the
  returned limiter at runtime is unsupported: it races concurrent callers and silently diverges
  from the configured budget. Construct a new client instead.
- The HTTP client timeout is 30s, and the chart's `terminationGracePeriodSeconds` is sized against
  it.
- The metric names in `metrics.go` (`fleet_api_requests_total`, `fleet_api_errors_total`,
  `fleet_api_request_duration_seconds`, `fleet_api_rate_limiter_wait_duration_seconds`) are
  consumed by the chart's shipped dashboard JSON and by `prometheusrule.yaml`. Renaming one blanks
  a panel and silences an alert with nothing going red.

## netguard

The SSRF denylist shared by the ExternalAttributeSync webhook and the HTTP source. There is no
allowlist and no opt-out flag, by design.

- `ValidateHostname` runs at admission and rejects `localhost`, `*.localhost`, `*.local`, `*.svc`,
  `*.cluster.local` and disallowed IP literals, so an in-cluster HTTP source target is unreachable
  on purpose.
- `GuardedDialContext` installs a `Control` hook that re-checks the resolved address on every dial.
  That is the only thing closing DNS rebinding and a 3xx redirect to an internal address; admission
  cannot. Any new outbound HTTP path must build its transport with it or it inherits neither guard.
- `GuardedDialContext` mutates the `*net.Dialer` it is given. Pass a freshly constructed one.

## sources

`source.go` declares the `Source` interface. Concrete kinds are dispatched by
`buildExternalSourceFactory` in `cmd/main.go`; a kind added to the CRD but not to that switch
compiles and fails at reconcile time with `unknown ExternalSource kind`.

Unit tests here must not take a live database dependency. The SQL source accepts an injected
`*sql.DB` and skips its driver-name check so tests can hand it `DATA-DOG/go-sqlmock`; `just check`
runs on a bare toolchain with no Docker daemon.

## Deeper references

- `reference/attributes-and-discovery.md`, section "External source plugins" - the auth Secret
  keys, the supported SQL drivers, the records-path shape and the empty-result guard.
