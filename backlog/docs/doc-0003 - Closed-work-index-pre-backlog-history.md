---
id: doc-0003
title: Closed work index (pre-backlog history)
type: other
created_date: '2026-08-14 16:39'
updated_date: '2026-08-14 16:39'
---
The record of work closed **before** `backlog/` became this repository's tracker, kept as one
document so the history is readable from the checkout alone and so the original ID spaces stay the
only ones. Compiled 2026-08-14 during the migration.

Backlog task IDs follow creation order, so importing any of this as `Done` tasks would have created
a second ID space over the same history - an `FMO-nnnn` that can never be made to match the `#N` or
phase number already cited in commit messages, code comments and the docs. These rows point at the
originals instead.

## Where the full detail still lives

- **GitHub issues** - bodies and comments are live at
  `gh issue view <N> -R rknightion/fleet-management-operator`. The issues were **not** deleted, so
  this section is a pointer, not a replacement.
- **`.planning/`** - the phase tracker was deleted from the working tree in the migration commit.
  Recover any of it with `git show <sha>:.planning/<path>`; the tree is intact at `72499c5`
  (2026-02-09), the last commit that touched it.
- **`docs/superpowers/`** - gitignored, still on disk, never committed. The production-readiness
  scorecard named below is not recoverable from git on another machine.

## Closed GitHub issues

Two. Everything numbered `#1`-`#7` in this repository is a **pull request**, not an issue -
`gh issue view <n>` silently renders a PR when the number is a PR, which makes the issue set look
larger than it is.

| # | Title | Closed | Reason | Resulting SHA |
|---|---|---|---|---|
| 8 | Optional namespace-scoped Fleet pipeline naming (non-breaking) + `spec.name` validation | 2026-06-01 | completed | `6b89fda` (webhook validation, phase 1), `7439ae4` (opt-in namespace-scoped naming with auto-migration) |
| 9 | Document cross-namespace discovery trust boundary; consider opt-in gate | 2026-06-01 | completed | `9b58bfc` |

`#8` was itself the non-breaking follow-up to PR `#3` ("Harden pipeline naming to namespace
scope"), which was closed unmerged because always deriving the Fleet pipeline name from
`namespace.name` is a silent breaking change - Fleet upsert is name-keyed for idempotency, so the
rename orphans the existing pipeline rather than moving it. That reasoning is the reason the
feature is opt-in today, and it is the kind of thing that gets re-litigated if only the outcome
survives.

`#9` resolved the mirror question for `PipelineDiscovery` / `CollectorDiscovery`: cross-namespace
mirroring is a **kept, intended capability**, documented as a trust boundary rather than blocked.
PR `#6` had proposed hard-blocking it; that was rejected.

The one open issue, `#46` "Dependency Dashboard", is Renovate's own bot-maintained issue. It is not
work, it is recreated on every Renovate run, and it is deliberately not represented in `backlog/`.

## `.planning/` - v1.0.0 and v1.1 phase tracker (2026-02-08 to 2026-02-09)

7 phases, 12 plans, all complete. Milestone consolidated as v1.0.0 at `6704801`; v1.0 closed at
`b2d223a`, v1.1 closed at `3fcc31b`.

| Phase | Plan | Title | Completed | SHA |
|---|---|---|---|---|
| 1 | 01-01 | Client Layer Error Foundation | 2026-02-08 | `fcbf1af` |
| 1 | 01-02 | Client layer error foundation tests | 2026-02-08 | `2b48d9e` |
| 2 | 02-01 | Controller error handling fixes | 2026-02-08 | `928aac6` |
| 2 | 02-02 | Controller error handling tests | 2026-02-08 | `b59fd6f` |
| 2 | - | Phase 2 execution complete | 2026-02-08 | `69d0bf6` |
| 3 | 03-01 | Logging quality improvements | 2026-02-08 | `d8c9a51` |
| 3 | 03-02 | Logging quality tests | 2026-02-08 | `0c749a2` |
| 3 | - | Phase 3 execution complete | 2026-02-08 | `ffeb8c2` |
| 4 | 04-01 | Mock Fleet Management API and fixtures | 2026-02-09 | `971ae7b` |
| 4 | 04-02 | Pipeline lifecycle E2E tests | 2026-02-09 | `35c3ca2` |
| 4 | 04-03 | GitHub Actions E2E workflow | 2026-02-09 | `fdc1796` |
| 4 | - | Phase 4 execution complete | 2026-02-09 | `5fea232` |
| 5 | 05-01 | Informer Cache Audit | 2026-02-09 | `ed9f948` |
| 6 | 06-01 | Reconcile Loop Optimization | 2026-02-09 | `69f2b79` |
| 6 | - | Phase 6 execution complete | 2026-02-09 | `d357d6e` |
| 7 | 07-01 | Watch Pattern Tuning | 2026-02-09 | `c98bdc7` |

**What it left behind that is still load-bearing in the codebase**, and should not be "cleaned up"
by someone who does not know why it is there:

- Zero `List()` calls in reconcile paths - all reads go through the informer cache, enforced by
  AST-based verification tests, not by convention.
- The grep-able comment prefixes `Cache:`, `Reconcile:` and `Watch:`. They exist so audit tooling
  can find every justified API call; they are not decorative.
- `SyncPeriod` deliberately absent from `ctrl.Options`, and the four documented return patterns for
  backoff.
- `updateStatusError` preserving the original error so exponential backoff works, and the
  single-retry guard on 404 recreation that stops infinite recursion.

**Two closed items that were re-opened by later work** - noted because the phase tracker records
them as done and it is misleading on its own. `loggerFor()` was flagged as dead code in the v1.0
milestone audit, and phase 1 never produced a `VERIFICATION.md`. Both were accepted as low-risk
tech debt at the time.

## `docs/superpowers/` - production-readiness audit (2026-04-28)

`docs/superpowers/audits/2026-04-28-production-readiness-scorecard.md`, against source commit
`013f042`. Calibration: single tenant, 30,000 collectors, thousands of pipelines, single replica,
3 req/s Fleet API limit. Roughly 70 findings across 10 categories, executed as a 10-way parallel
fan-out with an aggregation pass.

**Outcome: every category GREEN, no S1 findings ever raised.** The remediation log in that file is
the authoritative per-finding record, with a SHA on most rows. Headline fixes that changed
production behaviour: Fleet API `burst` made configurable and defaulted to 50 (`2e8aaf5`,
`7124397`, `6a7ba07`); `status.matchedCollectorIDs` / `ownedKeys` capped at 1000 with a `Truncated`
condition (`62cf4a3`, `c801c66`); selective Collector watch via `IndexField` on
`.spec.selector.matcherKeys`; per-controller `MaxConcurrentReconciles` (`d9ba8ef`); chart memory
limits raised from 128Mi to 1Gi; Fleet API metrics, rate-limiter wait histogram and OTEL tracing
added; Grafana dashboard, `PrometheusRule` alerts, five per-alert runbooks, `docs/troubleshooting.md`,
`docs/versioning.md` and `docs/webhook-setup.md` created.

**Two findings survived and are now live tasks, not history** - they were recorded only in this
gitignored file, invisible to any other clone or CI run, which is the specific reason they were
promoted during the migration:

- **PERF-02** - `CollectorDiscovery` pagination, blocked on the Fleet SDK.
- **API-09** - scale subresource, deferred to v1 graduation.

Find them with `backlog task list --plain`.
