# Testing Patterns

**Analysis Date:** 2026-02-08

## Test Framework

**Runner:**
- Ginkgo v2 (`github.com/onsi/ginkgo/v2`) - BDD-style test framework
- Config: Ginkgo is invoked through standard Go testing via `go test`

**Assertion Library:**
- Gomega (`github.com/onsi/gomega`) - assertion/expectation matcher library

**Run Commands:**
```bash
make test                              # Run all unit/integration tests (excludes e2e)
KUBEBUILDER_ASSETS="..." go test ...   # Run tests with envtest setup
make test-e2e                          # Run e2e tests in Kind cluster
make setup-test-e2e                    # Create Kind cluster for e2e
make cleanup-test-e2e                  # Tear down Kind cluster
```

**Coverage:**
- Currently generated: `cover.out` in root directory
- View with: `go tool cover -html=cover.out`
- Requirements: None enforced, but coverage metrics tracked

## Test File Organization

**Location:**
- Co-located with source files in same package
- Naming: `*_test.go` in same directory as `*.go`

**Directory Structure:**
```
internal/controller/
├── pipeline_controller.go
├── pipeline_controller_test.go      # Unit/integration tests
└── suite_test.go                    # Test environment setup

api/v1alpha1/
├── pipeline_webhook.go
├── pipeline_webhook_test.go         # Webhook validation tests
└── pipeline_types.go

test/
├── e2e/                             # E2E tests
│   ├── e2e_suite_test.go            # E2E suite setup
│   └── e2e_test.go                  # E2E test cases
└── utils/                           # Test utilities
    └── utils.go                     # Shared e2e utilities
```

**Naming:**
- Test files: `*_test.go`
- Suite setup: `suite_test.go`
- E2E tests: Located in `test/e2e/` directory

## Test Structure

**Suite Organization (Ginkgo Pattern):**

```go
var _ = Describe("Pipeline Controller", func() {
  Context("When reconciling a Pipeline", func() {
    const (
      pipelineName      = "test-pipeline"
      pipelineNamespace = "default"
      timeout           = time.Second * 10
      interval          = time.Millisecond * 250
    )

    ctx := context.Background()
    typeNamespacedName := types.NamespacedName{
      Name:      pipelineName,
      Namespace: pipelineNamespace,
    }

    AfterEach(func() {
      // Cleanup after each test
    })

    It("should successfully reconcile a new Pipeline", func() {
      By("Creating a new Pipeline")
      // Test setup

      By("Checking if finalizer is added")
      // Assertions
    })
  })
})
```

**Patterns:**
- Use `Describe` blocks to group related tests
- Use `Context` for test scenarios
- Use `It` for individual test cases
- Use `By` for narrative documentation within tests
- Use `BeforeSuite`/`AfterSuite` for test environment setup/teardown
- Use `BeforeEach`/`AfterEach` for per-test setup/cleanup
- Use `Eventually` for async assertions with timeout

## Unit Tests

**Scope:** Test single functions in isolation with mocked dependencies

**Example Pattern from `pipeline_controller_test.go`:**
```go
Context("When building UpsertPipelineRequest", func() {
  It("should use metadata.name when spec.name is empty", func() {
    reconciler := &PipelineReconciler{}
    pipeline := &fleetmanagementv1alpha1.Pipeline{
      ObjectMeta: metav1.ObjectMeta{
        Name:      "test-pipeline",
        Namespace: "default",
      },
      Spec: fleetmanagementv1alpha1.PipelineSpec{
        Contents:   "test content",
        Enabled:    true,
        ConfigType: fleetmanagementv1alpha1.ConfigTypeAlloy,
      },
    }

    req := reconciler.buildUpsertRequest(pipeline)
    Expect(req.Pipeline.Name).To(Equal("test-pipeline"))
  })
})
```

## Mocking

**Framework:** Hand-written mocks matching defined interfaces

**Mock Pattern - Fleet Management Client:**

