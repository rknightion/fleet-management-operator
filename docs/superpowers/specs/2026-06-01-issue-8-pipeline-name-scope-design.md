# Design: Optional namespace-scoped Fleet pipeline naming + spec.name validation

Resolves [#8](https://github.com/rknightion/fleet-management-operator/issues/8).
Follow-up to the closed PR #3 ("Harden pipeline naming to namespace scope"),
which was rejected for being a silent breaking change (rename-on-upgrade orphaned
existing Fleet pipelines).

## 1. Goals

1. Make namespace-scoped pipeline naming **opt-in** and **default-off** so existing
   installs see no behavior change.
2. Add **webhook validation of `spec.name`** (length, illegal characters,
   reserved-prefix impersonation) — independently valuable, ships first.
3. When scoping is enabled, **migrate existing pipelines safely** (no orphans, no
   duplicate-name collector config) via controller-driven delete-and-recreate.
4. Never touch the names of **discovered / read-only** pipelines (Fleet-assigned).
5. Update field docs, both samples, `docs/api-reference.md`, `docs/flags.md`, and
   ship a migration runbook.

## 2. Background and hard constraints

Verified against the code and the Fleet API proto (`fleet-management-api@v1.2.0`):

- **Fleet pipeline `name` is the unique identifier** and has **no charset or
  length constraint** in the API (`pipeline.proto:21` — `string name = 1`, no
  `buf.validate`). Any naming rules we add are operator-imposed; we therefore must
  not reject names existing installs already use.
- **`UpsertPipeline` is name-keyed.** `buildUpsertRequest`
  (`internal/controller/pipeline_controller.go:352`) sends `Name/Contents/Matchers/
  Enabled/ConfigType` and **no `id`**. Upserting a changed name creates a *new*
  Fleet pipeline (new server ID) and leaves the old one orphaned and still
  distributed to collectors. This is exactly the #3 failure.
- **Delete and Get are by server ID.** `DeletePipelineRequest`/`GetPipelineRequest`
  key on `id` (`pipeline.proto`), and the client sends `status.ID`
  (`pkg/fleetclient/client.go:219`). 404 on delete is treated as success. This is a
  *safety advantage* for migration: we can delete exactly the pipeline we created,
  by ID, with no risk of deleting a same-named stranger.
- **`validate_only` exists** on Upsert (`UpsertPipelineRequest.ValidateOnly`) — a
  server-side dry run. We use it to de-risk migration.
- **Carve-out signal.** `PipelineDiscovery` always stamps
  `fleetmanagement.grafana.com/fleet-pipeline-id` on the CRs it creates
  (`pipeline_discovery_controller.go:314`); adopt-mode imports flow through
  `buildUpsertRequest`, read-only imports do not (`isReadOnly`,
  `pipeline_controller.go:755`). Both must keep their Fleet-assigned names.
- **Rate limit** is the shared 3 req/s budget; `MaxConcurrentReconciles` for
  Pipeline stays at 1.

## 3. Decisions

| Axis | Decision |
|------|----------|
| Opt-in mechanism | **Cluster-wide flag sets the default + per-CR annotation override.** |
| Existing pipelines | **Auto-migrate**: controller deletes the old Fleet pipeline by ID and recreates it under the scoped name. |
| Validation sequencing | **Phase 1 ships `spec.name` validation independently**; Phase 2 adds scoping + migration. |
| Separator | `.` (dot), matching #3's `fmt.Sprintf("%s.%s", namespace, name)` and Fleet's dot-tolerant identifier convention. Kubernetes namespaces are single DNS-1123 labels (no dots), so the first dot-segment of a scoped name is unambiguously the namespace. |

## 4. Phase 1 — `spec.name` webhook validation (standalone)

Lives entirely in `api/v1alpha1/pipeline_webhook.go`; no flag, no controller change.
Closes the validation gap regardless of whether scoping is ever enabled.

`validatePipelineName()` is added to the `validatePipeline()` chain and runs only
when `spec.name` is non-empty (empty means "use `metadata.name`", which is already
DNS-1123-constrained by Kubernetes):

- **Length:** reject `> 253` characters. Operator-imposed sane cap (Fleet imposes
  none). Also enforced at the API-server via `+kubebuilder:validation:MaxLength=253`
  on `PipelineSpec.Name`, double-checked by the webhook (mirrors the matcher
  pattern in `pipeline_types.go:178`).
- **Whitespace / control characters:** reject leading/trailing whitespace, any
  embedded whitespace, and any control character (`\x00-\x1f`, `\x7f`). These never
  appear in legitimate names, so this is non-breaking.
- **Empty-when-set:** reject a value that is whitespace-only.

The charset rule is deliberately **permissive** (printable, non-whitespace): Fleet
allows arbitrary names and existing installs may use dots, slashes, underscores,
hyphens, or mixed case. The security-relevant restriction (reserved-prefix
impersonation) is intentionally **not** in Phase 1 because it depends on the
cluster scope setting introduced in Phase 2.

Docs: update the `PipelineSpec.Name` godoc, both `config/samples/*pipeline*` files,
and regenerate `docs/api-reference.md`. Table-driven webhook tests cover valid
names, over-length, whitespace, control chars, and empty-string (still allowed).

## 5. Phase 2 — opt-in namespace scoping + auto-migration

### 5.1 Configuration surface

- **Flag:** `--pipeline-name-scope` (string enum `none` | `namespace`, default
  `none`). Wired into a new `PipelineReconciler.NameScope` field in `cmd/main.go`
  (mirrors `externalSourceSecretLabelSelector` wiring, `cmd/main.go:153,612`).
- **Helm:** `controllers.pipeline.nameScope` (default `none`) rendered onto the
  manager args; documented in `docs/flags.md` via `make docs`.
- **Per-CR annotation override:** `fleetmanagement.grafana.com/name-scope` with
  values `namespace` or `none`. Precedence: **annotation > flag default**.
  Effective scope per CR is computed by a pure helper `effectiveNameScope(pipeline,
  defaultScope)`.

### 5.2 Name computation

Refactor the inline name logic in `buildUpsertRequest` into a pure function:

```
func desiredFleetName(pipeline, scope) string {
    base := pipeline.Spec.Name; if base == "" { base = pipeline.Name }
    if scope != "namespace"            { return base }   // default / opted out
    if isDiscovered(pipeline)          { return base }   // FleetPipelineIDAnnotation present
    if isReadOnly(pipeline)            { return base }   // observed, never upserted
    return pipeline.Namespace + "." + base
}
```

`isDiscovered` checks for the `fleet-pipeline-id` annotation. Both carve-outs keep
Fleet-assigned names, preventing adopt-mode imports from being re-keyed into
duplicates.

### 5.3 Status: track the synced name

Add `PipelineStatus.SyncedName string` — the name under which the pipeline
currently exists in Fleet. Set on every successful upsert.

**Backfill for upgrades:** on the first reconcile under the new version, if
`status.ID != "" && status.SyncedName == ""`, set `SyncedName` to the *unscoped*
base (`spec.Name || metadata.name`). That is exactly the name the old code produced,
so it correctly represents the current Fleet name. This backfill runs **before**
migration detection in the same reconcile, so an existing pipeline migrates on its
first post-upgrade reconcile when scoping is on, and is a no-op when scoping is off
(`desired == base == SyncedName`).

### 5.4 Auto-migration algorithm

In `reconcileNormal`, compute `desired := desiredFleetName(...)`. Migration is
required iff `status.ID != "" && status.SyncedName != "" && desired !=
status.SyncedName` (the carve-outs already force `desired == base`, so discovered/
read-only CRs never migrate). When required:

1. **Dry run:** `UpsertPipeline(validate_only=true)` with the new name. On error,
   abort migration (do *not* delete), emit a Warning event, return the error to
   requeue. This guarantees we only delete when the recreate will succeed.
2. **Delete old:** `DeletePipeline(status.ID)` (by ID; 404 = success).
3. **Recreate:** `UpsertPipeline(validate_only=false)` with the new name → new ID.
4. **Status:** `ID = newID`, `SyncedName = desired`, plus the usual conditions.
5. Emit a `Migrated` event and increment a `pipeline_name_migrations_total` metric.

Ordering rationale: delete-before-recreate avoids a window where two
differently-named pipelines with identical matchers/contents both match a collector
(which for Alloy would be a duplicate-component error). The `validate_only` pre-check
shrinks the "deleted but not yet recreated" window to a transient network failure
between steps 2 and 3; that state self-heals on requeue (`delete` returns 404 =
success, then recreate). Cost is 3 rate-limited calls per migrating pipeline.

**Crash-safety:** every step is idempotent and convergent. A crash after step 2
leaves `status.ID` pointing at a deleted pipeline; next reconcile re-detects
migration, `delete` 404s as success, recreate proceeds. A crash after step 3 before
4 leaves the new pipeline created but status stale; next reconcile recreates
idempotently (name-keyed) and updates status.

### 5.5 Webhook: reserved-prefix impersonation guard

The annotation **opt-out** (`name-scope: none` while the cluster default is
`namespace`) is the only way to produce an unscoped name in a scoping cluster, so it
is the only impersonation vector. The Pipeline validator gains a `nameScopeDefault`
field (set from the flag in `cmd/main.go`, like the tenant checker). When the
effective scope is `none` **and** the cluster default is `namespace`, reject a
`spec.name` matching `^[a-z0-9]([-a-z0-9]*[a-z0-9])?\.` (looks like
`<namespace-label>.…`) so an opted-out CR cannot squat on another namespace's scoped
name. When scope is `namespace`, no restriction is needed — the operator prepends
the CR's own namespace, so impersonation is impossible. The validator also rejects
unknown annotation values.

This guard is Phase 2 only (it needs the flag) and does not affect default
installs (`nameScopeDefault == none` ⇒ rule never fires).

**Known limitation (documented):** in a scoping cluster, an explicitly opted-out CR
cannot use a `spec.name` that begins with `<lowercase-label>.` (e.g.
`prometheus.scrape`), because such a name could collide with a scoped name. This
restriction applies only to the deliberately-opted-out minority in `namespace`-default
clusters; the runbook calls it out.

### 5.6 Migration runbook + docs

`docs/runbooks/pipeline-name-scope-migration.md`: what enabling the flag does, the
per-pipeline `Migrated` events and the `pipeline_name_migrations_total` metric to
watch, the rate-budget impact of a fleet-wide migration (bounded by 3 req/s — do it
in a deliberate window), how the annotation override works, and rollback (flip the
flag back triggers the reverse migration). Update `docs/security.md` to note scoping
as a defense against cross-namespace pipeline-name collisions, and the new annotation
in the CRD godoc + samples.

## 6. Invariants preserved

- **Single-writer:** unchanged — only the Pipeline reconciler upserts/deletes
  pipelines. Discovery still only creates/deletes CRs.
- **Discovered / read-only pipelines** never get scoped or migrated.
- **ObservedGeneration** short-circuit unchanged; migration is detected on the
  normal reconcile path (a scope/annotation change bumps generation or arrives via
  the flag at startup, both of which reconcile).
- **Default install** (`nameScope=none`, no annotations) is byte-for-byte identical
  to today: `desiredFleetName` returns the existing base, no migration, no new
  validation failures for previously-valid names.
- **Paused** pipelines (`spec.paused=true`) do not migrate until unpaused — migration
  lives on the normal reconcile path, which the existing Paused gate already
  short-circuits (verify the gate precedes `reconcileNormal` during implementation).
- The new `pipeline_name_migrations_total` metric is registered in
  `internal/controller/metrics.go`; the `Migrated`/migration-failure event reasons
  are added to the controller source and flow into `docs/events.md` via `make docs`.

## 7. Non-goals

- Global cross-tenant name-uniqueness enforcement (the operator's single-credential
  model does not need it; #8 explicitly de-scoped the takeover threat).
- Pagination of any list call; no list calls are added.
- Renaming via a Fleet "rename" API (none exists; name is the key).

## 8. Risks and mitigations

| Risk | Mitigation |
|------|------------|
| Flipping the flag on a large fleet triggers a migration storm | Bounded by the 3 req/s limiter; per-pipeline `Migrated` events + `pipeline_name_migrations_total` metric; runbook says enable deliberately. |
| Migration deletes a pipeline it then fails to recreate | `validate_only` dry-run before delete; transient failure self-heals on requeue. |
| Permissive charset misses a genuinely bad name | Accepted: Fleet imposes none and non-breaking is required; the security control is the reserved-prefix guard, not charset. |
| Annotation opt-out used to impersonate a scoped name | Reserved-prefix webhook guard rejects `<label>.` names for opted-out CRs in scoping clusters. |

## 9. Test plan

- **Phase 1 (webhook):** valid names; empty (allowed); over-253; leading/trailing/
  embedded whitespace; control chars.
- **Phase 2 (unit):** `effectiveNameScope` precedence; `desiredFleetName` for
  default / scoped / discovered / read-only; reserved-prefix guard fires only for
  opted-out CRs under a `namespace` default.
- **Phase 2 (controller, envtest + fake client):** first-create under scope; backfill
  of `SyncedName`; migration delete→recreate updates `status.ID`/`SyncedName`;
  dry-run failure aborts without deleting; discovered/read-only CRs never migrate;
  crash-safety convergence (simulate status not persisted, re-reconcile).
```
