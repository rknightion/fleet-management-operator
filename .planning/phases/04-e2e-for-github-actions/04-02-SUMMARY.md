---
phase: 04-e2e-for-github-actions
plan: 02
subsystem: testing
tags: [e2e-tests, pipeline-lifecycle, mock-api, kubernetes, ginkgo]
status: complete
completed: 2026-02-08

dependencies:
  requires: [04-01]
  provides: [pipeline-e2e-tests]
  affects: [test-automation, ci-cd]

tech-stack:
  added: []
  patterns: [ordered-tests, eventually-polling, webhook-validation-testing]

key-files:
  created:
    - test/e2e/pipeline_test.go
  modified:
    - test/e2e/e2e_suite_test.go
    - test/e2e/e2e_test.go
    - Makefile

decisions:
  - title: "Deploy mock API before controller in BeforeSuite"
    rationale: "Controller reads FLEET_MANAGEMENT_BASE_URL from secret at startup, so mock API and secret must exist first"
  - title: "Move all infrastructure setup to e2e_suite_test.go"
    rationale: "Suite-level BeforeSuite/AfterSuite is the right place for shared infrastructure that all tests depend on"
  - title: "Use docker-build-load instead of docker-build"
    rationale: "docker-build uses buildx with --push (for multi-arch), but E2E tests need --load to load into local Docker for Kind"
  - title: "Skip webhook test if CertManager disabled"
    rationale: "Webhook validation requires CertManager, so test should skip gracefully when CERT_MANAGER_INSTALL_SKIP=true"
  - title: "Use Ordered context for Pipeline lifecycle tests"
    rationale: "Tests must run sequentially: create before update before delete"

metrics:
  duration: 2m
  tasks_completed: 2
  files_modified: 4
  files_created: 1
  tests_added: 5
---

# Phase 04 Plan 02: Pipeline Lifecycle E2E Tests Summary

Pipeline lifecycle E2E tests added with mock API deployment in test suite.

## What Was Done

### Task 1: Update E2E suite setup to deploy mock API

**Updated test/e2e/e2e_suite_test.go:**
- Added `mockAPIImage` constant for mock-fleet-api:test
- Build and load mock API image in BeforeSuite (using docker build)
- Moved namespace creation, CRD installation, and controller deployment from e2e_test.go to BeforeSuite
- Deploy mock API manifests and wait for pod to be Running and Ready before deploying controller
- Updated AfterSuite to clean up in correct order: controller, mock API, CRDs, namespace, then CertManager
- Changed manager image build to use `docker-build-load` instead of `docker-build` to load image locally

**Updated test/e2e/e2e_test.go:**
- Removed duplicate namespace constant (now in e2e_suite_test.go)
- Removed BeforeAll and AfterAll (infrastructure setup moved to suite level)
- Kept AfterEach for failure log collection
- Kept existing Manager context with infrastructure tests

**Updated Makefile:**
- Added `docker-build-mock-api` target to build mock API image with tag mock-fleet-api:test

### Task 2: Write Pipeline lifecycle E2E tests

**Created test/e2e/pipeline_test.go:**
- **Test 1: Create Alloy pipeline** - Applies valid-alloy-pipeline.yaml, waits for status.id to be assigned, verifies Ready=True, checks observedGeneration matches generation
- **Test 2: Create OTEL pipeline** - Applies valid-otel-pipeline.yaml, waits for Ready=True, verifies status.id assigned
- **Test 3: Update pipeline** - Applies update-alloy-pipeline.yaml, waits for observedGeneration to advance, verifies Ready remains True
- **Test 4: Webhook rejection** - Attempts to apply invalid-mismatch-pipeline.yaml, expects webhook denial error (skips if CertManager disabled)
- **Test 5: Delete pipelines** - Deletes both pipelines, waits for NotFound errors confirming finalizer removal
- Uses Ordered context so tests run sequentially (create → update → delete)
- Uses Eventually with 2-minute timeout and 5-second polling for all async assertions
- Adds AfterAll cleanup to delete test pipelines if tests fail partway through

## Deviations from Plan

None - plan executed exactly as written.

## Verification Results

All verification criteria met:
- `go vet -tags=e2e ./test/e2e/...` passes with no errors
- test/e2e/pipeline_test.go exists with all lifecycle scenarios
- e2e_suite_test.go deploys mock API and waits for readiness before controller
- Makefile has docker-build-mock-api target
- Manager image build uses docker-build-load to load into local Docker

## Key Technical Details

**Deployment Order (Critical):**
1. Build and load manager image
2. Build and load mock API image
3. Setup CertManager
4. Create namespace and apply labels
5. Install CRDs
6. Deploy mock API manifests
7. Wait for mock API pod Running and Ready
8. Deploy controller manager

The mock API secret contains `FLEET_MANAGEMENT_BASE_URL` which the controller reads at startup. If the controller starts before the secret exists, it will fail to initialize. This is why the order is critical.

**Test Patterns:**
- All kubectl commands specify `-n fm-crd-system` namespace
- Eventually assertions use Gomega's `func(g Gomega)` pattern for proper failure messages
- ObservedGeneration comparison uses `BeNumerically(">=", expected)` to handle race conditions
- Webhook test checks for "denied" in error output (standard Kubernetes admission webhook response)

**Cleanup Strategy:**
- Suite-level AfterSuite cleans up in reverse order: controller → mock API → CRDs → namespace → CertManager
- Test-level AfterAll cleans up test pipelines with `--ignore-not-found` for idempotency
- Each test uses DeferCleanup (via AfterAll) to handle partial failures

## Self-Check: PASSED

**Files created:**
- FOUND: /Users/mbaykara/work/fleet-management-operator/test/e2e/pipeline_test.go

**Files modified:**
- FOUND: /Users/mbaykara/work/fleet-management-operator/test/e2e/e2e_suite_test.go
- FOUND: /Users/mbaykara/work/fleet-management-operator/test/e2e/e2e_test.go
- FOUND: /Users/mbaykara/work/fleet-management-operator/Makefile

**Commits:**
- FOUND: 36f6326 (Task 1: deploy mock API in E2E suite)
- FOUND: 76de415 (Task 2: Pipeline lifecycle E2E tests)

**Compilation:**
- PASSED: `go vet -tags=e2e ./test/e2e/...` with no errors

**Makefile target:**
- PASSED: `make -n docker-build-mock-api` outputs expected docker build command