```go
type mockFleetClient struct {
  pipelines         map[string]*fleetclient.Pipeline
  upsertError       error
  deleteError       error
  callCount         int
  lastUpsertRequest *fleetclient.UpsertPipelineRequest
  shouldReturn404   bool
  shouldReturn400   bool
  shouldReturn429   bool
}

func newMockFleetClient() *mockFleetClient {
  return &mockFleetClient{
    pipelines: make(map[string]*fleetclient.Pipeline),
  }
}

func (m *mockFleetClient) UpsertPipeline(ctx context.Context, req *fleetclient.UpsertPipelineRequest) (*fleetclient.Pipeline, error) {
  m.callCount++
  m.lastUpsertRequest = req
  // Implementation supports error injection for testing
  if m.shouldReturn400 {
    return nil, &fleetclient.FleetAPIError{
      StatusCode: http.StatusBadRequest,
      Operation:  "UpsertPipeline",
      Message:    "validation error: invalid configuration",
    }
  }
  // ... additional error scenarios
}
```

**What to Mock:**
- External API clients (`FleetPipelineClient`)
- HTTP responses (simulate 400, 404, 429 responses)
- Long-running operations

**What NOT to Mock:**
- Kubernetes client operations (use fake client in envtest)
- Controller-runtime components (use real manager in suite)
- Business logic - test with real objects where possible

## Integration Tests

**Scope:** Test controller reconciliation logic with real Kubernetes API (via envtest)

**Test Environment Setup (`internal/controller/suite_test.go`):**

```go
var (
  ctx       context.Context
  cancel    context.CancelFunc
  testEnv   *envtest.Environment
  cfg       *rest.Config
  k8sClient client.Client
)

var _ = BeforeSuite(func() {
  logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

  ctx, cancel = context.WithCancel(context.TODO())

  // Add CRD to scheme
  err = fleetmanagementv1alpha1.AddToScheme(scheme.Scheme)
  Expect(err).NotTo(HaveOccurred())

  // Start test environment with CRDs
  testEnv = &envtest.Environment{
    CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
    ErrorIfCRDPathMissing: true,
  }

  cfg, err = testEnv.Start()
  Expect(err).NotTo(HaveOccurred())

  k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
  Expect(err).NotTo(HaveOccurred())

  // Start controller manager with mock Fleet client
  mgr, err := ctrl.NewManager(cfg, ctrl.Options{
    Scheme: scheme.Scheme,
  })
  Expect(err).ToNot(HaveOccurred())

  mockFleetClient := newMockFleetClient()

  err = (&PipelineReconciler{
    Client:      mgr.GetClient(),
    Scheme:      mgr.GetScheme(),
    FleetClient: mockFleetClient,
  }).SetupWithManager(mgr)
  Expect(err).ToNot(HaveOccurred())

  // Run manager in background
  go func() {
    defer GinkgoRecover()
    err = mgr.Start(ctx)
    Expect(err).ToNot(HaveOccurred(), "failed to run manager")
  }()
})
```

**Key Patterns:**
- Use `envtest.Environment` to spawn API server and etcd
- CRD paths: `filepath.Join("..", "..", "config", "crd", "bases")`
- Create real Kubernetes client with fake scheme
- Run controller in background goroutine
- Inject mock Fleet Management client into reconciler

## E2E Tests

**Framework:** Ginkgo with Kind cluster

**Scope:** Test full deployment in Kubernetes cluster with real CRDs and controller

**Example Pattern from `test/e2e/e2e_test.go`:**

```go
var _ = Describe("Manager", Ordered, func() {
  var controllerPodName string

  BeforeAll(func() {
    By("creating manager namespace")
    cmd := exec.Command("kubectl", "create", "ns", namespace)
    _, err := utils.Run(cmd)
    Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

    By("labeling namespace with restricted security policy")
    cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
      "pod-security.kubernetes.io/enforce=restricted")
    _, err = utils.Run(cmd)
    Expect(err).NotTo(HaveOccurred())

    By("installing CRDs")
    cmd = exec.Command("make", "install")
    _, err = utils.Run(cmd)
    Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

    By("deploying controller-manager")
    cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", managerImage))
    _, err = utils.Run(cmd)
    Expect(err).NotTo(HaveOccurred(), "Failed to deploy controller-manager")
  })

  AfterAll(func() {
    By("undeploying controller-manager")
    cmd := exec.Command("make", "undeploy")
    _, _ = utils.Run(cmd)

    By("uninstalling CRDs")
    cmd = exec.Command("make", "uninstall")
    _, _ = utils.Run(cmd)

    By("removing manager namespace")
    cmd = exec.Command("kubectl", "delete", "ns", namespace)
    _, _ = utils.Run(cmd)
  })

  AfterEach(func() {
    specReport := CurrentSpecReport()
    if specReport.Failed() {
      // Collect logs for debugging
    }
  })
})
```

