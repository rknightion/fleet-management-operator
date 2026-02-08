# Phase 4: e2e for github actions - Research

**Researched:** 2026-02-09
**Domain:** Kubernetes operator E2E testing in GitHub Actions CI/CD
**Confidence:** HIGH

## Summary

This phase involves implementing end-to-end testing for a Kubernetes operator in GitHub Actions. The project already has scaffolded E2E tests using Ginkgo/Gomega that run against a Kind cluster, but they only test infrastructure (controller pod, metrics endpoint). The tests need to be extended with operator-specific scenarios and integrated into the GitHub Actions CI/CD pipeline.

The standard approach is to use Kind (Kubernetes in Docker) to spin up an ephemeral cluster, deploy the operator with test credentials, create Pipeline CRs, and verify the full reconciliation lifecycle. The key challenge is handling external API dependencies (Fleet Management API) - either by mocking the API or using test credentials in a secure manner.

**Primary recommendation:** Extend existing E2E tests with Fleet Management operator scenarios, integrate with GitHub Actions using helm/kind-action, and implement a mock Fleet Management API server for CI testing to avoid external dependencies and rate limits.

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Ginkgo | v2.27.2 | BDD test framework | De facto standard for Kubernetes E2E testing, rich reporting, parallel execution |
| Gomega | v1.38.2 | Matcher library | Pairs with Ginkgo, provides Eventually/Consistently for async assertions |
| Kind | latest (v0.25.x) | Local Kubernetes clusters | Designed for CI, fast startup, multi-node support, official Kubernetes project |
| controller-runtime | v0.23.0 | Testing utilities | Provides envtest, fake clients, test environment setup |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| helm/kind-action | v1.x | GitHub Action for Kind | Simplifies Kind cluster setup in CI, handles cleanup |
| httptest | stdlib | HTTP mock server | Mock external APIs (Fleet Management), no external dependencies |
| testify/assert | v1.11.1 | Assertion helpers | Already in project, useful for table-driven tests |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Kind | k3d | k3d is faster (~30s vs 1min) but Kind is more widely used and officially supported |
| Kind | minikube | Minikube is heavier, requires VM or Docker, slower startup for CI |
| Mock API | Real API with test credentials | Real API avoids mock drift but hits rate limits (3 req/s), costs money, requires secrets |
| Ginkgo | Standard go test | Ginkgo provides better reporting, parallel execution, BDD structure for complex E2E flows |

**Installation:**
```bash
# Already in project (see go.mod)
# Ginkgo v2.27.2
# Gomega v1.38.2

# GitHub Actions setup (no local install needed)
# Kind is handled by helm/kind-action
```

## Architecture Patterns

### Recommended Project Structure
```
test/
├── e2e/
│   ├── e2e_suite_test.go      # Test suite setup (existing)
│   ├── e2e_test.go             # Infrastructure tests (existing)
│   ├── pipeline_test.go        # NEW: Pipeline CR lifecycle tests
│   ├── webhook_test.go         # NEW: Webhook validation tests
│   └── fixtures/
│       ├── valid-alloy.yaml
│       ├── valid-otel.yaml
│       └── invalid-*.yaml
├── utils/
│   ├── utils.go                # Existing helper functions
│   └── mock_fleet_api.go       # NEW: Mock Fleet Management API
└── integration/                # Future: Integration tests with real API
```

### Pattern 1: E2E Test Suite with Mock API
**What:** Run E2E tests against a real Kubernetes cluster (Kind) with a mocked Fleet Management API server
**When to use:** CI/CD pipelines, local development, fast feedback loops

**Example:**
```go
// Source: Standard Kubernetes operator E2E pattern
var _ = BeforeSuite(func() {
    By("building the manager image")
    cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", managerImage))
    _, err := utils.Run(cmd)
    Expect(err).NotTo(HaveOccurred())

    By("starting mock Fleet Management API")
    mockAPI = utils.StartMockFleetAPI()

    By("loading the manager image on Kind")
    err = utils.LoadImageToKindClusterWithName(managerImage)
    Expect(err).NotTo(HaveOccurred())

    By("deploying the controller with mock API URL")
    // Override API URL to point to mock server
})
```

### Pattern 2: Pipeline Lifecycle Testing
**What:** Test full CRUD operations on Pipeline CRs with status verification
**When to use:** Core E2E tests, verify controller reconciliation

