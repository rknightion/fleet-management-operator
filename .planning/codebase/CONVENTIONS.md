# Coding Conventions

**Analysis Date:** 2026-02-08

## Naming Patterns

**Files:**
- Controllers: `*_controller.go` (e.g., `internal/controller/pipeline_controller.go`)
- Tests: `*_test.go` co-located with source (e.g., `internal/controller/pipeline_controller_test.go`)
- Webhooks: `*_webhook.go` (e.g., `api/v1alpha1/pipeline_webhook.go`)
- API types: `*_types.go` (e.g., `api/v1alpha1/pipeline_types.go`)
- Suites: `suite_test.go` for test environment setup

**Functions:**
- Exported: PascalCase (e.g., `NewClient`, `UpsertPipeline`, `ValidateCreate`)
- Unexported: camelCase (e.g., `reconcileNormal`, `handleAPIError`, `buildUpsertRequest`)
- Interface implementations use pointer receiver at compile-time verification point: `var _ Interface = &Struct{}`

**Variables:**
- Constants: UPPER_CASE or camelCase in const blocks (e.g., `pipelineFinalizer`, `conditionTypeReady`, `reasonSynced`)
- Receiver variables: short names like `r`, `m`, `c` for struct methods
- Package-level loggers: `setupLog`, `pipelinelog` with `.WithName()` pattern

**Types:**
- CRD types: Exported, follow Kubernetes naming (e.g., `Pipeline`, `PipelineSpec`, `ConfigType`)
- Enums: Use `const` blocks with exported values (e.g., `ConfigTypeAlloy = "Alloy"`)
- Interfaces: Consumer package defines interfaces (e.g., `FleetPipelineClient` defined in `internal/controller/`)

## Code Style

**Formatting:**
- Tool: `gofmt` - enforced in Makefile (`make fmt`)
- Imports: Organized by `goimports` - external tool run via `make fmt`
- Line length: General enforcement with exceptions for `api/` directory (excluded from lll check)

**Linting:**
- Tool: `golangci-lint` with custom config in `.golangci.yml`
- Run: `make lint` (check), `make lint-fix` (auto-fix)
- Enabled linters:
  - `errcheck` - catches unhandled errors
  - `gocyclo` - checks cyclic complexity
  - `revive` - style checker (includes comment-spacings, import-shadowing)
  - `staticcheck` - static analysis
  - `unused` - detects unused code
  - `govet` - Go vet analysis
  - `modernize` - Go code modernization

**Error Handling:**
- Always wrap errors with `fmt.Errorf("%w", err)` for error chain preservation
- Return errors rather than logging internally (let caller decide)
- Custom error types: Use `*FleetAPIError` with `StatusCode`, `Operation`, `Message` fields
- Check for specific errors: Use type assertion `if apiErr, ok := err.(*fleetclient.FleetAPIError)`
- Handle 404 gracefully: Treat as success in cleanup operations (finalizer deletion)

## Import Organization

**Order:**
1. Standard library imports (e.g., `"context"`, `"fmt"`, `"net/http"`)
2. Third-party imports (e.g., `"k8s.io/api/core/v1"`, `"sigs.k8s.io/controller-runtime"`)
3. Local package imports (e.g., `"github.com/grafana/fleet-management-operator/api/v1alpha1"`)

**Path Aliases:**
- Kubernetes API: `corev1 "k8s.io/api/core/v1"`, `apierrors "k8s.io/apimachinery/pkg/api/errors"`
- Controller-runtime: `ctrl "sigs.k8s.io/controller-runtime"`, `logf "sigs.k8s.io/controller-runtime/pkg/log"`
- Custom types: `fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"`

## Logging

**Framework:** `controller-runtime` logger via `logf.FromContext(ctx)`

