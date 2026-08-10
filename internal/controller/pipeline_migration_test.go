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
	"errors"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
	"github.com/grafana/fleet-management-operator/pkg/fleetclient"
)

const (
	migTestNS     = "team-a"
	migBaseName   = "mypipe"
	migOldID      = "old-id"
	migScopedName = "team-a.mypipe"
)

type recordedUpsert struct {
	name         string
	validateOnly bool
}

// recordingFleetClient records the sequence of Fleet calls so migration tests
// can assert dry-run -> delete -> recreate ordering. It satisfies
// FleetPipelineClient.
type recordingFleetClient struct {
	upserts   []recordedUpsert
	deletes   []string
	nextID    int
	dryRunErr error
	upsertErr error
	deleteErr error
}

func (c *recordingFleetClient) GetPipeline(_ context.Context, _ string) (*fleetclient.Pipeline, error) {
	return nil, errors.New("unexpected GetPipeline")
}

func (c *recordingFleetClient) UpsertPipeline(_ context.Context, req *fleetclient.UpsertPipelineRequest) (*fleetclient.Pipeline, error) {
	c.upserts = append(c.upserts, recordedUpsert{name: req.Pipeline.Name, validateOnly: req.ValidateOnly})
	if req.ValidateOnly {
		if c.dryRunErr != nil {
			return nil, c.dryRunErr
		}
		out := *req.Pipeline
		return &out, nil
	}
	if c.upsertErr != nil {
		return nil, c.upsertErr
	}
	c.nextID++
	out := *req.Pipeline
	out.ID = fmt.Sprintf("new-id-%d", c.nextID)
	return &out, nil
}

func (c *recordingFleetClient) DeletePipeline(_ context.Context, id string) error {
	c.deletes = append(c.deletes, id)
	return c.deleteErr
}

func scopedReconciler(fc FleetPipelineClient, scope string) *PipelineReconciler {
	return &PipelineReconciler{FleetClient: fc, NameScope: scope}
}

func migPipeline(specName, statusID, syncedName string, annotations map[string]string) *fleetmanagementv1alpha1.Pipeline {
	return &fleetmanagementv1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "cr", Namespace: migTestNS, Annotations: annotations},
		Spec:       fleetmanagementv1alpha1.PipelineSpec{Name: specName, Contents: "x", Enabled: new(true)},
		Status:     fleetmanagementv1alpha1.PipelineStatus{ID: statusID, SyncedName: syncedName},
	}
}

func TestBuildUpsertRequestNameScope(t *testing.T) {
	tests := []struct {
		name        string
		scope       string
		annotations map[string]string
		want        string
	}{
		{"no scope keeps base", fleetmanagementv1alpha1.NameScopeNone, nil, migBaseName},
		{"namespace scope prefixes", fleetmanagementv1alpha1.NameScopeNamespace, nil, migScopedName},
		{"discovered keeps base under scope", fleetmanagementv1alpha1.NameScopeNamespace, map[string]string{fleetmanagementv1alpha1.FleetPipelineIDAnnotation: "id1"}, migBaseName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := scopedReconciler(&recordingFleetClient{}, tt.scope)
			p := migPipeline(migBaseName, "", "", tt.annotations)
			req := r.buildUpsertRequest(p)
			if req.Pipeline.Name != tt.want {
				t.Errorf("buildUpsertRequest name = %q, want %q", req.Pipeline.Name, tt.want)
			}
		})
	}
}

func TestBackfillSyncedName(t *testing.T) {
	// status.ID set, SyncedName empty -> backfill to unscoped base
	p := migPipeline(migBaseName, migOldID, "", nil)
	backfillSyncedName(p)
	if p.Status.SyncedName != migBaseName {
		t.Errorf("backfill = %q, want %q", p.Status.SyncedName, migBaseName)
	}
	// already set -> unchanged
	p2 := migPipeline(migBaseName, migOldID, migScopedName, nil)
	backfillSyncedName(p2)
	if p2.Status.SyncedName != migScopedName {
		t.Errorf("backfill clobbered existing SyncedName: %q", p2.Status.SyncedName)
	}
	// no status.ID (never synced) -> unchanged (empty)
	p3 := migPipeline(migBaseName, "", "", nil)
	backfillSyncedName(p3)
	if p3.Status.SyncedName != "" {
		t.Errorf("backfill set SyncedName with no ID: %q", p3.Status.SyncedName)
	}
}

