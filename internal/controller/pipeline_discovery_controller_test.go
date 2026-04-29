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
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
	"github.com/grafana/fleet-management-operator/pkg/fleetclient"
)

// fakePipelineDiscoveryClient is an in-memory implementation of
// PipelineDiscoveryFleetClient for use in unit tests.
type fakePipelineDiscoveryClient struct {
	mu sync.Mutex

	// pipelines is the list returned by ListPipelines.
	pipelines []*fleetclient.Pipeline

	// listErr, if non-nil, is returned as the error from ListPipelines.
	listErr error

	// lastReq records the most recent ListPipelinesRequest for assertion.
	lastReq *fleetclient.ListPipelinesRequest

	// callCount tracks the number of ListPipelines invocations.
	callCount int
}

func (f *fakePipelineDiscoveryClient) ListPipelines(_ context.Context, req *fleetclient.ListPipelinesRequest) ([]*fleetclient.Pipeline, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++
	f.lastReq = req
	if f.listErr != nil {
		return nil, f.listErr
	}
	// Return a shallow copy so callers don't mutate the stored slice.
	out := make([]*fleetclient.Pipeline, len(f.pipelines))
	copy(out, f.pipelines)
	return out, nil
}

func (f *fakePipelineDiscoveryClient) getCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callCount
}

func (f *fakePipelineDiscoveryClient) getLastReq() *fleetclient.ListPipelinesRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastReq
}

// fleetPipeline is a small constructor so test setup reads cleanly.
func fleetPipeline(id, name, contents string, opts ...func(*fleetclient.Pipeline)) *fleetclient.Pipeline {
	p := &fleetclient.Pipeline{
		ID:         id,
		Name:       name,
		Contents:   contents,
		Enabled:    true,
		ConfigType: "CONFIG_TYPE_ALLOY",
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func withPipelineMatchers(matchers []string) func(*fleetclient.Pipeline) {
	return func(p *fleetclient.Pipeline) { p.Matchers = matchers }
}

// newPipelineDiscoveryTestScheme builds a runtime.Scheme for use with the
// fake client. Registers both the standard k8s types and the operator's CRDs.
func newPipelineDiscoveryTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, v1alpha1.AddToScheme(s))
	return s
}

// newPipelineDiscoveryReconciler builds a reconciler backed by a fake k8s
// client and the supplied mock fleet client.
func newPipelineDiscoveryReconciler(
	t *testing.T,
	fakeFleet *fakePipelineDiscoveryClient,
	initObjs ...client.Object,
) (*PipelineDiscoveryReconciler, client.Client) {
	t.Helper()
	s := newPipelineDiscoveryTestScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(initObjs...).
		WithStatusSubresource(&v1alpha1.PipelineDiscovery{}).
		Build()

	r := &PipelineDiscoveryReconciler{
		Client:      fakeClient,
		Scheme:      s,
		FleetClient: fakeFleet,
	}
	return r, fakeClient
}

func pdKey(namespace, name string) types.NamespacedName { //nolint:unparam
	return types.NamespacedName{Namespace: namespace, Name: name}
}

func reconcileN(t *testing.T, r *PipelineDiscoveryReconciler, key types.NamespacedName, n int) ctrl.Result { //nolint:unparam
	t.Helper()
	var result ctrl.Result
	var err error
	for range n {
		result, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
		require.NoError(t, err)
	}
	return result
}

// newPD creates a PipelineDiscovery CR with common test defaults.
func newPD(namespace, name string, spec v1alpha1.PipelineDiscoverySpec) *v1alpha1.PipelineDiscovery { //nolint:unparam
	return &v1alpha1.PipelineDiscovery{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: spec,
	}
}

// --- Tests ---

