# Pitfalls Research: Error Handling Refactoring in Kubernetes Operators

**Domain:** Kubernetes operator error handling refactoring
**Researched:** 2026-02-08
**Confidence:** HIGH

## Critical Pitfalls

### Pitfall 1: Ignoring io.ReadAll() Errors in HTTP Error Responses

**What goes wrong:**
When reading HTTP response bodies for error messages, the error from `io.ReadAll()` is silently ignored using blank identifier (`bodyBytes, _ := io.ReadAll(resp.Body)`). This can lead to incomplete error messages being returned to users, making debugging difficult. In production, network issues or memory pressure can cause ReadAll to fail, and users receive empty error messages instead of actionable information.

**Why it happens:**
Developers assume that if the HTTP request succeeded and we already have a status code, the body read will always succeed. The thinking is "we already know it's an error, why check another error?" This is compounded by the fact that the ignored error is in an error-handling path, creating nested error handling that feels awkward.

**How to avoid:**
- Check the io.ReadAll() error and include it in the error message
- Use a pattern like: `bodyBytes, readErr := io.ReadAll(resp.Body); if readErr != nil { return fmt.Errorf("HTTP %d (failed to read body: %w)", resp.StatusCode, readErr) }`
- For error responses, consider limiting body size with `io.LimitReader(resp.Body, 1<<20)` to prevent memory issues
- Include the read error as context even if you have the status code

**Warning signs:**
- Blank identifier (`_`) used with `io.ReadAll()` in error-handling branches
- Error messages that sometimes appear empty or truncated in production logs
- Users reporting "got HTTP 400 but no error details"
- Grep for pattern: `_, _ := io.ReadAll` or `bodyBytes, _ := io.ReadAll`

**Phase to address:**
Phase 1: Client Layer Error Handling - Fix all io.ReadAll() error ignoring patterns

---

### Pitfall 2: Status Update Triggering Infinite Reconciliation Loops

**What goes wrong:**
Status updates can trigger reconciliation loops even when only the status subresource changes (not the spec). If a controller updates status without proper guards, and the update itself triggers a new reconcile, you get infinite loops. The observedGeneration pattern helps but doesn't prevent all cases, especially when status updates fail and retry indefinitely.

**Why it happens:**
Kubernetes ResourceVersion changes on every status update, and without proper watch predicates, every change triggers reconciliation. Developers test with the happy path (successful status update) but miss the conflict retry case where `Requeue: true` without delay creates a tight loop. The controller-runtime default behavior triggers reconciliation on ANY resource change, including status-only changes.

**How to avoid:**
- Use watch predicates that filter on `Generation` changes (not `ResourceVersion`)
- Pattern: `WithEventFilter(predicate.GenerationChangedPredicate{})` in controller setup
- For status update conflicts, use exponential backoff not immediate requeue
- Add rate limiting: return `Result{RequeueAfter: time.Second}` not `Result{Requeue: true}`
- Check observedGeneration BEFORE attempting status updates to skip unnecessary work
- Never call `r.Status().Update()` inside an error handler without backoff

**Warning signs:**
- High CPU usage from controller with no spec changes
- Logs showing repeated "status update conflict, requeueing" messages
- ResourceVersion incrementing rapidly without Generation changes
- Controller metrics showing high reconciliation rate with low actual API calls
- `kubectl get events` showing Status update events in tight loops

**Phase to address:**
Phase 2: Controller Reconciliation Guards - Add predicates and backoff for status updates

---

### Pitfall 3: Missing Exponential Backoff on Conflict Retries

**What goes wrong:**
When status updates encounter conflicts (resource modified between read and update), using `Result{Requeue: true}` causes immediate retry without delay. Under high contention or rapid updates, this creates a thundering herd where multiple reconciliation loops compete, each immediately retrying on conflict. This burns CPU and API server resources while making conflicts more likely.

**Why it happens:**
Developers assume controller-runtime's exponential backoff applies to `Requeue: true`, but it only applies when returning an error. Immediate requeue feels correct because "we just need a fresh copy," but doesn't account for multiple competing reconciliation attempts. The conflict error handler tries to be helpful by immediately retrying, not realizing this makes the problem worse.

