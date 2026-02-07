# Codebase Concerns

**Analysis Date:** 2026-02-08

## Tech Debt

**Unhandled Error in HTTP Response Body Reading:**
- Issue: `io.ReadAll()` errors are silently ignored when parsing API error responses
- Files: `pkg/fleetclient/client.go` (lines 88, 139)
- Impact: Error messages from Fleet Management API may be truncated or malformed in logs/status. Could hide critical API feedback
- Fix approach: Capture and log `io.ReadAll()` errors instead of silently discarding them with `_`. Log truncated body if read fails

**External Deletion Detection Loop:**
- Issue: When pipeline is deleted externally (404), controller recursively calls `reconcileNormal()` which may recurse infinitely if API continues returning 404
- Files: `internal/controller/pipeline_controller.go` (lines 264-269)
- Impact: Potential infinite recursion without depth limit
- Fix approach: Add counter for external deletion retries, break loop after threshold (e.g., 3 attempts), emit warning event

**Status Update Conflicts Not Fully Handled:**
- Issue: ConflictError from `Status().Update()` triggers requeue but no jitter/backoff applied, could cause thundering herd
- Files: `internal/controller/pipeline_controller.go` (lines 336-340, 388-391)
- Impact: Under high load with concurrent updates, many controllers may hit conflict and immediately requeue, creating burst traffic
- Fix approach: Add randomized exponential backoff to requeue delays when conflict detected

**Recorder Nil Checks Throughout:**
- Issue: EventRecorder is checked for nil before each emission in `emitEvent()` and `emitEventf()` helper methods
- Files: `internal/controller/pipeline_controller.go` (lines 86-97)
- Impact: Not a functional issue but indicates Recorder can be nil at runtime (test scenarios). Events won't emit reliably in all contexts
- Fix approach: Document when Recorder is guaranteed non-nil vs when nil checks required; consider enforcing non-nil in constructor

---

## Known Bugs

**Validation Error Handling in HTTP Response:**
- Symptoms: When API returns non-200 status, response body read can fail but error is ignored
- Files: `pkg/fleetclient/client.go` (lines 88, 139)
- Trigger: Any error response from Fleet Management API with read failure
- Workaround: Check API response logs directly if error message appears truncated in controller status

**ObservedGeneration Skips Initial Reconciliation:**
- Symptoms: Pipeline spec changes while controller is starting up could be skipped if controller hasn't yet recorded generation
- Files: `internal/controller/pipeline_controller.go` (line 142)
- Trigger: Rapid create + update of pipeline before first reconciliation completes
- Workaround: Wait for Ready condition to be set before making spec changes in automation

---

## Security Considerations

**Basic Auth Credentials in Memory:**
- Risk: Fleet Management API credentials (username/password) stored in plain memory in Client struct
- Files: `pkg/fleetclient/client.go` (lines 36-37), `cmd/main.go` (lines 189-202)
- Current mitigation: Credentials passed via environment variables at startup, not in config files
- Recommendations:
  - Consider refreshing token-based auth instead of static credentials if Fleet Management API supports it
  - Add secret store integration (Vault, sealed Secrets) for credential rotation
  - Audit that basic auth credentials are never logged (currently enforced in main.go)

**Environment Variable Exposure:**
- Risk: `FLEET_MANAGEMENT_PASSWORD` visible in process environment, accessible via `/proc/[pid]/environ` on Linux
- Files: `cmd/main.go` (lines 189-202)
- Current mitigation: Only readable by controller process owner
- Recommendations:
  - Document requirement to run controller with restrictive file permissions
  - Consider using Kubernetes Service Account tokens if Fleet Management API supports OIDC

**No TLS Cert Pinning:**
- Risk: Controller validates only standard certificate chain, vulnerable to MITM on network where Fleet Management endpoint is not isolated
- Files: `pkg/fleetclient/client.go` (lines 46-52)
- Current mitigation: TLSHandshakeTimeout set to 10 seconds limits exposure window
- Recommendations:
  - For production: implement certificate pinning or add optional mTLS configuration

---

## Performance Bottlenecks

**Rate Limiter Global Across Controller Instance:**
- Problem: Rate limiter at 3 req/s is shared across all pipelines managed by single controller instance. High volume deployments may have all reconciliations queued
- Files: `pkg/fleetclient/client.go` (lines 54-55)
- Cause: Single `rate.Limiter` instance with limit(3), not per-pipeline queuing
- Improvement path:
  - Monitor actual API call patterns in production to confirm bottleneck
  - Consider if rate limit applies globally or per-endpoint/per-stack
  - Potential: implement per-pipeline backoff instead of global limiter, or request higher rate limit from Fleet Management team

**HTTP Client Connection Pooling Not Optimized:**
- Problem: MaxIdleConns=100 may be excessive for single controller managing < 50 pipelines, wastes resources
- Files: `pkg/fleetclient/client.go` (lines 48-50)
- Cause: Default settings not tuned for typical operator usage
- Improvement path: Make MaxIdleConns configurable via environment variable or flag

