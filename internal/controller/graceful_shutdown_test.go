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

package controller_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fleetclient "github.com/grafana/fleet-management-operator/pkg/fleetclient"
)

// slowFleetClient blocks UpsertPipeline until context cancels,
// simulating a slow or stalled Fleet API call.
type slowFleetClient struct{}

// Verify interface implementation at compile time.
var _ interface {
	UpsertPipeline(ctx context.Context, req *fleetclient.UpsertPipelineRequest) (*fleetclient.Pipeline, error)
	DeletePipeline(ctx context.Context, id string) error
} = &slowFleetClient{}

func (s *slowFleetClient) UpsertPipeline(ctx context.Context, req *fleetclient.UpsertPipelineRequest) (*fleetclient.Pipeline, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *slowFleetClient) DeletePipeline(_ context.Context, _ string) error {
	return nil
}

// TestGracefulShutdown_ContextCancellationPropagates asserts that context
// cancellation (as triggered by SIGTERM via controller-runtime's graceful
// shutdown path, REC-04) propagates through to in-flight Fleet API calls.
//
// Invariant: a goroutine blocked inside UpsertPipeline must unblock within
// 5 seconds of context cancellation, and the returned error must be
// context.Canceled.
func TestGracefulShutdown_ContextCancellationPropagates(t *testing.T) {
	client := &slowFleetClient{}

	ctx, cancel := context.WithCancel(context.Background())

	type result struct {
		pipeline *fleetclient.Pipeline
		err      error
	}
	done := make(chan result, 1)

	go func() {
		p, err := client.UpsertPipeline(ctx, &fleetclient.UpsertPipelineRequest{
			Pipeline: &fleetclient.Pipeline{
				Name:     "test-pipeline",
				Contents: "prometheus.scrape \"default\" { }",
				Enabled:  true,
			},
		})
		done <- result{pipeline: p, err: err}
	}()

	// Cancel the context, simulating SIGTERM propagation.
	cancel()

	select {
	case res := <-done:
		require.Error(t, res.err, "expected error after context cancellation")
		assert.Nil(t, res.pipeline)
		assert.ErrorIs(t, res.err, context.Canceled, "error must be context.Canceled")
	case <-time.After(5 * time.Second):
		t.Fatal("UpsertPipeline did not return within 5 seconds of context cancellation")
	}
}
