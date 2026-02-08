---
phase: 04-e2e-for-github-actions
plan: 03
subsystem: ci-cd
tags: [github-actions, e2e-tests, kind, ci-automation, failure-diagnostics]
status: complete
completed: 2026-02-09

dependencies:
  requires: [04-02]
  provides: [github-actions-e2e-workflow]
  affects: [ci-cd, test-automation]

tech-stack:
  added: [helm-kind-action]
  patterns: [ci-e2e-testing, failure-artifact-collection]

key-files:
  created:
    - .github/workflows/e2e.yaml
  modified: []

decisions:
  - title: "Use helm/kind-action for Kind cluster management"
    rationale: "Handles cluster creation, cleanup, and lifecycle automatically in GitHub Actions"
  - title: "Set KIND_CLUSTER=fm-crd-e2e environment variable"
    rationale: "Ensures workflow's Kind cluster name matches Makefile's expected cluster name for E2E tests"
  - title: "15-minute timeout for E2E test execution"
    rationale: "Allows sufficient time for: image builds (controller + mock API), Kind cluster setup, controller deployment, and test execution"
  - title: "Comprehensive failure artifact collection"
    rationale: "Collects controller logs, mock API logs, pod descriptions, events, Pipeline CR state, and Kind export logs to enable debugging of CI failures"
  - title: "Trigger on PR to main and push to main"
    rationale: "Runs E2E tests for all PR changes before merge and validates main branch after merge"

metrics:
  duration: 1m
  tasks_completed: 2
  files_created: 1
  tests_added: 0
  completed_date: 2026-02-09
---

# Phase 04 Plan 03: GitHub Actions E2E Workflow Summary

GitHub Actions workflow for automated E2E testing on Kind clusters with comprehensive failure diagnostics

## What Was Done

### Task 1: Create GitHub Actions E2E workflow

**Status:** Complete
**Commit:** 5b6c182

Created `.github/workflows/e2e.yaml` implementing complete E2E test automation:

**Workflow structure:**
- Triggers on `pull_request` and `push` to main branch
- Uses actions/checkout@v4 and actions/setup-go@v5 (consistent with ci.yaml)
- Sets up Go using go-version-file: go.mod with cache enabled
- Creates Kind cluster named "fm-crd-e2e" using helm/kind-action@v1
- Waits 5 minutes for cluster to be ready
- Verifies cluster with kubectl cluster-info and kubectl get nodes
- Runs `make test-e2e` with KIND_CLUSTER=fm-crd-e2e environment variable
- 15-minute timeout for test execution

**Failure artifact collection (only on failure):**
- Controller logs: kubectl logs -n fm-crd-system -l control-plane=controller-manager
- Mock API logs: kubectl logs -n fm-crd-system -l app=mock-fleet-api
- Pod descriptions: kubectl describe pods -n fm-crd-system
- Kubernetes events: kubectl get events --sort-by=.lastTimestamp
- Pipeline CR state: kubectl get pipelines -o yaml
- Kind export logs: kind export logs logs/kind-export
- All artifacts uploaded with 7-day retention via actions/upload-artifact@v4

**Key implementation details:**
- All failure collection commands use `|| true` to continue on errors (prevents cleanup failures from masking test failures)
- Logs written to logs/ directory created by the workflow
- Uses --tail=-1 to capture full controller and mock API logs
- Uploads entire logs/ directory as single artifact named "e2e-failure-logs"

**Verification results:**
```bash
# YAML syntax valid
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/e2e.yaml'))"
# No errors

# Required elements present
grep "helm/kind-action" .github/workflows/e2e.yaml
# Line 27: uses: helm/kind-action@v1

grep "make test-e2e" .github/workflows/e2e.yaml
# Line 38: run: make test-e2e

grep "upload-artifact" .github/workflows/e2e.yaml
# Line 59: uses: actions/upload-artifact@v4

grep "failure()" .github/workflows/e2e.yaml
# Lines 44, 54, 58: if: failure()
```

### Task 2: Verify complete E2E testing infrastructure

**Status:** Complete (human-verify checkpoint)
**Outcome:** User approved

