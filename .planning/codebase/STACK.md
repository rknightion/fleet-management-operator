# Technology Stack

**Analysis Date:** 2026-02-08

## Languages

**Primary:**
- Go 1.25.0 - Kubernetes operator implementation

## Runtime

**Environment:**
- Linux (multi-architecture support: amd64, arm64)

**Package Manager:**
- Go Modules
- Lockfile: `go.sum` (present)

## Frameworks

**Core:**
- sigs.k8s.io/controller-runtime v0.23.0 - Kubernetes controller and webhook framework
- k8s.io/client-go v0.35.0 - Kubernetes client library
- k8s.io/apimachinery v0.35.0 - Kubernetes API machinery and types

**Testing:**
- github.com/onsi/ginkgo/v2 v2.27.2 - BDD testing framework
- github.com/onsi/gomega v1.38.2 - Assertion/expectation library

**Build/Dev:**
- sigs.k8s.io/controller-tools v0.20.0 - CRD and webhook code generation (controller-gen)
- sigs.k8s.io/kustomize v5.7.1 - Kubernetes manifest customization (kustomize)
- golangci-lint v2.7.2 - Go linter

## Key Dependencies

**Critical:**
- golang.org/x/time v0.9.0 - Rate limiting (rate.Limiter for Fleet Management API throttling at 3 req/s)
- sigs.k8s.io/controller-runtime/pkg/webhook - Validation webhooks for Pipeline resources
- k8s.io/apimachinery/pkg/api/meta - Kubernetes condition management

**Infrastructure:**
- google.golang.org/grpc v1.72.2 - gRPC protocol support
- google.golang.org/protobuf v1.36.8 - Protocol buffers
- go.opentelemetry.io/otel* v1.36.0 - OpenTelemetry observability (traces, metrics)
- go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.61.0 - HTTP instrumentation
- github.com/prometheus/client_golang v1.23.2 - Prometheus metrics

**Utilities:**
- gopkg.in/yaml.v3 v3.0.1 - YAML parsing (for config validation)
- sigs.k8s.io/yaml v1.6.0 - Kubernetes-style YAML handling

## Configuration

**Environment:**
- Fleet Management API credentials injected via Kubernetes Secrets:
  - `FLEET_MANAGEMENT_BASE_URL` - API base URL
  - `FLEET_MANAGEMENT_USERNAME` - Stack ID (basic auth username)
  - `FLEET_MANAGEMENT_PASSWORD` - Cloud access token (basic auth password)
- Controller flags (command-line):
  - `--leader-elect` - Leader election for HA deployments
  - `--health-probe-bind-address` - Liveness/readiness probe endpoint (default: :8081)
  - `--metrics-bind-address` - Metrics endpoint (default: :8443 HTTPS or :8080 HTTP)
  - `--webhook-cert-path` - Webhook TLS certificate directory
  - `--enable-http2` - Enable HTTP/2 (disabled by default for security)

**Build:**
- Dockerfile: Multi-stage build (Go builder + distroless runtime)
- Makefile: Build targets for manifests, code generation, testing, Docker builds
- kustomize: Configuration overlays in `config/` directory for CRDs, RBAC, webhooks, manager deployment

## Platform Requirements

**Development:**
- Go 1.25+
- kubectl (Kubernetes CLI)
- Docker or podman (container runtime)
- kind (Kubernetes in Docker, for e2e tests)
- make (build orchestration)
- golangci-lint (linting)

**Production:**
- Kubernetes cluster (tested with v1.35 API version)
- TLS certificates for webhooks (cert-manager recommended)
- Secret containing Fleet Management API credentials
- Controller-runtime leader election lease (requires Lease API support)

---

*Stack analysis: 2026-02-08*
