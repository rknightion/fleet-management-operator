# FleetRateLimitSaturation Runbook

**Alert:** `FleetRateLimitSaturation` (available after OBS-02 metrics are enabled)
**Severity:** warning
**Condition:** 95th-percentile rate-limiter wait > 0.9s sustained for 5m.

## Background

The operator uses a two-tier rate-limiting model:

1. **Workqueue** (controller-runtime): dispatches reconciles to prevent K8s API floods.
2. **Fleet API client** (`--fleet-api-rps`): limits Fleet API calls to match the server-side
   budget. Default is 3 req/s with burst=50. The burst absorbs startup/restart spikes; burst=1
   causes livelock (with a 30s HTTP timeout, request N in a restart wave times out, which is
   indistinguishable from a Fleet API outage).

Saturation occurs when the Fleet API call rate exceeds `--fleet-api-rps`. Reconciles queue at
the limiter, holding goroutines. This is normal during burst events (pod restart, bulk CR apply)
and will clear as the queue drains.

Pipeline and Collector controllers are intentionally kept at `MaxConcurrentReconciles=1` because
they share the Fleet API budget. Increasing concurrency only queues more requests at the limiter
without increasing throughput.

## Verification

```bash
# Check rate-limiter wait metric (requires OBS-02)
# Grafana query:
#   histogram_quantile(0.95, rate(fleet_api_rate_limiter_wait_duration_seconds_bucket[5m]))

# Check for 429 errors returned by the Fleet API
# Grafana query:
#   rate(fleet_api_errors_total{status="resource_exhausted"}[5m])

# Check workqueue depth
# Grafana query:
#   workqueue_depth{name=~"pipeline|collector"}

# Check operator logs for rate-limit events
kubectl logs -n <namespace> -l app.kubernetes.io/name=fleet-management-operator | \
  grep -E "rate limit|RateLimited|resource_exhausted"

# Check Kubernetes events
kubectl get events -n <namespace> --field-selector reason=RateLimited
```

## Causes and Mitigations

### Transient burst (restart or bulk apply)

After a pod restart or a batch of new CRs, a burst of reconciles is dispatched. The burst
token bucket (default 50) absorbs the initial spike; remaining work drains at `--fleet-api-rps`.

No action needed if `workqueue_depth` is monotonically decreasing. Expect the queue to clear
within `queue_depth / rps` seconds (e.g. 200 CRs at 3 req/s = ~67s drain time).

### Sustained saturation -- rps misconfigured above server limit

**Signal:** `fleet_api_errors_total{status="resource_exhausted"}` is rising (429s from server).

The `--fleet-api-rps` value exceeds the Fleet Management server-side `api:` setting. The
server is returning 429s, which count as errors and trigger retries, further amplifying load.

```bash
helm upgrade fleet-management-operator charts/fleet-management-operator \
  --set fleetManagement.apiRatePerSecond=3 \
  --reuse-values
```

Match the value to your Fleet Management server-side `api:` configuration. The default on
both sides is 3 req/s.

### Sustained saturation -- CollectorDiscovery broad selector

A CollectorDiscovery with an empty or very broad `spec.selector` on a large fleet triggers a
large ListCollectors response on every poll interval. In a 30k fleet, a single response can be
~30MB. Each response causes a reconcile fan-out across all mirrored Collector CRs.

```bash
# Check CollectorDiscovery CRs and their selector
kubectl get collectordiscovery -A -o yaml | grep -A5 "selector:"

# Check the size of ListCollectors responses in logs
kubectl logs -n <namespace> -l app.kubernetes.io/name=fleet-management-operator | \
  grep -E "ListCollectors|collector_count"
```

**Fix:** Shard by creating multiple CollectorDiscovery CRs with disjoint matchers.

```yaml
# shard-production.yaml
apiVersion: fleetmanagement.grafana.com/v1alpha1
kind: CollectorDiscovery
metadata:
  name: discovery-production
spec:
  selector:
    matchers:
      - env=production
  pollInterval: 5m
---
# shard-staging.yaml
apiVersion: fleetmanagement.grafana.com/v1alpha1
kind: CollectorDiscovery
metadata:
  name: discovery-staging
spec:
  selector:
    matchers:
      - env=staging
  pollInterval: 5m
```

### Sustained saturation -- ExternalAttributeSync high-frequency schedule

An ExternalAttributeSync with a very short schedule (e.g. `10s`) combined with a large result
set causes frequent Collector reconcile fan-out via watches.

Check schedules:

```bash
kubectl get externalattributesync -A -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.schedule}{"\n"}{end}'
```

Increase the schedule to at least `1m` for production workloads. The Fleet API rate budget is
shared with Pipeline and Collector reconciles.

## Recovery Checklist

1. Confirm whether 429s are present (server-side saturation) or only queue depth is high
   (operator-side burst, self-resolving).
2. If 429s: reduce `--fleet-api-rps` to match server limit.
3. If broad CollectorDiscovery: add disjoint matchers to shard the selector.
4. If high-frequency ExternalAttributeSync: increase schedule interval.
5. After any Helm change, monitor `workqueue_depth` until it reaches zero.