Human verification checkpoint for the complete E2E testing infrastructure built across all three plans:
1. Mock Fleet Management API server (test/mockapi/)
2. Pipeline lifecycle E2E tests (test/e2e/pipeline_test.go)
3. Updated E2E suite setup with mock API deployment
4. GitHub Actions workflow (.github/workflows/e2e.yaml)
5. Updated Makefile targets

User reviewed and approved the infrastructure. No issues found.

## Deviations from Plan

None - plan executed exactly as written.

## Verification Results

All verification criteria met:
- .github/workflows/e2e.yaml is valid YAML syntax
- Workflow uses helm/kind-action@v1 for cluster setup
- Workflow runs make test-e2e with KIND_CLUSTER environment variable
- Failure artifacts include: controller logs, mock API logs, events, pod descriptions, pipeline state, Kind export logs
- Workflow has 15-minute timeout for test execution
- No hardcoded secrets or credentials
- Triggers configured for pull_request and push to main

## Key Technical Details

**GitHub Actions Integration:**

The workflow integrates with existing CI infrastructure:
- Consistent with .github/workflows/ci.yaml patterns (checkout@v4, setup-go@v5)
- Uses go-version-file: go.mod to automatically match project Go version
- Enables Go module caching for faster builds
- Sets permissions: contents: read (principle of least privilege)

**Kind Cluster Management:**

helm/kind-action@v1 provides:
- Automatic cluster creation with configurable name
- Built-in wait for cluster readiness (wait: 5m)
- Automatic cleanup on workflow completion (even on failure)
- Pre-configured kubeconfig for kubectl access

**Makefile Integration:**

The workflow relies on `make test-e2e` which orchestrates:
1. Building controller image (docker-build-load)
2. Building mock API image (docker-build-mock-api)
3. Loading both images into Kind cluster
4. Running ginkgo E2E tests

The KIND_CLUSTER environment variable ensures the workflow's cluster name matches the Makefile's expected cluster name.

**Failure Diagnostics Strategy:**

Artifact collection covers all layers:
- Application logs: controller and mock API
- Kubernetes state: pod descriptions, events
- Custom resources: Pipeline CR state
- Infrastructure: Kind export logs (kubelet logs, container logs, etc.)

All commands use `|| true` to ensure one failure doesn't prevent collection of other artifacts.

**Workflow Execution Time:**

15-minute timeout allows for:
- Go module download and caching (1-2 min)
- Docker image builds (2-3 min for controller + mock API)
- Kind cluster setup (1-2 min)
- CRD installation and controller deployment (1-2 min)
- Test execution (5-7 min for complete lifecycle tests)
- Buffer for slow GitHub Actions runners

## Complete E2E Infrastructure Summary

This plan completes the E2E testing infrastructure initiated in Phase 04:

**Plan 04-01: Mock Fleet Management API and E2E Test Fixtures**
- Mock API server (test/mockapi/main.go) with UpsertPipeline and DeletePipeline endpoints
- Kubernetes manifests for mock API deployment
- Test fixtures: valid-alloy, valid-otel, invalid-mismatch, update-alloy Pipeline CRs

**Plan 04-02: Pipeline Lifecycle E2E Tests**
- E2E suite setup with mock API deployment in BeforeSuite
- 5 Pipeline lifecycle tests: create Alloy, create OTEL, update, webhook rejection, delete
- Makefile targets: docker-build-load, docker-build-mock-api

**Plan 04-03: GitHub Actions E2E Workflow (this plan)**
- Automated E2E testing on every PR and main branch push
- Kind cluster provisioning with helm/kind-action
- Comprehensive failure artifact collection

**Result:** Full CI/CD integration for E2E testing with no external dependencies (mocked Fleet Management API).

## Self-Check: PASSED

**Files created:**
- FOUND: /Users/mbaykara/work/fleet-management-operator/.github/workflows/e2e.yaml

**Commits:**
- FOUND: 5b6c182 (Task 1: GitHub Actions E2E workflow)

**YAML validation:**
- PASSED: Python yaml.safe_load() succeeded
- PASSED: All required elements present (helm/kind-action, make test-e2e, upload-artifact, failure())

**Workflow structure:**
- PASSED: Triggers on pull_request and push to main
- PASSED: Uses correct Go setup with go-version-file
- PASSED: KIND_CLUSTER=fm-crd-e2e set for make test-e2e
- PASSED: 15-minute timeout configured
- PASSED: Failure artifacts collect all diagnostic data
