# Remote attributes, discovery and tenancy

Design invariants for the `Collector`, `RemoteAttributePolicy`, `ExternalAttributeSync`,
`CollectorDiscovery` and `TenantPolicy` CRDs. Read before changing any of those controllers,
their webhooks, or the merge/diff path.

## Opt-in wiring

Every CRD except `Pipeline` is default-off. CRDs always install with the chart; only the
reconcilers are gated.

| Helm value | Manager flag |
|---|---|
| `controllers.collector.enabled` | `--enable-collector-controller` |
| `controllers.remoteAttributePolicy.enabled` | `--enable-policy-controller` |
| `controllers.externalAttributeSync.enabled` | `--enable-external-sync-controller` |
| `controllers.collectorDiscovery.enabled` | `--enable-collector-discovery-controller` |
| `controllers.pipelineDiscovery.enabled` | `--enable-pipeline-discovery-controller` |
| `controllers.tenantPolicy.enabled` | `--enable-tenant-policy-enforcement` |

`--enable-collector-discovery-controller=true` requires `--enable-collector-controller=true`.
The manager refuses to start otherwise: discovery without the Collector reconciler creates CRs
nobody acts on.

## Single-writer principle

Only the Collector controller calls Fleet's `BulkUpdateCollectors`. RemoteAttributePolicy,
ExternalAttributeSync and the discovery controllers never write attributes; they publish intent
through their own status (`status.matchedCollectorIDs`, `status.ownedKeys`) and trigger Collector
reconciliation via watches. This is the only shape that avoids a write race when three layers can
claim the same key. Do not add a second writer.

Precedence, high to low:

1. `ExternalAttributeSync` owned keys (`status.ownedKeys[].attributes`)
2. `Collector` `spec.remoteAttributes`
3. `RemoteAttributePolicy` `spec.attributes` (highest `Priority` wins; equal priority broken
   alphabetically by namespaced name)

The Collector controller recomputes merged desired state on every reconcile and feeds it to
`attributes.Diff`, which emits ADD / REPLACE / REMOVE operations.

There is deliberately NO `ObservedGeneration` short-circuit on the Collector reconciler.
Cross-layer watches produce reconciles where the Collector spec generation is unchanged but an
upstream layer has moved. Idempotency lives in `updateStatusSuccess` via `mapsEqual` /
`ownerSlicesEqual`. Do not "fix" this by adding the guard.

Finalizer `collector.fleetmanagement.grafana.com/finalizer`: on delete the Collector controller
emits REMOVE ops for every key it owned across all owner kinds, because it is the sole writer.
404 from Fleet is success.

## Per-target ExternalAttributeSync rate limit

Two ExternalAttributeSync CRs pointing at the same upstream (HTTP host, or SQL DSN secret) share
a token bucket, so `--controller-sync-max-concurrent` cannot stampede a customer-owned source.
`--controller-sync-target-rate` (tokens/sec, default 0 = disabled) and
`--controller-sync-target-burst` (default 4, matching sync max-concurrent). Target-rate 1 is
usually plenty: EAS schedules run at 1m or slower.

## Empty-result safety guard

When `Fetch` returns 0 records, the previous run had more than 0, and `spec.allowEmptyResults` is
false, the previous OwnedKeys claim is preserved and a `Stalled` condition is set. Set
`allowEmptyResults: true` only where an empty upstream is legitimate.

## External source plugins (`pkg/sources`)

- `sources.Source`: `Fetch(ctx) ([]Record, error)` and `Kind() string`.
- HTTP (`pkg/sources/http`): auth from Secret keys `bearer-token`, or `username` + `password`.
  Bearer wins when both are present. `recordsPath` supports dotted nesting (`data.items`).
- SQL (`pkg/sources/sql`): drivers `postgres` (lib/pq) and `mysql` (go-sql-driver/mysql), DSN from
  Secret key `dsn`. Tests use `DATA-DOG/go-sqlmock`.
- New kinds are added by extending `buildExternalSourceFactory` in `cmd/main.go`, which dispatches
  on `spec.source.kind`.

## CollectorDiscovery

Polls Fleet's `ListCollectors` and creates one `Collector` CR per match.

Tracking is by labels and annotations, NOT OwnerReferences, so cascade-delete on the
CollectorDiscovery cannot clobber user-added `spec.remoteAttributes`:

