# Phase 3: Logging & Quality - Research

**Researched:** 2026-02-08
**Domain:** Go structured logging and Kubernetes controller observability
**Confidence:** HIGH

## Summary

Phase 3 focuses on establishing production-grade observability across the controller and ensuring all code paths have consistent structured logging. The current codebase uses controller-runtime's logr-based logging (via logf.FromContext) with key-value pairs, which is the standard approach. However, there are gaps: not all error paths include consistent context (namespace/name), status condition messages lack troubleshooting hints, and condition state transitions aren't logged for debugging.

Controller-runtime uses logr as its structured logging interface, which is backed by go-logr/zapr (wrapping uber-go/zap) in this project. The key pattern is extracting the logger from context (`logf.FromContext(ctx)`) and using structured key-value pairs instead of string formatting. The logger automatically includes controller metadata, but pipeline-specific context (namespace, name) must be added explicitly to every log statement. Status conditions are the primary debugging interface for users - messages must be actionable, not just error strings.

**Primary recommendation:** Audit all logging statements to ensure namespace/name are included, enhance status condition messages with troubleshooting hints ("Check X", "Verify Y"), log all condition state transitions (Ready: False->True), and add code comments explaining non-obvious error handling decisions. No new dependencies needed - use existing logr patterns consistently.

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| sigs.k8s.io/controller-runtime/pkg/log | v0.23.0 | Structured logging abstraction (logr) | Official controller-runtime logging interface, used by all Kubernetes controllers |
| github.com/go-logr/logr | v1.4.2 | Logging interface specification | Industry standard for structured logging in Go, allows backend swapping |
| github.com/go-logr/zapr | v1.3.0 | Zap backend for logr | High-performance structured logger, already configured in main.go:89 |
| go.uber.org/zap | v1.27.0 | Underlying logging implementation | Production-ready structured logger with zero-allocation fast paths |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| k8s.io/apimachinery/pkg/api/meta | v0.35.0 | Status condition helpers | SetStatusCondition, FindStatusCondition for managing conditions |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| logr/zapr | logrus, zerolog | logr is the Kubernetes standard. Changing would break controller-runtime integration and monitoring tools. |
| Structured key-value logging | fmt.Printf-style logging | Structured logging enables log aggregation, filtering, and analysis. String formatting loses structure. |
| Status conditions | Custom status fields | Conditions are the Kubernetes convention. Custom fields don't integrate with kubectl, client-go predicates, or tooling. |

**Installation:**
All dependencies already present in go.mod. No additional packages needed.

## Architecture Patterns

### Current Logging Pattern in Codebase

The project already uses the correct logging pattern, but inconsistently applied:

```
pipeline_controller.go logging structure:
- Reconcile() entry point: log := logf.FromContext(ctx)
- All functions extract logger: log := logf.FromContext(ctx)
- Structured logging: log.Info("message", "key", value, "key2", value2)
- Verbosity levels: log.V(1).Info() for debug-level logs
- Error logging: log.Error(err, "message", "key", value)
```

**Current gaps:**
1. Some log statements lack namespace/name context
2. Error paths missing structured fields (statusCode, operation)
3. No logging for condition state transitions
4. Status condition messages are raw error strings, not user-friendly

### Pattern 1: Consistent Context in All Log Statements

**What:** Every log statement includes namespace and name of the Pipeline being reconciled.

**Why:** When multiple pipelines reconcile concurrently, logs become interleaved. Without namespace/name in every statement, it's impossible to correlate logs to specific resources. Controller-runtime doesn't automatically add this context - it must be explicit.

**Current implementation (INCONSISTENT):**
```go
// From pipeline_controller.go:112 - Has context
log.Info("reconciling Pipeline", "namespace", req.Namespace, "name", req.Name)

// From pipeline_controller.go:144 - Has context
log.V(1).Info("pipeline already reconciled, skipping", "generation", pipeline.Generation)

// From pipeline_controller.go:260 - MISSING namespace/name
log.Info("validation error from Fleet Management API", "message", apiErr.Message)

// From pipeline_controller.go:306 - Has SOME context
log.Error(err, "Fleet Management API error",
    "statusCode", apiErr.StatusCode,
    "operation", apiErr.Operation,
    "pipelineID", pipeline.Status.ID,
    "message", apiErr.Message)
```

