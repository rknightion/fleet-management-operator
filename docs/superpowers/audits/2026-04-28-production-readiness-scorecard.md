# Production Readiness Scorecard

**Date:** 2026-04-28
**Source commit:** `013f042`
**Calibration:** single tenant, 30,000 collectors, thousands of pipelines, single replica, pre-production heading toward production. Fleet Management API rate limit 3 req/s.
**Methodology:** see `docs/superpowers/specs/2026-04-28-production-readiness-audit-design.md`. Executed via 10 parallel Haiku sub-agents (one per category) and a main-agent aggregation pass that applied the category-ownership rules to de-duplicate cross-category overlaps.

---

## 1. Executive Summary

The operator's foundations are sound. Reconciliation correctness, security/RBAC posture, image hardening, and webhook design choices are all defensible. The v1.1 milestone work (cache audit, reconcile-loop audit, watch-pattern audit) holds up under spot-check and gives the codebase a meaningful safety floor that is unusual at this stage.

The **production-readiness gap is not in the controller logic; it is in the deployment, scale-tuning, and operability surface**. Three workstreams compound to make the operator non-viable as-currently-configured against the stated 30k-collector target:

1. **Helm chart defaults are sized for a demo, not a fleet.** 128Mi memory limit will OOMKill at the 30k-collector informer-cache footprint; the metrics endpoint is silently unbound (`--metrics-bind-address` flag is never passed); log level is hard-set to development; webhook TLS is undocumented.
2. **Two scale-correctness defects need code changes**: the Fleet API rate-limiter has burst=1 (causes request starvation under load), and `RemoteAttributePolicy.status.matchedCollectorIDs` is unbounded (can hit ~1 MB per CR at this fleet size).
3. **The on-call surface is blind.** No custom Fleet API metrics, no dashboard JSON, no Prometheus alert rules, no runbook. The events and structured logging are good, but everything that turns metrics into "did we just page on-call appropriately?" is absent.

There are **no S1 findings**. Most S2 findings are XS or S effort — the heaviest items are observability scaffolding (M) and the scale-fan-out fix (M).

### Top-5 findings ("if you only fix five things")

| # | ID | Severity | Title |
|---|---|---|---|
| 1 | HELM-01 | S2 | Memory limit 128Mi will OOMKill at 30k collectors (XS fix) |
| 2 | REC-01 | S2 | Fleet API rate limiter `burst=1` starves requests under sustained load |
| 3 | PERF-01 | S2 | `RemoteAttributePolicy.status.matchedCollectorIDs` is unbounded — etcd/client bloat |
| 4 | OBS-01 | S2 | No custom Fleet API metrics — on-call cannot triage latency or error spikes |
| 5 | DOC-04 | S2 | No on-call runbook for the alerts that will fire at this scale |

---

## 1b. Remediation Log

Fixes applied after the initial audit (all commits prior to `2e8aaf5` unless otherwise noted).

| Category | ID | Status | Description |
|---|---|---|---|
| API | API-01 | FIXED | Added TenantPolicyStatus with observedGeneration, Conditions, BoundSubjectCount; added +kubebuilder:subresource:status |
| API | API-02 | FIXED | Added CEL immutability rule on Collector.Spec.ID (`self == oldSelf`) |
| API | API-03 | FIXED | Created docs/conditions.md — full cross-CRD condition type/reason registry |
| API | API-04 | FIXED | Added CEL rules for reserved-prefix keys (collector.*); items:MaxLength=200 on matcher slices; MaxItems on collector remoteAttributes |
| API | API-05 | FIXED | Created docs/versioning.md — v1 graduation criteria (6 gates), hub-and-spoke conversion strategy, deprecation policy |
| API | API-06 | FIXED | Added godoc comments explaining the MaxItems=100 matcher cap on every selector-bearing CRD |
| API | API-07 | FIXED | Created config/samples/invalid/ with README.md and 6 annotated counter-examples covering common admission failures |
| API | API-08 | FIXED | Added status.matchedCount int32 to RemoteAttributePolicyStatus; updated printer column to reference matchedCount instead of the string array |
| API | API-09 | DEFERRED | Scale subresource deferred to v1 graduation (documented in docs/versioning.md) |
| API | API-10 | INFO | No action needed |
| REC | REC-01 | FIXED | Both rate (rps) and burst configurable via --fleet-api-rps / --fleet-api-burst flags and Helm values; functional-options pattern; zero-value guard; Limiter() accessor (commits: 2e8aaf5, 7124397, 6a7ba07) |
| REC | REC-02..10 | INFO | All Info entries; key patterns documented in CLAUDE.md (commit: 6a7ba07) |
| PERF | PERF-01 | FIXED | Capped status.matchedCollectorIDs and status.ownedKeys at 1000 with MaxItems; Truncated condition + Warning event when cap hit; matchedCount always reflects full count; no-op short-circuit added to ExternalAttributeSync (commits: 62cf4a3, c801c66) |
| PERF | PERF-02 | PARTIAL | Admission Warning on CollectorDiscovery when spec.selector is empty; pagination adoption note in pkg/fleetclient/collector.go; sharding pattern documented (commit: d605701). Root cause blocked on Fleet SDK page_token/page_size |
| PERF | PERF-03 | PARTIAL | Added per-controller MaxConcurrentReconciles (Policy=4, ExternalSync=4) to cap blast radius (commit: d9ba8ef). Root cause (List-all-Collectors on every Collector change) requires Phase-3 label-selector index — deferred |
| PERF | PERF-04 | FIXED | Added MaxConcurrentReconciles to Policy, ExternalSync, Discovery reconcilers; new flags --controller-policy-max-concurrent / --controller-sync-max-concurrent / --controller-discovery-max-concurrent; Helm values added (commit: d9ba8ef) |
| PERF | PERF-05 | FIXED | Documented HA/leader-election behavior in CLAUDE.md: non-leader replicas do not reconcile; discovery polling only runs on current leader (commit: d605701) |
| PERF | PERF-06 | FIXED | Capped status.conflicts at 100 with MaxItems marker; TruncatedConflicts condition set when cap hit; truncation is deterministic after existing sort (commit: 75a3dbe) |
| PERF | PERF-07 | FIXED | Added two-tier rate-limiting comment block in cmd/main.go explaining workqueue tier vs Fleet API tier (commit: d9ba8ef) |
| PERF | PERF-08 | MOVED TO TEST | This S4 finding belongs in the TEST category; tracked as TEST-08 (scale test scaffolding) |
| PERF | PERF-09 | INFO | No action needed |

---

## 2. Per-category RAG Summary

| Category | RAG | Findings | Headline |
|---|---|---|---|
| API — CRD design | GREEN ✓ | 0x S2, 5x S3, 4x S4, 1x Info | S3/S4 findings addressed in API pass; API-09 deferred to v1 graduation |
| REC — Reconciliation | GREEN ✓ | 1x S2, 0x S3, 0x S4, 9x Info | REC-01 (S2) fixed — burst configurable; Info patterns documented in CLAUDE.md |
| PERF — Scaling | AMBER ✓ | 3x S2, 4x S3, 1x S4, 1x Info | S2s mitigated: PERF-01 fully fixed; PERF-02/03 have mitigations but root causes remain (SDK gap, Phase-3 label index) |
| WH — Webhook hardening | AMBER | 1x S2, 2x S3, 2x S4, 2x Info | Helm has no webhook TLS strategy; bootstrap deadlock risk = zero |
| OBS — Observability | AMBER | 1x S2, 7x S3, 0x S4, 1x Info | Custom Fleet API metrics absent; built-in metrics are fine |
| SEC — Security & RBAC | AMBER | 1x S2, 3x S3, 0x S4, 6x Info | Image-tag pinning aside, posture is exemplary |
| HELM — Deploy ergonomics | RED | 3x S2, 6x S3, 4x S4, 2x Info | Defaults are demo-grade; multiple chart fixes needed before prod |
| TEST — Testing depth | AMBER | 1x S2, 6x S3, 1x S4, 2x Info | Race detector off in CI; otherwise broad and well-structured |
| UPG — Upgrade & operability | GREEN | 0x S2, 2x S3, 4x S4, 2x Info | Conversion strategy not yet planned; graceful shutdown works |
| DOC — Documentation | RED | 4x S2, 3x S3, 0x S4, 5x Info | Install / dev docs are great; on-call surface is missing |

**RAG definitions** (from spec):
- GREEN: no S1 or S2 findings; S3s are standard polish.
- AMBER: has S2 findings but no S1; or pattern of S3s concentrated in one subsystem.
- RED: at least one S1; or compounding S2 findings (e.g. on-call would be blind).

HELM and DOC remain RED because their S2 findings compound into a single failure mode each: defaults that won't survive prod (HELM), and on-call blindness (DOC). PERF has been downgraded to AMBER — the three compounding S2s are mitigated (PERF-01 fully fixed; PERF-02/03 have mitigations in place with root causes documented and deferred).

---

## 3. Findings Inventory

### API — CRD / API design (RAG: GREEN ✓)

**Summary.** All six CRDs use v1alpha1 with proper status subresources (except TenantPolicy), comprehensive OpenAPI validation, printer columns, and admission webhooks. Modern API polish is missing: no CEL rules, no `+kubebuilder:validation:Immutable` markers (immutability enforced only in webhooks), no documented condition-type registry, no v1 graduation roadmap. None of these are blockers for the current pre-prod stage; they are the polish backlog before v1.

**Post-remediation:** All S3/S4 findings resolved in the API pass. TenantPolicy now has a status subresource and conditions (API-01). Collector.Spec.ID immutability enforced in CRD schema via CEL (API-02). Cross-CRD condition registry documented (API-03). CEL validation rules added for key constraints (API-04). v1 graduation criteria and conversion strategy documented (API-05). MaxItems cap godoc added (API-06). Invalid-example samples added (API-07). Printer column schema corrected with matchedCount field (API-08). Scale subresource deferred to v1 graduation per API-09.