// TestPipelineDiscovery_CreatesPipelineCRs verifies that two Fleet pipelines
// result in two Pipeline CRs with correct labels, annotations, and spec.
func TestPipelineDiscovery_CreatesPipelineCRs(t *testing.T) {
	fakeFleet := &fakePipelineDiscoveryClient{
		pipelines: []*fleetclient.Pipeline{
			fleetPipeline("id-1", "pipeline-one", "loki.source.api \"default\" {}",
				withPipelineMatchers([]string{"env=prod"})),
			fleetPipeline("id-2", "pipeline-two", "prometheus.scrape \"default\" {}"),
		},
	}

	pd := newPD("default", "pd-test", v1alpha1.PipelineDiscoverySpec{
		PollInterval: "5m",
		ImportMode:   v1alpha1.PipelineDiscoveryImportModeAdopt,
	})
	r, c := newPipelineDiscoveryReconciler(t, fakeFleet, pd)

	reconcileN(t, r, pdKey("default", "pd-test"), 1)

	var pipelines v1alpha1.PipelineList
	require.NoError(t, c.List(context.Background(), &pipelines,
		client.InNamespace("default"),
		client.MatchingLabels{v1alpha1.PipelineDiscoveryNameLabel: "pd-test"},
	))
	assert.Len(t, pipelines.Items, 2)

	// Build a map for deterministic assertions.
	byName := map[string]v1alpha1.Pipeline{}
	for _, p := range pipelines.Items {
		byName[p.Spec.Name] = p
	}

	p1, ok := byName["pipeline-one"]
	require.True(t, ok, "pipeline-one not found")
	assert.Equal(t, "loki.source.api \"default\" {}", p1.Spec.Contents)
	assert.Equal(t, []string{"env=prod"}, p1.Spec.Matchers)
	assert.Equal(t, v1alpha1.ConfigTypeAlloy, p1.Spec.ConfigType)
	assert.True(t, p1.Spec.Enabled)
	assert.Equal(t, v1alpha1.PipelineDiscoveryNameLabel, v1alpha1.PipelineDiscoveryNameLabel)
	assert.Equal(t, "pd-test", p1.Labels[v1alpha1.PipelineDiscoveryNameLabel])
	assert.Equal(t, "default/pd-test", p1.Annotations[v1alpha1.PipelineDiscoveredByAnnotation])
	assert.Equal(t, "id-1", p1.Annotations[v1alpha1.FleetPipelineIDAnnotation])
	assert.False(t, p1.Spec.Paused, "Adopt mode should leave Paused=false")

	p2, ok := byName["pipeline-two"]
	require.True(t, ok, "pipeline-two not found")
	assert.Equal(t, "id-2", p2.Annotations[v1alpha1.FleetPipelineIDAnnotation])
}

// TestPipelineDiscovery_ReadOnlyMode verifies that ImportMode=ReadOnly sets
// spec.paused=true on created Pipeline CRs.
func TestPipelineDiscovery_ReadOnlyMode(t *testing.T) {
	fakeFleet := &fakePipelineDiscoveryClient{
		pipelines: []*fleetclient.Pipeline{
			fleetPipeline("id-ro", "readonly-pipeline", "some.config {}"),
		},
	}

	pd := newPD("default", "pd-readonly", v1alpha1.PipelineDiscoverySpec{
		PollInterval: "5m",
		ImportMode:   v1alpha1.PipelineDiscoveryImportModeReadOnly,
	})
	r, c := newPipelineDiscoveryReconciler(t, fakeFleet, pd)

	reconcileN(t, r, pdKey("default", "pd-readonly"), 1)

	var pipelines v1alpha1.PipelineList
	require.NoError(t, c.List(context.Background(), &pipelines,
		client.InNamespace("default"),
		client.MatchingLabels{v1alpha1.PipelineDiscoveryNameLabel: "pd-readonly"},
	))
	require.Len(t, pipelines.Items, 1)
	assert.True(t, pipelines.Items[0].Spec.Paused, "ReadOnly import mode must set Paused=true")
}

// TestPipelineDiscovery_AdoptMode verifies that ImportMode=Adopt sets
// spec.paused=false on created Pipeline CRs.
func TestPipelineDiscovery_AdoptMode(t *testing.T) {
	fakeFleet := &fakePipelineDiscoveryClient{
		pipelines: []*fleetclient.Pipeline{
			fleetPipeline("id-adopt", "adopt-pipeline", "some.config {}"),
		},
	}

	pd := newPD("default", "pd-adopt", v1alpha1.PipelineDiscoverySpec{
		PollInterval: "5m",
		ImportMode:   v1alpha1.PipelineDiscoveryImportModeAdopt,
	})
	r, c := newPipelineDiscoveryReconciler(t, fakeFleet, pd)

	reconcileN(t, r, pdKey("default", "pd-adopt"), 1)

	var pipelines v1alpha1.PipelineList
	require.NoError(t, c.List(context.Background(), &pipelines,
		client.InNamespace("default"),
		client.MatchingLabels{v1alpha1.PipelineDiscoveryNameLabel: "pd-adopt"},
	))
	require.Len(t, pipelines.Items, 1)
	assert.False(t, pipelines.Items[0].Spec.Paused, "Adopt import mode must set Paused=false")
}

