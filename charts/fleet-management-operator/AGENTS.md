# charts/fleet-management-operator

## Chart.yaml is release-please managed

`version` and `appVersion` both carry `# x-release-please-version`. Never hand-bump either.

## CRDs

`crds/` is a hand-maintained copy of `config/crd/bases/`; see `api/v1alpha1` for the sync rule.
Helm installs those manifests on first install and never upgrades or deletes them, so a schema
change reaches an existing release only through
`kubectl apply -f charts/fleet-management-operator/crds/`.

## Render-time guards

`deployment.yaml` and `poddisruptionbudget.yaml` call `fail` on combinations that would otherwise
deploy something broken: `webhook.certManager.enabled` together with `webhook.certDir`, enabled
webhooks with no CA path (`webhook.certDir` + `webhook.certSecretName` + `webhook.caBundle`), a
digest in both `image.digest` and `image.repository`, and a PDB with neither or both of
`minAvailable` / `maxUnavailable`. Express a new mutual exclusion the same way, not as a comment
in `values.yaml`.

Two behaviours are deliberately implicit rather than values:

- The PDB renders only when `podDisruptionBudget.enabled` **and** `replicaCount > 1`. At one
  replica a PDB denies eviction indefinitely and blocks node drains.
- Soft pod anti-affinity is auto-injected when `replicaCount > 1` and `affinity` is empty. Setting
  `affinity` replaces it outright.

## What the chart does not guard

- `controllers.collectorDiscovery.enabled: true` with `controllers.collector.enabled: false`
  renders cleanly; the manager then refuses to start. It surfaces as CrashLoopBackOff, never as a
  Helm error.
- `terminationGracePeriodSeconds` (default 30) must stay at or above the Fleet API HTTP client
  timeout, which is 30s in `pkg/fleetclient`. Lower it and in-flight calls are killed mid-request.
- `controllers.crossNamespaceDiscoveryAuthz.enabled` defaults to `false`, so the SubjectAccessReview
  that closes the cross-namespace confused deputy is off in a default install.

## Gate

`just helm-lint` lints, template-renders, and asserts NetworkPolicy is absent from the default
render and present with `networkPolicy.enabled=true`. Flipping that default fails the gate.
