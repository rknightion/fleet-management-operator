# api/v1alpha1 — CRD Types and Admission Webhooks

This package defines all CRD Go types and their admission (validating) webhooks.

## Files

| File | Purpose |
|------|---------|
| `groupversion_info.go` | GroupVersion registration (`fleetmanagement.grafana.com/v1alpha1`) |
| `hub.go` | Conversion hub marker for future version migration |
| `pipeline_types.go` | Pipeline CRD spec/status; ConfigType constants |
| `collector_types.go` | Collector CRD spec/status; reserved key prefix rules |
| `policy_types.go` | RemoteAttributePolicy CRD |
| `external_sync_types.go` | ExternalAttributeSync CRD; HTTP/SQL source kinds |
| `collector_discovery_types.go` | CollectorDiscovery CRD; onCollectorRemoved policy |
| `tenant_policy_types.go` | TenantPolicy CRD; subject binding |
| `webhook_tenant.go` | `MatcherChecker` interface + tenant enforcement logic shared by all webhooks |
| `*_webhook.go` | Per-CRD webhook structs implementing `CustomValidator` |
| `*_webhook_test.go` | Table-driven webhook validation tests |
| `zz_generated.deepcopy.go` | Generated; do not edit manually |

## Running Tests

```bash
go test ./api/v1alpha1/...
go test ./api/v1alpha1/... -run TestPipeline_Validate
go test ./api/v1alpha1/... -run TestCollector_Validate
```

## Adding a New CRD

1. Create `<name>_types.go` with Spec/Status structs and kubebuilder markers.
2. Run `make generate && make manifests` to generate deepcopy and CRD YAML.
3. Create `<name>_webhook.go` with a `*Validator` struct that implements `admission.CustomValidator`.
4. Register the webhook in `cmd/main.go` alongside the others.
5. Add RBAC markers to the types file or the controller (see `pipeline_types.go` as a model).
6. Copy the CRD YAML from `config/crd/bases/` into `charts/fleet-management-operator/crds/`.

## Webhook Pattern

Each CRD has a `*Validator` struct in `<name>_webhook.go`:

```go
type PipelineValidator struct {
    tenantChecker MatcherChecker  // nil when TenantPolicy enforcement is off
}

func (v *PipelineValidator) ValidateCreate(ctx, obj) (warnings, error)
func (v *PipelineValidator) ValidateUpdate(ctx, old, new) (warnings, error)
func (v *PipelineValidator) ValidateDelete(ctx, obj) (warnings, error)
```

- Shared validation helpers (matcher syntax, key prefix rules, schedule parsing) live in the same file or `webhook_tenant.go`.
- `MatcherChecker` is `nil` unless `--enable-tenant-policy-enforcement` is set; always nil-check before calling.
- Tests use a `fakeMatcherChecker` in `webhook_tenant_test.go`.

## Matcher Validation Rules

- Syntax: `=`, `!=`, `=~`, `!~` — NOT `==`.
- Max 200 characters per matcher.
- Validated via `validateMatcherSyntax` in each webhook file.

## ConfigType Constants

```go
const (
    ConfigTypeAlloy                 ConfigType = "Alloy"
    ConfigTypeOpenTelemetryCollector ConfigType = "OpenTelemetryCollector"
)
```

API mapping:
- `Alloy` → `CONFIG_TYPE_ALLOY`
- `OpenTelemetryCollector` → `CONFIG_TYPE_OTEL`

Webhooks validate that config contents match the declared ConfigType before the resource is admitted.

## Key Invariants

- `spec.id` on Collector is immutable after creation (enforced in `ValidateUpdate`).
- `collector.*` key prefix is reserved; rejected by Collector and RemoteAttributePolicy webhooks.
- Max 100 remote attributes per Collector; max value length 1024 bytes.
- ExternalAttributeSync `schedule` must parse as `time.ParseDuration` OR a 5-field cron expression.
- CollectorDiscovery `pollInterval` must parse as `time.ParseDuration` and be `>= 1m`.
