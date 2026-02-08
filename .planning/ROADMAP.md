# Roadmap: Fleet Management Operator - Tech Debt Cleanup

## Overview

This roadmap addresses critical technical debt in the fleet-management-operator through a focused, three-phase approach. Starting with client layer error foundations, progressing through controller error handling fixes, and completing with logging and quality improvements, each phase delivers testable, production-ready reliability enhancements without changing external APIs.

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [ ] **Phase 1: Client Layer Error Foundation** - Enhance FleetAPIError and fix HTTP response handling
- [ ] **Phase 2: Controller Error Handling** - Fix critical bugs in status updates and reconciliation
- [ ] **Phase 3: Logging & Quality** - Standardize observability and finalize quality checks

## Phase Details

### Phase 1: Client Layer Error Foundation
**Goal**: HTTP client reliably captures and reports all error conditions with structured error types
**Depends on**: Nothing (first phase)
**Requirements**: ERR-01, ERR-03, ERR-05, TEST-01, TEST-03
**Success Criteria** (what must be TRUE):
  1. HTTP response body read failures are caught and logged with full context
  2. FleetAPIError instances indicate whether errors are transient (retriable) or permanent
  3. FleetAPIError instances include PipelineID for distributed tracing
  4. Error type assertions work correctly with wrapped errors (errors.As compatibility)
  5. Unit tests verify all error handling paths in HTTP client
**Plans:** 2 plans

Plans:
- [ ] 01-01-PLAN.md -- Enhance FleetAPIError type and fix io.ReadAll error handling in client.go
- [ ] 01-02-PLAN.md -- Unit tests for FleetAPIError and HTTP client error paths

### Phase 2: Controller Error Handling
**Goal**: Controller reconciliation correctly handles all error types with proper retry semantics
**Depends on**: Phase 1
**Requirements**: ERR-02, ERR-04, STAT-01, STAT-02, STAT-03, REC-01, REC-02, REC-03, TEST-02, TEST-04
**Success Criteria** (what must be TRUE):
  1. Status update failures preserve original reconciliation error for exponential backoff
  2. External deletion detection has recursion limit preventing infinite loops
  3. Transient errors (network, 5xx) trigger requeue while permanent errors (validation) do not
  4. Status update conflicts use proper requeue pattern without immediate retry
  5. All reconciliation error paths return errors properly to controller-runtime
  6. Unit tests demonstrate correct error classification and retry behavior
**Plans:** 2 plans

Plans:
- [ ] 02-01-PLAN.md -- Fix updateStatusError, handleAPIError recursion, and add error classification helpers
- [ ] 02-02-PLAN.md -- Unit tests for error classification, status error preservation, and recursion limits

### Phase 3: Logging & Quality
**Goal**: All code paths have production-grade observability and pass quality gates
**Depends on**: Phase 2
**Requirements**: LOG-01, LOG-02, LOG-03, LOG-04, QUAL-01, QUAL-02, QUAL-03, QUAL-04, TEST-05
**Success Criteria** (what must be TRUE):
  1. All error paths use structured logging with consistent key-value pairs
  2. Status condition messages include actionable troubleshooting hints
  3. Condition state transitions are logged for debugging
  4. All log statements include pipeline namespace and name
  5. No breaking changes to Pipeline CRD or webhook behavior
  6. All existing tests continue to pass
  7. Code follows existing conventions from CLAUDE.md
  8. Changes are documented in code comments
  9. Git commit history is clean and ready for code review
**Plans:** 2 plans

Plans:
- [ ] 03-01-PLAN.md -- Structured logging improvements, formatConditionMessage helper, and condition transition logging
- [ ] 03-02-PLAN.md -- Unit tests for formatConditionMessage and quality gate verification

### Phase 4: E2E for GitHub Actions
**Goal**: End-to-end tests run automatically in GitHub Actions against a Kind cluster with a mock Fleet Management API, validating the full Pipeline CR lifecycle (create, update, delete) and webhook validation
**Depends on**: Phase 3
**Success Criteria** (what must be TRUE):
  1. Mock Fleet Management API server accepts UpsertPipeline and DeletePipeline requests
  2. E2E tests verify Pipeline CR creation reaches Ready=True status
  3. E2E tests verify Pipeline CR update is reconciled
  4. E2E tests verify Pipeline CR deletion removes finalizer
  5. E2E tests verify admission webhook rejects invalid Pipeline CRs
  6. GitHub Actions workflow runs E2E tests on PRs and pushes to main
  7. Failure artifacts (logs, events, pod descriptions) are collected on test failure
**Plans:** 3 plans

Plans:
- [ ] 04-01-PLAN.md -- Mock Fleet Management API server, container image, K8s manifests, and test fixtures
- [ ] 04-02-PLAN.md -- Pipeline lifecycle E2E tests and suite setup with mock API deployment
- [ ] 04-03-PLAN.md -- GitHub Actions E2E workflow with Kind cluster and failure artifact collection

## Progress

**Execution Order:**
Phases execute in numeric order: 1 -> 2 -> 3 -> 4

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Client Layer Error Foundation | 2/2 | Complete | 2026-02-08 |
| 2. Controller Error Handling | 0/2 | Planning complete | - |
| 3. Logging & Quality | 0/2 | Planning complete | - |
| 4. E2E for GitHub Actions | 0/3 | Planning complete | - |

---
*Roadmap created: 2026-02-08*
*Last updated: 2026-02-09*
