# Security model and hardening

This document describes the operator's trust boundaries, the privileges it
holds, and how to deploy it safely. Read it before delegating custom-resource
access to non-admins or enabling the discovery controllers in a multi-tenant
cluster.

## What the operator can do

The controller manager runs with two powerful credentials:

1. A **cluster-wide Kubernetes ServiceAccount** bound to a `ClusterRole`. It can
   read/write the operator's CRDs in **every namespace**, read `namespaces`,
   and — when `ExternalAttributeSync` is enabled — **read every `Secret` in the
   cluster** (see [Secret access](#cluster-wide-secret-access)).
2. A single **org-wide Fleet Management API credential** (Stack ID + Cloud
   access token). Every Pipeline/Collector change the operator makes is applied
   to your shared Fleet Management tenant under this one identity.

The operator deliberately holds **no** privilege-escalation verbs: no
`bind`/`escalate`/`impersonate`, no `serviceaccounts/token`, and no write on
`clusterroles`, `rolebindings`, `validatingwebhookconfigurations`, or
`customresourcedefinitions`. Webhook CA injection is cert-manager's job, not the
operator's. A compromised operator cannot rewrite its own admission rules or
mint tokens — but it can read secrets and write CRs cluster-wide, so treat the
ServiceAccount token as sensitive.

## The central rule: creating a CR is a privileged action

Because the operator acts on the two credentials above, **the ability to create
a `fleetmanagement.grafana.com` custom resource is effectively a privileged
grant**, not an ordinary namespaced permission:

| Resource | What creating it actually does |
|----------|--------------------------------|
| `Pipeline` | Pushes Alloy/OTel config to the shared, org-wide Fleet tenant. |
| `Collector` / `RemoteAttributePolicy` / `ExternalAttributeSync` | Drives collector remote attributes via the org credential; EAS additionally fetches an external HTTP/SQL endpoint and can read a `Secret` in its own namespace. |
| `PipelineDiscovery` / `CollectorDiscovery` | Mirrors Fleet resources into Kubernetes — and with `spec.targetNamespace`, creates CRs in **another namespace** (see [Cross-namespace authority](#cross-namespace-authority)). |
| `TenantPolicy` | Defines the tenancy matchers themselves — a cluster-admin control. |

Out of the box this is safe because **the chart ships no user-facing roles** and
does not aggregate anything into the built-in `admin`/`edit`/`view` cluster
roles. Only subjects a cluster admin explicitly grants access can create these
CRs. The risk appears the moment you **delegate** CR creation to namespace
tenants. The rest of this document is about doing that safely.

## Delegating access: the opt-in user roles

Set `rbac.userRoles.create: true` to render two unbound `ClusterRole`s:

- `<release>-editor` — full management of the namespaced CRDs (excluding
  `TenantPolicy`).
- `<release>-viewer` — read-only on the same set.

The chart binds them to no one. You create the binding:

```sh
# Delegate management within a single namespace:
kubectl create rolebinding team-a-fleet-editor \
  --clusterrole=fleet-management-operator-editor \
  --group=team-a --namespace=team-a
```

> **`TenantPolicy` is intentionally excluded** from both roles. It is the
> cluster-admin tenancy control; letting an "editor" modify it would let a
> tenant grant itself matchers. Manage `TenantPolicy` with explicit admin RBAC.

### Do not aggregate into the built-in roles

`rbac.userRoles.aggregateToDefaultRoles` (default `false`) adds the
`rbac.authorization.k8s.io/aggregate-to-{admin,edit,view}` labels so the roles
fold into Kubernetes' built-in `admin`/`edit`/`view`.

**Leave this off in multi-tenant clusters.** Aggregating the editor role into
the built-in `edit` role grants **every namespace admin in the cluster** the
ability to create these effectively-privileged resources — including
`PipelineDiscovery`/`CollectorDiscovery`, which can write across namespaces.
That re-opens the [cross-namespace confused deputy](#cross-namespace-authority)
cluster-wide. Only enable it in single-tenant clusters where every `edit`-role
holder is already trusted with the Fleet credential.

## Cross-namespace authority

`PipelineDiscovery` and `CollectorDiscovery` accept `spec.targetNamespace`. When
set, the operator creates the mirrored `Pipeline`/`Collector` CRs in **that**
namespace using its cluster-wide ServiceAccount. The admission webhook validates
only that the value is a syntactically valid namespace name — so without extra
controls, a user who can create a discovery CR in namespace A can make the
operator write CRs into **any** namespace B. This is a classic confused-deputy.

**Recommended posture: treat creating a `PipelineDiscovery` or `CollectorDiscovery`
as a platform/admin operation.** Because cross-namespace mirroring is the point of
the feature, only cluster-admins or platform teams should be granted the ability to
create these CRs. Do not delegate discovery-CR creation to per-namespace tenants
unless you also enable the SubjectAccessReview gate below.

**Mitigation — `--enforce-cross-namespace-discovery-authz`** (Helm:
`controllers.crossNamespaceDiscoveryAuthz.enabled`). When enabled, the discovery
webhooks issue a `SubjectAccessReview` for the *requesting user* and reject the
CR unless that user can `create` the target resource (`pipelines`/`collectors`)
in the target namespace. Enable this whenever you delegate discovery-CR creation
to anyone who is not a cluster admin. It is default-off for backward
compatibility; turning it on is strongly recommended for multi-tenant clusters.

## Cluster-wide Secret access

When `controllers.externalAttributeSync.enabled` (default `true`), the operator
is granted `get`/`list`/`watch` on `secrets` cluster-wide so it can read the
auth material an `ExternalAttributeSync` references. This is the single
highest-value grant in the role.

Defenses and reductions:

- **Same-namespace only.** An `ExternalAttributeSync` may only reference a
  `Secret` in its own namespace; a cross-namespace `secretRef` is rejected at
  both admission and reconcile time. The operator never reads a foreign-namespace
  secret on behalf of a CR.
- **Label-scoped cache** (recommended). Set
  `controllers.externalAttributeSync.secretLabelSelector` (manager flag
  `--external-source-secret-label-selector`) to e.g.
  `fleetmanagement.grafana.com/external-source=true` so the operator's informer
  only watches/caches `Secret`s carrying a matching label. This shrinks both
  memory and the accidental-exposure surface, and means an
  `ExternalAttributeSync` can only use a `Secret` an admin has explicitly
  labelled. Label the secrets you intend EAS to read **before** setting this —
  an empty selector (default) watches all Secrets for backward compatibility.
- **Drop the cluster-wide grant.** Set
  `controllers.externalAttributeSync.clusterWideSecretAccess: false` to remove
  the `secrets` rule from the ClusterRole entirely, and instead provision your
  own namespaced `Role`/`RoleBinding` granting `get` on `secrets` only in the
  namespaces that actually hold EAS source Secrets. This is the true
  least-privilege posture (the label-scoped cache reduces what the operator
  *reads*, but RBAC still permits cluster-wide reads until you drop this grant).
- **Disable EAS if unused.** Setting `controllers.externalAttributeSync.enabled:
  false` removes the cluster-wide secret grant entirely.

The Fleet Management credentials `Secret` is delivered to the pod via the
kubelet (`env[].valueFrom.secretKeyRef`), **not** through the operator's API
client, so
it does not require — and is unaffected by — the secret cache scoping above.

## External sources (SSRF)

`ExternalAttributeSync` HTTP sources fetch a user-supplied URL. Controls:

- **Admission denylist.** The webhook rejects URLs whose host is loopback,
  RFC-1918 private, link-local (including the cloud metadata endpoint
  `169.254.169.254`), unspecified, or an in-cluster name (`localhost`, `*.local`,
  `*.svc`, `*.cluster.local`).
- **Dial-time re-check.** The HTTP source installs a guarded dialer that
  re-validates the **resolved** IP of every connection — including redirect
  targets — closing DNS-rebinding TOCTOU bypasses where a name resolves to a
  public address at admission and a private/metadata address at fetch time.
- **HTTPS required with auth.** A `secretRef` forces `https`, so credentials are
  not sent in cleartext.
- **SQL sources** are restricted to a single read-only `SELECT` (no multiple
  statements; a keyword denylist blocks DML/DDL).

Defense in depth: also enable [NetworkPolicy](#networkpolicy) egress restriction
so the operator can only reach approved destinations at the network layer.

## TenantPolicy is a guardrail, not an authorization boundary

When `--enable-tenant-policy-enforcement` is set, the validating webhooks for
`Pipeline`, `RemoteAttributePolicy`, `ExternalAttributeSync`, and
`CollectorDiscovery` require that a matched subject's required matchers appear in
the CR's matcher set. This is a useful guardrail, but **not** a complete
authorization boundary. Known residual gaps:

- `selector.collectorIDs` bypasses matcher checks (a selector by collector ID is
  not constrained by required matchers).
- Required-matcher semantics do not reason about negation/regex matchers.
- It is default-allow when no policy matches the requesting subject.
- `Collector` is not covered.
- `PipelineDiscovery` is **not** matcher-scoped — its selector filters by
  `configType`/`enabled`, so matcher enforcement does not apply. Its
  cross-namespace protection is the
  [`--enforce-cross-namespace-discovery-authz`](#cross-namespace-authority)
  SubjectAccessReview, not TenantPolicy.

Use it together with — not instead of — the RBAC controls above. See
[tenant-policy.md](tenant-policy.md) for details.

## Pod and admission hardening (defaults)

The chart already ships these; keep them on:

- **Restricted Pod Security Standard:** non-root (`runAsUser/Group: 65532`),
  `readOnlyRootFilesystem`, `allowPrivilegeEscalation: false`,
  `capabilities.drop: [ALL]`, `seccompProfile: RuntimeDefault`.
- **Fail-closed webhooks** (`failurePolicy: Fail`, `sideEffects: None`) so
  admission guards cannot be bypassed by killing the webhook pod.
- **Image digest pinning** via `image.digest` for supply-chain integrity.

## NetworkPolicy

`networkPolicy.enabled` is `false` by default for compatibility. Enabling it
gives default-deny egress with explicit allowances and is recommended in
production. A complete hardened example:

```yaml
networkPolicy:
  enabled: true
  egress:
    dns:
      enabled: true   # uses kube-system/kube-dns by default
    kubeAPI:
      enabled: true   # TCP/443; set `to` CIDRs for stricter clusters
    fleetAPI:
      enabled: true   # TCP/443 to Grafana Cloud Fleet Management
    # Only needed if ExternalAttributeSync HTTP/SQL sources are used. List the
    # approved CMDB/database destinations explicitly:
    externalSources:
    - to:
      - ipBlock:
          cidr: 203.0.113.10/32
      ports:
      - protocol: TCP
        port: 5432
```

If you enable egress restriction without populating `externalSources`,
`ExternalAttributeSync` fetches will be blocked at the network layer — which is
the safe default if you do not use external sources.

## Hardening checklist

- [ ] Disable controllers you do not use (`controllers.*.enabled: false`).
      Disabling `externalAttributeSync` removes the cluster-wide secret grant.
- [ ] Do **not** set `rbac.userRoles.aggregateToDefaultRoles` in multi-tenant
      clusters.
- [ ] Bind `<release>-editor` only to subjects trusted with the Fleet
      credential; prefer namespaced `RoleBinding`s over `ClusterRoleBinding`s.
- [ ] Enable `--enforce-cross-namespace-discovery-authz` if delegating
      discovery-CR creation to non-admins.
- [ ] Label the `Secret`s that `ExternalAttributeSync` reads with
      `fleetmanagement.grafana.com/external-source: "true"` and scope the secret
      cache to that label.
- [ ] Enable `networkPolicy.enabled` with an `externalSources` allowlist.
- [ ] Pin the image by digest (`image.digest`).
- [ ] Enable `--enable-tenant-policy-enforcement` as a guardrail, understanding
      the residual gaps above.
