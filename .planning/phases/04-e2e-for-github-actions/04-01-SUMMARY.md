---
phase: 04-e2e-for-github-actions
plan: 01
subsystem: testing
tags: [e2e, mock-api, test-infrastructure, fixtures]
dependency_graph:
  requires: []
  provides: [mock-fleet-api-server, e2e-test-fixtures]
  affects: []
tech_stack:
  added: [mock-api-server]
  patterns: [http-server, in-memory-storage]
key_files:
  created:
    - test/mockapi/main.go
    - test/mockapi/go.mod
    - test/mockapi/Dockerfile
    - test/mockapi/manifests/deployment.yaml
    - test/mockapi/manifests/service.yaml
    - test/mockapi/manifests/secret.yaml
    - test/e2e/fixtures/valid-alloy-pipeline.yaml
    - test/e2e/fixtures/valid-otel-pipeline.yaml
    - test/e2e/fixtures/invalid-mismatch-pipeline.yaml
    - test/e2e/fixtures/update-alloy-pipeline.yaml
  modified: []
decisions:
  - Use in-memory sync.Map for pipeline storage (simple, sufficient for testing)
  - Start pipeline IDs at 1000 (distinguishes from real API IDs)
  - Accept any basic auth credentials (simplifies E2E test setup)
  - Use standalone go.mod for mockapi (independent binary, not part of main project)
  - Use gcr.io/distroless/static:nonroot runtime (minimal, secure, follows best practices)
metrics:
  duration: 2m
  tasks_completed: 2
  files_created: 10
  tests_added: 0
  completed_date: 2026-02-08
---

# Phase 04 Plan 01: Mock Fleet Management API and E2E Test Fixtures

Mock Fleet Management API server for E2E testing in CI without external dependencies

## Summary

Created a lightweight HTTP server that mimics the Fleet Management Pipeline API contract, including UpsertPipeline and DeletePipeline endpoints with proper JSON responses, authentication, and health checks. Added Kubernetes manifests to deploy the mock API in Kind clusters and created comprehensive Pipeline CR fixtures for testing various scenarios (Alloy, OTEL, invalid configs, updates).

## Tasks Completed

### Task 1: Create mock Fleet Management API server and container image

**Status:** Complete
**Commit:** bbd89f1

Created test/mockapi/main.go implementing:
- POST /pipeline.v1.PipelineService/UpsertPipeline endpoint with full request/response contract
- POST /pipeline.v1.PipelineService/DeletePipeline endpoint with idempotent delete behavior
- GET /healthz endpoint for readiness probes
- In-memory pipeline storage using sync.Map
- Atomic ID counter starting at 1000
- Basic auth validation (401 on missing credentials)
- Upsert semantics (reuse ID when pipeline name already exists)
- RFC3339 timestamps for createdAt and updatedAt

Created test/mockapi/Dockerfile:
- Multi-stage build with golang:1.25 and gcr.io/distroless/static:nonroot
- CGO_ENABLED=0 for static binary
- Runs as non-root user 65532
- Exposes port 8080

**Verification:**
```bash
cd test/mockapi && go build -o /tmp/mockapi .
/tmp/mockapi &
curl -s -u test:test -X POST http://localhost:8080/pipeline.v1.PipelineService/UpsertPipeline \
  -d '{"pipeline":{"name":"test","contents":"test config","enabled":true}}'
# Returns: {"name":"test","contents":"test config","enabled":true,"id":"1001","createdAt":"...","updatedAt":"..."}
```

**Key Implementation Details:**
- Uses only standard library (net/http, encoding/json, sync, sync/atomic, time)
- No external dependencies for simplicity
- Logs all requests to stdout for debugging E2E tests
- Handles basic auth validation before routing
- Stores pipelines keyed by generated ID, lookups by name for upsert detection

### Task 2: Create K8s manifests for mock API and E2E test fixture YAMLs

**Status:** Complete
**Commit:** 2d970c0

Created K8s manifests for mock API deployment:

