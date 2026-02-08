---
phase: 02-controller-error-handling
plan: 01
subsystem: controller
tags: [error-handling, reconciliation, retry-logic, exponential-backoff]
dependency_graph:
  requires: [01-01, 01-02]
  provides: [error-classification-helpers, safe-status-updates, 404-recursion-prevention]
  affects: [internal/controller]
tech_stack:
  added: []
  patterns: [errors.As-for-wrapped-errors, single-retry-guard, original-error-preservation]
key_files:
  created:
    - internal/controller/errors.go
  modified:
    - internal/controller/pipeline_controller.go
decisions:
  - what: Use errors.As instead of type assertion for FleetAPIError
    why: Handles wrapped errors correctly when errors are wrapped with fmt.Errorf
    impact: Controller can now properly classify errors wrapped by other layers
  - what: Preserve original error in updateStatusError, not status update error
    why: Status update errors break exponential backoff (controller-runtime never sees original error)
    impact: Exponential backoff now works correctly even when status updates fail
  - what: Use single-retry guard (check Status.ID empty) for 404 recreation
    why: Prevents infinite recursion if recreation keeps failing
    impact: Controller cannot stack overflow on repeated 404 errors
  - what: Replace validation check with shouldRetry helper
    why: Centralizes retry logic, makes it easier to add new error types
    impact: All error classification goes through single helper function
metrics:
  duration: 2m
  completed: 2026-02-08
---

# Phase 02 Plan 01: Controller Error Handling Fixes Summary

Fix critical controller error handling bugs to preserve original reconciliation errors and prevent infinite recursion.

## One-liner

Fixed updateStatusError to preserve original reconciliation error (not status update error) for proper exponential backoff, prevented infinite recursion in 404 external deletion detection with single-retry guard, and added error classification helpers using errors.As for wrapped error support.

## What Was Done

### Task 1: Error Classification Helpers (commit 810d123)

Created `internal/controller/errors.go` with two helper functions:

**isTransientError(err error) bool:**
- Uses `errors.As` to extract FleetAPIError from error chain (handles wrapped errors)
- Delegates to `FleetAPIError.IsTransient()` from Phase 1
- Returns `false` for context.Canceled (cancelled operations don't retry)
- Returns `true` for network timeouts (net.Error with Timeout())
- Returns `true` for unknown errors (safe default)

**shouldRetry(err error, reason string) bool:**
- Returns `false` for validation errors (user must fix spec)
- Delegates to isTransientError for all other cases
- Centralizes retry logic in one place

**Key pattern:** Used `errors.As(err, &apiErr)` instead of type assertion `err.(*fleetclient.FleetAPIError)` to handle wrapped errors correctly.

### Task 2: Fixed updateStatusError and handleAPIError (commit 71fbafd)

**updateStatusError fix (lines 362-433):**
- Changed function signature from `err` to `originalErr` to clarify intent
- Preserved original error throughout function
- On status update conflict: return `Result{Requeue: true}, nil` (correct - cache is stale)
- On other status update errors: LOG the error with originalError context, but DON'T return it
- Replaced hardcoded `reason == reasonValidationError` check with `shouldRetry(originalErr, reason)`
- Final return: ALWAYS returns originalErr (for exponential backoff), or nil (for validation errors)

**handleAPIError 404 fix (lines 263-290):**
- Added single-retry guard: check if `pipeline.Status.ID == ""` at start of 404 case
- If ID already empty (already tried recreation): log error, emit event, return error (no recursion)
- If ID not empty (first detection): log with previousID, emit event, clear ID, rebuild request, call UpsertPipeline INLINE
- If inline UpsertPipeline fails: call handleAPIError to classify the NEW error (safe because ID is now empty)
- Removed recursive `return r.reconcileNormal(ctx, pipeline)` call

**Type assertion fix:**
- Changed `if apiErr, ok := err.(*fleetclient.FleetAPIError); ok {` to `var apiErr *fleetclient.FleetAPIError` followed by `if errors.As(err, &apiErr)`
- Added "errors" import

## Deviations from Plan

None - plan executed exactly as written.

## Verification Results

All verification criteria passed:

1. `go build ./...` - Compiled successfully
2. `go vet ./...` - Passed with no issues
3. `go test ./... -count=1 -short` - All tests passed
4. `grep -n "return ctrl.Result{}, updateErr"` - NO MATCHES (bug is gone)
5. `grep -n "return r.reconcileNormal"` - Only found on line 149 (main entry point, not in 404 case)
6. `grep -n "isTransientError\|shouldRetry" internal/controller/errors.go` - Both functions present
7. `grep -n "errors.As"` - Found on line 256 (pattern is used)
8. `grep -n "shouldRetry" internal/controller/pipeline_controller.go` - Found on line 427 (wiring is present)

## Impact Analysis

### Before (Bugs Present)

**Bug 1 - Status update error replaces original error:**
- Status update fails → controller-runtime sees updateErr, not original reconciliation error
- Exponential backoff breaks (wrong error type)
- Status update conflicts cause immediate retry without backoff
- Root cause errors are lost in logs

**Bug 2 - Infinite recursion on 404:**
- Pipeline deleted externally → 404 detected → clear ID → call reconcileNormal
- If recreation fails with 404 again → recurse infinitely
- Stack overflow if recreation keeps failing
- No escape from recursion loop

**Bug 3 - No error classification:**
- All errors treated the same (no transient vs permanent distinction)
- Validation errors retried forever (user can't fix without spec change)
- No centralized retry logic

### After (Bugs Fixed)

**Fix 1 - Original error preserved:**
- Status update fails → logged, but originalErr returned to controller-runtime
- Exponential backoff works correctly (controller-runtime sees original error)
- Status update conflicts trigger Requeue (correct pattern for stale cache)
- Root cause errors visible in logs and backoff behavior

**Fix 2 - Single-retry guard:**
- Pipeline deleted externally → 404 detected → check if ID already empty
- If ID empty (already tried) → return error, no recursion
- If ID not empty (first time) → clear ID, rebuild request, call UpsertPipeline inline
- If inline call fails → handleAPIError classifies new error (safe because ID is empty)
- Maximum one retry per reconciliation, no stack overflow

**Fix 3 - Centralized error classification:**
- shouldRetry helper determines retry behavior
- Validation errors return nil (no retry, user must fix)
- Transient errors return error (exponential backoff)
- Easy to extend with new error types

## Next Phase Readiness

Phase 2 Plan 2 (Enhanced Error Context) can proceed immediately. This plan provides:
- Error classification helpers (isTransientError, shouldRetry)
- Safe status update pattern (preserve original error)
- 404 handling that can be extended with structured context

## Self-Check: PASSED

**Created files:**
- [FOUND] internal/controller/errors.go

**Modified files:**
- [FOUND] internal/controller/pipeline_controller.go

**Commits exist:**
- [FOUND] 810d123 - feat(02-01): add error classification helpers for controller
- [FOUND] 71fbafd - fix(02-01): preserve original error in updateStatusError and prevent 404 recursion

**Pattern verification:**
- [PASSED] No `return ctrl.Result{}, updateErr` (bug removed)
- [PASSED] No `return r.reconcileNormal` in 404 case (recursion removed)
- [PASSED] shouldRetry used in updateStatusError (wiring present)
- [PASSED] errors.As used for FleetAPIError (pattern correct)
- [PASSED] All tests passing

All claims verified. Summary is accurate.
