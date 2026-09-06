# fleet-management-operator

Kubernetes operator that manages Grafana Cloud Fleet Management pipelines, collectors and
attribute policies as CRDs. Fleet Management distributes Alloy/OTel configuration fragments to
collectors, selected by Prometheus Alertmanager style matchers (`collector.os=linux`,
`team!=team-a`).

Never use emojis in code or documentation, with the single exception of the Alloy and OTel icons.

## Tracker

Work is tracked in `backlog/`. Deeper protocol lives in the `Agent fan-out protocol (canonical)`
and `Wave operating model` docs; `backlog doc list --plain` lists both.

- **`backlog/` is committed to a public repo, so tasks, docs and decisions must never carry real
  identifiers** - no email addresses, handles, usernames, stack ids, tenant ids, access tokens,
  live collector ids, cluster names or hostnames. Write the shape, not the instance:
  `<stack-id>`, `fleet-management-<cluster>.grafana.net`, `collector-<n>`. Counts, timings and
  structural findings are fine. Sweep before committing:

  ```bash
  grep -rniE "rknightion|rob-knight|m7kni|@gmail|grafana\.net|glc_|[0-9]{7}" backlog/ && echo "REVIEW NEEDED"
  ```

- `--notes` and `--plan` bare *silently replace* the whole section and destroy another session's
  writes. Use `--append-notes` / `--append-plan`. A `PreToolUse` hook denies the bare forms,
  including quoted.
- Finalize in one call so an interrupted session cannot leave finished work looking unfinished:
  `backlog task edit FMO-0001 --check-ac 1 --check-ac 2 -s Done`.
- Hand-editing task markdown breaks the HTML-comment section markers, which drops the section
  silently at exit code 0 and stays invisible until the next write destroys it. There is no repair
  command; `backlog doctor` only fixes duplicate task ids.
- Never let two sessions edit the same task. The v1.50.x concurrency fix covers the edit funnel
  but not reorder, draft saves, the TUI path, `doc update` or decision updates.
- `backlog config.yml` is the one file you may hand-edit; list-valued keys cannot be set through
  `backlog config set`.

## GitHub issues: always pass `-R`

Two remotes: `origin` is `rknightion/fleet-management-operator`, `upstream` is the fork source
`mbaykara/fleet-management-operator`. A bare `gh issue list` resolves to **upstream** and prints
nothing, which reads as an empty board rather than a wrong repo. Always
`-R rknightion/fleet-management-operator --limit 1000` (`gh issue list` defaults to 30).

## Task interface

`just check` is the gate. `just ci` adds `test-e2e`, which needs a Docker daemon and creates or
reuses the Kind cluster `fm-crd-test-e2e`. `just test` takes a filter mapping to `go test -run`.
`just docs` regenerates `docs/{flags,metrics,events,samples}.md`, `docs/api-reference.md` and the
chart README from source, including the per-controller Kubernetes event table; never hand-maintain
those. Cluster-mutating recipes are `[confirm]`-gated: run `just` with stdin from `/dev/null` and
ask rather than passing `--yes` or `JUST_YES=1`. `just --show <recipe>` prints what a recipe
actually runs.

## Fleet Management API

Base URL shape: `https://fleet-management-<CLUSTER_NAME>.grafana.net/pipeline.v1.PipelineService/`.
Basic auth, username is the stack id and password is a Cloud access token.

- **`UpsertPipeline` and `UpdatePipeline` are not selective. Unset fields are removed, not
  preserved.** Always send every spec field. Omit `matchers` and they are deleted.
- `UpsertPipeline` returns the full pipeline object; use it for status instead of a second
  `GetPipeline`. Never call `ListPipelines` on a normal reconcile - it spends rate budget.
- `validate_only: true` gives a dry run.
- Rate limiter defaults: `--fleet-api-rps` 3 (match your stack's server-side `api:` setting,
  Helm `fleetManagement.apiRatePerSecond`) and `--fleet-api-burst` 50
  (`fleetManagement.apiRateBurst`). **burst=1 livelocks:** with a 30s HTTP timeout, request
  number rps*30+1 in a restart wave waits the full timeout and fails in a way indistinguishable
  from a Fleet outage. Construct with `fleetclient.WithRateLimit(rps, burst)` and
  `limiter.Wait(ctx)` before each call.
- Pipeline names are unique across an entire Fleet Management org, not per namespace. See
  `--pipeline-name-scope` and `docs/runbooks/pipeline-name-scope-migration.md`.
- Collectors poll roughly every 5m, so nothing here is instant.
- Matchers are AND-ed, capped at 200 characters each, and several pipelines may match one
  collector.

## ConfigType

`ConfigType` must match both the configuration syntax and the target collector type; a mismatch
breaks the collector rather than failing the API call. Validate before the API call.

