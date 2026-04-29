# Helm Chart: fleet-management-operator

Helm chart for deploying the fleet-management-operator into Kubernetes.

## Commands

```bash
# Lint the chart
helm lint charts/fleet-management-operator

# Template locally (dry-run render)
helm template fleet-management-operator charts/fleet-management-operator \
  --set fleetManagement.baseUrl=https://example.grafana.net/pipeline.v1.PipelineService/ \
  --set fleetManagement.username=123456 \
  --set fleetManagement.password=token

# Regenerate README from README.md.gotmpl (requires helm-docs)
helm-docs --chart-search-root charts/

# Package
helm package charts/fleet-management-operator -d dist/

# Install / upgrade
helm upgrade --install fleet-management-operator charts/fleet-management-operator \
  --set fleetManagement.baseUrl=<URL> \
  --set fleetManagement.username=<stack-id> \
  --set fleetManagement.password=<token>
```

## Directory Structure

```
charts/fleet-management-operator/
  Chart.yaml              # Chart metadata, appVersion mirrors operator semver
  values.yaml             # All configurable values with inline docs
  values-example.yaml     # Annotated real-world example values
  README.md               # Auto-generated; edit README.md.gotmpl instead
  README.md.gotmpl        # helm-docs template — edit this, not README.md
  crds/                   # CRD manifests — Helm installs but does NOT upgrade or delete these
  templates/
    _helpers.tpl          # Named templates (fullname, labels, secretName)
    deployment.yaml       # Manager Deployment — most flag wiring is here
    secret.yaml           # Credentials Secret (only when existingSecret not set)
    clusterrole.yaml      # Aggregated RBAC for all enabled controllers
    validatingwebhookconfiguration.yaml  # Webhook registration
    certificate.yaml      # cert-manager Certificate (when certManager.enabled)
    servicemonitor.yaml   # Prometheus Operator ServiceMonitor (when enabled)
    prometheusrule.yaml   # PrometheusRule alerts (when enabled)
    poddisruptionbudget.yaml  # PDB (only when replicaCount > 1)
    grafana-dashboard-configmap.yaml
```

## Key Design Decisions

**CRD lifecycle:** CRDs live in `crds/` (not `templates/`). Helm installs them once but never upgrades or deletes them — intentional, to preserve user CRs across chart reinstalls. To update CRDs after a chart upgrade, run `kubectl apply -f charts/fleet-management-operator/crds/` manually.

**Controllers are opt-in:** `tenantPolicy` defaults to `false` (changes admission behaviour, must be explicitly opted in). All collector-management controllers (`collector`, `remoteAttributePolicy`, `externalAttributeSync`, `collectorDiscovery`) default to `true` — they are harmless with no CRs. Add a new controller by: adding the `--enable-<name>-controller` flag in `deployment.yaml`, a `controllers.<name>.enabled` value, and the corresponding RBAC in `clusterrole.yaml`.

**Credentials Secret:** When `fleetManagement.existingSecret` is set, the chart skips creating `secret.yaml` and references the named Secret directly. The `secretName` helper in `_helpers.tpl` handles this logic.

**HA cert requirement:** With `replicaCount > 1`, controller-runtime's in-memory self-signed certs break because each pod generates its own cert. Use `webhook.certManager.enabled: true` for HA. The chart auto-injects soft pod anti-affinity when `replicaCount > 1` and `affinity` is empty.

**PDB guard:** The PDB template renders only when `podDisruptionBudget.enabled: true` AND `replicaCount > 1`. A PDB with `minAvailable: 1` at `replicaCount: 1` blocks node drains — the template guard prevents this.

**Mutual exclusions validated at render time:**
- `webhook.certManager.enabled` and `webhook.certDir` are mutually exclusive — `deployment.yaml` calls `fail` if both are set.
- `image.digest` and `@` in `image.repository` are mutually exclusive — same pattern.

## Values Reference (Key Sections)

| Key | Default | Notes |
|-----|---------|-------|
| `fleetManagement.baseUrl` | `""` | Required; format `https://fleet-management-<cluster>.grafana.net/pipeline.v1.PipelineService/` |
| `fleetManagement.existingSecret` | `""` | Pre-existing Secret name; skips chart-managed Secret creation |
| `fleetManagement.apiRatePerSecond` | `3` | Match to Fleet Management server-side `api:` rate limit |
| `fleetManagement.apiRateBurst` | `50` | Token bucket size — do not reduce below 10; burst=1 causes livelock |
| `controllers.pipeline.enabled` | `true` | Core Pipeline reconciler |
| `controllers.collector.enabled` | `true` | Required before enabling `collectorDiscovery` |
| `controllers.collectorDiscovery.enabled` | `true` | Requires `collector.enabled: true` |
| `webhook.certManager.enabled` | `false` | Required for HA (replicaCount > 1) |
| `replicaCount` | `1` | Set > 1 for HA; also enable `podDisruptionBudget` and `webhook.certManager` |
| `resources.limits.memory` | `2Gi` | Sized for 30k Collectors; reduce to 512Mi for small fleets |
| `image.digest` | `""` | Digest-pin for production supply-chain hardening |

## Common Pitfalls

- Enabling `collectorDiscovery` without `collector.enabled: true` causes manager startup failure — the chart does not guard against this.
- Do NOT embed `@sha256:...` in `image.repository` when `image.digest` is set — the chart will fail to render.
- Reducing `apiRateBurst` to 1 causes livelock under restart: requests queue until the 30s HTTP timeout fires, indistinguishable from a Fleet API outage.
- `terminationGracePeriodSeconds` must stay >= 30 (Fleet API HTTP timeout); reducing it risks in-flight API calls being killed mid-request.
- `logging.level: debug` at 30k-collector scale is very verbose — use `info` in production.
