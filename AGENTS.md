# AGENTS.md

Canonical contributor and agent instructions for this repository. Claude Code reads it through
the `@AGENTS.md` import in `CLAUDE.md`; Codex reads this file directly. One file means the two
cannot drift apart - which they had, completely: `AGENTS.md` sat untouched from the original
kubebuilder scaffold commit while `CLAUDE.md` carried every real project rule, so Codex was
running against generic boilerplate that described none of this project.

Never ever use emojis in the code base and documentation except alloy or OTel icon.

## Tracker rules (non-negotiable)

Work is tracked in `backlog/` via the Backlog.md CLI. The queue is a query, not a file:
`backlog task list --plain`. Durable reference lives in `backlog doc list --plain`.

- Read the **Agent fan-out protocol (canonical)** doc before designing a wave, and the **Wave
  operating model** doc for this project's own rules. `backlog doc list --plain` shows both.
- **`backlog/` is committed to git, so tasks, docs and decisions must never contain real account
  identifiers or personal data** - no email addresses, handles, usernames, Grafana stack IDs,
  tenant IDs, cloud access tokens, collector IDs from a live fleet, cluster names, or hostnames.
  Write the shape, not the instance: `<stack-id>`, `fleet-management-<cluster>.grafana.net`,
  `collector-<n>`. Aggregate counts, timings and structural findings are fine. Sweep before
  committing:

  ```bash
  grep -rniE "rknightion|rob-knight|m7kni|@gmail|grafana\.net|glc_|[0-9]{7}" backlog/ && echo "REVIEW NEEDED"
  ```

- **Never use `--notes` or `--plan` bare.** They *silently replace* the whole section - an open
  upstream bug that destroys another session's writes with no warning. Use `--append-notes` and
  `--append-plan`. A global `PreToolUse` hook in the agent config denies the bare forms, including when they are quoted, which is how they used to slip past.
- **Finalize in one call**, so an interrupted agent cannot leave finished work looking unfinished:

  ```bash
  backlog task edit FMO-0001 --check-ac 1 --check-ac 2 -s Done
  ```

- **Never hand-edit task markdown.** Section boundaries are HTML-comment markers; break one and
  the section is silently dropped at exit code 0, invisible until the next write destroys it for
  real. There is no repair command - `backlog doctor` only fixes duplicate task IDs.
- **Never let two agents edit the same task.** The v1.50.x concurrency fix covers the edit funnel
  but not reorder, draft saves, the TUI path, `doc update` or decision updates.
- `backlog config.yml` is the one file you may hand-edit; list-valued keys cannot be set through
  `backlog config set`.

## GitHub issues: always pass `-R`

This checkout has two remotes - `origin` is `rknightion/fleet-management-operator` and `upstream`
is the fork source `mbaykara/fleet-management-operator`. A bare `gh issue list` resolves to
**upstream** and reports zero issues, which reads as "the board is empty" rather than "you asked
the wrong repo". Always pass `-R rknightion/fleet-management-operator`, and always
`--limit 1000` (`gh issue list` defaults to 30).

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
just gen

# Run tests
just test

# Run controller locally (against current kubeconfig)
just run

# Install CRDs to cluster
just install

# Build and deploy
IMG=<registry>/fleet-management-operator:tag just docker-build
IMG=<registry>/fleet-management-operator:tag just deploy

# Cleanup
just undeploy

# Format code
just fmt

# Lint
just lint
just lint-fix

# End-to-end tests (requires Kind; creates cluster fm-crd-test-e2e automatically)
just test-e2e

# Regenerate docs/events.md and other generated docs
just docs

# Regenerate Helm chart README from README.md.gotmpl (requires helm-docs)
just chart-docs
```

## Task interface

This repo's task surface is a `justfile`. Discover it, don't guess it:

    just --list                        # human-readable
    just --dump --dump-format json     # machine-readable
    just --show <recipe>               # what a recipe actually runs

- `just check` is the full gate and is exactly what CI enforces. It must pass before you commit.
- Prefer `just <recipe>` over the underlying tool. If you are typing `pytest`, you want `just test`.
- Run `just` with stdin from /dev/null. Recipes marked `[confirm]` are destructive — stop and ask
  before running one; never pass `--yes` or `JUST_YES=1`.
- If a task you need does not exist, add a recipe with a `#` doc comment and a `[group(...)]`
  rather than running a bare command.

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
- **Per-target ExternalAttributeSync rate limit (E19).** Two ExternalAttributeSync
  CRs pointing at the same upstream (HTTP host or SQL DSN secret) share a token
  bucket so `--controller-sync-max-concurrent` cannot stampede a customer-owned
  source. Configure via `--controller-sync-target-rate=<tokens/sec>` (default 0
  = disabled) and `--controller-sync-target-burst=<bucket-size>` (default 4,
  matching sync max-concurrent). Setting target-rate to 1 yields one fetch per
  second per upstream — typically plenty given that EAS schedules run at ≥1m.

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

Each controller emits Kubernetes events on significant reconcile outcomes.
The full per-controller table — Reason, EventType, Trigger — lives in
[`docs/events.md`](docs/events.md) and is regenerated from controller source
by `just docs`. Do not maintain the table by hand here.

