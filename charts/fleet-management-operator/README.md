# Fleet Management Operator Helm Chart

Helm chart for deploying the Grafana Fleet Management Pipeline Operator to Kubernetes.

## Prerequisites

- Kubernetes 1.23+
- Helm 3.8+
- Grafana Cloud account with Fleet Management enabled
- Fleet Management API credentials (Stack ID and Cloud API token)

## Installing the Chart

### Quick Start

```bash
# Add the Helm repository
helm repo add fm-operator https://grafana.github.io/fleet-management-operator
helm repo update

# Install with minimum configuration
helm install fleet-management-operator fm-operator/fleet-management-operator \
  --namespace fleet-management-system \
  --create-namespace \
  --set fleetManagement.baseUrl='https://fleet-management-prod-us-central-0.grafana.net/pipeline.v1.PipelineService/' \
  --set fleetManagement.username='YOUR_STACK_ID' \
  --set fleetManagement.password='YOUR_GRAFANA_CLOUD_TOKEN'
```

### Install from Source

```bash
cd charts/fleet-management-operator

helm install fleet-management-operator . \
  --namespace fleet-management-system \
  --create-namespace \
  --set fleetManagement.baseUrl='https://fleet-management-prod-us-central-0.grafana.net/pipeline.v1.PipelineService/' \
  --set fleetManagement.username='12345' \
  --set fleetManagement.password='glc_xxxxx'
```

### Using a Values File

Create a `values-prod.yaml` file:

```yaml
image:
  repository: ghcr.io/grafana/fleet-management-operator
  tag: v0.1.0

fleetManagement:
  baseUrl: https://fleet-management-prod-us-central-0.grafana.net/pipeline.v1.PipelineService/
  username: "12345"
  password: "glc_xxxxx"

resources:
  limits:
    cpu: 1000m
    memory: 256Mi
  requests:
    cpu: 100m
    memory: 128Mi

metrics:
  enabled: true
  service:
    serviceMonitor:
      enabled: true
```

Install with the values file:

```bash
helm install fleet-management-operator . \
  --namespace fleet-management-system \
  --create-namespace \
  -f values-prod.yaml
```

## Using Existing Secret

If you already have a secret with Fleet Management credentials:

```bash
kubectl create secret generic my-fleet-credentials \
  -n fleet-management-system \
  --from-literal=base-url='https://fleet-management-prod-us-central-0.grafana.net/pipeline.v1.PipelineService/' \
  --from-literal=username='12345' \
  --from-literal=password='glc_xxxxx'

helm install fleet-management-operator . \
  --namespace fleet-management-system \
  --set fleetManagement.existingSecret=my-fleet-credentials
```

## Configuration

The following table lists the configurable parameters and their default values.

### Image Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `image.repository` | Container image repository | `fleet-management-operator` |
| `image.tag` | Container image tag | `dev-v1.0.0` |
| `image.digest` | Image digest (overrides tag when set) | `""` |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `imagePullSecrets` | Image pull secrets | `[]` |

### Production image pinning

The default `image.tag` uses a floating tag. For production deployments, pin to a
specific digest to prevent supply-chain tag-swap attacks:

```yaml
image:
  digest: "sha256:<digest>"
  tag: ""   # ignored when digest is set
```

Find digests in the container registry. The operator's Dockerfile base image
(distroless/static:nonroot) is also pinned to a digest — see `Dockerfile`.

### Fleet Management API

| Parameter | Description | Default |
|-----------|-------------|---------|
| `fleetManagement.baseUrl` | Fleet Management API base URL | `""` (required) |
| `fleetManagement.username` | Grafana Cloud Stack ID | `""` (required) |
| `fleetManagement.password` | Grafana Cloud API token | `""` (required) |
| `fleetManagement.existingSecret` | Use existing secret for credentials | `""` |

### Deployment

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of replicas | `1` |
| `resources.limits.cpu` | CPU limit | `500m` |
| `resources.limits.memory` | Memory limit | `128Mi` |
| `resources.requests.cpu` | CPU request | `10m` |
| `resources.requests.memory` | Memory request | `64Mi` |

### Metrics

| Parameter | Description | Default |
|-----------|-------------|---------|
| `metrics.enabled` | Enable metrics endpoint | `true` |
| `metrics.port` | Metrics port | `8080` |
| `metrics.service.type` | Metrics service type | `ClusterIP` |
| `metrics.service.serviceMonitor.enabled` | Create ServiceMonitor for Prometheus Operator | `false` |
| `metrics.service.serviceMonitor.interval` | Scrape interval | `30s` |

### Leader Election

| Parameter | Description | Default |
|-----------|-------------|---------|
| `leaderElection.enabled` | Enable leader election | `true` |

### Controllers (per-resource opt-in)

Each controller is independently toggleable. New controllers default to **disabled** so existing chart installs see no behavior change. Enabling a controller turns on its reconciler, its admission webhook, and the matching RBAC.