**Example:**
```go
// Source: Operator SDK testing patterns
It("should create, update, and delete a Pipeline", func() {
    By("creating a Pipeline CR")
    pipeline := &v1alpha1.Pipeline{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "test-pipeline",
            Namespace: namespace,
        },
        Spec: v1alpha1.PipelineSpec{
            Contents: alloyConfig,
            ConfigType: "Alloy",
            Enabled: true,
        },
    }

    cmd := exec.Command("kubectl", "apply", "-f", "fixtures/valid-alloy.yaml", "-n", namespace)
    _, err := utils.Run(cmd)
    Expect(err).NotTo(HaveOccurred())

    By("waiting for Pipeline to be Ready")
    Eventually(func(g Gomega) {
        cmd := exec.Command("kubectl", "get", "pipeline", "test-pipeline",
            "-n", namespace, "-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
        output, err := utils.Run(cmd)
        g.Expect(err).NotTo(HaveOccurred())
        g.Expect(output).To(Equal("True"))
    }, 2*time.Minute, 5*time.Second).Should(Succeed())

    By("verifying observedGeneration is updated")
    // Check status.observedGeneration matches metadata.generation
})
```

### Pattern 3: GitHub Actions Workflow
**What:** CI workflow that sets up Kind, runs E2E tests, collects artifacts
**When to use:** Pull requests, main branch commits

**Example:**
```yaml
# Source: Standard Kubernetes operator CI pattern
name: E2E Tests

on:
  pull_request:
  push:
    branches: [main]

jobs:
  e2e:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: Create Kind cluster
        uses: helm/kind-action@v1
        with:
          cluster_name: e2e-test
          wait: 5m

      - name: Run E2E tests
        run: make test-e2e
        env:
          KIND_CLUSTER: e2e-test

      - name: Collect logs on failure
        if: failure()
        run: |
          kubectl logs -n fm-crd-system -l control-plane=controller-manager > controller.log
          kind export logs logs/

      - uses: actions/upload-artifact@v4
        if: failure()
        with:
          name: e2e-logs
          path: |
            controller.log
            logs/
```

### Pattern 4: Mock Fleet Management API
**What:** HTTP server that mimics Fleet Management API for testing
**When to use:** E2E tests in CI, avoid rate limits and external dependencies

**Example:**
```go
// Source: Standard Go httptest pattern
func StartMockFleetAPI() *httptest.Server {
    pipelines := make(map[string]*Pipeline)

    mux := http.NewServeMux()

    // UpsertPipeline
    mux.HandleFunc("/UpsertPipeline", func(w http.ResponseWriter, r *http.Request) {
        // Parse request, validate, store in memory
        // Return full pipeline object with ID
    })

    // DeletePipeline
    mux.HandleFunc("/DeletePipeline", func(w http.ResponseWriter, r *http.Request) {
        // Handle 404 gracefully (idempotent delete)
    })

    // Rate limiting simulation (3 req/s)
    limiter := rate.NewLimiter(rate.Limit(3), 1)

    return httptest.NewServer(rateLimitMiddleware(limiter, mux))
}
```

### Anti-Patterns to Avoid
- **Don't use real Fleet Management API in CI:** Hits rate limits, costs money, requires secrets, flaky due to external dependency
- **Don't skip cleanup in AfterEach:** Leaves test pollution, causes intermittent failures
- **Don't hardcode timeouts without context:** CI is slower than local, use Eventually with appropriate polling intervals
- **Don't test only happy path:** Test validation errors, external deletions, rate limit handling
- **Don't commit secrets:** Use GitHub Actions secrets for any optional integration tests with real API

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Kind cluster setup in GitHub Actions | Custom docker/kubectl commands | helm/kind-action | Handles cleanup, version management, pre-warming, image loading |
| Async status checking | Sleep loops with retries | Gomega Eventually/Consistently | Built for async assertions, automatic polling, better error messages |
| Test fixtures management | Hardcoded strings in tests | YAML files in test/e2e/fixtures/ | Easier to maintain, reuse in kubectl commands, version control |
| Log collection on failure | Custom shell scripts | AfterEach hooks + GitHub Actions artifacts | Ginkgo knows which test failed, automatic upload on failure |
| HTTP mocking | Custom TCP server | httptest.Server | Automatic port allocation, proper shutdown, TLS support |
| Rate limiting | Sleep-based throttling | golang.org/x/time/rate | Correct token bucket algorithm, burst support, context-aware |

**Key insight:** E2E testing infrastructure is complex with subtle gotchas (timing, cleanup, parallelization). Use battle-tested libraries and patterns from the Kubernetes community rather than custom solutions.

