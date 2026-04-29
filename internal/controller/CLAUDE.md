# internal/controller — Reconciliation Logic

All controller reconcilers live here. Each controller has a `_controller.go` and a `_controller_test.go`.

## Controllers

| File | CRD | Fleet API calls | MaxConcurrent |
|------|-----|-----------------|---------------|
| `pipeline_controller.go` | Pipeline | UpsertPipeline, DeletePipeline | 1 (shared rate budget) |
| `collector_controller.go` | Collector | BulkUpdateCollectors | 1 (shared rate budget) |
| `policy_controller.go` | RemoteAttributePolicy | none (K8s cache reads) | 4 (default) |
| `external_sync_controller.go` | ExternalAttributeSync | none (external HTTP/SQL) | 4 (default) |
| `collector_discovery_controller.go` | CollectorDiscovery | ListCollectors | 1 |
| `tenant_policy_controller.go` | TenantPolicy | none | 4 (default) |

## Subdirectories

| Dir | Purpose |
|-----|---------|
| `attributes/` | Diff/merge/match helpers for remote attribute precedence |
| `discovery/` | `SanitizedName` for turning Fleet collector IDs into DNS-1123 Collector names |

## Running Tests

```bash
# All controller tests (uses envtest)
make test

# Specific controller
go test ./internal/controller/... -run TestPipelineReconciler
go test ./internal/controller/... -run TestCollectorDiscovery
```

## Reconciler Template (Pipeline as canonical example)

```
1. Fetch CR; if not found return nil (already deleted)
2. Handle deletion: if DeletionTimestamp set → call DeletePipeline → remove finalizer
3. Add finalizer BEFORE first Fleet API call (crash between add and call leaves CR protected)
4. Check ObservedGeneration — skip if spec unchanged (Pipeline/RemoteAttributePolicy/ExternalAttributeSync only)
5. Call UpsertPipeline (returns full object — use it for status, no extra GetPipeline)
6. Update status conditions (Ready, Synced) via Status().Update()
7. On IsConflict from Status().Update(): return ctrl.Result{Requeue: true}, nil (no error, no backoff)
```

## Critical Rules

- **Single writer for BulkUpdateCollectors**: only `collector_controller.go` calls it. Policy and ExternalSync controllers expose intent via their own status and trigger Collector reconcile via watches — never call Fleet directly.
- **No ObservedGeneration short-circuit on Collector**: cross-layer watches produce reconciles where the Collector spec is unchanged but upstream layers have moved.
- **Finalizer name**: `pipeline.fleetmanagement.grafana.com/finalizer` (Pipeline), `collector.fleetmanagement.grafana.com/finalizer` (Collector).
- **404 on delete = success**: treat 404 from Fleet API during finalizer cleanup as already-deleted, remove finalizer and return nil.
- **No SyncPeriod in ctrl.Options**: causes full reconcile storms. Use watch events and RequeueAfter instead.
- **IsConflict on status update**: return `ctrl.Result{Requeue: true}, nil` — a conflict is cache lag, not an API error.

## Attribute Precedence (Collector reconciler)

High to low:
1. ExternalAttributeSync owned keys (`status.ownedKeys[].attributes`)
2. Collector CR `spec.remoteAttributes`
3. RemoteAttributePolicy `spec.attributes` (highest Priority wins ties; equal-priority broken alphabetically by namespaced name)

The Collector controller computes merged desired state on every reconcile and feeds it to `attributes.Diff` to produce ADD / REPLACE / REMOVE operations.

## Metrics

`metrics.go` defines Prometheus metrics for all controllers. Instrument new controllers here to keep dashboards consistent.

## Errors

`errors.go` defines sentinel error types for rate-limit errors, validation errors, and Fleet API errors. Use these instead of raw `fmt.Errorf` for error classification in controller logic.
