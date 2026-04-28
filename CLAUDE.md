# CLAUDE.md

Never ever use emojis in the code base and documentation except alloy or OTel icon.

## Project Overview

Kubernetes operator for managing Grafana Cloud Fleet Management Pipelines as CRDs.

Fleet Management distributes Alloy/OTEL configs to collectors using Prometheus-style matchers. Pipelines are configuration fragments assigned to collectors based on attribute matching.

**Key concepts:**
- **ConfigType**: "Alloy" (default) or "OpenTelemetryCollector" - must match collector type
- **Matchers**: Prometheus Alertmanager syntax (e.g., `collector.os=linux`, `team!=team-a`)
- **Rate limit**: 3 req/s for Fleet Management API
- **Collectors poll every 5m** - changes aren't instant

## Common Commands

```bash
# Generate code and manifests
make generate && make manifests

# Run tests
make test

# Run controller locally (against current kubeconfig)
make run

# Install CRDs to cluster
make install

# Build and deploy
make docker-build IMG=<registry>/fleet-management-operator:tag
make docker-push IMG=<registry>/fleet-management-operator:tag
make deploy IMG=<registry>/fleet-management-operator:tag

# Cleanup
make undeploy

# Format code
go fmt ./...
goimports -w .

# Lint
make lint
make lint-fix

# End-to-end tests (requires Kind; creates cluster fm-crd-test-e2e automatically)
make test-e2e
```

## Code Style

- Use structured logging with controller-runtime logger (key-value pairs)
- Return errors with fmt.Errorf("%w", err) wrapping
- Use Status().Update() not Update() for status changes
- Verify interface implementation at compile time: `var _ Interface = &Struct{}`
- Always defer resp.Body.Close() for HTTP responses
- Define interfaces in consumer package (controller), not provider (client)
- Use table-driven tests with testify/assert

## Critical Fleet Management API Behaviors

**Base URL:** `https://fleet-management-<CLUSTER_NAME>.grafana.net/pipeline.v1.PipelineService/`

**Authentication:** Basic auth with username/token

**CRITICAL - Update Semantics:**
- UpsertPipeline and UpdatePipeline are NOT selective
- Unset fields are REMOVED (not preserved)
- Always send ALL spec fields when calling Upsert
- Example: If you omit `matchers`, they will be deleted from the pipeline

**Rate Limiting:**
- Management endpoints: configurable; **default 3 req/s** (match to your Fleet Management
  server-side `api:` setting via `--fleet-api-rps` / `fleetManagement.apiRatePerSecond`)
- Implement with golang.org/x/time/rate: `rate.NewLimiter(rate.Limit(rps), burst)`
- Use `fleetclient.WithRateLimit(rps, burst)` when constructing the client
- **burst=50 is the default** — absorbs startup/restart spikes. burst=1 causes livelock:
  with a 30s HTTP timeout, request #(rps×30+1) in a restart wave waits 30s and times out,
  indistinguishable from a Fleet API outage.
- Use limiter.Wait(ctx) before each API call

**API Operations:**
- Use UpsertPipeline (idempotent, recommended for controllers)
- Returns full pipeline object - use it for status updates (avoid extra GetPipeline)
- validate_only: true for dry-run validation

## ConfigType: Critical Validation

**IMPORTANT:** ConfigType must match both the configuration syntax AND the target collector type.

**Alloy (default):**
- River/HCL-like syntax with component blocks
- Example: `prometheus.scrape "default" { }`
- For Grafana Alloy collectors only

**OpenTelemetryCollector:**
- YAML with receivers/processors/exporters/service sections
- Example: `receivers: { otlp: { protocols: { grpc: {} } } }`
- For OpenTelemetry Collector instances only

**Validation rules:**
- Validate configType matches contents syntax BEFORE API call
- Alloy configs should start with component blocks (not "receivers:")
- OTEL configs must be valid YAML with "service" section
- Mismatched types cause config errors on collectors

**CRD to API mapping:**
- CRD `Alloy` → API `CONFIG_TYPE_ALLOY`
- CRD `OpenTelemetryCollector` → API `CONFIG_TYPE_OTEL`

## Controller Architecture

**Reconciliation pattern:**
1. Check ObservedGeneration - skip if spec unchanged
2. Use finalizers for deletion (handle 404 gracefully)
3. Call UpsertPipeline (idempotent)
4. Update status with ID and timestamps
5. Set status conditions (Ready, Synced)

**CRITICAL Patterns:**
- Use status.observedGeneration to skip reconcile when spec unchanged
- Status updates: Use Status().Update(), never Update()
- Finalizer must handle 404 on delete (already deleted = success)
- Don't call GetPipeline unless debugging (UpsertPipeline returns full object)
- Don't call ListPipelines on every reconcile (rate limit)
- Return errors, don't swallow with Requeue: true
- **IsConflict on Status().Update()**: return `ctrl.Result{Requeue: true}, nil` —
  NO error, NO exponential backoff. A conflict is cache lag, not a transient API
  error; returning an error would trigger workqueue backoff, adding unnecessary delay.
