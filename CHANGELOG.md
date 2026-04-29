# Changelog

All notable changes to the Fleet Management Operator will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- TenantPolicy CRD with opt-in Kubernetes RBAC tenancy enforcement, plus its
  status reconciler (`Ready` / `Valid` conditions, `boundSubjectCount`).
- Collector, RemoteAttributePolicy, ExternalAttributeSync, and CollectorDiscovery
  CRDs, controllers, and admission webhooks (all default-off; opt in per controller).
- External source plugins for ExternalAttributeSync: HTTP (bearer / basic auth)
  and SQL (postgres via `lib/pq`, mysql via `go-sql-driver/mysql`). Both kinds
  ship in this release; the factory in `cmd/main.go` dispatches on
  `spec.source.kind`.
- CEL-based structural validation on CRD schemas: `Collector.spec.id`
  immutability, matcher caps, configType-vs-contents constraints.
- API versioning and graduation policy doc, plus a cross-CRD condition
  type/reason registry.
- Helm chart templates: webhook Service, ValidatingWebhookConfiguration,
  cert-manager Certificate, PodDisruptionBudget, ServiceMonitor, PrometheusRule
  with operator alerts, and an embedded Grafana dashboard ConfigMap (DOC-01/02,
  WH-01/02).
- Operator metrics: Fleet API request counters and rate-limiter wait histogram
  (OBS-01/02); reconcile-outcome counters (OBS-06); sync-age histogram, owned-key
  gauge, discovery-list-size gauge (OBS-03/04/05); OpenTelemetry tracing for
  Fleet API calls, noop by default (OBS-07).
- Per-target rate limiter for ExternalAttributeSync sources (E19): two syncs
  pointing at the same upstream (HTTP host or SQL secret) share a token bucket
  via `--controller-sync-target-rate` and `--controller-sync-target-burst`.
  Default off.
- Per-controller `MaxConcurrentReconciles` (policy=4, sync=4, discovery=1) with
  `--controller-{policy,sync,discovery}-max-concurrent` flags and matching Helm
  values (PERF-04). Pipeline and Collector remain at 1 by design.
- Selective Collector watch handler indexed by matcher key (PERF-03): policy
  changes now wake only the matching Collectors instead of every Collector.
- Helm chart values exposing `fleetManagement.apiRatePerSecond` and
  `fleetManagement.apiBurst` (configurable Fleet API rate limit / burst).
- Helm chart values exposing `image.digest`, `webhook.port`, leader-election
  lease tunables, and probe / security tunables (HELM-02/03/04/06/09).
- Production-readiness audit scorecard (`docs/superpowers/audits/`) and full
  troubleshooting guide / per-alert runbooks / webhook setup guide
  (DOC-03/04/05).
- Sample manifests: annotated invalid-CR examples for onboarding.
- Renovate configuration for dependency updates.
- Auto-generated chart README via helm-docs (`make chart-docs`,
  `make chart-docs-check`).

### Changed
- Memory defaults raised to limits=2Gi / requests=512Mi (HELM-01); 128Mi default
  was insufficient at 30k-Collector informer-cache footprint and would OOMKill.
  Sizing matrix in `values.yaml`.
- Liveness probe `initialDelaySeconds` raised so pods are not killed during
  initial cache warm-up at 30k CRs (HELM-08).
- Production logging defaults: structured JSON output, info level
  (`Development: false`).
- Fleet API HTTP client now closes its connection pool on shutdown (UPG-03);
  webhook port is a Helm value with startup cert validation (WH-04).
- Webhook entries set `timeoutSeconds: 5` (WH-02).
- `RemoteAttributePolicy.status.matchedCollectorIDs` capped at 1000 with
  `matchedCount` field (PERF-01); `ExternalAttributeSync.status.ownedKeys`
  capped at 1000 with no-op short-circuit; `CollectorDiscovery.status.conflicts`
  capped at 100 (PERF-06).
- CLAUDE.md: documented REC reconciler invariants, per-target sync rate limiter,
  per-controller event reasons, and updated SQL plugin to "currently shipped"
  (was Phase-3-only stub).

### Fixed
- Validating webhooks for Collector and CollectorDiscovery now validate the
  incoming `obj`, not the empty receiver. Previously, the framework's empty
  `*Collector{}` / `*CollectorDiscovery{}` receiver was being validated, so
  every admission request trivially passed (WH-05 follow-up).
- PERF-03 silent correctness regressions and no-op short-circuit gaps fixed
  (Batch B).
- TenantPolicy correctness, including D9 webhook markers (Batch C).
- Install-blocking Helm chart defects (Batch A): consolidated duplicate webhook
  sections, fixed RBAC/template inconsistencies.
- Fleet client interceptor / manager lifecycle / OTEL footguns (Batch D).
- Conflict-policy reconcile path now treats `ctrl.Result.Requeue=true` from
  status-conflict as cache lag (no error, no exponential backoff).
- 404 from Fleet API on Pipeline / Collector deletion is treated as success.
- Helm chart: leader-election lease flags wired; production log defaults
  applied; container ports declared on Deployment; metrics endpoint properly
  bound (HELM-02/04/06).
- Chart README: regenerated from `values.yaml` (helm-docs); previous manual
  table claimed `limits.memory: 128Mi`, drift since HELM-01 fix.
- Documentation: deployment / Secret / webhook-Service names now match the
  actual chart-rendered names; memory-limit references updated to reflect the
  2Gi default.
- Test: graceful-shutdown test now drives a real `PipelineReconciler` to verify
  context propagation through the full reconciler → client → interceptor chain
  (E1, replaces tautological stub).
- Lint: `make lint` from 44 issues to 0 (modernize/prealloc/errcheck/unused
  cleanups; no behaviour change).

### Security
- Container image base pinned to digest; added `image.digest` Helm value (SEC-01).
- Pod and container security context hardened: non-root, read-only root FS,
  dropped capabilities, restricted seccomp profile (SEC-02/03/04).
- Race detector enabled in `make test` target (TEST-01).

### Upgrade Notes
- Helm value renames: `fleet-management-credentials` Secret is now
  `<release>-credentials` (default `fleet-management-operator-credentials`).
  Existing self-managed Secrets continue to work via
  `fleetManagement.existingSecret.name`.
- Webhook Service is now `<release>-webhook` (was `<release>-webhook-service`).
- New CRDs (`Collector`, `RemoteAttributePolicy`, `ExternalAttributeSync`,
  `CollectorDiscovery`, `TenantPolicy`) install with the chart; controllers
  remain disabled until you set `controllers.<name>.enabled: true`.
- `controllers.collectorDiscovery.enabled: true` requires
  `controllers.collector.enabled: true`; the manager refuses to start otherwise.

## [0.1.0] - YYYY-MM-DD

### Added
- Initial release of Fleet Management Operator
- Pipeline CRD for managing Fleet Management pipelines
- Support for Alloy and OpenTelemetry Collector configurations
- Multi-architecture Docker images (linux/amd64, linux/arm64)
- Helm chart for easy deployment
- Source tracking (Git, Terraform, Kubernetes)
- Finalizer support for proper cleanup
- Status conditions following Kubernetes conventions
- Metrics endpoint on port 8080
- Leader election for high availability

[Unreleased]: https://github.com/grafana/fleet-management-operator/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/grafana/fleet-management-operator/releases/tag/v0.1.0
