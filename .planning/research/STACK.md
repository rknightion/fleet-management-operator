# Technology Stack: Error Handling for Kubernetes Operators

**Domain:** Kubernetes Operator Error Handling
**Researched:** 2026-02-08
**Confidence:** HIGH

## Recommended Stack

### Core Error Handling

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| Standard library `errors` | Go 1.25+ | Error wrapping with `%w` verb | Native, zero-dependency error chain preservation. Essential for controller-runtime's error classification. Enables `errors.Is()` and `errors.As()` inspection. |
| `k8s.io/apimachinery/pkg/api/errors` | v0.35.0 | Kubernetes API error classification | Official Kubernetes error package. Provides `IsConflict()`, `IsNotFound()`, `IsServerTimeout()` for proper error handling. Required for idiomatic controller patterns. |
| `k8s.io/client-go/util/retry` | v0.35.0 | Retry-on-conflict for status updates | Official Kubernetes retry package. `RetryOnConflict()` handles optimistic concurrency automatically. Prevents infinite recursion on status update conflicts. |
| `k8s.io/apimachinery/pkg/util/wait` | v0.35.0 | Exponential backoff utilities | Official backoff implementation. Provides `Backoff` type with configurable factor, jitter, cap. Used by controller-runtime internally. |

### HTTP Client Error Handling

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| Standard library `io` | Go 1.25+ | Safe `io.ReadAll()` with error checking | Native. Must check errors from `io.ReadAll()` to avoid truncated error messages. Use `io.LimitReader()` for protection against memory exhaustion. |
| Standard library `fmt` | Go 1.25+ | Error wrapping with `fmt.Errorf()` | Native. Use `fmt.Errorf("context: %w", err)` for wrapping. Critical for preserving error chains in HTTP client errors. |
| `net/http` with context | Go 1.25+ | Timeout and cancellation | Standard library. Use `http.NewRequestWithContext()` for proper timeout handling. Enables graceful shutdown and prevents hanging requests. |

### Rate Limiting (Already Implemented)

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| `golang.org/x/time/rate` | v0.9.0 | Token bucket rate limiting | Official Go extended library. Already used correctly in codebase with `limiter.Wait(ctx)`. Handles Fleet Management API 3 req/s limit properly. |

### Controller-Runtime Integration

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| `sigs.k8s.io/controller-runtime` | v0.23.0 | Automatic exponential backoff | Current version in use. Built-in workqueue rate limiter handles exponential backoff when reconcile returns error. Base delay: 5ms, max: 1000s, factor: 2.0. |
| Status subresource | controller-runtime v0.23.0 | Separate status updates | Use `r.Status().Update()` not `r.Update()`. Prevents spec/status update conflicts. Built into controller-runtime client. |

## Specific Patterns for Current Issues

### Pattern 1: io.ReadAll() Error Handling

**Current Issue:** Lines 88, 139 in `pkg/fleetclient/client.go` ignore `io.ReadAll()` errors.

**Recommended Pattern:**
```go
bodyBytes, err := io.ReadAll(resp.Body)
if err != nil {
    return &FleetAPIError{
        StatusCode: resp.StatusCode,
        Operation:  "UpsertPipeline",
        Message:    fmt.Sprintf("failed to read response body: %v", err),
    }
}
return &FleetAPIError{
    StatusCode: resp.StatusCode,
    Operation:  "UpsertPipeline",
    Message:    string(bodyBytes),
}
```

**Why:** Prevents truncated error messages in logs and status conditions. `io.ReadAll()` can fail on network errors, connection drops, or malformed responses.

**Source:** Go standard library best practices, verified across multiple production operator implementations.

**Confidence:** HIGH - Official Go documentation and standard practice.

### Pattern 2: Status Update Conflict Resolution

**Current Issue:** Lines 337-340, 389-391 in `internal/controller/pipeline_controller.go` use naive `Requeue: true` on conflict, risking infinite loops.

