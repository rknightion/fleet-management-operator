# Contributing to Fleet Management Operator

Thank you for your interest in contributing! This document provides guidelines and instructions for developing the Fleet Management Operator.

## Table of Contents

- [Development Setup](#development-setup)
- [Building and Testing](#building-and-testing)
- [Project Structure](#project-structure)
- [Development Workflow](#development-workflow)
- [Testing Guidelines](#testing-guidelines)
- [Code Style](#code-style)
- [Submitting Changes](#submitting-changes)

## Development Setup

### Prerequisites

- Go 1.27+
- Docker
- kubectl
- kind or minikube (for local testing)
- just
- Grafana Cloud Fleet Management credentials

### Clone the Repository

```bash
git clone https://github.com/rknightion/fleet-management-operator.git
cd fleet-management-operator
```

### Install Dependencies

```bash
# Download Go modules
go mod download

# Install development tools (controller-gen, kustomize, etc.)
just setup
```

### Set Up Fleet Management Credentials

Export your Fleet Management credentials as environment variables:

```bash
export FLEET_MANAGEMENT_BASE_URL="https://fleet-management-<CLUSTER>.grafana.net/pipeline.v1.PipelineService/"
export FLEET_MANAGEMENT_USERNAME="<STACK_ID>"
export FLEET_MANAGEMENT_PASSWORD="<API_TOKEN>"
```

Or create a secret in your cluster:

```bash
kubectl create secret generic fleet-management-credentials \
  -n fleet-management-system \
  --from-literal=base-url="$FLEET_MANAGEMENT_BASE_URL" \
  --from-literal=username="$FLEET_MANAGEMENT_USERNAME" \
  --from-literal=password="$FLEET_MANAGEMENT_PASSWORD"
```

## Building and Testing

### Run Locally

Install CRDs and run the controller locally against your kubeconfig cluster:

```bash
# Install CRDs
just install

# Run controller locally
just run
```

In another terminal, create a test pipeline:

```bash
kubectl apply -n <namespace> -f config/samples/alloy_pipeline_sample.yaml
```

### Run Tests

```bash
# Run all tests
just test

# Run tests with coverage
just test
go tool cover -html=cover.out

# Run specific test
go test -v ./internal/controller -run TestControllers

# Run tests with race detector
go test -race ./...
```

### Lint Code

```bash
# Run linter
just lint

# Auto-fix linting issues
just lint-fix
```

### Build Docker Image

```bash
# Build for local architecture
IMG=fleet-management-operator:dev just docker-build-load

# Build multi-arch image and push (prompts for confirmation)
IMG=ghcr.io/rknightion/fleet-management-operator:v0.1.0 just docker-build
```

### Deploy to Cluster

```bash
# Build and load image into kind cluster
IMG=fleet-management-operator:dev just docker-build-load
kind load docker-image fleet-management-operator:dev

# Deploy to cluster
IMG=fleet-management-operator:dev just deploy

# Check deployment
kubectl get pods -n fleet-management-system
kubectl logs -f deployment/fleet-management-operator-controller-manager -n fleet-management-system
```

## Project Structure

```
fleet-management-operator/
├── api/v1alpha1/              # CRD API definitions
│   ├── pipeline_types.go      # Pipeline CRD spec and status
│   └── groupversion_info.go   # API group metadata
│
├── internal/controller/       # Controller implementation
│   ├── pipeline_controller.go      # Main reconciliation logic
│   └── pipeline_controller_test.go # Controller tests
│
├── pkg/fleetclient/          # Fleet Management API client
│   ├── client.go             # HTTP client for Fleet Management API
│   └── types.go              # API request/response types
│
├── config/                   # Kubernetes manifests
│   ├── crd/bases/           # Generated CRD manifests
│   ├── manager/             # Controller deployment
│   ├── rbac/                # RBAC permissions
│   ├── samples/             # Example Pipeline resources
│   └── default/             # Kustomize overlay
│
├── charts/                  # Helm chart
│   └── fleet-management-operator/
│
├── cmd/main.go             # Controller entry point
├── justfile                # Task surface
├── Dockerfile              # Multi-arch container image
└── go.mod                  # Go module dependencies
```

### Key Files

- **api/v1alpha1/pipeline_types.go**: Defines the Pipeline CRD structure
- **internal/controller/pipeline_controller.go**: Core reconciliation logic
- **pkg/fleetclient/client.go**: Fleet Management API client
- **AGENTS.md**: Comprehensive development guide with API details (Claude Code reads it via the `@AGENTS.md` import in `CLAUDE.md`)
- **IMPLEMENTATION_STATUS.md**: Feature implementation checklist

## Development Workflow

### 1. Make Changes

Edit code in `api/`, `internal/controller/`, or `pkg/`.

### 2. Generate Code

After modifying CRD types in `api/v1alpha1/`:

```bash
# Generate DeepCopy methods and CRD manifests
just gen

# Verify changes
git diff config/crd/bases/
```

### 3. Run Tests

```bash
just test
```

### 4. Test Locally

```bash
# Run controller locally
just run

# In another terminal, test your changes
kubectl apply -n <namespace> -f config/samples/alloy_pipeline_sample.yaml
kubectl get pipelines -n <namespace>
kubectl describe pipeline alloy-pipeline-sample -n <namespace>
```

### 5. Build and Test in Cluster

```bash
# Build image
IMG=fleet-management-operator:dev just docker-build-load

# Load into kind
kind load docker-image fleet-management-operator:dev

# Deploy
IMG=fleet-management-operator:dev just deploy

# Verify
kubectl get pods -n fleet-management-system
```

### 6. Cleanup

```bash
# Undeploy from cluster
just undeploy

# Uninstall CRDs
just uninstall
```

## Testing Guidelines

### Unit Tests

Unit tests use mock Fleet Management API clients:

```go
func TestBuildUpsertRequest(t *testing.T) {
    reconciler := &PipelineReconciler{}
    pipeline := &fleetmanagementv1alpha1.Pipeline{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "test",
            Namespace: "default",
        },
        Spec: fleetmanagementv1alpha1.PipelineSpec{
            Contents:   "test content",
            Enabled:    true,
            ConfigType: fleetmanagementv1alpha1.ConfigTypeAlloy,
        },
    }

    req := reconciler.buildUpsertRequest(pipeline)
    assert.Equal(t, "test", req.Pipeline.Name)
}
```

### Integration Tests

Integration tests use envtest (fake Kubernetes API):

```go
var _ = Describe("Pipeline Controller", func() {
    It("should successfully reconcile a new Pipeline", func() {
        pipeline := &fleetmanagementv1alpha1.Pipeline{...}
        Expect(k8sClient.Create(ctx, pipeline)).To(Succeed())

        Eventually(func() string {
            err := k8sClient.Get(ctx, key, pipeline)
            if err != nil {
                return ""
            }
            return pipeline.Status.ID
        }, timeout, interval).Should(Equal("mock-id-123"))
    })
})
```

### E2E Tests

End-to-end tests against a real cluster:

```bash
# Run E2E tests (requires kind)
just test-e2e
```

## Code Style

### Go Conventions

- Follow [Effective Go](https://golang.org/doc/effective_go.html)
- Use `gofmt` for formatting
- Run `golangci-lint` before committing

### Kubernetes Patterns

- Use controller-runtime patterns
- Implement proper finalizers
- Update status subresource separately
- Use structured logging

### Error Handling

```go
// Return errors for automatic retry
if err := r.FleetClient.UpsertPipeline(ctx, req); err != nil {
    return ctrl.Result{}, fmt.Errorf("failed to upsert pipeline: %w", err)
}

// Use Result{RequeueAfter} for delays
return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
```

### Status Conditions

Use standard Kubernetes condition helpers:

```go
import "k8s.io/apimachinery/pkg/api/meta"

meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
    Type:               conditionTypeReady,
    Status:             metav1.ConditionTrue,
    Reason:             reasonSynced,
    Message:            "Pipeline successfully synced to Fleet Management",
    ObservedGeneration: pipeline.Generation,
})
```

## Submitting Changes

### Before Submitting

1. **Run the gate**: `just check < /dev/null` — this is the whole thing CI
   enforces, and it must pass before you submit.
2. **Check git diff**: Ensure no unintended changes

While iterating, the individual recipes are faster than the full gate, but none
of them replaces it:

- `just test`, `just lint` — the usual inner loop
- `just gen` — regenerate manifests after changing CRD types or RBAC markers
- `just docs` — regenerate docs after changing a flag in `cmd/main.go`, a
  Prometheus metric, an event reason, a sample CR, a CRD field godoc comment, or
  a status condition; `just check` fails on the resulting drift
- `just run` — run the operator against your current kubeconfig

### Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add support for source tracking
fix: correct finalizer removal logic
docs: update installation instructions
test: add integration tests for deletion
chore: update dependencies
```

### Pull Request Process

1. Fork the repository
2. Create a feature branch: `git checkout -b feat/my-feature`
3. Make your changes
4. Run tests and linting
5. Commit with descriptive messages
6. Push to your fork
7. Open a Pull Request

### Pull Request Template

```markdown
## Description
Brief description of changes

## Type of Change
- [ ] Bug fix
- [ ] New feature
- [ ] Breaking change
- [ ] Documentation update

## Testing
- [ ] Unit tests pass
- [ ] Integration tests pass
- [ ] Tested locally
- [ ] Tested in cluster

## Checklist
- [ ] Code follows style guidelines
- [ ] Manifests regenerated (if needed)
- [ ] Documentation updated
- [ ] Changelog updated (if needed)
```

## Useful Just Recipes

```bash
# Development
just manifests          # Generate CRD manifests
just generate           # Generate code (DeepCopy, etc.)
just fmt                # Format code and the justfile
just typecheck          # Run go vet
just lint               # Run golangci-lint

# Testing
just test               # Run all tests
just test-e2e           # Run E2E tests

# Building
just build              # Build manager binary
just docker-build       # Build and push a multi-arch image (prompts)
just docker-build-load  # Build and load locally

# Deployment
just install            # Install CRDs
just uninstall          # Remove CRDs (prompts)
just deploy             # Deploy controller (prompts)
just undeploy           # Remove controller (prompts)
just run                # Run locally

# Release
just build-installer    # Generate install.yaml
```

## Resources

- **AGENTS.md**: Comprehensive development guide with:
  - Fleet Management API documentation
  - Controller patterns and best practices
  - Kubernetes controller pitfalls
  - Go best practices

- **IMPLEMENTATION_STATUS.md**: Feature checklist and status

- **config/samples/**: Example Pipeline resources

- [Kubebuilder Book](https://book.kubebuilder.io/): Controller development guide
- [Controller Runtime](https://github.com/kubernetes-sigs/controller-runtime): Framework documentation

## Getting Help

- Check [AGENTS.md](AGENTS.md) for detailed development guidance
- Review [existing issues](https://github.com/rknightion/fleet-management-operator/issues)
- Open a new issue for bugs or feature requests

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.