- **SyncPeriod deliberately NOT set** in ctrl.Options. An explicit resync period
  triggers full reconcile storms on every interval. Use watch events and status-driven
  RequeueAfter instead. Do not add SyncPeriod without understanding the Fleet API
  rate budget.
- **MaxConcurrentReconciles:** Pipeline and Collector must stay at 1 — they share
  the Fleet API rate budget; parallelising them queues more requests at the rate
  limiter without increasing throughput. Policy, ExternalSync, and Discovery are
  safe to parallelise (local K8s cache reads or per-source Fetch calls with no
  shared Fleet API calls). Defaults: policy=4, sync=4, discovery=1 (configurable
  via `--controller-{policy,sync,discovery}-max-concurrent` or the Helm
  `controllers.*.maxConcurrent` value).

**Finalizer:**
- Name: `pipeline.fleetmanagement.grafana.com/finalizer`
- **Add finalizer BEFORE the first Fleet API call** — persisted first so a crash
  between add and API call leaves the CR protected, not leaked.
- On deletion: call DeletePipeline, handle 404 as success, remove finalizer
- **Remove finalizer ONLY after Fleet cleanup succeeds or returns 404** — this
  ordering is the only window that prevents external resource leaks on pod restart.

**Status Conditions:**
- Ready: Pipeline successfully synced to Fleet Management
- Synced: Last reconciliation succeeded
- ValidationError: Pipeline contents failed validation

## Project-Specific Gotchas

- Pipeline name must be unique across entire Fleet Management (consider namespace prefixing)
- Informer cache has no read-your-writes consistency - use ObservedGeneration pattern
- Collectors poll every 5m by default - changes aren't instant
- Matchers have 200 character limit per matcher
- Matchers are AND'd together (all must match)
- Multiple pipelines can match same collector

## Validation Webhook

**IMPORTANT:** Admission webhook validates Pipeline resources before creation/update:
- Validates configType matches contents syntax (Alloy vs OTEL)
- Validates Prometheus matcher syntax (=, !=, =~, !~)
- Enforces 200 character limit per matcher
- Rejects empty contents
- Provides clear error messages

**Common validation errors:**
- Using `==` instead of `=` in matchers
- ConfigType mismatch (Alloy config marked as OpenTelemetryCollector)
- Missing `service` section in OTEL configs

See `docs/webhook-setup.md` for setup instructions.

## TenantPolicy (opt-in K8s RBAC tenancy)

Cluster-scoped `TenantPolicy` CRD binds K8s subjects (groups, users, SAs)
to a set of required matchers. When `--enable-tenant-policy-enforcement`
is set (Helm: `controllers.tenantPolicy.enabled: true`), the validating
webhooks for `Pipeline`, `RemoteAttributePolicy`, and
`ExternalAttributeSync` require that at least one of the matched
subject's required matchers appears in the CR's matcher set. Default-allow
when no policy matches the requesting user.

Webhook plumbing: each CR has a private `*Validator` struct in
`api/v1alpha1/*_webhook.go` that holds a `MatcherChecker` interface (see
`api/v1alpha1/webhook_tenant.go`). The concrete checker is
`internal/tenant.Checker` and is constructed in `cmd/main.go` only when
the flag is on. Tests in `api/v1alpha1/webhook_tenant_test.go` use a fake
checker; tests in `internal/tenant/checker_test.go` exercise subject
matching, multi-policy union, and namespaceSelector.

Status reconciler: when enforcement is on, `TenantPolicyReconciler`
(`internal/controller/tenant_policy_controller.go`) is also started. It
re-runs spec validation (matcher syntax, namespace selector parse) and
writes `Ready`/`Valid` conditions plus `status.boundSubjectCount` and
`status.observedGeneration`. No Fleet Management calls; no finalizer.

V1 gaps (documented): `selector.collectorIDs` bypasses matcher checks;
required-matcher semantics don't reason about negation/regex; `Collector`
and `CollectorDiscovery` are not covered. See `docs/tenant-policy.md`.

## Kubernetes Events

**Controller emits events for debugging:**
- Normal/Created: Pipeline created in Fleet Management
- Normal/Updated: Pipeline updated
- Normal/Synced: Successful reconciliation
- Normal/Deleted: Pipeline deleted
- Warning/SyncFailed: API errors
- Warning/ValidationFailed: Validation errors
- Warning/RateLimited: Hit rate limit
- Warning/Recreated: External deletion detected