**How to avoid:**
- For conflict errors, use `Result{RequeueAfter: time.Second}` minimum delay
- Better: Use `client.RetryOnConflict()` from client-go for status updates
- Pattern: Wrap status updates in retry logic with exponential backoff (e.g., 1s, 2s, 4s, 8s)
- Consider using `workqueue.RateLimitingInterface` for sophisticated backoff
- For transient errors (conflicts, rate limits), always add delay before retry
- Only return error (for controller-runtime backoff) on non-retriable errors

**How to avoid:**
```go
// WRONG: Immediate requeue on conflict
if apierrors.IsConflict(err) {
    return ctrl.Result{Requeue: true}, nil
}

// RIGHT: Backoff on conflict
if apierrors.IsConflict(err) {
    return ctrl.Result{RequeueAfter: time.Second}, nil
}

// BEST: Use RetryOnConflict wrapper
err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
    // Get fresh copy
    pipeline := &v1alpha1.Pipeline{}
    if err := r.Get(ctx, req.NamespacedName, pipeline); err != nil {
        return err
    }
    // Update status
    pipeline.Status.ObservedGeneration = pipeline.Generation
    return r.Status().Update(ctx, pipeline)
})
```

**Warning signs:**
- CPU spikes during resource updates
- Many "status update conflict" log messages in quick succession
- API server rate limiting the operator
- Metrics showing sub-second reconciliation times with high failure rate
- Etcd performance issues correlated with operator activity
- Grep for pattern: `IsConflict.*Requeue: true` without RequeueAfter

**Phase to address:**
Phase 3: Backoff Strategy - Replace immediate requeue with exponential backoff

---

### Pitfall 4: Breaking Error Visibility with Over-Wrapping

**What goes wrong:**
When refactoring to add `%w` wrapping everywhere, you can accidentally expose internal implementation details through error chains that should have been opaque. For example, wrapping a `sql.ErrNoRows` makes it part of your public API - callers can now `errors.Is()` check for it, creating a dependency you can't change. Changing database libraries later becomes a breaking change.

**Why it happens:**
After learning about `%w`, developers enthusiastically wrap everything without considering API boundaries. The Go 1.13 error wrapping guidance says "wrap errors" but doesn't emphasize that wrapping makes errors part of your public contract. Refactoring tools blindly convert `errors.Wrap()` to `fmt.Errorf(..., %w)` without considering API exposure.

**How to avoid:**
- At API boundaries, use `%v` not `%w` to break the error chain
- Only wrap errors that are part of your public contract
- For library code: wrap internal errors but unwrap at public boundaries
- Document which errors callers can depend on with `errors.Is()` or `errors.As()`
- Consider: "If I change implementation, will this break callers?"
- For operator code: wrap client-go errors (callers expect them), don't wrap HTTP client errors (implementation detail)

**Warning signs:**
- Every error in codebase uses `%w`
- Public functions returning wrapped errors from third-party libraries
- Difficulty changing dependencies because error types are leaked
- Callers doing `errors.Is()` checks on unexported error types
- No distinction between "sentinel errors" and "internal errors"

**Phase to address:**
Phase 1: Client Layer Error Handling - Review which errors should be wrapped vs opaque

---

### Pitfall 5: Introducing Race Conditions When Adding Error Checks

**What goes wrong:**
Adding deferred error checks to previously-ignored errors can introduce race conditions. For example, checking `resp.Body.Close()` error in a defer after already processing the body can cause nil pointer dereferences if the body was already closed. Multiple goroutines accessing error variables in defers can race.

**Why it happens:**
When fixing ignored errors, developers add `defer func() { if err := x.Close(); err != nil { ... } }()` patterns without considering what happens if Close() is called multiple times, or if the error variable is captured incorrectly. The defer runs after the function returns, but error handling code might need the error value from the defer scope.

