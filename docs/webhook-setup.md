# Webhook TLS Setup

The fleet-management-operator uses validating admission webhooks. The webhook server requires
a TLS certificate trusted by the Kubernetes API server. Three strategies are supported.

## Option 1: Self-Signed (Default, Dev/Test Only)

controller-runtime auto-generates a self-signed certificate on startup if no cert path is
configured.

**Limitations:**
- Certificate is regenerated on every pod restart. The caBundle in the ValidatingWebhookConfiguration
  becomes stale until the next restart, causing a brief window where admissions fail.
- Not safe for HA deployments (multiple replicas will have different self-signed certs).
- Not suitable for production.

No configuration needed. Use this only for local development.

## Option 2: cert-manager (Recommended for Production)

Requires [cert-manager](https://cert-manager.io) installed in the cluster.

### 1. Create an Issuer or ClusterIssuer

If you do not already have one, create a self-signed ClusterIssuer for the cluster:

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: selfsigned-issuer
spec:
  selfSigned: {}
```

For production, use a CA-backed issuer or ACME issuer instead.

### 2. Enable cert-manager in Helm values

```yaml
webhook:
  certManager:
    enabled: true
    issuerRef:
      name: selfsigned-issuer
      kind: ClusterIssuer
```

With this enabled, the chart creates a `Certificate` resource targeting the webhook Service.
cert-manager injects the caBundle into the `ValidatingWebhookConfiguration` automatically via
the `cert-manager.io/inject-ca-from` annotation on the VWC.

### 3. Verify

```bash
# Check certificate issuance
kubectl describe certificate -n <namespace> <release-name>-webhook
# Status should show: Certificate is up to date and has not expired

# Check caBundle is populated
kubectl get validatingwebhookconfiguration <release-name>-validating-webhook \
  -o jsonpath='{.webhooks[0].clientConfig.caBundle}' | base64 -d | openssl x509 -noout -text
```

### Certificate Rotation

cert-manager renews automatically before expiry (default 2/3 of the validity period). If
renewal fails:

```bash
# Check renewal status
kubectl describe certificate -n <namespace> <release-name>-webhook

# Force re-issue
kubectl delete certificate -n <namespace> <release-name>-webhook

# Wait for the new certificate
kubectl wait certificate -n <namespace> <release-name>-webhook \
  --for=condition=Ready --timeout=120s

# Restart the operator to pick up the new cert
kubectl rollout restart deployment -n <namespace> fleet-management-operator
```

## Option 3: Manual Certificate

For environments without cert-manager.

### 1. Generate a certificate

The certificate CN and SAN must match the webhook Service DNS name:

```bash
openssl req -x509 -newkey rsa:4096 -keyout tls.key -out tls.crt -days 365 -nodes \
  -subj "/CN=<release-name>-webhook-service.<namespace>.svc" \
  -addext "subjectAltName=DNS:<release-name>-webhook-service,DNS:<release-name>-webhook-service.<namespace>.svc,DNS:<release-name>-webhook-service.<namespace>.svc.cluster.local"
```

### 2. Create the Secret

```bash
kubectl create secret tls <release-name>-webhook-certs \
  --cert=tls.crt --key=tls.key \
  -n <namespace>
```

### 3. Mount the Secret in the Deployment

Add a volume and volumeMount to the Deployment (via Helm values or a patch):

```yaml
# In values.yaml (if the chart supports it)
webhook:
  certDir: /tmp/webhook-certs
  existingSecretName: <release-name>-webhook-certs
```

If the chart does not yet support cert volume mounts, use cert-manager (Option 2) instead.

### 4. Update the caBundle in the VWC

The caBundle must contain the DER-encoded CA certificate that signed the webhook cert. For
a self-signed cert, that is the cert itself.

```bash
CA_BUNDLE=$(base64 -w0 tls.crt)

# List the number of webhook entries first
kubectl get validatingwebhookconfiguration <release-name>-validating-webhook \
  -o jsonpath='{range .webhooks[*]}{.name}{"\n"}{end}'

# Update caBundle for each entry (adjust indices 0 through N-1)
for IDX in 0 1 2 3 4 5; do
  kubectl patch validatingwebhookconfiguration <release-name>-validating-webhook \
    --type=json \
    -p="[{\"op\": \"replace\", \"path\": \"/webhooks/${IDX}/clientConfig/caBundle\", \"value\": \"${CA_BUNDLE}\"}]"
done
```

### Certificate Rotation

Rotate before NotAfter:

```bash
# Generate new cert (same command as step 1 above)
# Replace the Secret in-place
kubectl create secret tls <release-name>-webhook-certs \
  --cert=new-tls.crt --key=new-tls.key \
  -n <namespace> --dry-run=client -o yaml | kubectl apply -f -

# Update caBundle (repeat step 4 above with new-tls.crt)
# Restart the operator
kubectl rollout restart deployment -n <namespace> fleet-management-operator
```

## Verification Commands

```bash
# Check webhook is registered
kubectl get validatingwebhookconfiguration | grep fleet

# List all webhook entries
kubectl get validatingwebhookconfiguration <release-name>-validating-webhook \
  -o jsonpath='{range .webhooks[*]}{.name}{"\n"}{end}'

# Check cert expiry
kubectl get secret <release-name>-webhook-certs -n <namespace> \
  -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -noout -dates

# Test a webhook rejection (should return validation error, not connection refused)
kubectl apply -f - <<EOF
apiVersion: fleetmanagement.grafana.com/v1alpha1
kind: Pipeline
metadata:
  name: webhook-test
  namespace: <namespace>
spec:
  contents: "invalid {{{"
  configType: Alloy
EOF
# Expected: admission webhook rejected the request with a validation error
# If you see "connection refused" or x509 errors, the webhook TLS setup has a problem.
```

## Common Failures

| Symptom | Cause | Fix |
|---------|-------|-----|
| `x509: certificate signed by unknown authority` | caBundle mismatch or stale | Restart pod (self-signed) or re-issue cert (cert-manager); update caBundle in VWC |
| `connection refused` on port 443 | Webhook Service missing or pod not ready | Check Service and pod endpoints |
| `no endpoints available for service` | Pod selector mismatch | Verify Service selector matches Deployment labels |
| `certificate has expired` | Cert not rotated | Renew cert; see Option 2 or 3 above |
| All admission requests fail after pod restart | Self-signed cert regenerated, caBundle stale | Restart pod again or migrate to cert-manager |

## TenantPolicy and Webhook Interaction

When `--enable-tenant-policy-enforcement` is set, the validating webhooks for `Pipeline`,
`RemoteAttributePolicy`, and `ExternalAttributeSync` also enforce that the requesting subject's
required matchers appear in the CR's matcher set. This check runs after schema validation.

If tenant policy enforcement is causing unexpected rejections, check:

```bash
# List TenantPolicies that match the requesting subject's groups/identity
kubectl get tenantpolicy -o yaml

# Check TenantPolicy status for parse errors
kubectl get tenantpolicy -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.conditions[?(@.type=="Ready")].reason}{"\n"}{end}'
```

See `docs/tenant-policy.md` for the full TenantPolicy reference.