```
ID:             API-01
Title:          TenantPolicy missing status subresource
Severity:       S3
Effort:         S
Status:         FIXED — Added TenantPolicyStatus with observedGeneration, Conditions, BoundSubjectCount; +kubebuilder:subresource:status added
Evidence:       api/v1alpha1/tenant_policy_types.go:68-78; config/crd/bases/fleetmanagement.grafana.com_tenantpolicies.yaml (subresources: {})
Observation:    TenantPolicy has no status subresource and no conditions, so users cannot tell whether a policy is syntactically valid, currently bound, or rejected. Other CRDs in this group all carry status with conditions.
Recommendation: Add TenantPolicyStatus with observedGeneration and a Conditions slice (Ready, Valid). Mark with +kubebuilder:subresource:status.
Risk:           At hundreds of policies in a single tenant, debugging policy enforcement failures becomes opaque — there is no in-cluster signal of policy health.
```

```
ID:             API-02
Title:          Immutability enforced only in webhook, not in CRD schema
Severity:       S3
Effort:         XS
Status:         FIXED — Added CEL immutability rule on Collector.Spec.ID (`self == oldSelf`) in CRD schema
Evidence:       api/v1alpha1/collector_types.go:55-56 (no marker); api/v1alpha1/collector_webhook.go:67-70 (webhook check)
Observation:    Collector.Spec.ID is documented and webhook-enforced as immutable, but lacks the +kubebuilder:validation:Immutable marker (or a CEL rule). Tooling that consumes the OpenAPI schema cannot discover the constraint without reading webhook code.
Recommendation: Add +kubebuilder:validation:Immutable to Collector.Spec.ID and any other field that the webhooks treat as immutable. Optionally add a CEL rule for stronger validation in dry-run.
Risk:           Tools (doc generators, client SDKs, policy engines) silently miss the constraint; users discover the rule only at admission time.
```

```
ID:             API-03
Title:          Condition type vocabulary inconsistent across CRDs
Severity:       S3
Effort:         S
Status:         FIXED — Created docs/conditions.md with full cross-CRD condition type/reason registry
Evidence:       api/v1alpha1/pipeline_types.go:199-202 (Ready, Synced); api/v1alpha1/external_sync_types.go:175 (Ready, Synced, Stalled); api/v1alpha1/policy_types.go:80-85 (none documented); api/v1alpha1/collector_discovery_types.go:193 (Ready, Synced)
Observation:    Different CRDs document different condition types with no shared registry. Reason codes are not standardised. Generic alerting and dashboard queries cannot key off conditions consistently.
Recommendation: Add a docs/conditions.md (or a CLAUDE.md section) listing every (Type, Reason) pair used across CRDs and the controller code. Backfill missing godoc on RemoteAttributePolicy and CollectorDiscovery.
Risk:           Operators cannot write a single "X is not Ready for >10m" PromQL/alerting expression for the family; per-CRD logic duplicates.
```

```
ID:             API-04
Title:          No CEL validation rules for runtime constraints
Severity:       S3
Effort:         M
Status:         FIXED — Added CEL rules for reserved-prefix keys (collector.*); items:MaxLength=200 on matcher slices; MaxItems on collector remoteAttributes
Evidence:       config/crd/bases/*.yaml (no x-kubernetes-validations); CLAUDE.md documents 200-char matcher cap and matcher syntax, enforced only in Go webhooks
Observation:    Validation that could be enforced server-side via CEL (matcher syntax, 200-char cap, configType/contents consistency) is implemented only in admission webhooks. CEL would enable client-side dry-run, kubectl --dry-run=client, and reduce reliance on webhook availability.
Recommendation: Add CEL rules for the rules that webhook tests already cover: matcher length cap (`size(m) <= 200`), basic matcher syntax shape, configType/contents pairing for Pipeline.
Risk:           Webhook outage = no validation = malformed CRs reach reconcilers. Also blocks dry-run validation in CI tooling.
```

```
ID:             API-05
Title:          No v1 graduation criteria or v2 migration plan
Severity:       S3
Effort:         S
Status:         FIXED — Created docs/versioning.md with v1 graduation criteria (6 gates), hub-and-spoke conversion strategy, and deprecation policy
Evidence:       api/v1alpha1/groupversion_info.go (only v1alpha1); CLAUDE.md documents TenantPolicy v1 gaps without any roadmap
Observation:    Multiple known gaps (TenantPolicy collectorIDs bypass, no negation/regex matcher reasoning) need addressing before v1. There is no documented graduation criteria, breaking-change policy, or deprecation window. Cross-references with UPG-01.
Recommendation: Document v1 graduation criteria. Establish a deprecation policy (≥ 6 months notice for v1alpha1 removal). Decide which gaps are v1-blockers vs v2.
Risk:           Adopters may delay or refuse migration if version stability is unclear; once they're entrenched on v1alpha1, breaking changes become politically expensive.
```

```
ID:             API-06
Title:          Inconsistent matchers cap documentation
Severity:       S4
Effort:         XS
Status:         FIXED — Added godoc comments explaining the MaxItems=100 matcher cap on every selector-bearing CRD
Evidence:       api/v1alpha1/policy_types.go:35 (MaxItems=100); external_sync_types.go:128; collector_discovery_types.go:129
Observation:    100-matcher cap is enforced in schema across all selector-bearing CRDs but never documented in the field godoc. Users hit it as a generic validation error.
Recommendation: Add a godoc comment near each MaxItems=100 marker explaining the cap and rationale.
Risk:           Minor UX friction; misleading error messages.
```

```
ID:             API-07
Title:          No invalid-example samples for documentation
Severity:       S4
Effort:         S
Status:         FIXED — Created config/samples/invalid/ with README.md and 6 annotated counter-examples covering common admission failures
Evidence:       config/samples/*.yaml — all samples valid
Observation:    Users have no documented examples of common admission failures (configType mismatch, reserved key prefix, oversized matcher).
Recommendation: Add config/samples/invalid/ with annotated counter-examples and the expected webhook error.
Risk:           Onboarding friction; duplicated support questions.
```

```
ID:             API-08
Title:          RemoteAttributePolicy "Matched" printer column references whole array
Severity:       S4
Effort:         XS
Status:         FIXED — Added status.matchedCount int32 to RemoteAttributePolicyStatus; updated printer column to reference matchedCount instead of the string array
Evidence:       api/v1alpha1/policy_types.go:91 (printcolumn type=integer JSONPath=.status.matchedCollectorIDs)
Observation:    Printer column declares type=integer but JSONPath points at a string array. kubectl coerces (it shows the count) but the schema is misleading and will trip strict OpenAPI clients.
Recommendation: Add a status.matchedCount int32 and point the printer column at it. Cross-reference PERF-01 (this would also help bound the unbounded array).
Risk:           Strict schema clients error; otherwise minimal.
```

```
ID:             API-09
Title:          No scale subresource on manageable CRDs
Severity:       S4
Effort:         S
Status:         DEFERRED — Scale subresource deferred to v1 graduation; documented in docs/versioning.md
Question:       Is scale subresource on the roadmap for any of these CRDs?
Observation:    Pipeline, Collector, CollectorDiscovery don't expose +kubebuilder:subresource:scale. Not a blocker — these aren't workload CRDs — but Kubernetes idiom is to support kubectl scale where it makes sense.
Recommendation: Defer to v1 graduation. Decide per-CRD whether the idiom helps any user.
Risk:           Negligible.
```

```
ID:             API-10
Title:          Collector.status.attributeOwners list semantics correctly declared
Severity:       Info
Evidence:       api/v1alpha1/collector_types.go:116-119 (+listType=map, +listMapKey=key)
Observation:    Map-list semantics are correctly declared, enabling stable patches and atomic updates. Logged as Info to prevent re-flagging.
```

---

### REC — Reconciliation correctness (RAG: GREEN ✓)

**Summary.** Reconciliation discipline across all five controllers is solid: idempotency, finalizer ordering, 404-on-delete, ObservedGeneration usage (and the deliberate non-use on Collector with documented rationale and idempotency via `mapsEqual`/`ownerSlicesEqual`), Status().Update() exclusively for status writes, error wrapping with `%w`, conflict-on-update returning `Requeue: true` without backoff penalty, and clean context propagation through the rate-limiter and HTTP client. Most findings here are Info entries capturing correct patterns so they aren't re-flagged. One real defect: the rate-limiter is configured with `burst=1`, which combined with the 30s HTTP timeout creates a starvation risk under sustained load.

**Post-remediation:** REC-01 (the S2) is fully fixed. Both rate and burst are now configurable via `--fleet-api-rps` / `--fleet-api-burst` flags and Helm values `fleetManagement.apiRatePerSecond` / `fleetManagement.apiRateBurst`, defaulting to 3 rps / burst 50. Functional-options pattern used; zero-value guard prevents misconfiguration. All Info patterns (REC-02..REC-10) documented in CLAUDE.md.

```
ID:             REC-01
Title:          Fleet API rate-limiter burst=1 starves requests under sustained load
Severity:       S2
Effort:         S
Status:         FIXED — Both rps and burst configurable via --fleet-api-rps (default 3) and --fleet-api-burst (default 50); functional-options pattern fleetclient.WithRateLimit(rps, burst); zero-value guard; Limiter() accessor added (commits: 2e8aaf5, 7124397, 6a7ba07)
Evidence:       pkg/fleetclient/client.go:62 — rate.NewLimiter(rate.Limit(3), 1)
Observation:    The limiter refills at 3 tokens/s with bucket capacity = 1. A queue of N requests waits N/3 seconds in serial; with HTTP timeout 30s, queued requests time out at depth 91. Under burst arrivals (controller restart, post-discovery sync), this manifests as "Fleet API timeouts" when the API itself is healthy.
Recommendation: Increase burst to ~50-100. The sustained 3 req/s ceiling stays enforced; burst absorbs queue spikes. E.g. rate.NewLimiter(rate.Limit(3), 100). Add a metric on limiter wait time (see OBS-03).
Risk:           At thousands of pipelines, a single controller restart triggers reconcile of all of them. With burst=1 and 30s timeout, ~91 will succeed and the rest will time out, requeue, and starve again — a livelock at the rate-limiter, indistinguishable from API outage.
```