**Correct implementation:**
```go
// Standard pattern: include namespace/name in EVERY log statement
log := logf.FromContext(ctx)

// Info logs
log.Info("validation error from Fleet Management API",
    "namespace", pipeline.Namespace,
    "name", pipeline.Name,
    "message", apiErr.Message)

// Error logs
log.Error(err, "Fleet Management API error",
    "namespace", pipeline.Namespace,
    "name", pipeline.Name,
    "statusCode", apiErr.StatusCode,
    "operation", apiErr.Operation,
    "pipelineID", pipeline.Status.ID)

// Debug logs (V level)
log.V(1).Info("status update conflict, requeueing",
    "namespace", pipeline.Namespace,
    "name", pipeline.Name,
    "generation", pipeline.Generation)
```

### Pattern 2: Actionable Status Condition Messages

**What:** Status condition messages include troubleshooting guidance, not just raw error text.

**Why:** Users debug operator issues via `kubectl describe pipeline <name>` which shows status conditions. Raw error messages like "HTTP 400 (failed to read response body: EOF)" don't tell users what to do. Good messages explain the problem AND suggest next steps.

**Current implementation (NOT ACTIONABLE):**
```go
// From pipeline_controller.go:348, 357
meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
    Type:               conditionTypeReady,
    Status:             metav1.ConditionTrue,
    Reason:             reasonSynced,
    Message:            "Pipeline successfully synced to Fleet Management",
    ObservedGeneration: pipeline.Generation,
})

// From pipeline_controller.go:400 (in updateStatusError)
meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
    Type:               conditionTypeReady,
    Status:             metav1.ConditionFalse,
    Reason:             reason,
    Message:            originalErr.Error(), // RAW ERROR STRING
    ObservedGeneration: pipeline.Generation,
})
```

**Correct implementation:**
```go
// Helper function to create user-friendly error messages
func formatErrorMessage(reason string, err error) string {
    var apiErr *fleetclient.FleetAPIError
    if errors.As(err, &apiErr) {
        switch apiErr.StatusCode {
        case http.StatusBadRequest:
            return fmt.Sprintf("Configuration validation failed: %s. Review pipeline contents for syntax errors.", apiErr.Message)
        case http.StatusUnauthorized:
            return fmt.Sprintf("Authentication failed: %s. Verify FLEET_MANAGEMENT_USERNAME and PASSWORD are correct.", apiErr.Message)
        case http.StatusForbidden:
            return fmt.Sprintf("Permission denied: %s. Check Fleet Management access token has pipeline:write permission.", apiErr.Message)
        case http.StatusNotFound:
            return fmt.Sprintf("Pipeline not found in Fleet Management (ID: %s). It may have been deleted externally.", pipeline.Status.ID)
        case http.StatusTooManyRequests:
            return fmt.Sprintf("Rate limited by Fleet Management API: %s. Retry will occur automatically.", apiErr.Message)
        case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
            return fmt.Sprintf("Fleet Management API unavailable: %s. Retry will occur automatically.", apiErr.Message)
        default:
            return fmt.Sprintf("API error (HTTP %d): %s", apiErr.StatusCode, apiErr.Message)
        }
    }

    // Network errors
    if errors.Is(err, context.DeadlineExceeded) {
        return fmt.Sprintf("Connection timeout: %v. Check network connectivity to Fleet Management API.", err)
    }

    // Generic error
    return fmt.Sprintf("Sync failed: %v. Check controller logs for details.", err)
}

// In updateStatusError:
meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
    Type:               conditionTypeReady,
    Status:             metav1.ConditionFalse,
    Reason:             reason,
    Message:            formatErrorMessage(reason, originalErr),
    ObservedGeneration: pipeline.Generation,
})
```

