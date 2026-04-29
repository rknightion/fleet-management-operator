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

package fleetclient

import (
	"context"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	pipelinev1 "github.com/grafana/fleet-management-api/api/gen/proto/go/pipeline/v1"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

// histogramSampleCount returns the cumulative sample count for the package's
// rate-limit-wait histogram by collecting one snapshot via the prometheus
// Collector interface.
func histogramSampleCount(t *testing.T, h prometheus.Histogram) uint64 {
	t.Helper()
	ch := make(chan prometheus.Metric, 1)
	h.Collect(ch)
	close(ch)
	m := <-ch
	require.NotNil(t, m, "expected one Metric from histogram Collect")
	var pb dto.Metric
	require.NoError(t, m.Write(&pb))
	require.NotNil(t, pb.Histogram, "metric did not carry histogram data")
	return pb.Histogram.GetSampleCount()
}

// TestRateLimitInterceptor_ObservesOnSuccess confirms the wait-time histogram
// fires for the success path (baseline behaviour).
func TestRateLimitInterceptor_ObservesOnSuccess(t *testing.T) {
	limiter := rate.NewLimiter(rate.Limit(1000), 100)
	interceptor := rateLimitInterceptor(limiter)

	before := histogramSampleCount(t, fleetAPIRateLimiterWait)

	called := false
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		called = true
		return connect.NewResponse(&pipelinev1.Pipeline{}), nil
	})
	wrapped := interceptor(next)

	req := connect.NewRequest(&pipelinev1.UpsertPipelineRequest{})
	_, err := wrapped(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, called, "next was not called on success path")

	after := histogramSampleCount(t, fleetAPIRateLimiterWait)
	assert.Equal(t, uint64(1), after-before,
		"expected exactly one new sample on the rate-limit wait histogram (success)")
}

// TestRateLimitInterceptor_ObservesOnError verifies the bug fix: the wait
// histogram MUST be observed even when limiter.Wait returns an error, e.g.
// when the context is cancelled mid-wait during shutdown. Without the fix,
// long waits cancelled by ctx cancellation would never be recorded — making
// the limiter look free in dashboards even when it was the dominant cost.
func TestRateLimitInterceptor_ObservesOnError(t *testing.T) {
	// rps=0 with burst=0 makes Wait block until ctx is cancelled.
	limiter := rate.NewLimiter(rate.Limit(0), 0)
	interceptor := rateLimitInterceptor(limiter)

	before := histogramSampleCount(t, fleetAPIRateLimiterWait)

	called := false
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		called = true
		return connect.NewResponse(&pipelinev1.Pipeline{}), nil
	})
	wrapped := interceptor(next)

	// Pre-cancelled context guarantees Wait returns an error immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := connect.NewRequest(&pipelinev1.UpsertPipelineRequest{})
	_, err := wrapped(ctx, req)
	require.Error(t, err, "expected limiter.Wait to error when ctx is cancelled")
	assert.False(t, called, "next must NOT be called when limiter errors")

	after := histogramSampleCount(t, fleetAPIRateLimiterWait)
	assert.Equal(t, uint64(1), after-before,
		"wait histogram must observe a sample on the error path; otherwise "+
			"shutdown-cancel waits and ctx-deadline expirations are invisible")
}

// TestRateLimitInterceptor_ObservesOnDeadline simulates a more realistic
// scenario: a short context deadline expires while Wait is still queued.
// The histogram must still record the (positive) wait duration that elapsed.
func TestRateLimitInterceptor_ObservesOnDeadline(t *testing.T) {
	limiter := rate.NewLimiter(rate.Limit(0), 0)
	interceptor := rateLimitInterceptor(limiter)

	before := histogramSampleCount(t, fleetAPIRateLimiterWait)

	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&pipelinev1.Pipeline{}), nil
	})
	wrapped := interceptor(next)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	req := connect.NewRequest(&pipelinev1.UpsertPipelineRequest{})
	_, err := wrapped(ctx, req)
	require.Error(t, err)

	after := histogramSampleCount(t, fleetAPIRateLimiterWait)
	assert.Equal(t, uint64(1), after-before,
		"wait histogram must observe a sample even when deadline expires")
}