## Common Pitfalls

### Pitfall 1: Timeout Misconfiguration
**What goes wrong:** Tests fail intermittently in CI but pass locally due to insufficient timeouts
**Why it happens:** CI runners are slower and more variable than local environments, image pulls take time
**How to avoid:**
- Use `SetDefaultEventuallyTimeout(2 * time.Minute)` in suite setup
- Set polling intervals: `SetDefaultEventuallyPollingInterval(5 * time.Second)`
- Use `--poll-progress-after=30s` Ginkgo flag to debug hanging tests
**Warning signs:** Tests fail with "timeout waiting for..." only in CI, not locally

### Pitfall 2: Test Pollution and Cleanup Failures
**What goes wrong:** Tests leave resources behind, causing subsequent test failures
**Why it happens:** AfterEach/AfterAll don't run on panic, resources not properly cleaned up
**How to avoid:**
- Use `DeferCleanup()` instead of manual cleanup in AfterEach
- Use unique namespaces per test: `fmt.Sprintf("test-%s", GinkgoRandomSeed())`
- Call `defer GinkgoRecover()` in goroutines
- Handle 404 gracefully in cleanup (resource already deleted = success)
**Warning signs:** Tests pass individually but fail when run together, "already exists" errors

### Pitfall 3: Race Conditions with Finalizers
**What goes wrong:** Pipeline deletion hangs or fails due to finalizer not being removed
**Why it happens:** Controller doesn't handle 404 when calling DeletePipeline (pipeline already deleted externally)
**How to avoid:**
- Test must verify finalizer is removed within timeout
- Controller must treat 404 as success during finalizer handling
- Use `--force --grace-period=0` in cleanup scripts
**Warning signs:** Resources stuck in "Terminating" state, test timeout on deletion

### Pitfall 4: Mock API Drift
**What goes wrong:** Tests pass with mock API but fail with real API due to behavior differences
**Why it happens:** Mock doesn't accurately reflect actual API semantics (error codes, validation, rate limiting)
**How to avoid:**
- Document mock behavior based on real API observation
- Add integration test job (optional, with secrets) that uses real API
- Keep mock simple - only implement paths actually used by controller
- Review Fleet Management API changes when upgrading
**Warning signs:** E2E tests pass but manual testing fails, production issues not caught by tests

### Pitfall 5: Secret Management in CI
**What goes wrong:** Tests can't run without hardcoded credentials, or secrets leak in logs
**Why it happens:** Controller needs Fleet Management URL/credentials to start
**How to avoid:**
- Use mock API by default (no secrets needed)
- Add optional integration test job that only runs when secrets are available
- Use GitHub Actions secrets, never commit credentials
- Override base URL via environment variable in E2E tests
**Warning signs:** CI requires repository secrets to run, secret values in workflow logs

### Pitfall 6: Image Build and Load Timing
**What goes wrong:** Test fails with "ImagePullBackOff" or uses stale image
**Why it happens:** Image not loaded into Kind cluster before deployment, caching issues
**How to avoid:**
- Always build image fresh in E2E workflow: `make docker-build`
- Load image into Kind: `kind load docker-image` or use utils helper
- Set imagePullPolicy: Never in test manifests
- Verify image tag matches what was built
**Warning signs:** Tests use old operator version, "ErrImagePull" in pod events

## Code Examples

Verified patterns from official sources:

### E2E Test Structure (Ginkgo/Gomega)
```go
// Source: Kubebuilder E2E scaffolding pattern
Context("Pipeline Lifecycle", func() {
    It("should create and reconcile an Alloy pipeline", func() {
        By("applying a valid Alloy Pipeline")
        cmd := exec.Command("kubectl", "apply", "-f",
            "test/e2e/fixtures/valid-alloy.yaml", "-n", namespace)
        _, err := utils.Run(cmd)
        Expect(err).NotTo(HaveOccurred())

        By("waiting for pipeline to have an ID in status")
        Eventually(func(g Gomega) {
            cmd := exec.Command("kubectl", "get", "pipeline", "test-alloy",
                "-n", namespace, "-o", "jsonpath={.status.id}")
            output, err := utils.Run(cmd)
            g.Expect(err).NotTo(HaveOccurred())
            g.Expect(output).NotTo(BeEmpty())
        }, 2*time.Minute, 5*time.Second).Should(Succeed())

        By("verifying Ready condition is True")
        Eventually(func(g Gomega) {
            cmd := exec.Command("kubectl", "get", "pipeline", "test-alloy",
                "-n", namespace, "-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
            output, err := utils.Run(cmd)
            g.Expect(err).NotTo(HaveOccurred())
            g.Expect(output).To(Equal("True"))
        }, 2*time.Minute, 5*time.Second).Should(Succeed())
    })
})
```