### Pattern 3: Log Condition State Transitions

**What:** When a condition changes state (Ready: True->False or False->True), log the transition with full context.

**Why:** Condition transitions are critical debugging signals. Knowing WHEN a pipeline went from Ready to not Ready (and why) helps diagnose issues. Without logging transitions, this information is lost - status conditions only show current state.

**Implementation:**
```go
// In updateStatusSuccess - log transition to Ready
func (r *PipelineReconciler) updateStatusSuccess(ctx context.Context, pipeline *fleetmanagementv1alpha1.Pipeline, apiPipeline *fleetclient.Pipeline) (ctrl.Result, error) {
    log := logf.FromContext(ctx)

    // Check current Ready condition before updating
    oldReadyCondition := meta.FindStatusCondition(pipeline.Status.Conditions, conditionTypeReady)
    wasReady := oldReadyCondition != nil && oldReadyCondition.Status == metav1.ConditionTrue

    // Update status fields
    pipeline.Status.ID = apiPipeline.ID
    pipeline.Status.ObservedGeneration = pipeline.Generation
    // ... timestamp updates ...

    // Set Ready condition
    meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
        Type:               conditionTypeReady,
        Status:             metav1.ConditionTrue,
        Reason:             reasonSynced,
        Message:            "Pipeline successfully synced to Fleet Management",
        ObservedGeneration: pipeline.Generation,
    })

    // Log state transition if changed
    if !wasReady {
        log.Info("pipeline became ready",
            "namespace", pipeline.Namespace,
            "name", pipeline.Name,
            "id", apiPipeline.ID,
            "generation", pipeline.Generation,
            "previousReason", func() string {
                if oldReadyCondition != nil {
                    return oldReadyCondition.Reason
                }
                return "Unknown"
            }())
    }

    // ... rest of function ...
}

// In updateStatusError - log transition to not Ready
func (r *PipelineReconciler) updateStatusError(ctx context.Context, pipeline *fleetmanagementv1alpha1.Pipeline, reason string, originalErr error) (ctrl.Result, error) {
    log := logf.FromContext(ctx)

    // Check current Ready condition before updating
    oldReadyCondition := meta.FindStatusCondition(pipeline.Status.Conditions, conditionTypeReady)
    wasReady := oldReadyCondition != nil && oldReadyCondition.Status == metav1.ConditionTrue

    // Update conditions
    pipeline.Status.ObservedGeneration = pipeline.Generation
    meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
        Type:               conditionTypeReady,
        Status:             metav1.ConditionFalse,
        Reason:             reason,
        Message:            formatErrorMessage(reason, originalErr),
        ObservedGeneration: pipeline.Generation,
    })

    // Log state transition if changed
    if wasReady {
        log.Error(originalErr, "pipeline became not ready",
            "namespace", pipeline.Namespace,
            "name", pipeline.Name,
            "reason", reason,
            "generation", pipeline.Generation)
    }

    // ... status update logic ...
}
```

### Pattern 4: Structured Error Logging Context

**What:** All error log statements include relevant structured fields for debugging.

**Why:** String-formatted errors lose structure. Structured fields enable log aggregation systems (Loki, ELK) to filter, group, and alert on specific error types. The pattern is: `log.Error(err, "human message", "key1", val1, "key2", val2)`.

**Required context fields by error type:**

| Error Type | Required Fields | Optional Fields |
|------------|----------------|-----------------|
| Fleet API error | namespace, name, statusCode, operation, pipelineID | message, generation |
| Network error | namespace, name, error type | retryCount |
| Status update error | namespace, name, generation, originalError | reason |
| Validation error | namespace, name, message | configType, line number if available |
| External deletion | namespace, name, previousID | |

**Example:**
```go
// Good - structured fields
log.Error(err, "Fleet Management API error",
    "namespace", pipeline.Namespace,
    "name", pipeline.Name,
    "statusCode", apiErr.StatusCode,
    "operation", apiErr.Operation,
    "pipelineID", pipeline.Status.ID,
    "message", apiErr.Message)

// Bad - string formatting
log.Error(err, fmt.Sprintf("Fleet API error for %s/%s: HTTP %d",
    pipeline.Namespace, pipeline.Name, apiErr.StatusCode))
```

