# Error Handling Architecture for Controller-Runtime Operators

**Domain:** Kubernetes Operator Error Handling
**Researched:** 2026-02-08
**Confidence:** HIGH

## Executive Summary

Error handling in controller-runtime operators follows a layered architecture where errors are caught, enriched with context, and returned through structured patterns. The key principle is that **controllers should NOT swallow errors** - instead, they should return errors to leverage exponential backoff, use status conditions for observability, and handle specific error types (conflicts, not-found) with appropriate strategies.

This research focuses on refactoring error handling in an existing controller without major architectural changes, integrating proper error flows into the current `PipelineReconciler` structure.

## Standard Architecture

### System Overview

```
┌─────────────────────────────────────────────────────────────┐
│                    Reconciliation Layer                      │
│  ┌────────────────────────────────────────────────────────┐  │
│  │  Reconcile(ctx, req) → (Result, error)                 │  │
│  │    ↓                                                    │  │
│  │  reconcileNormal / reconcileDelete                     │  │
│  └────────────────────────────────────────────────────────┘  │
├─────────────────────────────────────────────────────────────┤
│                   Error Handling Layer                       │
│  ┌──────────────┐  ┌─────────────┐  ┌──────────────┐        │
│  │ Error        │  │ Status      │  │ K8s API      │        │
│  │ Classification│  │ Update      │  │ Error Check  │        │
│  │ (API/Network)│  │ (Conditions)│  │ (IsConflict) │        │
│  └──────────────┘  └─────────────┘  └──────────────┘        │
├─────────────────────────────────────────────────────────────┤
│                      Client Layer                            │
│  ┌────────────────────────────────────────────────────────┐  │
│  │  FleetPipelineClient Interface                         │  │
│  │    - UpsertPipeline(ctx, req) → (*Pipeline, error)    │  │
│  │    - DeletePipeline(ctx, id) → error                  │  │
│  └────────────────────────────────────────────────────────┘  │
├─────────────────────────────────────────────────────────────┤
│                   HTTP/Network Layer                         │
│  ┌──────────────────────────────────────────────────────┐    │
│  │  HTTP Client with Rate Limiting                       │    │
│  │    - Transient error detection (5xx, timeout, EOF)   │    │
│  │    - Status code → FleetAPIError mapping             │    │
│  │    - Context propagation for cancellation            │    │
│  └──────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────┘
```

### Component Responsibilities

| Component | Responsibility | Error Handling Role |
|-----------|----------------|---------------------|
| **Reconcile Entry Point** | Fetch resource, route to reconcileNormal/Delete | Handle NotFound errors, add finalizers |
| **reconcileNormal** | Build request, call API, update status | Delegate to handleAPIError on failure |
| **reconcileDelete** | Delete from external API, remove finalizer | Handle 404 gracefully (already deleted = success) |
| **handleAPIError** | Classify Fleet API errors, emit events | Map HTTP status → appropriate Result/error |
| **updateStatusSuccess** | Update status conditions on success | Handle conflict errors with Requeue |
| **updateStatusError** | Update status conditions on failure | Handle conflict errors with Requeue, return original error |
| **FleetClient** | HTTP operations with rate limiting | Return typed errors (FleetAPIError), wrap network errors |

## Recommended Error Flow Architecture

### Error Flow Through Layers

```
[External API Error] (e.g., 400, 500, network failure)
    ↓
[HTTP Client] → Wraps as FleetAPIError OR network error
    ↓
[FleetClient Interface] → Returns error to controller
    ↓
[reconcileNormal/Delete] → Calls handleAPIError
    ↓
[handleAPIError] → Classifies error, emits event, calls updateStatusError
    ↓
[updateStatusError] → Updates status conditions, returns original error
    ↓
[Reconcile] → Returns (Result, error) to controller-runtime
    ↓
[Controller-Runtime] → Applies exponential backoff, requeues
```

### Error Classification Decision Tree

