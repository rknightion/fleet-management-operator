# Condition Registry

Cross-CRD reference for the condition types and reasons emitted by
`fleet-management-operator`. Use this table to write generic alerting
and dashboard queries (e.g. "any CR with `Ready=False` for >10m"
across the entire CRD family).

The source of truth is the `internal/controller/*.go` const blocks
listed under each subsystem; this document mirrors them. Anyone adding
a new condition type or reason must update this file in the same PR.

## Condition types

| CRD                       | Type      | Meaning                                                                              |
| ------------------------- | --------- | ------------------------------------------------------------------------------------ |
| `Pipeline`                | `Ready`   | Pipeline is fully synced to Fleet Management and serving the current spec.           |
| `Pipeline`                | `Synced`  | The most recent reconciliation completed without error.                              |
| `Collector`               | `Ready`   | The merged remote-attribute set is reflected in Fleet for this collector.            |
| `Collector`               | `Synced`  | The most recent reconciliation completed without error.                              |
| `RemoteAttributePolicy`   | `Ready`   | The selector matched at least one collector and status is up to date.                |
| `RemoteAttributePolicy`   | `Synced`  | The selector evaluated successfully (matched ≥0 collectors, no list error).          |
| `ExternalAttributeSync`   | `Ready`   | The most recent fetch produced a usable result that has been claimed.                |
| `ExternalAttributeSync`   | `Synced`  | The most recent reconciliation completed without error.                              |
| `ExternalAttributeSync`   | `Stalled` | An empty fetch was suppressed by `spec.allowEmptyResults=false`; previous claim kept. |
| `CollectorDiscovery`      | `Ready`   | Most recent `ListCollectors` succeeded and CR mirroring is up to date.               |
| `CollectorDiscovery`      | `Synced`  | The most recent reconciliation completed without error.                              |
| `TenantPolicy`            | `Ready`   | Mirrors `Valid`. Surfaced as the headline status for operators.                      |
| `TenantPolicy`            | `Valid`   | Spec parses cleanly: matcher syntax + namespace selector are well-formed.            |

## Reasons by CRD

Reason strings are short PascalCase identifiers. They are stable: a
reason name is never repurposed across releases. Renames go through an
additive-then-remove window.

### Pipeline (`internal/controller/pipeline_controller.go`)

| Reason             | Used on        | Meaning                                                              |
| ------------------ | -------------- | -------------------------------------------------------------------- |
| `Synced`           | Ready, Synced  | UpsertPipeline succeeded.                                            |
| `SyncFailed`       | Ready, Synced  | Fleet API call failed (network, 5xx, rate-limit).                    |
| `ValidationError`  | Ready, Synced  | Spec failed pre-API validation (configType/contents, matchers).      |
| `Deleting`         | Ready, Synced  | DeletionTimestamp set; finalizer running.                            |
| `DeleteFailed`     | Ready, Synced  | DeletePipeline returned an error other than 404.                     |

### Collector (`internal/controller/collector_controller.go`)

| Reason             | Used on        | Meaning                                                                                   |
| ------------------ | -------------- | ----------------------------------------------------------------------------------------- |
| `Synced`           | Ready, Synced  | Merged desired-state successfully written via BulkUpdateCollectors.                       |
| `SyncFailed`       | Ready, Synced  | Fleet API call failed.                                                                    |
| `NotRegistered`    | Ready, Synced  | Collector CR points at an ID that has not yet appeared in Fleet Management. Requeues.     |
| `ValidationError`  | Ready, Synced  | Spec fails server-side validation (e.g. reserved key prefix slipped past a stale schema). |
| `Deleting`         | Ready, Synced  | DeletionTimestamp set; finalizer running.                                                 |
| `DeleteFailed`     | Ready, Synced  | Cleanup of owned keys failed.                                                             |

### RemoteAttributePolicy (`internal/controller/policy_controller.go`)

| Reason       | Used on        | Meaning                                                                       |
| ------------ | -------------- | ----------------------------------------------------------------------------- |
| `Matched`    | Ready, Synced  | Selector matched at least one collector.                                      |
| `NoMatch`    | Ready, Synced  | Selector matched zero collectors. Synced is still True (eval succeeded).      |
| `ListFailed` | Ready, Synced  | List of Collector CRs failed (cache miss / API error).                        |

### ExternalAttributeSync (`internal/controller/external_sync_controller.go`)

| Reason            | Used on              | Meaning                                                                            |
| ----------------- | -------------------- | ---------------------------------------------------------------------------------- |
| `Synced`          | Ready, Synced        | Fetch returned records and the claim was applied.                                  |
| `SourceFailed`    | Ready, Synced        | Source `Fetch` returned a non-nil error.                                           |
| `Stalled`         | Stalled              | `Fetch` returned 0 records and `spec.allowEmptyResults=false`; claim preserved.    |
| `InvalidSchedule` | Ready, Synced        | `spec.schedule` failed both duration and cron parsers.                             |

### CollectorDiscovery (`internal/controller/collector_discovery_controller.go`)

| Reason                  | Used on        | Meaning                                                                       |
| ----------------------- | -------------- | ----------------------------------------------------------------------------- |
| `Synced`                | Ready, Synced  | ListCollectors succeeded; mirror CRs are up to date.                          |
| `ListCollectorsFailed`  | Ready, Synced  | Fleet API call failed.                                                        |
| `UpsertFailed`          | Ready, Synced  | Creating or updating a mirrored Collector CR failed.                          |
| `InvalidConfig`         | Ready, Synced  | Spec failed validation post-admission (e.g. malformed selector).              |

### TenantPolicy (`internal/controller/tenant_policy_controller.go`)

| Reason       | Used on        | Meaning                                                                       |
| ------------ | -------------- | ----------------------------------------------------------------------------- |
| `Valid`      | Ready, Valid   | Spec parses cleanly.                                                          |
| `ParseError` | Ready, Valid   | Required-matcher syntax or namespace selector is malformed.                   |

## Conventions

- Reason values are stable PascalCase identifiers.
- `Ready=True` means the CR is fully usable. Partial success is
  `Ready=False` with a specific reason; no half-Ready states.
- Conditions are written via `meta.SetStatusCondition` from
  `k8s.io/apimachinery/pkg/api/meta` so `LastTransitionTime` is
  managed correctly.
- Conditions are persisted via `Status().Update()` only — never
  `Update()`.
- `ObservedGeneration` on each condition reflects the spec generation
  observed when the condition was set; status-level
  `observedGeneration` is updated alongside.
- Event reasons (emitted via `record.EventRecorder`) intentionally
  mirror condition reasons where appropriate but are tracked
  separately in each controller's `*EventReason*` consts. Event
  reasons are not part of this registry — they are user-facing log
  text, not API surface.

## Adding a new condition type or reason

1. Add the const to the relevant `internal/controller/*.go` file.
2. Add the row to the table above in this file.
3. If a new condition type, mention it in the relevant CRD's
   `Status.Conditions` godoc on `api/v1alpha1/*_types.go`.
4. If alerting / dashboards live elsewhere (Grafana Cloud, etc.),
   note the new reason in the relevant runbook so on-call knows what
   it means.
