# Tenant Policy

`TenantPolicy` is an opt-in cluster-scoped CRD that lets you implement
per-tenant authorization for Fleet Management resources using K8s RBAC
group membership. Fleet Management's API has no native RBAC — anyone with
a cloud access token has full power. By routing all configuration changes
through the operator's CRDs, K8s admission becomes the enforcement point;
`TenantPolicy` adds the missing *which-collectors-can-this-user-target*
layer that plain K8s RBAC can't express on its own.

## When to use it

Use `TenantPolicy` when you have multiple teams sharing a single
Kubernetes cluster and a single Fleet Management stack, and you need to
prevent team A from authoring a `Pipeline` (or `RemoteAttributePolicy`,
`ExternalAttributeSync`) whose matchers target team B's collectors.
Standard K8s RBAC handles "team A can create Pipelines in their
namespace"; `TenantPolicy` handles "the Pipelines they create must be
scoped to *their* collectors".

## How it works

When the manager is run with `--enable-tenant-policy-enforcement=true`
(Helm: `controllers.tenantPolicy.enabled: true`), the validating
webhooks for `Pipeline`, `RemoteAttributePolicy`,
`ExternalAttributeSync`, and `CollectorDiscovery` gain a final step:

1. Read `UserInfo` from the admission request (the K8s API server
   includes this with every admission webhook call).
2. List all `TenantPolicy` resources in the cluster.
3. Filter to the policies whose `spec.subjects` match the requesting
   user — User by username, Group by group membership,
   ServiceAccount by `system:serviceaccount:<ns>:<name>`.
4. Filter further by `spec.namespaceSelector` if set, against the labels
   of the namespace the CR is being created in.
5. If no policy matches the user, **allow the request** (default-allow,
   so existing installs and the operator's own service account work
   without change).
6. If at least one policy matches, the **union** of every matching
   policy's `spec.requiredMatchers` is the allowed set. The CR satisfies
   the check by including **at least one** element of that union in its
   own matcher list (`Pipeline.spec.matchers` or
   `spec.selector.matchers` for the other two CRs).
7. If none of the union elements appears, the admission webhook returns
   a denial naming the policies that applied and the matchers that
   would have satisfied them.

## Example

Two teams, each with their own group identity in the cluster's IDP, want
to manage their own pipelines without stepping on each other.

```yaml
apiVersion: fleetmanagement.grafana.com/v1alpha1
kind: TenantPolicy
metadata:
  name: team-billing
spec:
  subjects:
    - kind: Group
      name: team-billing-engineers
    - kind: ServiceAccount
      name: argocd-billing
      namespace: argocd
  requiredMatchers:
    - team=billing
    - team=billing-shared
---
apiVersion: fleetmanagement.grafana.com/v1alpha1
kind: TenantPolicy
metadata:
  name: team-payments
spec:
  subjects:
    - kind: Group
      name: team-payments-engineers
  requiredMatchers:
    - team=payments
```

A member of `team-billing-engineers` creating this Pipeline is allowed:

```yaml
apiVersion: fleetmanagement.grafana.com/v1alpha1
kind: Pipeline
metadata:
  name: billing-logs
  namespace: billing
spec:
  contents: "loki.source.file \"app\" { ... }"
  matchers:
    - team=billing       # <- satisfies the policy
    - environment=prod
```

The same user trying this gets denied:

```yaml
spec:
  contents: "loki.source.file \"app\" { ... }"
  matchers:
    - team=payments       # other team's matcher
    - environment=prod
# Denied: matchers must include at least one of [team=billing, team=billing-shared]
# (required by TenantPolicy: team-billing)
```

## Enabling

Helm value:

```yaml
controllers:
  tenantPolicy:
    enabled: true
```

This sets the manager flag `--enable-tenant-policy-enforcement=true` and
grants the operator the RBAC needed to read `TenantPolicy` and
`Namespace` resources at admission time. The CRD itself is installed
unconditionally with the chart so users can pre-populate policies before
flipping the flag.

The flag is off by default. Existing installs see no behavior change
until you both create at least one `TenantPolicy` *and* enable the flag.

## Coverage and v1 limits

Matcher-based enforcement covers:
- `Pipeline.spec.matchers`
- `RemoteAttributePolicy.spec.selector.matchers`
- `ExternalAttributeSync.spec.selector.matchers`
- `CollectorDiscovery.spec.selector.matchers` — `CollectorDiscovery`'s
  selector is a matcher/collectorID selector (the same `PolicySelector`
  shape), so the required-matcher check applies exactly as it does for
  the three CRDs above, plus the same `collectorIDs` guard.

Residual gaps (deliberate in v1):
- **`spec.selector.collectorIDs` bypass — NOT closed.** On
  `RemoteAttributePolicy`, `ExternalAttributeSync`, and
  `CollectorDiscovery`, a request whose selector lists explicit
  `collectorIDs` escapes the matcher check entirely. The webhooks apply a
  partial guard: when a `TenantPolicy` matches the requesting user, a
  selector that uses `collectorIDs` is *rejected* (the user must scope by
  matchers instead). But a user to whom **no** `TenantPolicy` applies is
  default-allowed, and the matcher semantics still do not reason about the
  IDs themselves. A future revision may add an `allowedCollectorIDs` field
  on `TenantPolicy`. Do not rely on `TenantPolicy` alone to fence off
  collector IDs.