```
ID:             REC-02
Title:          ObservedGeneration short-circuit deliberately absent on Collector reconciler — verified
Severity:       Info
Evidence:       internal/controller/collector_controller.go:143-149 (rationale comment); :461-472 (mapsEqual/ownerSlicesEqual idempotency in updateStatusSuccess)
Observation:    Cross-layer watches (RemoteAttributePolicy / ExternalAttributeSync) trigger reconciles where the Collector spec generation is unchanged but the desired attribute set has moved. Idempotency is via merged-state comparison rather than generation. Pattern is correct; logged to prevent re-flagging.
```

```
ID:             REC-03
Title:          Conflict on Status().Update is correctly handled without backoff
Severity:       Info
Evidence:       pipeline_controller.go:439-442; collector_controller.go:503-507; policy_controller.go:242-246; external_sync_controller.go:239-243; collector_discovery_controller.go (status update conflict)
Observation:    All five controllers map IsConflict to ctrl.Result{Requeue: true}, nil. Correct: a conflict is cache-lag, not transient API error, so exponential backoff would be wrong.
```

```
ID:             REC-04
Title:          Rate-limiter and HTTP client respect context cancellation
Severity:       Info
Evidence:       pkg/fleetclient/interceptors.go:35 (limiter.Wait(ctx)); internal/controller/errors.go:41-42 (context.Canceled non-transient), :98-99 (context.DeadlineExceeded transient)
Observation:    Shutdown paths cancel cleanly. Error classification distinguishes Canceled from DeadlineExceeded correctly.
```

```
ID:             REC-05
Title:          404-on-delete handled correctly across all controllers
Severity:       Info
Evidence:       pipeline_controller.go:227-243; collector_controller.go:362-375; pkg/fleetclient/client.go:114-126
Observation:    DeletePipeline (and equivalents) treat HTTP 404 as success; finalizer-removal proceeds. CR is garbage-collected; no stuck-finalizer pattern observed.
```

```
ID:             REC-06
Title:          K8s API call count per reconcile is minimal and audited
Severity:       Info
Evidence:       pipeline_controller.go:17-54 (audit comment block); reconcile_audit_test.go (RECON-02, -04, -05)
Observation:    Pipeline controller does at most 5 K8s calls per reconcile (1 Get, 2 Update for finalizer, 2 Status().Update). UpsertPipeline returns the full object so no extra GetPipeline. Pattern replicated across controllers and machine-checked in audit tests.
```

```
ID:             REC-07
Title:          FleetAPIError chain preserves wrapped errors via Unwrap
Severity:       Info
Evidence:       pkg/fleetclient/errors.go:35-51; pkg/fleetclient/types.go:67-69; internal/controller/errors.go:76-94
Observation:    connectErrToFleetErr wraps connect-go errors in a typed FleetAPIError with Unwrap, so errors.As works through the chain. Matches Go best practice.
```

```
ID:             REC-08
Title:          HTTP client configuration appropriate for production
Severity:       Info
Evidence:       pkg/fleetclient/client.go:51-58 — 30s timeout, MaxIdleConns=100, IdleConnTimeout=90s, TLSHandshakeTimeout=10s
Observation:    Defaults are sensible for a 3 req/s client. Connection pool prevents exhaustion. See REC-01 for the orthogonal rate-limiter concern.
```

```
ID:             REC-09
Title:          Finalizer add-then-reconcile, delete-after-cleanup ordering correct
Severity:       Info
Evidence:       pipeline_controller.go:175-187 (add); :215-257 (delete)
Observation:    Finalizer is persisted before any Fleet API call; finalizer is removed only after Fleet-side cleanup confirms (or returns 404). No data-loss window.
```

```
ID:             REC-10
Title:          No watch storms or unprotected status update cycles
Severity:       Info
Evidence:       cmd/main.go:189-206 (no SyncPeriod set); pipeline_controller.go:585-597 (WATCH-04 documentation); watch_audit_test.go (TestResyncPeriodDocumented)
Observation:    SyncPeriod is unset (no periodic resync storms). Status().Update is used everywhere — no spec-change watch loops. Audited in test code.
```

---

### PERF — Performance and scaling at 30k collectors (RAG: AMBER ✓)

**Summary.** v1.1 audit work (cache, reconcile loop, watch patterns) is real and holds up under spot-check — that floor is good. What's missing is everything that becomes load-bearing only at fleet scale. Three S2 findings compound into the same failure mode (scale-induced incidents that the operator currently has no defence against): unbounded status fields will bloat etcd, ListCollectors has no pagination, and policy/sync reconcilers fan out List calls per Collector change. Memory sizing is captured under HELM-01 (per ownership rules) but the math originates here.

**Post-remediation:** PERF-01 (unbounded status) fully fixed — MaxItems=1000 caps added with Truncated condition and Warning event; matchedCount always reflects the full count; no-op short-circuit added for unchanged source data. PERF-04 fully fixed — MaxConcurrentReconciles configurable per controller. PERF-05, PERF-06, PERF-07 fully fixed. PERF-02 (pagination) and PERF-03 (fan-out) have mitigations in place but root causes remain blocked: PERF-02 on Fleet SDK adding page_token/page_size; PERF-03 on a Phase-3 label-selector index. PERF-08 moved to TEST category (tracked as TEST-08).

```
ID:             PERF-01
Title:          status.matchedCollectorIDs and status.ownedKeys grow unbounded
Severity:       S2
Effort:         S
Status:         FIXED — Capped status.matchedCollectorIDs at 1000 with MaxItems marker; capped status.ownedKeys at 1000 with MaxItems marker; Truncated condition set when cap hit; Warning event emitted; matchedCount always reflects full count; no-op short-circuit added to ExternalAttributeSync to skip status writes when source data unchanged (commits: 62cf4a3, c801c66)
Evidence:       api/v1alpha1/policy_types.go:58-62 (MatchedCollectorIDs []string, no MaxItems); internal/controller/policy_controller.go:128-147 (full Collector list, all matches appended); api/v1alpha1/external_sync_types.go (OwnedKeys list, similar)
Observation:    A single RemoteAttributePolicy with broad matchers against 30k collectors stores ~27k strings × ~32 bytes ≈ 860KB in a single status field. Multiple policies multiply this. ExternalAttributeSync's OwnedKeys is similar per source. etcd, the API server, and any client watching status pay the size on every update.
Recommendation: Cap with +kubebuilder:validation:MaxItems (suggest 1000 initially; calibrate). When the cap trips, set a Truncated condition and emit a Warning event. Alternatively replace the slice with a count field and move the full list into a separate status-only resource if the user-visible IDs are needed.
Risk:           At 5-10 broad-matcher policies in a 30k fleet, status objects approach multi-MB. etcd write latency rises; status watchers (kubectl, controllers) pay 10-100ms deserialization per read; control-plane bandwidth bloats.
```

```
ID:             PERF-02
Title:          ListCollectors lacks pagination — full-fleet lists block at scale
Severity:       S2
Effort:         M
Status:         PARTIAL — Added admission Warning on CollectorDiscovery when spec.selector is empty (match-all risk); added pagination adoption note in pkg/fleetclient/collector.go; sharding pattern documented in CLAUDE.md and values.yaml (commit: d605701). Root cause blocked on Fleet SDK adding page_token/page_size — full fix cannot land until SDK ships it
Evidence:       CLAUDE.md:298 (documented SDK gap); pkg/fleetclient/collector.go:78-92 (list returns full array); internal/controller/collector_discovery_controller.go:183 (single ListCollectors call)
Observation:    The Fleet SDK's ListCollectorsRequest does not currently expose page_token / page_size. A broad CollectorDiscovery selector in a 30k fleet returns all 30k collectors in one response (~30MB depending on payload). One slow API response or one network blip = whole discovery cycle fails or times out. CLAUDE.md acknowledges the workaround (shard via disjoint matchers) but it is manual.
Recommendation: Track upstream SDK pagination support. Until then, document the recommended sharding pattern in the Helm README and add a webhook warning when an empty selector is used on a CollectorDiscovery whose targetNamespace implies a large fleet. Plan to adopt page_token without a CRD change as soon as the SDK ships it.
Risk:           At 30k, a single discovery poll deserialises ~30MB; if Fleet's server-side timeout fires (~30s) the discovery never completes and the controller never makes progress. Multiple discoveries compound the API budget hit.
```

```
ID:             PERF-03
Title:          Policy/Sync reconciler fan-out lists all Collectors per change
Severity:       S2
Effort:         M
Status:         PARTIAL — Added per-controller MaxConcurrentReconciles (Policy=4, ExternalSync=4) to cap concurrent fan-out blast radius (commit: d9ba8ef). Root cause (List-all-Collectors on every Collector change) requires Phase-3 label-selector index — deferred
Evidence:       internal/controller/policy_controller.go:129 (List Collectors per policy reconcile); internal/controller/external_sync_controller.go:463 (List ExternalAttributeSync per Collector change); internal/controller/collector_controller.go:223 (List both per Collector reconcile)
Observation:    A single Collector change wakes up multiple policy/sync reconciles, each of which lists the entire Collector set in the namespace to recompute matches. With 100 policies × 30k Collectors, a batch enrolment of even 50 collectors (rolling deploy) triggers 50 × 100 = 5000 list operations against the K8s API. The Fleet API rate-limiter does not protect against this — it's pure K8s load.
Recommendation: Phase 3 work: introduce a label-selector index keyed by matcher dimensions so policies can precompute which Collectors might match without a full List per reconcile. Short-term: cap MaxConcurrentReconciles per controller (see PERF-04) and document the "split policies across namespaces" mitigation.
Risk:           At 30k Collectors with a rolling-update wave, the K8s API server sees thousands of List(Collector) calls in a minute; informer cache pressure and apiserver CPU rise. The Fleet rate-limit budget is also starved by the dependent Pipeline reconciles waiting in the same workqueue.
```

