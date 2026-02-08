# Phase 1: Client Layer Error Foundation - Research

**Researched:** 2026-02-08
**Domain:** Go HTTP Client Error Handling
**Confidence:** HIGH

## Summary

Phase 1 focuses on establishing robust error handling in the Fleet Management HTTP client (`pkg/fleetclient`). The primary objectives are to fix io.ReadAll() error handling (lines 88, 139 in client.go), enhance FleetAPIError with transient error classification, and add comprehensive unit tests.

Go 1.25's error handling ecosystem is mature, with errors.Is/errors.As providing type-safe error inspection through wrapped error chains. The standard pattern uses fmt.Errorf with %w for error wrapping, which this codebase already follows consistently. HTTP client error handling requires careful classification of transient (retriable) vs permanent errors based on status codes and error types, plus proper handling of io.ReadAll failures when reading response bodies.

**Primary recommendation:** Use structured FleetAPIError type with IsTransient() method, wrap all errors with fmt.Errorf("%w"), handle io.ReadAll() failures explicitly with logging, and write table-driven tests using Ginkgo/Gomega (existing test framework).

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Standard library `errors` | Go 1.25 | Error wrapping/unwrapping with Is/As | Native error handling, zero dependencies |
| Standard library `fmt` | Go 1.25 | Error wrapping with %w verb | Standard Go error context pattern |
| Standard library `net/http` | Go 1.25 | HTTP status code constants | Native HTTP client error classification |
| Standard library `io` | Go 1.25 | ReadAll for response body reading | Standard I/O operations |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| github.com/onsi/ginkgo/v2 | v2.27.2 | BDD-style test framework | Already used in codebase (pipeline_controller_test.go) |
| github.com/onsi/gomega | v1.38.2 | Matcher/assertion library | Pairs with Ginkgo, existing test pattern |
| github.com/stretchr/testify | v1.11.1 | Alternative test assertions | Already in go.mod, alternative to Gomega |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Ginkgo/Gomega | testify/assert with table tests | Both valid. Ginkgo already used in controller tests. Consistent with existing patterns. |
| Custom error classification | github.com/hashicorp/go-retryablehttp | go-retryablehttp adds retry logic we don't need (controller handles retries via requeueing). Custom is simpler. |

**Installation:**
No new dependencies needed - all required packages already in go.mod.

## Architecture Patterns

### Recommended Project Structure
```
pkg/fleetclient/
├── client.go          # HTTP client implementation
├── types.go           # FleetAPIError and request/response types
└── client_test.go     # NEW: Unit tests for error handling
```

### Pattern 1: Enhanced FleetAPIError Structure
**What:** Structured error type with status code, operation, message, pipeline ID, and transient classification.

**When to use:** All Fleet Management API errors should use this type for consistent error handling.

**Example:**
```go
// Enhanced FleetAPIError with transient classification
type FleetAPIError struct {
    StatusCode int
    Operation  string
    Message    string
    PipelineID string  // NEW: for distributed tracing
    Wrapped    error   // NEW: for error chain compatibility
}

func (e *FleetAPIError) Error() string {
    if e.PipelineID != "" {
        return fmt.Sprintf("%s failed (pipeline=%s, status=%d): %s",
            e.Operation, e.PipelineID, e.StatusCode, e.Message)
    }
    return fmt.Sprintf("%s failed (status=%d): %s",
        e.Operation, e.StatusCode, e.Message)
}

// NEW: Enable error wrapping compatibility
func (e *FleetAPIError) Unwrap() error {
    return e.Wrapped
}

// NEW: Transient error classification
func (e *FleetAPIError) IsTransient() bool {
    // Transient: 429 (rate limit), 5xx (server errors)
    // Permanent: 4xx (client errors) except 429
    if e.StatusCode == http.StatusTooManyRequests {
        return true
    }
    if e.StatusCode >= 500 && e.StatusCode < 600 {
        return true
    }
    return false
}
```

### Pattern 2: Safe io.ReadAll with Error Handling
**What:** Always check io.ReadAll() errors and log them with full context.

**When to use:** Everywhere response bodies are read (client.go lines 88, 139).

**Example:**
```go
// Current (WRONG - ignores error):
bodyBytes, _ := io.ReadAll(resp.Body)

// Correct pattern:
bodyBytes, err := io.ReadAll(resp.Body)
if err != nil {
    // Log the read failure
    return &FleetAPIError{
        StatusCode: resp.StatusCode,
        Operation:  "UpsertPipeline",
        Message:    fmt.Sprintf("HTTP %d (failed to read response body: %v)", resp.StatusCode, err),
        Wrapped:    err,
    }
}
```

### Pattern 3: Error Classification in Controller
**What:** Use errors.As to extract FleetAPIError and call IsTransient() for retry decisions.

