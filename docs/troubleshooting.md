# Troubleshooting Guide

Cross-reference with `docs/runbooks/` for per-alert runbooks and `docs/conditions.md`
for the full condition type/reason registry.

## Quick Diagnosis

Start here for any issue:

```bash
# Check operator pod status
kubectl get pods -n <namespace> -l app.kubernetes.io/name=fleet-management-operator

# Check recent events
kubectl get events -n <namespace> --sort-by=.metadata.creationTimestamp | tail -20

# Check operator logs
kubectl logs -n <namespace> -l app.kubernetes.io/name=fleet-management-operator --tail=100

# Check a specific CR
kubectl describe pipeline <name> -n <namespace>
kubectl describe collector <name> -n <namespace>
```

## Pipeline Not Syncing

**Symptom:** Pipeline CR exists but `Ready=False` or `Synced=False` in `status.conditions`.

1. Check the condition reason: `kubectl get pipeline <name> -o jsonpath='{.status.conditions}'`
2. Reason `SyncFailed`: Fleet API is unreachable or returning 5xx. Check `FLEET_MANAGEMENT_BASE_URL`
   and credentials. Run `kubectl logs ... | grep SyncFailed`.
3. Reason `ValidationError`: The pipeline spec failed validation. Check the condition message for
   the specific field that failed.
4. Reason `Deleting`: The CR has a DeletionTimestamp and the finalizer is running. If stuck >5m,
   see "Finalizer Stuck on Delete" below.
5. Cache lag: The controller uses `status.observedGeneration` to skip unchanged specs. If the
   pipeline synced at a previous generation and the generation has not changed, the controller
   will not re-sync. Force a reconcile by making a no-op annotation change.

## Rate-Limit Saturation

**Symptom:** SyncFailed events with "rate limited" messages; reconcile queue growing.

Distinguish rate-limiter saturation from Fleet API outage:
- **Rate-limiter queue** (operator-side): `fleet_api_rate_limiter_wait_duration_seconds_p95 > 0.5s`
  means requests are queueing. The operator is healthy but throttled.
- **Fleet API errors** (server-side): `fleet_api_errors_total{status="resource_exhausted"}` rising
  means the server is returning 429. The rate-limit is misconfigured above the server setting.
- **Fleet API outage**: `fleet_api_errors_total{status!="ok"}` rising across all operations.

**Mitigation:**
1. Check `--fleet-api-rps` matches your Fleet Management server-side `api:` setting (default 3).
2. If sustained 429s: reduce `--fleet-api-rps` or raise the server-side limit.
3. If queue depth growing but no 429s: a burst of reconciles (e.g. after restart) is clearing.
   The workqueue will drain; no action needed if `workqueue_depth` is decreasing.
4. Sharding: if CollectorDiscovery is running a broad selector on >1000 collectors, each
   ListCollectors response can be 30MB+. Shard via multiple CRs with disjoint matchers.

## Webhook Rejection at Enrollment

**Symptom:** `kubectl apply -f pipeline.yaml` returns an admission error.

1. Validation error: Check the error message — it includes the specific field and reason.
   Common causes: `==` instead of `=` in matchers; missing `service:` in OTEL config;
   configType mismatch with config syntax.
2. Webhook unreachable: `x509: certificate signed by unknown authority` or connection refused.
   - Check webhook service: `kubectl get svc -n <namespace> | grep webhook`
   - Check cert: `kubectl get secret <name>-webhook-certs -n <namespace> -o yaml | grep tls.crt`
   - See "Webhook Certificate Expiry" below for cert rotation.
3. Namespace bypass: if the webhook has a namespaceSelector, requests from excluded namespaces
   are silently allowed. Check the VWC: `kubectl describe validatingwebhookconfiguration`.

## Finalizer Stuck on Delete

**Symptom:** `kubectl delete pipeline <name>` hangs; CR shows `deletionTimestamp` but is not removed.

1. Check the finalizer is present: `kubectl get pipeline <name> -o jsonpath='{.metadata.finalizers}'`
2. Check logs for the deletion error: `kubectl logs ... | grep -i "finalizer\|delete\|DeleteFailed"`
3. If Fleet API is permanently unavailable and you must force-delete:
   - Verify the Fleet resource no longer exists in Fleet Management (out-of-band check)
   - Patch the finalizer: `kubectl patch pipeline <name> -p '{"metadata":{"finalizers":[]}}' --type=merge`
   - **WARNING:** Only do this if you have confirmed the Fleet resource state. If the Fleet resource
     still exists, it will become unmanaged (orphaned).
4. `DeleteFailed` reason with non-404 errors: Check Fleet API connectivity and credentials.

## Informer Cache Rebuild on Restart

**Symptom:** After pod restart, reconciles are slow for 1-5 minutes; liveness probe may fail.

At 30k Collectors, the initial cache warm-up pulls all CR objects from the K8s API server.
This can take 20-45s. A liveness probe with `initialDelaySeconds: 15` (old default) will kill
the pod before warm-up completes, causing a crash loop.

