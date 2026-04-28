# API Versioning and Graduation Policy

This document defines the rules for evolving the
`fleetmanagement.grafana.com` API group: how versions graduate from
`v1alpha1`, how breaking changes are introduced, and how long deprecated
versions remain served.

## Current state

- All CRDs ship at **`v1alpha1`** and are the only served, stored version.
- No conversion webhook is wired up yet.
- Six CRDs in scope: `Pipeline`, `Collector`, `RemoteAttributePolicy`,
  `ExternalAttributeSync`, `CollectorDiscovery`, `TenantPolicy`.

## Graduation criteria — `v1alpha1` to `v1`

A CRD is considered ready to graduate to `v1` only when **all** of the
following hold:

1. **Known design gaps closed.** Every gap documented in
   `CLAUDE.md` ("V1 gaps", `docs/tenant-policy.md`, the production-
   readiness audit at `docs/superpowers/audits/`) is either resolved or
   re-classified as v2 work with a documented rationale.
2. **Schema validation parity in CEL.** Every webhook validation rule
   that is structurally expressible (length caps, prefix bans, simple
   shape checks) is mirrored as either an OpenAPI structural rule or a
   CEL `XValidation`. The Go webhook stays in place as
   defence-in-depth, but the API server itself rejects malformed CRs
   even with the webhook offline.
3. **Status discipline.** Every CRD has a status subresource, an
   `observedGeneration` field, and at least one documented condition
   type per the registry in `docs/conditions.md`.
4. **Production soak.** At least one production deployment has been
   running on the candidate spec for ≥3 months without an incident
   that would have required a CRD-shape change.
5. **Printer columns finalised.** All printer columns expose a typed
   field (no string-array-as-integer coercion). Standard columns
   present: `Age`, `Ready`.
6. **Documentation alignment.** User-facing samples
   (`config/samples/*.yaml`) are valid against the v1 schema; the
   chart, RBAC, and webhook configurations install cleanly.

## Path to `v1` — hub-and-spoke conversion

When the gates above are met:

1. Add `v1` as a **served, non-storage** version alongside `v1alpha1`.
2. `v1alpha1` remains the storage version through the deprecation
   window — no etcd rewrites, no migration job.
3. Stand up a **conversion webhook** before flipping any defaults.
   Round-trip every supported field; envtest the conversion before
   the served-version change merges.
4. After ≥6 months of `v1` being available and at least one minor
   release exposing it, switch the storage version to `v1`.
5. After a further ≥6 months, mark `v1alpha1` as not served (still
   stored where applicable until an explicit storage migration). The
   deprecation timer for `v1alpha1` removal starts when "not served"
   ships and is recorded in this document.

## Deprecation policy

- **Notice window:** ≥6 months between announcement and removal for
  any served-version drop, field rename, type change, or default
  change visible to users.
- **Announcements** appear in:
  - The release notes for the version that introduces the deprecation.
  - This document, in a "Deprecations in flight" section (added when
    needed; absent today).
  - A `Deprecated` condition on affected CRs where applicable
    (e.g. when a single field is being removed but the CR is
    otherwise valid).
- **Field renames** go through additive-then-remove:
  1. Introduce the new field; both fields are accepted; the new field
     wins on read.
  2. Mark the old field deprecated in godoc + release notes.
  3. After the notice window, remove the old field in the next
     served version (not in `v1alpha1` patches).
- **Type changes** are not allowed in served versions — they require
  a new field name and the deprecate-then-remove dance above.
- **Default changes** count as breaking. Same window, same notice.

## Known v1 blockers

These items must be resolved (or explicitly punted) before any CRD
graduates to `v1`. They are sourced from the production-readiness
audit (2026-04-28) and `CLAUDE.md`.

- **TenantPolicy `selector.collectorIDs` bypass.** Required-matcher
  enforcement does not apply when a CR uses `spec.selector.collectorIDs`.
  Either close the gap or add a documented `allowedCollectorIDs` field.
- **TenantPolicy required-matcher semantics.** No reasoning about
  negation or regex. `team!=billing` and `team=~.*` both pass a
  `team=billing` requirement. A "strict mode" with subset semantics
  would be a CRD-compatible upgrade.
- **TenantPolicy coverage.** `Collector` and `CollectorDiscovery` are
  not subject to TenantPolicy enforcement.
- **`status.matchedCollectorIDs` unbounded** on
  `RemoteAttributePolicy` and `ExternalAttributeSync` — at 30k
  collectors a single status field could grow large enough to bloat
  etcd. Decide on a cap or move to a count-only summary by `v1`.
  (Production-readiness audit finding `PERF-01`.)
- **CRD condition vocabulary** finalised — see
  `docs/conditions.md`. Any rename here is a breaking change.

## Scale subresource (audit finding API-09)

The scale subresource is **deferred to v1+** for every CRD in this
group. `Pipeline`, `Collector`, `RemoteAttributePolicy`,
`ExternalAttributeSync`, `CollectorDiscovery`, and `TenantPolicy` are
not workload CRDs — `kubectl scale` semantics ("set replicas") do not
map cleanly onto "make this Pipeline match more collectors" or "bind
this Policy to more subjects". We have not identified a user workflow
that needs it. Revisit only if a concrete request surfaces.

## Conversion webhook — out of scope today

Until at least one `v1` version is on the roadmap, no conversion
webhook is added. The single served version makes conversion a no-op
and we deliberately avoid setting up infrastructure that has nothing
to convert. When the first v1 type lands, the webhook is added in the
same change.