// TestPipelineDiscovery_ScheduleCheck verifies that a second reconcile within
// the poll interval skips the ListPipelines call.
func TestPipelineDiscovery_ScheduleCheck(t *testing.T) {
	now := time.Now()
	fakeFleet := &fakePipelineDiscoveryClient{
		pipelines: []*fleetclient.Pipeline{
			fleetPipeline("id-sched", "sched-pipeline", "some.config {}"),
		},
	}

	pd := newPD("default", "pd-sched", v1alpha1.PipelineDiscoverySpec{
		PollInterval: "5m",
	})
	r, _ := newPipelineDiscoveryReconciler(t, fakeFleet, pd)
	// Use a fixed clock so we can control the "now".
	r.Now = func() time.Time { return now }

	// First reconcile: should call ListPipelines and set LastSyncTime.
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: pdKey("default", "pd-sched")})
	require.NoError(t, err)
	assert.Equal(t, 1, fakeFleet.getCallCount())

	// Second reconcile with the same time: schedule check should skip ListPipelines.
	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: pdKey("default", "pd-sched")})
	require.NoError(t, err)
	assert.Equal(t, 1, fakeFleet.getCallCount(), "ListPipelines should not be called within poll interval")

	// Advance time past the poll interval: next reconcile should call ListPipelines again.
	r.Now = func() time.Time { return now.Add(6 * time.Minute) }
	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: pdKey("default", "pd-sched")})
	require.NoError(t, err)
	assert.Equal(t, 2, fakeFleet.getCallCount(), "ListPipelines should be called after poll interval expires")
}

// TestPipelineDiscovery_KeepPolicy verifies that a vanished pipeline gets
// the stale annotation but is not deleted when policy=Keep.
func TestPipelineDiscovery_KeepPolicy(t *testing.T) {
	fakeFleet := &fakePipelineDiscoveryClient{
		pipelines: []*fleetclient.Pipeline{
			fleetPipeline("id-keep", "keep-pipeline", "some.config {}"),
		},
	}

	pd := newPD("default", "pd-keep", v1alpha1.PipelineDiscoverySpec{
		PollInterval: "5m",
		Policy:       v1alpha1.PipelineDiscoveryPolicy{OnPipelineRemoved: v1alpha1.PipelineDiscoveryOnRemovedKeep},
	})
	r, c := newPipelineDiscoveryReconciler(t, fakeFleet, pd)

	// First reconcile: pipeline is created.
	reconcileN(t, r, pdKey("default", "pd-keep"), 1)

	var pipelines v1alpha1.PipelineList
	require.NoError(t, c.List(context.Background(), &pipelines,
		client.InNamespace("default"),
		client.MatchingLabels{v1alpha1.PipelineDiscoveryNameLabel: "pd-keep"},
	))
	require.Len(t, pipelines.Items, 1)

	// Pipeline vanishes from Fleet.
	fakeFleet.mu.Lock()
	fakeFleet.pipelines = nil
	fakeFleet.mu.Unlock()

	// Advance past poll interval.
	r.Now = func() time.Time { return time.Now().Add(10 * time.Minute) }

	reconcileN(t, r, pdKey("default", "pd-keep"), 1)

	// CR should still exist with stale annotation.
	require.NoError(t, c.List(context.Background(), &pipelines,
		client.InNamespace("default"),
		client.MatchingLabels{v1alpha1.PipelineDiscoveryNameLabel: "pd-keep"},
	))
	require.Len(t, pipelines.Items, 1)
	assert.Equal(t, v1alpha1.PipelineDiscoveryStaleAnnotationValue,
		pipelines.Items[0].Annotations[v1alpha1.PipelineDiscoveryStaleAnnotation])
}

