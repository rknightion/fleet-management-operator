# Roadmap: Fleet Management Operator

## Milestones

- ✅ **v1.0 Tech Debt Cleanup** - Phases 1-4 (shipped 2026-02-09)
- 🚧 **v1.1 Best Practices Audit** - Phases 5-7 (in progress)

## Phases

<details>
<summary>✅ v1.0 Tech Debt Cleanup (Phases 1-4) - SHIPPED 2026-02-09</summary>

- [x] Phase 1: Client Layer Error Foundation (2/2 plans) - completed 2026-02-08
- [x] Phase 2: Controller Error Handling (2/2 plans) - completed 2026-02-08
- [x] Phase 3: Logging & Quality (2/2 plans) - completed 2026-02-08
- [x] Phase 4: E2E for GitHub Actions (3/3 plans) - completed 2026-02-09

**See:** `.planning/milestones/v1.0-ROADMAP.md` for full phase details

</details>

### 🚧 v1.1 Best Practices Audit (In Progress)

**Milestone Goal:** Verify and optimize operator implementation against Kubernetes controller best practices, focusing on API server efficiency patterns.

#### Phase 5: Informer Cache Audit
**Goal**: Verify all read operations use informer cache, eliminating direct API server calls
**Depends on**: Phase 4
**Requirements**: CACHE-01, CACHE-02, CACHE-03
**Success Criteria** (what must be TRUE):
  1. Code audit confirms zero List() calls bypass informer cache in controller reconciliation
  2. All Get/List operations use cached client from manager.GetClient()
  3. Code comments document cache usage rationale for each read operation
**Plans:** 1 plan

Plans:
- [ ] 05-01-PLAN.md -- Audit and document informer cache usage with verification tests

#### Phase 6: Reconcile Loop Optimization
**Goal**: Minimize API server calls in reconcile loop and verify status update patterns
**Depends on**: Phase 5
**Requirements**: RECON-01, RECON-02, RECON-03, RECON-04, RECON-05
**Success Criteria** (what must be TRUE):
  1. Every Get/Update in reconcile loop has documented justification comment
  2. All status updates use Status().Update(), never Update() on full resource
  3. ObservedGeneration pattern verified to skip unchanged specs
  4. No redundant Get after Create/Update operations (returned object reused)
  5. Finalizer removal makes exactly two API calls: one Get, one Update
**Plans:** 1 plan

Plans:
- [ ] 06-01-PLAN.md -- Audit reconcile loop API calls and write verification tests

#### Phase 7: Watch Pattern Tuning
**Goal**: Production-ready watch configuration with appropriate resync, rate limiting, and exponential backoff
**Depends on**: Phase 6
**Requirements**: WATCH-01, WATCH-02, WATCH-03, WATCH-04
**Success Criteria** (what must be TRUE):
  1. Resync period configuration documented with rationale for chosen value
  2. Workqueue rate limiter tuned for production (no defaults without justification)
  3. Exponential backoff configured to handle transient Fleet Management API errors
  4. No watch storm scenarios identified in audit (single event triggering rapid reconciles)
**Plans:** 1 plan

Plans:
- [ ] 07-01-PLAN.md -- Audit watch patterns and add verification tests

## Progress

**Execution Order:**
Phases execute in numeric order: 5 -> 6 -> 7

| Phase | Milestone | Plans Complete | Status | Completed |
|-------|-----------|----------------|--------|-----------|
| 1. Client Layer Error Foundation | v1.0 | 2/2 | Complete | 2026-02-08 |
| 2. Controller Error Handling | v1.0 | 2/2 | Complete | 2026-02-08 |
| 3. Logging & Quality | v1.0 | 2/2 | Complete | 2026-02-08 |
| 4. E2E for GitHub Actions | v1.0 | 3/3 | Complete | 2026-02-09 |
| 5. Informer Cache Audit | v1.1 | 0/1 | Not started | - |
| 6. Reconcile Loop Optimization | v1.1 | 0/1 | Not started | - |
| 7. Watch Pattern Tuning | v1.1 | 0/1 | Not started | - |

---
*Roadmap created: 2026-02-08*
*Last updated: 2026-02-09 after phase 7 planning*