**How to avoid:**
- Use named return values for deferred error checks: `func() (err error) { defer func() { err = combineErrors(err, cleanup()) }() }`
- Don't check Close() errors if you're already returning an error from the main path
- For HTTP responses: `defer resp.Body.Close()` is sufficient, don't check error unless you have specific cleanup requirements
- Use tools like `go vet` and `errcheck` to find missed errors, but review each fix contextually
- Be especially careful with deferred functions that modify return values

**Warning signs:**
- Defers that capture loop variables or parameters
- Multiple Close() calls on the same resource
- Named return values modified in multiple defer statements
- Tests failing with "nil pointer dereference" after error-handling refactor
- Race detector warnings in code that wasn't racy before

**Phase to address:**
Phase 1: Client Layer Error Handling - Review defer patterns when adding error checks

---

### Pitfall 6: Swallowing Errors to "Maintain Existing Behavior"

**What goes wrong:**
During refactoring, developers discover error-handling bugs but decide to maintain the existing (broken) behavior to avoid changing semantics. For example, finding an ignored io.ReadAll() error but keeping it ignored because "tests expect empty string on error." This technical debt compounds because the fix becomes harder as more code depends on the broken behavior.

**Why it happens:**
Fear of breaking changes and lack of comprehensive tests. The refactor reveals that existing behavior is wrong, but changing it might break unknown callers. The thinking is "this bug might be a feature someone depends on." Without confidence in test coverage, it's safer to keep the bug.

**How to avoid:**
- If existing behavior is wrong, fix it and update tests
- Add tests that verify the correct behavior before refactoring
- Use feature flags or gradual rollouts for behavior changes if needed
- Document the behavior change in commit messages and changelogs
- For operators: most users prefer correct errors over consistent bugs
- Consider: "Would I defend this behavior in a design review?"

**Warning signs:**
- Comments like "TODO: fix this but keeping for compatibility"
- Error handling that does nothing but preserve existing behavior
- Tests that explicitly check for broken behavior
- Increasing number of "maintain compatibility" workarounds
- Code smell: `if err != nil { /* ignore for backwards compatibility */ }`

**Phase to address:**
Phase 1: Client Layer Error Handling - Fix errors correctly, don't preserve bugs

## Testing Anti-Patterns

### Anti-Pattern 1: Testing Implementation Instead of Behavior

**What goes wrong:**
Tests check that errors are wrapped with specific text or type, rather than testing that the operation fails appropriately. For example, testing that error contains "failed to read body" instead of testing that a network failure causes request failure. This makes refactoring brittle - changing error messages breaks tests.

**Prevention:**
- Test behavior: "Does operation fail when it should?"
- Test observable state changes, not error strings
- Use `errors.Is()` for sentinel errors, not string matching
- For operators: test that Status conditions are set correctly, not error text
- Mock external failures (network, API) and verify graceful degradation

**Example:**
```go
// WRONG: Brittle test coupled to implementation
assert.Contains(t, err.Error(), "failed to read body")

// RIGHT: Test behavior
assert.Error(t, err)
assert.True(t, errors.Is(err, ErrAPIFailure))
// And verify status was updated correctly
assert.Equal(t, metav1.ConditionFalse, pipeline.Status.Conditions[0].Status)
```

---

### Anti-Pattern 2: Not Testing Error Paths

**What goes wrong:**
Tests only cover happy path, missing error-handling bugs that only appear in production. For io.ReadAll() errors, tests never simulate body read failures. For status updates, tests never inject conflict errors. The refactored error handling is never exercised.

**Prevention:**
- For every error return, write a test that triggers it
- Use mock clients that can inject failures on demand
- Test network errors, API errors, resource conflicts separately
- For controller tests: inject conflict errors, not found errors, rate limit errors
- Use table-driven tests with error cases prominently featured