// TestPipelineDiscovery_DeletePolicy verifies that a vanished pipeline CR is
// deleted when policy=Delete.
func TestPipelineDiscovery_DeletePolicy(t *testing.T) {
	fakeFleet := &fakePipelineDiscoveryClient{
		pipelines: []*fleetclient.Pipeline{
			fleetPipeline("id-del", "del-pipeline", "some.config {}"),
		},
	}

	pd := newPD("default", "pd-delete", v1alpha1.PipelineDiscoverySpec{
		PollInterval: "5m",
		Policy:       v1alpha1.PipelineDiscoveryPolicy{OnPipelineRemoved: v1alpha1.PipelineDiscoveryOnRemovedDelete},
	})
	r, c := newPipelineDiscoveryReconciler(t, fakeFleet, pd)

	// First reconcile: create pipeline.
	reconcileN(t, r, pdKey("default", "pd-delete"), 1)

	var pipelines v1alpha1.PipelineList
	require.NoError(t, c.List(context.Background(), &pipelines,
		client.InNamespace("default"),
		client.MatchingLabels{v1alpha1.PipelineDiscoveryNameLabel: "pd-delete"},
	))
	require.Len(t, pipelines.Items, 1)

	// Pipeline vanishes.
	fakeFleet.mu.Lock()
	fakeFleet.pipelines = nil
	fakeFleet.mu.Unlock()

	r.Now = func() time.Time { return time.Now().Add(10 * time.Minute) }
	reconcileN(t, r, pdKey("default", "pd-delete"), 1)

	require.NoError(t, c.List(context.Background(), &pipelines,
		client.InNamespace("default"),
		client.MatchingLabels{v1alpha1.PipelineDiscoveryNameLabel: "pd-delete"},
	))
	assert.Len(t, pipelines.Items, 0, "stale pipeline CR should be deleted with Delete policy")
}

// TestPipelineDiscovery_StaleCleared verifies that a pipeline reappearing
// in Fleet causes the stale annotation to be removed from its CR.
func TestPipelineDiscovery_StaleCleared(t *testing.T) {
	fakeFleet := &fakePipelineDiscoveryClient{
		pipelines: []*fleetclient.Pipeline{
			fleetPipeline("id-flap", "flap-pipeline", "some.config {}"),
		},
	}

	pd := newPD("default", "pd-flap", v1alpha1.PipelineDiscoverySpec{
		PollInterval: "5m",
		Policy:       v1alpha1.PipelineDiscoveryPolicy{OnPipelineRemoved: v1alpha1.PipelineDiscoveryOnRemovedKeep},
	})
	r, c := newPipelineDiscoveryReconciler(t, fakeFleet, pd)

	// First reconcile: create pipeline.
	reconcileN(t, r, pdKey("default", "pd-flap"), 1)

	// Pipeline vanishes — stale annotation applied.
	fakeFleet.mu.Lock()
	fakeFleet.pipelines = nil
	fakeFleet.mu.Unlock()
	r.Now = func() time.Time { return time.Now().Add(10 * time.Minute) }
	reconcileN(t, r, pdKey("default", "pd-flap"), 1)

	var pipelines v1alpha1.PipelineList
	require.NoError(t, c.List(context.Background(), &pipelines,
		client.InNamespace("default"),
		client.MatchingLabels{v1alpha1.PipelineDiscoveryNameLabel: "pd-flap"},
	))
	require.Len(t, pipelines.Items, 1)
	assert.Equal(t, v1alpha1.PipelineDiscoveryStaleAnnotationValue,
		pipelines.Items[0].Annotations[v1alpha1.PipelineDiscoveryStaleAnnotation])

	// Pipeline reappears.
	fakeFleet.mu.Lock()
	fakeFleet.pipelines = []*fleetclient.Pipeline{
		fleetPipeline("id-flap", "flap-pipeline", "some.config {}"),
	}
	fakeFleet.mu.Unlock()
	r.Now = func() time.Time { return time.Now().Add(20 * time.Minute) }
	reconcileN(t, r, pdKey("default", "pd-flap"), 1)

	require.NoError(t, c.List(context.Background(), &pipelines,
		client.InNamespace("default"),
		client.MatchingLabels{v1alpha1.PipelineDiscoveryNameLabel: "pd-flap"},
	))
	require.Len(t, pipelines.Items, 1)
	assert.Empty(t, pipelines.Items[0].Annotations[v1alpha1.PipelineDiscoveryStaleAnnotation],
		"stale annotation should be cleared when pipeline reappears")
}