| Parameter | Description | Default |
|-----------|-------------|---------|
| `controllers.pipeline.enabled` | Pipeline reconciler (existing behavior) | `true` |
| `controllers.collector.enabled` | Collector reconciler (manages collector remote attributes) | `false` |
| `controllers.remoteAttributePolicy.enabled` | RemoteAttributePolicy reconciler (bulk attribute assignment by selector) | `false` |
| `controllers.externalAttributeSync.enabled` | ExternalAttributeSync reconciler (HTTP/SQL-backed scheduled attribute pulls) | `false` |
| `controllers.collectorDiscovery.enabled` | CollectorDiscovery reconciler (auto-mirrors Fleet collectors as Collector CRs) | `false` |

CRDs are always installed when `crds.install=true` regardless of which controllers are enabled. Unused CRDs are harmless; missing CRDs would prevent users from inspecting pre-existing objects after a downgrade.

When `controllers.collector.enabled=true`, the operator additionally gets `get/list/watch` on `RemoteAttributePolicy` and `ExternalAttributeSync` so it can compute the merged desired-attribute set for each Collector — even when the corresponding controllers are themselves disabled.

`controllers.collectorDiscovery.enabled=true` requires `controllers.collector.enabled=true`. The manager refuses to start otherwise — discovery would create Collector CRs that no controller acts on.

### RBAC

| Parameter | Description | Default |
|-----------|-------------|---------|
| `rbac.create` | Create RBAC resources | `true` |
| `serviceAccount.create` | Create service account | `true` |
| `serviceAccount.name` | Service account name | `""` (auto-generated) |

## Upgrading the Chart

```bash
helm upgrade fleet-management-operator . \
  --namespace fleet-management-system \
  -f values-prod.yaml
```

## Uninstalling the Chart

```bash
helm uninstall fleet-management-operator --namespace fleet-management-system
```

**Note**: This will NOT delete the CRDs. To delete CRDs:

```bash
kubectl delete crd pipelines.fleetmanagement.grafana.com
```

## Examples

### Create a Pipeline

After installing the operator, create a Pipeline resource:

```yaml
apiVersion: fleetmanagement.grafana.com/v1alpha1
kind: Pipeline
metadata:
  name: prometheus-monitoring
  namespace: fleet-management-system
spec:
  contents: |
    prometheus.exporter.self "alloy" { }

    prometheus.scrape "alloy" {
      targets = prometheus.exporter.self.alloy.targets
      forward_to = [prometheus.remote_write.grafanacloud.receiver]
    }
  matchers:
    - collector.os=linux
    - environment=production
  enabled: true
  configType: Alloy
  source:
    type: Kubernetes
    namespace: production-cluster
```

### Manage a Collector's Remote Attributes

```yaml
controllers:
  collector:
    enabled: true
```

```yaml
apiVersion: fleetmanagement.grafana.com/v1alpha1
kind: Collector
metadata:
  name: edge-host-42
spec:
  id: edge-host-42        # Must match a registered collector ID in Fleet
  remoteAttributes:
    env: prod
    region: us-east-1
```

### Apply Bulk Attribute Defaults via a Policy

```yaml
controllers:
  collector:
    enabled: true
  remoteAttributePolicy:
    enabled: true
```

```yaml
apiVersion: fleetmanagement.grafana.com/v1alpha1
kind: RemoteAttributePolicy
metadata:
  name: linux-prod-defaults
spec:
  selector:
    matchers:
      - "collector.os=linux"
      - "env=prod"
  attributes:
    region: us-east-1
    team: platform
  priority: 0
```

### Sync Attributes from an External CMDB

```yaml
controllers:
  collector:
    enabled: true
  externalAttributeSync:
    enabled: true
```

```yaml
apiVersion: fleetmanagement.grafana.com/v1alpha1
kind: ExternalAttributeSync
metadata:
  name: cmdb-host-attributes
spec:
  source:
    kind: HTTP
    http:
      url: https://cmdb.example.com/api/hosts
    secretRef:
      name: cmdb-credentials   # keys: bearer-token | username + password
  schedule: 5m                 # or "*/15 * * * *"
  selector:
    matchers:
      - "collector.os=linux"
  mapping:
    collectorIDField: hostname
    attributeFields:
      env: env
      region: region
    requiredKeys: [hostname, env]
  allowEmptyResults: false
```

**Precedence (high to low):** ExternalAttributeSync → Collector spec → RemoteAttributePolicy. The Collector controller is the sole writer to Fleet for collector remote attributes; the other controllers maintain status that the Collector reads on each reconcile.

### Auto-Discover Collectors from Fleet Management

```yaml
controllers:
  collector:
    enabled: true
  collectorDiscovery:
    enabled: true
```

