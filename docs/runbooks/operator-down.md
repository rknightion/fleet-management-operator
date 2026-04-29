# FleetOperatorDown Runbook

**Alert:** `FleetOperatorDown`
**Severity:** critical
**Condition:** No fleet-management-operator instances healthy for 5 minutes.

## Verification

```bash
# Check pod status
kubectl get pods -n <namespace> -l app.kubernetes.io/name=fleet-management-operator -o wide

# Check pod events
kubectl describe pod -n <namespace> -l app.kubernetes.io/name=fleet-management-operator

# Check recent logs (if pod is running but unhealthy)
kubectl logs -n <namespace> -l app.kubernetes.io/name=fleet-management-operator --tail=50
```

## Causes and Mitigations

### OOMKilled

**Signal:** `kubectl describe pod` shows `OOMKilled` in Last State Reason.

At 30k Collectors, informer-cache footprint is approximately 150MB + Pipeline cache + Go runtime.
Default limit of 2Gi should be sufficient; lower limits (pre-HELM-01 fix: 128Mi) will OOMKill.

**Fix:**

```bash
# Increase memory limit via Helm upgrade (rarely needed above the 2Gi default)
helm upgrade fleet-management-operator charts/fleet-management-operator \
  --set resources.limits.memory=4Gi \
  --set resources.requests.memory=1Gi \
  --reuse-values
```

### CrashLoopBackOff -- Missing Credentials

**Signal:** Log line `FLEET_MANAGEMENT_BASE_URL environment variable is required`.

```bash
# Verify the credentials Secret exists and has the expected keys
# (chart names this <release>-credentials; default release name = fleet-management-operator)
kubectl get secret fleet-management-operator-credentials -n <namespace> -o yaml | \
  grep -E "base-url|username|password"

# If missing, create it
kubectl create secret generic fleet-management-operator-credentials \
  --from-literal=base-url=https://fleet-management-<cluster>.grafana.net/... \
  --from-literal=username=<stack-id> \
  --from-literal=password=<api-token> \
  -n <namespace>
```

### CrashLoopBackOff -- Startup Validation Failure

**Signal:** Log contains `no controllers enabled` or `discovery requires collector`.

Check that at least one controller flag is enabled in values.yaml
(`controllers.pipeline.enabled: true`). If `controllers.collectorDiscovery.enabled: true`,
also ensure `controllers.collector.enabled: true` — the manager refuses to start with
discovery enabled but the Collector controller disabled.

### ImagePullBackOff

Check `imagePullSecrets` is configured and the registry is accessible.
For digest-pinned images, verify the digest is still available in the registry.

```bash
kubectl get pod -n <namespace> -l app.kubernetes.io/name=fleet-management-operator \
  -o jsonpath='{.items[0].status.containerStatuses[0].state}'
```

### Liveness Probe Failing -- Slow Startup

At 30k CRs, initial cache warm-up can take 20-45s. If `initialDelaySeconds < 45`, the pod
may be killed before it finishes warming up, causing a crash loop.

```bash
helm upgrade fleet-management-operator charts/fleet-management-operator \
  --set healthProbe.liveness.initialDelaySeconds=60 \
  --reuse-values
```

### Leader Election Contention

**Signal:** Multiple pods running; logs show repeated `failed to acquire leader lease`.

With `--leader-elect` set, only the leader runs reconciles. The non-leader replicas wait
for the lease and take over on pod failure. Contention is normal during a rolling restart.
If the lease is permanently stuck:

```bash
# Check the lease object
kubectl get lease -n <namespace> fleet-management-operator

# If the holder pod no longer exists, delete the lease to force re-election
kubectl delete lease -n <namespace> fleet-management-operator
```

**WARNING:** Deleting the lease during active reconciliation can cause a brief gap where
no reconciles are running. Existing CRs continue to function; only new changes are delayed.

## Impact While Down

- No new Pipeline, Collector, or attribute-sync changes are pushed to Fleet Management.
- Collectors continue polling Fleet Management every 5 minutes; already-synced configs
  continue to work.
- Webhook server is also down: new Pipeline/Collector creates and updates will be rejected
  (failurePolicy: Fail). Existing CRs are unaffected.
- Finalizers on CRs pending deletion will not be processed until the operator restarts.

## Escalation

If none of the above resolves the issue within 15 minutes, escalate to the
platform-observability team. Provide: pod describe output, last 200 log lines, and Fleet
Management API reachability status:

```bash
curl -u <username>:<password> <base-url>
```
