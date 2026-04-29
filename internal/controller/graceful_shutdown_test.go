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

package controller

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
	"github.com/grafana/fleet-management-operator/pkg/fleetclient"
)

// stallingFleetClient blocks UpsertPipeline / DeletePipeline until the call's
// context is cancelled, mimicking a Fleet API server that has stopped
// responding (the same wedge condition that motivates REC-04 and the 30s
// HTTP client timeout in pkg/fleetclient).
//
// It signals startedC when a call is in flight so tests can synchronise on
// "we are wedged" before triggering shutdown.
type stallingFleetClient struct {
	startedOnce sync.Once
	startedC    chan struct{}
}

func newStallingFleetClient() *stallingFleetClient {
	return &stallingFleetClient{startedC: make(chan struct{})}
}

func (s *stallingFleetClient) markStarted() {
	s.startedOnce.Do(func() { close(s.startedC) })
}

func (s *stallingFleetClient) UpsertPipeline(ctx context.Context, _ *fleetclient.UpsertPipelineRequest) (*fleetclient.Pipeline, error) {
	s.markStarted()
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *stallingFleetClient) DeletePipeline(ctx context.Context, _ string) error {
	s.markStarted()
	<-ctx.Done()
	return ctx.Err()
}

// TestGracefulShutdown_ReconcileObservesContextCancellation drives a real
// PipelineReconciler against a fake K8s client and a stalling Fleet client.
// REC-04: when the controller-runtime manager cancels its root context (as it
// does on SIGTERM), every in-flight Reconcile must observe the cancellation
// via its own ctx parameter and propagate it through to FleetClient.UpsertPipeline.
//
// This test catches the regression where any link in the chain (reconciler,
// client wrapper, interceptor) accidentally swallows the parent ctx and
// substitutes context.Background() — which would let a wedged Fleet API call
// outlive the manager and block process exit past the pod's terminationGracePeriod.
func TestGracefulShutdown_ReconcileObservesContextCancellation(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, fleetmanagementv1alpha1.AddToScheme(scheme))

	pipeline := &fleetmanagementv1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "wedged-pipeline",
			Namespace:  "default",
			Generation: 1,
			Finalizers: []string{pipelineFinalizer},
		},
		Spec: fleetmanagementv1alpha1.PipelineSpec{
			Contents:   "prometheus.scrape \"default\" {}",
			ConfigType: fleetmanagementv1alpha1.ConfigTypeAlloy,
			Enabled:    true,
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&fleetmanagementv1alpha1.Pipeline{}).
		WithObjects(pipeline).
		Build()

	stalling := newStallingFleetClient()
	r := &PipelineReconciler{
		Client:      k8sClient,
		Scheme:      scheme,
		FleetClient: stalling,
	}

	parentCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type result struct {
		res ctrl.Result
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := r.Reconcile(parentCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: "default", Name: "wedged-pipeline"},
		})
		done <- result{res: res, err: err}
	}()

	// Wait for the reconciler to enter UpsertPipeline. Without this barrier
	// the test could race past the wedge and the cancel below would land
	// before any call is in flight, defeating the point of the test.
	select {
	case <-stalling.startedC:
	case <-time.After(5 * time.Second):
		t.Fatal("Reconcile did not enter UpsertPipeline within 5s")
	}

	// Simulate manager.Stop / SIGTERM cancelling the root reconcile context.
	cancel()

	// REC-04 invariant: Reconcile must return promptly after its ctx cancels.
	// 5s is well within the 30s pod terminationGracePeriod the chart sets.
	// Whether the returned error is nil or wraps context.Canceled is an
	// implementation choice (this controller treats Canceled as non-transient
	// and returns nil to skip workqueue backoff); what matters is that the
	// reconcile *unblocks* — proving the cancellation propagated through the
	// FleetClient call back into the reconciler.
	select {
	case got := <-done:
		// On the cancellation path the reconciler should not request a requeue
		// (it would just refire and re-block on the still-stalled client).
		assert.Equal(t, ctrl.Result{}, got.res, "ctx-cancelled reconcile must not request requeue")
		_ = got.err // accepted: nil or context.Canceled — see comment above
	case <-time.After(5 * time.Second):
		t.Fatal("Reconcile did not return within 5s of ctx cancellation; FleetClient call probably ignored its parent context")
	}
}