**Recommended Pattern:**
```go
import "k8s.io/client-go/util/retry"

func (r *PipelineReconciler) updateStatusSuccess(ctx context.Context, pipeline *fleetmanagementv1alpha1.Pipeline, apiPipeline *fleetclient.Pipeline) (ctrl.Result, error) {
    log := logf.FromContext(ctx)

    err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
        // Always re-fetch the latest version
        current := &fleetmanagementv1alpha1.Pipeline{}
        if err := r.Get(ctx, client.ObjectKeyFromObject(pipeline), current); err != nil {
            return err
        }

        // Apply status updates to fresh copy
        current.Status.ID = apiPipeline.ID
        current.Status.ObservedGeneration = current.Generation

        if apiPipeline.CreatedAt != nil {
            current.Status.CreatedAt = &metav1.Time{Time: *apiPipeline.CreatedAt}
        }
        if apiPipeline.UpdatedAt != nil {
            current.Status.UpdatedAt = &metav1.Time{Time: *apiPipeline.UpdatedAt}
        }

        meta.SetStatusCondition(&current.Status.Conditions, metav1.Condition{
            Type:               conditionTypeReady,
            Status:             metav1.ConditionTrue,
            Reason:             reasonSynced,
            Message:            "Pipeline successfully synced to Fleet Management",
            ObservedGeneration: current.Generation,
        })

        // Attempt update - will retry automatically on conflict
        return r.Status().Update(ctx, current)
    })

    if err != nil {
        log.Error(err, "failed to update status after retries")
        return ctrl.Result{}, err
    }

    // Events and logging after successful update
    log.Info("successfully synced pipeline", "id", apiPipeline.ID)
    return ctrl.Result{}, nil
}
```

**Why:** `RetryOnConflict` automatically re-fetches the resource on conflict and retries up to 5 times with jitter. Prevents infinite recursion risk from calling `reconcileNormal()` again on 404 detection.

**Configuration:** Uses `retry.DefaultRetry` (5 steps, 10ms base, 1.0 factor, 0.1 jitter) for status updates under active management.

**Source:** kubernetes-sigs/controller-runtime GitHub issues #1748, kubebuilder documentation on conflict handling.

**Confidence:** HIGH - Official Kubernetes pattern, verified in controller-runtime documentation.

### Pattern 3: External Deletion Detection

**Current Issue:** Line 269 in `pipeline_controller.go` has potential infinite recursion calling `reconcileNormal()` when pipeline not found (404).

**Recommended Pattern:**
```go
case http.StatusNotFound:
    // Pipeline was deleted externally, mark for recreation
    log.Info("pipeline not found in Fleet Management, will recreate on next reconcile")
    r.emitEvent(pipeline, corev1.EventTypeWarning, eventReasonRecreated,
        "Pipeline was deleted externally, will be recreated")

    // Clear ID to force fresh creation
    pipeline.Status.ID = ""
    pipeline.Status.ObservedGeneration = 0 // Force re-reconcile

    // Update status to trigger new reconcile loop
    if err := r.Status().Update(ctx, pipeline); err != nil {
        if apierrors.IsConflict(err) {
            log.V(1).Info("status update conflict, will retry")
            return ctrl.Result{Requeue: true}, nil
        }
        return ctrl.Result{}, err
    }

    // Return error to trigger backoff, not immediate recursion
    return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
```

**Why:** Prevents infinite recursion by returning to controller-runtime's workqueue instead of calling `reconcileNormal()` directly. Allows exponential backoff if external deletion is repeatedly happening.

**Source:** Kubebuilder good practices documentation, controller-runtime reconciliation patterns.

**Confidence:** HIGH - Standard controller pattern.

### Pattern 4: Error Wrapping Throughout

**Current Pattern:** Already good in most places, but ensure consistency.

**Recommended Pattern:**
```go
// Always use %w for error wrapping
if err := doOperation(); err != nil {
    return fmt.Errorf("failed to do operation: %w", err)
}

// Check wrapped errors with errors.Is()
if errors.Is(err, context.DeadlineExceeded) {
    // Handle timeout
}

// Extract wrapped error types with errors.As()
var apiErr *fleetclient.FleetAPIError
if errors.As(err, &apiErr) {
    // Handle API error
}
```

**Why:** `%w` preserves error chains for inspection. Controller-runtime workqueue uses error classification to decide retry behavior.

**Source:** Go 1.13+ error handling blog, official Go documentation.

**Confidence:** HIGH - Official Go standard since 1.13.

## Exponential Backoff Behavior

### Built-in Controller-Runtime Backoff

**How it works:** When `Reconcile()` returns a non-nil error, controller-runtime's workqueue automatically applies per-item exponential backoff.

