# Architecture

**Analysis Date:** 2026-02-08

## Pattern Overview

**Overall:** Kubernetes Operator using controller-runtime with reconciliation pattern. Bridges Kubernetes Pipeline CRDs with Grafana Cloud Fleet Management API.

**Key Characteristics:**
- Single Custom Resource type: Pipeline (CRD)
- Reconciliation-driven: watches Pipeline resources and syncs state to Fleet Management API
- Admission webhook validates Pipeline specs before creation/update
- Rate-limited API client (3 req/s) with idempotent UpsertPipeline operations
- Finalizer-based deletion handling to clean up external resources
- Status conditions track synchronization state
- Event recording for observability

## Layers

**API Layer (`api/v1alpha1/`):**
- Purpose: Define the Pipeline CRD schema and conversions between Kubernetes and Fleet Management API formats
- Location: `api/v1alpha1/`
- Contains: CRD type definitions, webhook validation, schema converters
- Depends on: controller-runtime, apimachinery
- Used by: Controller reconciler, webhook server

**Controller Layer (`internal/controller/`):**
- Purpose: Reconcile Pipeline resources with Fleet Management API state
- Location: `internal/controller/`
- Contains: PipelineReconciler that implements reconciliation logic, error handling, status management
- Depends on: API types, Fleet Management client, controller-runtime
- Used by: Controller manager

**Client Layer (`pkg/fleetclient/`):**
- Purpose: HTTP client for Fleet Management Pipeline API with rate limiting
- Location: `pkg/fleetclient/`
- Contains: UpsertPipeline, DeletePipeline operations with basic auth and rate limiting
- Depends on: golang.org/x/time/rate
- Used by: Controller layer

**Manager/Setup (`cmd/main.go`):**
- Purpose: Bootstrap controller, webhook server, and manager
- Location: `cmd/main.go`
- Contains: Manager initialization, webhook registration, Fleet Management client setup
- Depends on: All layers, controller-runtime, zap logging
- Used by: Entry point

## Data Flow

**Create/Update Workflow:**

1. User creates or updates Pipeline CRD in Kubernetes
2. Webhook intercepts (ValidateCreate/ValidateUpdate)
3. Webhook validates: contents not empty, configType matches syntax, matchers are valid Prometheus syntax
4. If validation passes, CRD is stored in etcd
5. Controller watches Pipeline changes, triggers reconciliation
6. Reconciler checks ObservedGeneration (skip if spec unchanged)
7. Reconciler adds finalizer if missing
8. Reconciler calls buildUpsertRequest to convert CRD to Fleet Management format
9. UpsertPipeline called (rate-limited)
10. Fleet Management API responds with complete Pipeline object (including assigned ID)
11. Controller updates status with ID, timestamps, Ready/Synced conditions
12. Controller emits event (Normal/Created or Normal/Updated)

**Delete Workflow:**

1. User deletes Pipeline CRD (or kubectl delete)
2. Kubernetes marks for deletion (sets DeletionTimestamp)
3. Controller detects deletion in reconciliation
4. Controller calls DeletePipeline with stored ID (rate-limited)
5. If 404 returned, treats as already deleted (idempotent)
6. Controller removes finalizer from CRD
7. Kubernetes garbage collector deletes CRD
8. Controller emits event (Normal/Deleted)

**Error Scenarios:**

- **400 Bad Request (Validation Error)**: Status set to ValidationError, no immediate retry
- **404 Not Found (External Delete)**: Clears ID and recreates pipeline
- **429 Too Many Requests (Rate Limited)**: Requeues with 10s delay
- **Other errors**: Returns error for exponential backoff by controller-runtime

**State Management:**
- **Spec Generation**: Used via ObservedGeneration to skip reconcile when spec unchanged
- **External State**: Pipeline ID and timestamps stored in Status, sourced from Fleet Management API
- **Status Conditions**: Ready (synced to API), Synced (last reconciliation succeeded)

## Key Abstractions

**Pipeline CRD (`api/v1alpha1/Pipeline`):**
- Purpose: User-facing Kubernetes resource representing a Fleet Management pipeline configuration
- Examples: `config/samples/alloy_pipeline_sample.yaml`, `config/samples/pipeline_otel_sample.yaml`
- Pattern: Standard Kubernetes resource with Spec (desired state) and Status (observed state)

**PipelineSpec:**
- Contents: Configuration string (Alloy or OpenTelemetry YAML)
- Matchers: Prometheus Alertmanager syntax array for collector targeting
- ConfigType: "Alloy" or "OpenTelemetryCollector" (must match contents syntax)
- Source: Origin tracking (Git, Terraform, Kubernetes)
- Enabled: Boolean to enable/disable pipeline