func TestMigratePipelineName(t *testing.T) {
	ctx := context.Background()

	t.Run("no migration when never synced", func(t *testing.T) {
		fc := &recordingFleetClient{}
		r := scopedReconciler(fc, fleetmanagementv1alpha1.NameScopeNamespace)
		p := migPipeline(migBaseName, "", "", nil)
		migrated, err := r.migratePipelineName(ctx, p)
		if migrated || err != nil || len(fc.upserts) != 0 || len(fc.deletes) != 0 {
			t.Fatalf("expected no migration, got migrated=%v err=%v upserts=%v deletes=%v", migrated, err, fc.upserts, fc.deletes)
		}
	})

	t.Run("no migration when name unchanged", func(t *testing.T) {
		fc := &recordingFleetClient{}
		r := scopedReconciler(fc, fleetmanagementv1alpha1.NameScopeNamespace)
		p := migPipeline(migBaseName, migOldID, migScopedName, nil)
		migrated, err := r.migratePipelineName(ctx, p)
		if migrated || err != nil || len(fc.deletes) != 0 {
			t.Fatalf("expected no migration, got migrated=%v err=%v deletes=%v", migrated, err, fc.deletes)
		}
	})

	t.Run("migrates: dry-run then delete then zero ID", func(t *testing.T) {
		fc := &recordingFleetClient{}
		r := scopedReconciler(fc, fleetmanagementv1alpha1.NameScopeNamespace)
		p := migPipeline(migBaseName, migOldID, migBaseName, nil)
		migrated, err := r.migratePipelineName(ctx, p)
		if !migrated || err != nil {
			t.Fatalf("expected migration, got migrated=%v err=%v", migrated, err)
		}
		if len(fc.upserts) != 1 || !fc.upserts[0].validateOnly || fc.upserts[0].name != migScopedName {
			t.Fatalf("expected one dry-run upsert of %q, got %v", migScopedName, fc.upserts)
		}
		if len(fc.deletes) != 1 || fc.deletes[0] != migOldID {
			t.Fatalf("expected delete of %q, got %v", migOldID, fc.deletes)
		}
		if p.Status.ID != "" {
			t.Fatalf("expected status.ID cleared to force recreate, got %q", p.Status.ID)
		}
	})

	t.Run("dry-run failure aborts without delete", func(t *testing.T) {
		fc := &recordingFleetClient{dryRunErr: errors.New("invalid config")}
		r := scopedReconciler(fc, fleetmanagementv1alpha1.NameScopeNamespace)
		p := migPipeline(migBaseName, migOldID, migBaseName, nil)
		migrated, err := r.migratePipelineName(ctx, p)
		if migrated || err == nil {
			t.Fatalf("expected abort with error, got migrated=%v err=%v", migrated, err)
		}
		if len(fc.deletes) != 0 {
			t.Fatalf("expected no delete on dry-run failure, got %v", fc.deletes)
		}
		if p.Status.ID != migOldID {
			t.Fatalf("expected status.ID preserved on abort, got %q", p.Status.ID)
		}
	})

	t.Run("delete error preserves status.ID for retry", func(t *testing.T) {
		fc := &recordingFleetClient{deleteErr: errors.New("boom")}
		r := scopedReconciler(fc, fleetmanagementv1alpha1.NameScopeNamespace)
		p := migPipeline(migBaseName, migOldID, migBaseName, nil)
		migrated, err := r.migratePipelineName(ctx, p)
		if migrated || err == nil {
			t.Fatalf("expected error, got migrated=%v err=%v", migrated, err)
		}
		if p.Status.ID != migOldID {
			t.Fatalf("expected status.ID preserved for retry, got %q", p.Status.ID)
		}
	})

	t.Run("discovered pipeline never migrates", func(t *testing.T) {
		fc := &recordingFleetClient{}
		r := scopedReconciler(fc, fleetmanagementv1alpha1.NameScopeNamespace)
		p := migPipeline("fleet-name", migOldID, "fleet-name",
			map[string]string{fleetmanagementv1alpha1.FleetPipelineIDAnnotation: "id1"})
		migrated, err := r.migratePipelineName(ctx, p)
		if migrated || err != nil || len(fc.deletes) != 0 {
			t.Fatalf("discovered pipeline must not migrate: migrated=%v err=%v deletes=%v", migrated, err, fc.deletes)
		}
	})
}