**Example:**
```go
tests := []struct {
    name    string
    setupFn func(*mockClient)
    wantErr bool
    errType error
}{
    {
        name: "success",
        setupFn: func(m *mockClient) { m.returnPipeline(...) },
        wantErr: false,
    },
    {
        name: "API returns 400",
        setupFn: func(m *mockClient) { m.returnError(400, "invalid") },
        wantErr: true,
        errType: fleetclient.FleetAPIError{},
    },
    {
        name: "status update conflict",
        setupFn: func(m *mockClient) { m.returnConflict() },
        wantErr: false, // Should handle gracefully
    },
}
```

---

### Anti-Pattern 3: Mocking Too Much

**What goes wrong:**
Overly specific mocks that return exactly what the code expects make tests pass but miss real-world bugs. For example, a mock HTTP client that always returns valid JSON never catches the io.ReadAll() error case. Mocks that know about implementation details (like status update order) create brittle tests.

**Prevention:**
- Use real implementations for leaf nodes (JSON encoding, error wrapping)
- Only mock external boundaries (HTTP clients, Kubernetes API)
- Make mocks dumb - they shouldn't know about your implementation
- For Kubernetes operators: use controller-runtime's fake client, not hand-rolled mocks
- Inject failures at the boundary, not in business logic

**Example:**
```go
// WRONG: Mock that knows too much
mockClient.On("UpsertPipeline").Return(&Pipeline{ID: "123"}, nil).Once()
mockClient.On("UpdateStatus").Return(nil).Once()
// This breaks if you change the order or add another call

// RIGHT: Mock that just provides data
mockClient.UpsertPipelineFunc = func(ctx, req) (*Pipeline, error) {
    return &Pipeline{ID: "123", Name: req.Pipeline.Name}, nil
}
// Business logic determines when to call it
```

---

### Anti-Pattern 4: Not Testing Idempotency of Error Recovery

**What goes wrong:**
Tests verify that error handling works once, but don't test repeated reconciliation after errors. For operators, this is critical - reconciliation must be idempotent even after partial failures. Tests don't verify that running reconcile twice after an error produces consistent results.

**Prevention:**
- For operator tests: reconcile multiple times and verify convergence
- Test: error → fix condition → reconcile → verify success
- Test: reconcile → conflict → reconcile again → should succeed
- Verify observedGeneration prevents unnecessary reconciliation
- Test that status updates are idempotent (same conditions on retry)

**Example:**
```go
// First reconcile fails
mockClient.UpsertPipelineFunc = func(...) (*Pipeline, error) {
    return nil, &FleetAPIError{StatusCode: 500}
}
result, err := reconciler.Reconcile(ctx, req)
assert.Error(t, err)

// Second reconcile succeeds (network recovered)
mockClient.UpsertPipelineFunc = func(...) (*Pipeline, error) {
    return &Pipeline{ID: "123"}, nil
}
result, err = reconciler.Reconcile(ctx, req)
assert.NoError(t, err)
assert.Equal(t, "123", pipeline.Status.ID)
```

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|----------------|-----------------|
| Ignore io.ReadAll() errors | Simpler code, fewer error checks | Production debugging nightmare, incomplete error messages | Never - always check errors |
| Use `Requeue: true` for conflicts | Immediate retry feels responsive | CPU burn, API server load, worse conflicts | Never - always add backoff delay |
| Wrap all errors with %w | Feels "correct", enables errors.Is | Leaks implementation, breaks API compatibility | Only at module boundaries |
| Return nil error on status conflict | Appears to handle conflict gracefully | Infinite loop if conflict persists | Never - must requeue with delay |
| Keep existing broken behavior | No breaking changes during refactor | Bug persists, compounds over time | Never - fix bugs when found |

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|----------------|
| Immediate retry on conflicts | High CPU, many reconciliations | Add exponential backoff (1s, 2s, 4s) | >10 resources with concurrent updates |
| Status updates without observedGeneration | Reconciles on every status change | Check generation before reconciling | Any production deployment |
| Missing watch predicates | Reconciles on ResourceVersion changes | Filter on Generation with predicates | >50 resources in cluster |
| Unbounded io.ReadAll() | Memory exhaustion from large responses | Use io.LimitReader(resp.Body, 1<<20) | Error responses >1MB |
| Synchronous retries in reconcile loop | Controller blocks on retry delays | Return error for exponential backoff | Any retriable error |