1. **test/mockapi/manifests/deployment.yaml**
   - Single replica Deployment named "mock-fleet-api"
   - Image: mock-fleet-api:test with imagePullPolicy: Never (loaded into Kind)
   - Readiness probe on /healthz
   - Restricted pod security: runAsNonRoot, readOnlyRootFilesystem, drop ALL capabilities
   - seccompProfile: RuntimeDefault
   - runAsUser: 65532

2. **test/mockapi/manifests/service.yaml**
   - ClusterIP Service named "mock-fleet-api"
   - Port 80 -> targetPort 8080
   - Selector: app=mock-fleet-api

3. **test/mockapi/manifests/secret.yaml**
   - Secret named "fleet-management-credentials" (matches config/manager/manager.yaml)
   - base-url: http://mock-fleet-api.fm-crd-system.svc.cluster.local/pipeline.v1.PipelineService/
   - username: test-user, password: test-password

Created E2E test fixtures:

4. **test/e2e/fixtures/valid-alloy-pipeline.yaml**
   - Pipeline CR named "test-alloy-pipeline"
   - ConfigType: Alloy
   - Contents: prometheus.exporter.self "alloy" { }
   - Matchers: ["collector.os=linux"]

5. **test/e2e/fixtures/valid-otel-pipeline.yaml**
   - Pipeline CR named "test-otel-pipeline"
   - ConfigType: OpenTelemetryCollector
   - Contents: Valid OTEL YAML with receivers/processors/exporters/service sections
   - Matchers: ["collector.type=otel"]

6. **test/e2e/fixtures/invalid-mismatch-pipeline.yaml**
   - Pipeline CR with configType: OpenTelemetryCollector but Alloy syntax contents
   - Should be rejected by admission webhook validation

7. **test/e2e/fixtures/update-alloy-pipeline.yaml**
   - Same name as valid-alloy-pipeline ("test-alloy-pipeline")
   - Updated contents with additional scrape and remote_write blocks
   - Updated matchers: ["collector.os=linux", "environment=staging"]
   - Tests update lifecycle path

**Verification:**
```bash
# K8s manifests valid
kubectl apply --dry-run=client -f test/mockapi/manifests/
# All 3 manifests validated successfully

# Secret name matches controller expectations
grep "fleet-management-credentials" test/mockapi/manifests/secret.yaml
grep "fleet-management-credentials" config/manager/manager.yaml
# Both found - names match

# Fixtures have correct structure
for f in test/e2e/fixtures/*.yaml; do
  grep "kind: Pipeline" "$f" && grep "apiVersion: fleetmanagement.grafana.com/v1alpha1" "$f"
done
# All 4 fixtures have correct apiVersion and kind
```

## Deviations from Plan

None - plan executed exactly as written.

## Implementation Notes

**Mock API Design Decisions:**

1. **In-memory storage:** Used sync.Map for thread-safe pipeline storage without external dependencies. Sufficient for E2E tests where the mock API lifecycle is bounded by test execution.

2. **ID generation:** Started at 1000 to distinguish mock IDs from real API IDs (which typically start low). Used atomic counter for thread-safe increment.

3. **Upsert semantics:** Implemented proper upsert by scanning existing pipelines for matching names. Reuses ID when found, generates new ID otherwise. Preserves createdAt timestamp on updates.

4. **Authentication:** Accepts any username/password combination as long as basic auth header is present. Simplifies E2E test setup without requiring credential management.

5. **Standalone binary:** Created minimal go.mod (just "module mockapi" and "go 1.25.0") with no imports from the main project. Makes the mock API independently buildable and deployable.

**Test Fixture Coverage:**

The four fixtures cover key E2E test scenarios:
- **valid-alloy-pipeline.yaml:** Basic Alloy pipeline creation and reconciliation
- **valid-otel-pipeline.yaml:** OTEL pipeline creation, verifies configType handling
- **invalid-mismatch-pipeline.yaml:** Webhook validation rejection (wrong configType for contents)
- **update-alloy-pipeline.yaml:** Update lifecycle (same name, different contents/matchers)

**Security Context:**

