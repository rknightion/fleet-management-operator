# Phase 2: Controller Error Handling - Research

**Researched:** 2026-02-08
**Domain:** Kubernetes controller error handling patterns
**Confidence:** HIGH

## Summary

Phase 2 focuses on making the controller reconciliation loop handle errors correctly with proper retry semantics. The core challenge is ensuring status update failures preserve the original reconciliation error (for exponential backoff), preventing infinite loops from external deletion detection, and properly classifying transient vs permanent errors.

Controller-runtime provides sophisticated error handling through the `Reconcile(ctx, req) (Result, error)` return pattern. The combination of Result and error determines retry behavior: errors trigger exponential backoff, `Result{Requeue: true}` triggers immediate retry WITHOUT backoff, and `Result{RequeueAfter: duration}` provides controlled retry timing. The critical insight is that **status update errors must NOT replace the original reconciliation error**, otherwise exponential backoff is lost and conflicts cause infinite retry loops.

**Primary recommendation:** Return the original reconciliation error from `updateStatusError`, handle status update conflicts with `Result{Requeue: true}` (not immediate retry), add recursion limits to external deletion detection, and create error classification helpers to distinguish transient from permanent errors.

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| controller-runtime | v0.23.0 | Kubernetes operator framework | Official Kubernetes SIG project, provides reconciliation loop with exponential backoff, status subresource handling, and watch predicates |
| k8s.io/apimachinery/pkg/api/errors | v0.35.0 | Kubernetes API error classification | Official error classification helpers (`IsConflict`, `IsNotFound`, `IsServerTimeout`) for proper error handling |
| k8s.io/client-go/util/retry | v0.35.0 | Retry with exponential backoff | Standard Kubernetes retry utilities, provides `RetryOnConflict` for status updates |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| golang.org/x/time/rate | v0.9.0 | Rate limiting | Already in use for Fleet API rate limiting (3 req/s) |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Manual retry logic | client-go/util/retry.RetryOnConflict | Manual logic is error-prone, RetryOnConflict provides standard exponential backoff |
| Custom error classification | k8s.io/apimachinery errors helpers | Custom logic misses edge cases, standard helpers cover all Kubernetes error types |

**Installation:**
Already present in go.mod - no additional dependencies needed.

## Architecture Patterns

### Recommended Error Return Structure

The controller currently has these error handling functions that need fixes:

```
pipeline_controller.go
├── Reconcile() - Entry point, routes to reconcileNormal/Delete
├── reconcileNormal() - Calls handleAPIError on Fleet API errors
├── reconcileDelete() - Already handles 404 correctly
├── handleAPIError() - Classifies Fleet API errors, routes to updateStatusError
├── updateStatusSuccess() - Handles status update conflicts (✓ correct pattern)
└── updateStatusError() - CRITICAL: Must return original error, not status update error
```

### Pattern 1: Preserve Original Error in Status Updates

**What:** When status update fails, return the ORIGINAL reconciliation error, not the status update error.

**Why:** Status update errors (especially conflicts) should not replace the original error because:
1. Original error determines retry behavior (exponential backoff)
2. Status conflicts indicate stale cache, should requeue for fresh data
3. Returning status update error loses the root cause

**Current implementation (INCORRECT):**
```go
// From pipeline_controller.go:388-395
if updateErr := r.Status().Update(ctx, pipeline); updateErr != nil {
    if apierrors.IsConflict(updateErr) {
        log.V(1).Info("status update conflict, requeueing")
        return ctrl.Result{Requeue: true}, nil
    }
    log.Error(updateErr, "failed to update status")
    return ctrl.Result{}, updateErr  // WRONG: Returns status error, loses original error
}

// For validation errors, don't retry immediately
if reason == reasonValidationError {
    log.Info("validation error, not requeueing", "error", err.Error())
    return ctrl.Result{}, nil
}

// For other errors, return error for exponential backoff
return ctrl.Result{}, err  // This line is unreachable if status update fails!
```

