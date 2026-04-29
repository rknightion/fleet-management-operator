# Fleet Management Operator

A Kubernetes operator for managing [Grafana Cloud Fleet Management](https://grafana.com/docs/grafana-cloud/send-data/fleet-management/) Pipelines as native Kubernetes resources.

> **Disclaimer**: This is not an official Grafana Labs product and is not officially supported by Grafana Labs. This is a community project provided as-is. For official Grafana Cloud Fleet Management support, please contact [Grafana Support](https://grafana.com/support/).

## Overview

This operator enables declarative management of Fleet Management configuration pipelines using Kubernetes. Define your Alloy or OpenTelemetry Collector configurations as Kubernetes resources, and the operator automatically syncs them to Grafana Cloud Fleet Management.

### Features

- **Declarative Pipeline Management**: Define pipelines as Kubernetes resources
- **Dual Config Support**: Both Grafana Alloy and OpenTelemetry Collector configurations
- **Source Tracking**: Track pipeline origins (Git, Terraform, Kubernetes)
- **GitOps Friendly**: Manage pipelines through version control
- **Status Tracking**: Pipeline status reflects Fleet Management state with conditions
- **High Availability**: Leader election support for multiple replicas

## Installation

### Prerequisites
- Grafana Cloud Fleet Management credentials (base URL, username, password/token)

Get credentials from your Grafana Cloud Fleet Management interface:
- Navigate to **Connections > Collector > Fleet Management**
- Switch to the **API tab**
- Find the base URL and generate an API token

### Install with Helm

```bash
# Add the Helm repository
helm repo add fleet-management https://YOUR_USERNAME.github.io/fleet-management-operator/charts
helm repo update

# Install the operator
helm install fleet-management-operator fleet-management/fleet-management-operator \
  --namespace fleet-management-system \
  --create-namespace \
  --set fleetManagement.baseUrl='https://fleet-management-<CLUSTER>.grafana.net/pipeline.v1.PipelineService/' \
  --set fleetManagement.username='<STACK_ID>' \
  --set fleetManagement.password='<API_TOKEN>'
```

### Install with kubectl

```bash
# Download and apply the installation manifest
kubectl apply -f https://github.com/YOUR_USERNAME/fleet-management-operator/releases/latest/download/install.yaml

# Create credentials secret
kubectl create secret generic fleet-management-operator-credentials \
  -n fleet-management-system \
  --from-literal=base-url='https://fleet-management-<CLUSTER>.grafana.net/pipeline.v1.PipelineService/' \
  --from-literal=username='<STACK_ID>' \
  --from-literal=password='<API_TOKEN>'
```

## Usage

### Create a Pipeline

Create a Pipeline resource to define your Alloy or OpenTelemetry Collector configuration:

**Grafana Alloy Example:**

```yaml
apiVersion: fleetmanagement.grafana.com/v1alpha1
kind: Pipeline
metadata:
  name: prometheus-metrics
  namespace: default
spec:
  contents: |
    prometheus.exporter.self "alloy" { }

    prometheus.scrape "alloy" {
      targets    = prometheus.exporter.self.alloy.targets
      forward_to = [prometheus.remote_write.grafanacloud.receiver]
      scrape_interval = "60s"
    }

    prometheus.remote_write "grafanacloud" {
      external_labels = {"collector_id" = constants.hostname}
      endpoint {
        url = env("PROMETHEUS_URL")
        basic_auth {
          username      = env("PROMETHEUS_USER")
          password_file = "/etc/secrets/prometheus-password"
        }
      }
    }

  matchers:
    - collector.os=linux
    - environment=production

  enabled: true
  configType: Alloy
```

**OpenTelemetry Collector Example:**

```yaml
apiVersion: fleetmanagement.grafana.com/v1alpha1
kind: Pipeline
metadata:
  name: otel-metrics
  namespace: default
spec:
  contents: |
    receivers:
      otlp:
        protocols:
          grpc:
            endpoint: 0.0.0.0:4317

    processors:
      batch:
        timeout: 10s

    exporters:
      prometheusremotewrite:
        endpoint: ${env:PROMETHEUS_URL}

    service:
      pipelines:
        metrics:
          receivers: [otlp]
          processors: [batch]
          exporters: [prometheusremotewrite]

  matchers:
    - collector.type=otel
    - environment=production

  enabled: true
  configType: OpenTelemetryCollector
```

Apply the pipeline:

```bash
kubectl apply -f pipeline.yaml
```

### Check Pipeline Status

```bash
# List all pipelines
kubectl get pipelines

# Get detailed status
kubectl describe pipeline prometheus-metrics

# Watch for changes
kubectl get pipelines -w
```

The status shows the Fleet Management pipeline ID and sync state:

```yaml
status:
  id: "12345"
  observedGeneration: 1
  conditions:
  - type: Ready
    status: "True"
    reason: Synced
  - type: Synced
    status: "True"
    reason: UpsertSucceeded
```

### Update a Pipeline

Edit the Pipeline resource and apply changes:

```bash
kubectl edit pipeline prometheus-metrics
```

The operator automatically syncs changes to Fleet Management.

### Delete a Pipeline

```bash
kubectl delete pipeline prometheus-metrics
```

The operator removes the pipeline from Fleet Management before deleting the Kubernetes resource.

## Configuration

### Matchers

Pipelines are assigned to collectors using matchers with Prometheus Alertmanager syntax:

- `key=value` - Equals
- `key!=value` - Not equals
- `key=~regex` - Regex match
- `key!~regex` - Regex not match

Example:
```yaml
matchers:
  - collector.os=linux
  - environment!=development
  - region=~us-.*
```

### Config Types

- **Alloy**: For Grafana Alloy collectors (default)
- **OpenTelemetryCollector**: For OpenTelemetry Collector instances

The `configType` must match your collector type.

### Source Tracking

Track pipeline origins with the `source` field:

```yaml
spec:
  source:
    type: Git  # or Terraform, Kubernetes, Unspecified
    namespace: github.com/myorg/configs
```

## Troubleshooting

### Pipeline not syncing

**Check controller logs:**
```bash
kubectl logs -n fleet-management-system -l app.kubernetes.io/name=fleet-management-operator
```

**Check pipeline status:**
```bash
kubectl describe pipeline <pipeline-name>
```

Look for error messages in `status.conditions`.

### Common Issues

**Authentication error:**
- Verify credentials in the secret are correct
- Check that the API token has Pipeline Management permissions

**Validation error:**
- Check `status.conditions` for specific validation errors
- Verify `configType` matches the configuration syntax (Alloy vs OTEL)

**Rate limit exceeded:**
- Controller automatically retries with exponential backoff
- Fleet Management has a 3 req/s limit on management endpoints

**Pipeline stuck in Terminating:**
- Check controller logs for finalizer removal errors
- Verify network connectivity to Fleet Management API

## Uninstall

**With Helm:**
```bash
helm uninstall fleet-management-operator -n fleet-management-system
kubectl delete namespace fleet-management-system
```

**With kubectl:**
```bash
kubectl delete -f https://github.com/YOUR_USERNAME/fleet-management-operator/releases/latest/download/install.yaml
```

## Contributing

Contributions are welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, testing, and contribution guidelines.

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

## Resources

- [Fleet Management Documentation](https://grafana.com/docs/grafana-cloud/send-data/fleet-management/)
- [Helm Chart Documentation](charts/fleet-management-operator/README.md)
- [API Reference](CLAUDE.md)