### Anti-Patterns to Avoid

- **Inconsistent context:** Some logs have namespace/name, others don't. Maintain consistency across ALL log statements.
- **Raw error strings in conditions:** `Message: err.Error()` dumps technical errors into user-facing status. Use formatted messages.
- **No transition logging:** Only logging errors/successes without tracking state changes makes debugging harder.
- **String formatting instead of structured fields:** `log.Info(fmt.Sprintf("error: %v", err))` loses structure for aggregation.
- **Verbose logging in hot paths:** Don't log on EVERY reconciliation when spec hasn't changed. Use observedGeneration to skip unnecessary logs.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Custom logging library | Your own logger | logr via logf.FromContext | controller-runtime provides logger with context, automatic enrichment, and standardization |
| Status condition helpers | Manual condition manipulation | meta.SetStatusCondition, meta.FindStatusCondition | Kubernetes standard helpers handle timestamps, transitions, and consistency |
| Error message formatting | Ad-hoc string building | Dedicated formatErrorMessage helper | Centralized formatting ensures consistency and enables future localization |
| Log level management | Manual verbosity checks | log.V(level).Info() | logr provides declarative verbosity levels, filterable at runtime |

**Key insight:** Structured logging in Kubernetes is about consistency and conventions. The technical implementation (logr/zap) is mature and efficient - the challenge is applying patterns consistently across all code paths. Don't reinvent logging infrastructure; focus on consistent application of existing patterns.

## Common Pitfalls

### Pitfall 1: Missing namespace/name Context in Logs

**What goes wrong:** Log statements don't include pipeline namespace and name, making it impossible to correlate logs when multiple pipelines reconcile concurrently.

**Why it happens:** Controller-runtime logger includes controller-level context (controller name, reconciliation ID), but NOT resource-level context. Developers assume the framework adds this automatically, but it doesn't - it must be explicit.

**How to avoid:**
- Audit every log.Info() and log.Error() call
- Add "namespace" and "name" fields to ALL log statements
- Consider adding a helper that wraps the logger with resource context:
  ```go
  func resourceLogger(ctx context.Context, pipeline *Pipeline) logr.Logger {
      return logf.FromContext(ctx).WithValues(
          "namespace", pipeline.Namespace,
          "name", pipeline.Name,
      )
  }
  ```

**Warning signs:**
- Interleaved logs from multiple pipelines without resource identifiers
- Log queries that can't filter by specific pipeline
- "Which pipeline failed?" questions that can't be answered from logs
- Test: Run multiple pipelines concurrently and verify logs are distinguishable

### Pitfall 2: Non-Actionable Status Condition Messages

**What goes wrong:** Status condition messages are raw error strings (e.g., "HTTP 400 (failed to read response body: EOF)") that don't tell users what action to take.

**Why it happens:** Directly using `originalErr.Error()` as the condition message is easiest. Developers don't consider that status conditions are the PRIMARY user debugging interface - they're viewed via `kubectl describe`, not controller logs.

**How to avoid:**
- Never use raw error strings in condition messages
- Create formatErrorMessage helper that maps errors to actionable messages
- Include troubleshooting hints: "Check X", "Verify Y", "Review Z"
- Test messages by showing them to users unfamiliar with the codebase

**Warning signs:**
- Users asking "what does this error mean?" for condition messages
- Support tickets with condition messages but no actionable guidance
- Users unable to self-service error resolution
- Test: Review all condition messages - can a user fix the issue without reading code?

### Pitfall 3: Not Logging Condition State Transitions

**What goes wrong:** Logs show "pipeline synced" or "pipeline sync failed" but don't capture WHEN the condition changed state (Ready: True -> False). Historical debugging becomes impossible because you only see current state.