**Fix:**
1. Ensure `healthProbe.liveness.initialDelaySeconds: 45` (the current default after HELM-08 fix).
2. Check the `resources.limits.memory` — the chart default is 2Gi (raised by HELM-01).
   If the pod is OOMKilled during warm-up at very large fleets (>30k Collectors), consider
   raising further. See the sizing guide in values.yaml.

## Per-Controller Failure Modes

### CollectorDiscovery

**Collision / SanitizedName hash suffix**
Fleet collector IDs may not be valid K8s DNS-1123 names. The controller sanitizes them
(lowercase + replace invalid chars). If two IDs sanitize to the same string, a 5-char SHA-256
suffix is appended. Check `status.conflicts` for IDs that could not be mirrored. If
`status.conditions[TruncatedConflicts]=True`, the conflicts list is capped at 100; check
Kubernetes Events for the full list.

**Stale collectors (onCollectorRemoved: Keep)**
When a collector disappears from Fleet Management, the Collector CR is kept with annotation
`fleetmanagement.grafana.com/discovery-stale=true`. These appear in `status.staleCollectors`.
Clean up manually: `kubectl delete collector -l fleetmanagement.grafana.com/discovery-stale=true`
or switch to `spec.policy.onCollectorRemoved: Delete` for automatic cleanup.

### ExternalAttributeSync

**Stalled (empty result guard)**
Condition `Stalled=True` with reason `Stalled` means the source returned 0 records and
`spec.allowEmptyResults=false`. The previous ownedKeys claim is preserved.
- Is the empty result legitimate? Set `spec.allowEmptyResults: true`.
- Is the source down? Check source connectivity: `kubectl logs ... | grep SourceFailed`.

**ownedKeys Truncated**
Condition `Truncated=True` means the source returned more than 1000 collector entries.
Attributes for collectors beyond the cap may not be cleaned up on CR deletion.
Shard the source by creating multiple ExternalAttributeSync CRs with disjoint selectors.

### RemoteAttributePolicy

**No matchers (NoMatch)**
`Ready=False` with reason `NoMatch` means the selector matched 0 collectors. This is not an
error (Synced=True); it means no collectors currently satisfy the matchers. Check the matcher
syntax and verify collectors exist with the expected attributes.

**matchedCollectorIDs Truncated**
Condition `Truncated=True` means >1000 collectors matched. `status.matchedCount` has the real
count; `status.matchedCollectorIDs` is a sample of the first 1000 for debugging.

## etcd Bloat from Large Status

**Symptom:** etcd shows large objects for RemoteAttributePolicy or ExternalAttributeSync CRs;
slow reads; `kubectl get` times out.

Check truncation conditions: a CR with thousands of matched collectors or owned keys will have
large status. With the current cap (1000), this is bounded.
- `RemoteAttributePolicy` status is bounded to 1000 matchedCollectorIDs + one int32 matchedCount.
- `ExternalAttributeSync` status is bounded to 1000 ownedKeys entries.
- If pre-cap versions are in place, upgrade to get the cap applied.

## Webhook Certificate Expiry

**Symptom:** `x509: certificate has expired` in operator logs; all Pipeline/Collector creates
and updates are rejected.

**Self-signed mode (default):** The cert regenerates on pod restart.

```bash
kubectl rollout restart deployment -n <namespace> fleet-management-operator
```

Note: self-signed is not HA-safe. The caBundle in the VWC becomes stale until the next restart.
Migrate to cert-manager for production. See `docs/webhook-setup.md`.

**cert-manager mode:** Check whether cert renewal failed.

```bash
kubectl describe certificate -n <namespace> <release-name>-webhook
# Look for renewal errors in Status.Conditions
kubectl delete certificate -n <namespace> <release-name>-webhook  # triggers re-issue
```

**Manual cert mode:** Rotate the Secret and restart.

```bash
kubectl create secret tls <name>-webhook-certs \
  --cert=new-tls.crt --key=new-tls.key \
  -n <namespace> --dry-run=client -o yaml | kubectl apply -f -
kubectl rollout restart deployment -n <namespace> fleet-management-operator
```

## TenantPolicy Enforcement Rejections

**Symptom:** Pipeline create/update rejected with "required matcher not present in matcher set".

This occurs when `--enable-tenant-policy-enforcement` is set and the requesting subject is bound
by a TenantPolicy that requires at least one matcher that is absent from the Pipeline spec.

1. Identify which TenantPolicy matches the requesting subject:
   `kubectl get tenantpolicy -o yaml | grep -A10 subjects`
2. Add the required matcher to the Pipeline spec, or request a policy exemption.
3. If the TenantPolicy itself is misconfigured, check its `Ready` condition:
   `kubectl get tenantpolicy <name> -o jsonpath='{.status.conditions}'`
   Reason `ParseError` means the policy spec itself has a malformed matcher or namespace selector.

## Collector NotRegistered

**Symptom:** Collector CR is `Ready=False` with reason `NotRegistered`.

The Collector CR's `spec.id` points to a Fleet Management collector ID that has not yet appeared
in Fleet Management. This is expected if:
- The collector is newly registered and Fleet Management has not yet processed it.
- The `spec.id` has a typo or case mismatch (Fleet IDs are case-sensitive).

The controller requeues automatically. If the reason persists after 10 minutes, verify the
collector ID against Fleet Management directly.