### Mock API Server Setup
```go
// Source: Standard Go httptest pattern for operator testing
func StartMockFleetAPI() (*httptest.Server, string) {
    pipelines := sync.Map{} // Thread-safe for parallel tests
    nextID := int64(1000)

    mux := http.NewServeMux()

    mux.HandleFunc("/UpsertPipeline", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost {
            http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
            return
        }

        var req struct {
            Pipeline struct {
                Name     string   `json:"name"`
                Contents string   `json:"contents"`
                Matchers []string `json:"matchers"`
            } `json:"pipeline"`
        }

        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            http.Error(w, err.Error(), http.StatusBadRequest)
            return
        }

        // Generate ID if new, reuse if exists
        id := atomic.AddInt64(&nextID, 1)
        idStr := fmt.Sprintf("%d", id)

        pipelines.Store(req.Pipeline.Name, idStr)

        resp := map[string]interface{}{
            "id": idStr,
            "name": req.Pipeline.Name,
            "contents": req.Pipeline.Contents,
        }

        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(resp)
    })

    mux.HandleFunc("/DeletePipeline", func(w http.ResponseWriter, r *http.Request) {
        var req struct {
            ID string `json:"id"`
        }
        json.NewDecoder(r.Body).Decode(&req)

        // Always return success (idempotent delete, even for non-existent)
        w.WriteHeader(http.StatusOK)
        json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
    })

    server := httptest.NewServer(mux)
    return server, server.URL
}
```