**No Exponential Backoff for Transient Errors:**
- Problem: Non-rate-limit errors return error to be handled by controller-runtime's default backoff (1s, 2s, 4s...), but this is not logged/visible
- Files: `internal/controller/pipeline_controller.go` (lines 295, 404)
- Cause: Relying on controller-runtime default behavior without instrumentation
- Improvement path: Add explicit exponential backoff tracking in status conditions so operators can see retry state

---

## Fragile Areas

**ConfigType Validation Uses String Contains Heuristics:**
- Files: `api/v1alpha1/pipeline_webhook.go` (lines 108-138)
- Why fragile: Regex patterns like `prometheus.scrape` could appear in comments, YAML keys, or docstrings without being actual components. Validation is not exhaustive
- Safe modification:
  - When adding new validation rules: always test edge cases like escaped strings, YAML multi-line values
  - Consider formal parsing instead of string patterns if validation becomes more complex
- Test coverage: `api/v1alpha1/pipeline_webhook_test.go` covers basic cases but not edge cases with special YAML characters

**Matcher Syntax Validation Regex:**
- Files: `api/v1alpha1/pipeline_webhook.go` (lines 205, 220-226)
- Why fragile: Regex pattern allows any value after operator (`.+$`), doesn't validate regex syntax for `=~` and `!~` operators
- Safe modification:
  - If regex matchers become more common, add separate validation for regex pattern syntax
  - Test with malformed regex like `key=~[a-` (unclosed bracket)
- Test coverage: Covers basic operators but not invalid regex patterns

**External Deletion Detected But Not Fully Audited:**
- Files: `internal/controller/pipeline_controller.go` (lines 264-269)
- Why fragile: Detecting 404 and attempting recreation works, but if external system is intentionally deleting pipelines this creates thrashing
- Safe modification:
  - Add check for repeated external deletion in short time window (e.g., 5 deletions in 10 seconds)
  - Emit strong warning event to alert operators
  - Consider adding opt-in flag to disable auto-recreation

**Test Mock Client Doesn't Validate Request Content:**
- Files: `internal/controller/pipeline_controller_test.go` (lines 53-96)
- Why fragile: Mock accepts any request without validation, so tests may pass even if real API would reject the request
- Safe modification:
  - Add validation to mock client that mirrors webhook validation rules
  - Ensure mock returns 400 for invalid configs before tests pass
- Test coverage: Good coverage of happy path and 404/429/400 responses, but mocks don't validate configType/matchers

---

## Scaling Limits

**No Horizontal Scaling - Single Controller Instance:**
- Current capacity: Single controller instance with rate limit 3 req/s can manage approximately 150-300 pipelines (assuming 1-2 updates/day per pipeline)
- Limit: If cluster needs > 500 pipelines with frequent updates, single controller becomes bottleneck at rate limit
- Scaling path:
  - Implement leader election (flags present but not enabled by default in `cmd/main.go` line 69)
  - Enable leader election to allow multi-instance deployments: `make deploy ENABLE_LEADER_ELECT=true`
  - Verify controller handles distributed cache consistency with Informer watches

**Cache Consistency Without Read-Your-Writes:**
- Current capacity: Controller relies on Informer cache which has eventual consistency, not immediate
- Limit: If operator chains multiple kubectl apply commands rapidly, changes may not be visible to controller immediately
- Scaling path:
  - Document that direct API calls (not through kubectl) bypass cache
  - If critical, implement status update that forces controller-runtime to skip cache on next Get()

**No Garbage Collection for Failed Pipelines:**
- Current capacity: Failed pipelines in status `Ready=False` accumulate indefinitely in etcd
- Limit: After 10,000+ failed pipelines, cluster etcd could be impacted
- Scaling path:
  - Implement TTL-based cleanup for failed pipelines (e.g., remove status after 30 days)
  - Consider implementing finalizer-based cleanup of old failed attempts

---

## Dependencies at Risk

**controller-runtime Version Not Pinned Aggressively:**
- Risk: Minor/patch updates to controller-runtime could introduce API changes or performance regressions without explicit testing
- Files: Would be in `go.mod` (not checked for content)
- Impact: Upgrading controller-runtime could unexpectedly break reconciliation
- Migration plan:
  - Pin to specific minor version (e.g., `sigs.k8s.io/controller-runtime v0.23.x`)
  - Test each minor version upgrade in staging before production roll
  - Subscribe to upstream controller-runtime security advisories

**No Explicit Error Type Handling for Deprecated Fleet APIs:**
- Risk: If Fleet Management API deprecates endpoints without notice, controller will get 404 and attempt recreation indefinitely
- Files: `pkg/fleetclient/client.go`, `internal/controller/pipeline_controller.go`
- Impact: Pipelines could enter failed state without clear error message
- Migration plan:
  - Add version detection for Fleet Management API (e.g., via new endpoint or version header)
  - Implement deprecation warning events when API compatibility drops