## Operator-Specific Gotchas

### Gotcha 1: Status Updates Don't Use Optimistic Locking Like Spec

**Issue:** Status updates use optimistic locking (resourceVersion) but have different retry semantics than spec updates. Status conflicts are more common because controllers constantly update status, but many operators treat them like spec conflicts.

**Correct approach:**
- Status update conflicts should always requeue with delay
- Use `retry.RetryOnConflict()` wrapper for status updates
- Don't return error on status conflict unless retry exhausted

---

### Gotcha 2: ObservedGeneration Doesn't Prevent All Reconciliations

**Issue:** Checking `observedGeneration == generation` prevents reconciliation on status updates, but doesn't prevent reconciliation from external triggers (periodic sync, watch restarts, operator restart).

**Correct approach:**
- ObservedGeneration is an optimization, not a guarantee
- Make reconciliation fully idempotent regardless of observedGeneration
- Use observedGeneration to skip API calls, not skip entire reconciliation

---

### Gotcha 3: Error Returns Trigger Exponential Backoff, Result.Requeue Does Not

**Issue:** Controller-runtime applies exponential backoff (and rate limiting) when returning error, but NOT when returning `Result{Requeue: true}`. Many developers assume both trigger backoff.

**Correct approach:**
- Return error for failures that should back off (API errors, network errors)
- Use `Result{Requeue: true}` only for known-fast operations
- For conflicts or transient errors, use `Result{RequeueAfter: duration}`
- Never return `Result{Requeue: true}` for conditions that might persist

---

### Gotcha 4: Finalizers Must Handle Already-Deleted Resources

**Issue:** When a resource with finalizer is deleted, the finalizer runs. But if the external resource (Fleet Management pipeline) was already deleted, the Delete API call returns 404. If the finalizer treats 404 as error, the finalizer never completes and the resource never deletes.

**Correct approach:**
- Treat 404 on DELETE as success (idempotent deletion)
- Only return error from finalizer if resource exists but deletion fails
- Same pattern for all external resource cleanup in finalizers

## "Looks Done But Isn't" Checklist

Error handling refactoring that appears complete but is missing critical pieces:

- [ ] **Client error handling:** io.ReadAll() errors checked in ALL error response paths (not just some)
- [ ] **Response body limits:** All io.ReadAll() calls use io.LimitReader() to prevent memory exhaustion
- [ ] **Status update conflicts:** Handled with backoff, not immediate requeue
- [ ] **Watch predicates:** Controller filters on Generation changes, not all changes
- [ ] **ObservedGeneration checks:** Present before reconciliation AND before status updates
- [ ] **Error wrapping boundaries:** Only wrapped at module boundaries, not everywhere
- [ ] **Defer error checks:** Named return values used when defers modify errors
- [ ] **Finalizer 404 handling:** DELETE operations treat 404 as success
- [ ] **Test coverage:** Error paths tested, not just happy path
- [ ] **Idempotency tests:** Reconciliation tested multiple times after errors
- [ ] **Integration tests:** Run against real Kubernetes (envtest), not just mocks
- [ ] **Backoff verification:** CPU usage checked under conflict scenarios

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|---------------|----------------|
| Ignored io.ReadAll() errors | LOW | Add error checks, update tests, verify error messages in logs |
| Infinite reconciliation loop | HIGH | Add watch predicates, observedGeneration guards, restart operator |
| Missing backoff on conflicts | MEDIUM | Replace Requeue: true with RequeueAfter, add RetryOnConflict wrapper |
| Over-wrapped errors | MEDIUM | Review API boundaries, use %v at boundaries, version bump if breaking |
| Race conditions from defer | MEDIUM | Use named returns, review defer patterns, add race detector to CI |
| Preserved broken behavior | LOW-HIGH | Fix behavior, add tests, document change, consider feature flag |

## Phase-Specific Warnings