```
ID:             PERF-04
Title:          MaxConcurrentReconciles not configurable per controller
Severity:       S3
Effort:         S
Status:         FIXED — Added MaxConcurrentReconciles field to Policy, ExternalSync, Discovery reconcilers; wired via controller.Options in SetupWithManager; new flags --controller-policy-max-concurrent (4), --controller-sync-max-concurrent (4), --controller-discovery-max-concurrent (1); Helm values added for controllers.{remoteAttributePolicy,externalAttributeSync}.maxConcurrent (commit: d9ba8ef)
Evidence:       cmd/main.go:207-225 (no explicit Options.MaxConcurrentReconciles); per-controller SetupWithManager calls use defaults
Observation:    All controllers run with the controller-runtime default (MaxConcurrentReconciles=1). Pipeline and Collector are correct at 1 because Fleet API serialisation is the bottleneck; Policy and ExternalSync are parallel-safe (their work is per-CR independent computation against the K8s informer cache). Currently a single slow Policy reconcile blocks all other policies' updates.
Recommendation: Add Helm values for per-controller concurrency, e.g. controllers.policy.maxConcurrent: 4. Document which controllers are safe to parallelise (Policy, Sync, Discovery) and which must stay at 1 (Pipeline, Collector — they share the Fleet API budget).
Risk:           Batch enrolment (e.g. 1000 new Collectors) takes 1000 × ~10s ≈ 2.7h to drain through Policy reconcile at concurrency=1.
```

```
ID:             PERF-05
Title:          CollectorDiscovery poll ignores leader-election in HA setups
Severity:       S3
Effort:         XS
Status:         FIXED — Documented HA/leader-election behavior in CLAUDE.md: when --leader-elect is set, non-leader replicas do NOT reconcile; discovery polling only runs on current leader (commit: d605701)
Evidence:       internal/controller/collector_discovery_controller.go:171-176 (RequeueAfter on every replica); cmd/main.go (leader election enabled)
Observation:    Today this is single replica so impact is nil. When HA is added (replicas > 1), standby replicas will independently RequeueAfter and call ListCollectors, doubling/tripling Fleet API list load. Controller-runtime's leader election only gates the manager's reconcile dispatch, not the RequeueAfter queue.
Recommendation: Document this in the chart README and in CLAUDE.md as a known gotcha for HA. Long-term, gate the discovery reconcile on leader-election lease ownership.
Risk:           Wasted Fleet API budget at HA scale-out.
```

```
ID:             PERF-06
Title:          CollectorDiscovery.status.conflicts is unbounded
Severity:       S3
Effort:         S
Status:         FIXED — Capped status.conflicts at 100 with MaxItems marker; TruncatedConflicts condition set when cap hit; truncation is deterministic (after the existing sort) (commit: 75a3dbe)
Evidence:       api/v1alpha1/collector_discovery_types.go:189-190 (Conflicts []DiscoveryConflict no MaxItems); internal/controller/collector_discovery_controller.go:359-393 (append without truncation)
Observation:    Same shape as PERF-01 but on a different CR. Repeated sanitisation collisions or ownership clashes append indefinitely.
Recommendation: Cap at 100 most-recent conflicts; emit a TruncatedConflicts condition.
Risk:           A discovery with broad selectors and noisy IDs can produce thousands of conflict entries, multi-MB status.
```

```
ID:             PERF-07
Title:          Two-tier rate limiting (workqueue + Fleet client) not documented
Severity:       S3
Effort:         S
Status:         FIXED — Added two-tier rate-limiting comment block in cmd/main.go explaining workqueue tier (10 qps, K8s flood prevention) vs Fleet API tier (--fleet-api-rps, budget enforcement) (commit: d9ba8ef)
Evidence:       internal/controller/pipeline_controller.go:55-58 (workqueue default 10qps); pkg/fleetclient/interceptors.go:32-40 (Fleet 3 req/s)
Observation:    Workqueue admits up to 10 reconciles/s; Fleet client rate-limits to 3 req/s. The 7 in-flight reconciles per second wait at limiter.Wait(ctx) holding heap and goroutines. This is correct (workqueue prevents memory bloat; Fleet limiter is the true ceiling) but undocumented and easy to misread when tuning.
Recommendation: Add a comment block in cmd/main.go explaining the two-tier model and what each limiter protects.
Risk:           Operator-team confusion when tuning; no real correctness risk.
```

```
ID:             PERF-08
Title:          ObservedGeneration short-circuit not exercised at 30k scale in tests
Severity:       S4
Effort:         M
Status:         MOVED TO TEST — This S4 finding belongs in the TEST category. Now tracked as TEST-08 (scale test scaffolding). See the TEST section for current status.
Evidence:       v1.1 audit tests verify the pattern at unit scale; absent: a fleet-shaped scale test
Observation:    The v1.1 work (cache_audit_test.go, reconcile_audit_test.go) is correct in shape but operates on small fixtures. There is no scale test confirming that 95%+ of reconciles short-circuit at 30k.
Recommendation: TEST item; cross-reference TEST-08 for the test scaffolding.
Risk:           A latent ObservedGeneration regression would not be caught until production sees the symptom (reconcile rate elevated).
```

```
ID:             PERF-09
Title:          v1.1 milestone audit work verified by spot-check
Severity:       Info
Evidence:       internal/controller/cache_audit_test.go, watch_audit_test.go, reconcile_audit_test.go
Observation:    The v1.1 audit harness machine-checks the cache/reconcile/watch invariants. Spot-check found no drift since 3fcc31b. Logged to prevent re-flagging.
Question:       Is there an issue tracking the gap between unit-scale audit tests and 30k-scale validation?
```

---

### WH — Webhook hardening (RAG: AMBER)

**Summary.** Validating-only design (no mutating, no defaulting) with `failurePolicy: Fail` and `sideEffects: None` is correct and means bootstrap deadlock risk is structurally zero. The gap is in deployment ergonomics — Helm has no values for cert strategy, no namespaceSelector to skip system namespaces, no explicit timeoutSeconds (default 10s is generous for a 3 req/s API; 5s would be safer).

```
ID:             WH-01
Title:          Helm chart has no values for webhook TLS strategy
Severity:       S2
Effort:         S
Evidence:       charts/fleet-management-operator/values.yaml — no webhook.certManager / webhook.manual / webhook.selfSigned toggle; deployment.yaml does not pass --webhook-cert-* flags or mount cert volumes
Observation:    The controller falls back to controller-runtime's self-signed cert generator if no path is set. That regenerates certs on every pod restart. There is no Helm path to wire cert-manager (the kustomize manifest webhookcainjection_patch.yaml exists but is dormant) or to provide a manual Secret-mounted cert.
Recommendation: Add webhook section with .enabled, .certManager.enabled, .certManager.issuerRef, and a manual mode. Wire --webhook-cert-path / --webhook-cert-name / --webhook-cert-key into deployment.yaml conditionally. Document the trade-offs in values.yaml.
Risk:           Pre-prod self-signed regeneration is fine, but production at HA (replicas > 1, future) will see cert mismatch between replicas. Cross-references DOC-05 (user-facing webhook docs) and HELM-01 family.
```

```
ID:             WH-02
Title:          No timeoutSeconds, namespaceSelector, or objectSelector on webhooks
Severity:       S3
Effort:         XS
Evidence:       config/webhook/manifests.yaml (lines 7-126) — defaults used everywhere
Observation:    All webhooks rely on the implicit 10s timeoutSeconds and have no namespace selector to skip kube-system or the operator's own namespace. Validating-only means no functional risk today, but this is a required pattern before any defaulting / mutating webhook lands.
Recommendation: Add timeoutSeconds: 5 (Fleet API enforces 3 req/s; even with one limiter wait, validation work is local). Add namespaceSelector to skip kube-system, kube-node-lease, and the operator namespace.
Risk:           Low today. Pattern hygiene.
```

```
ID:             WH-03
Title:          cert-manager integration scaffolded but not wired in chart
Severity:       S3
Effort:         S
Evidence:       config/default/kustomization.yaml:29-30 (commented patch); charts/fleet-management-operator/templates/deployment.yaml (no cert volume mounts); values.yaml (no certManager.enabled)
Observation:    The kubebuilder scaffolding for cert-manager exists. The Helm chart does not expose it. Users wanting cert-manager-managed certs must fork the chart.
Recommendation: Subset of WH-01 — add controllers.certManager.enabled: false default plus the conditional template patch (cert-manager.io/inject-ca-from annotation on the ValidatingWebhookConfiguration).
Risk:           See WH-01.
```

```
ID:             WH-04
Title:          Webhook port not exposed via Helm; bind not validated at startup
Severity:       S4
Effort:         XS
Evidence:       cmd/main.go:137-152 (defaults to 9443, not logged); config/webhook/service.yaml (targetPort 9443 hardcoded)
Observation:    Webhook bind is implicit. No startup-time validation that the cert files exist (when manual mode is used). Bind address not logged. Debugging requires reading code.
Recommendation: Log the webhook bind address at startup. Validate cert files are readable when --webhook-cert-path is set, fail fast otherwise. Make the port a Helm value.
Risk:           A typo in a manual cert path causes a cryptic late failure.
```

```
ID:             WH-05
Title:          Webhook tests don't exercise admission context (TenantPolicy plumbing)
Severity:       S4
Effort:         M
Evidence:       api/v1alpha1/pipeline_webhook_test.go (spec validation only); api/v1alpha1/webhook_tenant_test.go (Checker unit tests, decoupled from validator struct)
Observation:    The pipelineValidator's MatcherChecker field is not exercised by webhook unit tests; tenant.Checker is tested in isolation. A regression in the wiring (validator forgetting to call checker, or passing wrong namespace) would not be caught.
Recommendation: Add table-driven tests in *_webhook_test.go that inject a fake MatcherChecker and assert it was called with the right namespace and matcher set, and that nil is safe (enforcement disabled path).
Risk:           A wiring regression would manifest as silent tenant-policy bypass.
```

```
ID:             WH-06
Title:          Validating-only webhook strategy with no defaulting webhook is intentional — verified
Severity:       Info
Evidence:       config/webhook/manifests.yaml; api/v1alpha1/*_webhook.go (no DefaultX methods)
Observation:    Defaulting is performed in Go validation logic and reconcilers rather than via mutating webhook. This is conservative and correct for this CRD family.
Question:       Should this be documented in CLAUDE.md as a deliberate design choice?
```

```
ID:             WH-07
Title:          Bootstrap deadlock risk is structurally zero
Severity:       Info
Evidence:       cmd/main.go:280-304 (webhooks registered after manager creation); failurePolicy: Fail with sideEffects: None
Observation:    The operator's own bootstrap doesn't depend on its own webhooks. Validating-only with Fail-policy gives strong ingest guarantees with no deadlock surface.
```