---

## Missing Critical Features

**No Dry-Run / Validation-Only Mode:**
- Problem: Controller always calls UpsertPipeline with `ValidateOnly: false`. No way to validate config without applying
- Blocks: Cannot preview what will be sent to Fleet Management API before committing
- Recommendation:
  - Add optional CRD annotation `fleetmanagement.grafana.com/validate-only: "true"` to test configs
  - Emit event with validation result but don't update pipeline ID
  - Document in webhook setup guide

**No Webhook for Pipeline Validation Dry-Run:**
- Problem: Only validating on create/update in admission webhook. No explicit test endpoint
- Blocks: CI/CD pipelines cannot validate configs before committing CRs
- Recommendation:
  - Add `kubectl pipeline validate <resource.yaml>` command via kubectl plugin
  - Alternative: Make webhook validation accessible via standard kubectl apply with `--dry-run=server`

**No Observability Metrics for Fleet API Calls:**
- Problem: No prometheus metrics for API call latency, error rates, or rate limiter queue depth
- Blocks: Cannot observe operator health without parsing logs or checking controller status
- Recommendation:
  - Add metrics: `fleet_api_calls_total`, `fleet_api_duration_seconds`, `fleet_rate_limiter_queue_depth`
  - Export via standard `/metrics` endpoint

**No Webhook Retry Logic for Failed Validations:**
- Problem: If webhook validation fails during admission, object is rejected entirely. No indication of why
- Blocks: Users cannot easily diagnose validation errors
- Recommendation:
  - Add detailed error messages from validation helpers to webhook response
  - Consider adding webhook pre-flight check endpoint for testing

---

## Test Coverage Gaps

**E2E Tests Not Fully Implemented:**
- What's not tested: End-to-end reconciliation of actual Fleet Management API (using real credentials)
- Files: `test/e2e/e2e_test.go` (lines 57, 271)
- Risk: Changes to controller or API integration could break production without detection
- Priority: HIGH - This is critical for reliability
- Recommendation:
  - Implement e2e tests with sandbox Fleet Management stack or mock server with HTTP contract testing
  - Test full lifecycle: create → update matchers → delete
  - Test external deletion scenario

**No Tests for Credential Rotation:**
- What's not tested: What happens if `FLEET_MANAGEMENT_PASSWORD` is rotated while controller is running
- Files: `cmd/main.go`, `pkg/fleetclient/client.go`
- Risk: Rotating credentials may leave controller in broken state
- Priority: MEDIUM - Only affects ops procedures
- Recommendation:
  - Document manual restart is required after credential rotation
  - Consider adding controller flag to reload credentials from Secret at runtime

**No Load Tests for Rate Limiter:**
- What's not tested: Behavior when concurrent reconciliations exceed 3 req/s limit
- Files: `pkg/fleetclient/client.go`
- Risk: Unknown how many controllers/pipelines can be safely deployed
- Priority: MEDIUM - Depends on scale expectations
- Recommendation:
  - Implement load test creating 100+ pipelines simultaneously
  - Measure queue depth and latency impact

**Webhook Validation Not Tested Against Real Fleet API:**
- What's not tested: Validation catches all configs that would fail on real Fleet API
- Files: `api/v1alpha1/pipeline_webhook_test.go`
- Risk: Valid webhook configs could still fail on actual API submission
- Priority: HIGH - Validation is only guarantee before API call
- Recommendation:
  - Add integration tests that submit webhook-validated configs to sandbox Fleet Management API
  - Verify no 400 errors from API on configs that passed webhook

**No Tests for Status Update Conflicts:**
- What's not tested: Multiple controllers updating same pipeline status simultaneously
- Files: `internal/controller/pipeline_controller_test.go`
- Risk: Race conditions on status field not detected until production
- Priority: MEDIUM - Only affects leader election scenarios
- Recommendation:
  - Add concurrent test: 2+ mock client calls updating same status
  - Verify conflict handling doesn't leak pipelines

---

## Operational Gaps

**No Monitoring for Stale Pipelines:**
- Problem: Pipeline with `Ready=False` for > 24h receives no alert
- Files: Status conditions in `api/v1alpha1/pipeline_types.go`
- Recommendation:
  - Add PrometheusRule for pipelines stuck in non-Ready state for configurable threshold
  - Document in deployment guide

**No Documentation of Failure Scenarios:**
- Problem: Operators may not understand what causes each error reason (SyncFailed vs ValidationError vs RateLimited)
- Files: `internal/controller/pipeline_controller.go` (reason constants)
- Recommendation:
  - Add runbook linking error reasons to troubleshooting steps
  - Include example commands to debug each error type

---

*Concerns audit: 2026-02-08*