```
Error received in reconcileNormal/Delete
    │
    ├─ Is FleetAPIError?
    │   ├─ 400 (Bad Request) → ValidationError
    │   │   - Update status with ValidationError condition
    │   │   - Return (Result{}, nil) [don't retry invalid config]
    │   │
    │   ├─ 404 (Not Found) → Context-dependent
    │   │   - In UpsertPipeline: External deletion detected, retry with cleared ID
    │   │   - In DeletePipeline: Already deleted, return success
    │   │
    │   ├─ 429 (Rate Limited) → Requeue with delay
    │   │   - Return (Result{RequeueAfter: 10s}, nil)
    │   │
    │   └─ 5xx or other → Retriable API error
    │       - Update status with SyncFailed condition
    │       - Return (Result{}, error) [exponential backoff]
    │
    └─ Is network/context error?
        - Update status with SyncFailed condition
        - Return (Result{}, error) [exponential backoff]
```

### Status Update Error Handling

```
Status Update Attempted
    │
    ├─ Success → Continue
    │
    └─ Error?
        ├─ IsConflict(err)?
        │   └─ YES → Return (Result{Requeue: true}, nil)
        │       [Resource was modified, requeue to reconcile with fresh data]
        │
        └─ NO → Return (Result{}, err)
            [Unexpected error, use exponential backoff]
```

## Architectural Patterns

### Pattern 1: Error Wrapping with Context

**What:** Use `fmt.Errorf` with `%w` to wrap errors while adding context at each layer.

**When to use:** At every layer boundary when passing errors up the stack.

**Trade-offs:**
- **Pro:** Preserves error types for `errors.Is()` and `errors.As()` checks
- **Pro:** Builds clear error trail showing failure path
- **Con:** Can create verbose error messages if over-wrapped
- **Con:** Wrapping status update errors can lose retryable error types

**Example:**
```go
// Client layer - wrap network errors
resp, err := c.httpClient.Do(httpReq)
if err != nil {
    return nil, fmt.Errorf("failed to execute HTTP request: %w", err)
}

// Controller layer - add operation context
pipeline, err := r.FleetClient.UpsertPipeline(ctx, req)
if err != nil {
    return r.handleAPIError(ctx, pipeline, err)
}

// DON'T wrap when error already has clear message
// Status update errors are already descriptive
if err := r.Status().Update(ctx, pipeline); err != nil {
    return ctrl.Result{}, err  // Don't wrap
}
```

### Pattern 2: Typed Errors for Classification

**What:** Create custom error types that implement `error` interface and carry structured information (HTTP status, operation name, etc.).

**When to use:** At client/API boundaries where errors have distinct handling requirements.

**Trade-offs:**
- **Pro:** Enables type-based error handling decisions
- **Pro:** Carries structured metadata for logging/metrics
- **Con:** Requires type assertions/checks at call sites
- **Con:** Must maintain error type definitions

**Example:**
```go
// Define typed error
type FleetAPIError struct {
    StatusCode int
    Operation  string
    Message    string
    PipelineID string  // Add context fields
}

func (e *FleetAPIError) Error() string {
    return fmt.Sprintf("%s failed (HTTP %d): %s", e.Operation, e.StatusCode, e.Message)
}

// Return typed errors from client
if resp.StatusCode != http.StatusOK {
    bodyBytes, _ := io.ReadAll(resp.Body)
    return nil, &FleetAPIError{
        StatusCode: resp.StatusCode,
        Operation:  "UpsertPipeline",
        Message:    string(bodyBytes),
        PipelineID: req.Pipeline.ID,
    }
}

// Check error types in controller
if apiErr, ok := err.(*FleetAPIError); ok {
    switch apiErr.StatusCode {
    case http.StatusBadRequest:
        // Handle validation errors
    case http.StatusTooManyRequests:
        // Handle rate limits
    }
}
```

### Pattern 3: Status Conditions as Error State

**What:** Use Kubernetes Status Conditions to represent error states, not just for observability but as the source of truth for resource state.

**When to use:** For all reconciliation outcomes - success and failure.

**Trade-offs:**
- **Pro:** Standard Kubernetes pattern for observability
- **Pro:** Queryable by kubectl, monitoring tools
- **Pro:** Supports multiple concurrent conditions (Ready, Synced, ValidationError)
- **Con:** Requires careful ObservedGeneration management
- **Con:** Eventual consistency - status updates may fail