---

### OBS — Observability (RAG: AMBER)

**Summary.** Event coverage is solid (Created/Updated/Synced/Deleted/SyncFailed/ValidationFailed/RateLimited/Recreated land in the right places). Logging is structured with sensible keys and no credential leakage. The gap is the metrics layer: controller-runtime built-ins are exposed in code but the Helm deployment never passes `--metrics-bind-address` so the endpoint is silently unbound (this finding lives in HELM-03 per ownership). Beyond that, no custom Fleet API metrics, no rate-limiter wait-time histogram, no sync-drift gauge. At this scale, those gaps blind the on-call.

```
ID:             OBS-01
Title:          No custom metrics for Fleet Management API calls
Severity:       S2
Effort:         M
Evidence:       absent: pkg/fleetclient/ has no prometheus registration; internal/controller/ has no domain metrics
Observation:    UpsertPipeline, DeletePipeline, BulkUpdateCollectors, ListCollectors are uninstrumented. There is no way to distinguish "Fleet API slow" from "rate-limiter saturated" from "controller looping" with the current metric set.
Recommendation: Register on controller-runtime's metrics registry: fleet_api_request_duration_seconds (histogram, labels: operation, status_code), fleet_api_requests_total (counter), fleet_api_errors_total (counter, labels: operation, error_type). Wrap each Fleet client method.
Risk:           Latency or error spikes are invisible until pipelines accumulate sync drift. On-call has no triage signal. Cross-references DOC-01 (dashboards) and DOC-02 (alerts).
```

```
ID:             OBS-02
Title:          Rate-limiter wait time not instrumented
Severity:       S3
Effort:         S
Evidence:       pkg/fleetclient/interceptors.go:32-41 — limiter.Wait(ctx) with no timing
Observation:    The 3 req/s limiter is the dominant operator-side latency contributor at scale, but its impact is invisible.
Recommendation: Wrap limiter.Wait with a prometheus histogram fleet_api_rate_limiter_wait_duration_seconds. Optionally also count cancellations (ctx-deadline-during-wait).
Risk:           Surge-induced queueing looks like API slowness; misdiagnosed.
```

```
ID:             OBS-03
Title:          No sync-drift gauge per CR
Severity:       S3
Effort:         S
Evidence:       absent: no fleet_resource_last_sync_age_seconds metric
Observation:    Each CR's status records a last-sync timestamp but there's no PromQL-friendly gauge to alert on staleness ("Pipeline X has not synced in 30m").
Recommendation: In each updateStatusSuccess, set a gauge (namespace, name, kind) = unix(now) - unix(lastSync). Use rate() and absent_over_time() in alerts.
Risk:           A CR stuck in retry-backoff is invisible until drift accumulates and a user notices in Fleet.
```

```
ID:             OBS-04
Title:          ExternalAttributeSync owned-key cardinality not exposed
Severity:       S3
Effort:         S
Evidence:       absent: no fleet_external_sync_owned_keys gauge
Observation:    OwnedKeys size is a useful capacity-planning and regression-detection signal (sudden drop = source down with allowEmptyResults=false; sudden rise = scope misconfiguration).
Recommendation: gauge fleet_external_sync_owned_keys{namespace, name} = len(status.ownedKeys), set on reconcile success.
Risk:           Stalled syncs accumulate without visibility; capacity plans rely on guesswork.
```

```
ID:             OBS-05
Title:          CollectorDiscovery ListCollectors result size not tracked
Severity:       S3
Effort:         S
Evidence:       absent: no fleet_discovery_list_collectors_result_size gauge
Observation:    Whether a discovery is matching 800 vs 8000 vs 30000 collectors materially changes its cost. No metric records this.
Recommendation: gauge fleet_discovery_list_collectors_result_size{namespace, name} after each successful list.
Risk:           Scope drift on discoveries goes undetected until reconcile latency degrades. Cross-references PERF-02.
```

```
ID:             OBS-06
Title:          Per-CRD reconcile-outcome counters missing
Severity:       S3
Effort:         M
Evidence:       absent: no fleet_resource_synced_total counter; only generic controller_runtime_reconcile_total exists
Observation:    Conditions and events carry the rich outcome (Synced / SyncFailed / ValidationError / RateLimited / Recreated), but no metric tallies these. Dashboards must parse events (lossy) or settle for the controller-runtime generic.
Recommendation: counter fleet_resource_synced_total{namespace, name, kind, reason} incremented in updateStatusSuccess / updateStatusError.
Risk:           A category of failures (e.g. ValidationError after a chart upgrade) is invisible to monitoring.
```

```
ID:             OBS-07
Title:          No OpenTelemetry tracing
Severity:       S3
Effort:         M
Evidence:       absent: cmd/main.go has no OTEL setup; pkg/fleetclient has no spans
Observation:    Distributed-trace correlation between K8s admission, controller reconcile, and Fleet API call is not possible.
Recommendation: Optional OTEL setup gated on OTEL_EXPORTER_OTLP_ENDPOINT env. Wrap Fleet client methods in spans with operation, payload size, retry-count attributes.
Risk:           Multi-layer incident triage is manual log diffing.
```

```
ID:             OBS-08
Title:          Event emission inconsistent across controllers
Severity:       S3
Effort:         XS
Evidence:       internal/controller/policy_controller.go (event helpers exist but are sparse — no Created/Deleted on success); pipeline_controller.go (full coverage)
Observation:    Pipeline controller emits the full Created/Updated/Synced/SyncFailed/RateLimited/Recreated/Deleted set; Policy and ExternalSync are sparser. Inconsistency undermines kubectl describe troubleshooting.
Recommendation: Audit every controller's success and failure paths and standardise event emission against a single matrix.
Risk:           Different on-call experience per CRD type; troubleshooting timelines incomplete.
```

```
ID:             OBS-09
Title:          Rate-limit log lines lack queue context
Severity:       Info
Evidence:       internal/controller/pipeline_controller.go:350; internal/controller/errors.go:88
Observation:    "rate limited by Fleet Management API, requeueing" is logged but lacks queue depth or expected next-slot. Polish; once OBS-02 lands the metric covers most of the diagnostic need.
```

---

### SEC — Security and RBAC (RAG: AMBER)

**Summary.** This is the strongest category. Distroless image with nonroot user (65532), comprehensive PodSecurityContext (readOnlyRootFilesystem, drop ALL capabilities, seccompProfile: RuntimeDefault), least-privilege ClusterRole scoped to the operator's CRDs, secrets handled via env-from with no logging of credentials. The single S2 is image-tag pinning, which is conventional polish at this stage. The TenantPolicy v1 gap (collectorIDs bypasses matcher checks) is documented and out-of-scope per calibration.

```
ID:             SEC-01
Title:          Image tag is mutable (dev-v1.0.0); no digest pin
Severity:       S2
Effort:         XS
Evidence:       charts/fleet-management-operator/values.yaml:7 (tag: "dev-v1.0.0"); deployment.yaml:39
Observation:    Default image reference uses a floating tag. A registry compromise or tag re-push could swap the image under deployed clusters. The Dockerfile base image (gcr.io/distroless/static:nonroot) is also unpinned.
Recommendation: For production Helm releases, ship values pinned to digest (image: ...@sha256:...). Recommend the practice in chart README. Pin the Dockerfile base image to digest as well.
Risk:           Supply-chain vector. At Grafana's internal registry the risk is lower than the public worst case, but the principle is non-negotiable for production.
```

```
ID:             SEC-02
Title:          PodSecurityContext missing fsGroup
Severity:       S3
Effort:         XS
Evidence:       charts/fleet-management-operator/values.yaml:62-73
Observation:    podSecurityContext sets runAsNonRoot and seccompProfile but no fsGroup. Mounted Secrets and emptyDir volumes carry their host group ownership rather than a controlled fsGroup.
Recommendation: Add fsGroup: 65532 to align with the container UID.
Risk:           Edge case; mounted secret may be unreadable in some configurations.
```

```
ID:             SEC-03
Title:          ServiceAccount automountServiceAccountToken not disabled
Severity:       S3
Effort:         XS
Evidence:       charts/fleet-management-operator/templates/serviceaccount.yaml:12; values.yaml:48 (automount: true default)
Observation:    The operator does not need the SA token mounted into the pod (it uses the in-cluster kubeconfig via controller-runtime). Mounting expands the blast radius of a container compromise.
Recommendation: Default automount to false in values.yaml; document the override for users who need it.
Risk:           Defence-in-depth. The RBAC is scoped, so token theft cannot escalate to cluster-admin, but the principle of least mount applies.
```

```
ID:             SEC-04
Title:          Container securityContext lacks explicit runAsUser/runAsGroup
Severity:       S3
Effort:         S
Evidence:       Dockerfile:31 (USER 65532:65532); values.yaml:68-73 (no runAsUser/runAsGroup)
Observation:    The Dockerfile and the distroless base enforce nonroot, but the K8s-level securityContext does not redundantly declare runAsUser/runAsGroup. Pod Security Standards "restricted" mode prefers explicit declarations.
Recommendation: Add runAsUser: 65532 and runAsGroup: 65532 to securityContext in values.yaml.
Risk:           Belt-and-suspenders.
```

```
ID:             SEC-05
Title:          TenantPolicy collectorIDs bypass — documented v1 gap
Severity:       Info
Evidence:       CLAUDE.md (TenantPolicy v1 gaps); api/v1alpha1/policy_types.go (CollectorIDs field); internal/tenant/checker.go (Check signature takes only namespace + matchers)
Observation:    A user with write access to RemoteAttributePolicy or ExternalAttributeSync spec.selector.collectorIDs can target arbitrary Collectors regardless of TenantPolicy required matchers. Mitigated by K8s RBAC on the CR. Acknowledged in CLAUDE.md.
Recommendation: For v2, extend MatcherChecker to evaluate the full selector. Document the gap inline near the field godoc.
Risk:           Logical RBAC gap on the matcher-based isolation model. Not a code-level vulnerability.
```