| Phase Topic | Likely Pitfall | Mitigation |
|-------------|---------------|------------|
| Client layer error handling | Ignoring io.ReadAll() errors, over-wrapping errors | Check ALL error returns, review API boundaries |
| Controller reconciliation | Infinite loops, missing observedGeneration | Add watch predicates, test with status-only updates |
| Backoff implementation | Immediate requeue, no exponential backoff | Use RetryOnConflict, RequeueAfter with delays |
| Testing error paths | Only happy path tested, brittle mocks | Add error injection, test conflict scenarios |
| Integration testing | Mocks hide real issues | Use envtest, test against real Kubernetes API |
| Production rollout | Error message changes confuse users | Document error message improvements in changelog |

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|------------------|--------------|
| Ignored io.ReadAll() errors | Phase 1: Client Layer | Grep for `_, _ := io.ReadAll`, check all return paths |
| Infinite reconciliation | Phase 2: Controller Guards | Monitor CPU usage, check reconciliation rate metrics |
| Missing backoff | Phase 3: Backoff Strategy | Test conflict scenarios, verify RequeueAfter usage |
| Over-wrapped errors | Phase 1: Client Layer | Review public API, check error type exposure |
| Race conditions | Phase 1: Client Layer | Run tests with `-race`, review defer patterns |
| Broken behavior preserved | Phase 1: Client Layer | Review TODOs, check for compatibility workarounds |
| Testing anti-patterns | All phases | Measure coverage, verify error path tests exist |

## Sources

### Kubernetes Operator Patterns
- [Kubernetes operators best practices: understanding conflict errors](https://alenkacz.medium.com/kubernetes-operators-best-practices-understanding-conflict-errors-d05353dff421) (MEDIUM confidence)
- [Reconcile is triggered after status update - Issue #2831](https://github.com/kubernetes-sigs/controller-runtime/issues/2831) (HIGH confidence - official source)
- [Error Back-off with Controller Runtime](https://stuartleeks.com/posts/error-back-off-with-controller-runtime/) (MEDIUM confidence)
- [Good Practices - The Kubebuilder Book](https://book.kubebuilder.io/reference/good-practices) (HIGH confidence - official docs)

### Go HTTP Client Error Handling
- [Be careful with ioutil.ReadAll in Golang](https://haisum.github.io/2017/09/11/golang-ioutil-readall/) (MEDIUM confidence)
- [Go HTTP Client Patterns: Production-Ready Implementation](https://jsschools.com/golang/go-http-client-patterns-a-production-ready-implem/) (MEDIUM confidence)
- [net/http package - Go Packages](https://pkg.go.dev/net/http) (HIGH confidence - official docs)

### Go Error Wrapping
- [Working with Errors in Go 1.13](https://go.dev/blog/go1.13-errors) (HIGH confidence - official Go blog)
- [Error Values: Frequently Asked Questions](https://go.dev/wiki/ErrorValueFAQ) (HIGH confidence - official wiki)
- [Errors and Error Wrapping in Go](https://trstringer.com/errors-and-error-wrapping-go/) (MEDIUM confidence)

### Testing Patterns
- [Unit testing Kubernetes operators using mocks](https://itnext.io/unit-testing-kubernetes-operators-using-mocks-ba3ba2483ba3) (MEDIUM confidence)
- [Testing: Mocking the Kubernetes API in Go](https://charlottemach.com/2022/09/14/mocking-kubernetes.html) (MEDIUM confidence)
- [Testing - kube-rs docs](https://kube.rs/controllers/testing/) (MEDIUM confidence - different language but patterns apply)

### Controller-Runtime Patterns
- [Add ObservedGeneration to status field - Issue #9673](https://github.com/rook/rook/issues/9673) (MEDIUM confidence)
- [controller-runtime FAQ](https://github.com/kubernetes-sigs/controller-runtime/blob/main/FAQ.md) (HIGH confidence - official docs)
- [reconcile package documentation](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/reconcile) (HIGH confidence - official docs)

---
*Pitfalls research for: Kubernetes operator error handling refactoring*
*Researched: 2026-02-08*
*Domain: Kubernetes operators with external API integration*