// TestPipelineDiscovery_ConflictNotOwnedByDiscovery verifies that an existing
// Pipeline CR without the discovery label is left alone and a conflict is
// recorded.
func TestPipelineDiscovery_ConflictNotOwnedByDiscovery(t *testing.T) {
	// Pre-create a manual Pipeline CR (no discovery label).
	manual := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "manual-pipeline",
			Namespace: "default",
		},
		Spec: v1alpha1.PipelineSpec{
			Name:     "manual-pipeline",
			Contents: "original.content {}",
			Enabled:  true,
		},
	}

	fakeFleet := &fakePipelineDiscoveryClient{
		pipelines: []*fleetclient.Pipeline{
			// Fleet has a pipeline whose sanitized name would be "manual-pipeline".
			fleetPipeline("id-manual", "manual-pipeline", "fleet.content {}"),
		},
	}

	pd := newPD("default", "pd-conflict", v1alpha1.PipelineDiscoverySpec{
		PollInterval: "5m",
	})
	r, c := newPipelineDiscoveryReconciler(t, fakeFleet, manual, pd)

	reconcileN(t, r, pdKey("default", "pd-conflict"), 1)

	// Check PD status has a conflict.
	var freshPD v1alpha1.PipelineDiscovery
	require.NoError(t, c.Get(context.Background(), pdKey("default", "pd-conflict"), &freshPD))
	require.Len(t, freshPD.Status.Conflicts, 1)
	assert.Equal(t, v1alpha1.PipelineDiscoveryConflictNotOwned, freshPD.Status.Conflicts[0].Reason)

	// Manual CR is unchanged.
	var freshManual v1alpha1.Pipeline
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "manual-pipeline"}, &freshManual))
	assert.Empty(t, freshManual.Labels[v1alpha1.PipelineDiscoveryNameLabel])
	assert.Equal(t, "original.content {}", freshManual.Spec.Contents)
}

// TestPipelineDiscovery_ConflictOwnedByOtherDiscovery verifies that a
// Pipeline CR owned by a different PipelineDiscovery produces a conflict.
func TestPipelineDiscovery_ConflictOwnedByOtherDiscovery(t *testing.T) {
	// Pre-create a Pipeline CR owned by a different PipelineDiscovery.
	otherOwned := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "owned-pipeline",
			Namespace: "default",
			Labels: map[string]string{
				v1alpha1.PipelineDiscoveryNameLabel: "other-pd",
			},
			Annotations: map[string]string{
				v1alpha1.FleetPipelineIDAnnotation: "id-owned-other",
			},
		},
		Spec: v1alpha1.PipelineSpec{
			Name:     "owned-pipeline",
			Contents: "other.content {}",
			Enabled:  true,
		},
	}

	fakeFleet := &fakePipelineDiscoveryClient{
		pipelines: []*fleetclient.Pipeline{
			fleetPipeline("id-owned-new", "owned-pipeline", "fleet.content {}"),
		},
	}

	pd := newPD("default", "pd-other-conflict", v1alpha1.PipelineDiscoverySpec{
		PollInterval: "5m",
	})
	r, c := newPipelineDiscoveryReconciler(t, fakeFleet, otherOwned, pd)

	reconcileN(t, r, pdKey("default", "pd-other-conflict"), 1)

	var freshPD v1alpha1.PipelineDiscovery
	require.NoError(t, c.Get(context.Background(), pdKey("default", "pd-other-conflict"), &freshPD))
	require.Len(t, freshPD.Status.Conflicts, 1)
	assert.Equal(t, v1alpha1.PipelineDiscoveryConflictOwnedByOther, freshPD.Status.Conflicts[0].Reason)
}

