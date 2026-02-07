# Codebase Structure

**Analysis Date:** 2026-02-08

## Directory Layout

```
/Users/mbaykara/work/fleet-management-operator/
├── api/v1alpha1/              # CRD types, schemas, and webhook validation
├── internal/controller/       # Reconciliation logic and error handling
├── pkg/fleetclient/           # Fleet Management API HTTP client
├── cmd/                       # Manager entry point
├── config/                    # Kubernetes manifests and CRD bases
│   ├── crd/bases/             # Generated CRD manifests
│   ├── manager/               # Deployment and manager config
│   ├── rbac/                  # ServiceAccount, roles, role bindings
│   ├── webhook/               # Webhook service configuration
│   ├── samples/               # Example Pipeline resources
│   └── default/               # Kustomization overlay
├── charts/                    # Helm chart for deployment
├── test/                      # Integration and E2E tests
│   ├── e2e/                   # End-to-end test suites
│   └── utils/                 # Test utilities and helpers
├── docs/                      # Documentation
├── hack/                      # Build and code generation scripts
├── bin/                       # Build artifacts and binaries
└── dist/                      # Distribution binaries
```

## Directory Purposes

**api/v1alpha1/:**
- Purpose: API resource definitions and validation logic
- Contains: CRD types, webhook validation, type conversions
- Key files: `pipeline_types.go` (CRD schema), `pipeline_webhook.go` (validation), `groupversion_info.go` (API group setup)

**internal/controller/:**
- Purpose: Core reconciliation business logic
- Contains: PipelineReconciler implementation, error handling strategies, status management
- Key files: `pipeline_controller.go` (reconciliation), `pipeline_controller_test.go` (unit tests), `suite_test.go` (test setup)

**pkg/fleetclient/:**
- Purpose: External API client library for Fleet Management
- Contains: HTTP client, rate limiting, request/response types
- Key files: `client.go` (API operations), `types.go` (data models)

**cmd/:**
- Purpose: Application entry point
- Contains: main() function, manager initialization, webhook setup
- Key files: `main.go` (bootstrap)

**config/crd/bases/:**
- Purpose: Generated CRD manifest definitions
- Contains: Kubernetes API schema and CustomResourceDefinition manifests
- Key files: `fleetmanagement.grafana.com_pipelines.yaml` (Pipeline CRD definition)

**config/manager/:**
- Purpose: Deployment configuration for controller manager
- Contains: Kubernetes Deployment, namespace, service account binding
- Key files: `manager.yaml` (full deployment spec), `kustomization.yaml` (overlay)

**config/rbac/:**
- Purpose: Kubernetes RBAC configuration
- Contains: ServiceAccount, ClusterRole with Pipeline permissions, RoleBinding, leader election role
- Key files: `role.yaml` (CRD permissions), `service_account.yaml`, `role_binding.yaml`

**config/webhook/:**
- Purpose: Admission webhook service configuration
- Contains: Webhook Service, listener manifests
- Key files: `service.yaml` (webhook endpoint), `kustomization.yaml`

**config/samples/:**
- Purpose: Example Pipeline resources for users
- Contains: Alloy and OpenTelemetry Collector sample configurations
- Key files: `alloy_pipeline_sample.yaml`, `pipeline_otel_sample.yaml`

**config/default/:**
- Purpose: Kustomization overlay combining all components
- Contains: Certificate injection, metrics patches, webhook patches
- Key files: `kustomization.yaml` (overlay definition), patches for webhook/metrics

**test/e2e/:**
- Purpose: End-to-end test suite
- Contains: Full integration tests with envtest
- Key files: `e2e_test.go` (test cases), `e2e_suite_test.go` (ginkgo suite setup)

**test/utils/:**
- Purpose: Test utilities and helper functions
- Contains: Common test setup, client factories, assertion helpers
- Key files: `utils.go` (test helpers)

**charts/fleet-management-operator/:**
- Purpose: Helm chart for deploying operator
- Contains: values.yaml, templates for deployment, service, RBAC
- Subdirectories: `crds/` (Helm CRD hooks), `templates/` (manifest templates)

## Key File Locations

**Entry Points:**
- `cmd/main.go`: Kubernetes operator entry point - initializes manager, webhook, Fleet Management client, starts reconciliation loop

**CRD Types:**
- `api/v1alpha1/pipeline_types.go`: Pipeline, PipelineSpec, PipelineStatus structs and ConfigType/SourceType enums

**Reconciliation Logic:**
- `internal/controller/pipeline_controller.go`: PipelineReconciler.Reconcile, reconcileNormal, reconcileDelete, buildUpsertRequest, handleAPIError

**Webhook Validation:**
- `api/v1alpha1/pipeline_webhook.go`: ValidateCreate, ValidateUpdate, validatePipeline, validateConfigType, validateMatchers

