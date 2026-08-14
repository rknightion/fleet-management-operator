---
id: doc-0002
title: Wave operating model
type: guide
created_date: '2026-08-14 16:37'
updated_date: '2026-08-14 16:37'
---
Everything generic about running a wave lives in the **Agent fan-out protocol (canonical)** doc.
This document adds only what is true of *this* repository, and deliberately restates none of it.

## Exclusive resource: the Fleet Management API rate budget

The Fleet Management API is rate-limited at **3 req/s by default** for the whole Grafana Cloud
org, and it is shared with any real collector fleet already pointed at that stack. It is not a
per-lane resource and it cannot be sharded.

**At most one lane at a time may hold live Fleet Management credentials.** A wave that wants two
lanes exercising the live API must serialise them explicitly in the goal file, or give the second
lane the mock API in `test/e2e` instead. Two lanes racing the live stack do not fail loudly - they
consume each other's tokens and both look like an upstream outage.

The measured shape of that failure is recorded in the code: with `burst=1` and a 30s HTTP timeout,
request number `(rps x 30) + 1` in a restart wave waits the full 30s at the limiter and then times
out, which is indistinguishable from the Fleet API being down. That is why `burst=50` is the
default and why `MaxConcurrentReconciles` stays at 1 for the Pipeline and Collector controllers.
A lane that "parallelises the reconciler to go faster" is re-introducing a defect that was already
found, diagnosed and fixed here.

## Generated artifacts are wiring, never lane-owned

These are regenerated from source markers, not hand-written. Two lanes both regenerating produce
conflicting blobs that no merge resolves sensibly:

- `api/v1alpha1/zz_generated.deepcopy.go` - `make generate`
- `config/crd/bases/*.yaml` and `config/rbac/role.yaml` - `make manifests`
- `charts/fleet-management-operator/README.md` - `helm-docs`, via `make chart-docs`
- `docs/` API reference and `docs/events.md` - `make docs`

**No lane runs `make manifests`, `make generate` or `make docs`.** The wiring pass runs them once,
after the lanes land, and commits the regenerated output as its own change. A lane that adds a
kubebuilder marker states that fact in its final summary so the wiring pass knows to regenerate.

This is the repo's most reliable way to produce a red CI on a green-looking wave: `ci.yaml` runs
`make manifests` and `make docs-check` / `make chart-docs-check` and fails on any diff, so an
un-regenerated marker breaks the build for every other lane at once.

## Adding a CRD field is a five-file change, not a one-file change

The single most common incomplete change in this codebase. A new field on any CRD type touches:

1. `api/v1alpha1/<kind>_types.go` - the field plus its kubebuilder validation markers;
2. `api/v1alpha1/<kind>_webhook.go` - admission validation, if the field has rules a CEL marker
   cannot express;
3. `internal/controller/<kind>_controller.go` - the field actually being read;
4. the regenerated set above;
5. tests in both `api/v1alpha1/` (table-driven webhook cases) and `internal/controller/`.

Landing 1 and 3 without 2 ships a field that admission accepts in any shape. Landing 1 without 4
reds the build. Assign the whole vertical to one lane; do not split a CRD field across lanes by
layer.

## Recurring defects in this codebase, with instances

**Fleet `UpsertPipeline` / `UpdatePipeline` are not selective - unset fields are deleted.** A lane
"only changing the config contents" that builds a partial request silently removes the pipeline's
matchers from the live fleet, and the operator's own status will report success. Always send every
spec field. This is the one API behaviour here that destroys customer state rather than erroring.

**`Update()` where `Status().Update()` was meant**, and the mirror of it. Status subresource writes
that go through `Update()` are dropped without an error.

**Returning an error on `IsConflict` from `Status().Update()`.** A conflict is informer cache lag,
not a transient API failure. Returning an error puts the item into workqueue exponential backoff
for a condition that clears on the next read. The correct return is
`ctrl.Result{Requeue: true}, nil`.

**`SyncPeriod` being helpfully added back to `ctrl.Options`.** It is omitted deliberately. An
explicit resync period triggers a full reconcile storm of every CR on every interval, straight into
the 3 req/s budget above. It has been re-proposed more than once because its absence looks like an
oversight.

