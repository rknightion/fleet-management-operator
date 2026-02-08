# Requirements: Fleet Management Operator - Tech Debt Cleanup

**Defined:** 2026-02-08
**Core Value:** Improve code reliability and maintainability by fixing high-priority technical debt that could cause production failures

## v1 Requirements

Requirements for this tech debt cleanup milestone. Each maps to roadmap phases.

### Error Handling

- [ ] **ERR-01**: HTTP response body read errors are captured and logged in pkg/fleetclient/client.go (lines 88, 139)
- [ ] **ERR-02**: updateStatusError function returns original error instead of status update error (preserves exponential backoff)
- [ ] **ERR-03**: FleetAPIError has IsTransient() method to classify retriable vs permanent errors
- [ ] **ERR-04**: Error classification helpers (isTransientError, shouldRetry) available in controller
- [ ] **ERR-05**: FleetAPIError includes PipelineID field for error tracing

### Status Updates

- [ ] **STAT-01**: Status update conflicts use proper requeue pattern (not immediate retry)
- [ ] **STAT-02**: Original reconciliation error preserved when status update fails
- [ ] **STAT-03**: Status update failures logged with full context

### Reconciliation Safety

- [ ] **REC-01**: External deletion detection has recursion/retry limit (fixes infinite loop risk at line 269)
- [ ] **REC-02**: All reconciliation paths return errors properly (no swallowed errors)
- [ ] **REC-03**: RequeueAfter used consistently for rate limits and transient failures

### Logging & Observability

- [ ] **LOG-01**: All error paths use structured logging with consistent key-value pairs
- [ ] **LOG-02**: Status condition messages include troubleshooting hints
- [ ] **LOG-03**: Condition state transitions are logged
- [ ] **LOG-04**: Pipeline namespace/name included in all log statements

### Testing

- [ ] **TEST-01**: Unit tests added for io.ReadAll() error handling
- [ ] **TEST-02**: Unit tests verify updateStatusError returns original error
- [ ] **TEST-03**: Unit tests cover error classification (IsTransient)
- [ ] **TEST-04**: Unit tests for recursion limit enforcement
- [ ] **TEST-05**: All existing tests continue to pass

### Code Quality

- [ ] **QUAL-01**: No breaking changes to Pipeline CRD or webhook behavior
- [ ] **QUAL-02**: Code follows existing conventions from CLAUDE.md
- [ ] **QUAL-03**: Changes documented in code comments
- [ ] **QUAL-04**: Clean git commit history ready for code review

## v2 Requirements

Deferred to future milestones.

### Metrics & Monitoring
- **MET-01**: Prometheus metrics for API call latency and error rates
- **MET-02**: Metrics for reconciliation duration by result type
- **MET-03**: Rate limiter queue depth metrics
- **MET-04**: Grafana dashboard for operator health

### Advanced Reliability
- **ADV-01**: Circuit breaker pattern for Fleet Management API calls
- **ADV-02**: Distributed tracing integration
- **ADV-03**: Graceful degradation modes

### E2E Testing
- **E2E-01**: Integration tests with real Fleet Management API
- **E2E-02**: Contract tests for API responses
- **E2E-03**: Load tests for concurrent reconciliations

## Out of Scope

| Feature | Reason |
|---------|--------|
| EventRecorder nil check refactoring | Low priority, not a reliability issue |
| HTTP connection pool tuning | Optimization, not correctness |
| Leader election implementation | Already designed, just needs testing |
| Dry-run validation mode | Enhancement, not tech debt |
| Credential rotation without restart | Operational enhancement, separate effort |
| Major architectural changes | Existing structure is sound |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| ERR-01 | TBD | Pending |
| ERR-02 | TBD | Pending |
| ERR-03 | TBD | Pending |
| ERR-04 | TBD | Pending |
| ERR-05 | TBD | Pending |
| STAT-01 | TBD | Pending |
| STAT-02 | TBD | Pending |
| STAT-03 | TBD | Pending |
| REC-01 | TBD | Pending |
| REC-02 | TBD | Pending |
| REC-03 | TBD | Pending |
| LOG-01 | TBD | Pending |
| LOG-02 | TBD | Pending |
| LOG-03 | TBD | Pending |
| LOG-04 | TBD | Pending |
| TEST-01 | TBD | Pending |
| TEST-02 | TBD | Pending |
| TEST-03 | TBD | Pending |
| TEST-04 | TBD | Pending |
| TEST-05 | TBD | Pending |
| QUAL-01 | TBD | Pending |
| QUAL-02 | TBD | Pending |
| QUAL-03 | TBD | Pending |
| QUAL-04 | TBD | Pending |

**Coverage:**
- v1 requirements: 24 total
- Mapped to phases: 0 (pending roadmap creation)
- Unmapped: 24

---
*Requirements defined: 2026-02-08*
*Last updated: 2026-02-08 after initial definition*