```
ID:             SEC-06
Title:          Credential handling secure — no token leakage
Severity:       Info
Evidence:       cmd/main.go:267 (logs baseURL and username, not password); pkg/fleetclient/interceptors.go:46-54 (basic-auth header constructed once); pkg/sources/http/source.go (auth in request, not logged)
Observation:    FLEET_MANAGEMENT_PASSWORD is read from env and never logged. Basic-auth header is encoded once and reused. HTTP and SQL source plugins handle auth correctly.
```

```
ID:             SEC-07
Title:          RBAC is least-privilege and scoped
Severity:       Info
Evidence:       config/rbac/role.yaml; charts/fleet-management-operator/templates/clusterrole.yaml
Observation:    All ClusterRole rules scope to fleetmanagement.grafana.com or to leader-election resources. No wildcard verbs or resources. Namespace read access conditional on tenant-policy enforcement flag.
```

```
ID:             SEC-08
Title:          Image hardening exemplary
Severity:       Info
Evidence:       Dockerfile (gcr.io/distroless/static:nonroot, USER 65532:65532, multi-stage build, CGO_ENABLED=0)
Observation:    Distroless static base, nonroot user, no shell or package manager in final image, multi-stage build. Only suggestion: pin base by digest (covered in SEC-01).
```

```
ID:             SEC-09
Title:          readOnlyRootFilesystem and capabilities drop ALL enforced
Severity:       Info
Evidence:       charts/fleet-management-operator/values.yaml:69, :71-73
Observation:    Container security context enforces both. Combined with the nonroot user and distroless base this is a strong defence-in-depth posture.
```

```
ID:             SEC-10
Title:          NetworkPolicy absent — out of scope per calibration
Severity:       Info
Question:       If multi-tenant clusters are ever a deployment target, is a NetworkPolicy template a planned chart deliverable?
Observation:    No NetworkPolicy in the chart. Per calibration (single tenant) this is intentional. Documented as Info to capture the deliberate choice.
```

---

### HELM — Helm and deploy ergonomics (RAG: RED)

**Summary.** Three S2 findings compound: memory limits will OOMKill, log level is hard-set to development, and the metrics endpoint is silently unbound (the deployment never passes `--metrics-bind-address`). Several S3 findings around leader-election lease tuning, container ports declaration, liveness initial delay, and PDB readiness add up to "this chart is sized for a demo." None are individually catastrophic; collectively they make the chart's defaults actively misleading.

```
ID:             HELM-01
Title:          Memory limits 128Mi will OOMKill at 30k collectors
Severity:       S2
Effort:         XS
Evidence:       charts/fleet-management-operator/values.yaml:76-82 (limits.memory: 128Mi, requests.memory: 64Mi); cmd/main.go:287-289 (cached client; informer caches all watched CRs)
Observation:    Informer-cache footprint at 30k Collectors ≈ 30,000 × ~5KB = ~150MB. Add Pipeline and other caches (~50MB) and Go runtime (~50MB). 128Mi is short by an order of magnitude.
Recommendation: Default memory limits to 1Gi, requests to 512Mi. Document a sizing matrix in values.yaml: "<1k CRs: 256Mi; 10k: 512Mi; 30k+: 1Gi." Comment shows the math. Cross-references PERF-04 (concurrency) and PERF-08 (scale-test scaffolding to validate sizing).
Risk:           OOMKill in production = informer cache rebuild on every restart = 5-10 min reconciliation lag during the rebuild. At 30k scale, the rebuild itself fights memory and may not stabilise.
```

```
ID:             HELM-02
Title:          Log level hardcoded to Development; no Helm value
Severity:       S2
Effort:         XS
Evidence:       cmd/main.go:114-120 (zap.Options{Development: true}); deployment.yaml has no --zap-log-level flag
Observation:    Development mode emits stack traces and high verbosity on every reconcile. Single replica × 30k Collectors × routine reconciles will produce millions of log lines per hour.
Recommendation: Add logging.level (default "info") to values.yaml. Wire --zap-log-level={{ .Values.logging.level }} into deployment args. Default the manager's zap to Development=false.
Risk:           Log aggregation costs and disk pressure at production scale. Real errors get lost in stacktrace noise.
```

```
ID:             HELM-03
Title:          --metrics-bind-address never passed in deployment; metrics are silently disabled
Severity:       S2
Effort:         XS
Evidence:       cmd/main.go:81-82 (default "0" disables metrics); charts/fleet-management-operator/templates/deployment.yaml:43-53 (no flag); ServiceMonitor (servicemonitor.yaml:14) targets port "http" expecting metrics
Observation:    The Service and ServiceMonitor templates expect metrics on port 8080. The Deployment never passes the flag, so the manager binds to "0" and serves no metrics. Prometheus scrapes silently fail. This is the cause of OBS-01's premise: the controller-runtime built-in metrics exist in code but never reach Prometheus.
Recommendation: Add `- --metrics-bind-address=:{{ .Values.metrics.port }}` to deployment.yaml args, gated on metrics.enabled.
Risk:           Operator is silently unobserved. Both controller-runtime built-in metrics (reconcile_total, workqueue_depth, etc.) and any future custom metrics from OBS-01 are unreachable until this is fixed.
```

```
ID:             HELM-04
Title:          terminationGracePeriodSeconds: 10s too tight for in-flight Fleet API calls
Severity:       S3
Effort:         XS
Evidence:       charts/fleet-management-operator/templates/deployment.yaml:106 (terminationGracePeriodSeconds: 10)
Observation:    HTTP client timeout is 30s. A SIGTERM that arrives mid-call won't let the call complete; the rate-limiter queue is also dropped. Idempotency catches the resulting half-states on the next reconcile, but the window is wider than necessary.
Recommendation: Increase to 30s or expose as a Helm value (default 30).
Risk:           Pod kills mid-call leave Fleet in inconsistent partial state until next reconcile picks up.
```

```
ID:             HELM-05
Title:          PodDisruptionBudget pitfall not documented (default values)
Severity:       S3
Effort:         XS
Evidence:       charts/fleet-management-operator/values.yaml:148-151 (PDB disabled by default; minAvailable: 1 with replicaCount: 1 would block drains)
Observation:    Currently safe (PDB disabled). The defaults if a user enables it without thinking (minAvailable: 1, replicaCount: 1) would block node drains.
Recommendation: Comment in values.yaml warns about the pitfall: only enable when replicaCount > 1 and use maxUnavailable, not minAvailable.
Risk:           User-error footgun.
```

```
ID:             HELM-06
Title:          Leader-election lease parameters defined in values.yaml but not wired through
Severity:       S3
Effort:         S
Evidence:       cmd/main.go:212-213 (no flag binding for lease params); values.yaml:84-92 (parameters defined)
Observation:    values.yaml has leaseDuration, renewDeadline, retryPeriod fields, but the deployment template never passes them and main.go never registers flags for them. The values are dead weight.
Recommendation: Add flag bindings in main.go (DurationVar) and pass them via deployment args conditionally on leaderElection.enabled. Then the values are actually honoured.
Risk:           Operators tuning values for failover speed will see no effect.
```

```
ID:             HELM-07
Title:          Anti-affinity not templated
Severity:       S3
Effort:         XS
Evidence:       charts/fleet-management-operator/values.yaml:141-142 (affinity: {})
Observation:    No anti-affinity default. Per calibration HA polish is deferred, but the chart should ship a commented-out example so the path to HA is clear.
Recommendation: Add a commented-out podAntiAffinity preferredDuringScheduling block to values.yaml.
Risk:           When HA is turned on, accidental pod co-location undermines the point of replicas > 1.
```

```
ID:             HELM-08
Title:          Liveness probe initial delay 15s may race with informer cache warmup at 30k
Severity:       S3
Effort:         XS
Evidence:       charts/fleet-management-operator/templates/deployment.yaml; values.yaml:99-103
Observation:    At 30k Collectors, initial cache-warm-up may take 20-45s on a busy apiserver. A 15s liveness initial delay can trigger false restarts during normal startup, which then makes things worse (cache rebuilds again).
Recommendation: Increase initialDelaySeconds to 45s. Consider switching the slow path to a startupProbe with longer failureThreshold so liveness can stay tight in steady state.
Risk:           Crash loop during legitimate startup.
```

```
ID:             HELM-09
Title:          Container ports not declared in deployment spec
Severity:       S3
Effort:         S
Evidence:       charts/fleet-management-operator/templates/deployment.yaml — no ports: stanza
Observation:    Health, metrics, and webhook ports are referenced from probes, Service, and code but not enumerated under containers.ports. Network-policy authoring and basic introspection (kubectl describe, kubectl port-forward) require explicit declaration.
Recommendation: Declare ports for health (8081), metrics ({{ .Values.metrics.port }}), and webhook (9443).
Risk:           Documentation/UX issue rather than runtime risk; complicates network policy work later.
```

```
ID:             HELM-10
Title:          Helm Chart.yaml has placeholder metadata
Severity:       S4
Effort:         XS
Evidence:       charts/fleet-management-operator/Chart.yaml:6, :14-19 (appVersion mismatch with version; home/sources point to "YOUR_USERNAME"; maintainer email "your-email@example.com")
Observation:    Pre-release boilerplate. Chart looks unmaintained to anyone fetching it.
Recommendation: Set version, appVersion, home, sources, maintainers to real values before publishing.
Risk:           Trust and discoverability.
```

```
ID:             HELM-11
Title:          Image pull docs and example values absent
Severity:       S4
Effort:         XS
Evidence:       charts/fleet-management-operator/values.yaml:4-10
Observation:    pullPolicy and imagePullSecrets are templated correctly but undocumented for air-gapped or private-registry use cases.
Recommendation: Add a values-example.yaml or extend README with a private-registry example.
Risk:           Onboarding friction in regulated environments.
```

```
ID:             HELM-12
Title:          Controller toggles miss the discovery-requires-collector dependency
Severity:       S4
Effort:         XS
Evidence:       charts/fleet-management-operator/values.yaml:161-191
Observation:    main.go enforces "--enable-collector-discovery-controller requires --enable-collector-controller" at startup. The values.yaml comment doesn't mention this. A user enabling collectorDiscovery alone will hit a startup failure.
Recommendation: Annotate the collectorDiscovery toggle: "Requires controllers.collector.enabled: true."
Risk:           Self-inflicted startup failure on a misconfigured chart install.
```

