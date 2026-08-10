---
title: Architecture
description: Reconcile loop, CRD-to-controller mapping, admission webhooks, and how fleet-management-operator talks to Grafana Cloud.
---

# Architecture

## Components

A single `manager` binary (`cmd/main.go`) runs everything: one `controller-runtime` manager
hosting a reconciler per enabled CRD, a validating admission webhook server, a metrics
endpoint, and health probes. There is no separate agent or sidecar — the manager Pod is the
whole operator.

```
Kubernetes API server
   │  watch / list / status update
   ▼
manager Pod
 ├─ Pipeline reconciler                ─┐
 ├─ PipelineDiscovery reconciler        │  each has its own admission
 ├─ Collector reconciler                │  webhook, registered only if
 ├─ CollectorDiscovery reconciler       │  the matching controller flag
 ├─ RemoteAttributePolicy reconciler    │  is enabled
 ├─ ExternalAttributeSync reconciler    │
 └─ TenantPolicy reconciler (status)   ─┘
        │ rate-limited HTTP client (pkg/fleetclient)
        ▼
 Grafana Cloud Fleet Management API (pipeline.v1.PipelineService)
```

## Controllers (`internal/controller/`)

Each CRD except `TenantPolicy` has a dedicated file:
`pipeline_controller.go`, `pipeline_discovery_controller.go`, `collector_controller.go`,
`collector_discovery_controller.go`, `policy_controller.go` (`RemoteAttributePolicy`),
`external_sync_controller.go`, and `tenant_policy_controller.go`. All of them are watch-driven
— `SyncPeriod` is deliberately left unset so there is no full-resync storm on an interval; a
reconcile fires only on a CRD change or a status-driven `RequeueAfter`.

**Reconcile pattern**, common to the Fleet-backed controllers (`Pipeline`, `Collector`,
`RemoteAttributePolicy`, `ExternalAttributeSync`, the two discovery controllers):

1. Fetch the CR from the informer cache (a single `Get`, no `List` in the hot path for
   per-resource controllers).
2. Skip if `status.observedGeneration` already matches `metadata.generation` — the spec has
   not changed since the last successful sync.
3. Add a finalizer before the first Fleet API call, so a delete always has a chance to clean
   up the remote side.
4. Call the Fleet Management API (`UpsertPipeline`, `BulkUpdateCollectors`, `ListCollectors`,
   the external source's `Fetch`, and so on) through the shared rate-limited client.
5. Write `status` (ID, sync timestamps, counts) via `Status().Update()` — never the plain
   `Update()`, which would race with spec changes — and set `Ready` / `Synced` conditions via
   `meta.SetStatusCondition`.
6. Emit a Kubernetes event summarising the outcome. See [events.md](events.md) for the full
   reason catalogue and [conditions.md](conditions.md) for condition types/reasons.
7. On delete: call the matching Fleet delete API, treat 404 as success, then remove the
   finalizer.

`Pipeline` and `Collector` are kept at `MaxConcurrentReconciles=1` because they share the same
Fleet API rate budget — running them concurrently only queues more requests at the limiter
without raising throughput. `RemoteAttributePolicy`, `ExternalAttributeSync`, and the discovery
controllers are safe to parallelise (pure cache reads, or one `Fetch`/`List` call per source)
and default to higher concurrency; see [flags.md](flags.md) for the exact defaults and the
`--controller-*-max-concurrent` flags that override them.

`TenantPolicy`'s own reconciler does not call Fleet Management at all — it only re-validates
the spec (matcher syntax, namespace selector) and mirrors the result into `status`.

## Fleet API client (`pkg/fleetclient/`)

A single shared client wraps every outbound call in:

- A token-bucket rate limiter (`golang.org/x/time/rate`), default 3 requests/second with a
  burst of 50 — see [flags.md](flags.md) for `--fleet-api-rps` / `--fleet-api-burst`.
- Prometheus instrumentation (`fleet_api_requests_total`, `fleet_api_errors_total`,
  `fleet_api_request_duration_seconds`, `fleet_api_rate_limiter_wait_duration_seconds`) — see
  [metrics.md](metrics.md).
- Optional OpenTelemetry trace spans, wired through when `OTEL_EXPORTER_OTLP_ENDPOINT` is set
  in the manager's environment. Tracing is a no-op exporter by default; setting that
  environment variable switches to a real batched OTLP-over-gRPC exporter configured from the
  standard `OTEL_EXPORTER_OTLP_*` variables, with a bounded 5s flush on shutdown.

Authentication is HTTP Basic auth against the Stack ID and API token supplied via the
`fleetManagement.*` Helm values or an `existingSecret`.

## External sources (`pkg/sources/`)

`ExternalAttributeSync` delegates to a pluggable `Source` interface with two implementations
selected by `spec.source.kind`: `pkg/sources/http` (bearer or basic auth, JSON response with an
optional dotted `recordsPath`) and `pkg/sources/sql` (a single read-only `SELECT` against
Postgres or MySQL, DML/DDL keyword-denylisted). `pkg/netguard` backs the HTTP source's SSRF
defences — an admission-time denylist plus a dial-time re-check of the resolved IP — described
in [Security Model and Hardening](security.md#external-sources-ssrf).

## Admission webhooks (`api/v1alpha1/*_webhook.go`)

Every CRD has a validating webhook (no mutating webhooks), registered only for the CRDs whose
controller is enabled — the discovery and attribute controllers ship their webhook alongside
their reconciler. Webhooks enforce what CRD schema validation cannot express as OpenAPI
structural rules: matcher syntax, `configType` agreeing with `contents`, reserved
`collector.`-prefixed attribute keys, namespace-name syntax on `spec.targetNamespace`, and,
when the relevant manager flags are set, [TenantPolicy](tenant-policy.md) matcher requirements
and the [cross-namespace discovery `SubjectAccessReview`](security.md#cross-namespace-authority)
check. All webhooks are registered with `failurePolicy: Fail`, so a webhook outage blocks new
creates/updates rather than silently admitting unvalidated specs — see
[Webhook TLS Setup](webhook-setup.md) for how the serving certificate is provisioned.

## Multi-tenancy layer (`internal/tenant/`)

Backs the `TenantPolicy` CRD's admission-time checks: reading the admission request's
`UserInfo`, matching it against `TenantPolicy.spec.subjects`, and unioning
`requiredMatchers` across every policy that matches. See [Tenant Policy](tenant-policy.md) for
the full mechanism, coverage, and known v1 gaps.

## Where to go next

- [flags.md](flags.md) — every manager flag and its Helm value.
- [metrics.md](metrics.md) — the Prometheus metrics this reconcile loop and client emit.
- [security.md](security.md) — the privilege model this architecture implies.
- [versioning.md](versioning.md) — how this API group evolves from `v1alpha1`.