**Instruction-file rot, measured.** `AGENTS.md` sat at its original kubebuilder scaffold content
from the `fm init` commit through roughly four months of active development, describing none of
this project, while `CLAUDE.md` carried every real rule. Codex sessions were running against
generic boilerplate the whole time and nothing surfaced it. That is why there is now exactly one
canonical file and `CLAUDE.md` is a four-line import. **The four sub-directory `CLAUDE.md` files
are still live and still auto-load** - a lane changing a rule in `AGENTS.md` must check whether
`api/v1alpha1/`, `internal/controller/`, `pkg/` or `charts/fleet-management-operator/` contradicts
it, because a contradiction between two auto-loaded files is worse than either fact being absent.

**Competing queues.** Before this tracker existed the repo carried open work in three places at
once - GitHub Issues, a tracked `.planning/` phase tracker, and gitignored `docs/superpowers/`
audits. Two genuine deferrals (`PERF-02`, `API-09`) survived only in the gitignored one, invisible
to any clone or CI run. `backlog/` is now the only queue. If a wave produces durable state, it goes
in a task or a doc - not a new directory.

**Bare `gh` resolves to the wrong repository.** This checkout has `origin`
(`rknightion/fleet-management-operator`) and `upstream` (`mbaykara/fleet-management-operator`, the
fork source). A bare `gh issue list` answers for upstream and returns zero, which reads as "the
board is clean" rather than "you asked the wrong repo" - it did exactly that during this
migration, on a repo that had three issues. Always `-R rknightion/fleet-management-operator`, and
always `--limit 1000`.

## Lane conventions

Natural, non-overlapping boundaries in this codebase, roughly in descending order of how cleanly
they separate:

- `pkg/fleetclient/` - the Fleet API client, its interceptors, metrics and tracing;
- `pkg/sources/{http,sql}/` - external source plugins; each plugin is its own lane;
- `api/v1alpha1/` - CRD types and admission webhooks;
- `internal/controller/<name>_controller.go` - one lane per controller. The five are Pipeline,
  Collector, RemoteAttributePolicy, ExternalAttributeSync, and the two Discovery reconcilers;
- `internal/tenant/` - the TenantPolicy subject-matcher checker;
- `charts/fleet-management-operator/` - chart templates and values;
- `docs/` - prose docs that are *not* generated (runbooks, security, troubleshooting, versioning).

**Cross-lane hazards that look like separate lanes but are not.** The Collector controller is the
single writer of collector attributes; RemoteAttributePolicy and ExternalAttributeSync express
intent through their own status and never call `BulkUpdateCollectors`. A lane touching either
policy controller is inside the Collector controller's contract even though it edits a different
file - freeze the precedence order and the status field names in the goal file before fanning out,
or the merge produces a write race that no test in the repo currently catches.

`cmd/main.go` is a wiring file. Flags, controller registration and the source factory all live
there and every lane wants to add to it. **One lane or the wiring pass owns it - never two.**

## Ownership and the escape hatch

One file, one owner, for the duration of the wave. A lane that needs to change a file it does not
own **stops and returns the question in its final summary** rather than editing it or working
around it. That is the escape hatch, and it is why a boundary here is not a stop condition: the
wiring pass exists to absorb exactly these, and one round-trip is cheaper than untangling two
lanes' edits to `cmd/main.go`.

If a lane is blocked on something outside the repo - a Grafana Cloud stack it cannot reach, a
credential it does not have, an SDK capability that does not exist yet - it does not guess. It
parks its task with a concrete resume boundary and says what would unblock it.

## Run-end against this tracker

Task state is the record. Nothing durable may live only in the terminal.

- Landed work: `backlog task edit FMO-nnnn --check-ac 1 --check-ac 2 -s Done` in **one call**,
  with the commit SHA in the final summary.
- Blocked work: status `Parked`, with a resume boundary specific enough that a session with no
  memory of the run can pick it up - what was tried, what it is waiting on, and what would
  unblock it. "Blocked on upstream" is not a resume boundary; "blocked until the Fleet SDK's
  `ListCollectorsRequest` exposes `page_token`/`page_size`, at which point `pkg/fleetclient/
  collector.go` adopts it with no CRD change" is.
- Untouched work is self-evidently still `To Do`. Do not touch it to show progress.
- Work the run discovered: a new task labelled `needs-triage`, not a fix smuggled into an
  unrelated lane.

The gate before anything moves to `Done` is `definition_of_done` in `backlog/config.yml` -
`make lint` and `make test` always, plus the regeneration and docs checks when their inputs
changed. Evidence, not assertion: a lane claiming green without the output is treated as not run.

The closing message to the terminal is a covering note answering one question - **what did this run
learn that no single task captures?** It is a terminal action, not a reply to a request. Nobody
asks for it; writing it is the last unit of work.