**View events:**
```bash
kubectl describe pipeline <name>
kubectl get events --field-selector involvedObject.kind=Pipeline
kubectl get events --field-selector involvedObject.kind=Collector
kubectl get events --field-selector involvedObject.kind=CollectorDiscovery
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

**Credentials stored in Secret:** chart-managed; the chart names the Secret
`<release>-credentials` (default release name `fleet-management-operator`,
giving `fleet-management-operator-credentials`). Override via
`fleetManagement.existingSecret.name`.

```yaml
env:
  - name: FLEET_MANAGEMENT_BASE_URL
    valueFrom:
      secretKeyRef:
        name: fleet-management-operator-credentials  # <release>-credentials
        key: base-url
  - name: FLEET_MANAGEMENT_USERNAME
    # Stack ID
  - name: FLEET_MANAGEMENT_PASSWORD
    # Cloud access token
```

## Project Structure

```
api/v1alpha1/                          # CRD types and webhooks (see api/v1alpha1/CLAUDE.md)
cmd/                                   # main.go — flag wiring and controller registration
internal/controller/                   # Reconciliation logic (see internal/controller/CLAUDE.md)
internal/controller/attributes/        # Attribute diff/merge/match helpers
internal/controller/discovery/         # CollectorDiscovery naming helpers
internal/tenant/                       # TenantPolicy subject-matcher checker
pkg/fleetclient/                       # Fleet Management API client (see pkg/CLAUDE.md)
pkg/sources/                           # External source plugins (HTTP, SQL)
config/crd/                            # Generated CRD manifests (not managed by Helm upgrade)
config/rbac/                           # RBAC roles
config/manager/                        # Controller deployment
config/samples/                        # Example CRs
charts/fleet-management-operator/      # Helm chart (see charts/fleet-management-operator/CLAUDE.md)
docs/                                  # Runbooks, API reference, conditions, events, flags, metrics
```

## Sub-directory CLAUDE.md Files

Deeper context is in sub-directory files auto-discovered by Claude Code:

| File | When to read |
|------|-------------|
| `api/v1alpha1/CLAUDE.md` | Adding/modifying CRD types or webhooks |
| `internal/controller/CLAUDE.md` | Adding/modifying controllers or reconcile logic |
| `pkg/CLAUDE.md` | Working on the Fleet API client or external source plugins |
| `charts/fleet-management-operator/CLAUDE.md` | Helm chart values, templates, or release |

## Collector / RemoteAttributePolicy / ExternalAttributeSync

Three additional CRDs manage collector remote attributes. They are individually opt-in via Helm and corresponding manager flags (Helm key → flag):
- `controllers.collector.enabled` → `--enable-collector-controller`
- `controllers.remoteAttributePolicy.enabled` → `--enable-policy-controller`
- `controllers.externalAttributeSync.enabled` → `--enable-external-sync-controller`
- `controllers.collectorDiscovery.enabled` → `--enable-collector-discovery-controller`
- `controllers.pipelineDiscovery.enabled` → `--enable-pipeline-discovery-controller`

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
- SQL impl in `pkg/sources/sql`: drivers `postgres` (lib/pq) and `mysql` (go-sql-driver/mysql). DSN via Secret key `dsn`. Tests use `DATA-DOG/go-sqlmock`.
- Both HTTP and SQL kinds are currently shipped and wired through the factory in `cmd/main.go` (`buildExternalSourceFactory`), which dispatches on `spec.source.kind`. Add new kinds by extending it.

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

## Security Model

Full trust model and hardening guidance: [`docs/security.md`](docs/security.md).

Key facts when touching RBAC, webhooks, or the discovery/EAS controllers:
- Creating any `fleetmanagement.grafana.com` CR is an **effectively-privileged**
  action - Pipelines write to the shared org-wide Fleet credential; a
  `PipelineDiscovery`/`CollectorDiscovery` with `spec.targetNamespace` makes the
  operator create CRs in **other** namespaces (confused deputy).
- Cluster-wide `secrets` read is granted only when `externalAttributeSync` is
  enabled; cross-namespace `secretRef` is blocked at admission and reconcile.
- The chart ships **no** aggregated user roles by default. Opt-in
  `<release>-editor`/`-viewer` roles exist behind `rbac.userRoles.create`;
  aggregating them into the built-in `edit` role re-opens the confused deputy
  cluster-wide.
- `TenantPolicy` is a guardrail, not an authorization boundary (collectorIDs
  bypass, default-allow). Do not rely on it alone for tenancy.

## External Documentation

For detailed information, use the `/fleet-api` and `/controller-patterns` skills.

- Fleet Management API details: Use `/fleet-api` skill
- Kubernetes controller best practices: Use `/controller-patterns` skill
- Alloy config syntax: https://grafana.com/docs/alloy/latest/
- OTEL config syntax: https://opentelemetry.io/docs/collector/configuration/

<!-- BACKLOG.MD GUIDELINES START -->
<!-- backlog.md-instructions-version: 1.50.1 -->
<CRITICAL_INSTRUCTION>

## Backlog.md Workflow

This project uses Backlog.md for task and project management.

**For every user request in this project, run `backlog instructions overview` before answering or taking action.**

Use the overview to decide whether to search, read, create, or update Backlog tasks.

Before task lifecycle actions, read the matching detailed guide:
- `backlog instructions task-creation` before creating or splitting tasks
- `backlog instructions task-execution` before planning, changing status or assignee, adding a plan or implementation notes, or implementing task work
- `backlog instructions task-finalization` before checking acceptance criteria, writing final summaries, or moving tasks to terminal statuses

Use `backlog <command> --help` before running unfamiliar commands. Help shows options, fields, and examples.

Do not edit Backlog task, draft, document, decision, or milestone markdown files directly. Use the `backlog` CLI so metadata, relationships, and history stay consistent.

</CRITICAL_INSTRUCTION>
<!-- BACKLOG.MD GUIDELINES END -->
