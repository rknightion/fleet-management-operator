# Fleet Management Operator - Tech Debt Cleanup

## What This Is

A focused technical debt cleanup initiative for the fleet-management-operator, a Kubernetes operator that manages Grafana Cloud Fleet Management Pipelines as CRDs. This milestone addresses critical code quality issues identified in the codebase audit without changing external behavior or APIs.

## Core Value

Improve code reliability and maintainability by fixing high-priority technical debt: error handling gaps, infinite loop risks, and concurrency issues that could cause production failures.

## Requirements

### Validated

- ✓ Pipeline CRD with Alloy and OpenTelemetry Collector support — existing
- ✓ Admission webhook validation (configType, matchers) — existing
- ✓ Rate-limited Fleet Management API client (3 req/s) — existing
- ✓ Reconciliation with finalizer-based cleanup — existing
- ✓ Status conditions tracking (Ready, Synced) — existing
- ✓ ObservedGeneration optimization — existing
- ✓ Idempotent UpsertPipeline operations — existing
- ✓ Event recording for observability — existing
- ✓ Unit test coverage for core logic — existing

### Active

- [ ] Fix ignored io.ReadAll() errors in HTTP response handling
- [ ] Add recursion limit for external deletion retry loop
- [ ] Implement exponential backoff for status update conflicts
- [ ] Add unit tests for all fixed behaviors
- [ ] Ensure no breaking changes to external APIs
- [ ] Document fixes in code comments
- [ ] Clean commit history ready for code review

### Out of Scope

- Observability metrics (Prometheus) — defer to future milestone
- E2E test implementation — defer to future milestone
- Dry-run validation mode — defer to future milestone
- Credential rotation without restart — defer to future milestone
- EventRecorder nil check refactoring — low priority, defer
- HTTP client connection pool tuning — optimization, not reliability
- Leader election testing — not critical tech debt

## Context

This is a brownfield project with an existing, functional Kubernetes operator codebase. A recent codebase audit (2026-02-08) identified several high-priority technical debt items that pose reliability risks:

**Current Issues:**
1. **Error Handling Gap (pkg/fleetclient/client.go:88, 139)**: io.ReadAll() errors are silently ignored, potentially truncating API error messages in logs and status conditions
2. **Infinite Loop Risk (internal/controller/pipeline_controller.go:264-269)**: External deletion detection recursively calls reconcileNormal() without depth limit, could cause infinite recursion
3. **Conflict Storm (internal/controller/pipeline_controller.go:336-340, 388-391)**: Status update conflicts trigger immediate requeue without backoff, risking thundering herd under load

**Existing Architecture:**
- Controller-runtime based operator (v0.23.0)
- Go 1.25.0 with standard Kubernetes patterns
- Comprehensive CLAUDE.md with patterns and gotchas
- Existing unit test suite with table-driven tests
- Mock client for API interactions

## Constraints

- **Backward Compatibility**: No breaking changes to Pipeline CRD, webhook behavior, or external APIs
- **Testing**: Must add unit tests for each fix, existing tests must continue passing
- **Timeline**: Quick, focused fixes suitable for 1-2 day completion
- **Go Version**: 1.25.0 (no upgrade needed)
- **Dependencies**: Use existing dependency versions (controller-runtime v0.23.0, etc.)
- **Code Style**: Follow existing conventions from CLAUDE.md and codebase patterns

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Fix only high-priority tech debt | Focus on reliability issues, defer enhancements | — Pending |
| Internal fixes only (no API changes) | Maintain backward compatibility, minimize risk | — Pending |
| Add unit tests, not E2E tests | Quick validation, E2E tests are separate effort | — Pending |
| Use existing error handling patterns | Consistency with codebase conventions | — Pending |

---
*Last updated: 2026-02-08 after initialization*