**Patterns:**
- Log reconciliation start/end with key-value pairs: `log.Info("reconciling Pipeline", "namespace", req.Namespace, "name", req.Name)`
- Use verbose level for debug info: `log.V(1).Info("pipeline already reconciled, skipping", "generation", pipeline.Generation)`
- Error logging includes context: `log.Error(err, "failed to get Pipeline")`
- Structured logging: Key-value pairs, not string concatenation
- Log at appropriate levels:
  - `Info`: Normal operations (reconcile start, status updates, deletions)
  - `V(1)`: Debug details (skipped reconciles, internal decisions)
  - `Error`: Only actual errors that cause failures
- Never log credentials or secrets - use sanitized values only

**Safe Event Emission:**
- Always check if `Recorder` is not nil before emitting (testing pattern):
  ```go
  func (r *PipelineReconciler) emitEvent(object runtime.Object, eventtype, reason, message string) {
    if r.Recorder != nil {
      r.Recorder.Event(object, eventtype, reason, message)
    }
  }
  ```

## Comments

**When to Comment:**
- Public functions: Always add comment explaining purpose, parameters, return values
- Complex logic: Explain the "why" not the "what"
- Non-obvious decisions: Document rationale
- Webhook functions: Include kubebuilder comment markers above function signatures

**JSDoc/GoDoc Pattern:**
- Public functions get leading comment: `// FunctionName does X with Y`
- Receivers use comment on function: `// ValidateCreate implements webhook.CustomValidator...`
- Package-level: Include file header with Apache license

## Function Design

**Size:** Keep functions focused and under ~50 lines where possible; longer functions like `Reconcile` (41 lines) are acceptable for main controller logic

**Parameters:**
- Always accept `context.Context` as first parameter for cancellation/timeout support
- Error returns should be last in return tuple
- Receiver methods use pointer receivers for types that may need mutation

**Return Values:**
- Controllers return `(ctrl.Result, error)` tuple
- Validators return `(admission.Warnings, error)` tuple
- Builders return pointers to constructed objects
- API client methods follow: `(*Type, error)` for create/read, `error` for delete

**Error Checking Pattern:**
- Explicit error checks on every operation that can fail
- No `_ = err` or `_ =` discards unless intentional (comment why if so)
- Immediately return or handle errors, don't defer error handling

## Status Updates

**Critical Pattern:**
- Always use `Status().Update()` NOT `Update()` for status subresources
- Update `ObservedGeneration` to mark spec as processed
- Use `meta.SetStatusCondition()` for condition updates with `ObservedGeneration`
- Example from `pipeline_controller.go`:
  ```go
  meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
    Type:               conditionTypeReady,
    Status:             metav1.ConditionTrue,
    Reason:             reasonSynced,
    Message:            fmt.Sprintf("UpsertPipeline succeeded, ID: %s", apiPipeline.ID),
    ObservedGeneration: pipeline.Generation,
  })
  if err := r.Status().Update(ctx, pipeline); err != nil {
    // Handle error
  }
  ```

## Reconciliation Pattern

**Structure:** (`internal/controller/pipeline_controller.go` pattern)
1. Log reconciliation start with resource identity
2. Fetch resource (handle NotFound as clean exit)
3. Check deletion timestamp (handle deletion logic separately)
4. Add/verify finalizer
5. Check `ObservedGeneration` == `Generation` (skip if unchanged)
6. Perform reconciliation
7. Update status and return result

**Interface Verification:** Use compile-time check:
```go
var _ reconcile.Reconciler = &PipelineReconciler{}
```

## Finalizer Handling

**Pattern:**
- Finalizer name: `"pipeline.fleetmanagement.grafana.com/finalizer"`
- Add before first sync
- On deletion: call API delete, handle 404 as success (already deleted)
- Remove finalizer last, allowing resource to be garbage collected

## Module Design

**Exports:**
- Exported types/functions have explicit documentation comments
- Package-level constants for string literals used across the codebase

**Interfaces:**
- Define in consumer package (controller), not provider package (client)
- Example: `FleetPipelineClient` interface defined in `internal/controller/` package, not in `pkg/fleetclient/`
- Single-method interfaces for mock-friendly testing

---

*Convention analysis: 2026-02-08*