```
ID:             HELM-13
Title:          values.yaml lacks a top-level overview comment
Severity:       S4
Effort:         XS
Evidence:       charts/fleet-management-operator/values.yaml:1-2
Observation:    The header is a single-line description. Users don't get a map of which features are independently opt-in vs default-on.
Recommendation: Add a header block summarising the controller layer model and pointing at CLAUDE.md.
Risk:           Cognitive load on first-time users.
```

```
ID:             HELM-14
Title:          ServiceMonitor port name aligned with Service
Severity:       Info
Evidence:       servicemonitor.yaml:14 (port: http); service.yaml:15 (- name: http)
Observation:    Confirmed correct. ServiceMonitor will route to the metrics port once HELM-03 lands.
```

```
ID:             HELM-15
Title:          Leader election ID hardcoded
Severity:       Info
Evidence:       cmd/main.go:213 (LeaderElectionID: "0fcf8538.grafana.com")
Question:       Are multiple-release deployments in the same cluster a supported scenario? If yes, the ID should incorporate Helm release name or namespace to avoid lease conflicts.
Observation:    Single LeaderElectionID across all chart releases. Today single tenant = single release, so impact is nil.
```

---

### TEST — Testing depth (RAG: AMBER)

**Summary.** Coverage is broad and structured: 30 test files, ~125 tests, comprehensive webhook validation tests, envtest harness covering all five controllers, dedicated audit tests for cache / watch / reconcile invariants. The single S2 is that the race detector is not enabled in CI — given the controllers' goroutine work and finalizer race surfaces, this is a real gap. Other gaps: no scale tests, ExternalAttributeSync coverage thinner than other controllers, no end-to-end 429 test, no explicit graceful-shutdown / SIGTERM test, lint config has dupl/lll disabled on internal/.

```
ID:             TEST-01
Title:          Race detector not enabled in CI
Severity:       S2
Effort:         XS
Evidence:       .github/workflows/ci.yaml:32 (make test, no -race); Makefile:62 (go test, no -race)
Observation:    Tests use mutex-protected mocks and reconcilers do concurrent work. Without -race, data races are undetectable until production.
Recommendation: Add -race to the test target in Makefile and to CI invocation.
Risk:           Latent races in mock or controller code surface only under production load. At 30k Collectors, concurrent reconciles are continuous.
```

```
ID:             TEST-02
Title:          ExternalAttributeSync controller test coverage is sparse
Severity:       S3
Effort:         S
Evidence:       internal/controller/external_sync_controller_test.go (4 It blocks vs ~13 for CollectorDiscovery)
Observation:    Missing scenarios: schedule parsing both cron and duration; HTTP basic and bearer auth; SQL DSN parse failures; the empty-result safety guard (Stalled condition); ownership claim reversion under stall.
Recommendation: Add table-driven cases covering each missing scenario, particularly the stall guard which is an explicit invariant.
Risk:           Auth or schedule regressions surface only when a customer first adopts a new source plugin.
```

```
ID:             TEST-03
Title:          No end-to-end test of 429 / rate-limit handling
Severity:       S3
Effort:         S
Evidence:       pipeline_controller_test.go:79 (mock has shouldReturn429 but no test exercises it); errors_test.go:64-70 (isTransientError correctly classifies 429)
Observation:    Error classification is unit-tested but no envtest verifies the reconciler's RequeueAfter is set correctly when 429 hits.
Recommendation: Add a test that flips shouldReturn429, runs reconcile, and asserts result.RequeueAfter > 0 and a follow-up reconcile (after delay) succeeds.
Risk:           A regression in the requeue path would re-enter the workqueue without backoff and saturate. Cross-references REC-01.
```

```
ID:             TEST-04
Title:          Lint config disables dupl and lll on internal/
Severity:       S3
Effort:         XS
Evidence:       .golangci.yml:5-6 (default: none); :39-42 (dupl, lll disabled on internal/* and api/*)
Observation:    Critical-path code is exempted from duplication and line-length checks. Default-none means accidental coverage gaps are likely.
Recommendation: Enable dupl on internal/controller; document any per-file exclusions inline.
Risk:           Copy-paste of error-handling and finalizer logic across controllers — exactly the code where a one-place fix can miss the others.
```

```
ID:             TEST-05
Title:          Finalizer 404-on-delete not directly tested
Severity:       S3
Effort:         S
Evidence:       pipeline_controller_test.go:269-301 (delete tested; no 404-injection test); CLAUDE.md mandates 404-as-success
Observation:    The behaviour is implemented (REC-05 verified) but no test asserts that injecting a 404 from DeletePipeline still completes finalizer removal.
Recommendation: Add a test that injects 404, deletes the CR, and asserts the finalizer is removed and the CR is GC'd.
Risk:           A regression in the 404 path would leak finalizers; CRs would stick on delete.
```

```
ID:             TEST-06
Title:          Test isolation between Ginkgo specs not explicitly serialised
Severity:       S3
Effort:         M
Evidence:       suite_test.go:101 (shared collectorMock); policy_controller_test.go:54-62 (BeforeEach reset, mutex doesn't cover all mock fields)
Observation:    Ginkgo's default is serial within a Describe but a future flip to parallel mode would race on the shared mock. Current test passes but is brittle.
Recommendation: Switch to per-test mock instances or guard the shared mock comprehensively. Add a //go:build comment or RunSpecsWithDefaultAndCustomReporters flag asserting serial mode if shared state is intentional.
Risk:           Flaky tests when CI is sped up via parallelism.
```

```
ID:             TEST-07
Title:          Graceful shutdown / SIGTERM behaviour not tested
Severity:       S3
Effort:         S
Evidence:       absent: no test exercises SetupSignalHandler path with a slow Fleet API
Observation:    The graceful-shutdown design is sound (REC-04, UPG-04) but no test asserts that a reconcile in-flight at SIGTERM completes within terminationGracePeriodSeconds. Cross-references HELM-04.
Recommendation: Envtest scenario: slow mock Fleet, in-flight reconcile, send shutdown context, assert clean completion.
Risk:           A regression in shutdown wiring is invisible until a deployment rolls.
```

```
ID:             TEST-08
Title:          No benchmarks or scale-test scaffolding
Severity:       S4
Effort:         M
Note:           PERF-08 (ObservedGeneration short-circuit not exercised at 30k scale) has been moved to this TEST category and is now tracked here alongside the broader scale-test scaffolding work.
Evidence:       absent: no *_bench_test.go; no test/scale/
Observation:    Per the calibration target (30k Collectors), even a 1% scale (300) baseline test would surface memory and reconcile-latency regressions early. Cross-references PERF-09. Also covers PERF-08 (moved): no scale test confirming that 95%+ of reconciles short-circuit at 30k.
Recommendation: Add test/scale/ harness creating N Collectors / M Pipelines / P Policies and measuring memory, reconcile p50/p95/p99, and Fleet API budget consumption. Include an ObservedGeneration short-circuit coverage assertion (from PERF-08). Run on demand in CI rather than every PR.
Risk:           Scale regressions land silently; the next 30k-validation cycle costs days. A latent ObservedGeneration regression would not be caught until production sees the symptom.
```

```
ID:             TEST-09
Title:          Webhook tests question — selector empty-rejection and priority tie-breaking
Severity:       Info
Question:       Are RemoteAttributePolicy "selector must be non-empty" and "priority tie-break alphabetical" both covered in policy_webhook_test.go? Grep was inconclusive.
Observation:    Webhook tests are extensive but two specific invariants from CLAUDE.md may be under-tested.
```

```
ID:             TEST-10
Title:          Discovery sanitization edge-case coverage unclear
Severity:       Info
Question:       Do tests cover both lossless and lossy SanitizedName paths (e.g. id "x" → "x" no suffix vs ids "x"/"X" both → "x-{hash}")?
Observation:    collector_discovery_controller_test.go has a hash-suffix test but the named coverage is unclear from test descriptions alone.
```

---

### UPG — Upgrade and operability (RAG: GREEN)

**Summary.** No S2 findings. Graceful shutdown via controller-runtime's SetupSignalHandler is correct. Context propagation through rate-limiter and HTTP client is verified. Findings are all about preparing for a future v1beta1 transition that hasn't happened yet — conversion-webhook scaffolding, version-policy documentation, leader-election lease release on cancel.

```
ID:             UPG-01
Title:          No conversion-webhook scaffolding or v1 migration plan
Severity:       S3
Effort:         M
Evidence:       api/v1alpha1/groupversion_info.go (only v1alpha1); cmd/main.go (no conversion setup); PROJECT (only v1alpha1 listed). Cross-references API-05.
Observation:    All six CRDs declare only v1alpha1 with storage: true. A future v1beta1 introducing breaking schema changes will need a conversion webhook, cert plumbing, and a migration playbook.
Recommendation: Document the upgrade strategy in docs/versioning.md. Sketch a conversion-webhook skeleton even before v1beta1 lands.
Risk:           A future breaking change will require a conversion webhook to bridge stored v1alpha1 CRs. Without scaffolding, the migration is a manual data exercise.
```

```
ID:             UPG-02
Title:          LeaderElectionReleaseOnCancel disabled
Severity:       S4
Effort:         XS
Evidence:       cmd/main.go:224 (commented-out)
Observation:    Today single-replica means failover doesn't matter. When HA arrives, the previous leader will hold its lease for the full LeaseDuration on shutdown.
Recommendation: Enable LeaderElectionReleaseOnCancel: true (the in-line comment already notes it's safe).
Risk:           Slow failover at HA scale-out (~15s gap during rolling restart).
```

```
ID:             UPG-03
Title:          HTTP client connection pool not explicitly closed at shutdown
Severity:       S4
Effort:         XS
Evidence:       pkg/fleetclient/client.go:51-62 (no Close); cmd/main.go:384 (no shutdown hook)
Observation:    Idle pooled connections linger until process exit. Nominal at single-replica, low pod churn; minor TCP TIME_WAIT pressure under high churn.
Recommendation: Add a Close() method on the client calling httpClient.CloseIdleConnections; register with mgr.Add or wrap manager start.
Risk:           Resource hygiene only.
```