**Example:**
```go
// Success condition
meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
    Type:               "Ready",
    Status:             metav1.ConditionTrue,
    Reason:             "Synced",
    Message:            "Pipeline successfully synced to Fleet Management",
    ObservedGeneration: pipeline.Generation,
})

// Error condition
meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
    Type:               "Ready",
    Status:             metav1.ConditionFalse,
    Reason:             "ValidationError",
    Message:            err.Error(),
    ObservedGeneration: pipeline.Generation,
})

// Always update ObservedGeneration to prevent repeated reconciliation
pipeline.Status.ObservedGeneration = pipeline.Generation
```

### Pattern 4: Return Error vs Requeue Decision

**What:** Choose between returning an error (exponential backoff) or Result with Requeue/RequeueAfter (explicit timing).

**When to use:**
- **Return error:** Transient failures (network, 5xx, unexpected errors)
- **Return Result{RequeueAfter: duration}:** Known wait conditions (rate limit, polling)
- **Return Result{Requeue: true}:** Status update conflicts (rare, prefer returning error)
- **Return Result{}:** Success or validation errors (don't retry)

**Trade-offs:**
- **Error return:** Automatic exponential backoff (good for transient failures), but no control over timing
- **RequeueAfter:** Precise control over retry timing, but no backoff for repeated failures
- **Requeue: true:** Applies backoff, but deprecated in favor of error return

**Example:**
```go
// Pattern 1: Transient failure - use error for exponential backoff
apiPipeline, err := r.FleetClient.UpsertPipeline(ctx, req)
if err != nil {
    if apiErr, ok := err.(*fleetclient.FleetAPIError); ok {
        if apiErr.StatusCode >= 500 {
            // Transient server error - return error for backoff
            return ctrl.Result{}, fmt.Errorf("fleet API server error: %w", err)
        }
    }
}

// Pattern 2: Rate limit - use RequeueAfter for explicit timing
if apiErr.StatusCode == http.StatusTooManyRequests {
    log.Info("rate limited, requeueing after 10 seconds")
    r.emitEvent(pipeline, corev1.EventTypeWarning, "RateLimited",
        "Rate limited by API, will retry in 10 seconds")
    return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// Pattern 3: Validation error - don't retry
if apiErr.StatusCode == http.StatusBadRequest {
    log.Info("validation error, not requeueing")
    r.updateStatusError(ctx, pipeline, "ValidationError", err)
    return ctrl.Result{}, nil  // No error, no requeue
}

// Pattern 4: Status update conflict - requeue for fresh data
if err := r.Status().Update(ctx, pipeline); err != nil {
    if apierrors.IsConflict(err) {
        log.V(1).Info("status update conflict, requeueing")
        return ctrl.Result{Requeue: true}, nil
    }
    return ctrl.Result{}, err
}
```

### Pattern 5: Structured Logging with Error Context

**What:** Use structured logging (key-value pairs) to log errors with relevant context for debugging.

**When to use:** At each error handling point, before returning errors up the stack.

**Trade-offs:**
- **Pro:** Enables log aggregation and filtering
- **Pro:** Provides context without parsing log strings
- **Con:** More verbose than simple string logging
- **Con:** Requires discipline to maintain consistency

**Example:**
```go
// Controller layer - log with operation context
log := logf.FromContext(ctx)
if err != nil {
    log.Error(err, "failed to sync with Fleet Management",
        "operation", "UpsertPipeline",
        "pipelineID", pipeline.Status.ID,
        "pipelineName", pipeline.Name,
        "namespace", pipeline.Namespace,
    )
}

// Client layer - log with request details
if resp.StatusCode != http.StatusOK {
    // Note: Don't log sensitive data like auth tokens
    log.V(1).Info("Fleet API error",
        "statusCode", resp.StatusCode,
        "operation", "UpsertPipeline",
        "pipelineName", req.Pipeline.Name,
    )
}

// Use log levels appropriately
// Error: Unexpected failures requiring attention
// Info: Normal operations (create, update, delete)
// V(1): Debug information (skipped reconciliation, conflicts)
```

## Integration Points for Current Architecture

Based on analysis of `/Users/mbaykara/work/fleet-management-operator/internal/controller/pipeline_controller.go` and `/Users/mbaykara/work/fleet-management-operator/pkg/fleetclient/client.go`, here are the specific integration points for error handling improvements:

### 1. Client Layer Improvements

**Current state:**
- `FleetAPIError` exists but only contains StatusCode, Operation, Message
- Network errors wrapped with `fmt.Errorf` but not consistently
- No distinction between transient vs permanent errors

**Integration point:**
```go
// In pkg/fleetclient/client.go

// Enhance FleetAPIError with more context
type FleetAPIError struct {
    StatusCode int
    Operation  string
    Message    string
    PipelineID string      // NEW: Add for tracing
    IsTransient bool       // NEW: Flag for retry logic
}

// Add method to determine if error is transient
func (e *FleetAPIError) IsTransient() bool {
    return e.StatusCode >= 500 || e.StatusCode == http.StatusTooManyRequests
}

// In UpsertPipeline, enhance error creation
if resp.StatusCode != http.StatusOK {
    bodyBytes, _ := io.ReadAll(resp.Body)
    apiErr := &FleetAPIError{
        StatusCode: resp.StatusCode,
        Operation:  "UpsertPipeline",
        Message:    string(bodyBytes),
        PipelineID: req.Pipeline.ID,  // NEW
    }
    apiErr.IsTransient = apiErr.StatusCode >= 500 ||
                         apiErr.StatusCode == http.StatusTooManyRequests
    return nil, apiErr
}

// For network errors, wrap with context
resp, err := c.httpClient.Do(httpReq)
if err != nil {
    // Check for specific error types
    if errors.Is(err, context.Canceled) {
        return nil, fmt.Errorf("request canceled: %w", err)
    }
    if errors.Is(err, context.DeadlineExceeded) {
        return nil, fmt.Errorf("request timeout: %w", err)
    }
    // Generic network error
    return nil, fmt.Errorf("network error during %s: %w", "UpsertPipeline", err)
}
```

### 2. Controller Layer Improvements

**Current state:**
- `handleAPIError` exists and handles typed errors well
- `updateStatusError` exists but returns the update error, not the original error
- Some error paths don't update status conditions

**Integration point:**
```go
// In internal/controller/pipeline_controller.go

// FIX: updateStatusError should return original error, not status update error
func (r *PipelineReconciler) updateStatusError(ctx context.Context,
    pipeline *fleetmanagementv1alpha1.Pipeline,
    reason string,
    originalErr error) (ctrl.Result, error) {

    log := logf.FromContext(ctx)

    // Update observedGeneration
    pipeline.Status.ObservedGeneration = pipeline.Generation

    // Set conditions
    meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
        Type:               conditionTypeReady,
        Status:             metav1.ConditionFalse,
        Reason:             reason,
        Message:            originalErr.Error(),
        ObservedGeneration: pipeline.Generation,
    })

    meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
        Type:               conditionTypeSynced,
        Status:             metav1.ConditionFalse,
        Reason:             reason,
        Message:            originalErr.Error(),
        ObservedGeneration: pipeline.Generation,
    })

    // Update status - handle conflicts
    if updateErr := r.Status().Update(ctx, pipeline); updateErr != nil {
        if apierrors.IsConflict(updateErr) {
            log.V(1).Info("status update conflict, requeueing",
                "originalError", originalErr.Error())
            // IMPORTANT: Requeue to get fresh resource, but log original error
            return ctrl.Result{Requeue: true}, nil
        }
        // CHANGED: Log status update failure but return original error
        log.Error(updateErr, "failed to update status",
            "originalError", originalErr.Error())
        return ctrl.Result{}, originalErr  // Return original error
    }

    // For validation errors, don't retry
    if reason == reasonValidationError {
        log.Info("validation error, not requeueing", "error", originalErr.Error())
        return ctrl.Result{}, nil
    }

    // For other errors, return original error for exponential backoff
    return ctrl.Result{}, originalErr
}

// ADD: Helper to check if error is transient
func isTransientError(err error) bool {
    // Check for FleetAPIError
    var apiErr *fleetclient.FleetAPIError
    if errors.As(err, &apiErr) {
        return apiErr.IsTransient()
    }

    // Check for network errors
    if errors.Is(err, context.DeadlineExceeded) ||
       errors.Is(err, io.EOF) ||
       errors.Is(err, io.ErrUnexpectedEOF) {
        return true
    }

    return false
}
```

### 3. Finalizer Error Handling

**Current state:**
- `reconcileDelete` handles 404 correctly
- But doesn't update status on delete failures

**Integration point:**
```go
// In reconcileDelete
if err := r.FleetClient.DeletePipeline(ctx, pipeline.Status.ID); err != nil {
    if apiErr, ok := err.(*fleetclient.FleetAPIError); ok &&
       apiErr.StatusCode == http.StatusNotFound {
        // Already deleted - success
        log.Info("pipeline already deleted from Fleet Management")
        r.emitEvent(pipeline, corev1.EventTypeNormal, eventReasonDeleted,
            "Pipeline already deleted from Fleet Management")
    } else {
        // Delete failed - update status before returning error
        log.Error(err, "failed to delete pipeline from Fleet Management")
        r.emitEventf(pipeline, corev1.EventTypeWarning, eventReasonDeleteFailed,
            "Failed to delete pipeline: %v", err)

        // NEW: Update status to reflect deletion failure
        return r.updateStatusError(ctx, pipeline, reasonDeleteFailed, err)
    }
}
```

### 4. Logging Enhancements

**Current state:**
- Uses structured logging inconsistently
- Some error logs missing context

**Integration point:**
```go
// Standardize structured logging across controller

// In Reconcile
log.Info("reconciling Pipeline",
    "namespace", req.Namespace,
    "name", req.Name,
    "generation", pipeline.Generation,
    "observedGeneration", pipeline.Status.ObservedGeneration,
)

// In handleAPIError
log.Error(err, "Fleet Management API error",
    "statusCode", apiErr.StatusCode,
    "operation", apiErr.Operation,
    "pipelineID", apiErr.PipelineID,  // NEW field
    "pipelineName", pipeline.Name,
    "namespace", pipeline.Namespace,
)

// In reconcileNormal on success
log.Info("successfully synced pipeline",
    "id", apiPipeline.ID,
    "generation", pipeline.Generation,
    "observedGeneration", pipeline.Status.ObservedGeneration,
)
```

## Build Order and Dependencies

For minimal disruption, implement error handling fixes in this order:

### Phase 1: Client Layer Foundation (No Breaking Changes)
**Goal:** Improve error information without changing interfaces

1. Enhance `FleetAPIError` type with additional fields:
   - Add `PipelineID` field
   - Add `IsTransient` helper method
   - Improve `Error()` message formatting

2. Improve error wrapping in client methods:
   - Wrap context cancellation/timeout errors explicitly
   - Add operation context to network errors
   - Ensure consistent error wrapping with `%w`

3. Add tests for error types:
   - Test `FleetAPIError` type assertions
   - Test error wrapping preserves types
   - Test transient error detection

**Dependencies:** None (standalone client improvements)
**Risk:** Low (backwards compatible, only adds information)

### Phase 2: Controller Error Handling (Fixes Logic Bugs)
**Goal:** Fix error return paths to use correct patterns

1. Fix `updateStatusError` to return original error:
   - Save original error before status update
   - Handle status update conflicts with Requeue
   - Return original error for exponential backoff

2. Fix `updateStatusSuccess` conflict handling:
   - Current code returns `(Result{Requeue: true}, nil)` on conflict ✓ (correct)
   - Add similar logging as updateStatusError

3. Add error classification helpers:
   - `isTransientError(err)` function
   - `shouldRetry(err)` function

4. Enhance logging consistency:
   - Add structured context to all error logs
   - Use consistent field names (pipelineID, operation, etc.)

**Dependencies:** Phase 1 (uses enhanced FleetAPIError)
**Risk:** Medium (changes error return semantics, needs thorough testing)

### Phase 3: Status Condition Improvements (Optional Enhancement)
**Goal:** Better observability through status conditions

1. Add more specific condition types:
   - Consider splitting "Synced" into "Created"/"Updated"
   - Add "Deleting" condition during finalizer execution

2. Improve condition messages:
   - Include error codes in messages
   - Add troubleshooting hints for common errors

3. Add condition transitions logging:
   - Log when conditions change state
   - Emit events on condition state changes

**Dependencies:** Phase 2 (builds on fixed error handling)
**Risk:** Low (purely additive observability improvements)

### Phase 4: Metrics and Monitoring (Future Enhancement)
**Goal:** Production observability

1. Add Prometheus metrics:
   - Reconciliation errors by type
   - API error rates by status code
   - Status update conflict rate

2. Add tracing support:
   - Propagate trace context through client calls
   - Add spans for API operations

**Dependencies:** Phase 3 (requires stable error handling)
**Risk:** Low (additive feature, no changes to core logic)

## Anti-Patterns to Avoid

### Anti-Pattern 1: Swallowing Errors

**What people do:**
```go
// BAD: Swallow error and just requeue
if err != nil {
    log.Error(err, "sync failed")
    return ctrl.Result{Requeue: true}, nil  // Lost error!
}
```

**Why it's wrong:**
- Loses exponential backoff (will retry at fixed rate)
- Error not surfaced to controller-runtime metrics
- Can cause rapid retry loops

**Do this instead:**
```go
// GOOD: Return error for proper handling
if err != nil {
    log.Error(err, "sync failed")
    return ctrl.Result{}, err  // Controller-runtime handles backoff
}
```

### Anti-Pattern 2: Using RetryOnConflict for Status Updates

**What people do:**
```go
// BAD: Retry status update in reconciler
err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
    pipeline := &fleetmanagementv1alpha1.Pipeline{}
    if err := r.Get(ctx, req.NamespacedName, pipeline); err != nil {
        return err
    }
    pipeline.Status.ID = "some-id"
    return r.Status().Update(ctx, pipeline)
})
```

**Why it's wrong:**
- Controller makes decisions based on stale data
- Conflict indicates resource changed - should re-reconcile with fresh data
- Hides that controller is operating on outdated state

**Do this instead:**
```go
// GOOD: Let conflict errors trigger requeue
if err := r.Status().Update(ctx, pipeline); err != nil {
    if apierrors.IsConflict(err) {
        // Resource changed, requeue to reconcile with fresh data
        return ctrl.Result{Requeue: true}, nil
    }
    return ctrl.Result{}, err
}
```

### Anti-Pattern 3: Wrapping Status Update Errors

**What people do:**
```go
// BAD: Wrap the status update error
if err := r.Status().Update(ctx, pipeline); err != nil {
    return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
}
```

**Why it's wrong:**
- Loses the specific error type (IsConflict, IsNotFound)
- Cannot handle conflicts appropriately
- Error message misleading (focuses on status update, not original problem)

**Do this instead:**
```go
// GOOD: Return status update error unwrapped
if err := r.Status().Update(ctx, pipeline); err != nil {
    if apierrors.IsConflict(err) {
        return ctrl.Result{Requeue: true}, nil
    }
    // Return unwrapped for type checking
    return ctrl.Result{}, err
}
```

### Anti-Pattern 4: Not Checking for NotFound

**What people do:**
```go
// BAD: Return error immediately
pipeline := &fleetmanagementv1alpha1.Pipeline{}
if err := r.Get(ctx, req.NamespacedName, pipeline); err != nil {
    return ctrl.Result{}, err  // Will retry NotFound forever
}
```

**Why it's wrong:**
- NotFound is not an error for reconcilers (resource was deleted)
- Causes unnecessary error logs and metrics
- Wastes CPU on pointless retries

**Do this instead:**
```go
// GOOD: Handle NotFound as success
pipeline := &fleetmanagementv1alpha1.Pipeline{}
if err := r.Get(ctx, req.NamespacedName, pipeline); err != nil {
    if apierrors.IsNotFound(err) {
        log.Info("Pipeline not found, likely deleted")
        return ctrl.Result{}, nil  // Success, resource deleted
    }
    log.Error(err, "failed to get Pipeline")
    return ctrl.Result{}, err
}
```

### Anti-Pattern 5: Logging Without Structured Context

**What people do:**
```go
// BAD: String concatenation logging
log.Error(err, "failed to sync pipeline " + pipeline.Name)
```

**Why it's wrong:**
- Cannot filter/aggregate logs by field values
- Hard to extract data programmatically
- String formatting overhead

**Do this instead:**
```go
// GOOD: Structured key-value logging
log.Error(err, "failed to sync pipeline",
    "pipelineName", pipeline.Name,
    "namespace", pipeline.Namespace,
    "generation", pipeline.Generation,
)
```

## Scalability Considerations

| Scale | Error Handling Approach |
|-------|------------------------|
| 0-100 resources | Current patterns sufficient - exponential backoff prevents overload |
| 100-1000 resources | Consider metrics for error rate monitoring, may need to tune backoff parameters |
| 1000+ resources | Add circuit breaker to prevent API overload during outages, implement error rate limiting per resource, consider dedicated retry queues for different error types |

### Scaling Priorities

1. **First bottleneck: API rate limits during mass reconciliation**
   - Detection: Spike in 429 responses during controller restart or mass resource creation
   - Fix: Implement controller-side rate limiting (beyond client rate limiter), stagger initial reconciliations, add jitter to retry timing

2. **Second bottleneck: Status update conflicts at scale**
   - Detection: High rate of status update conflicts in logs
   - Fix: Add optimistic locking awareness (check resourceVersion before update), consider batching status updates where possible, tune conflict requeue timing

## Sources

### Controller-Runtime Documentation (HIGH confidence)
- [reconcile package documentation](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/reconcile) - Result types and error semantics
- [controller-runtime logging package](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/log) - Structured logging patterns
- [controller-runtime FAQ](https://github.com/kubernetes-sigs/controller-runtime/blob/main/FAQ.md) - Common patterns

### Official Kubernetes/Operator Best Practices (HIGH confidence)
- [Kubebuilder Good Practices](https://book.kubebuilder.io/reference/good-practices) - Idempotency and status conditions
- [Operator SDK Common Recommendations](https://sdk.operatorframework.io/docs/best-practices/common-recommendation/) - Error handling and testing
- [k8s.io/apimachinery errors package](https://pkg.go.dev/k8s.io/apimachinery/pkg/api/errors) - IsConflict, IsNotFound patterns

### Error Handling Patterns (MEDIUM confidence)
- [Error Back-off with Controller Runtime](https://stuartleeks.com/posts/error-back-off-with-controller-runtime/) - When to return error vs requeue
- [Understanding Conflict Errors](https://alenkacz.medium.com/kubernetes-operators-best-practices-understanding-conflict-errors-d05353dff421) - Why not to use RetryOnConflict
- [Kubernetes Controllers at Scale](https://medium.com/@timebertt/kubernetes-controllers-at-scale-clients-caches-conflicts-patches-explained-aa0f7a8b4332) - Conflict handling at scale

### Go Error Handling (MEDIUM confidence)
- [Error Handling Patterns in Go](https://medium.com/@virtualik/error-handling-patterns-in-go-every-developer-should-know-8962777c935b) - Error wrapping with %w
- [Master Advanced Error Handling & Logging in Go](https://medium.com/@romulo.gatto/master-advanced-error-handling-logging-in-go-7d951d12e4a2) - Structured logging
- [How to Implement Retry Logic in Go](https://oneuptime.com/blog/post/2026-01-07-go-retry-exponential-backoff/view) - Exponential backoff patterns

### HTTP Client Error Handling (MEDIUM confidence)
- [hashicorp/go-retryablehttp](https://github.com/hashicorp/go-retryablehttp) - Transient error retry patterns
- [Go HTTP Client Patterns](https://jsschools.com/golang/go-http-client-patterns-a-production-ready-implem/) - Production-ready HTTP clients
- [Building Resilient Go Services](https://medium.com/@serifcolakel/building-resilient-go-services-context-graceful-shutdown-and-retry-timeout-patterns-041eea332162) - Context and retry patterns

---
*Architecture research for: Controller-Runtime Error Handling*
*Researched: 2026-02-08*
*Based on analysis of: /Users/mbaykara/work/fleet-management-operator/internal/controller/pipeline_controller.go, /Users/mbaykara/work/fleet-management-operator/pkg/fleetclient/client.go*
