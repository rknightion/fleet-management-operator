---
id: FMO-0005
title: Upgrade Go toolchain to 1.27
status: In Progress
assignee: []
created_date: '2026-08-23 19:06'
updated_date: '2026-08-23 20:21'
labels: []
dependencies: []
ordinal: 5000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Adopt Go 1.27 consistently across the application, nested modules, build images, CI configuration, setup automation, and version-specific contributor documentation.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 All active Go module and toolchain pins require Go 1.27
- [x] #2 Build images, CI jobs, setup automation, and current documentation agree with the Go 1.27 requirement
- [x] #3 The repository green-bar validation passes under Go 1.27
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 make lint
- [x] #2 make test
- [x] #3 make manifests generate && git diff --exit-code (only if CRD types or RBAC markers changed)
- [x] #4 make docs-check && make chart-docs-check (only if docs/ or chart values changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Inventory every active Go version pin, including nested modules and container or CI toolchains. 2. Update the pins and version-specific documentation to Go 1.27.0 without changing historical records or fixtures. 3. Run the repository-defined validation gate, review the diff, commit to main, push, and confirm hosted CI.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Local Go 1.27.0 evidence: make test passed all packages and the controller envtest suite on the complete rerun; make lint passed with 0 issues using golangci-lint v2.12.2; root and mock module build, test, and vet passed. No CRD/RBAC markers, docs/ tree, or chart values changed, so conditional generation and docs gates were not required. The first envtest run hit a transient Kubernetes 409 conflict and was not counted as a pass. CodeRabbit was skipped because no application logic or branching code changed.

Exact-head CI run 32662554820 exposed Linux-only analyzer behavior under Go 1.27. golangci-lint was raised to current v2.13.1. Its staticcheck update reports controller-runtime Requeue as deprecated; the repository intentionally requires rate-limited conflict retries and audits that return pattern, so a narrow internal/controller deprecation exclusion preserves behavior instead of replacing it with differently timed RequeueAfter. GOOS=linux v2.13.1 lint then passed with 0 issues.
<!-- SECTION:NOTES:END -->
