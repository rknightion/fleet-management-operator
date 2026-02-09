---
status: incomplete
phase: 04-e2e-for-github-actions
source:
  - 04-01-SUMMARY.md
  - 04-02-SUMMARY.md
  - 04-03-SUMMARY.md
started: 2026-02-09T18:00:00Z
updated: 2026-02-09T18:02:00Z
---

## Current Test

number: 3
name: Mock API validates authentication
expected: |
  Sending UpsertPipeline without basic auth credentials returns 401 unauthorized error.
awaiting: user response

## Tests

### 1. Mock API responds to UpsertPipeline requests
expected: Running the mock API server locally and sending an UpsertPipeline request with basic auth returns a JSON response with pipeline ID (starting at 1000), createdAt/updatedAt timestamps, and all requested fields (name, contents, enabled, configType, matchers).
result: pass

### 2. Mock API implements upsert semantics correctly
expected: Sending UpsertPipeline twice with the same pipeline name reuses the same ID, preserves createdAt timestamp, but updates the updatedAt timestamp.
result: pass

### 3. Mock API validates authentication
expected: Sending UpsertPipeline without basic auth credentials returns 401 unauthorized error.
result: [pending]

### 4. E2E test creates Alloy pipeline successfully
expected: Running `make test-e2e` executes the "Create Alloy pipeline" test which applies valid-alloy-pipeline.yaml, waits for status.id assignment, and verifies Ready=True condition with observedGeneration matching generation.
result: [pending]

### 5. E2E test creates OTEL pipeline successfully
expected: Running `make test-e2e` executes the "Create OTEL pipeline" test which applies valid-otel-pipeline.yaml, waits for Ready=True, and verifies status.id is assigned.
result: [pending]

### 6. E2E test updates pipeline successfully
expected: Running `make test-e2e` executes the "Update pipeline" test which applies update-alloy-pipeline.yaml (updated contents/matchers), waits for observedGeneration to advance, and verifies Ready remains True.
result: [pending]

### 7. E2E test validates webhook rejection
expected: Running `make test-e2e` executes the "Webhook rejection" test which attempts to apply invalid-mismatch-pipeline.yaml (wrong configType) and expects webhook denial error. Test skips gracefully if CertManager is disabled.
result: [pending]

### 8. E2E test deletes pipelines successfully
expected: Running `make test-e2e` executes the "Delete pipelines" test which deletes both test pipelines and waits for NotFound errors confirming finalizer removal completed.
result: [pending]

### 9. GitHub Actions E2E workflow triggers on PRs
expected: The .github/workflows/e2e.yaml workflow triggers automatically on pull requests to main branch, creates a Kind cluster, builds images, deploys controller and mock API, and runs E2E tests.
result: [pending]

### 10. GitHub Actions workflow collects failure artifacts
expected: When E2E tests fail in GitHub Actions, the workflow collects and uploads controller logs, mock API logs, pod descriptions, events, Pipeline CR state, and Kind export logs as artifacts with 7-day retention.
result: [pending]

## Summary

total: 10
passed: 2
issues: 0
pending: 8
skipped: 0

## Gaps

[none yet]