// TestPipelineDiscovery_SpecImmutability verifies that the controller does NOT
// overwrite user edits to Pipeline CR spec on subsequent reconciles.
func TestPipelineDiscovery_SpecImmutability(t *testing.T) {
	fakeFleet := &fakePipelineDiscoveryClient{
		pipelines: []*fleetclient.Pipeline{
			fleetPipeline("id-immut", "immut-pipeline", "original.content {}"),
		},
	}

	pd := newPD("default", "pd-immut", v1alpha1.PipelineDiscoverySpec{
		PollInterval: "5m",
	})
	r, c := newPipelineDiscoveryReconciler(t, fakeFleet, pd)

	// First reconcile: create pipeline CR.
	reconcileN(t, r, pdKey("default", "pd-immut"), 1)

	// User edits the Pipeline CR's contents.
	var createdPipeline v1alpha1.Pipeline
	require.NoError(t, c.List(context.Background(), &v1alpha1.PipelineList{},
		client.InNamespace("default"),
		client.MatchingLabels{v1alpha1.PipelineDiscoveryNameLabel: "pd-immut"},
	))

	var pipelines v1alpha1.PipelineList
	require.NoError(t, c.List(context.Background(), &pipelines,
		client.InNamespace("default"),
		client.MatchingLabels{v1alpha1.PipelineDiscoveryNameLabel: "pd-immut"},
	))
	require.Len(t, pipelines.Items, 1)
	createdPipeline = pipelines.Items[0]

	// Simulate a user edit.
	createdPipeline.Spec.Contents = "user.edited.content {}"
	require.NoError(t, c.Update(context.Background(), &createdPipeline))

	// Second reconcile past poll interval: controller must NOT overwrite spec.
	r.Now = func() time.Time { return time.Now().Add(10 * time.Minute) }
	reconcileN(t, r, pdKey("default", "pd-immut"), 1)

	require.NoError(t, c.List(context.Background(), &pipelines,
		client.InNamespace("default"),
		client.MatchingLabels{v1alpha1.PipelineDiscoveryNameLabel: "pd-immut"},
	))
	require.Len(t, pipelines.Items, 1)
	assert.Equal(t, "user.edited.content {}", pipelines.Items[0].Spec.Contents,
		"controller must not overwrite user edits to Pipeline CR spec")
}

// TestPipelineDiscovery_ConfigTypeSelector verifies that selector.configType
// is forwarded to the ListPipelines request.
func TestPipelineDiscovery_ConfigTypeSelector(t *testing.T) {
	fakeFleet := &fakePipelineDiscoveryClient{}

	alloy := v1alpha1.ConfigTypeAlloy
	pd := newPD("default", "pd-ct", v1alpha1.PipelineDiscoverySpec{
		PollInterval: "5m",
		Selector: v1alpha1.PipelineDiscoverySelector{
			ConfigType: &alloy,
		},
	})
	r, _ := newPipelineDiscoveryReconciler(t, fakeFleet, pd)

	reconcileN(t, r, pdKey("default", "pd-ct"), 1)

	lastReq := fakeFleet.getLastReq()
	require.NotNil(t, lastReq)
	require.NotNil(t, lastReq.ConfigType)
	assert.Equal(t, "CONFIG_TYPE_ALLOY", *lastReq.ConfigType)
}

// TestPipelineDiscovery_EnabledSelector verifies that selector.enabled is
// forwarded to the ListPipelines request.
func TestPipelineDiscovery_EnabledSelector(t *testing.T) {
	fakeFleet := &fakePipelineDiscoveryClient{}

	enabled := true
	pd := newPD("default", "pd-en", v1alpha1.PipelineDiscoverySpec{
		PollInterval: "5m",
		Selector: v1alpha1.PipelineDiscoverySelector{
			Enabled: &enabled,
		},
	})
	r, _ := newPipelineDiscoveryReconciler(t, fakeFleet, pd)

	reconcileN(t, r, pdKey("default", "pd-en"), 1)

	lastReq := fakeFleet.getLastReq()
	require.NotNil(t, lastReq)
	require.NotNil(t, lastReq.Enabled)
	assert.True(t, *lastReq.Enabled)
}

