# api/v1alpha1

CRD Go types and their validating webhooks.

## Every bound is enforced twice

A limit lives both as a kubebuilder marker on the type (`XValidation`, `MaxProperties`,
`MaxLength`), evaluated as CEL by the API server, and as Go code in the matching `*_webhook.go`.
Change one and you must change the other, or the two disagree depending on whether the webhook is
installed and reachable.

`crd_validation_test.go` reads the generated manifests under `config/crd/bases/` and pins the CEL
side: immutable `spec.id`, the reserved `collector.` key prefix, 100 attributes with 1024-character
values, matcher item length 200, selector minimum lengths, and the ExternalAttributeSync
source/secret rules. Editing a marker without running `just gen` fails `just check`.

## The chart CRD copy is manual

`just gen` writes CRDs to `config/crd/bases/` only. `charts/fleet-management-operator/crds/` is a
byte copy that nothing regenerates and no gate compares: `just gen-check` diffs `config/crd/bases`,
`config/rbac/role.yaml`, `config/webhook/manifests.yaml` and the deepcopy file, and stops there.
Copy the changed manifests across in the same commit or the chart ships a stale schema.

## Cross-namespace discovery authorization defaults OFF

`webhook_authz.go` runs a SubjectAccessReview asking whether the requester may create the mirrored
CR in `spec.targetNamespace`. That check is what closes the confused-deputy escalation named in the
root security model, and it is off unless `--enforce-cross-namespace-discovery-authz` is set (Helm
`controllers.crossNamespaceDiscoveryAuthz.enabled`, default `false`). A nil `SubjectAccessReviewer`
short-circuits to allow, so a new CRD carrying a `targetNamespace` that does not call
`checkCrossNamespaceCreate` and get wired in `cmd/main.go` reopens the escalation with no symptom.

## The tenant checker is a nil interface when enforcement is off

Every `*Validator` here holds a `MatcherChecker` field, and `cmd/main.go` leaves the value it
passes nil unless `--enable-tenant-policy-enforcement` is set (Helm
`controllers.tenantPolicy.enabled`). The interface doc says implementations treat a nil receiver as
a no-op, but a nil interface has no method set at all, so calling one panics at admission time.
Guard the call site as `pipeline_webhook.go` does, or go through `runTenantChecks` in
`webhook_tenant.go`, which nil-checks for you.

## Deeper references

- `reference/attributes-and-discovery.md`, section "Webhook validation rules" - the per-CRD rule
  list. Read before adding, relaxing or removing a validation rule.