**View events:**
```bash
kubectl describe pipeline <name>
kubectl get events --field-selector involvedObject.kind=Pipeline
```

## Testing

**Unit tests:**
- Mock Fleet Management API client (define FleetPipelineClient interface in controller package)
- Test with fake K8s client from controller-runtime
- Test finalizer handles 404 on delete
- Test rate limiting behavior
- Test ObservedGeneration skips reconcile
- **Webhook validation tests:** `go test ./api/v1alpha1/... -run TestPipeline_Validate`

**Integration tests:**
- Use envtest (controller-runtime test framework)
- CRD paths: `filepath.Join("..", "config", "crd", "bases")`

## Configuration

**Credentials stored in Secret:**
```yaml
env:
  - name: FLEET_MANAGEMENT_BASE_URL
    valueFrom:
      secretKeyRef:
        name: fleet-management-credentials
        key: base-url
  - name: FLEET_MANAGEMENT_USERNAME
    # Stack ID
  - name: FLEET_MANAGEMENT_PASSWORD
    # Cloud access token
```

## Project Structure

```
api/v1alpha1/                          # CRD types and webhooks
internal/controller/                   # Reconciliation logic
internal/controller/attributes/        # Attribute diff/merge/match helpers
internal/controller/discovery/         # CollectorDiscovery naming helpers
pkg/fleetclient/                       # Fleet Management API client
pkg/sources/                           # External source plugins (HTTP, SQL)
config/crd/                            # Generated CRD manifests
config/rbac/                           # RBAC roles
config/manager/                        # Controller deployment
config/samples/                        # Example CRs
```

## Collector / RemoteAttributePolicy / ExternalAttributeSync

Three additional CRDs manage collector remote attributes. They are individually opt-in via Helm and corresponding manager flags (Helm key → flag):
- `controllers.collector.enabled` → `--enable-collector-controller`
- `controllers.remoteAttributePolicy.enabled` → `--enable-policy-controller`
- `controllers.externalAttributeSync.enabled` → `--enable-external-sync-controller`
- `controllers.collectorDiscovery.enabled` → `--enable-collector-discovery-controller`

Default-off so existing chart installs see no behavior change. CRDs always install with the chart.

**Single-writer principle.** Only the Collector controller calls Fleet's `BulkUpdateCollectors`. RemoteAttributePolicy and ExternalAttributeSync controllers never write attributes directly; they expose intent through their own status (`status.matchedCollectorIDs`, `status.ownedKeys`) and trigger Collector reconciliation via watches. This is the only design that avoids a write-race when three controllers can claim the same key.

**Precedence (high to low):**
1. `ExternalAttributeSync` owned keys (per `status.ownedKeys[].attributes`)
2. `Collector` CR `spec.remoteAttributes`
3. `RemoteAttributePolicy` `spec.attributes` (highest `Priority` wins ties; equal-priority broken alphabetically by namespaced name)

The Collector controller computes the merged desired state on every reconcile and feeds it to `attributes.Diff` to emit ADD / REPLACE / REMOVE Operations. There is intentionally NO `ObservedGeneration` short-circuit on the Collector reconciler — cross-layer watches produce reconciles where the Collector spec generation is unchanged but upstream layers have moved. Idempotency is handled inside `updateStatusSuccess` via `mapsEqual` / `ownerSlicesEqual`.

**Finalizer:** `collector.fleetmanagement.grafana.com/finalizer`. On delete, the Collector controller emits REMOVE ops for every key it owned (across all owner kinds — it is the sole writer). 404 from Fleet on deletion is treated as success.

**Webhook validation** (per CRD):
- `Collector`: rejects `collector.*` reserved-prefix keys; immutable `spec.id`; max 100 attrs; value length cap 1024.
- `RemoteAttributePolicy`: same key/value rules; matcher syntax via `validateMatcherSyntax`; selector must be non-empty (matchers OR collectorIDs).
- `ExternalAttributeSync`: schedule must parse as either `time.ParseDuration` or 5-field cron via `cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow)`; HTTP/SQL kind/spec consistency.

**External source plugin model** (`pkg/sources`):
- `sources.Source` interface: `Fetch(ctx) ([]Record, error)`; `Kind() string`.
- HTTP impl in `pkg/sources/http`: bearer/basic auth via Secret keys `bearer-token` / `username`+`password`. Records-path supports dotted nesting (`data.items`).
- SQL impl in `pkg/sources/sql`: drivers `postgres` (lib/pq) and `mysql` (go-sql-driver). DSN via Secret key `dsn`. Tests use `DATA-DOG/go-sqlmock`.
- The factory in `cmd/main.go` (`buildExternalSourceFactory`) dispatches by `spec.source.kind`. Add new sources by extending it.