### GitHub Actions E2E Workflow
```yaml
# Source: Standard Kubernetes operator E2E pattern with Kind
name: E2E Tests

on:
  pull_request:
    branches: [main]
  push:
    branches: [main]

jobs:
  e2e:
    name: End-to-End Tests
    runs-on: ubuntu-latest

    steps:
      - name: Checkout code
        uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: Create Kind cluster
        uses: helm/kind-action@v1
        with:
          cluster_name: fm-crd-e2e
          wait: 5m
          kubectl_version: v1.35.0

      - name: Verify cluster
        run: |
          kubectl cluster-info
          kubectl get nodes

      - name: Run E2E tests
        run: make test-e2e
        env:
          KIND_CLUSTER: fm-crd-e2e

      - name: Collect controller logs on failure
        if: failure()
        run: |
          mkdir -p logs
          kubectl logs -n fm-crd-system -l control-plane=controller-manager --tail=-1 > logs/controller.log || true
          kubectl describe pods -n fm-crd-system > logs/pods.txt || true
          kubectl get events -n fm-crd-system --sort-by=.lastTimestamp > logs/events.txt || true

      - name: Export Kind logs on failure
        if: failure()
        run: |
          kind export logs logs/kind-export --name fm-crd-e2e || true

      - name: Upload failure artifacts
        if: failure()
        uses: actions/upload-artifact@v4
        with:
          name: e2e-failure-logs
          path: logs/
          retention-days: 7
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Ginkgo v1 | Ginkgo v2 | 2022 | Breaking changes in imports, improved reporting, JSON output, spec priority |
| Manual kubectl in tests | controller-runtime client in Go | Ongoing | More idiomatic, richer object handling, but adds complexity |
| Kubebuilder e2e scaffolding | Custom E2E per operator | Per project | Scaffolding provides infrastructure tests only, operators must add domain tests |
| Blocking API calls in tests | Eventually/Consistently with polling | Always | Handles async nature of Kubernetes reconciliation |
| Kind v0.11.x | Kind v0.20+ | 2023-2024 | Faster cluster creation, better cgroup v2 support, multi-arch images |
| Basic error messages | Progress reporting with --poll-progress-after | Ginkgo v2.5+ (2023) | Better debugging of hanging tests |

**Deprecated/outdated:**
- **Ginkgo v1:** Deprecated, use v2 (different import paths: github.com/onsi/ginkgo/v2)
- **envtest for E2E:** Still common but real cluster (Kind) is better for true E2E, envtest for integration
- **Kubebuilder's "make test-e2e" with CertManager only:** Scaffolded tests don't include operator-specific scenarios (noted by TODO comments in e2e_test.go)

## Open Questions

1. **Should we test with real Fleet Management API?**
   - What we know: Mock API avoids rate limits, secrets, external dependency
   - What's unclear: Whether mock API will catch all real API edge cases
   - Recommendation: Use mock API for standard E2E tests, add optional integration test job (runs on main branch only) with real API using GitHub Actions secrets

2. **How to handle webhook testing in E2E?**
   - What we know: Project has admission webhook, requires CertManager, scaffolded tests install CertManager
   - What's unclear: Whether webhook tests should be separate job or same E2E workflow
   - Recommendation: Keep in same E2E workflow, CertManager is already set up by scaffolded tests, test invalid Pipeline CRs that should be rejected

3. **Should E2E tests run on every PR or only main?**
   - What we know: E2E tests are slower (~5-10min with image build), more flaky than unit tests
   - What's unclear: Balance between fast feedback and CI cost
   - Recommendation: Run on all PRs to catch issues early, optimize with caching (Go modules, Docker layers), consider running only if operator code changed (not docs-only PRs)

4. **How to test finalizer and deletion scenarios?**
   - What we know: Finalizers are critical, deletion must handle 404 gracefully
   - What's unclear: Best way to simulate external deletion (pipeline deleted outside of Kubernetes)
   - Recommendation: Create test that directly calls mock API to delete pipeline, then deletes CR, verifies finalizer is removed and CR deletion completes

## Sources

### Primary (HIGH confidence)
- [Testing Kubernetes Operators using GitHub Actions and Kind](https://medium.com/codex/testing-kubernetes-operators-using-github-actions-and-kind-c4086d37dd30) - Kind + GitHub Actions integration pattern
- [helm/kind-action GitHub repository](https://github.com/helm/kind-action) - Official Kind action for GitHub Actions
- [E2E Testing Best Practices, Reloaded | Kubernetes Contributors](https://www.kubernetes.dev/blog/2023/04/12/e2e-testing-best-practices-reloaded/) - Official Kubernetes E2E testing guidelines
- [Writing controller tests | Kubebuilder Book](https://book.kubebuilder.io/cronjob-tutorial/writing-tests) - Official controller-runtime testing guide
- [Testing Kubernetes Operators with Ginkgo, Gomega and the Operator Runtime](https://itnext.io/testing-kubernetes-operators-with-ginkgo-gomega-and-the-operator-runtime-6ad4c2492379) - Operator testing patterns
- Project's existing test files: test/e2e/e2e_test.go, test/e2e/e2e_suite_test.go, test/utils/utils.go

### Secondary (MEDIUM confidence)
- [Testing Kubernetes Controllers with the E2E-Framework](https://medium.com/programming-kubernetes/testing-kubernetes-controllers-with-the-e2e-framework-fac232843dc6) - Alternative E2E framework
- [Best Practices for Testing Kubernetes Operators - WafaTech Blogs](https://wafatech.sa/blog/devops/kubernetes/best-practices-for-testing-kubernetes-operators/) - General best practices
- [Ginkgo v2 Releases](https://github.com/onsi/ginkgo/releases) - Current version features (v2.27.2 as of project go.mod)
- [Using secrets in GitHub Actions](https://docs.github.com/actions/security-guides/using-secrets-in-github-actions) - GitHub Actions secrets management
- [How to Optimize GitHub Actions Performance](https://oneuptime.com/blog/post/2026-02-02-github-actions-performance-optimization/view) - CI optimization patterns

### Tertiary (LOW confidence, needs validation)
- [k3d + GitHub Actions](https://www.arrikto.com/uncategorized/k3d-github-actions-kubernetes-e2e-testing-made-easy/) - Alternative to Kind (k3d is faster but less standard)
- [External Secrets Operator for GitHub Actions](https://external-secrets.io/latest/provider/github/) - Secret management pattern (may be overkill for this use case)

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - Ginkgo/Gomega/Kind are verified in project's go.mod and existing E2E tests, widely documented
- Architecture: HIGH - Patterns verified from official Kubebuilder docs and existing scaffolded tests in project
- Pitfalls: MEDIUM-HIGH - Based on general Kubernetes E2E testing experience and documentation, not Fleet Management specific
- Mock API pattern: MEDIUM - Standard Go testing pattern, but Fleet Management API specifics need validation

**Research date:** 2026-02-09
**Valid until:** 2026-04-09 (60 days) - E2E testing infrastructure is relatively stable, but check for Ginkgo/Kind updates
