# Production Readiness Audit — Framework Spec

**Date:** 2026-04-28
**Author:** Rob Knight (with Claude)
**Status:** Approved framework. Phase 1 execution pending.

## Purpose

Define the methodology, rubric, severity model, and deliverable shape for a
production-readiness audit of the `fleet-management-operator` codebase. This
document is the contract between the user and the audit-execution step. The
audit itself (the scorecard) is a separate research artefact produced under
`docs/superpowers/audits/`. Once the scorecard exists, a follow-up
brainstorming pass will design the remediation work for the highest-priority
findings.

## Calibration

The audit is calibrated against the following operational target. Every
severity rating depends on this calibration; if the target changes, the
scorecard is partially invalidated.

- **Tenancy:** single tenant, large.
- **Scale:** 30,000 collectors, thousands of pipelines, single Grafana Cloud
  Fleet Management stack.
- **Lifecycle:** pre-production today, heading toward production.
- **Replication:** single replica acceptable for the foreseeable future;
  HA polish (PDB, anti-affinity, multi-replica leader election tuning) is
  flagged as a readiness concern but not deeply audited.
- **API ceiling:** Fleet Management management endpoints are rate-limited
  to 3 requests/second.
- **Existing audit work:** v1.1 milestone covered Informer Cache Audit,
  Reconcile Loop Optimization, and Watch Pattern Tuning. Those areas are
  spot-checked only, not re-audited end-to-end.

## Rubric — Audit Categories

Ten categories, each with a stable ID prefix used in finding identifiers.
Sub-areas listed below each are illustrative, not exhaustive.

1. **API — CRD / API design.** Versioning posture, status subresource on
   every CRD, printer columns, OpenAPI/CEL validation, immutability
   markers, declarative defaults, standardized condition types and
   reasons, scale subresource where applicable, short names and
   categories.
2. **REC — Reconciliation correctness.** Idempotency, finalizer ordering
   and 404-on-delete handling, `ObservedGeneration` usage (and the
   deliberate non-use on the Collector reconciler — verify rationale
   still holds), `Status().Update()` discipline, error wrapping
   (`fmt.Errorf("%w", err)`), retry vs `RequeueAfter` semantics, conflict
   resolution on optimistic concurrency, context propagation. Includes
   Fleet API client robustness: retry/backoff, 429 handling, timeouts,
   connection reuse.
3. **PERF — Performance and scaling at 30k collectors.** Informer cache
   sizing and label-selector filtering, `MaxConcurrentReconciles` per
   controller, watch predicates, workqueue depth and rate-limiter
   tuning, the 3 req/s API budget across reconcilers, memory bounds on
   status fields (e.g. `matchedCollectorIDs`), pagination readiness for
   `ListCollectors`, CollectorDiscovery sharding strategy.
4. **WH — Webhook hardening.** Cert management strategy, `FailurePolicy`
   (Fail vs Ignore — and bootstrap-deadlock risk), `sideEffects: None`,
   `namespaceSelector`/`objectSelector`, `timeoutSeconds`, webhook
   self-bootstrap risk.
5. **OBS — Observability.** controller-runtime built-in metrics exposed
   and scraped, custom domain metrics (Fleet API call count/latency/
   errors, sync-drift gauges, owned-key cardinality, rate-limiter wait
   time), event recorder coverage, structured logging hygiene, tracing
   readiness.
6. **SEC — Security and RBAC.** Least-privilege ClusterRole, Pod
   `SecurityContext`, image hardening, secret handling, NetworkPolicy
   (de-prioritized for single tenant).
7. **HELM — Helm and deploy ergonomics.** Resource requests/limits sized
   for 30k-collector cache, probes, leader election, PDB readiness (gated
   until replicas > 1), anti-affinity, configurable log level, image
   pull policy, graceful shutdown.
8. **TEST — Testing depth.** Unit coverage of reconcile happy/error
   paths, envtest integration breadth, e2e coverage, race detector in
   CI, webhook test depth, lint hygiene, scale-test scaffolding.
9. **UPG — Upgrade and operability.** CRD version-bump readiness
   (v1alpha1 → v1beta1 plan), conversion-webhook scaffolding, status-
   field backwards compatibility, deprecation policy, graceful shutdown
   completing in-flight reconciles, restart resilience.
