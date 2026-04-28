# Finalizer Stuck Runbook

**Condition:** CR has `deletionTimestamp` but finalizer not removed after >5 minutes.

## Background

The operator adds a finalizer to each managed CR before the first Fleet API call. The finalizer
ensures the external Fleet Management resource is deleted before the Kubernetes object is
removed. Finalizer names:

- Pipeline: `pipeline.fleetmanagement.grafana.com/finalizer`
- Collector: `collector.fleetmanagement.grafana.com/finalizer`

The finalizer is removed only after Fleet cleanup succeeds or returns 404 (already deleted).
If the operator is not running or the Fleet API returns non-404 errors, the finalizer stalls.

## Verification

```bash
# Confirm a finalizer is present
kubectl get pipeline <name> -n <namespace> \
  -o jsonpath='{.metadata.finalizers}{"\n"}'

# Check deletion errors in logs
kubectl logs -n <namespace> -l app.kubernetes.io/name=fleet-management-operator | \
  grep -E "finalizer|DeleteFailed|DeletePipeline|DeleteCollector"

# Check the CR's conditions
kubectl get pipeline <name> -n <namespace> \
  -o jsonpath='{range .status.conditions[*]}{.type}{"\t"}{.reason}{"\t"}{.message}{"\n"}{end}'

# Check events on the CR
kubectl describe pipeline <name> -n <namespace> | grep -A20 Events
```

## Causes

### Operator pod is down

If the operator is not running, finalizers are never processed. Start or restart the operator:

```bash
kubectl rollout restart deployment -n <namespace> fleet-management-operator
kubectl rollout status deployment -n <namespace> fleet-management-operator
```

The pending deletion will complete once the operator is healthy.

### DeleteFailed -- transient Fleet API error

The operator retries deletion with exponential backoff. Condition reason `DeleteFailed` on the
CR indicates the Fleet API returned a non-404 error. This is usually transient (network blip,
Fleet API restart). The operator will retry automatically.

Check Fleet API reachability:

```bash
# Obtain credentials from the Secret
BASE_URL=$(kubectl get secret fleet-management-credentials -n <namespace> \
  -o jsonpath='{.data.base-url}' | base64 -d)
USER=$(kubectl get secret fleet-management-credentials -n <namespace> \
  -o jsonpath='{.data.username}' | base64 -d)
PASS=$(kubectl get secret fleet-management-credentials -n <namespace> \
  -o jsonpath='{.data.password}' | base64 -d)

curl -u "${USER}:${PASS}" "${BASE_URL}"
```

If Fleet API is healthy but the CR is still stuck after 30 minutes, proceed to the emergency
removal below.

### DeleteFailed -- 404 not treated as success (pre-fix operator)

In versions before the 404-as-success fix, a missing Fleet resource could cause a deletion
loop. Upgrade the operator to get the fix applied, then restart.

## Emergency Finalizer Removal

**Only remove a finalizer manually after confirming the Fleet resource state.**

1. Verify the Fleet resource no longer exists (use Fleet Management UI or API out-of-band).
2. If confirmed deleted or never created:

```bash
kubectl patch pipeline <name> -n <namespace> \
  -p '{"metadata":{"finalizers":[]}}' \
  --type=merge
```

For a Collector CR:

```bash
kubectl patch collector <name> -n <namespace> \
  -p '{"metadata":{"finalizers":[]}}' \
  --type=merge
```

**WARNING:** If the Fleet resource still exists after the finalizer is removed, it becomes
orphaned -- unmanaged by this operator. You will need to delete it manually from Fleet
Management. Orphaned resources continue to distribute their configuration to matching
collectors until explicitly removed.

## Bulk Stuck Finalizers

If many CRs are stuck (e.g. after a Fleet API outage during a bulk delete):

```bash
# List all CRs with a deletionTimestamp
kubectl get pipelines -A -o json | \
  jq '.items[] | select(.metadata.deletionTimestamp != null) | .metadata.name'

# After confirming Fleet state, patch all at once (careful: this skips the 404 check)
kubectl get pipelines -A -o json | \
  jq -r '.items[] | select(.metadata.deletionTimestamp != null) | "\(.metadata.namespace) \(.metadata.name)"' | \
  while read ns name; do
    kubectl patch pipeline "${name}" -n "${ns}" \
      -p '{"metadata":{"finalizers":[]}}' --type=merge
  done
```