```
ID:             UPG-04
Title:          HTTP timeout 30s vs rate-limiter queueing not coordinated
Severity:       S4
Effort:         S
Evidence:       pkg/fleetclient/client.go:52 (Timeout 30s); :62 (limiter 3 req/s with burst=1)
Observation:    Same finding as REC-01 from the upgrade-operability angle: the HTTP timeout should be set with the rate-limiter queue depth in mind. Once REC-01 is fixed (burst increase), the ceiling becomes more honest.
Recommendation: Resolve REC-01; then document the relationship explicitly.
Risk:           See REC-01.
```

```
ID:             UPG-05
Title:          CHANGELOG lacks upgrade-notes section template
Severity:       S4
Effort:         S
Evidence:       CHANGELOG.md (empty under Unreleased; no Upgrade Notes section). Cross-references DOC-06.
Observation:    There is no scaffold for documenting per-release upgrade steps (CRD schema changes, Helm value renames, drainage requirements).
Recommendation: Add an "Upgrade Notes" subsection template in the keep-a-changelog format and start populating from v1.0.0 onward.
Risk:           Operators upgrading will not have a single place to find migration steps.
```

```
ID:             UPG-06
Title:          Storage version policy and breaking-change playbook absent
Severity:       S3
Effort:         S
Evidence:       config/crd/bases/*.yaml (storage: true on v1alpha1 only); no MIGRATION.md
Observation:    Without a documented policy, a maintainer making an "innocent" status field rename in v1alpha1 today will strand existing CRs.
Recommendation: Document API stability commitments per resource version. List allowed/forbidden change classes (additive: yes; renames: needs conversion webhook; semantic shifts: bump version).
Risk:           Accidental breaking change in v1alpha1 is the single biggest upgrade hazard while there's no v1beta1.
```

```
ID:             UPG-07
Title:          Rate-limiter Wait respects context — verified
Severity:       Info
Evidence:       pkg/fleetclient/interceptors.go:32-40
Observation:    limiter.Wait(ctx) returns context.Canceled cleanly on shutdown.
```

```
ID:             UPG-08
Title:          HTTP client context propagation correct across all Fleet API calls
Severity:       Info
Evidence:       pkg/fleetclient/client.go:97-127; internal/controller/pipeline_controller.go:205
Observation:    All Fleet API calls receive the reconcile context; cancellation aborts in-flight requests.
```

---

### DOC — Documentation and runbooks (RAG: RED)

**Summary.** Developer-facing docs (README, CONTRIBUTING, CLAUDE.md, samples for each CRD, Helm chart README, TenantPolicy v1 gap docs) are genuinely good. The on-call and operability surface is missing entirely: no Grafana dashboard JSON, no Prometheus alert rules deliverable, no runbook for the alerts that will fire at this scale, no troubleshooting guide for the failure modes a 30k tenant will hit. Four S2 findings compound: an incident in production today would land on someone with no playbook.

```
ID:             DOC-01
Title:          No Grafana dashboard JSON for operator metrics
Severity:       S2
Effort:         S
Evidence:       absent: expected at config/dashboards/ or charts/fleet-management-operator/dashboards/
Observation:    Once OBS-01 / HELM-03 land, the metric set will be useful. Without a dashboard, on-call is reading raw PromQL during incidents.
Recommendation: Ship a starter dashboard with: per-controller reconcile latency / error rate; Fleet API call latency / errors / rate-limiter wait time; workqueue depth; sync-drift gauges; webhook latency and rejection rate; leader-election state. JSON in chart so users get it on install.
Risk:           Triage MTTR multiplies. A rate-limit saturation event at 30k looks identical to "Fleet API down" without dashboard panels distinguishing them.
```

```
ID:             DOC-02
Title:          No Prometheus alert rules
Severity:       S2
Effort:         S
Evidence:       absent: no PrometheusRule template under charts/fleet-management-operator/templates/
Observation:    ServiceMonitor exists but no alert rules. Operator-down, reconcile-error-rate, rate-limit saturation, webhook-unavailable, finalizer-stuck-on-delete are unalerted.
Recommendation: Ship a PrometheusRule template gated on alerts.enabled with rules for the items above. Use OBS metrics once they exist; for now, controller-runtime built-ins cover the basics.
Risk:           On-call is not paged. Silent degradation for hours-to-days.
```

```
ID:             DOC-03
Title:          Troubleshooting guide thin and missing scale failure modes
Severity:       S2
Effort:         M
Evidence:       README.md:231-263 (4 issues: pipeline not syncing, auth error, validation error, rate limit)
Observation:    Misses: rate-limit saturation diagnosis (not just "you got 429"); webhook rejection at enrollment; finalizer stuck after partition; large-status etcd bloat; cache lag symptoms; informer cache rebuild on restart; per-controller failure modes (Sync stalled, Discovery sanitisation collisions).
Recommendation: Promote troubleshooting to docs/troubleshooting.md with a symptom-to-cause decision tree and per-controller subsections.
Risk:           Each on-call rediscovers the same patterns. MTTR stays high.
```

```
ID:             DOC-04
Title:          No on-call runbook
Severity:       S2
Effort:         M
Evidence:       absent: docs/runbooks/ or equivalent
Observation:    Alerts (when they exist) need linked runbooks. None today.
Recommendation: docs/runbooks/ with one page per alert: signature, verification steps, mitigation options, escalation path. At minimum: controller-down, rate-limit-saturation, webhook-unavailable, finalizer-stuck, cache-lag, discovery-collision.
Risk:           A 3am page lands on someone who has to read code to act. 30+ min of avoidable MTTR per incident.
```

```
ID:             DOC-05
Title:          Webhook setup and TLS strategy not documented user-side
Severity:       S3
Effort:         XS
Evidence:       absent: docs/webhook-setup.md; CLAUDE.md does not cover TLS strategy. Cross-references WH-01, WH-03.
Observation:    Users deploying without cert-manager have no path to manual cert provisioning. Cert lifetime, rotation, and CA injection are undocumented.
Recommendation: docs/webhook-setup.md covering self-signed (default), cert-manager (recommended), and manual modes. Include kubectl checks and common failure signatures.
Risk:           Production webhook outage from cert expiry is a 30+ min triage without a runbook.
```

```
ID:             DOC-06
Title:          CHANGELOG lacks per-release migration notes
Severity:       S3
Effort:         XS
Evidence:       CHANGELOG.md (template state). Cross-references UPG-05.
Observation:    Same finding as UPG-05 from the user-doc angle. Resolve once.
```

```
ID:             DOC-07
Title:          No Helm-chart-deployment troubleshooting section
Severity:       S3
Effort:         S
Evidence:       README.md and chart README focus on Pipeline lifecycle, not install-time failures
Observation:    Users hitting CRD-not-found, RBAC mismatch, or webhook-not-ready during helm install have no guide.
Recommendation: Add a "Troubleshooting installation" section to the chart README with kubectl debugging commands per failure class.
Risk:           Onboarding friction; support load.
```

```
ID:             DOC-08
Title:          CRD samples are realistic and complete — verified
Severity:       Info
Evidence:       config/samples/ — one per CRD, exercised by e2e
Observation:    Samples are usable out of the box. Logged Info to prevent re-flagging.
```

```
ID:             DOC-09
Title:          User-facing README and chart README are production-grade — verified
Severity:       Info
Evidence:       README.md; charts/fleet-management-operator/README.md
Observation:    Install paths, configuration options, parameter tables, HA section, and per-CRD examples are present.
```

```
ID:             DOC-10
Title:          TenantPolicy v1 gaps explicitly documented — verified
Severity:       Info
Evidence:       docs/tenant-policy.md:128-146; CLAUDE.md
Observation:    Cluster admins are not blindsided by the documented v1 limits. Cross-references SEC-05.
```

```
ID:             DOC-11
Title:          API godoc adequate for kubectl explain
Severity:       Info
Evidence:       api/v1alpha1/*_types.go — field-level comments throughout
Observation:    kubectl explain output is informative. No critical missing godoc.
```

```
ID:             DOC-12
Title:          CONTRIBUTING.md is comprehensive — verified
Severity:       Info
Evidence:       CONTRIBUTING.md
Observation:    Dev setup, build, test, code-style, PR process, useful make targets all documented.
```

---

## 4. Out-of-scope notes

The following were out of scope per the framework spec and were not audited:

- Downstream / wrapper Helm charts.
- Fleet Management server-side behaviour. The audit covered the operator's *use* of the API.
- Multi-tenant isolation hardening (NetworkPolicy, per-tenant RBAC sharding).
- Producing the dashboard JSON / Prometheus alert rules themselves (DOC-01, DOC-02 recommend them; phase 2 builds them).
- CI/CD pipeline review beyond Makefile and lint.
- Performance load testing (the audit identifies *where* scale risk lives by code reading; running an actual 30k-collector load test is separate).
- License and CVE scanning.
- HA polish for replicas > 1 beyond identifying readiness concerns.
- Re-auditing v1.1 milestone work end-to-end (PERF-10 spot-check confirms drift-free).

## 5. Method recap

- **Source commit:** `013f042`. Findings reference file:line at this commit.
- **Categories and rubric:** `docs/superpowers/specs/2026-04-28-production-readiness-audit-design.md`.
- **Severity:** S1 / S2 / S3 / S4 / Info as defined in the spec.
- **Execution:** 10 parallel Haiku sub-agents (one per category), each given a tight prompt with calibration, schema, and category-ownership rules. Aggregation pass de-duplicated cross-category overlaps using the ownership rules:
  - Resource sizing: HELM owns the values; PERF carries the math.
  - Metrics not bound: HELM owns the template fix; OBS carries the consequence.
  - Missing tests: TEST always wins.
  - Security/RBAC concerns: SEC always wins.
  - Documentation deliverables: DOC owns the artefact; UPG/WH carry the cross-references.
- **RAG status:** computed from S1/S2 findings per the spec's RAG definitions, with "compounding S2" promoting AMBER to RED for PERF, HELM, and DOC where the S2 set converges on a single failure mode.

## 6. Phase 2 entry

After this scorecard is reviewed, a separate brainstorming session will design the remediation work. Cite finding IDs (e.g. "address HELM-01, HELM-02, HELM-03, REC-01, PERF-01") to scope phase 2.
