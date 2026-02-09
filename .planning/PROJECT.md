# Fleet Management Operator

## What This Is

A production Kubernetes operator that manages Grafana Cloud Fleet Management Pipelines as CRDs, with robust error handling, structured logging, and comprehensive E2E testing infrastructure. Built with controller-runtime, it provides reliable reconciliation of Pipeline resources with proper status tracking, finalizer-based cleanup, and admission webhook validation.

## Core Value

Reliable, maintainable operator code with comprehensive error handling and observability that prevents production failures.

## Current State

**Latest Release:** v1.0.0 Production-Ready Operator (2026-02-09)

Production-ready Kubernetes operator with:
- Robust error handling and structured logging
- Complete E2E testing infrastructure
- Verified Kubernetes controller best practices (cache usage, reconcile loop efficiency, watch configuration)
- 13 AST-based verification tests preventing regressions
- Comprehensive documentation with grep-able audit markers

## Next Milestone Goals

(To be defined - run `/gsd:new-milestone` to start planning)

## Requirements

### Validated

**v1.0.0 - Production-Ready Operator (2026-02-09):**
- ✓ Enhanced FleetAPIError with IsTransient() method for error classification — v1.0
- ✓ FleetAPIError includes PipelineID field for distributed tracing — v1.0
- ✓ HTTP response body read errors captured and logged with full context — v1.0
- ✓ Status update failures preserve original reconciliation error for exponential backoff — v1.0
- ✓ External deletion detection has single-retry guard preventing infinite loops — v1.0
- ✓ Error classification helpers (isTransientError, shouldRetry) using errors.As — v1.0
- ✓ Status update conflicts use proper requeue pattern (not immediate retry) — v1.0
- ✓ All error paths use structured logging with namespace/name key-value pairs — v1.0
- ✓ Status condition messages include actionable troubleshooting hints via formatConditionMessage — v1.0
- ✓ Condition state transitions logged explicitly for timeline reconstruction — v1.0
- ✓ Unit tests for error handling, status updates, recursion limits, and logging quality — v1.0
- ✓ Mock Fleet Management API server for E2E testing without external dependencies — v1.0
- ✓ Complete Pipeline lifecycle E2E tests (create, update, delete, webhook validation) — v1.0
- ✓ GitHub Actions CI/CD workflow with Kind cluster and failure artifact collection — v1.0

**Pre-existing:**
- ✓ Pipeline CRD with Alloy and OpenTelemetry Collector support
- ✓ Admission webhook validation (configType, matchers)
- ✓ Rate-limited Fleet Management API client (3 req/s)
- ✓ Reconciliation with finalizer-based cleanup
- ✓ Status conditions tracking (Ready, Synced)
- ✓ ObservedGeneration optimization
- ✓ Idempotent UpsertPipeline operations
- ✓ Event recording for observability

### Active

(To be defined in next milestone)

### Out of Scope

- Observability metrics (Prometheus) — defer to future milestone
- Dry-run validation mode — defer to future milestone
- Credential rotation without restart — defer to future milestone
- HTTP client connection pool tuning — optimization, not reliability issue
- Leader election testing — not critical tech debt

## Context

**Current State (v1.0.0):**
- 2,778 lines of Go code in internal/ (plus test files)
- Tech stack: Go 1.25.0, controller-runtime v0.23.0, Ginkgo/Gomega for E2E tests
- 13 AST-based verification tests for best practices enforcement
- Comprehensive documentation with grep-able audit prefixes

**Shipped v1.0.0 (2026-02-09):**
- Enhanced error handling at client and controller layers
- Production-grade structured logging with actionable error messages
- Complete E2E testing infrastructure for CI/CD
- Verified production-ready Kubernetes controller patterns
- Documented all cache, reconcile, and watch configuration decisions
- Added regression prevention tests for best practices

**Technical Decisions:**
- Use in-memory sync.Map for mock API (simple, sufficient for testing)
- Start mock API IDs at 1000 (distinguishes from real API IDs)
- Deploy mock API in-cluster before controller (controller reads URL at startup)
- Use docker-build-load for Kind compatibility (buildx --push doesn't work locally)

## Constraints

- **Backward Compatibility**: No breaking changes to Pipeline CRD, webhook behavior, or external APIs
- **Testing**: Must add unit tests for each fix, existing tests must continue passing
- **Go Version**: 1.25.0 (no upgrade needed)
- **Dependencies**: Use existing dependency versions (controller-runtime v0.23.0, etc.)
- **Code Style**: Follow existing conventions from CLAUDE.md and codebase patterns

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Fix only high-priority tech debt | Focus on reliability issues, defer enhancements | ✓ Good - Completed 24/24 requirements |
| Internal fixes only (no API changes) | Maintain backward compatibility, minimize risk | ✓ Good - No breaking changes |
| Add unit tests, not E2E tests (Phase 1-3) | Quick validation, E2E tests are separate effort | ✓ Good - Unit tests added for all fixes |
| Use existing error handling patterns | Consistency with codebase conventions | ✓ Good - Follows controller-runtime patterns |
| Use errors.As instead of type assertion for FleetAPIError | Handles wrapped errors correctly | ✓ Good - Works with fmt.Errorf("%w") |
| Preserve original error in updateStatusError | Enables proper exponential backoff | ✓ Good - Controller-runtime gets right error |
| Single-retry guard for 404 recreation | Prevents infinite recursion | ✓ Good - Simple, effective |
| Use formatConditionMessage for all status messages | Improves user experience with actionable hints | ✓ Good - Consistent messaging |
| Add namespace/name to every log statement | Enables log correlation for concurrent reconciliation | ✓ Good - Critical for debugging |
| Add E2E testing (Phase 4) | Originally out of scope, added as new phase | ✓ Good - Valuable CI/CD automation |
| Use in-memory mock API | Avoids external dependencies, rate limits, secrets | ✓ Good - Fast, reliable tests |
| Deploy mock API before controller | Controller reads Fleet Management URL at startup | ✓ Good - Correct initialization order |
| Use AST parsing for best practices verification | Compile-time enforcement vs runtime checks | ✓ Good - Catches violations during tests |
| Document with "Cache:", "Reconcile:", "Watch:" prefixes | Grep-able audit markers for tooling | ✓ Good - Supports automated compliance checking |
| Resync disabled (nil SyncPeriod) | Watch-driven controller, no external drift | ✓ Good - Eliminates unnecessary reconciliation load |
| Default workqueue rate limiter | Defense in depth with Fleet API rate limiting | ✓ Good - Exponential backoff + throughput cap |

---
*Last updated: 2026-02-09 after completing v1.0.0 milestone*