// TestPipelineDiscovery_ListPipelinesError verifies that a ListPipelines
// failure propagates the error (so controller-runtime applies exponential
// backoff) and sets the Ready condition to False.
func TestPipelineDiscovery_ListPipelinesError(t *testing.T) {
	listErr := fmt.Errorf("simulated network failure")
	fakeFleet := &fakePipelineDiscoveryClient{
		listErr: listErr,
	}

	pd := newPD("default", "pd-err", v1alpha1.PipelineDiscoverySpec{
		PollInterval: "5m",
	})
	r, c := newPipelineDiscoveryReconciler(t, fakeFleet, pd)

	// The reconcile returns the original error so controller-runtime backs off.
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: pdKey("default", "pd-err")})
	// Expect the ListPipelines error to be propagated.
	require.Error(t, err)
	// On error the result should be zero (controller-runtime handles requeue).
	assert.Equal(t, ctrl.Result{}, result)

	var freshPD v1alpha1.PipelineDiscovery
	require.NoError(t, c.Get(context.Background(), pdKey("default", "pd-err"), &freshPD))

	var readyCondition *metav1.Condition
	for i := range freshPD.Status.Conditions {
		if freshPD.Status.Conditions[i].Type == conditionTypeReady {
			readyCondition = &freshPD.Status.Conditions[i]
			break
		}
	}
	require.NotNil(t, readyCondition, "Ready condition should be set after ListPipelines failure")
	assert.Equal(t, metav1.ConditionFalse, readyCondition.Status)
}

// TestPipelineDiscovery_HashSuffixedName verifies that a pipeline name
// requiring sanitization gets a hash-suffixed CR name.
func TestPipelineDiscovery_HashSuffixedName(t *testing.T) {
	fakeFleet := &fakePipelineDiscoveryClient{
		pipelines: []*fleetclient.Pipeline{
			fleetPipeline("id-upper", "My.Pipeline.Name", "some.config {}"),
		},
	}

	pd := newPD("default", "pd-hash", v1alpha1.PipelineDiscoverySpec{
		PollInterval: "5m",
	})
	r, c := newPipelineDiscoveryReconciler(t, fakeFleet, pd)

	reconcileN(t, r, pdKey("default", "pd-hash"), 1)

	var pipelines v1alpha1.PipelineList
	require.NoError(t, c.List(context.Background(), &pipelines,
		client.InNamespace("default"),
		client.MatchingLabels{v1alpha1.PipelineDiscoveryNameLabel: "pd-hash"},
	))
	require.Len(t, pipelines.Items, 1)

	// The CR name should be sanitized (lowercase, hyphens) with a hash suffix
	// since "My.Pipeline.Name" is lossy.
	assert.NotEqual(t, "My.Pipeline.Name", pipelines.Items[0].Name,
		"CR name should be sanitized")
	assert.Equal(t, "id-upper", pipelines.Items[0].Annotations[v1alpha1.FleetPipelineIDAnnotation],
		"Fleet pipeline ID annotation must record the original ID")
}

// TestPipelineDiscovery_TargetNamespace verifies that CRs are created in the
// specified target namespace rather than the PD's own namespace.
func TestPipelineDiscovery_TargetNamespace(t *testing.T) {
	fakeFleet := &fakePipelineDiscoveryClient{
		pipelines: []*fleetclient.Pipeline{
			fleetPipeline("id-ns", "ns-pipeline", "some.config {}"),
		},
	}

	// Create a PD in "default" but targeting "target-ns".
	pd := newPD("default", "pd-ns", v1alpha1.PipelineDiscoverySpec{
		PollInterval:    "5m",
		TargetNamespace: "target-ns",
	})
	r, c := newPipelineDiscoveryReconciler(t, fakeFleet, pd)

	reconcileN(t, r, pdKey("default", "pd-ns"), 1)

	// Nothing in "default".
	var pipelinesDefault v1alpha1.PipelineList
	require.NoError(t, c.List(context.Background(), &pipelinesDefault,
		client.InNamespace("default"),
		client.MatchingLabels{v1alpha1.PipelineDiscoveryNameLabel: "pd-ns"},
	))
	assert.Len(t, pipelinesDefault.Items, 0)

	// One in "target-ns".
	var pipelinesTarget v1alpha1.PipelineList
	require.NoError(t, c.List(context.Background(), &pipelinesTarget,
		client.InNamespace("target-ns"),
		client.MatchingLabels{v1alpha1.PipelineDiscoveryNameLabel: "pd-ns"},
	))
	assert.Len(t, pipelinesTarget.Items, 1)
}