10. **DOC — Documentation and runbooks.** User-facing samples, deployment
    doc, troubleshooting guide, dashboard JSON for the metrics in OBS,
    alert rules for the SLOs implied by OBS, on-call runbook.

## Severity Tiers

- **S1 — Critical.** Correctness bug, data-loss risk, security hole, or
  behavior that will page on-call once load arrives. Blocks production
  adoption.
- **S2 — High.** Degrades meaningfully at 30k-collector scale, blocks
  on-call usability (no metric / no event / no log to triage by), or
  non-trivial security posture gap. Address before serving real traffic.
- **S3 — Medium.** Best-practice gap or hardening that improves
  operability and longevity. Steady-state work.
- **S4 — Low.** Nice-to-have, future-proofing, micro-optimization.
  Backlog.
- **Info.** Observation only — no recommended action. Used to capture
  deliberate design choices that should not be re-flagged later.

## Effort Estimates

Coarse single-engineer estimates:

- **XS** — under 1 day
- **S** — 1–3 days
- **M** — 3–10 days
- **L** — 2–4 weeks
- **XL** — multi-month or multi-engineer

## Finding Schema

Each finding is rendered as a small block (not a wide table — rows
exceed terminal width).

```
ID:             <CAT>-<NN>      e.g. PERF-03, OBS-05
Title:          one-line summary
Severity:       S1 | S2 | S3 | S4 | Info
Effort:         XS | S | M | L | XL
Evidence:       file:line(s), or "absent: expected at <path>"
Observation:    what is there (or not), why this is a finding
Recommendation: proposed remediation, high-level (spec'd properly in phase 2)
Risk:           what could go wrong if untreated — at this scale, not generic
```

For Info-tier findings only, an additional optional field:

```
Question:       open question for the user to answer on review
```

IDs are stable and used by phase 2 remediation specs. Numbering is
sequential within each category prefix, in the order findings are
written into the scorecard.

## Evidence and Quality Rules

These rules apply to every finding written by every sub-agent.

1. **Every finding cites evidence.** Either a `file:line` reference, or
   an explicit "absent: expected at `<path>`" line. No "I think this
   might be a problem" findings without a concrete anchor.
2. **Uncertainty downgrades to Info with a Question.** If a sub-agent
   cannot decide a finding's severity within roughly 5 minutes of
   reading, it files the finding as Info with a `Question` field
   instead of guessing. The user resolves on review.
3. **Risk lines are concrete and scale-anchored.** Not "could cause
   problems" but a sentence with numbers where they can be computed —
   e.g. "at 30k collectors a full re-list saturates the 3 req/s limit
   for ~2.7 hours" — and the calculation is shown inline so the user
   can sanity-check it.
4. **No code edits.** Audit is read-only.
5. **No test runs, no manager startup.** Code review only; runtime
   probing is out of scope.
6. **Trust-but-spot-check on v1.1 milestone work.** Sub-agents read the
   v1.1 commit messages and supporting tests but only re-flag if they
   observe drift since.
7. **Category ownership.** When a single root cause spans multiple
   categories, it is filed once under its primary category and
   cross-referenced from the others. Ownership rules:
   - Missing metric or event → OBS.
   - Missing test → TEST.
   - Security or RBAC concerns (missing SecurityContext, RBAC
     over-grant, secret leakage in logs, image hardening) → SEC.
   - Missing Helm value or probe → HELM.
   - All other reconciler behaviour → REC.
   - Scale-specific concerns (memory, cache size, rate-limit budget) →
     PERF, even if they manifest in a single reconciler.

## Execution — Parallel Sub-Agent Architecture

Phase 1 produces the scorecard. Execution model:

- One `Explore`-type sub-agent on Haiku per category (10 sub-agents
  total), dispatched in parallel from a single message.
- Each sub-agent receives a tight prompt that includes:
  - Its category name, ID prefix, and the relevant scoping bullet from
    the rubric above.
  - The reading scope (which files/directories to inspect).
  - The full severity rubric and finding schema verbatim.
  - The evidence and quality rules verbatim.
  - The category-ownership rules so it does not file out of scope.
  - The calibration block.