**When to use:** In controller reconciliation logic when handling Fleet API errors.

**Example:**
```go
// In controller (internal/controller/pipeline_controller.go):
apiPipeline, err := r.FleetClient.UpsertPipeline(ctx, req)
if err != nil {
    var apiErr *fleetclient.FleetAPIError
    if errors.As(err, &apiErr) {
        if apiErr.IsTransient() {
            // Retry: rate limit or server error
            log.Info("transient error, will retry",
                "statusCode", apiErr.StatusCode,
                "operation", apiErr.Operation)
            return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
        }
        // Permanent error: validation failure, not found, etc.
        log.Error(err, "permanent error", "statusCode", apiErr.StatusCode)
        return ctrl.Result{}, err
    }
    // Other error type (network, timeout, etc.)
    return ctrl.Result{}, fmt.Errorf("failed to upsert pipeline: %w", err)
}
```

### Pattern 4: Table-Driven Tests with Ginkgo
**What:** Use Ginkgo's DescribeTable for testing multiple error scenarios.

**When to use:** Testing error handling paths, classification, wrapping behavior.

**Example:**
```go
// In client_test.go (NEW file):
var _ = Describe("FleetAPIError", func() {
    DescribeTable("IsTransient classification",
        func(statusCode int, expectedTransient bool) {
            err := &FleetAPIError{
                StatusCode: statusCode,
                Operation:  "TestOp",
                Message:    "test error",
            }
            Expect(err.IsTransient()).To(Equal(expectedTransient))
        },
        Entry("429 is transient", http.StatusTooManyRequests, true),
        Entry("500 is transient", http.StatusInternalServerError, true),
        Entry("503 is transient", http.StatusServiceUnavailable, true),
        Entry("400 is permanent", http.StatusBadRequest, false),
        Entry("404 is permanent", http.StatusNotFound, false),
        Entry("401 is permanent", http.StatusUnauthorized, false),
    )

    It("should work with errors.As", func() {
        err := &FleetAPIError{StatusCode: 500, Operation: "test"}
        wrapped := fmt.Errorf("context: %w", err)

        var apiErr *FleetAPIError
        Expect(errors.As(wrapped, &apiErr)).To(BeTrue())
        Expect(apiErr.StatusCode).To(Equal(500))
    })
})
```

### Anti-Patterns to Avoid
- **Ignoring io.ReadAll errors:** Lines 88, 139 use `bodyBytes, _ := io.ReadAll()` - this silently drops read failures
- **Not wrapping errors:** Return FleetAPIError directly without wrapping causes loss of error chain
- **Using == for error comparison:** Use errors.Is() instead to work with wrapped errors
- **Treating all errors as retriable:** Distinguish transient (429, 5xx) from permanent (4xx) errors

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Error wrapping | Custom wrapper types | fmt.Errorf("%w") + Unwrap() method | Standard library handles error chains, works with errors.Is/errors.As |
| HTTP status classification | Manual switch statements everywhere | IsTransient() method on FleetAPIError | Centralized logic, single source of truth |
| Mock HTTP client for tests | Manual mock implementation | Existing mockFleetClient in controller_test.go pattern | Already established in codebase, interface-based mocking |
| Retry logic | Custom backoff in client | Controller's ctrl.Result{RequeueAfter: duration} | Kubernetes controller pattern separates client (stateless) from reconciliation (stateful) |

**Key insight:** Go's error handling is designed around interfaces (error, Unwrap(), Is(), As()). Custom error types should implement these interfaces, not reinvent them. The controller-runtime framework provides retry mechanisms via Result.RequeueAfter - client should be stateless and return errors with metadata for classification.

## Common Pitfalls

### Pitfall 1: Ignoring io.ReadAll() Errors
**What goes wrong:** Network failures during body reading are silently ignored, leaving empty error messages.

**Why it happens:** io.ReadAll() returns ([]byte, error), but developers use blank identifier `_` to ignore the error when they only care about success cases.

**How to avoid:** Always check the error. If read fails, wrap it in FleetAPIError with clear context.

**Warning signs:**
- Empty error messages in logs for non-2xx responses
- FleetAPIError.Message contains empty strings
- Inconsistent error reporting (sometimes detailed, sometimes empty)

**Example:**
```go
// WRONG (current code):
bodyBytes, _ := io.ReadAll(resp.Body)
return &FleetAPIError{
    StatusCode: resp.StatusCode,
    Operation:  "UpsertPipeline",
    Message:    string(bodyBytes),  // Empty if read failed!
}

// CORRECT:
bodyBytes, err := io.ReadAll(resp.Body)
if err != nil {
    return &FleetAPIError{
        StatusCode: resp.StatusCode,
        Operation:  "UpsertPipeline",
        Message:    fmt.Sprintf("HTTP %d (body read failed: %v)", resp.StatusCode, err),
        Wrapped:    err,
    }
}
```

