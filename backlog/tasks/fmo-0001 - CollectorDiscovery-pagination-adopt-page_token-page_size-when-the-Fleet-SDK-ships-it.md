---
id: FMO-0001
title: >-
  CollectorDiscovery pagination: adopt page_token/page_size when the Fleet SDK
  ships it
status: Parked
assignee: []
created_date: '2026-08-14 16:39'
labels:
  - blocked-upstream
  - discovery
  - scale
dependencies: []
ordinal: 1000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
PERF-02 from the 2026-04-28 production-readiness audit, still PARTIAL. Fleet Management SDK's ListCollectorsRequest does not expose page_token or page_size, so a broad CollectorDiscovery selector in a 30k-collector fleet pulls every collector in a single response (roughly 30 MB). Mitigations already landed and are NOT the fix: an admission Warning when spec.selector is empty, the sharding pattern documented in AGENTS.md, and a pagination adoption note in pkg/fleetclient/collector.go (commit d605701).

RESUME BOUNDARY: blocked until the Fleet Management Go SDK's ListCollectorsRequest gains page_token and page_size fields. At that point pkg/fleetclient/collector.go adopts them transparently - no CRD change is required, so no API version bump and no migration. Check the SDK's ListCollectorsRequest for those fields before assuming this is still blocked.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 pkg/fleetclient/collector.go pages through ListCollectors rather than assuming a single response
- [ ] #2 No CRD or API version change was required
- [ ] #3 The pagination adoption note in pkg/fleetclient/collector.go is removed or replaced with the real behaviour
- [ ] #4 Sharding guidance in AGENTS.md updated to say whether sharding is still needed
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make lint
- [ ] #2 make test
- [ ] #3 make manifests generate && git diff --exit-code (only if CRD types or RBAC markers changed)
- [ ] #4 make docs-check && make chart-docs-check (only if docs/ or chart values changed)
<!-- DOD:END -->
