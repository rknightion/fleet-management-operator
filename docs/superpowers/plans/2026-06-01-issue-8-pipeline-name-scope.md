# Optional namespace-scoped pipeline naming + spec.name validation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (inline) or superpowers:subagent-driven-development to implement task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Make Fleet pipeline naming optionally namespace-scoped (opt-in, default-off) with safe auto-migration of existing pipelines, and add `spec.name` webhook validation.

**Architecture:** Phase 1 adds standalone `spec.name` validation in the Pipeline webhook. Phase 2 adds a `--pipeline-name-scope` flag + per-CR annotation override, a pure `desiredFleetName` helper with discovery/read-only carve-outs, a new `status.syncedName`, and a controller-driven validate→delete→recreate migration keyed on the server-assigned `status.ID`.

**Tech Stack:** Go, controller-runtime, kubebuilder webhooks, connectrpc Fleet client, testify table tests, envtest.

Spec: `docs/superpowers/specs/2026-06-01-issue-8-pipeline-name-scope-design.md`.

---

## File structure

| File | Responsibility | Phase |
|------|----------------|-------|
| `api/v1alpha1/pipeline_webhook.go` | `validatePipelineName` (P1) + reserved-prefix/annotation guard (P2) | 1,2 |
| `api/v1alpha1/pipeline_types.go` | `spec.Name` MaxLength marker + godoc; `status.SyncedName`; name-scope annotation const | 1,2 |
| `api/v1alpha1/pipeline_naming.go` | pure helpers: `EffectiveNameScope`, `DesiredFleetName`, `IsDiscovered`, scope consts + reserved-prefix regex | 2 |
| `api/v1alpha1/pipeline_naming_test.go` | unit tests for helpers | 2 |
| `internal/controller/pipeline_controller.go` | use `DesiredFleetName`; backfill + migration in `reconcileNormal`; set `SyncedName` | 2 |
| `internal/controller/pipeline_migration_test.go` | migration + backfill controller tests | 2 |
| `internal/controller/metrics.go` | `pipeline_name_migrations_total` | 2 |
| `cmd/main.go` | `--pipeline-name-scope` flag → `PipelineReconciler.NameScope` + validator default | 2 |
| `charts/.../templates/deployment.yaml`, `values.yaml`, `README.md.gotmpl` | `controllers.pipeline.nameScope` | 2 |
| `config/samples/*pipeline*`, `docs/api-reference.md`, `docs/flags.md`, `docs/events.md`, `docs/security.md`, `docs/runbooks/pipeline-name-scope-migration.md` | docs | 1,2 |

---

## PHASE 1 — spec.name validation (ships independently)

### Task 1: `validatePipelineName` webhook rule (TDD)

**Files:** Modify `api/v1alpha1/pipeline_webhook.go`; Test `api/v1alpha1/pipeline_webhook_test.go`.

- [ ] **Step 1 — failing tests.** Add cases to the Pipeline validate test table:
  empty name (allowed), normal name `my-pipeline` (allowed), dotted `a.b` (allowed in P1),
  253-char name (allowed), 254-char (rejected), leading space `" x"` (rejected),
  embedded space `"a b"` (rejected), tab/newline (rejected), control char `"a\x01"` (rejected).
- [ ] **Step 2 — run, expect FAIL.** `go test ./api/v1alpha1/... -run TestPipeline_Validate -v`
- [ ] **Step 3 — implement.** Add `validateName()` and call it from `validatePipeline()`:

```go
func (r *Pipeline) validateName() error {
    name := r.Spec.Name
    if name == "" { return nil } // empty -> metadata.name (already DNS-1123)
    if len(name) > maxPipelineNameLength {
        return fmt.Errorf("spec.name must be at most %d characters, got %d", maxPipelineNameLength, len(name))
    }
    if strings.TrimSpace(name) != name || strings.ContainsAny(name, " \t\r\n\v\f") {
        return fmt.Errorf("spec.name must not contain whitespace")
    }
    for _, r := range name {
        if r < 0x20 || r == 0x7f {
            return fmt.Errorf("spec.name must not contain control characters")
        }
    }
    return nil
}
```
  with `const maxPipelineNameLength = 253`. Insert the call as step "0" of `validatePipeline`.