### Pitfall 2: Breaking errors.As with Missing Unwrap()
**What goes wrong:** errors.As(err, &target) fails to match FleetAPIError when it's wrapped.

**Why it happens:** Without an Unwrap() method, FleetAPIError doesn't participate in the error chain. errors.As can't find it.

**How to avoid:** Add Unwrap() method that returns the wrapped error (if any).

**Warning signs:**
- errors.As returns false even though FleetAPIError is in the chain
- Controller can't classify errors for retry decisions
- Type assertions fail on wrapped errors

**Example:**
```go
// Without Unwrap():
err := &FleetAPIError{StatusCode: 429, ...}
wrapped := fmt.Errorf("context: %w", err)

var apiErr *FleetAPIError
errors.As(wrapped, &apiErr)  // Returns false! errors.As can't unwrap

// With Unwrap():
func (e *FleetAPIError) Unwrap() error {
    return e.Wrapped
}

// Now errors.As works:
errors.As(wrapped, &apiErr)  // Returns true
```

### Pitfall 3: Incorrect Transient Classification
**What goes wrong:** Treating 4xx errors as transient causes infinite retry loops on validation errors.

**Why it happens:** Confusion about which HTTP errors are retriable. 429 is special (rate limit = transient). Other 4xx are client errors (permanent).

**How to avoid:** Follow standard classification: 429 + 5xx = transient, other 4xx = permanent.

**Warning signs:**
- Reconciliation loops retrying 400 (Bad Request) errors
- Status conditions flip between Ready and not Ready repeatedly
- Rate limiting on validation errors