**Fleet Management Client:**
- `pkg/fleetclient/client.go`: UpsertPipeline, DeletePipeline with rate limiting
- `pkg/fleetclient/types.go`: Pipeline, Source, UpsertPipelineRequest, FleetAPIError types

**Tests:**
- `internal/controller/pipeline_controller_test.go`: Reconciliation unit tests, mock Fleet client
- `api/v1alpha1/pipeline_webhook_test.go`: Webhook validation tests
- `test/e2e/e2e_test.go`: Integration and E2E tests

**Configuration:**
- `config/manager/manager.yaml`: Deployment with environment variable injection from Secret
- `config/rbac/role.yaml`: RBAC for Pipeline resource management
- `config/crd/bases/fleetmanagement.grafana.com_pipelines.yaml`: CRD manifest

## Naming Conventions

**Files:**
- `*_types.go`: CRD type definitions and schemas
- `*_webhook.go`: Admission webhook implementations
- `*_test.go`: Unit tests for corresponding file
- `*_controller.go`: Reconciler implementations
- `suite_test.go`: Ginkgo test suite setup
- Package names match directory names (e.g., `package v1alpha1` in `api/v1alpha1/`)

**Directories:**
- `api/vX/`: API version directories (currently v1alpha1)
- `internal/`: Private packages not for external import
- `pkg/`: Public packages for external use
- `config/`: Kubernetes manifests (not Go code)
- `test/`: All test code

**Go Functions:**
- Reconcile methods: `PipelineReconciler.Reconcile()`
- Webhook validators: `ValidateCreate()`, `ValidateUpdate()`, `ValidateDelete()`
- Helpers: camelCase starting lowercase (e.g., `buildUpsertRequest()`, `reconcileNormal()`)
- Public APIs: PascalCase (e.g., `PipelineReconciler`, `Pipeline`, `UpsertPipeline`)
- Constants: UPPER_SNAKE_CASE (e.g., `pipelineFinalizer`, `conditionTypeReady`)

**Kubernetes Resources:**
- Kind: PascalCase (Pipeline)
- API Group: lowercase with dots (fleetmanagement.grafana.com)
- Version: v1alpha1 (lowercase v + number + alpha/beta suffix)
- Names in manifests: lowercase with hyphens (e.g., alloy-pipeline-sample)

## Where to Add New Code

**New Controller Feature (handling new Pipeline field):**
- Modify Spec struct: `api/v1alpha1/pipeline_types.go`
- Add webhook validation: `api/v1alpha1/pipeline_webhook.go:validatePipeline()`
- Update reconciler: `internal/controller/pipeline_controller.go:buildUpsertRequest()`
- Update client request: `pkg/fleetclient/types.go:UpsertPipelineRequest` if needed
- Add test: `internal/controller/pipeline_controller_test.go`

**New Validation Rule:**
- Add validation: `api/v1alpha1/pipeline_webhook.go` (new validate* function)
- Call from: `pipeline_webhook.go:validatePipeline()`
- Test: `api/v1alpha1/pipeline_webhook_test.go`

**New API Error Handling:**
- Handle in: `internal/controller/pipeline_controller.go:handleAPIError()`
- Define event reason constant in: `internal/controller/pipeline_controller.go` (top of file)
- Emit event: `r.emitEventf(pipeline, corev1.EventTypeWarning, eventReason...)`

**New Fleet Management API Operation:**
- Add method to Client: `pkg/fleetclient/client.go`
- Add/update types: `pkg/fleetclient/types.go`
- Add interface method to FleetPipelineClient: `internal/controller/pipeline_controller.go:FleetPipelineClient`
- Call from reconciler: `internal/controller/pipeline_controller.go:Reconcile()`

**Test Utilities:**
- Add helpers: `test/utils/utils.go`
- Use in controller tests: `internal/controller/pipeline_controller_test.go`
- Use in E2E tests: `test/e2e/e2e_test.go`

## Special Directories

**config/samples/:**
- Purpose: Example Pipeline resources (Alloy and OTEL syntax)
- Generated: No, manually maintained
- Committed: Yes

**bin/k8s/:**
- Purpose: Downloaded Kubernetes binaries for envtest
- Generated: Yes (by `make test`)
- Committed: No (gitignored)

**dist/:**
- Purpose: Compiled binary distributions
- Generated: Yes (by `make docker-build`)
- Committed: No

**config/default/:**
- Purpose: Kustomization overlay applying all patches
- Generated: No, manually maintained
- Committed: Yes
- Used: By `make deploy` and helm chart generation

**hack/:**
- Purpose: Code generation and build scripts
- Generated: No, manually maintained
- Committed: Yes
- Files: Used by `make generate` and `make manifests`

---

*Structure analysis: 2026-02-08*
