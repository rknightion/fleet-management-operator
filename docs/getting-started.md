---
title: Getting Started
description: Install fleet-management-operator with Helm, apply a minimal Pipeline, and verify it synced to Grafana Cloud.
---

# Getting Started

## Prerequisites

- A Kubernetes cluster, `kubectl`, and Helm 3.
- **cert-manager**, installed in the cluster. The chart's default webhook TLS strategy
  (`webhook.certManager.enabled: true`) requires it — see
  [Webhook TLS Setup](webhook-setup.md) for the alternatives if you cannot run cert-manager.
- **Grafana Cloud Fleet Management credentials.** From your Grafana Cloud stack, go to
  **Connections → Collector → Fleet Management → API tab** and note:
    - The base URL, shaped like
      `https://fleet-management-<cluster>.grafana.net/pipeline.v1.PipelineService/`.
    - Your Stack ID (used as the HTTP Basic auth username).
    - An API token (used as the HTTP Basic auth password).

## Install the chart

The chart ships CRDs, RBAC, the webhook `Certificate`, and the manager `Deployment`. Create a
self-signed `ClusterIssuer` first if you do not already have one cert-manager can use:

```bash
kubectl apply -f - <<'EOF'
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: selfsigned-issuer
spec:
  selfSigned: {}
EOF
```

Then install the operator from the published OCI chart, supplying credentials directly:

```bash
helm install fleet-management-operator \
  oci://ghcr.io/rknightion/charts/fleet-management-operator \
  --namespace fleet-management-system \
  --create-namespace \
  --set fleetManagement.baseUrl='https://fleet-management-<CLUSTER>.grafana.net/pipeline.v1.PipelineService/' \
  --set fleetManagement.username='<STACK_ID>' \
  --set fleetManagement.password='<API_TOKEN>'
```

Prefer an `existingSecret` over inline `--set` values for anything beyond a quick test — see
`fleetManagement.existingSecret` in the chart's `values.yaml`. If your `ClusterIssuer` has a
different name, override it with `--set webhook.certManager.issuerRef.name=<name>`.

Only the `Pipeline` controller is enabled out of the box
(`controllers.pipeline.enabled: true`); every other controller — `Collector`,
`CollectorDiscovery`, `RemoteAttributePolicy`, `ExternalAttributeSync`, `TenantPolicy` — is
opt-in. See [Manager Flags](flags.md) for the full `controllers.*` map and what each one costs
in RBAC.

Confirm the rollout and webhook are ready:

```bash
kubectl rollout status deploy/fleet-management-operator -n fleet-management-system
kubectl get validatingwebhookconfiguration | grep fleet-management-operator
```

## Apply a minimal Pipeline

This Alloy pipeline scrapes its own self-metrics and remote-writes them, scoped to Linux
collectors in production. It is the smallest realistic example from
[Sample CRs](samples.md#pipeline-alloy-pipeline-sample):

```yaml
apiVersion: fleetmanagement.grafana.com/v1alpha1
kind: Pipeline
metadata:
  name: alloy-pipeline-sample
spec:
  # Fleet Management pipeline name. Optional: defaults to metadata.name.
  name: pipeline_sample

  contents: |
    prometheus.exporter.self "alloy" { }

    prometheus.scrape "alloy" {
      targets    = prometheus.exporter.self.alloy.targets
      forward_to = [prometheus.remote_write.grafanacloud.receiver]
      scrape_interval = "60s"
    }

    prometheus.remote_write "grafanacloud" {
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

```bash
kubectl apply -n <namespace> -f pipeline.yaml
```

`configType` must match the configuration syntax: `Alloy` for the River-like syntax above, or
`OpenTelemetryCollector` for a YAML receivers/processors/exporters document — the admission
webhook rejects a mismatch before it reaches Fleet Management. See
[Sample CRs](samples.md) for a working `OpenTelemetryCollector` example and every other CRD.

## Verify it synced

```bash
kubectl get pipeline alloy-pipeline-sample -n <namespace>
kubectl describe pipeline alloy-pipeline-sample -n <namespace>
```

A synced pipeline shows both conditions `True`:

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
    reason: Synced
```

`Ready=False` means something needs attention — check the condition's `reason` against
[conditions.md](conditions.md) and, if it doesn't resolve on its own, the matching entry in
[Troubleshooting](troubleshooting.md).

Collectors matching the pipeline's selector pick up the new configuration on their next poll
of Fleet Management (every 5 minutes by default on the collector side) — the operator itself
does not push to collectors directly.

## Next steps

- [Sample CRs](samples.md) — every CRD, with a working example.
- [Manager Flags](flags.md) — enable the other controllers and tune concurrency/rate limits.
- [Security Model and Hardening](security.md) — read before delegating CR creation to
  non-admins or enabling discovery controllers.
- [Architecture](architecture.md) — how the reconcile loop, webhook, and Fleet API client fit
  together.
