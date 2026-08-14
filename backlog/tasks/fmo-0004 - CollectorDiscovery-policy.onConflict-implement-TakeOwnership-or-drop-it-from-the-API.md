---
id: FMO-0004
title: >-
  CollectorDiscovery policy.onConflict: implement TakeOwnership or drop it from
  the API
status: To Do
assignee: []
created_date: '2026-08-14 16:39'
labels:
  - discovery
  - api
dependencies: []
ordinal: 4000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
CollectorDiscovery's spec.policy.onConflict currently validates to exactly one value, Skip. TakeOwnership is reserved in the docs for v2 but is not accepted by the webhook, so today the field is a single-valued enum that cannot express a choice - it reads to a user like a knob that does nothing.

Two honest resolutions, and either is better than leaving it:
- implement TakeOwnership, which means defining what happens when discovery finds a Collector CR it did not create whose spec.id matches a discovered collector - specifically whether user-set spec.remoteAttributes survive, because the whole reason discovery uses labels and annotations rather than OwnerReferences is that cascade-delete must not clobber them;
- or remove the field from v1alpha1 and reintroduce it when there is a second value, which costs a CRD surface change but stops promising something that does not exist.

Decide before implementing. The precedent in this repo is that discovery never modifies a Collector CR's spec after creation, and TakeOwnership as usually understood would break that invariant.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Decision recorded: implement TakeOwnership or remove onConflict from v1alpha1, with the reason
- [ ] #2 If implemented: the spec-discipline invariant (discovery never modifies a Collector spec after creation) is either preserved or the departure is documented
- [ ] #3 Webhook validation and its table-driven tests match whichever set of values is now legal
- [ ] #4 AGENTS.md and the CollectorDiscovery docs no longer describe a reserved value that does not exist
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make lint
- [ ] #2 make test
- [ ] #3 make manifests generate && git diff --exit-code (only if CRD types or RBAC markers changed)
- [ ] #4 make docs-check && make chart-docs-check (only if docs/ or chart values changed)
<!-- DOD:END -->