**Correct implementation:**
```go
func (r *PipelineReconciler) updateStatusError(ctx context.Context, pipeline *fleetmanagementv1alpha1.Pipeline, reason string, originalErr error) (ctrl.Result, error) {
    log := logf.FromContext(ctx)

    // Update observedGeneration and conditions
    pipeline.Status.ObservedGeneration = pipeline.Generation
    meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
        Type:               conditionTypeReady,
        Status:             metav1.ConditionFalse,
        Reason:             reason,
        Message:            originalErr.Error(),
        ObservedGeneration: pipeline.Generation,
    })

    // Try to update status, but preserve original error
    if updateErr := r.Status().Update(ctx, pipeline); updateErr != nil {
        if apierrors.IsConflict(updateErr) {
            // Cache is stale, requeue to get fresh copy
            log.V(1).Info("status update conflict during error handling, requeueing")
            return ctrl.Result{Requeue: true}, nil
        }
        // Log status update failure but still return original error
        log.Error(updateErr, "failed to update status after reconciliation error",
            "originalError", originalErr)
    }

    // CRITICAL: Return original error for exponential backoff
    // For validation errors, don't retry
    if reason == reasonValidationError {
        return ctrl.Result{}, nil
    }

    // For transient errors, return error to trigger exponential backoff
    return ctrl.Result{}, originalErr
}
```

### Pattern 2: Error Classification Helpers

**What:** Helper functions to determine if errors are transient (retriable) or permanent.

**Why:** Different error types require different retry strategies:
- Transient errors (network, 5xx, timeouts) → exponential backoff
- Rate limits (429) → fixed delay requeue
- Permanent errors (validation, 400) → don't retry
- Not found (404) → context-dependent handling

**Implementation:**
```go
// In controller package (internal/controller/errors.go)

// isTransientError determines if an error should be retried with exponential backoff
func isTransientError(err error) bool {
    // Check for FleetAPIError with transient status codes
    if apiErr, ok := err.(*fleetclient.FleetAPIError); ok {
        return apiErr.IsTransient()
    }

    // Network errors are transient
    if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
        return false // Don't retry cancelled operations
    }

    // Other network-related errors
    var netErr net.Error
    if errors.As(err, &netErr) && netErr.Timeout() {
        return true
    }

    // Unknown errors are treated as transient (safe default)
    return true
}

// shouldRetry determines if reconciliation should retry after an error
func shouldRetry(err error, reason string) bool {
    // Validation errors should not retry
    if reason == reasonValidationError {
        return false
    }

    // Check if error is transient
    return isTransientError(err)
}
```

### Pattern 3: Recursion Limit for External Deletion Detection

**What:** Add retry/recursion limit to prevent infinite loops when external deletion is detected repeatedly.

**Why:** Current code at line 269 recursively calls `reconcileNormal` when 404 is detected:
```go
case http.StatusNotFound:
    // Pipeline was deleted externally, recreate it
    log.Info("pipeline not found in Fleet Management, will recreate")
    pipeline.Status.ID = "" // Clear the ID so it's created fresh
    return r.reconcileNormal(ctx, pipeline)  // DANGER: Can recurse infinitely
```

