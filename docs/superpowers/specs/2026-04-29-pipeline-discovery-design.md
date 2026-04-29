# PipelineDiscovery Design

## Problem

Pipelines created outside Kubernetes (via CLI, Terraform, or Git) have no
corresponding Pipeline CR. Operators cannot manage or inspect them via kubectl
without manual CR creation.

## Solution

A `PipelineDiscovery` CRD that periodically polls Fleet Management's `ListPipelines`
and creates `Pipeline` CRs for each discovered pipeline.

## Key Design Decisions

### ImportMode: Adopt vs ReadOnly

- `Adopt` (default): discovered Pipeline CRs are immediately reconciled by the
  Pipeline controller. Direct Fleet edits are overwritten on the next reconcile.
- `ReadOnly`: discovered Pipeline CRs have `spec.paused=true`. The Pipeline controller
  skips reconciliation. Users promote individual pipelines by adding the annotation
  `fleetmanagement.grafana.com/import-mode=adopt`.

This allows bulk discovery with selective adoption rather than all-or-nothing.

### spec.paused on Pipeline CRD

Added `spec.paused: bool` to Pipeline. When true, the Pipeline controller skips
the reconcile loop (after finalizer handling), making the CR read-only from an
operator perspective. The per-pipeline annotation overrides this for individual
promotion without requiring a spec edit.

### Selector

Only `configType` and `enabled` filters, matching what `ListPipelinesRequest` supports
server-side. No name/pattern filtering to keep the implementation simple. A broad
selector (no filters) discovers all pipelines.

### Conflict Detection

When PipelineDiscovery computes a CR name for a Fleet pipeline and a Pipeline CR with
that name already exists, it records a conflict and skips creation. This naturally
handles pipelines already managed by the operator (which have existing Pipeline CRs).
Conflict reasons: `NotOwnedByDiscovery`, `OwnedByOtherDiscovery`, `NameSanitizationFailed`.

### Name Sanitization

Fleet pipeline names (arbitrary strings) are converted to DNS-1123 labels using the
existing `internal/controller/discovery.SanitizedName` and `HashedName` functions.
The original name is stored in the `fleetmanagement.grafana.com/fleet-pipeline-id`
annotation for reverse lookup.

### Single-Writer Principle

PipelineDiscovery only manages Pipeline CR lifecycle (create/delete). It never calls
Fleet Management APIs directly (no UpsertPipeline). The Pipeline controller remains
the sole writer to Fleet. This avoids write races.

### Lifecycle on Removal

When a pipeline disappears from `ListPipelines`:
- `onPipelineRemoved=Keep` (default): Pipeline CR stays with stale annotation
- `onPipelineRemoved=Delete`: Pipeline CR is deleted; the Pipeline finalizer handles
  Fleet cleanup (404 = success for vanished pipelines)

## Architecture

```
PipelineDiscovery CR
      | polls ListPipelines every pollInterval
      v
Fleet Management API --> creates Pipeline CRs (paused if ReadOnly)
                                |
                                v
                    Pipeline controller
                    (skips if paused, unless adopt annotation)
                    (reconciles to Fleet if adopted)
```

## CRD Fields

See `api/v1alpha1/pipeline_discovery_types.go` for the full spec.

Short name: `fmpd`

## Opt-In

Default-off: `--enable-pipeline-discovery-controller` flag required.
Helm: `controllers.pipelineDiscovery.enabled: true`.