**PipelineStatus:**
- ID: Server-assigned identifier from Fleet Management API
- ObservedGeneration: Generation of last reconciled spec (idempotency key)
- CreatedAt/UpdatedAt: Timestamps from Fleet Management API
- RevisionID: Current revision from Fleet Management
- Conditions: array of metav1.Condition (Ready, Synced)

**FleetPipelineClient Interface (`internal/controller/`):**
- Purpose: Define contract for Fleet Management API interactions (testability)
- Methods: UpsertPipeline, DeletePipeline
- Implementation: `pkg/fleetclient/Client`

**Config Type Conversion:**
- CRD format: "Alloy" or "OpenTelemetryCollector"
- API format: "CONFIG_TYPE_ALLOY" or "CONFIG_TYPE_OTEL"
- Converters: `ConfigType.ToFleetAPI()`, `ConfigTypeFromFleetAPI()`

**Source Type Conversion:**
- CRD format: "Git", "Terraform", "Kubernetes", "Unspecified"
- API format: "SOURCE_TYPE_*" uppercase
- Default: SourceTypeKubernetes with namespace "namespace/name"

## Entry Points

**Controller Process (`cmd/main.go`):**
- Location: `cmd/main.go`
- Triggers: Kubernetes manager startup (POD_INIT or `make run`)
- Responsibilities:
  - Parse flags for metrics, webhook, health probe endpoints
  - Initialize Kubernetes manager with controller-runtime
  - Load Fleet Management credentials from environment
  - Create Fleet Management API client
  - Register PipelineReconciler with manager
  - Register Pipeline webhook with manager
  - Start health checks and manager event loop

**Reconciliation (`internal/controller/pipeline_controller.go` - Reconcile):**
- Location: `internal/controller/pipeline_controller.go:108`
- Triggers: Pipeline CRD create/update/delete, periodic resync, or explicit requeue
- Responsibilities:
  - Fetch Pipeline resource from Kubernetes
  - Handle deletion (finalizer cleanup)
  - Add finalizer on create
  - Check ObservedGeneration to skip unnecessary reconciles
  - Delegate to reconcileNormal or reconcileDelete

**Webhook Validation (`api/v1alpha1/pipeline_webhook.go`):**
- Location: `api/v1alpha1/pipeline_webhook.go:35-62`
- Triggers: Pipeline CRD create or update HTTP request
- Responsibilities:
  - ValidateCreate: full validation pipeline
  - ValidateUpdate: full validation pipeline
  - ValidateDelete: no-op
  - Check contents not empty, configType matches syntax, matchers valid

## Error Handling

**Strategy:** Structured error returns with Fleet Management API error details. Validation errors marked and not retried. Network/API errors trigger exponential backoff.

**Patterns:**

1. **API Errors Detection** (`internal/controller/pipeline_controller.go:250`):
   - Cast err to FleetAPIError, check StatusCode
   - Different handling per HTTP status code

2. **Graceful Degradation**:
   - 404 on delete = success (already deleted)
   - Validation errors set status but don't requeue
   - Rate limits requeue with explicit delay

3. **Status Propagation**:
   - Errors update Status.Conditions with reason and message
   - Ready condition set to False with error details
   - ObservedGeneration updated to indicate we attempted reconciliation

4. **Event Recording**:
   - Normal events for creation/update/deletion
   - Warning events for failures
   - Prevents nil pointer errors by checking r.Recorder != nil

## Cross-Cutting Concerns

**Logging:** Uses controller-runtime logger (logf.FromContext) with key-value pairs
- Log level V(1) for non-critical info (skipped reconciles)
- Log.Error() for actual failures
- Example: `log.Info("reconciling Pipeline", "namespace", req.Namespace, "name", req.Name)`

**Validation:** Three-layer approach
1. Webhook admission (pre-storage validation)
2. Controller pre-flight checks (contents type match)
3. API validation (returned as 400 Bad Request)

**Authentication:** Basic auth with username/token from environment variables
- FLEET_MANAGEMENT_BASE_URL, FLEET_MANAGEMENT_USERNAME, FLEET_MANAGEMENT_PASSWORD
- Credentials stored in Secret, mounted to Pod environment

**Rate Limiting:** golang.org/x/time/rate with Limiter(3, 1)
- UpsertPipeline and DeletePipeline both rate-limited
- Wait before each API call
- 429 responses trigger requeue with 10s delay

---

*Architecture analysis: 2026-02-08*
