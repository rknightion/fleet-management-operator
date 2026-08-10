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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
)

// TestReconcileMigratesDespiteObservedGeneration proves that a pending
// name-scope migration bypasses the observedGeneration short-circuit. The
// pipeline is fully synced (ObservedGeneration == Generation, Ready=Synced), so
// without the namePending bypass the reconcile would skip and never migrate.
func TestReconcileMigratesDespiteObservedGeneration(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := fleetmanagementv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add to scheme: %v", err)
	}

	pipeline := &fleetmanagementv1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "cr",
			Namespace:  migTestNS,
			Generation: 1,
			Finalizers: []string{pipelineFinalizer},
		},
		Spec: fleetmanagementv1alpha1.PipelineSpec{Name: migBaseName, Contents: "x", Enabled: new(true)},
		Status: fleetmanagementv1alpha1.PipelineStatus{
			ID:                 migOldID,
			SyncedName:         migBaseName, // currently unscoped in Fleet
			ObservedGeneration: 1,           // == Generation, so the guard would normally skip
			Conditions: []metav1.Condition{{
				Type:               conditionTypeReady,
				Status:             metav1.ConditionTrue,
				Reason:             reasonSynced,
				LastTransitionTime: metav1.Now(),
			}},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pipeline).
		WithStatusSubresource(&fleetmanagementv1alpha1.Pipeline{}).
		Build()

	fc := &recordingFleetClient{}
	r := &PipelineReconciler{Client: cl, Scheme: scheme, FleetClient: fc, NameScope: fleetmanagementv1alpha1.NameScopeNamespace}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: migTestNS, Name: "cr"},
	}); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	// The migration must have run (guard bypassed): old pipeline deleted by ID,
	// then recreated under the scoped name.
	if len(fc.deletes) != 1 || fc.deletes[0] != migOldID {
		t.Fatalf("expected delete of %q (migration bypassing observedGeneration guard), got %v", migOldID, fc.deletes)
	}
	var sawScopedCreate bool
	for _, u := range fc.upserts {
		if !u.validateOnly && u.name == migScopedName {
			sawScopedCreate = true
		}
	}
	if !sawScopedCreate {
		t.Fatalf("expected a non-dry-run upsert of %q, got %v", migScopedName, fc.upserts)
	}

	// Status should now track the scoped name.
	var got fleetmanagementv1alpha1.Pipeline
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: migTestNS, Name: "cr"}, &got); err != nil {
		t.Fatalf("get after reconcile: %v", err)
	}
	if got.Status.SyncedName != migScopedName {
		t.Errorf("status.syncedName = %q, want %q", got.Status.SyncedName, migScopedName)
	}
}