**Empty-result safety guard.** When `ExternalAttributeSync.Fetch` returns 0 records but the previous run had > 0 and `spec.allowEmptyResults` is false, the previous OwnedKeys claim is preserved and a `Stalled` condition is set. Set `allowEmptyResults: true` to opt out (e.g. when an empty result is legitimate).

## CollectorDiscovery

A fifth CRD, `CollectorDiscovery`, periodically calls Fleet Management's `ListCollectors` and creates one `Collector` CR per matching collector. Opt in via `controllers.collectorDiscovery.enabled` in the chart and `--enable-collector-discovery-controller` on the manager binary.

**Hard requirement.** `--enable-collector-discovery-controller=true` requires `--enable-collector-controller=true`. The manager refuses to start otherwise (discovery without the Collector reconciler creates CRs that nobody acts on).

**Tracking via labels and annotations, NOT OwnerReferences.** Discovered CRs carry:
- Label `fleetmanagement.grafana.com/discovery-name=<cd-name>` for label-selector lists.
- Annotation `fleetmanagement.grafana.com/discovered-by=<cd-namespace>/<cd-name>` for human-readable provenance.
- Annotation `fleetmanagement.grafana.com/fleet-collector-id=<original-id>` so the collector ID can be recovered even after name sanitization.

Owner refs are intentionally avoided so cascade-delete on the CD does NOT clobber user-added `spec.remoteAttributes`. Bulk cleanup uses `kubectl delete collector -l fleetmanagement.grafana.com/discovery-name=<name>`.

**Naming.** Fleet collector IDs are not guaranteed to be DNS-1123 valid (uppercase, dots, slashes are allowed). The reconciler uses `internal/controller/discovery/SanitizedName` to lowercase and replace invalid chars; if the transformation is lossy it appends a 5-character SHA-256 suffix to disambiguate two ids that sanitize to the same form. Collisions among lossless ids are detected and also fall back to the hashed form.

**Spec discipline.** Discovery only writes `spec.id` at creation. It never modifies a Collector CR's spec on subsequent polls — user edits to `spec.remoteAttributes`, `spec.enabled`, etc. survive forever. Discovery only manages CR existence and the stale annotation.

**Vanishing-collector policy.** `spec.policy.onCollectorRemoved` defaults to `Keep` (CR stays with `fleetmanagement.grafana.com/discovery-stale=true` annotation; status reports the collector ID in `staleCollectors`). `Delete` opts into clean-mirror semantics. The existing Collector finalizer issues REMOVE ops to Fleet on delete, but a vanished collector returns 404 (treated as success) — net no-op API call.

**Pagination caveat.** The Fleet Management SDK's `ListCollectorsRequest` does not currently expose `page_token` / `page_size`. A broad selector in a 30k fleet returns all collectors in one response (~30 MB). Adopt pagination transparently in `pkg/fleetclient/collector.go` when the SDK ships it — no CRD change required.

**Sharding pattern.** For fleets with >1000 collectors, create N CollectorDiscovery CRs with disjoint matchers — e.g. `env=production`, `env=staging`, `env=dev`. Each covers ~⌈fleet_size/N⌉ collectors; no single ListCollectors response becomes unwieldy. The admission webhook emits a Warning when `spec.selector` is empty (match-all).

**HA / leader-election.** When `--leader-elect` is set, controller-runtime gates the **entire** controller manager (including all reconcile dispatch) on the leader lease. Non-leader replicas do NOT run any reconciles. Discovery polling therefore runs only on the current leader. A lease failover causes the new leader to immediately begin polling; the first post-failover ListCollectors may consume measurable Fleet API budget.

**Webhook validation:**
- `pollInterval` must parse via `time.ParseDuration` and be `>= 1m` (rate-limiter protection).
- `selector` may be empty (mirror everything is legal); matcher syntax + 200-char cap apply when set. Empty selector emits an admission Warning (large-fleet risk).
- `targetNamespace` (when set) must be a valid DNS-1123 label.
- `policy.onCollectorRemoved ∈ {Keep, Delete}`; `policy.onConflict ∈ {Skip}` (TakeOwnership reserved for v2).

**Single-writer principle preserved.** The discovery reconciler never calls `BulkUpdateCollectors`. Only the Collector reconciler writes attributes. Discovery only creates / deletes Collector CRs and writes its own status.

**No watches on the discovery controller.** Discovery is purely poll-driven via `RequeueAfter`. Generation bumps (spec edits) bypass the schedule check by clearing the `observedGeneration == generation` guard.

## External Documentation

For detailed information, use the `/fleet-api` and `/controller-patterns` skills.

- Fleet Management API details: Use `/fleet-api` skill
- Kubernetes controller best practices: Use `/controller-patterns` skill
- Alloy config syntax: https://grafana.com/docs/alloy/latest/
- OTEL config syntax: https://opentelemetry.io/docs/collector/configuration/
