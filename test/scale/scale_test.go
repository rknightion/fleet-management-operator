//go:build scale

/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package scale_test

import "testing"

// The tests in this file are placeholders. Every entry calls t.Skip with a
// description of the assertion it WILL make once the envtest cluster + 300-
// Collector fixture are wired up. The build tag already excludes these from
// `make test`; running `go test -tags=scale` surfaces a clear "not yet
// implemented" message per case rather than silently passing.
//
// Tracking: TEST-08 in docs/superpowers/audits/2026-04-28-production-readiness-scorecard.md.

const notImplementedMsg = "scale test not yet implemented — see TEST-08 in the production-readiness scorecard"

// TestScale_ObservedGenerationShortCircuit verifies that at 1%-scale (300
// Collectors), no-op spec updates do not result in Fleet API calls. Create 300
// Collector CRs, trigger updates that do not change spec (e.g. label
// annotations), and assert that >95% of reconcile cycles skip
// BulkUpdateCollectors entirely by inspecting the mock client call count.
func TestScale_ObservedGenerationShortCircuit(t *testing.T) {
	t.Skip(notImplementedMsg)
}

// TestScale_MemoryBaseline verifies that the controller process does not
// exceed 150 MiB of heap allocation when 300 Collector CRs are live. Create
// 300 Collectors, let the controller reach steady state, then assert
// runtime.MemStats.HeapAlloc < 150*1024*1024.
func TestScale_MemoryBaseline(t *testing.T) {
	t.Skip(notImplementedMsg)
}

// TestScale_ReconcileLatency verifies that p99 reconcile latency stays below
// 2 seconds under light load. Create 10 Pipeline CRs, record wall-clock time
// for 10 back-to-back reconciles (each triggered by a spec bump), sort the
// durations, and assert durations[9] < 2*time.Second.
func TestScale_ReconcileLatency(t *testing.T) {
	t.Skip(notImplementedMsg)
}