- `Alloy` (default) maps to API `CONFIG_TYPE_ALLOY`: River/HCL component blocks
  (`prometheus.scrape "default" { }`). Must not start with `receivers:`.
- `OpenTelemetryCollector` maps to API `CONFIG_TYPE_OTEL`: YAML with a `service` section.

The admission webhook enforces this plus matcher syntax (`=`, `!=`, `=~`, `!~`; `==` is the common
mistake), the 200-character matcher cap and non-empty contents. Setup: `docs/webhook-setup.md`.

## Controller invariants

- Status writes use `Status().Update()`, never `Update()`.
- On `IsConflict` from `Status().Update()`, return `ctrl.Result{Requeue: true}, nil` with **no**
  error. A conflict is cache lag; returning an error adds workqueue backoff for nothing.
- **`SyncPeriod` is deliberately unset** in `ctrl.Options`, and `internal/controller/watch_audit_test.go`
  asserts it stays unset. An explicit resync triggers full reconcile storms against the Fleet rate
  budget. Use watch events and status-driven `RequeueAfter`.
- **Pipeline and Collector must stay at `MaxConcurrentReconciles` 1** (the controller-runtime
  default, left unset in `cmd/main.go`). They share the Fleet API rate budget, so parallelising
  only queues more work at the limiter. Policy, sync and the discovery controllers are safe to
  parallelise and are configurable: `--controller-policy-max-concurrent` (4),
  `--controller-sync-max-concurrent` (4), `--controller-discovery-max-concurrent` (1),
  `--controller-pipeline-discovery-max-concurrent` (1), Helm `controllers.*.maxConcurrent`.
- The Pipeline finalizer is `pipeline.fleetmanagement.grafana.com/finalizer`. **Add it before the
  first Fleet API call** so a crash between the two leaves the CR protected rather than leaking an
  external resource, and **remove it only after Fleet cleanup succeeds or returns 404.** That
  ordering is the only leak-free window.
- Skip work with `status.observedGeneration` on Pipeline. The informer cache has no
  read-your-writes consistency, so do not infer state from a read straight after a write.
- Return errors; do not swallow them behind `Requeue: true`.
- Conditions: `Ready` (synced to Fleet), `Synced` (last reconcile succeeded), `ValidationError`.

Interfaces are declared in the consumer package (the controller), not the provider
(`pkg/fleetclient`), and asserted at compile time with `var _ Interface = &Struct{}`.

## Security model

`docs/security.md` holds the full trust model. When touching RBAC, webhooks, or the discovery and
external-sync controllers:

- Creating any `fleetmanagement.grafana.com` CR is **effectively privileged**. Pipelines write
  through the shared org-wide Fleet credential, and a `PipelineDiscovery` or `CollectorDiscovery`
  with `spec.targetNamespace` makes the operator create CRs in other namespaces - a confused
  deputy.
- Cluster-wide `secrets` read is granted only when `externalAttributeSync` is enabled;
  cross-namespace `secretRef` is blocked at admission and at reconcile.
- The chart ships **no** aggregated user roles by default. `<release>-editor` / `-viewer` exist
  behind `rbac.userRoles.create`; aggregating them into the built-in `edit` role re-opens the
  confused deputy cluster-wide.
- `TenantPolicy` is a guardrail, not an authorization boundary.

## Credentials

The chart creates `<fullname>-credentials` (default release gives
`fleet-management-operator-credentials`) with keys `base-url`, `username`, `password`, read by the
manager as `FLEET_MANAGEMENT_BASE_URL`, `FLEET_MANAGEMENT_USERNAME` and
`FLEET_MANAGEMENT_PASSWORD`. `fleetManagement.existingSecret` is a plain string naming a
pre-existing Secret; rename its keys with `fleetManagement.existingSecretKeys`.

## Deeper references

- `reference/attributes-and-discovery.md` - read before changing the Collector,
  RemoteAttributePolicy, ExternalAttributeSync, CollectorDiscovery or TenantPolicy controllers,
  their webhooks, or the attribute merge/diff path. Carries the single-writer rule, the precedence
  order, the opt-in flag map and the discovery naming and pagination traps.
- `.claude/skills/fleet-api/SKILL.md` - full Fleet Management API surface: endpoints, request and
  response shapes, error codes.
- `.claude/skills/controller-patterns/SKILL.md` - controller-runtime patterns and Go idioms behind
  the invariants above.
- `docs/flags.md` - every manager flag with its default, generated from source.
- `api/v1alpha1/`, `internal/controller/`, `pkg/` and `charts/fleet-management-operator/` each
  carry their own agent instructions. A session launched at the repo root does not load them; read
  the one for the area you are changing.
- Alloy config syntax: https://grafana.com/docs/alloy/latest/
- OTel collector config syntax: https://opentelemetry.io/docs/collector/configuration/

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
