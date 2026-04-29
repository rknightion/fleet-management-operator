# Scale Tests

Scaffolding for 1%-scale (300 Collectors) regression tests of the
fleet-management-operator. Calibrated for a 30k-collector production target,
so 1% is 300 — small enough to run in CI, big enough to catch O(N) regressions
that unit tests miss.

## Status

**These are placeholder stubs.** Each test calls `t.Skip(notImplementedMsg)`
with a description of the assertion it will make once the envtest fixture is
wired up. Running the build-tagged invocation prints "SKIP" per test with a
pointer to the scorecard finding TEST-08.

The scaffolding exists so:

- The test file compiles and stays in lockstep with the production code as
  signatures evolve.
- Future work has a concrete shape to fill in (test names, assertions, target
  numbers) rather than a blank file.
- Reviewers can see what shape the scale-test plan takes without reading the
  audit document.

## Running

```sh
go test -tags=scale ./test/scale/ -v -timeout=10m
```

Not included in `make test`. The `scale` build tag keeps these out of the
default unit suite.

## Implementing the placeholders

See `TEST-08` in `docs/superpowers/audits/2026-04-28-production-readiness-scorecard.md`
for the calibration rationale (30k production fleet → 1% = 300 Collectors,
which fits comfortably in envtest with the existing CRD path).

Each placeholder's godoc describes the exact assertion and threshold. The
shared envtest fixture pattern lives in `internal/controller/suite_test.go` —
spin up envtest with the CRD path, register the relevant reconcilers against
mocks, then exercise the scale fixture via the K8s client.