**Why it happens:** Conditions are updated frequently, and logging every update would be noisy. Developers skip transition logging to reduce log volume, not realizing that transitions are the HIGH-VALUE signal, not every update.

**How to avoid:**
- Check previous condition state before updating
- Log transition only when status actually changes (True->False or False->True)
- Include previous reason in transition log for context
- Pattern: "pipeline became ready (was: SyncFailed)" or "pipeline became not ready (reason: ValidationError)"

**Warning signs:**
- "When did this pipeline break?" questions can't be answered from logs
- Debugging requires polling kubectl describe in loops to catch transitions
- Post-incident analysis can't establish timeline of failures
- Test: Trigger Ready->NotReady->Ready cycle and verify all transitions are logged

### Pitfall 4: Inconsistent Structured Logging Patterns

**What goes wrong:** Some logs use structured key-value pairs, others use string formatting. This breaks log aggregation, alerting, and analysis tools that rely on structured fields.

**Why it happens:** Mixing patterns is easy - fmt.Sprintf feels natural for complex messages. Developers don't understand that log aggregation tools (Loki, ELK, Splunk) parse structured fields but treat formatted strings as opaque text.

**How to avoid:**
- Never use fmt.Sprintf for log messages with dynamic values
- Always use key-value pairs: log.Info("message", "key", value)
- Code review checklist: search for fmt.Sprintf in logging statements
- Use linter rules if available (e.g., logcheck)

**Warning signs:**
- Can't filter logs by specific error codes or pipeline IDs
- Alert rules require regex parsing instead of field matching
- Dashboard queries are brittle and break with message wording changes
- Test: Verify all logs parse as structured JSON in log aggregation system

### Pitfall 5: Over-Logging on Unchanged Resources

**What goes wrong:** Controller logs "reconciling pipeline" on every reconciliation, even when observedGeneration matches generation (spec unchanged). This floods logs and makes real errors hard to find.

**Why it happens:** Initial reconciliation log at entry point doesn't check if work is needed. Pattern is: log, then check if work needed, then return early. This creates log noise for no-op reconciliations.

**How to avoid:**
- Use log.V(1).Info() for "no-op reconciliation skipped" (debug level)
- Reserve log.Info() for actual work being performed
- Pattern: Check observedGeneration BEFORE logging work start
- Example: Line 144 correctly uses V(1) for skip message

**Warning signs:**
- Log volume scales with watch trigger rate, not actual work rate
- Logs dominated by "pipeline already reconciled, skipping" messages
- Can't find real errors in sea of no-op logs
- Test: Verify no Info-level logs when spec is unchanged

## Code Examples

Verified patterns from existing codebase and controller-runtime documentation:

### Current Logging Pattern (Correct Structure)

```go
// From pipeline_controller.go:110-112 - CORRECT
log := logf.FromContext(ctx)
log.Info("reconciling Pipeline", "namespace", req.Namespace, "name", req.Name)
```

### Structured Error Logging (Current - Needs Enhancement)

```go
// From pipeline_controller.go:306-310 - Good structure, missing namespace/name
log.Error(err, "Fleet Management API error",
    "statusCode", apiErr.StatusCode,
    "operation", apiErr.Operation,
    "pipelineID", pipeline.Status.ID,
    "message", apiErr.Message)

// ENHANCED - Add namespace/name
log.Error(err, "Fleet Management API error",
    "namespace", pipeline.Namespace,
    "name", pipeline.Name,
    "statusCode", apiErr.StatusCode,
    "operation", apiErr.Operation,
    "pipelineID", pipeline.Status.ID,
    "message", apiErr.Message)
```

### Verbosity Levels (Current - Correct)

```go
// From pipeline_controller.go:144 - CORRECT use of V(1) for debug
log.V(1).Info("pipeline already reconciled, skipping", "generation", pipeline.Generation)

// From pipeline_controller.go:365 - CORRECT use of V(1) for operational detail
log.V(1).Info("status update conflict, requeueing")
```

### Status Condition Message Enhancement