- [ ] **Step 4 — run, expect PASS.** Same command.
- [ ] **Step 5 — commit.** `git commit -m "feat(webhook): validate pipeline spec.name (#8)"`

### Task 2: CRD marker + docs for spec.name

**Files:** Modify `api/v1alpha1/pipeline_types.go` (godoc + `+kubebuilder:validation:MaxLength=253` on `Name`); samples; regenerate.

- [ ] **Step 1.** Add marker + expand godoc on `PipelineSpec.Name` (note uniqueness, validation, that empty uses metadata.name).
- [ ] **Step 2.** `make manifests && cp config/crd/bases/fleetmanagement.grafana.com_pipelines.yaml charts/fleet-management-operator/crds/` then `make docs`.
- [ ] **Step 3.** Update both `config/samples/*pipeline*` name comments (drop the misleading "no hyphens"; state the real rules).
- [ ] **Step 4 — verify.** `go build ./... && make api-docs-check` (exit 0); `git diff --stat` shows only expected files.
- [ ] **Step 5 — commit.** `git commit -m "docs(crd): document and bound pipeline spec.name (#8)"`

---

## PHASE 2 — opt-in scoping + auto-migration

### Task 3: `status.SyncedName` field

**Files:** Modify `api/v1alpha1/pipeline_types.go`; regenerate deepcopy + CRD.

- [ ] **Step 1.** Add to `PipelineStatus`:
```go
// SyncedName is the pipeline name currently present in Fleet Management.
// Used to detect and migrate name changes when name scoping is toggled.
// +optional
SyncedName string `json:"syncedName,omitempty"`
```
- [ ] **Step 2.** `make generate && make manifests && cp ... charts crds && make docs`.
- [ ] **Step 3 — verify build.** `go build ./...`
- [ ] **Step 4 — commit.** `git commit -m "feat(api): add Pipeline status.syncedName (#8)"`

### Task 4: naming helpers (TDD)

**Files:** Create `api/v1alpha1/pipeline_naming.go` + `pipeline_naming_test.go`; add annotation const to `pipeline_types.go`.

- [ ] **Step 1 — failing tests** for:
  - `EffectiveNameScope`: no annotation → default; annotation `namespace`/`none` overrides default; unknown annotation → default (validation rejects separately).
  - `DesiredFleetName`: scope none → base; scope namespace + plain CR → `ns.base`; scope namespace + discovered (fleet-pipeline-id annotation) → base; scope namespace + read-only → base; empty spec.name uses metadata.name.
  - `IsDiscovered`: true iff `FleetPipelineIDAnnotation` present.