- label `fleetmanagement.grafana.com/discovery-name=<cd-name>`
- annotation `fleetmanagement.grafana.com/discovered-by=<cd-namespace>/<cd-name>`
- annotation `fleetmanagement.grafana.com/fleet-collector-id=<original-id>`, so the collector id
  survives name sanitisation
- annotation `fleetmanagement.grafana.com/discovery-stale=true` on a vanished collector

Bulk cleanup: `kubectl delete collector -l fleetmanagement.grafana.com/discovery-name=<name>`.

**Naming.** Fleet collector ids are not guaranteed DNS-1123 valid (uppercase, dots, slashes are
legal). `internal/controller/discovery.SanitizedName` lowercases and replaces invalid characters;
a lossy transformation gets a 5-character SHA-256 suffix. Collisions among lossless ids also fall
back to the hashed form.

**Spec discipline.** Discovery writes `spec.id` at creation and never touches a Collector CR spec
again. User edits to `spec.remoteAttributes`, `spec.enabled` and the rest survive forever.
Discovery manages CR existence and the stale annotation only.

**Vanishing collectors.** `spec.policy.onCollectorRemoved` defaults to `Keep` (CR stays, stale
annotation set, id reported in `status.staleCollectors`). `Delete` opts into clean-mirror
semantics; the Collector finalizer then issues REMOVE ops that Fleet answers 404 to, a net no-op.

**Pagination.** `ListCollectorsRequest` in `github.com/grafana/fleet-management-api` v1.3.0 carries
only `Matchers` - no `page_token` / `page_size`. A broad selector against a 30k fleet returns
everything in one ~30 MB response. Adopt pagination in `pkg/fleetclient/collector.go` when the SDK
ships it; no CRD change needed.

**Sharding.** Above ~1000 collectors, create N CollectorDiscovery CRs with disjoint matchers
(`env=production`, `env=staging`, ...). The webhook emits an admission Warning on an empty
selector.

**No watches.** Discovery is purely poll-driven via `RequeueAfter`. A spec edit bypasses the
schedule check by clearing the `observedGeneration == generation` guard.

**Leader election.** `--leader-elect` gates the entire manager, including all reconcile dispatch,
on the lease. Non-leader replicas run nothing. A failover makes the new leader poll immediately,
and that first `ListCollectors` can consume measurable Fleet API budget.

## Webhook validation rules

- `Collector`: rejects the reserved `collector.` key prefix; `spec.id` immutable; max 100
  attributes; value length cap 1024.
- `RemoteAttributePolicy`: same key/value rules; matcher syntax via `validateMatcherSyntax`;
  selector must be non-empty (matchers or collectorIDs).
- `ExternalAttributeSync`: `schedule` must parse as either `time.ParseDuration` or a 5-field cron
  (`cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow`); HTTP/SQL kind and spec must agree.
- `CollectorDiscovery`: `pollInterval` must parse via `time.ParseDuration` and be at least 1m
  (rate-limiter protection); `selector` may be empty (Warning); `targetNamespace` must be a valid
  DNS-1123 label; `policy.onCollectorRemoved` in {Keep, Delete}; `policy.onConflict` in {Skip}
  (TakeOwnership is reserved for v2).

## TenantPolicy

Cluster-scoped CRD binding K8s subjects (groups, users, service accounts) to a set of required
matchers. With enforcement on, the validating webhooks for `Pipeline`, `RemoteAttributePolicy` and
`ExternalAttributeSync` require at least one of the matched subject's required matchers to appear
in the CR's matcher set. Default-allow when no policy matches the requester.

Plumbing: each CR has a private `*Validator` in `api/v1alpha1/*_webhook.go` holding a
`MatcherChecker` (`api/v1alpha1/webhook_tenant.go`). The concrete checker is
`internal/tenant.Checker`, constructed in `cmd/main.go` only when the flag is on.
`TenantPolicyReconciler` (`internal/controller/tenant_policy_controller.go`) starts alongside it,
re-runs spec validation and writes `Ready`/`Valid` conditions plus `status.boundSubjectCount` and
`status.observedGeneration`. No Fleet calls, no finalizer.

Known v1 gaps: `selector.collectorIDs` bypasses matcher checks; required-matcher semantics do not
reason about negation or regex; `Collector` and `CollectorDiscovery` are not covered. It is a
guardrail, not an authorization boundary. `docs/tenant-policy.md` carries the user-facing version.