```go
// NEW - Error message formatting helper
func formatConditionMessage(reason string, err error, pipeline *Pipeline) string {
    var apiErr *fleetclient.FleetAPIError

    if errors.As(err, &apiErr) {
        switch apiErr.StatusCode {
        case http.StatusBadRequest:
            return fmt.Sprintf("Configuration validation failed: %s. Review spec.contents for syntax errors.", apiErr.Message)
        case http.StatusUnauthorized:
            return "Authentication failed. Verify Fleet Management credentials are correct (check Secret)."
        case http.StatusForbidden:
            return "Permission denied. Fleet Management access token requires pipeline:write permission."
        case http.StatusNotFound:
            if pipeline.Status.ID != "" {
                return fmt.Sprintf("Pipeline deleted externally (ID: %s). Recreating automatically.", pipeline.Status.ID)
            }
            return "Pipeline not found in Fleet Management. Creation may have failed."
        case http.StatusTooManyRequests:
            return "Rate limited by Fleet Management API. Retry will occur automatically after delay."
        case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
            return fmt.Sprintf("Fleet Management API unavailable (HTTP %d). Retry will occur automatically.", apiErr.StatusCode)
        default:
            return fmt.Sprintf("API error (HTTP %d): %s", apiErr.StatusCode, apiErr.Message)
        }
    }

    if errors.Is(err, context.DeadlineExceeded) {
        return "Connection timeout. Check network connectivity to Fleet Management API."
    }

    var netErr net.Error
    if errors.As(err, &netErr) && netErr.Timeout() {
        return "Network timeout. Check Fleet Management API endpoint is reachable."
    }

    // Default
    return fmt.Sprintf("Sync failed: %v. Check controller logs for details.", err)
}
```

### Condition State Transition Logging

```go
// NEW - Add to updateStatusSuccess
func (r *PipelineReconciler) updateStatusSuccess(ctx context.Context, pipeline *Pipeline, apiPipeline *Pipeline) (ctrl.Result, error) {
    log := logf.FromContext(ctx)

    // Capture previous Ready state
    oldCondition := meta.FindStatusCondition(pipeline.Status.Conditions, conditionTypeReady)
    wasReady := oldCondition != nil && oldCondition.Status == metav1.ConditionTrue

    // Update status (existing code)
    pipeline.Status.ID = apiPipeline.ID
    pipeline.Status.ObservedGeneration = pipeline.Generation
    // ... existing code ...

    // Set Ready=True
    meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
        Type:               conditionTypeReady,
        Status:             metav1.ConditionTrue,
        Reason:             reasonSynced,
        Message:            "Pipeline successfully synced to Fleet Management",
        ObservedGeneration: pipeline.Generation,
    })

    // NEW - Log transition if state changed
    if !wasReady {
        previousReason := "None"
        if oldCondition != nil {
            previousReason = oldCondition.Reason
        }
        log.Info("pipeline condition transitioned to Ready",
            "namespace", pipeline.Namespace,
            "name", pipeline.Name,
            "previousStatus", "False",
            "previousReason", previousReason,
            "generation", pipeline.Generation)
    }

    // ... rest of existing code ...
}
```

### Resource-Context Logger Helper