- Each sub-agent returns a structured markdown report with: per-finding
  blocks for its category, a one-paragraph summary, and a proposed RAG
  status with rationale.
- Aggregation step (main agent) merges the 10 reports into the single
  scorecard, performs cross-category de-duplication using the ownership
  rules, writes the executive summary and the per-category RAG block,
  and adds cross-references between related findings.

### Reading Scope (per sub-agent, common base)

- `api/v1alpha1/*.go`
- `internal/controller/*.go` and subpackages
- `pkg/fleetclient/`, `pkg/sources/`
- `cmd/main.go`
- `config/{crd,rbac,webhook,manager}/`
- `charts/fleet-management-operator/`
- `Dockerfile`, `Makefile`, `PROJECT`, `go.mod`
- `test/`
- `docs/` (existing) and recent v1.1 milestone commits

Per-category sub-agent prompts narrow this scope to the directories
that matter most for that category (e.g. WH inspects the webhook files
plus `config/webhook/manifests.yaml` plus the chart's webhook
templates; HELM inspects the chart and `Dockerfile`).

### Aggregation Rules

- De-dup by root cause, not by surface symptom. If three sub-agents
  flag the same root cause, keep the one in the owning category and
  add `See also: <ID>, <ID>` cross-references on the others (or drop
  them entirely if they add no incremental information).
- The main agent does not invent new findings during aggregation. It
  only merges, de-dups, and writes summary/RAG content.
- If aggregation reveals a category has fewer than 2 findings, the
  main agent does not pad. Empty is a finding in itself.
- If aggregation reveals more than 8 findings in a category, the main
  agent considers consolidation but defers to the sub-agent's
  judgement when the findings are independent.

## Deliverable

**File:** `docs/superpowers/audits/2026-04-28-production-readiness-scorecard.md`

**Outline:**

1. Executive summary — 1-paragraph headline, calibration recap, top-5
   findings as ID + title + severity (no detail).
2. Per-category RAG summary — for each of the 10 categories: name,
   GREEN/AMBER/RED status with one-line rationale, finding counts,
   1–3 standout observations.
3. Findings inventory — grouped by category, ordered within category by
   severity (S1 first), one block per finding using the schema above.
4. Out-of-scope notes — list of deliberate non-goals (see below).
5. Calibration and method — source commit SHA, rubric used, severity
   rubric used, RAG status definitions used.

**RAG status definitions:**

- **GREEN** — no S1 or S2 findings; S3 findings are standard polish.
- **AMBER** — has S2 findings but no S1; or pattern of S3 findings
  concentrated in one subsystem.
- **RED** — at least one S1; or compounding S2 findings (e.g.
  on-call would be blind).

**Length budget:** ~3,000–5,000 words total. Roughly 1–2 pages exec
plus RAG, 6–10 pages findings inventory.

## Out of Scope (Explicit Non-Goals)

- Downstream / wrapper Helm charts. Only `charts/fleet-management-operator/`.
- Fleet Management server-side behaviour. Audit covers the operator's
  *use* of the API.
- Multi-tenant isolation hardening. Single-tenant target — NetworkPolicy
  and per-tenant RBAC sharding flagged as Info at most.
- Grafana Cloud dashboards and alert rules as deliverables. The audit
  recommends them and defines what should be measured; producing
  dashboard JSON and Prometheus rules is phase 2 work.
- CI/CD pipeline review. The audit reads `Makefile` and notes lint/
  test/coverage gaps but does not audit GitHub Actions workflows or
  release pipelines.
- Performance load testing. The audit identifies where scale risk
  lives via code reading and back-of-envelope math; running an actual
  30k-collector load test is a separate exercise.
- License and dependency-CVE scanning.
- HA polish for replicas greater than 1. PDB, anti-affinity, leader-
  election lease tuning are called out as readiness concerns but not
  deeply audited.
- Re-auditing v1.1 milestone work end-to-end. Spot-check only.

## Phase 2

After the scorecard exists and the user has triaged it, a separate
brainstorming session designs the remediation work. That session cites
finding IDs from this audit as the unit of scope.