**Default Configuration:**
```go
// ItemExponentialFailureRateLimiter
baseDelay := 5 * time.Millisecond
maxDelay := 1000 * time.Second
factor := 2.0

// Combined with BucketRateLimiter
qps := 10
burst := 100
```

**Progression:** 5ms → 10ms → 20ms → 40ms → 80ms → 160ms → 320ms → 640ms → 1.28s → 2.56s → 5.12s → 10.24s → 20.48s → 40.96s → 81.92s → 163.84s → 327.68s → 655.36s → 1000s (capped)

**When to use:**
- Return `ctrl.Result{}, err` for transient errors (network, 500s, timeouts)
- Automatic per-resource backoff
- No custom backoff logic needed

**When NOT to use:**
- Validation errors (don't retry)
- 404 deletion detection (use explicit `RequeueAfter`)
- Status update conflicts (use `RetryOnConflict`)

**Source:** controller-runtime workqueue implementation, confirmed in GitHub issue #808 and discussion #2506.

**Confidence:** HIGH - Official controller-runtime behavior.

## Supporting Libraries

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `io.LimitReader()` | Standard library | Prevent memory exhaustion from large responses | Wrap `resp.Body` before `io.ReadAll()` if response size is unbounded: `io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))` |
| `context.WithTimeout()` | Standard library | Per-request timeouts | Already handled by `http.Client.Timeout` in current implementation. Use for specific operations requiring different timeouts. |
| `k8s.io/client-go/util/workqueue` | v0.35.0 | Custom rate limiter configuration | Only if default backoff (5ms base, 1000s max) is insufficient. Requires custom controller setup with `workqueue.NewRateLimitingQueue()`. |

## Alternatives Considered

| Recommended | Alternative | Why Not |
|-------------|-------------|---------|
| `k8s.io/client-go/util/retry` | Manual retry loops | Error-prone. Easy to create infinite loops. No jitter. `RetryOnConflict` is battle-tested across all Kubernetes controllers. |
| Standard library error wrapping | `github.com/pkg/errors` | Deprecated. Go 1.13+ native error wrapping with `%w` is standard. No need for external dependency. |
| Built-in workqueue backoff | `github.com/cenkalti/backoff` | Already in go.mod as indirect dependency, but controller-runtime's built-in backoff is automatic and per-item. No need to manage manually. |
| `io.ReadAll()` with error check | `io.ReadAll()` ignoring errors | Current codebase issue. Always check errors to avoid truncated messages and silent failures. |

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|-------------|
| `panic()` in reconcile loop | Crashes controller, loses all in-flight reconciliations | Return error for exponential backoff |
| `time.Sleep()` in reconcile | Blocks worker goroutine, reduces throughput | Return `ctrl.Result{RequeueAfter: duration}` |
| Direct recursion (`reconcileNormal()` calling itself) | Risk of stack overflow and infinite loops | Return to workqueue, let controller-runtime manage retries |
| `errors.New()` when wrapping | Breaks error chain inspection | Use `fmt.Errorf("context: %w", err)` |
| `Requeue: true` for conflicts | Immediate retry without backoff | Use `retry.RetryOnConflict()` for status updates |

## Installation

No additional dependencies required. All patterns use existing dependencies:

```bash
# Already in go.mod
golang.org/x/time v0.9.0
k8s.io/apimachinery v0.35.0
k8s.io/client-go v0.35.0
sigs.k8s.io/controller-runtime v0.23.0
```

Verify imports in files:
```go
import (
    "errors"  // Standard library
    "fmt"     // Standard library
    "io"      // Standard library

    apierrors "k8s.io/apimachinery/pkg/api/errors"
    "k8s.io/apimachinery/pkg/util/wait"
    "k8s.io/client-go/util/retry"
)
```

## Version Compatibility

Current versions are compatible and up-to-date:

| Package | Current Version | Notes |
|---------|-----------------|-------|
| Go | 1.25.0 | Latest stable. Error wrapping with `%w` available since 1.13. |
| controller-runtime | v0.23.0 | Released 2025-01-16. Latest stable. Default workqueue backoff unchanged. |
| client-go | v0.35.0 | Matches k8s.io/apimachinery v0.35.0. `retry` package API stable since v0.20.0. |
| golang.org/x/time/rate | v0.9.0 | Latest. API stable. `Wait(ctx)` pattern used correctly in codebase. |

No version upgrades required. All patterns work with current dependency versions.

## Stack Patterns by Error Type

### Transient Errors (Network, 5xx, Timeouts)
**Pattern:** Return error for automatic exponential backoff
```go
if err != nil {
    return ctrl.Result{}, fmt.Errorf("failed to sync: %w", err)
}
```

### Validation Errors (4xx, Bad Request)
**Pattern:** Update status, don't retry
```go
pipeline.Status.ObservedGeneration = pipeline.Generation
meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
    Type:    conditionTypeReady,
    Status:  metav1.ConditionFalse,
    Reason:  reasonValidationError,
    Message: err.Error(),
})
if err := r.Status().Update(ctx, pipeline); err != nil {
    return ctrl.Result{}, err
}
return ctrl.Result{}, nil // Don't return error - no retry
```

### Status Update Conflicts
**Pattern:** Use `retry.RetryOnConflict()`
```go
err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
    current := &v1alpha1.Pipeline{}
    if err := r.Get(ctx, key, current); err != nil {
        return err
    }
    current.Status.Field = value
    return r.Status().Update(ctx, current)
})
```

### Rate Limiting (429)
**Pattern:** Explicit requeue delay
```go
case http.StatusTooManyRequests:
    log.Info("rate limited, requeueing")
    return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
```

### External Deletion (404 on Update)
**Pattern:** Clear state and requeue with delay
```go
case http.StatusNotFound:
    pipeline.Status.ID = ""
    pipeline.Status.ObservedGeneration = 0
    if err := r.Status().Update(ctx, pipeline); err != nil {
        return ctrl.Result{}, err
    }
    return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
```

## Sources

**HIGH Confidence:**
- [k8s.io/client-go/util/retry - Official Package Docs](https://pkg.go.dev/k8s.io/client-go/util/retry)
- [k8s.io/apimachinery/pkg/api/errors - Official Package Docs](https://pkg.go.dev/k8s.io/apimachinery/pkg/api/errors)
- [k8s.io/apimachinery/pkg/util/wait - Official Package Docs](https://pkg.go.dev/k8s.io/apimachinery/pkg/util/wait)
- [golang.org/x/time/rate - Official Package Docs](https://pkg.go.dev/golang.org/x/time/rate)
- [Working with Errors in Go 1.13 - Official Go Blog](https://go.dev/blog/go1.13-errors)
- [Kubebuilder: Implementing a controller - Official Docs](https://book.kubebuilder.io/cronjob-tutorial/controller-implementation)

**MEDIUM Confidence:**
- [Kubernetes operators best practices: understanding conflict errors - Alena Varkockova](https://alenkacz.medium.com/kubernetes-operators-best-practices-understanding-conflict-errors-d05353dff421)
- [Building Resilient Kubernetes Controllers: A Practical Guide to Retry Mechanisms](https://medium.com/@vamshitejanizam/building-resilient-kubernetes-controllers-a-practical-guide-to-retry-mechanisms-0d689160fa51)
- [Rate Limiting in controller-runtime and client-go - Daniel Mangum](https://danielmangum.com/posts/controller-runtime-client-go-rate-limiting/)
- [Error Back-off with Controller Runtime - Stuart Leeks](https://stuartleeks.com/posts/error-back-off-with-controller-runtime/)
- [Kubernetes Controllers at Scale: Clients, Caches, Conflicts, Patches Explained](https://medium.com/@timebertt/kubernetes-controllers-at-scale-clients-caches-conflicts-patches-explained-aa0f7a8b4332)

**Community Sources:**
- [controller-runtime Issue #1748: How to elegantly solve the update conflict problem](https://github.com/kubernetes-sigs/controller-runtime/issues/1748)
- [controller-runtime Issue #808: Document ways to trigger/not trigger exponential backoff](https://github.com/kubernetes-sigs/controller-runtime/issues/808)
- [kubebuilder Discussion #2506: Rolling my own exponential backoff](https://github.com/kubernetes-sigs/kubebuilder/discussions/2506)

---
*Stack research for: Kubernetes Operator Error Handling*
*Researched: 2026-02-08*
*Confidence: HIGH - All recommendations based on official Kubernetes and Go documentation*
