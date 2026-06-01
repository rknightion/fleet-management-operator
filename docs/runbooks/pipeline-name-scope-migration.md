# Runbook: enabling Pipeline name scoping

Name scoping prefixes each Fleet Management pipeline name with `<namespace>.` so
that `Pipeline` CRs in different Kubernetes namespaces cannot collide on a shared,
org-wide Fleet name. It is **opt-in and default-off**; existing installs are
unaffected until you turn it on.

## What it does

- **Default (`none`):** the Fleet pipeline name is `spec.name` (or `metadata.name`
  when `spec.name` is empty), exactly as before.
- **`namespace`:** the Fleet pipeline name becomes `<namespace>.<base>`.
  Discovered pipelines (those carrying
  `fleetmanagement.grafana.com/fleet-pipeline-id`) and read-only pipelines keep
  their Fleet-assigned name and are never prefixed.

Configure the cluster default with the manager flag `--pipeline-name-scope`
(Helm: `controllers.pipeline.nameScope`). Override per-Pipeline with the
annotation `fleetmanagement.grafana.com/name-scope: namespace|none`.

## The migration (auto, delete-and-recreate)

Fleet's upsert is **name-keyed**: changing the name would create a *second*
pipeline and orphan the original (which keeps being distributed to collectors).
To avoid that, when the controller sees that an already-synced pipeline's desired
name has changed (tracked via `status.syncedName`), it:

1. **dry-runs** the new name (`validate_only`) — if that fails, it aborts without
   deleting anything and surfaces the error;
2. **deletes** the old pipeline by its server `status.id` (a 404 is treated as
   success);
3. **recreates** it under the new, scoped name and records the new `status.id`
   and `status.syncedName`.

Each migration emits a `Migrated` Kubernetes event on the Pipeline and increments
the `fleet_pipeline_name_migrations_total` metric. The sequence is crash-safe: an
interrupted migration converges on the next reconcile.

## Enabling it safely

1. **Pick a quiet window.** Switching the cluster default to `namespace` migrates
   *every* managed (non-discovered, non-read-only) pipeline. Each migration costs
   three Fleet API calls (dry-run + delete + create) and is bounded by the shared
   rate limiter (default 3 req/s), so a large fleet migrates gradually rather than
   instantly.
2. **(Optional) stage with the annotation.** Set
   `fleetmanagement.grafana.com/name-scope: namespace` on a few Pipelines first to
   validate the behavior before flipping the cluster default.
3. **Flip the default:** set `controllers.pipeline.nameScope: namespace` (or
   `--pipeline-name-scope=namespace`) and roll out.
4. **Watch:** `kubectl get events --field-selector reason=Migrated`, the
   `fleet_pipeline_name_migrations_total` metric, and each Pipeline's
   `status.syncedName` settling on the prefixed name.

## Rollback

Set the scope back to `none`. The controller detects that the desired name
reverted and migrates each pipeline back to its unprefixed name using the same
dry-run -> delete -> recreate sequence.

## Known limitation: opted-out dotted names

In a cluster whose default is `namespace`, a Pipeline that explicitly opts out
(`fleetmanagement.grafana.com/name-scope: none`) may **not** use a `spec.name`
that begins with `<label>.` (for example `prod.metrics`). Such a name could
collide with another namespace's scoped name, so the admission webhook rejects
it. Use a non-dotted name, or leave the pipeline scoped.
