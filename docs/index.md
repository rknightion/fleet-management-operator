---
title: fleet-management-operator — Kubernetes operator for Grafana Cloud Fleet Management
description: Manage Grafana Cloud Fleet Management pipelines, collectors, and attribute policies as native Kubernetes custom resources.
---

# fleet-management-operator

**Manage Grafana Cloud Fleet Management as Kubernetes custom resources.** Fleet Management
distributes Alloy or OpenTelemetry Collector configuration fragments — "pipelines" — to fleets
of collectors, matching them by Prometheus Alertmanager-style label selectors
(`collector.os=linux`, `env!=development`). Fleet Management's own console and Terraform
provider are the usual way to author pipelines; this operator lets you do it with `kubectl
apply` instead, so pipeline definitions live in Git next to the rest of your cluster
configuration and go through the same review and GitOps flow.

> This is a community project, not an official Grafana Labs product. See the disclaimer in the
> [GitHub README](https://github.com/rknightion/fleet-management-operator).

## What it manages

Seven CRDs in the `fleetmanagement.grafana.com/v1alpha1` API group, each mapping to a Fleet
Management concept:

| CRD | What it does |
|---|---|
| [`Pipeline`](api-reference.md#pipeline) | Pushes an Alloy or OpenTelemetry Collector config fragment to Fleet Management, scoped to matching collectors. |
| [`PipelineDiscovery`](api-reference.md#pipelinediscovery) | Imports existing Fleet Management pipelines as read-only (or adoptable) `Pipeline` CRs. |
| [`Collector`](api-reference.md#collector) | Manages remote attributes for one specific, already-registered collector by ID. |
| [`CollectorDiscovery`](api-reference.md#collectordiscovery) | Mirrors Fleet Management's registered collectors into `Collector` CRs automatically. |
| [`RemoteAttributePolicy`](api-reference.md#remoteattributepolicy) | Assigns default remote attributes to every collector matching a selector. |
| [`ExternalAttributeSync`](api-reference.md#externalattributesync) | Pulls collector attributes from an external HTTP or SQL source (a CMDB, for example) on a schedule. |
| [`TenantPolicy`](api-reference.md#tenantpolicy) | Cluster-scoped RBAC guardrail: requires specific matchers on CRs authored by a given Kubernetes subject. |

Every CRD except `TenantPolicy` is namespaced. `Pipeline` is the only controller enabled by
default — everything else is opt-in per the [flags reference](flags.md).

## Why a Kubernetes operator for this

Fleet Management's API has no concept of Kubernetes RBAC: anyone holding the Cloud access
token has full read/write over every pipeline and collector in the stack. Routing all changes
through this operator's CRDs turns Kubernetes admission into the enforcement point instead —
standard RBAC decides who can create a `Pipeline` in which namespace, and the opt-in
[`TenantPolicy`](tenant-policy.md) CRD adds a further "which collectors can this subject
target" layer on top. The trade-off is that the operator itself becomes a single, powerful
credential holder; read [Security Model and Hardening](security.md) before delegating CR
creation to non-admins.

## How it works, briefly

The controller manager runs one reconciler per enabled CRD. Each reconciler is driven by
Kubernetes watch events (no polling of the API server), calls the Fleet Management HTTP API
through a shared rate-limited client (default 3 requests/second, matching Fleet Management's
own server-side limit), and writes the result back as `status.conditions` and Kubernetes
events. A validating admission webhook enforces spec-shape rules — matcher syntax, config
type/contents agreement, reserved attribute-key prefixes — before anything reaches the Fleet
API. See [Architecture](architecture.md) for the full reconcile-loop and webhook picture.

## Current status

All CRDs ship at `v1alpha1`, the only served and stored version — see
[API Versioning and Graduation Policy](versioning.md) for the graduation criteria and known
`v1` blockers. The project has tagged releases (images at
`ghcr.io/rknightion/fleet-management-operator`, Helm chart at
`oci://ghcr.io/rknightion/charts/fleet-management-operator`) but the API and defaults can still
change within `v1alpha1` per that document's compatibility rules.

## Start here

- [Getting Started](getting-started.md) — install the chart, get credentials, apply your first
  `Pipeline`.
- [Sample CRs](samples.md) — a runnable example of every CRD.
- [Manager Flags](flags.md) — every CLI flag, its default, and the matching Helm value.
- [Security Model and Hardening](security.md) — read this before delegating CR creation to
  non-admins or enabling the discovery controllers in a multi-tenant cluster.
- [Troubleshooting](troubleshooting.md) and [Runbooks](runbooks/index.md) — diagnosing sync
  failures, webhook rejections, and alert conditions.

The source, issue tracker, and CONTRIBUTING guide live on
[GitHub](https://github.com/rknightion/fleet-management-operator).
