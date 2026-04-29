# Webhook Unavailable Runbook

**Alert:** `FleetWebhookRejectionRateHigh`
**Severity:** warning
**Condition:** Admission webhook returning 500s for 5m.

## Background

All validating webhooks use `failurePolicy: Fail`. If the webhook server is completely
unreachable, **all new Pipeline, Collector, RemoteAttributePolicy, ExternalAttributeSync,
CollectorDiscovery, and TenantPolicy creates and updates will be rejected**. Existing CRs
continue to reconcile normally -- webhooks only apply at admission time.

The webhook server runs in the same process as the operator. An operator pod outage is also
a webhook outage. Check `docs/runbooks/operator-down.md` first if the operator pod is down.

## Verification

```bash
# Check webhook service
kubectl get svc -n <namespace> | grep webhook

# Check webhook configuration
kubectl describe validatingwebhookconfiguration | grep -E "Name:|Service:|Failure|caBundle"

# Check endpoints (must be non-empty)
kubectl get endpoints -n <namespace> <release-name>-webhook

# Check cert expiry
kubectl get secret -n <namespace> | grep webhook
kubectl get secret -n <namespace> <release-name>-webhook-certs \
  -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -noout -dates

# Check operator logs for TLS errors
kubectl logs -n <namespace> -l app.kubernetes.io/name=fleet-management-operator | \
  grep -E "x509|certificate|tls|webhook"
```

## Certificate Expiry

**Signal:** `x509: certificate has expired` in operator logs; admission requests failing with
TLS errors.

### cert-manager mode

cert-manager renews automatically. If renewal failed:

```bash
# Check certificate status
kubectl describe certificate -n <namespace> <release-name>-webhook
# Look for: Status.Conditions showing renewal errors or NotAfter already past

# Force re-issue by deleting the certificate (cert-manager recreates it)
kubectl delete certificate -n <namespace> <release-name>-webhook

# Wait for the new certificate to be issued
kubectl wait certificate -n <namespace> <release-name>-webhook \
  --for=condition=Ready --timeout=120s
```

After renewal, restart the operator to pick up the new cert:

```bash
kubectl rollout restart deployment -n <namespace> fleet-management-operator
```

### Self-signed mode (default)

The cert is regenerated on every pod restart. Restart the pod:

```bash
kubectl rollout restart deployment -n <namespace> fleet-management-operator
```

**Note:** Self-signed mode is not HA-safe. The caBundle in the ValidatingWebhookConfiguration
becomes stale between restarts, causing a brief window where admissions fail. Migrate to
cert-manager for production (see `docs/webhook-setup.md`).

### Manual cert mode

Rotate the Secret before NotAfter:

```bash
# Generate a new cert (adjust CN and SAN to match your release name and namespace)
openssl req -x509 -newkey rsa:4096 -keyout new-tls.key -out new-tls.crt -days 365 -nodes \
  -subj "/CN=<release-name>-webhook.<namespace>.svc" \
  -addext "subjectAltName=DNS:<release-name>-webhook,DNS:<release-name>-webhook.<namespace>.svc,DNS:<release-name>-webhook.<namespace>.svc.cluster.local"

# Replace the Secret in-place
kubectl create secret tls <release-name>-webhook-certs \
  --cert=new-tls.crt --key=new-tls.key \
  -n <namespace> --dry-run=client -o yaml | kubectl apply -f -

# Restart the operator
kubectl rollout restart deployment -n <namespace> fleet-management-operator

# Update caBundle in the VWC for all webhook entries
CA_BUNDLE=$(base64 -w0 new-tls.crt)
# Repeat for each index (0 through N-1) in .webhooks[]
kubectl patch validatingwebhookconfiguration <release-name>-validating-webhook \
  --type=json \
  -p="[{\"op\": \"replace\", \"path\": \"/webhooks/0/clientConfig/caBundle\", \"value\": \"${CA_BUNDLE}\"}]"
```

## Webhook Service Not Reachable

Check that the webhook Service exists and endpoints are populated:

```bash
kubectl get endpoints -n <namespace> <release-name>-webhook
```

If endpoints are empty:
1. Verify the pod is running: `kubectl get pods -n <namespace> -l app.kubernetes.io/name=fleet-management-operator`
2. Verify the Service selector matches the Deployment labels:
   `kubectl get svc -n <namespace> <release-name>-webhook -o yaml | grep selector`
   `kubectl get pods -n <namespace> -l app.kubernetes.io/name=fleet-management-operator --show-labels`

## failurePolicy: Fail Implications

While the webhook is unavailable:
- Creating or updating CRs is blocked. Existing CRs continue reconciling normally.
- Do not delete existing CRs -- they continue to function and would need to be re-created
  once the webhook is restored.
- Fix the underlying cert or connectivity issue before attempting new CRs.

If you need to bypass the webhook temporarily in a genuine emergency (e.g. the webhook itself
has a bug that must be patched), you can set `failurePolicy: Ignore` on the VWC:

```bash
kubectl patch validatingwebhookconfiguration <release-name>-validating-webhook \
  --type=json \
  -p='[{"op": "replace", "path": "/webhooks/0/failurePolicy", "value": "Ignore"}]'
# Repeat for each webhook entry
```

**WARNING:** Setting `failurePolicy: Ignore` disables all validation. Invalid CRs will be
admitted and will fail at reconcile time with condition reasons `ValidationError` or
`SyncFailed`. Restore `failurePolicy: Fail` as soon as the webhook is operational.