**Prerequisites:**
- Kind cluster must exist (created by `make setup-test-e2e`)
- Docker image built and loaded into Kind cluster
- CRDs and RBAC properly deployed

## Webhook Validation Tests

**Scope:** Test admission webhook validation logic via `ValidateCreate`, `ValidateUpdate`, `ValidateDelete`

**Test Pattern from `api/v1alpha1/pipeline_webhook_test.go`:**

Uses standard Go `testing` package (not Ginkgo) for simpler unit tests:

```go
func TestPipeline_ValidateCreate(t *testing.T) {
  tests := []struct {
    name     string
    pipeline *Pipeline
    wantErr  bool
    errMsg   string
  }{
    {
      name: "valid Alloy pipeline",
      pipeline: &Pipeline{
        ObjectMeta: metav1.ObjectMeta{
          Name:      "test-pipeline",
          Namespace: "default",
        },
        Spec: PipelineSpec{
          Contents:   "prometheus.scrape \"default\" { }",
          ConfigType: ConfigTypeAlloy,
          Enabled:    true,
          Matchers:   []string{"collector.os=linux"},
        },
      },
      wantErr: false,
    },
    {
      name: "configType mismatch - Alloy config marked as OTEL",
      pipeline: &Pipeline{
        // ... setup
      },
      wantErr: true,
      errMsg:  "configType is 'Alloy' but contents appear to be OpenTelemetry",
    },
  }

  for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
      ctx := context.Background()
      _, err := tt.pipeline.ValidateCreate(ctx, tt.pipeline)
      if (err != nil) != tt.wantErr {
        t.Errorf("ValidateCreate() error = %v, wantErr %v", err, tt.wantErr)
        return
      }
      if tt.wantErr && err != nil {
        if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
          t.Errorf("ValidateCreate() error = %v, should contain %v", err, tt.errMsg)
        }
      }
    })
  }
}
```

**Table-Driven Pattern:**
- Define `[]struct` with test cases including name, input, expected error, error message
- Use `t.Run()` for individual test cases
- Check both error occurrence and error message content

## Async Testing

**Pattern with `Eventually` and timeout:**

```go
Eventually(func() string {
  err := k8sClient.Get(ctx, typeNamespacedName, pipeline)
  if err != nil {
    return ""
  }
  return pipeline.Status.ID
}, timeout, interval).Should(Equal("mock-id-123"))
```

**Timeout/Interval Constants:**
```go
const (
  timeout  = time.Second * 10
  interval = time.Millisecond * 250
)
```

## Error Testing

**Testing Expected Errors:**

```go
It("should handle validation errors from Fleet Management API", func() {
  mock := newMockFleetClient()
  mock.shouldReturn400 = true

  _, err := mock.UpsertPipeline(ctx, req)
  Expect(err).To(HaveOccurred())
  Expect(err.(*fleetclient.FleetAPIError).StatusCode).To(Equal(http.StatusBadRequest))
})
```

## Common Test Utilities

**From `test/utils/utils.go`:**

- `Run(cmd *exec.Cmd)` - Execute command with proper directory setup
- `LoadImageToKindClusterWithName(name string)` - Load Docker image into Kind
- `GetProjectDir()` - Get project root for relative paths
- `InstallCertManager()` / `UninstallCertManager()` - Manage cert-manager
- `IsCertManagerCRDsInstalled()` - Check cert-manager installation
- `UncommentCode()` - Enable commented code sections in tests

## Safe Event Emission in Tests

**Critical Pattern:**
- Controller must safely handle nil `Recorder` (used in unit tests)
- Always use safe emission helper:
  ```go
  func (r *PipelineReconciler) emitEvent(object runtime.Object, eventtype, reason, message string) {
    if r.Recorder != nil {
      r.Recorder.Event(object, eventtype, reason, message)
    }
  }
  ```
- This prevents nil pointer dereference in tests that don't set up event recorder

---

*Testing analysis: 2026-02-08*
