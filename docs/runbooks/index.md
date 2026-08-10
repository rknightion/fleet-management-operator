---
title: Runbooks
description: Per-alert operational runbooks for fleet-management-operator, indexed by the Prometheus alert that fires.
---

# Runbooks

Each runbook below is written against a specific alert or failure condition: verification
commands first, then causes and mitigations. See [troubleshooting.md](../troubleshooting.md)
for a symptom-first index instead, and [conditions.md](../conditions.md) for the full
condition type/reason registry these runbooks reference.

| Runbook | Alert / condition |
|---|---|
| [Operator Down](operator-down.md) | `FleetOperatorDown` — no healthy operator instances for 5 minutes |
| [High Reconcile Error Rate](high-reconcile-error-rate.md) | `FleetReconcileErrorRateHigh` — reconcile errors > 0.1/s for 10 minutes |
| [Rate Limit Saturation](rate-limit-saturation.md) | `FleetRateLimitSaturation` — p95 rate-limiter wait > 0.9s for 5 minutes |
| [Webhook Unavailable](webhook-unavailable.md) | `FleetWebhookRejectionRateHigh` — admission webhook returning 500s for 5 minutes |
| [Finalizer Stuck](finalizer-stuck.md) | A CR has `deletionTimestamp` set but its finalizer has not been removed after 5+ minutes |
| [Pipeline Name Scope Migration](pipeline-name-scope-migration.md) | Operational guide for enabling `--pipeline-name-scope=namespace` |

The alert names above match the PrometheusRule shipped by the Helm chart when
`alerts.enabled: true`.
