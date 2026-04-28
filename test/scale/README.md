# Scale Tests

Scaffolding for 1%-scale (300 Collectors) regression tests.

Run manually:
    go test -tags=scale ./test/scale/ -v -timeout=10m

Not included in `make test`. Gate on the `scale` build tag.

## Current status

Tests are scaffolded with t.Skip stubs. See TEST-08 in the scorecard for the
30k-collector calibration context.
