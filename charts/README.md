# Helm Charts

This directory contains the Helm chart for the Fleet Management Operator.

## Published location

The chart is published as an OCI artifact to GHCR by CI. There is no
`helm repo add` index and no GitHub Pages site:

```
oci://ghcr.io/rknightion/charts/fleet-management-operator
```

Install a released version:

```bash
helm install fleet-management-operator \
  oci://ghcr.io/rknightion/charts/fleet-management-operator \
  --version 1.0.0 \
  --namespace fleet-management-system \
  --create-namespace \
  --set fleetManagement.baseUrl='https://fleet-management-prod-us-central-0.grafana.net/pipeline.v1.PipelineService/' \
  --set fleetManagement.username='YOUR_STACK_ID' \
  --set fleetManagement.password='YOUR_TOKEN'
```

Inspect what is available:

```bash
helm show chart oci://ghcr.io/rknightion/charts/fleet-management-operator --version 1.0.0
```

## How publishing works

`.github/workflows/release-please.yaml` runs on every push to `main`.

- Conventional-commit history produces a release PR. Merging it tags `vX.Y.Z`
  and creates the GitHub release, which sets `release_created`, which calls
  `.github/workflows/publish.yml` with the tag.
- `publish.yml` delegates to the shared `rknightion/.github`
  `container-publish.yml` reusable workflow, which packages the chart with
  `--version X.Y.Z --app-version X.Y.Z` taken from the tag, pushes it to
  `oci://ghcr.io/rknightion/charts`, cosign-signs it, and attaches the `.tgz`
  to the GitHub release.
- Pushes to `main` that do *not* create a release publish a snapshot chart
  versioned `0.0.0-main.t<epoch>.g<sha>` instead. Those are throwaway builds —
  always install with an explicit `--version`.

The `version` and `appVersion` in `Chart.yaml` carry
`# x-release-please-version` markers so release-please keeps them in step with
the operator release. They are overridden again at package time, so they only
matter when installing straight from a checkout.

## Local development

Install from the local chart:

```bash
cd charts/fleet-management-operator
helm install fleet-management-operator . \
  --namespace fleet-management-system \
  --create-namespace \
  -f values-example.yaml
```

Test rendering:

```bash
helm template test . \
  --set fleetManagement.baseUrl=https://test \
  --set fleetManagement.username=test \
  --set fleetManagement.password=test
```

Lint the chart:

```bash
helm lint . \
  --set fleetManagement.baseUrl=https://test \
  --set fleetManagement.username=test \
  --set fleetManagement.password=test
```