If the pipeline keeps failing to create (e.g., validation error that isn't properly detected), this creates an infinite loop within a single reconciliation.

**Correct implementation:**
```go
// In PipelineReconciler struct
type PipelineReconciler struct {
    client.Client
    Scheme      *runtime.Scheme
    FleetClient FleetPipelineClient
    Recorder    record.EventRecorder
}

// In handleAPIError function
case http.StatusNotFound:
    // Pipeline was deleted externally
    // Check if we've already retried recreation in this reconciliation
    // by checking if ID was already cleared
    if pipeline.Status.ID == "" {
        // ID was already cleared, but still getting 404
        // This indicates a problem (permissions, validation, etc.)
        log.Error(apiErr, "pipeline creation failed after external deletion detection")
        r.emitEvent(pipeline, corev1.EventTypeWarning, eventReasonSyncFailed,
            "Failed to recreate pipeline after external deletion")
        return r.updateStatusError(ctx, pipeline, reasonSyncFailed,
            fmt.Errorf("pipeline not found and recreation failed: %w", err))
    }

    // First detection of external deletion - try to recreate
    log.Info("pipeline not found in Fleet Management, will recreate",
        "previousID", pipeline.Status.ID)
    r.emitEvent(pipeline, corev1.EventTypeWarning, eventReasonRecreated,
        "Pipeline was deleted externally, recreating in Fleet Management")

    // Clear ID and try again
    pipeline.Status.ID = ""
    req := r.buildUpsertRequest(pipeline)
    apiPipeline, err := r.FleetClient.UpsertPipeline(ctx, req)
    if err != nil {
        return r.handleAPIError(ctx, pipeline, err)
    }

    return r.updateStatusSuccess(ctx, pipeline, apiPipeline)
```

### Pattern 4: Status Update Conflict Requeue

**What:** For status update conflicts, use `Result{Requeue: true}` to get a fresh copy of the resource.

**Why:** Status update conflicts indicate the cached copy is stale. The correct response is to requeue (without delay) to reconcile with fresh data from the API server. The conflict error itself is not the "real" error - it's just a signal that our cache is stale.

**Current pattern in updateStatusSuccess (line 337-340) - CORRECT:**
```go
if err := r.Status().Update(ctx, pipeline); err != nil {
    if apierrors.IsConflict(err) {
        // Resource was modified, requeue to get fresh copy
        log.V(1).Info("status update conflict, requeueing")
        return ctrl.Result{Requeue: true}, nil  // ✓ Correct
    }
    log.Error(err, "failed to update status")
    return ctrl.Result{}, err
}
```

**Pattern in updateStatusError (line 388-395) - NEEDS FIX:**
Should preserve original error while handling conflict, not return status update error.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Retry on conflict | Custom retry loops with manual backoff | `retry.RetryOnConflict` from client-go | Handles exponential backoff, max retries, and proper context cancellation. Custom loops miss edge cases. |
| Error classification | Custom HTTP status code checks | `apierrors.IsConflict`, `IsNotFound`, `IsServerTimeout` | Standard helpers cover all Kubernetes error types and handle wrapped errors correctly. |
| Exponential backoff | Manual backoff calculation | Return error from Reconcile | Controller-runtime provides built-in exponential backoff when errors are returned. Manual backoff duplicates this logic. |
| Status update wrapper | Custom status update helpers | Direct `r.Status().Update()` with conflict handling | Kubernetes API guarantees status subresource behavior. Custom wrappers hide important conflict errors. |

**Key insight:** Controller-runtime's reconciliation loop already implements exponential backoff, rate limiting (10 req/s max), and retry logic. The controller's job is to return the RIGHT error (original reconciliation error, not status update error) so the framework's backoff works correctly.

## Common Pitfalls

### Pitfall 1: Returning Status Update Error Instead of Original Error

**What goes wrong:** The `updateStatusError` function returns the status update error when status update fails, not the original reconciliation error. This breaks exponential backoff because the original error (which determines retry strategy) is lost.

**Why it happens:** The code path handles status update error immediately and returns it, never reaching the code that returns the original error. Developer assumes "if status update fails, that's the error to return" without considering that the original error is more important for retry logic.

**How to avoid:**
- Log status update failures but ALWAYS return the original reconciliation error
- Only exception: status update conflicts should return `Result{Requeue: true}, nil` to get fresh data
- Pattern: `if updateErr != nil { log.Error(updateErr, "status update failed"); } return ctrl.Result{}, originalErr`

**Warning signs:**
- CPU spikes when status updates fail (no exponential backoff)
- Logs showing immediate retries instead of increasing delays
- Status update errors in logs but reconciliation errors are missing
- Test: inject status update failure and verify exponential backoff still works

### Pitfall 2: Infinite Recursion on External Deletion

**What goes wrong:** When Fleet API returns 404 (pipeline not found), the code clears the ID and recursively calls `reconcileNormal`. If recreation fails with another 404 or a validation error that isn't properly caught, this recurses infinitely within a single reconciliation, eventually stack overflowing.

**Why it happens:** The code assumes external deletion is rare and recreation will succeed. It doesn't guard against the case where recreation keeps failing with 404 or where validation errors are misclassified.

**How to avoid:**
- Check if `pipeline.Status.ID` is already empty before recursing
- If ID is empty and still getting 404, return error (don't recurse)
- Better: Don't recurse at all - clear ID, rebuild request, call UpsertPipeline inline
- Add metrics/logging to detect recreation failure patterns

**Warning signs:**
- Stack overflow errors in controller logs
- Single reconciliation taking abnormally long (>10s)
- Logs showing repeated "pipeline not found, will recreate" without success
- Memory usage spikes in controller pod
- Test: Mock 404 responses repeatedly and verify no stack overflow

### Pitfall 3: Using `Requeue: true` for Errors That Need Backoff

**What goes wrong:** Using `Result{Requeue: true}, nil` for transient errors (network failures, 5xx) causes immediate retry without exponential backoff. Under sustained failures, this creates a tight loop burning CPU and hammering the failing API.

**Why it happens:** Developer conflates "retry" with "immediate requeue". The mental model is "error occurred, queue it again" but doesn't consider that immediate retry makes failures worse. Controller-runtime's `Requeue: true` is designed for "work is incomplete but will succeed soon" not "error occurred, try again".

**How to avoid:**
- Return error (not nil) for transient failures → controller-runtime applies exponential backoff
- Use `Result{RequeueAfter: duration}` only for specific delays (rate limits, scheduled retries)
- Use `Result{Requeue: true}` ONLY for conflicts (stale cache, need fresh data)
- Never use `Requeue: true` after API errors unless error is explicitly a conflict

**Warning signs:**
- High reconciliation rate (>100/s) during API outages
- CPU usage remains high during transient failures
- No increasing delay between retries in logs
- API rate limit errors from downstream services
- Test: Mock sustained 5xx errors and verify exponential backoff behavior

### Pitfall 4: Misclassifying Transient vs Permanent Errors

**What goes wrong:** Treating permanent errors (validation, 400 Bad Request) as transient causes infinite retries. Treating transient errors (network timeouts, 5xx) as permanent prevents recovery from temporary issues.

**Why it happens:** HTTP status codes don't perfectly map to transient/permanent. Some 400 errors are transient (rate limit format varies), some 5xx errors are permanent (501 Not Implemented). Developers apply simple rules ("4xx = permanent, 5xx = transient") that don't match reality.

**How to avoid:**
- Use FleetAPIError's `IsTransient()` method (already implemented in Phase 1)
- Validation errors (400 from Fleet API) → permanent, don't retry
- Rate limits (429) → transient, but use fixed delay not exponential backoff
- Network errors (timeout, connection refused) → transient, use exponential backoff
- 5xx errors → transient (server might recover)
- 404 on DELETE → success (idempotent deletion)
- 404 on GET/UPDATE → external deletion, handle specially

**Warning signs:**
- Resources stuck with validation errors being retried forever
- Resources with network errors never recovering
- Logs showing repeated validation errors without human intervention
- Test: Inject various error types and verify correct retry behavior

## Code Examples

Verified patterns from existing codebase and controller-runtime docs:

### Error Classification Helper

```go
// internal/controller/errors.go
package controller

import (
    "context"
    "errors"
    "net"

    "github.com/grafana/fleet-management-operator/pkg/fleetclient"
)

// isTransientError determines if an error should be retried with exponential backoff
func isTransientError(err error) bool {
    // Check for FleetAPIError with IsTransient method
    var apiErr *fleetclient.FleetAPIError
    if errors.As(err, &apiErr) {
        return apiErr.IsTransient()
    }

    // Context cancellation is not transient
    if errors.Is(err, context.Canceled) {
        return false
    }

    // Network timeouts are transient
    var netErr net.Error
    if errors.As(err, &netErr) && netErr.Timeout() {
        return true
    }

    // Default: treat unknown errors as transient (safe default)
    return true
}

// shouldRetry determines if reconciliation should retry after an error
func shouldRetry(err error, reason string) bool {
    // Validation errors should not retry
    if reason == reasonValidationError {
        return false
    }

    return isTransientError(err)
}
```

### Fixed updateStatusError

```go
// internal/controller/pipeline_controller.go
func (r *PipelineReconciler) updateStatusError(ctx context.Context, pipeline *fleetmanagementv1alpha1.Pipeline, reason string, originalErr error) (ctrl.Result, error) {
    log := logf.FromContext(ctx)

    // Update observedGeneration to indicate we attempted reconciliation
    pipeline.Status.ObservedGeneration = pipeline.Generation

    // Set Ready condition to False
    meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
        Type:               conditionTypeReady,
        Status:             metav1.ConditionFalse,
        Reason:             reason,
        Message:            originalErr.Error(),
        ObservedGeneration: pipeline.Generation,
    })

    // Set Synced condition to False
    meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
        Type:               conditionTypeSynced,
        Status:             metav1.ConditionFalse,
        Reason:             reason,
        Message:            originalErr.Error(),
        ObservedGeneration: pipeline.Generation,
    })

    // CRITICAL: Try to update status, but preserve original error
    if updateErr := r.Status().Update(ctx, pipeline); updateErr != nil {
        if apierrors.IsConflict(updateErr) {
            // Cache is stale, requeue to get fresh copy
            // Don't return original error - conflict means we need fresh data
            log.V(1).Info("status update conflict during error handling, requeueing")
            return ctrl.Result{Requeue: true}, nil
        }
        // Log status update failure but continue to return original error
        log.Error(updateErr, "failed to update status after reconciliation error",
            "originalError", originalErr.Error(),
            "reason", reason)
    }

    // For validation errors, don't retry
    if reason == reasonValidationError {
        log.Info("validation error, not requeueing", "error", originalErr.Error())
        return ctrl.Result{}, nil
    }

    // CRITICAL: Return original error to preserve exponential backoff
    return ctrl.Result{}, originalErr
}
```

### Fixed External Deletion Handling

```go
// internal/controller/pipeline_controller.go
func (r *PipelineReconciler) handleAPIError(ctx context.Context, pipeline *fleetmanagementv1alpha1.Pipeline, err error) (ctrl.Result, error) {
    log := logf.FromContext(ctx)

    // Check if it's a Fleet API error
    if apiErr, ok := err.(*fleetclient.FleetAPIError); ok {
        switch apiErr.StatusCode {
        case http.StatusBadRequest:
            // Validation error - update status and don't retry
            log.Info("validation error from Fleet Management API", "message", apiErr.Message)
            r.emitEventf(pipeline, corev1.EventTypeWarning, eventReasonValidationFail,
                "Fleet Management API validation failed: %s", apiErr.Message)
            return r.updateStatusError(ctx, pipeline, reasonValidationError, err)

        case http.StatusNotFound:
            // Pipeline was deleted externally
            // Check if we've already tried to recreate (ID is empty)
            if pipeline.Status.ID == "" {
                // Already tried recreation and still getting 404
                log.Error(apiErr, "pipeline creation failed after external deletion detection")
                r.emitEvent(pipeline, corev1.EventTypeWarning, eventReasonSyncFailed,
                    "Failed to recreate pipeline after external deletion")
                return r.updateStatusError(ctx, pipeline, reasonSyncFailed,
                    fmt.Errorf("pipeline not found and recreation failed: %w", err))
            }

            // First detection - try to recreate inline (no recursion)
            log.Info("pipeline not found in Fleet Management, attempting recreation",
                "previousID", pipeline.Status.ID)
            r.emitEvent(pipeline, corev1.EventTypeWarning, eventReasonRecreated,
                "Pipeline was deleted externally, recreating in Fleet Management")

            // Clear ID and rebuild request
            pipeline.Status.ID = ""
            req := r.buildUpsertRequest(pipeline)

            // Try to create - if this fails, handleAPIError will handle it
            apiPipeline, err := r.FleetClient.UpsertPipeline(ctx, req)
            if err != nil {
                // Let handleAPIError classify the new error
                return r.handleAPIError(ctx, pipeline, err)
            }

            // Successfully recreated
            return r.updateStatusSuccess(ctx, pipeline, apiPipeline)

        case http.StatusTooManyRequests:
            // Rate limit - requeue with fixed delay
            log.Info("rate limited by Fleet Management API, requeueing")
            r.emitEvent(pipeline, corev1.EventTypeWarning, eventReasonRateLimited,
                "Rate limited by Fleet Management API, will retry in 10 seconds")
            return ctrl.Result{RequeueAfter: 10 * time.Second}, nil

        default:
            // Other API errors - return for exponential backoff
            log.Error(err, "Fleet Management API error",
                "statusCode", apiErr.StatusCode,
                "operation", apiErr.Operation,
                "pipelineID", pipeline.Status.ID,
                "message", apiErr.Message)
            r.emitEventf(pipeline, corev1.EventTypeWarning, eventReasonSyncFailed,
                "Fleet Management API error (HTTP %d): %s", apiErr.StatusCode, apiErr.Message)
            return r.updateStatusError(ctx, pipeline, reasonSyncFailed, err)
        }
    }

    // Network or other errors - return for exponential backoff
    log.Error(err, "failed to sync with Fleet Management")
    r.emitEventf(pipeline, corev1.EventTypeWarning, eventReasonSyncFailed,
        "Failed to sync with Fleet Management: %v", err)
    return r.updateStatusError(ctx, pipeline, reasonSyncFailed, err)
}
```

### Unit Test for Preserved Error

```go
// internal/controller/pipeline_controller_test.go
func TestUpdateStatusError_PreservesOriginalError(t *testing.T) {
    // Setup
    reconciler := &PipelineReconciler{
        Client: fakeclient.NewClientBuilder().Build(),
    }
    pipeline := &fleetmanagementv1alpha1.Pipeline{
        ObjectMeta: metav1.ObjectMeta{
            Name:       "test",
            Namespace:  "default",
            Generation: 2,
        },
    }
    originalErr := errors.New("API connection failed")

    // Call updateStatusError
    result, err := reconciler.updateStatusError(context.Background(), pipeline,
        reasonSyncFailed, originalErr)

    // Verify original error is returned
    assert.Error(t, err)
    assert.Equal(t, originalErr, err, "updateStatusError must return original error")
    assert.Equal(t, ctrl.Result{}, result)

    // Verify status was updated with error
    assert.Equal(t, pipeline.Generation, pipeline.Status.ObservedGeneration)
    condition := meta.FindStatusCondition(pipeline.Status.Conditions, conditionTypeReady)
    assert.NotNil(t, condition)
    assert.Equal(t, metav1.ConditionFalse, condition.Status)
    assert.Equal(t, reasonSyncFailed, condition.Reason)
}

func TestUpdateStatusError_ConflictHandling(t *testing.T) {
    // Setup with mock client that returns conflict on status update
    mockClient := &mockConflictClient{
        statusUpdateError: apierrors.NewConflict(
            schema.GroupResource{}, "test", errors.New("conflict")),
    }
    reconciler := &PipelineReconciler{
        Client: mockClient,
    }
    pipeline := &fleetmanagementv1alpha1.Pipeline{
        ObjectMeta: metav1.ObjectMeta{
            Name:       "test",
            Namespace:  "default",
            Generation: 2,
        },
    }
    originalErr := errors.New("API connection failed")

    // Call updateStatusError - should handle conflict without returning it
    result, err := reconciler.updateStatusError(context.Background(), pipeline,
        reasonSyncFailed, originalErr)

    // Verify conflict triggers requeue, not error return
    assert.NoError(t, err, "conflict should not return error")
    assert.True(t, result.Requeue, "conflict should trigger requeue")
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Return status update error | Preserve original error, log status failures | controller-runtime v0.7+ | Enables exponential backoff to work correctly |
| Immediate requeue on conflicts | `Result{Requeue: true}` for conflicts only | Always recommended | Distinguishes conflict (need fresh data) from error (need backoff) |
| Manual retry loops | Return error → framework handles backoff | controller-runtime v0.1+ | Simplifies code, leverages framework capabilities |
| Custom error classification | Use `apierrors.IsConflict`, `IsNotFound` | Always available in k8s.io/apimachinery | Standard helpers handle wrapped errors correctly |

**Deprecated/outdated:**
- Manual exponential backoff implementation - controller-runtime provides this via error returns
- `client.RetryOnConflict` for ALL status updates - only needed when you can't accept a requeue (rare)
- Checking ResourceVersion manually - use `apierrors.IsConflict` instead

## Open Questions

1. **Should we use `retry.RetryOnConflict` for status updates?**
   - What we know: Controller-runtime docs show both patterns (return conflict vs wrap in RetryOnConflict)
   - What's unclear: Which is preferred for operators? Current code uses `Result{Requeue: true}` pattern.
   - Recommendation: Keep current `Result{Requeue: true}` pattern - it's simpler and relies on framework behavior. Only use `RetryOnConflict` if we need guaranteed status update within single reconciliation (not typical).

2. **How many external deletion recreation attempts before giving up?**
   - What we know: Current code has no limit (can recurse infinitely)
   - What's unclear: Should we track attempts in annotations? Use event counting?
   - Recommendation: Single retry (check if ID is empty). If recreation fails, return error for exponential backoff. Let user intervene if pipeline keeps getting deleted externally.

3. **Should validation errors emit events on every reconciliation?**
   - What we know: Current code emits event each time validation error occurs
   - What's unclear: This could spam events if reconciliation is triggered frequently
   - Recommendation: Keep current behavior - events show what's happening. Validation errors don't retry, so events are only emitted when user changes spec.

## Sources

### Primary (HIGH confidence)
- [controller-runtime reconcile package docs](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/reconcile) - Result and error return semantics
- [Error Back-off with Controller Runtime](https://stuartleeks.com/posts/error-back-off-with-controller-runtime/) - Explains exponential backoff behavior
- [controller-runtime FAQ](https://github.com/kubernetes-sigs/controller-runtime/blob/main/FAQ.md) - Official guidance on error handling
- [Kubebuilder Book: Implementing a controller](https://book.kubebuilder.io/cronjob-tutorial/controller-implementation) - Status update patterns
- Existing codebase: `internal/controller/pipeline_controller.go` - Current implementation patterns

### Secondary (MEDIUM confidence)
- [Kubernetes operators best practices: understanding conflict errors](https://alenkacz.medium.com/kubernetes-operators-best-practices-understanding-conflict-errors-d05353dff421) - Conflict error handling
- [Building Resilient Kubernetes Controllers: A Practical Guide to Retry Mechanisms](https://medium.com/@vamshitejanizam/building-resilient-kubernetes-controllers-a-practical-guide-to-retry-mechanisms-0d689160fa51) - Retry strategies
- [Kubernetes Controllers at Scale: Clients, Caches, Conflicts, Patches Explained](https://medium.com/@timebertt/kubernetes-controllers-at-scale-clients-caches-conflicts-patches-explained-aa0f7a8b4332) - Why conflicts happen
- [10 Things You Should Know Before Writing a Kubernetes Controller](https://medium.com/@gallettilance/10-things-you-should-know-before-writing-a-kubernetes-controller-83de8f86d659) - Common pitfalls
- [k8s.io/client-go/util/retry package](https://pkg.go.dev/k8s.io/client-go/util/retry) - RetryOnConflict documentation

### Tertiary (LOW confidence)
- [How to elegantly solve the update conflict problem - Issue #1748](https://github.com/kubernetes-sigs/controller-runtime/issues/1748) - Community discussion on conflicts
- [Reconcile is triggered after status update - Issue #2831](https://github.com/kubernetes-sigs/controller-runtime/issues/2831) - Status update reconciliation behavior

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - controller-runtime v0.23 and k8s.io/apimachinery are official, stable APIs
- Architecture patterns: HIGH - Verified against controller-runtime documentation and existing codebase
- Error classification: HIGH - FleetAPIError.IsTransient() already implemented in Phase 1
- Recursion limit: HIGH - Clear bug in current code (line 269), fix pattern is straightforward
- Status error preservation: HIGH - Well-documented controller-runtime pattern, existing code has the bug

**Research date:** 2026-02-08
**Valid until:** 60 days - controller-runtime is stable, error handling patterns don't change frequently