- [ ] **Step 2 — run, expect FAIL** (undefined). `go test ./api/v1alpha1/... -run TestNaming -v`
- [ ] **Step 3 — implement** `pipeline_naming.go`:
```go
const (
    NameScopeNone      = "none"
    NameScopeNamespace = "namespace"
    PipelineNameScopeAnnotation = "fleetmanagement.grafana.com/name-scope"
)
func IsDiscovered(p *Pipeline) bool { _, ok := p.Annotations[FleetPipelineIDAnnotation]; return ok }
func EffectiveNameScope(p *Pipeline, def string) string {
    switch p.Annotations[PipelineNameScopeAnnotation] {
    case NameScopeNamespace: return NameScopeNamespace
    case NameScopeNone:      return NameScopeNone
    default:                 return def
    }
}
func DesiredFleetName(p *Pipeline, def string) string {
    base := p.Spec.Name; if base == "" { base = p.Name }
    if EffectiveNameScope(p, def) != NameScopeNamespace { return base }
    if IsDiscovered(p) || isReadOnly(p) { return base }
    return p.Namespace + "." + base
}
```
  (Move/duplicate a small `isReadOnly` predicate into the api package, or gate on the import-mode annotation directly to avoid an import cycle — the controller's `isReadOnly` is in `internal/`.) Use the annotation directly here:
  `readOnly := p.Annotations[PipelineImportModeAnnotation] == PipelineImportModeAnnotationReadOnly`.
- [ ] **Step 4 — run, expect PASS.**
- [ ] **Step 5 — commit.** `git commit -m "feat(api): pipeline name-scope helpers (#8)"`

### Task 5: flag + Helm wiring

**Files:** `cmd/main.go`; chart `values.yaml`, `templates/deployment.yaml`, `README.md.gotmpl`.

- [ ] **Step 1.** `cmd/main.go`: `var pipelineNameScope string`; `flag.StringVar(&pipelineNameScope, "pipeline-name-scope", "none", "Pipeline Fleet-name scope: none (default) or namespace")`; validate value ∈ {none,namespace} at startup (fatal otherwise); set `NameScope: pipelineNameScope` on the `PipelineReconciler` (line ~551) and pass as the validator default (Task 8).
- [ ] **Step 2.** `PipelineReconciler` struct: add `NameScope string`.
- [ ] **Step 3.** Chart: `controllers.pipeline.nameScope: none` value; render `--pipeline-name-scope={{ . }}` arg; `make docs` (chart README + flags.md).
- [ ] **Step 4 — verify.** `go build ./...`; `helm template` smoke (or `make lint`).
- [ ] **Step 5 — commit.** `git commit -m "feat(cmd,chart): --pipeline-name-scope flag (#8)"`

### Task 6: use DesiredFleetName + backfill SyncedName

**Files:** `internal/controller/pipeline_controller.go`.

- [ ] **Step 1 — test (write with Task 7's).** Covered by migration tests.
- [ ] **Step 2.** In `buildUpsertRequest`, replace the inline name logic with
  `pipelineName := fleetmanagementv1alpha1.DesiredFleetName(pipeline, r.NameScope)`.
- [ ] **Step 3.** In `updateStatusSuccess` (and read-only/ migration status writers), set
  `pipeline.Status.SyncedName = apiPipeline.Name` (the name Fleet echoed back) alongside `Status.ID`.
- [ ] **Step 4.** Add a backfill at the top of `reconcileNormal`:
  `if pipeline.Status.ID != "" && pipeline.Status.SyncedName == "" { pipeline.Status.SyncedName = unscopedBase(pipeline) }`
  (in-memory; persisted by the subsequent status update).
- [ ] **Step 5 — commit** with Task 7.

### Task 7: auto-migration (TDD)

**Files:** `internal/controller/pipeline_controller.go`; `metrics.go`; Test `internal/controller/pipeline_migration_test.go`.

- [ ] **Step 1 — failing tests** (fake FleetClient recording calls):
  - first create under `NameScope=namespace`: name == `ns.base`, no delete.
  - existing pipeline (status.ID set, SyncedName=`base`), flag namespace: expect validate(dry-run) → delete(oldID) → upsert(`ns.base`); status.ID=newID, SyncedName=`ns.base`.
  - dry-run returns error: expect NO delete, reconcile returns error.
  - discovered CR (fleet-pipeline-id annotation), namespace scope: no migration, name unchanged.
  - crash-safety: pre-set status.ID=oldID, SyncedName=base, make delete return 404 → still upserts and converges.
- [ ] **Step 2 — run, expect FAIL.** `go test ./internal/controller/... -run TestPipelineMigration -v`
- [ ] **Step 3 — implement** in `reconcileNormal` before the normal upsert:
```go
desired := fleetmanagementv1alpha1.DesiredFleetName(pipeline, r.NameScope)
if pipeline.Status.ID != "" && pipeline.Status.SyncedName != "" && desired != pipeline.Status.SyncedName {
    // dry-run the recreate first; only delete if it will succeed
    dry := r.buildUpsertRequest(pipeline); dry.ValidateOnly = true
    if _, err := r.FleetClient.UpsertPipeline(ctx, dry); err != nil {
        r.emitEventf(pipeline, corev1.EventTypeWarning, eventReasonMigrate, "migration dry-run failed: %v", err)
        return r.handleAPIError(ctx, pipeline, err, outcome)
    }
    if err := r.FleetClient.DeletePipeline(ctx, pipeline.Status.ID); err != nil {
        return r.handleAPIError(ctx, pipeline, err, outcome)
    }
    metrics.PipelineNameMigrations.Inc()
    r.emitEventf(pipeline, corev1.EventTypeNormal, eventReasonMigrate, "migrating pipeline name %q -> %q", pipeline.Status.SyncedName, desired)
    pipeline.Status.ID = "" // old gone; force create path
}
```
  Then the existing upsert runs with the new name; `updateStatusSuccess` sets ID + SyncedName.
- [ ] **Step 4 — add metric** in `metrics.go`: `PipelineNameMigrations = prometheus.NewCounter(..."pipeline_name_migrations_total"...)` and register it; add `eventReasonMigrate = "Migrated"`.
- [ ] **Step 5 — run, expect PASS.**
- [ ] **Step 6 — commit.** `git commit -m "feat(controller): auto-migrate pipelines on name-scope change (#8)"`

### Task 8: reserved-prefix + annotation webhook guard (TDD)

**Files:** `api/v1alpha1/pipeline_webhook.go`; `cmd/main.go` wiring; Test `pipeline_webhook_test.go`.

- [ ] **Step 1 — failing tests:** validator with `nameScopeDefault=namespace`:
  CR annotation `name-scope: none` + name `prod.x` → rejected; same but name `plainname` → allowed;
  CR scope namespace (no annotation) + name `prod.x` → allowed (operator prepends ns);
  unknown annotation value `bogus` → rejected; `nameScopeDefault=none` + opted-out + `prod.x` → allowed (guard off).
- [ ] **Step 2 — run, expect FAIL.**
- [ ] **Step 3 — implement:** add `nameScopeDefault string` to the validator struct; in `validatePipeline` (needs the obj's effective scope) reject unknown annotation values always; when `effective==none && nameScopeDefault==namespace`, reject `spec.name` matching `^[a-z0-9]([-a-z0-9]*[a-z0-9])?\.`. Wire `nameScopeDefault` from the flag in `cmd/main.go` where the validator is registered.
- [ ] **Step 4 — run, expect PASS.**
- [ ] **Step 5 — commit.** `git commit -m "feat(webhook): reserved-prefix guard for opted-out pipelines (#8)"`

### Task 9: runbook + docs

**Files:** `docs/runbooks/pipeline-name-scope-migration.md` (new); `docs/security.md`; samples; regenerate `docs/events.md`, `docs/flags.md`, `docs/api-reference.md`.

- [ ] **Step 1.** Write the runbook (enable steps, `Migrated` events + `pipeline_name_migrations_total` to watch, 3 req/s budget caveat, annotation override, opted-out dotted-name limitation, rollback).
- [ ] **Step 2.** Add a cross-namespace name-collision note + the `name-scope` annotation to `docs/security.md` and the CRD godoc/samples.
- [ ] **Step 3.** `make docs`; verify `make api-docs-check` exit 0.
- [ ] **Step 4 — commit.** `git commit -m "docs: pipeline name-scope migration runbook + security note (#8)"`

---

## Self-review

- **Spec coverage:** #8 task1=Tasks 4–7 (opt-in flag+annotation, default off); task2(runbook)=Task 9; task3(spec.name validation)=Tasks 1–2 + reserved-prefix Task 8; task4(discovery carve-out)=Task 4 `IsDiscovered`/read-only in `DesiredFleetName` + Task 7 no-migrate; task5(docs)=Tasks 2,9. All covered.
- **Type consistency:** `DesiredFleetName(p, def)`, `EffectiveNameScope(p, def)`, `NameScopeNone/Namespace`, `PipelineNameScopeAnnotation`, `status.SyncedName`, `PipelineReconciler.NameScope`, `pipelineValidator.nameScopeDefault`, `metrics.PipelineNameMigrations`, `eventReasonMigrate` — consistent across tasks.
- **Placeholders:** none; helper and migration code shown in full.
- **Import-cycle check:** `DesiredFleetName` lives in `api/v1alpha1` and must not import `internal/`; it computes read-only via the import-mode annotation directly (noted in Task 4).

## Execution

Inline TDD in this session (user waived approval gates). Phase 1 → verify → Phase 2 → verify → adversarial review → PRs.
