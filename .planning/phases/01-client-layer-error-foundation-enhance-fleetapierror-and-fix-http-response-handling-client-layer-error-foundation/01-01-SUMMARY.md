---
phase: 01-client-layer-error-foundation
plan: 01
subsystem: pkg/fleetclient
tags: [error-handling, observability, reliability]
dependencies:
  requires: []
  provides:
    - FleetAPIError.IsTransient() for retry classification
    - FleetAPIError.PipelineID for distributed tracing
    - FleetAPIError.Unwrap() for error chain compatibility
    - Safe io.ReadAll error handling in HTTP client
  affects:
    - internal/controller (Phase 2 will consume IsTransient() and PipelineID)
tech_stack:
  added: []
  patterns:
    - Error wrapping with Unwrap() for errors.As/errors.Is compatibility
    - Transient error classification based on HTTP status codes
key_files:
  created: []
  modified:
    - pkg/fleetclient/types.go
    - pkg/fleetclient/client.go
decisions: []
metrics:
  duration_minutes: 2
  completed_date: 2026-02-08
---

# Phase 1 Plan 1: Client Layer Error Foundation Summary

**One-liner:** Enhanced FleetAPIError with transient classification, PipelineID tracing, error chain support, and fixed silent io.ReadAll failures in HTTP client.

## What Was Built

### Enhanced FleetAPIError Type (types.go)

**Added fields:**
- `PipelineID string` - For distributed tracing, enables correlation across logs/traces
- `Wrapped error` - For error chain compatibility with Go 1.13+ error handling

**Added methods:**
- `IsTransient() bool` - Classifies errors as retryable (429/408/5xx) or permanent (4xx)
- `Unwrap() error` - Enables `errors.As()` and `errors.Is()` compatibility

**Enhanced Error() method:**
- With PipelineID: `"{Operation} failed (pipeline={PipelineID}, status={StatusCode}): {Message}"`
- Without PipelineID: `"{Operation} failed (status={StatusCode}): {Message}"`

### Fixed HTTP Client Error Handling (client.go)

**Line 88 (UpsertPipeline error path):**
- Previously: `bodyBytes, _ := io.ReadAll(resp.Body)` (silent failure)
- Now: Checks io.ReadAll error, wraps in FleetAPIError with descriptive message

**Line 147 (DeletePipeline error path):**
- Previously: `bodyBytes, _ := io.ReadAll(resp.Body)` (silent failure)
- Now: Checks io.ReadAll error, wraps in FleetAPIError with PipelineID and descriptive message

**Error construction pattern:**
```go
if err != nil {
    return &FleetAPIError{
        StatusCode: resp.StatusCode,
        Operation:  "DeletePipeline",
        Message:    fmt.Sprintf("HTTP %d (failed to read response body: %v)", resp.StatusCode, err),
        PipelineID: id,
        Wrapped:    err,
    }
}
```

## Task Execution

| Task | Name                                      | Status | Commit  | Files Modified                        |
|------|-------------------------------------------|--------|---------|---------------------------------------|
| 1    | Enhance FleetAPIError in types.go        | Done   | d0311d6 | pkg/fleetclient/types.go              |
| 2    | Fix io.ReadAll error handling in client.go | Done   | a25776d | pkg/fleetclient/client.go             |

## Verification Results

All verification criteria passed:

- [x] `go build ./...` passes (no compilation errors)
- [x] `go vet ./...` passes (no static analysis issues)
- [x] `go test ./... -count=1 -short` passes (all tests green)
- [x] No ignored io.ReadAll errors remain in client.go
- [x] IsTransient, Unwrap, and PipelineID confirmed present in types.go

**Test output:**
```
ok  	github.com/grafana/fleet-management-operator/api/v1alpha1	0.309s
ok  	github.com/grafana/fleet-management-operator/internal/controller	8.015s
```

## Deviations from Plan

None - plan executed exactly as written.

## Success Criteria Validation

- [x] FleetAPIError has PipelineID field (ERR-05)
- [x] FleetAPIError has IsTransient() method classifying 429/5xx as transient (ERR-03)
- [x] FleetAPIError has Unwrap() for errors.As compatibility (ERR-03 prerequisite)
- [x] io.ReadAll errors are caught at both locations in client.go (ERR-01)
- [x] All existing tests pass unchanged
- [x] No changes to external APIs or function signatures

## Technical Notes

### IsTransient() Classification Logic

```go
// Transient (retry-worthy)
429 Too Many Requests    -> true
408 Request Timeout      -> true
500-599 Server Errors    -> true

// Permanent (no retry)
All other 4xx            -> false
```

### PipelineID Population Strategy

- **UpsertPipeline error path:** PipelineID NOT populated (client doesn't have Kubernetes pipeline name at this layer)
- **DeletePipeline error path:** PipelineID populated with `id` parameter (Fleet Management pipeline ID known)
- **Controller layer (Phase 2):** Will populate PipelineID when wrapping/inspecting client errors

### Backward Compatibility

New fields use zero-value defaults:
- `PipelineID string` defaults to `""` (empty string)
- `Wrapped error` defaults to `nil`

Existing code constructing `FleetAPIError` without new fields compiles unchanged.

## Impact Analysis

**Immediate benefits:**
1. No more silent io.ReadAll failures in HTTP error paths
2. Better error messages with status codes and operation context
3. Foundation for retry logic in controller layer

**Enables Phase 2 work:**
1. Controller can use `IsTransient()` to decide retry vs. fail-fast
2. Controller can populate PipelineID for full distributed tracing
3. Controller can use `errors.As()` to extract FleetAPIError from error chains

**Risk assessment:** Low - additive changes only, no breaking API changes

## Next Phase Readiness

**Ready for Phase 2:** YES

Phase 2 (Controller Error Handling) can now:
- Use `IsTransient()` for retry classification
- Populate `PipelineID` when wrapping client errors
- Use `errors.As(&fleetAPIErr)` to extract error details
- Rely on safe io.ReadAll error handling

**No blockers identified.**

## Self-Check: PASSED

**Created files:** None (all modifications to existing files)

**Modified files verified:**
- FOUND: pkg/fleetclient/types.go (modified, committed d0311d6)
- FOUND: pkg/fleetclient/client.go (modified, committed a25776d)

**Commits verified:**
```bash
$ git log --oneline --all | grep -E "d0311d6|a25776d"
a25776d fix(01-01): handle io.ReadAll errors in client HTTP response handling
d0311d6 feat(01-01): enhance FleetAPIError with PipelineID, IsTransient, and Unwrap
```

**Build verification:**
- go build ./... - PASSED
- go vet ./... - PASSED
- go test ./... -count=1 -short - PASSED

**Method/field verification:**
```bash
$ grep -n "IsTransient\|Unwrap\|PipelineID" pkg/fleetclient/types.go
55:	PipelineID string
60:	if e.PipelineID != "" {
61:		return fmt.Sprintf("%s failed (pipeline=%s, status=%d): %s", e.Operation, e.PipelineID, e.StatusCode, e.Message)
66:func (e *FleetAPIError) Unwrap() error {
71:func (e *FleetAPIError) IsTransient() bool {
```

All expected changes confirmed present and functional.