```go
// NEW - Helper to avoid repetitive namespace/name in every log
func (r *PipelineReconciler) loggerFor(ctx context.Context, pipeline *Pipeline) logr.Logger {
    return logf.FromContext(ctx).WithValues(
        "namespace", pipeline.Namespace,
        "name", pipeline.Name,
    )
}

// Usage:
func (r *PipelineReconciler) reconcileNormal(ctx context.Context, pipeline *Pipeline) (ctrl.Result, error) {
    log := r.loggerFor(ctx, pipeline)

    // Now all logs automatically include namespace/name
    log.Info("building upsert request")

    apiPipeline, err := r.FleetClient.UpsertPipeline(ctx, req)
    if err != nil {
        log.Error(err, "upsert failed", "statusCode", apiErr.StatusCode)
        return r.handleAPIError(ctx, pipeline, err)
    }

    log.Info("successfully synced", "id", apiPipeline.ID, "generation", pipeline.Generation)
    return r.updateStatusSuccess(ctx, pipeline, apiPipeline)
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| String formatting in logs | Structured key-value pairs | logr adoption (2019) | Enables log aggregation, filtering, and alerting on specific fields |
| Raw error strings in conditions | User-friendly messages with hints | Kubernetes best practices | Users can debug without reading code or logs |
| Log every reconciliation | Log only state changes | Verbosity level patterns | Reduces noise, makes errors findable |
| klog (old Kubernetes logger) | logr interface | Kubernetes 1.19+ (2020) | Pluggable backends, better structure, controller-runtime integration |

**Deprecated/outdated:**
- klog direct usage - use logr via controller-runtime
- fmt.Printf for logging - not structured, no log level control
- Manual JSON marshaling for structured logs - logr handles structure automatically
- Condition messages without actionable guidance - industry best practice now requires troubleshooting hints

## Open Questions

1. **Should we localize error messages (i18n)?**
   - What we know: Kubernetes API objects support localization, but rarely implemented
   - What's unclear: Whether Fleet Management users need non-English messages
   - Recommendation: Start with English only. Error messages are developer-facing (not end-user UI). Add localization only if customer demand exists.

2. **Should we add metrics for condition transitions?**
   - What we know: Prometheus metrics can track condition flapping, time in not-Ready state
   - What's unclear: Whether logging is sufficient or metrics add value
   - Recommendation: Phase 3 focuses on logging. Metrics can be Phase 4 if observability gaps remain after logging improvements.

3. **Should we truncate long error messages in conditions?**
   - What we know: Kubernetes recommends keeping condition messages under 256 characters
   - What's unclear: Current messages can exceed this with long API error responses
   - Recommendation: Truncate messages to 256 chars with "... (see logs for full error)" suffix. Log the full error.

4. **Should we add structured fields to Events?**
   - What we know: Kubernetes Events currently use string messages, losing structure
   - What's unclear: Whether Event aggregation tools parse structured fields
   - Recommendation: Keep Events as human-readable strings (current pattern). Logs provide structured data.

## Sources

### Primary (HIGH confidence)
- [logr documentation](https://pkg.go.dev/github.com/go-logr/logr) - Structured logging interface spec
- [controller-runtime logging](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/log) - Controller logging patterns
- [Kubernetes API Conventions - Status](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties) - Condition message guidelines
- [zapr documentation](https://pkg.go.dev/github.com/go-logr/zapr) - Zap backend for logr
- Existing codebase: cmd/main.go (zap setup), internal/controller/pipeline_controller.go (logging patterns)

### Secondary (MEDIUM confidence)
- [Kubernetes Enhancement Proposal: Structured Logging](https://github.com/kubernetes/enhancements/tree/master/keps/sig-instrumentation/1602-structured-logging) - Kubernetes structured logging migration
- [Controller-runtime Book: Logging](https://book.kubebuilder.io/reference/using-finalizers.html) - Kubebuilder logging examples
- [Writing Kubernetes Controllers: Observability Best Practices](https://www.cncf.io/blog/2021/06/21/kubernetes-controllers-observability-best-practices/) - Industry patterns
- [Effective Go Logging](https://dave.cheney.net/2015/11/05/lets-talk-about-logging) - Go logging philosophy

### Tertiary (LOW confidence)
- Community discussions on condition message length limits - Various opinions, no official limit documented
- Log aggregation vendor documentation (Loki, ELK) - Structured field parsing capabilities vary by tool

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - logr/zapr/zap are stable, controller-runtime v0.23 is production-ready
- Logging patterns: HIGH - Verified from existing codebase and controller-runtime docs
- Condition message guidance: MEDIUM - Kubernetes docs provide examples but not strict rules
- Transition logging: HIGH - Straightforward pattern, widely used in operators

**Research date:** 2026-02-08
**Valid until:** 2026-04-08 (60 days - logging patterns are stable, minimal churn expected)
