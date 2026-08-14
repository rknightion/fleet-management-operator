---
id: FMO-0003
title: >-
  Close the TenantPolicy v1 gaps: collectorIDs bypass, negation/regex semantics,
  uncovered kinds
status: To Do
assignee: []
created_date: '2026-08-14 16:39'
labels:
  - tenant-policy
  - security
  - v2
dependencies: []
ordinal: 3000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TenantPolicy is documented as a guardrail, not an authorization boundary, and AGENTS.md plus docs/tenant-policy.md both say so. Three gaps are known, documented, and deliberately shipped:

1. selector.collectorIDs bypasses matcher checks entirely - a subject constrained by required matchers can still name collector IDs directly. Marked TODO(v2) at api/v1alpha1/webhook_tenant_test.go:381.
2. Required-matcher semantics do not reason about negation or regex. A required matcher of team=team-a is not satisfied-checked against a CR carrying team!=team-b or team=~team-.*, so the check is syntactic.
3. Collector and CollectorDiscovery are not covered by enforcement at all - only Pipeline, RemoteAttributePolicy and ExternalAttributeSync webhooks consult the checker.

Enforcement is default-allow when no policy matches the requesting user, which is the right default for an opt-in guardrail and the wrong one for an authorization boundary. Closing these three does not by itself make TenantPolicy an authorization boundary; decide explicitly whether that is the goal before starting, because it changes the default-allow question.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Decision recorded on whether TenantPolicy is intended to become an authorization boundary, with the reason
- [ ] #2 collectorIDs bypass closed end-to-end, with the TODO(v2) at api/v1alpha1/webhook_tenant_test.go:381 removed
- [ ] #3 Required-matcher checking handles negation and regex matchers, or documents precisely why it does not
- [ ] #4 Collector and CollectorDiscovery either covered by enforcement or documented as intentionally out of scope
- [ ] #5 docs/tenant-policy.md V1 gaps section updated to match reality
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make lint
- [ ] #2 make test
- [ ] #3 make manifests generate && git diff --exit-code (only if CRD types or RBAC markers changed)
- [ ] #4 make docs-check && make chart-docs-check (only if docs/ or chart values changed)
<!-- DOD:END -->
