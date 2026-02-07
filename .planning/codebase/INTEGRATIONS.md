# External Integrations

**Analysis Date:** 2026-02-08

## APIs & External Services

**Grafana Fleet Management API:**
- Primary service for managing pipelines across collectors
  - Base URL: `https://fleet-management-<CLUSTER_NAME>.grafana.net/pipeline.v1.PipelineService/`
  - Endpoints: UpsertPipeline, DeletePipeline
  - SDK/Client: Custom HTTP client in `pkg/fleetclient/client.go`
  - Auth: HTTP Basic Auth (username=Stack ID, password=Cloud Access Token)
  - Rate Limit: 3 requests/second (enforced with golang.org/x/time/rate)
  - API Response: Returns full Pipeline object with ID, timestamps, configType

**Kubernetes API Server:**
- Native Kubernetes integration via controller-runtime
  - Client: k8s.io/client-go v0.35.0
  - Auth: kubeconfig (supports all auth plugins: Azure, GCP, OIDC via k8s.io/client-go/plugin/pkg/client/auth)
  - Operations: Get, List, Watch, Create, Update, Patch, Delete Pipeline CRDs
  - Events: Emits Kubernetes events for debugging (via record.EventRecorder)

## Data Storage

**Databases:**
- None - Pipelines stored in Grafana Fleet Management API
- No local persistence layer

**File Storage:**
- Local filesystem only - TLS certificates for webhooks (configurable path)

**Caching:**
- Kubernetes informer cache (controller-runtime)
  - Used for efficient resource watch and filtering
  - Cache invalidation: Automatic via informer resyncing

## Authentication & Identity

**Auth Provider:**
- Basic HTTP Auth (username/password credentials)
  - Username: Grafana Stack ID
  - Password: Grafana Cloud Access Token
  - Storage: Kubernetes Secret (`fleet-management-credentials`)
    - Key `base-url`: Fleet Management API base URL
    - Key `username`: Stack ID
    - Key `password`: Cloud access token

**Webhook Authentication:**
- Mutual TLS (mTLS) for webhook server
  - Certificate injection via Kubernetes
  - Recommended: cert-manager automatic certificate provisioning
  - Alternative: Manual certificate provisioning (paths configurable)

## Monitoring & Observability

**Error Tracking:**
- Kubernetes events emitted on errors
  - Event types: Normal (Synced, Created, Updated, Deleted), Warning (SyncFailed, ValidationFailed, RateLimited, Recreated)

**Logs:**
- Structured logging via controller-runtime (zap-based, key-value pairs)
- Log level: Configurable via zap Options in `cmd/main.go`
- Development mode: Enabled by default (verbose output)
- Destinations: stdout (captured by container runtime/kubectl logs)

**Metrics:**
- Prometheus metrics endpoint (configurable):
  - Default: :8443 (HTTPS with TLS)
  - Can be disabled or switched to :8080 (HTTP)
  - Metrics sources: controller-runtime built-in + gRPC observability
- OpenTelemetry instrumentation (optional):
  - go.opentelemetry.io/otel for traces
  - go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp for HTTP spans
  - Exporters: OTLP gRPC (otlptracegrpc)

## CI/CD & Deployment

**Hosting:**
- Kubernetes cluster (self-hosted or cloud)
- Deployment manifest: `config/manager/manager.yaml` (Deployment with 1 replica)
- Namespace: `fleet-management-system` (created by kustomize)

**CI Pipeline:**
- GitHub Actions workflows:
  - `test.yml` - Runs unit tests on push/PR (go test + make test)
  - `ci.yaml` - CI checks
  - `docker.yaml` - Docker image build
  - `release.yaml` - Release automation

**Container Registry:**
- Configurable via `IMG` variable in Makefile
- Default image: `fleet-management-operator:latest`
- Multi-architecture build: linux/amd64, linux/arm64
- Base image: gcr.io/distroless/static:nonroot (non-root user 65532:65532)

## Environment Configuration

**Required env vars (from Kubernetes Secret):**
- `FLEET_MANAGEMENT_BASE_URL` - Fleet Management API endpoint
- `FLEET_MANAGEMENT_USERNAME` - Grafana Stack ID
- `FLEET_MANAGEMENT_PASSWORD` - Grafana Cloud Access Token

**Optional env vars:**
- Controller flags set via command-line args in deployment spec

**Secrets location:**
- Kubernetes Secret: `fleet-management-credentials` (same namespace as operator)
  - Keys: `base-url`, `username`, `password`

## Webhooks & Callbacks

**Incoming:**
- Admission webhook: `/validate-fleetmanagement-grafana-com-v1alpha1-pipeline`
  - Validates Pipeline resource create/update
  - Validation rules:
    - ConfigType must match contents syntax (Alloy vs OpenTelemetryCollector)
    - Matchers must follow Prometheus AlertManager syntax (=, !=, =~, !~)
    - Each matcher must be <= 200 characters
    - Contents must not be empty
  - Failure policy: Fail (rejects invalid resources)
  - Webhook server: controller-runtime built-in (port configurable)

**Outgoing:**
- Events emitted to Kubernetes event stream (viewed via `kubectl describe pipeline` or `kubectl get events`)
- No external HTTP callbacks configured

## HTTP Client Configuration

**Details from `pkg/fleetclient/client.go`:**
- Timeout: 30 seconds per request
- Max idle connections: 100
- Idle connection timeout: 90 seconds
- TLS handshake timeout: 10 seconds
- Request format: JSON (Content-Type: application/json)
- Response body always closed via defer (safe resource handling)

---

*Integration audit: 2026-02-08*