**Reference:** Industry standard per [Baeldung HTTP retry guide](https://www.baeldung.com/cs/http-error-status-codes-retry):
- Retry: 408 (Request Timeout), 429 (Too Many Requests), 500, 502, 503, 504
- Don't retry: 400, 401, 403, 404, 405, etc. (client errors)

### Pitfall 4: Not Including Context in Error Messages
**What goes wrong:** Errors lack information needed for debugging (which pipeline, which operation).

**Why it happens:** FleetAPIError has PipelineID field but Error() method doesn't use it.

**How to avoid:** Include all relevant fields (PipelineID, Operation, StatusCode) in Error() string.

**Warning signs:**
- Can't identify which pipeline failed from logs
- Multiple pipelines reconciling = can't correlate errors
- Debugging requires cross-referencing multiple log sources

### Pitfall 5: Testing Only Happy Paths
**What goes wrong:** Error handling code has bugs that only appear in production under failure conditions.

**Why it happens:** Writing tests for failures requires more setup (mocking errors). Developers focus on success cases.

**How to avoid:** Use table-driven tests with both success and error cases. Test error classification explicitly.

**Warning signs:**
- No tests for io.ReadAll failures
- No tests for errors.As compatibility
- No tests for IsTransient edge cases (429, 5xx, 4xx)

## Code Examples

Verified patterns from existing codebase and Go documentation:

### Error Wrapping Pattern (Existing)
```go
// From pkg/fleetclient/client.go:63 (CORRECT - already used):
if err := c.limiter.Wait(ctx); err != nil {
    return nil, fmt.Errorf("rate limiter error: %w", err)
}
```

### Structured Logging Pattern (Existing)
```go
// From internal/controller/pipeline_controller.go:280 (CORRECT):
log.Error(err, "Fleet Management API error",
    "statusCode", apiErr.StatusCode,
    "operation", apiErr.Operation,
    "message", apiErr.Message)
```

### Mock Client Pattern (Existing)
```go
// From internal/controller/pipeline_controller_test.go:36:
type mockFleetClient struct {
    pipelines         map[string]*fleetclient.Pipeline
    upsertError       error
    deleteError       error
    shouldReturn400   bool
    shouldReturn429   bool
}

func (m *mockFleetClient) UpsertPipeline(ctx context.Context, req *fleetclient.UpsertPipelineRequest) (*fleetclient.Pipeline, error) {
    if m.shouldReturn429 {
        return nil, &fleetclient.FleetAPIError{
            StatusCode: http.StatusTooManyRequests,
            Operation:  "UpsertPipeline",
            Message:    "rate limit exceeded",
        }
    }
    // ... rest of mock logic
}
```

### Safe Response Body Reading (NEW - Required)
```go
// Replace client.go lines 88, 139:
if resp.StatusCode != http.StatusOK {
    bodyBytes, err := io.ReadAll(resp.Body)
    if err != nil {
        // Log the io error
        return &FleetAPIError{
            StatusCode: resp.StatusCode,
            Operation:  "UpsertPipeline",
            Message:    fmt.Sprintf("HTTP %d (failed to read response body: %v)", resp.StatusCode, err),
            Wrapped:    err,
        }
    }
    return &FleetAPIError{
        StatusCode: resp.StatusCode,
        Operation:  "UpsertPipeline",
        Message:    string(bodyBytes),
    }
}
```

### Error Type Inspection (NEW - Required for Controller)
```go
// For use in controller after client changes:
if err != nil {
    var apiErr *fleetclient.FleetAPIError
    if errors.As(err, &apiErr) {
        // Check transient classification
        if apiErr.IsTransient() {
            // Will retry automatically via requeue
            return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
        }
        // Permanent error - update status and don't retry
        // ... set status condition
    }
    // Not a FleetAPIError (network error, timeout, etc.)
    return ctrl.Result{}, fmt.Errorf("failed to sync: %w", err)
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| pkg/errors.Wrap() | fmt.Errorf("%w") | Go 1.13 (2019) | Standard library now has wrapping. No need for third-party packages. |
| Manual type assertions | errors.As() | Go 1.13 (2019) | Type-safe error inspection through wrapped chains. |
| == comparison for errors | errors.Is() | Go 1.13 (2019) | Works with wrapped errors, prevents false negatives. |
| io/ioutil.ReadAll | io.ReadAll | Go 1.16 (2021) | ioutil deprecated, functions moved to io package. |

**Deprecated/outdated:**
- github.com/pkg/errors: Use standard library fmt.Errorf("%w") instead
- io/ioutil package: Use io.ReadAll, os.ReadFile, os.WriteFile directly

## Open Questions

1. **Should FleetAPIError include original HTTP response headers?**
   - What we know: Some APIs return Retry-After header for 429 errors, X-Request-ID for tracing
   - What's unclear: Whether Fleet Management API uses these headers
   - Recommendation: Start without headers. Add only if needed for retry logic or tracing. Keep it simple for Phase 1.

2. **Should PipelineID be required or optional in FleetAPIError?**
   - What we know: Not all operations have a pipeline ID (initial creation before ID is assigned)
   - What's unclear: Value of requiring it vs making it optional
   - Recommendation: Make it optional (empty string = not available). Constructor can take it as parameter.

3. **Should network errors (timeout, connection refused) be classified as transient?**
   - What we know: net.Error can be temporary, context.DeadlineExceeded is a timeout
   - What's unclear: Whether to add IsTransient check for non-FleetAPIError types
   - Recommendation: Phase 1 focuses on FleetAPIError. Network error classification can be Phase 2 if needed. Controller already handles all errors with requeue.

## Sources

### Primary (HIGH confidence)
- Go standard library errors package: https://pkg.go.dev/errors - Is/As/Unwrap documentation verified
- Go standard library net/http package: https://pkg.go.dev/net/http - Status code constants verified
- Existing codebase patterns: pkg/fleetclient/client.go, internal/controller/pipeline_controller.go - error wrapping with %w confirmed at lines 63, 68, 73, 81, 98, 108, 114, 119, 127

### Secondary (MEDIUM confidence)
- [Working with Errors in Go 1.13](https://go.dev/blog/go1.13-errors) - Official Go blog on error wrapping
- [A practical guide to error handling in Go - Datadog](https://www.datadoghq.com/blog/go-error-handling/) - Comprehensive error handling guide
- [Which HTTP Error Status Codes Should Not Be Retried? - Baeldung](https://www.baeldung.com/cs/http-error-status-codes-retry) - Transient vs permanent classification
- [Handle Context Deadline Exceeded error in Go](https://gosamples.dev/context-deadline-exceeded/) - Timeout error handling
- [Testing in Go with Testify - Better Stack](https://betterstack.com/community/guides/scaling-go/golang-testify/) - Testify/Ginkgo patterns
- [Go Testing Excellence: Table-Driven Tests and Mocking](https://dasroot.net/posts/2026/01/go-testing-excellence-table-driven-tests-mocking/) - Current best practices (2026)

### Tertiary (LOW confidence)
- WebSearch findings on io.ReadAll failures and HTTP client error handling - Multiple sources agree on always checking io.ReadAll errors, but no single authoritative source
- Community discussions on retry logic - General consensus on 429/5xx = transient, but implementation details vary by use case

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - All packages are Go standard library or already in go.mod with verified versions
- Architecture: HIGH - Patterns verified from existing codebase (client.go, controller.go) and official Go documentation
- Pitfalls: HIGH - Issues at lines 88, 139 confirmed by code inspection, classification rules from official HTTP specs

**Research date:** 2026-02-08
**Valid until:** 2026-03-08 (30 days - Go error handling is stable, no rapid changes expected)
