# High Reconcile Error Rate Runbook

**Alert:** `FleetReconcileErrorRateHigh`
**Severity:** warning
**Condition:** Reconcile errors > 0.1/s for 10m on any controller.

## Verification

```bash
# Identify the failing controller from the alert labels
# Grafana query:
#   rate(controller_runtime_reconcile_errors_total[5m]) by (controller)

# Check events for the controller's CRs
kubectl get events -n <namespace> --sort-by=.metadata.creationTimestamp | \
  grep -E "SyncFailed|ValidationError|DeleteFailed|SourceFailed|ListCollectorsFailed"

# Find CRs in a non-Ready state
kubectl get pipelines -A -o json | \
  jq '.items[] | select(.status.conditions[]? | select(.type=="Ready" and .status=="False")) | .metadata.name'

kubectl get collectors -A -o json | \
  jq '.items[] | select(.status.conditions[]? | select(.type=="Ready" and .status=="False")) | .metadata.name'

# Check conditions on a specific CR
kubectl get pipeline <name> -n <namespace> \
  -o jsonpath='{range .status.conditions[*]}{.type}{"\t"}{.reason}{"\t"}{.message}{"\n"}{end}'
```

## Causes by Error Type

### SyncFailed -- Fleet API connectivity

Fleet API calls are failing (network error, 5xx, rate-limit 429). All CRs managed by the
affected controller will have condition reason `SyncFailed`.

```bash
# Check for API errors
kubectl logs -n <namespace> -l app.kubernetes.io/name=fleet-management-operator | \
  grep SyncFailed | tail -20

# Verify Fleet API reachability
BASE_URL=$(kubectl get secret fleet-management-operator-credentials -n <namespace> \
  -o jsonpath='{.data.base-url}' | base64 -d)
USER=$(kubectl get secret fleet-management-operator-credentials -n <namespace> \
  -o jsonpath='{.data.username}' | base64 -d)
PASS=$(kubectl get secret fleet-management-operator-credentials -n <namespace> \
  -o jsonpath='{.data.password}' | base64 -d)
curl -u "${USER}:${PASS}" "${BASE_URL}"
```

If Fleet API is down: wait for recovery. The operator retries with exponential backoff.
If 429s: see `docs/runbooks/rate-limit-saturation.md`.

### ValidationError -- spec changed to invalid state

A spec update bypassed webhook validation (e.g. schema incompatibility after an operator
upgrade or a direct etcd write). The CR must be edited to a valid state.

```bash
# Find the failing field in the condition message
kubectl get pipeline <name> -n <namespace> \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}'
```

Fix the spec and re-apply. The webhook will validate the corrected spec before admission.

### DeleteFailed -- deletion error (not 404)

Fleet API returned an error other than 404 during deletion. Usually transient; resolves
when Fleet API recovers. If persistent after >30m, see `docs/runbooks/finalizer-stuck.md`.

### SourceFailed -- ExternalAttributeSync source unreachable

The HTTP or SQL source configured in `spec.source` is returning errors.

```bash
kubectl logs -n <namespace> -l app.kubernetes.io/name=fleet-management-operator | \
  grep SourceFailed | tail -20

# Check which EAS CRs are failing
kubectl get externalattributesync -A -o json | \
  jq '.items[] | select(.status.conditions[]? | select(.type=="Ready" and .status=="False")) | "\(.metadata.namespace)/\(.metadata.name)"'
```

Verify source credentials (Secret keys `bearer-token`, `username`/`password`, or `dsn`)
and source connectivity from within the cluster.

### ListCollectorsFailed -- CollectorDiscovery Fleet API error

Fleet's ListCollectors call is failing. Check Fleet API connectivity (same as SyncFailed above)
and verify the CollectorDiscovery's `spec.selector` matchers are valid.

### InvalidSchedule -- ExternalAttributeSync bad schedule

Condition reason `InvalidSchedule` means `spec.schedule` failed both `time.ParseDuration` and
5-field cron parsing. Correct the schedule field.

Valid formats:
- Duration: `5m`, `1h`, `30s` (must be >= 1m for CollectorDiscovery)
- Cron: `*/5 * * * *` (minute hour dom month dow, no seconds field)

### Sustained errors on a single controller

If errors concentrate on one controller:

```bash
# Check which controller is producing errors
kubectl logs -n <namespace> -l app.kubernetes.io/name=fleet-management-operator | \
  grep -E "controller=|Reconciler error" | sort | uniq -c | sort -rn | head -20
```

A single CRD with repeated validation errors can dominate the error rate metric. Find the
specific failing CR and fix its spec, or delete it if it is no longer needed.

## Recovery Checklist

1. Identify the controller from the alert label (`controller=pipeline`, `controller=collector`, etc.).
2. Find the specific failing CRs using the commands above.
3. Determine error type from condition reason and log messages.
4. Apply the relevant fix (Fleet API connectivity, spec correction, source fix).
5. Confirm the error rate drops: `rate(controller_runtime_reconcile_errors_total[5m])` should
   return to zero within one reconcile interval after the fix.