Mock API deployment follows restricted pod security standards:
- runAsNonRoot: true (enforced at pod and container level)
- runAsUser: 65532 (matches distroless nonroot user)
- readOnlyRootFilesystem: true
- allowPrivilegeEscalation: false
- capabilities: drop ALL
- seccompProfile: RuntimeDefault

## Testing Results

**Manual Testing:**
```bash
# Build succeeds
cd test/mockapi && go build .
# OK

# Mock API handles UpsertPipeline correctly
curl -s -u test:test -X POST http://localhost:8080/pipeline.v1.PipelineService/UpsertPipeline \
  -d '{"pipeline":{"name":"test_pipeline","contents":"test config","enabled":true,"configType":"Alloy","matchers":["collector.os=linux"]}}'
# Returns: {"name":"test_pipeline","contents":"test config","matchers":["collector.os=linux"],"enabled":true,"id":"1001","configType":"Alloy","createdAt":"2026-02-08T23:25:22.008171Z","updatedAt":"2026-02-08T23:25:22.008171Z"}

# Upsert reuses ID for same name
curl -s -u test:test -X POST http://localhost:8080/pipeline.v1.PipelineService/UpsertPipeline \
  -d '{"pipeline":{"name":"test_pipeline","contents":"updated config","enabled":false,"configType":"Alloy"}}'
# Returns: {"name":"test_pipeline","contents":"updated config","enabled":false,"id":"1001","configType":"Alloy","createdAt":"2026-02-08T23:25:22.008171Z","updatedAt":"2026-02-08T23:25:22.015477Z"}
# ID 1001 reused, createdAt preserved, updatedAt changed

# DeletePipeline is idempotent
curl -s -u test:test -X POST http://localhost:8080/pipeline.v1.PipelineService/DeletePipeline \
  -d '{"id":"1001"}'
# Returns: {"status":"ok"}

# Auth validation works
curl -s -X POST http://localhost:8080/pipeline.v1.PipelineService/UpsertPipeline \
  -d '{"pipeline":{"name":"test","contents":"test"}}'
# Returns: {"error":"unauthorized"}

# Health check works
curl -s http://localhost:8080/healthz
# Returns: ok
```

**YAML Validation:**
- All 3 K8s manifests (deployment, service, secret) pass kubectl dry-run validation
- All 4 Pipeline fixtures have correct apiVersion and kind fields
- Secret name "fleet-management-credentials" matches config/manager/manager.yaml references

## Success Criteria

- [x] `cd test/mockapi && go build .` succeeds
- [x] Mock API Dockerfile is ready for `docker build -t mock-fleet-api:test test/mockapi/`
- [x] All YAML files pass syntax validation
- [x] Fixture Pipeline CRs have correct apiVersion and kind
- [x] Mock API returns pipeline responses with IDs, timestamps, and correct field names

## Next Steps

The mock API server and test fixtures are ready for use in E2E tests. Next plan should:

1. Write E2E test suite using these fixtures
2. Add GitHub Actions workflow that:
   - Creates Kind cluster
   - Builds and loads mock-fleet-api:test image
   - Deploys mock API with kubectl apply -f test/mockapi/manifests/
   - Installs CRDs and operator
   - Runs E2E tests against the fixtures

## Self-Check: PASSED

**Files created:**
- [x] test/mockapi/main.go (exists, 248 lines)
- [x] test/mockapi/go.mod (exists)
- [x] test/mockapi/Dockerfile (exists)
- [x] test/mockapi/manifests/deployment.yaml (exists)
- [x] test/mockapi/manifests/service.yaml (exists)
- [x] test/mockapi/manifests/secret.yaml (exists)
- [x] test/e2e/fixtures/valid-alloy-pipeline.yaml (exists)
- [x] test/e2e/fixtures/valid-otel-pipeline.yaml (exists)
- [x] test/e2e/fixtures/invalid-mismatch-pipeline.yaml (exists)
- [x] test/e2e/fixtures/update-alloy-pipeline.yaml (exists)

**Commits exist:**
- [x] bbd89f1: feat(04-01): create mock Fleet Management API server
- [x] 2d970c0: feat(04-01): add K8s manifests for mock API and E2E test fixtures

All artifacts verified present on disk and in git history.