- **`PipelineDiscovery` is NOT constrained by matcher enforcement.** Its
  selector (`PipelineDiscoverySelector`) filters by `configType` /
  `enabled`, not by matchers, so there is no matcher scope for a
  `TenantPolicy` to require. A `PipelineDiscovery` imports Fleet pipelines
  fleet-wide and creates `Pipeline` CRs locally; the control that protects
  it is the **cross-namespace discovery authorization** check below (a
  user can only mirror into a namespace they may themselves write
  `Pipeline` CRs in), plus standard K8s RBAC on who may create
  `PipelineDiscovery` at all. Imported `Pipeline` CRs are themselves
  matcher-checked on any subsequent edit by a tenanted user.
- **`Collector`** CRs bind to a specific collector ID rather than
  selecting by matchers and are not matcher-checked.
- The matcher check is **required-matcher** semantics: the CR's matcher
  list must contain at least one matcher equal to one of the policy's
  required matchers. It does **not** reason about negation or regex —
  e.g. `team=billing` AND `team!=billing` (matches nothing) still
  passes, and `team=billing` AND `team=~.*` (matches everything) also
  passes. A future strict mode (subset semantics) is a
  CRD-compatible upgrade.

## Cross-namespace discovery authorization

`PipelineDiscovery` and `CollectorDiscovery` are namespaced CRDs with a
`spec.targetNamespace` field. When set, the operator creates the mirrored
`Pipeline` / `Collector` CRs in that namespace using its own cluster-wide
ServiceAccount. Without a guard this is a classic **confused-deputy**
escalation: anyone permitted to create a discovery CR in namespace A could
name `targetNamespace: B` and have the operator write CRs into namespace B
on their behalf, even if they have no RBAC there.

The `--enforce-cross-namespace-discovery-authz` manager flag (Helm:
`controllers.crossNamespaceDiscoveryAuthz.enabled: true`) closes this.
When enabled, both discovery webhooks — on create **and** update — perform
a `SubjectAccessReview` whenever `spec.targetNamespace` is non-empty and
differs from the discovery CR's own namespace. The review asks the API
server whether the **requesting user** (taken from the admission request's
`UserInfo`) may `create` `pipelines` (for `PipelineDiscovery`) or
`collectors` (for `CollectorDiscovery`) in the target namespace. If the
answer is not an explicit allow, the request is rejected with an error
naming the user, the target namespace, and the required permission.

A same-namespace target (empty, or equal to the CR's own namespace) is
never reviewed — it is already governed by the RBAC that let the user
create the discovery CR in the first place.

```yaml
controllers:
  crossNamespaceDiscoveryAuthz:
    enabled: true
```

This sets `--enforce-cross-namespace-discovery-authz=true`. The flag is
**off by default** (default-allow) so existing installs see no behavior
change until it is set. The chart grants the operator
`create` on `subjectaccessreviews.authorization.k8s.io` whenever
`rbac.create` is true, so the check works the moment the flag is flipped.

This control is independent of TenantPolicy enforcement: you can enable
either flag without the other. For `PipelineDiscovery` it is the *primary*
tenancy control (see the coverage note above); for `CollectorDiscovery` it
layers underneath the matcher check (the matcher check runs first, so a
tenant denial short-circuits before any `SubjectAccessReview` is issued).

## Status conditions

When `--enable-tenant-policy-enforcement=true`, the manager also runs a
small `TenantPolicy` reconciler that maintains `status` on every policy.
The reconciler does not call Fleet Management — it only re-validates the
spec and surfaces the result.

| Type    | Status | Reason       | Meaning |
| ------- | ------ | ------------ | ------- |
| `Valid` | True   | `Valid`      | All required matchers parse, namespace selector parses. |
| `Valid` | False  | `ParseError` | A matcher or selector is malformed. The condition message names the offending field. |
| `Ready` | True   | `Valid`      | Policy is enforceable. Mirrors `Valid=True`. |
| `Ready` | False  | `ParseError` | Mirrors `Valid=False`. |

`status.observedGeneration` tracks the last spec generation reconciled.
`status.boundSubjectCount` reflects `len(spec.subjects)` and is what the
`Subjects` printer column reads. The `Ready` printer column reads the
condition's `Status` field directly.

```bash
kubectl get tenantpolicy            # SUBJECTS / READY / AGE columns
kubectl describe tenantpolicy <n>   # Conditions block
```

## Cluster admin caveat

If a cluster admin happens to be a member of a tenant's group, they will
be subject to that policy's matcher requirements. There is no special
"admin bypass" — you can either avoid being in tenant groups, or create
an admin-scoped `TenantPolicy` whose `requiredMatchers` are permissive
enough for your needs.

## Who can edit `TenantPolicy`

Standard K8s RBAC on the CRD itself. Cluster admins should restrict
write access to the platform team — typically by not granting `create`,
`update`, `delete` on `tenantpolicies.fleetmanagement.grafana.com` to
tenant groups.