```yaml
apiVersion: fleetmanagement.grafana.com/v1alpha1
kind: CollectorDiscovery
metadata:
  name: prod-linux
  namespace: fleet-management-system
spec:
  pollInterval: 5m
  selector:
    matchers:
      - "collector.os=linux"
      - "env=prod"
  # Optional: where to create the mirrored Collector CRs.
  # Defaults to the CollectorDiscovery's own namespace.
  targetNamespace: fleet-mirror
  # Optional: include collectors marked inactive in Fleet (default: false).
  includeInactive: false
  policy:
    # Keep (default) marks vanished CRs as stale; Delete removes them.
    onCollectorRemoved: Keep
```

The reconciler periodically calls Fleet's `ListCollectors` and ensures one `Collector` CR exists per matching collector. Each mirrored CR carries a `fleetmanagement.grafana.com/discovery-name` label and a `fleetmanagement.grafana.com/discovered-by` annotation so users can filter and audit.

Discovery only writes `spec.id` at creation time — users add `spec.remoteAttributes` (and the existing Collector reconciler propagates them to Fleet). Manual edits to discovered CRs survive subsequent polls.

Deleting a `CollectorDiscovery` does NOT cascade-delete its mirrored CRs (orphan-on-delete, to preserve user-added attributes). To remove all mirrored CRs of a discovery, do:

```bash
kubectl delete collector -l fleetmanagement.grafana.com/discovery-name=prod-linux -n fleet-mirror
```

**Pagination caveat:** the Fleet Management SDK's `ListCollectors` does not currently expose pagination. For fleets with more than ~1000 collectors a single response may be truncated server-side. Shard via multiple `CollectorDiscovery` resources with disjoint selectors as a workaround until SDK pagination lands.

### Enable Prometheus Monitoring

```yaml
metrics:
  enabled: true
  service:
    serviceMonitor:
      enabled: true
      additionalLabels:
        prometheus: kube-prometheus
```

### High Availability Setup

```yaml
replicaCount: 2
leaderElection:
  enabled: true

podDisruptionBudget:
  enabled: true
  minAvailable: 1

affinity:
  podAntiAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
    - weight: 100
      podAffinityTerm:
        labelSelector:
          matchLabels:
            app.kubernetes.io/name: fleet-management-operator
        topologyKey: kubernetes.io/hostname
```

## Troubleshooting

### Check Operator Status

```bash
kubectl get pods -n fleet-management-system
kubectl logs -n fleet-management-system deployment/fleet-management-operator-controller-manager
```

### Verify CRD Installation

```bash
kubectl get crds pipelines.fleetmanagement.grafana.com
kubectl explain pipeline.spec
```

### Check Pipeline Status

```bash
kubectl get pipelines -A
kubectl describe pipeline <pipeline-name> -n <namespace>
```

## Troubleshooting Installation

### CRDs not found after install

```bash
kubectl get crd | grep fleetmanagement.grafana.com
```

If empty, CRDs were not installed. Verify `crds.install: true` (the default) and re-run `helm install`.
If upgrading from a manual install, apply CRDs first: `kubectl apply -f charts/fleet-management-operator/crds/`

### Webhook admission failures after install

```bash
kubectl describe validatingwebhookconfiguration | grep -A5 "Failure\|caBundle"
```

If `caBundle` is empty, the TLS cert has not been injected. See `docs/webhook-setup.md` for TLS strategy options.
If the webhook Service has no endpoints, the operator pod may still be starting up — wait for Ready status.

### RBAC permission errors

```bash
kubectl auth can-i create pipelines.fleetmanagement.grafana.com --as=system:serviceaccount:<namespace>:<sa-name>
```

If denied, verify `rbac.create: true` (default) and that the ClusterRole was created. Check: `kubectl get clusterrole | grep fleet`.

### Operator pod not starting — credential Secret missing

The operator reads Fleet Management credentials from a Secret. If the Secret is missing:
```bash
kubectl get secret -n <namespace> | grep fleet-management
```

Create it if missing:
```bash
kubectl create secret generic fleet-management-credentials \
  --from-literal=base-url=https://fleet-management-<cluster>.grafana.net/... \
  --from-literal=username=<stack-id> \
  --from-literal=password=<api-token> \
  -n <namespace>
```

### Metrics endpoint not scraped by Prometheus

Verify the metrics endpoint is bound (requires `metrics.enabled: true` and `metrics.secure: false`):
```bash
kubectl exec -n <namespace> <pod-name> -- wget -qO- http://localhost:8080/metrics | head -5
```

If empty, check that `--metrics-bind-address` is being passed (verify pod args: `kubectl describe pod <pod> | grep metrics-bind`).

## Support

For issues and questions:
- GitHub Issues: https://github.com/grafana/fleet-management-operator/issues
- Grafana Fleet Management Documentation: https://grafana.com/docs/grafana-cloud/monitor-infrastructure/fleet-management/
- Grafana Alloy Documentation: https://grafana.com/docs/alloy/latest/
