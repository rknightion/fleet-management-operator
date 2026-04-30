# fleet-management-operator

![Version: 0.1.0](https://img.shields.io/badge/Version-0.1.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: v0.1.0](https://img.shields.io/badge/AppVersion-v0.1.0-informational?style=flat-square)

A Kubernetes operator for managing Grafana Cloud Fleet Management Pipelines

**Homepage:** <https://github.com/rknightion/fleet-management-operator>

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| Rob Knight | <rob.knight@grafana.com> |  |

## Source Code

* <https://github.com/rknightion/fleet-management-operator>

## Requirements

Kubernetes: `>=1.23.0-0`

## Prerequisites

- Kubernetes 1.23+
- Helm 3.8+
- cert-manager installed, plus an `Issuer` or `ClusterIssuer` matching
  `webhook.certManager.issuerRef` (default: `ClusterIssuer/selfsigned-issuer`),
  unless you configure manual webhook TLS with `webhook.certManager.enabled=false`,
  `webhook.certDir`, `webhook.certSecretName`, and `webhook.caBundle`
- Grafana Cloud account with Fleet Management enabled
- Fleet Management API credentials (Stack ID and Cloud API token)

## Installing the Chart

### Quick Start

```bash
helm install fleet-management-operator . \
  --namespace fleet-management-system \
  --create-namespace \
  --set fleetManagement.baseUrl='https://fleet-management-prod-us-central-0.grafana.net/pipeline.v1.PipelineService/' \
  --set fleetManagement.username='YOUR_STACK_ID' \
  --set fleetManagement.password='YOUR_GRAFANA_CLOUD_TOKEN'
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

metrics:
  enabled: true
  service:
    serviceMonitor:
      enabled: true
```

> Resource limits and requests are intentionally omitted from this example —
> the chart defaults (limits 500m CPU / 2Gi memory, requests 10m CPU / 512Mi
> memory) are tuned for the 30k-Collector tier. Override `resources.*` only
> if you have measured your fleet size and steady-state working set; see the
> sizing guidance comment in `values.yaml`.

Install with the values file:

```bash
helm install fleet-management-operator . \
  --namespace fleet-management-system \
  --create-namespace \
  -f values-prod.yaml
```

### Using an Existing Secret

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

## Production image pinning

The default `image.tag` uses a floating tag. For production deployments, pin to a
specific digest to prevent supply-chain tag-swap attacks:

```yaml
image:
  digest: "sha256:<digest>"
  tag: ""   # ignored when digest is set
```

Find digests in the container registry. The operator's Dockerfile base image
(distroless/static:nonroot) is also pinned to a digest — see `Dockerfile`.

## Controllers (per-resource opt-in)

Each controller is independently toggleable. New controllers default to **disabled**
so existing chart installs see no behavior change. Enabling a controller turns on
its reconciler, its admission webhook, and the matching RBAC.

CRDs are always installed regardless of which controllers are enabled. Unused CRDs
are harmless; missing CRDs would prevent users from inspecting pre-existing
objects after a downgrade. See "CRD lifecycle" below for how Helm 3 manages CRD
install/upgrade/uninstall semantics.

When `controllers.collector.enabled=true`, the operator additionally gets
`get/list/watch` on `RemoteAttributePolicy` and `ExternalAttributeSync` so it can
compute the merged desired-attribute set for each Collector — even when those
controllers are themselves disabled.

`controllers.collectorDiscovery.enabled=true` requires `controllers.collector.enabled=true`.
The manager refuses to start otherwise — discovery would create Collector CRs
that no controller acts on.

`controllers.pipelineDiscovery.enabled=true` enables PipelineDiscovery and its
validating webhook. The chart renders the webhook Service, cert-manager
Certificate, and ValidatingWebhookConfiguration for PipelineDiscovery even when
the Pipeline controller is disabled.

`controllers.tenantPolicy.enabled=true` enables the TenantPolicy admission webhook
that enforces required-matcher constraints on Pipeline / RemoteAttributePolicy /
ExternalAttributeSync. The TenantPolicy CRD is always installed; only enforcement
is gated by the flag.

## Webhook TLS

The chart renders fail-closed validating webhooks, so the Kubernetes API server
must trust the webhook serving certificate. The default path uses cert-manager CA
injection. For manual TLS, disable cert-manager and set all of:
`webhook.certDir`, `webhook.certSecretName`, and `webhook.caBundle`. Rendering
fails if webhooks are enabled without either cert-manager injection or the full
manual CA/certificate configuration.

## NetworkPolicy

`networkPolicy.enabled=false` by default. When enabled, the chart renders a
single NetworkPolicy selecting the operator pods. It allows webhook ingress,
metrics ingress, DNS egress, Kubernetes API egress, Fleet API egress, and any
approved ExternalAttributeSync HTTP/SQL source egress described in values.

Kubernetes NetworkPolicy cannot match DNS names, so set cluster-specific
`networkPolicy.egress.*.to` peers or `ipBlock` CIDRs for the Kubernetes API,
Grafana Cloud Fleet endpoint, private Fleet endpoints, and approved CMDB or SQL
sources. Empty `to` lists allow the configured port to any destination.

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Pod affinity / anti-affinity rules. Empty default plus `replicaCount > 1` triggers an auto-injected soft hostname anti-affinity. Set non-empty to opt out of the auto-injected default. |
| alerts.enabled | bool | `false` |  |
| alerts.labels | object | `{}` |  |
| controllers.collector.enabled | bool | `false` | Enable the Collector reconciler and its admission webhook. Manages remote attributes on a registered collector. |
| controllers.collectorDiscovery.enabled | bool | `false` | Enable the CollectorDiscovery reconciler and its admission webhook. **REQUIRES `controllers.collector.enabled: true`.** The manager refuses to start otherwise — discovery would create Collector CRs that no controller acts on. |
| controllers.collectorDiscovery.maxConcurrent | int | `1` | Max concurrent reconciles for CollectorDiscovery. Keep at 1: concurrency > 1 triggers multiple ListCollectors per poll cycle without benefit. |
| controllers.externalAttributeSync.enabled | bool | `false` | Enable the ExternalAttributeSync reconciler and its admission webhook. Pulls attributes from CMDB / SQL / HTTP sources on a schedule. |
| controllers.externalAttributeSync.maxConcurrent | int | `4` | Max concurrent reconciles for ExternalAttributeSync. Safe to increase: Fetch calls are per-source. |
| controllers.externalAttributeSync.sourceTargetBurst | int | `4` | Token bucket size for the per-target limiter. Ignored when `sourceTargetRate` is 0. Default 4 matches `maxConcurrent` so a single concurrency generation passes through immediately, then refills at `sourceTargetRate` per second. |
| controllers.externalAttributeSync.sourceTargetRate | int | `0` | Per-target rate limit (tokens/sec). Two syncs that resolve to the same upstream share a token bucket so a restart wave cannot stampede a customer source. 0 (default) disables per-target limiting. Set to 1 for one fetch/sec/upstream — typically plenty since sync schedules run every minute or longer. |
| controllers.pipeline.enabled | bool | `true` | Enable the Pipeline reconciler and its admission webhook. The core controller; default on. |
| controllers.pipelineDiscovery.enabled | bool | `true` | Enable the PipelineDiscovery reconciler and its admission webhook. Polls Fleet Management ListPipelines and creates Pipeline CRs for each discovered pipeline. |
| controllers.pipelineDiscovery.maxConcurrent | int | `1` | Max concurrent reconciles for PipelineDiscovery. Keep at 1: concurrency > 1 triggers multiple ListPipelines calls per poll cycle without benefit. |
| controllers.remoteAttributePolicy.enabled | bool | `false` | Enable the RemoteAttributePolicy reconciler and its admission webhook. Bulk attribute assignment by selector across many Collectors. |
| controllers.remoteAttributePolicy.maxConcurrent | int | `4` | Max concurrent reconciles for RemoteAttributePolicy. Safe to increase: reconciles are pure K8s cache reads with no Fleet API calls. Pipeline and Collector always run at 1 because they share the Fleet API rate budget. |
| controllers.tenantPolicy.enabled | bool | `false` | Enable TenantPolicy enforcement. When set, validating webhooks for Pipeline / RemoteAttributePolicy / ExternalAttributeSync require K8s subjects matched by a TenantPolicy CR to include at least one of the policy's required matchers. Default-allow when no policy matches; the CRD is always installed so policies can be authored ahead of enforcement. |
| enableHTTP2 | bool | `false` | Enable HTTP/2 on the metrics and webhook servers. Default `false` mitigates the HTTP/2 Stream Cancellation (CVE GHSA-qppj-fm5r-hxr3) and Rapid Reset (CVE GHSA-4374-p667-p6c8) DoS classes — the K8s API server webhook client and standard Prometheus scrapers both speak HTTP/1.1, so disabling HTTP/2 has no practical impact for in-cluster traffic. Flip to `true` only if your scrape path or webhook caller mandates HTTP/2 (rare). |
| fleetManagement.apiRateBurst | int | `50` | Token bucket size for the Fleet API rate limiter. Absorbs startup / post-restart spikes without changing the sustained RPS ceiling. burst=1 causes livelock at scale (request queue backs up to the 30s HTTP timeout). |
| fleetManagement.apiRatePerSecond | int | `3` | Sustained Fleet Management API rate limit in requests per second. Match this to your stack's server-side `api:` rate setting. Standard stacks: 3. |
| fleetManagement.baseUrl | string | `""` | Fleet Management API base URL. Required unless `existingSecret` is set. Example: `https://fleet-management-prod-us-central-0.grafana.net/pipeline.v1.PipelineService/` |
| fleetManagement.existingSecret | string | `""` | Use a pre-existing Secret for credentials instead of creating one. When set, `baseUrl` / `username` / `password` are ignored. |
| fleetManagement.existingSecretKeys | object | `{"baseUrl":"base-url","password":"password","username":"username"}` | Keys to read from `existingSecret`. Override only if your secret uses different keys. |
| fleetManagement.password | string | `""` | Grafana Cloud API token. Required unless `existingSecret` is set. |
| fleetManagement.username | string | `""` | Grafana Cloud Stack ID. Required unless `existingSecret` is set. |
| fullnameOverride | string | `""` | Override the chart fullname (defaults to "<release-name>-fleet-management-operator"). |
| grafana.dashboards.enabled | bool | `false` |  |
| healthProbe.liveness.failureThreshold | int | `3` |  |
| healthProbe.liveness.initialDelaySeconds | int | `45` |  |
| healthProbe.liveness.periodSeconds | int | `20` |  |
| healthProbe.liveness.timeoutSeconds | int | `1` |  |
| healthProbe.port | int | `8081` |  |
| healthProbe.readiness.failureThreshold | int | `3` |  |
| healthProbe.readiness.initialDelaySeconds | int | `5` |  |
| healthProbe.readiness.periodSeconds | int | `10` |  |
| healthProbe.readiness.timeoutSeconds | int | `1` |  |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy. |
| image.repository | string | `"ghcr.io/rknightion/fleet-management-operator"` | Container image repository. |
| image.tag | string | `"dev"` | Image tag. Ignored when `image.digest` is set. |
| imagePullSecrets | list | `[]` | Image pull secrets for private / air-gapped registries. |
| leaderElection.enabled | bool | `true` |  |
| leaderElection.leaseDuration | string | `"15s"` |  |
| leaderElection.renewDeadline | string | `"10s"` |  |
| leaderElection.retryPeriod | string | `"2s"` |  |
| logging.level | string | `"info"` |  |
| metrics.enabled | bool | `true` |  |
| metrics.port | int | `8080` |  |
| metrics.secure | bool | `false` |  |
| metrics.service.annotations | object | `{}` |  |
| metrics.service.port | int | `8080` |  |
| metrics.service.serviceMonitor.additionalLabels | object | `{}` |  |
| metrics.service.serviceMonitor.enabled | bool | `false` |  |
| metrics.service.serviceMonitor.interval | string | `"30s"` |  |
| metrics.service.serviceMonitor.scrapeTimeout | string | `"10s"` |  |
| metrics.service.type | string | `"ClusterIP"` |  |
| nameOverride | string | `""` | Override the chart name (defaults to "fleet-management-operator"). |
| networkPolicy.egress.dns.enabled | bool | `true` | Allow DNS lookups. Adjust `to` if your cluster DNS pods use different labels or run outside kube-system. |
| networkPolicy.egress.dns.ports[0].port | int | `53` |  |
| networkPolicy.egress.dns.ports[0].protocol | string | `"UDP"` |  |
| networkPolicy.egress.dns.ports[1].port | int | `53` |  |
| networkPolicy.egress.dns.ports[1].protocol | string | `"TCP"` |  |
| networkPolicy.egress.dns.to[0].namespaceSelector.matchLabels."kubernetes.io/metadata.name" | string | `"kube-system"` |  |
| networkPolicy.egress.dns.to[0].podSelector.matchLabels.k8s-app | string | `"kube-dns"` |  |
| networkPolicy.egress.externalSources | list | `[]` | Egress rules for ExternalAttributeSync HTTP/SQL sources. Populate with the approved CMDB/database destinations before enabling those syncs. |
| networkPolicy.egress.extra | list | `[]` | Additional egress rules appended verbatim to the NetworkPolicy. |
| networkPolicy.egress.fleetAPI.enabled | bool | `true` | Allow Fleet Management API access. Empty `to` allows any destination on TCP/443; set Grafana Cloud/private endpoint CIDRs where available. |
| networkPolicy.egress.fleetAPI.ports[0].port | int | `443` |  |
| networkPolicy.egress.fleetAPI.ports[0].protocol | string | `"TCP"` |  |
| networkPolicy.egress.fleetAPI.to | list | `[]` |  |
| networkPolicy.egress.kubeAPI.enabled | bool | `true` | Allow Kubernetes API access for controller-runtime watches and writes. Empty `to` allows any destination on TCP/443; set control-plane CIDRs or selectors for stricter clusters. |
| networkPolicy.egress.kubeAPI.ports[0].port | int | `443` |  |
| networkPolicy.egress.kubeAPI.ports[0].protocol | string | `"TCP"` |  |
| networkPolicy.egress.kubeAPI.to | list | `[]` |  |
| networkPolicy.enabled | bool | `false` | Create a NetworkPolicy for the operator pods. Disabled by default. |
| networkPolicy.ingress.extra | list | `[]` | Additional ingress rules appended verbatim to the NetworkPolicy. |
| networkPolicy.ingress.metrics.enabled | bool | `true` | Allow Prometheus scrapes to the metrics port when metrics are enabled. Add `from` peers for Prometheus namespace/pod selectors or CIDRs. |
| networkPolicy.ingress.metrics.from | list | `[]` |  |
| networkPolicy.ingress.webhook.enabled | bool | `true` | Allow Kubernetes API server admission calls to the webhook port. Add `from` peers to restrict this to control-plane/API-server CIDRs. |
| networkPolicy.ingress.webhook.from | list | `[]` |  |
| nodeSelector | object | `{}` |  |
| podAnnotations | object | `{}` |  |
| podDisruptionBudget.enabled | bool | `false` | Enable the PodDisruptionBudget. Only takes effect when `replicaCount > 1`. |
| podDisruptionBudget.minAvailable | int | `1` | Minimum available replicas during voluntary disruption. For HA (replicaCount > 1), prefer `maxUnavailable: 1` over `minAvailable` for graceful rolling updates. |
| podLabels | object | `{}` |  |
| podSecurityContext.fsGroup | int | `65532` |  |
| podSecurityContext.runAsNonRoot | bool | `true` |  |
| podSecurityContext.seccompProfile.type | string | `"RuntimeDefault"` |  |
| priorityClassName | string | `""` |  |
| rbac.create | bool | `true` |  |
| replicaCount | int | `1` | Number of operator replicas. Only one is active at a time via leader election. Set > 1 for HA — also see `podDisruptionBudget` and `affinity.podAntiAffinity`. At replicaCount > 1 with empty `affinity`, the chart auto-injects a soft pod anti-affinity (hostname topology) so replicas spread across nodes. |
| resources | object | `{"limits":{"cpu":"500m","memory":"2Gi"},"requests":{"cpu":"10m","memory":"512Mi"}}` | Pod CPU/memory resource requests and limits. Defaults are tuned for the 30k-Collector tier; see the inline sizing comment above for smaller fleets. |
| securityContext.allowPrivilegeEscalation | bool | `false` |  |
| securityContext.capabilities.drop[0] | string | `"ALL"` |  |
| securityContext.readOnlyRootFilesystem | bool | `true` |  |
| securityContext.runAsGroup | int | `65532` |  |
| securityContext.runAsUser | int | `65532` |  |
| serviceAccount.annotations | object | `{}` |  |
| serviceAccount.automount | bool | `true` |  |
| serviceAccount.create | bool | `true` |  |
| serviceAccount.name | string | `""` |  |
| terminationGracePeriodSeconds | int | `30` |  |
| tolerations | list | `[]` |  |
| updateStrategy.rollingUpdate.maxSurge | int | `1` | Max pods over `replicas` allowed during a rollout. 1 lets the new ReplicaSet bring up one pod before the old one is removed. |
| updateStrategy.rollingUpdate.maxUnavailable | int | `0` | Max pods unavailable during a rollout. 0 keeps at least one Ready replica throughout the rollout — required for HA admission availability. |
| updateStrategy.type | string | `"RollingUpdate"` | Deployment strategy type. RollingUpdate (default) or Recreate. |
| webhook.caBundle | string | `""` | Base64-encoded PEM CA bundle for manual webhook TLS. Required when `webhook.certManager.enabled=false` and validating webhooks are rendered. Must trust the serving certificate in `webhook.certSecretName`. |
| webhook.certDir | string | `""` | Directory containing webhook TLS cert files (tls.crt, tls.key). When set, passed as `--webhook-cert-path` to the manager AND requires `webhook.certSecretName` so the chart can mount that Secret read-only at this path. Leave empty to use controller-runtime's auto-generated self-signed certs (dev/test only). Mutually exclusive with `webhook.certManager.enabled` — the chart fails to render if both are set. |
| webhook.certKey | string | `"tls.key"` | Filename of the webhook TLS private key inside `webhook.certDir` or the cert-manager-mounted Secret. Override only when the issuer or external Secret writes a non-default key name. Default `tls.key` matches cert-manager and standard Secret-as-volume conventions. |
| webhook.certManager.enabled | bool | `true` | Use cert-manager to issue and rotate webhook TLS certs. Recommended for HA (replicas > 1) — controller-runtime's in-memory self-signed fallback regenerates per pod, breaking webhook calls under HA. Requires cert-manager to be installed in the cluster. |
| webhook.certManager.issuerRef | object | `{"kind":"ClusterIssuer","name":"selfsigned-issuer"}` | Reference to an existing cert-manager `Issuer` or `ClusterIssuer`. Create the issuer before enabling. |
| webhook.certName | string | `"tls.crt"` | Filename of the webhook TLS certificate inside `webhook.certDir` or the cert-manager-mounted Secret. Override only when the issuer or external Secret writes a non-default key name. Default `tls.crt` matches cert-manager and standard Secret-as-volume conventions. |
| webhook.certSecretName | string | `""` | Name of a pre-existing Secret containing `tls.crt` and `tls.key` to mount at `webhook.certDir`. Required when `webhook.certDir` is non-empty and `webhook.certManager.enabled` is false. The Secret must already exist in the release namespace; the chart does not create it. |
| webhook.port | int | `9443` | Port the webhook server listens on inside the pod. Wired into both the `--webhook-port` flag and the Service `targetPort`. |

## Upgrading

```bash
helm upgrade fleet-management-operator . \
  --namespace fleet-management-system \
  -f values-prod.yaml
```

See [Upgrade Guide](../../docs/upgrade.md) for CRD, webhook, and rollback
guidance.

## Uninstalling

```bash
helm uninstall fleet-management-operator --namespace fleet-management-system
```

This will NOT delete the CRDs. To delete CRDs:

```bash
kubectl delete crd pipelines.fleetmanagement.grafana.com \
                   pipelinediscoveries.fleetmanagement.grafana.com \
                   collectors.fleetmanagement.grafana.com \
                   collectordiscoveries.fleetmanagement.grafana.com \
                   externalattributesyncs.fleetmanagement.grafana.com \
                   remoteattributepolicies.fleetmanagement.grafana.com \
                   tenantpolicies.fleetmanagement.grafana.com
```

### CRD lifecycle (Helm 3)

This chart relies on Helm 3 conventions for CRD installation:

- On `helm install`, every YAML file in the chart's `crds/` directory is applied
  to the cluster exactly once.
- Helm 3 does **not** re-apply or update CRDs on subsequent `helm upgrade`. To
  roll out CRD schema changes, run
  `kubectl apply -f charts/fleet-management-operator/crds/` manually before
  upgrading the release.
- Helm 3 does **not** delete CRDs on `helm uninstall`. This is intentional — it
  preserves any user-created custom resources across reinstalls. Use the
  `kubectl delete crd ...` command shown above to remove them by hand.

## Examples

The CR examples below are meant for application or tenant namespaces, not the
operator release namespace. Apply namespaced CRs with `kubectl -n <namespace>`;
`TenantPolicy` is cluster-scoped. The chart uses a `ClusterRole` for the
manager so reconcilers can watch namespaced Fleet CRs across namespaces and, for
discovery, optionally create child CRs in `spec.targetNamespace`.

### Pipeline

```yaml
apiVersion: fleetmanagement.grafana.com/v1alpha1
kind: Pipeline
metadata:
  name: prometheus-monitoring
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
```

### Collector remote attributes

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

### Bulk attribute defaults via a Policy

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

### Sync attributes from an external CMDB

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

**Precedence (high to low):** ExternalAttributeSync → Collector spec → RemoteAttributePolicy.
The Collector controller is the sole writer to Fleet for collector remote
attributes; the other controllers maintain status that the Collector reads on
each reconcile.

### Auto-discover Collectors from Fleet Management

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
spec:
  pollInterval: 5m
  selector:
    matchers:
      - "collector.os=linux"
      - "env=prod"
  # Defaults to this CollectorDiscovery's namespace. Set only when
  # intentionally mirroring into another namespace.
  # targetNamespace: fleet-mirror
  policy:
    onCollectorRemoved: Keep
```

The reconciler periodically calls Fleet's `ListCollectors` and ensures one
`Collector` CR exists per matching collector. Each mirrored CR carries a
`fleetmanagement.grafana.com/discovery-name` label and a
`fleetmanagement.grafana.com/discovered-by` annotation so users can filter and
audit.

Discovery only writes `spec.id` at creation time — users add `spec.remoteAttributes`
(and the Collector reconciler propagates them to Fleet). Manual edits to
discovered CRs survive subsequent polls.

Deleting a `CollectorDiscovery` does NOT cascade-delete its mirrored CRs
(orphan-on-delete, to preserve user-added attributes). To remove all mirrored
CRs of a discovery:

```bash
kubectl delete collector -l fleetmanagement.grafana.com/discovery-name=prod-linux -n <namespace>
```

**Pagination caveat:** the Fleet Management SDK's `ListCollectors` does not
currently expose pagination. For fleets with more than ~1000 collectors a single
response may be truncated server-side. Shard via multiple `CollectorDiscovery`
resources with disjoint selectors as a workaround until SDK pagination lands.

## High Availability

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

> **Note.** `podDisruptionBudget.enabled: true` at `replicaCount: 1` blocks node
> drains. Only enable PDB when running multiple replicas.

## Troubleshooting

The release name varies — substitute `<release>` with whatever you passed to
`helm install`. The default release name is `fleet-management-operator`.

### Check operator status

```bash
kubectl get pods -n fleet-management-system -l app.kubernetes.io/name=fleet-management-operator
kubectl logs -n fleet-management-system -l app.kubernetes.io/name=fleet-management-operator
```

### Verify CRD installation

```bash
kubectl get crds | grep fleetmanagement.grafana.com
kubectl explain pipeline.spec
```

### Inspect Pipeline / Collector status

```bash
kubectl get pipelines -A
kubectl describe pipeline <name> -n <namespace>
kubectl get events --field-selector involvedObject.kind=Pipeline -n <namespace>
```

### Verify webhook reachability

```bash
kubectl get validatingwebhookconfiguration <release>-validating-webhook -o yaml
kubectl get service -n fleet-management-system -l app.kubernetes.io/name=fleet-management-operator
```

For deeper troubleshooting, see [docs/troubleshooting.md](../../docs/troubleshooting.md)
and the per-alert runbooks in [docs/runbooks/](../../docs/runbooks/).

