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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
	"github.com/grafana/fleet-management-operator/pkg/sources"
)

// fakeSource is a test-controlled implementation of sources.Source. Tests
// configure the records-to-return and observe call counts; the controller
// uses it via a SourceFactory closure.
type fakeSource struct {
	mu         sync.Mutex
	records    []sources.Record
	err        error
	calls      int32
	closeCalls int32
}

func (f *fakeSource) Kind() string { return "FAKE" }

func (f *fakeSource) Fetch(_ context.Context) ([]sources.Record, error) {
	atomic.AddInt32(&f.calls, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := make([]sources.Record, len(f.records))
	copy(out, f.records)
	return out, nil
}

// Close satisfies sources.Source. The counter lets tests verify the
// controller's defer-Close contract holds across success, fetch-error and
// projection-error paths (S1).
func (f *fakeSource) Close() error {
	atomic.AddInt32(&f.closeCalls, 1)
	return nil
}

func (f *fakeSource) setRecords(rs []sources.Record) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = rs
}

func (f *fakeSource) callCount() int32 {
	return atomic.LoadInt32(&f.calls)
}

func (f *fakeSource) closeCount() int32 {
	return atomic.LoadInt32(&f.closeCalls)
}

var (
	externalSyncTestCounter atomic.Uint64
	externalSyncFakeSource  *fakeSource
)

func uniqueExternalSyncSuffix() string {
	return fmt.Sprintf("%d", externalSyncTestCounter.Add(1))
}

var _ = Describe("ExternalAttributeSync Controller", func() {
	const (
		extTimeout  = 10 * time.Second
		extInterval = 250 * time.Millisecond
	)

	var extNS string

	BeforeEach(func() {
		ctx := context.Background()
		extNS = "extsync-" + uniqueExternalSyncSuffix()
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: extNS},
		})).To(Succeed())

		// Reset the package-level fake source for this test.
		externalSyncFakeSource = &fakeSource{}
		collectorMock.reset()
	})

	It("projects records into status.OwnedKeys for matched collectors", func() {
		ctx := context.Background()
		collectorID := "extsync-collector-" + uniqueExternalSyncSuffix()

		// Pre-register the collector in the Fleet mock so its first
		// Collector reconcile mirrors LocalAttributes into status.
		registerMockCollector(collectorID, map[string]string{
			"region": "us-east-1", "collector.os": "linux",
		})

		Expect(k8sClient.Create(ctx, &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: extNS},
			Spec:       fleetmanagementv1alpha1.CollectorSpec{ID: collectorID, RemoteAttributes: map[string]string{}},
		})).To(Succeed())

		// Wait for the Collector's status.LocalAttributes to appear so
		// the sync's selector can see "region=us-east-1".
		Eventually(func() string {
			c := &fleetmanagementv1alpha1.Collector{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "edge"}, c); err != nil {
				return ""
			}
			return c.Status.LocalAttributes["region"]
		}, extTimeout, extInterval).Should(Equal("us-east-1"))

		externalSyncFakeSource.setRecords([]sources.Record{
			{"hostname": collectorID, "env": "prod", "team": "platform"},
		})

		Expect(k8sClient.Create(ctx, &fleetmanagementv1alpha1.ExternalAttributeSync{
			ObjectMeta: metav1.ObjectMeta{Name: "cmdb", Namespace: extNS},
			Spec: fleetmanagementv1alpha1.ExternalAttributeSyncSpec{
				Source: fleetmanagementv1alpha1.ExternalSource{
					Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
					HTTP: &fleetmanagementv1alpha1.HTTPSourceSpec{URL: "http://example/"},
				},
				Schedule: "5m",
				Selector: fleetmanagementv1alpha1.PolicySelector{
					Matchers: []string{"region=us-east-1"},
				},
				Mapping: fleetmanagementv1alpha1.AttributeMapping{
					CollectorIDField: "hostname",
					AttributeFields:  map[string]string{"env": "env", "team": "team"},
				},
			},
		})).To(Succeed())

		Eventually(func() []fleetmanagementv1alpha1.OwnedKeyEntry {
			s := &fleetmanagementv1alpha1.ExternalAttributeSync{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "cmdb"}, s); err != nil {
				return nil
			}
			return s.Status.OwnedKeys
		}, extTimeout, extInterval).Should(HaveLen(1))

		s := &fleetmanagementv1alpha1.ExternalAttributeSync{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "cmdb"}, s)).To(Succeed())
		Expect(s.Status.OwnedKeys[0].CollectorID).To(Equal(collectorID))
		Expect(s.Status.OwnedKeys[0].Attributes).To(Equal(map[string]string{
			"env": "prod", "team": "platform",
		}))
		Expect(s.Status.RecordsApplied).To(Equal(int32(1)))
	})

	It("skips records that fail RequiredKeys", func() {
		ctx := context.Background()
		collectorID := "extsync-collector-" + uniqueExternalSyncSuffix()
		registerMockCollector(collectorID, map[string]string{"collector.os": "linux"})

		Expect(k8sClient.Create(ctx, &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: extNS},
			Spec:       fleetmanagementv1alpha1.CollectorSpec{ID: collectorID, RemoteAttributes: map[string]string{}},
		})).To(Succeed())

		Eventually(func() bool {
			c := &fleetmanagementv1alpha1.Collector{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "edge"}, c)
			return err == nil && len(c.Status.LocalAttributes) > 0
		}, extTimeout, extInterval).Should(BeTrue())

		externalSyncFakeSource.setRecords([]sources.Record{
			{"hostname": collectorID, "env": "prod"}, // missing "team"
		})

		Expect(k8sClient.Create(ctx, &fleetmanagementv1alpha1.ExternalAttributeSync{
			ObjectMeta: metav1.ObjectMeta{Name: "cmdb", Namespace: extNS},
			Spec: fleetmanagementv1alpha1.ExternalAttributeSyncSpec{
				Source: fleetmanagementv1alpha1.ExternalSource{
					Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
					HTTP: &fleetmanagementv1alpha1.HTTPSourceSpec{URL: "http://example/"},
				},
				Schedule: "5m",
				Selector: fleetmanagementv1alpha1.PolicySelector{
					CollectorIDs: []string{collectorID},
				},
				Mapping: fleetmanagementv1alpha1.AttributeMapping{
					CollectorIDField: "hostname",
					AttributeFields:  map[string]string{"env": "env", "team": "team"},
					RequiredKeys:     []string{"hostname", "team"},
				},
			},
		})).To(Succeed())

		Eventually(func() int32 {
			s := &fleetmanagementv1alpha1.ExternalAttributeSync{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "cmdb"}, s); err != nil {
				return -1
			}
			return s.Status.RecordsSeen
		}, extTimeout, extInterval).Should(Equal(int32(1)))

		s := &fleetmanagementv1alpha1.ExternalAttributeSync{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "cmdb"}, s)).To(Succeed())
		Expect(s.Status.RecordsApplied).To(Equal(int32(0)))
		Expect(s.Status.OwnedKeys).To(BeEmpty())
	})

	It("preserves prior OwnedKeys when fetch returns 0 records and AllowEmptyResults is false", func() {
		ctx := context.Background()
		collectorID := "extsync-collector-" + uniqueExternalSyncSuffix()
		registerMockCollector(collectorID, map[string]string{"collector.os": "linux"})

		Expect(k8sClient.Create(ctx, &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: extNS},
			Spec:       fleetmanagementv1alpha1.CollectorSpec{ID: collectorID, RemoteAttributes: map[string]string{}},
		})).To(Succeed())

		Eventually(func() bool {
			c := &fleetmanagementv1alpha1.Collector{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "edge"}, c)
			return err == nil && len(c.Status.LocalAttributes) > 0
		}, extTimeout, extInterval).Should(BeTrue())

		externalSyncFakeSource.setRecords([]sources.Record{
			{"hostname": collectorID, "env": "prod"},
		})

		Expect(k8sClient.Create(ctx, &fleetmanagementv1alpha1.ExternalAttributeSync{
			ObjectMeta: metav1.ObjectMeta{Name: "cmdb", Namespace: extNS},
			Spec: fleetmanagementv1alpha1.ExternalAttributeSyncSpec{
				Source: fleetmanagementv1alpha1.ExternalSource{
					Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
					HTTP: &fleetmanagementv1alpha1.HTTPSourceSpec{URL: "http://example/"},
				},
				Schedule: "5m",
				Selector: fleetmanagementv1alpha1.PolicySelector{CollectorIDs: []string{collectorID}},
				Mapping: fleetmanagementv1alpha1.AttributeMapping{
					CollectorIDField: "hostname",
					AttributeFields:  map[string]string{"env": "env"},
				},
			},
		})).To(Succeed())

		Eventually(func() int32 {
			s := &fleetmanagementv1alpha1.ExternalAttributeSync{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "cmdb"}, s); err != nil {
				return -1
			}
			return s.Status.RecordsApplied
		}, extTimeout, extInterval).Should(Equal(int32(1)))

		// Now drop to zero records and force a re-reconcile by bumping the
		// spec generation (changing schedule). Empty-result guard should
		// keep the previous OwnedKeys claim.
		externalSyncFakeSource.setRecords(nil)
		s := &fleetmanagementv1alpha1.ExternalAttributeSync{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "cmdb"}, s)).To(Succeed())
		s.Spec.Schedule = "10m"
		Expect(k8sClient.Update(ctx, s)).To(Succeed())

		Eventually(func() string {
			s := &fleetmanagementv1alpha1.ExternalAttributeSync{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "cmdb"}, s); err != nil {
				return ""
			}
			for _, c := range s.Status.Conditions {
				if c.Type == externalSyncConditionStalled && c.Status == metav1.ConditionTrue {
					return c.Reason
				}
			}
			return ""
		}, extTimeout, extInterval).Should(Equal(externalSyncReasonStalled))

		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "cmdb"}, s)).To(Succeed())
		Expect(s.Status.OwnedKeys).To(HaveLen(1), "previous claim should be preserved by empty-result guard")
	})

	It("surfaces a failure condition when the source errors", func() {
		ctx := context.Background()
		externalSyncFakeSource.err = errors.New("upstream is down")

		Expect(k8sClient.Create(ctx, &fleetmanagementv1alpha1.ExternalAttributeSync{
			ObjectMeta: metav1.ObjectMeta{Name: "cmdb", Namespace: extNS},
			Spec: fleetmanagementv1alpha1.ExternalAttributeSyncSpec{
				Source: fleetmanagementv1alpha1.ExternalSource{
					Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
					HTTP: &fleetmanagementv1alpha1.HTTPSourceSpec{URL: "http://example/"},
				},
				Schedule: "5m",
				Selector: fleetmanagementv1alpha1.PolicySelector{CollectorIDs: []string{"any"}},
				Mapping: fleetmanagementv1alpha1.AttributeMapping{
					CollectorIDField: "hostname",
					AttributeFields:  map[string]string{"env": "env"},
				},
			},
		})).To(Succeed())

		Eventually(func() string {
			s := &fleetmanagementv1alpha1.ExternalAttributeSync{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "cmdb"}, s); err != nil {
				if apierrors.IsNotFound(err) {
					return ""
				}
				return ""
			}
			for _, c := range s.Status.Conditions {
				if c.Type == conditionTypeReady && c.Status == metav1.ConditionFalse {
					return c.Reason
				}
			}
			return ""
		}, extTimeout, extInterval).Should(Equal(externalSyncReasonSourceFailed))
	})

	It("closes the Source on every reconcile, on both success and failure paths (S1)", func() {
		// S1: the controller defers src.Close() at the Fetch call site so
		// SQL connection pools are released even on early-error paths.
		// We exercise both branches in the same test:
		//   1. a healthy Fetch increments callCount AND closeCount.
		//   2. an erroring Fetch ALSO increments closeCount, proving
		//      defer fires before the function returns the error.
		// Without the defer, closeCount would lag callCount and the SQL
		// source would leak a *sql.DB per reconcile.
		ctx := context.Background()
		collectorID := "extsync-collector-" + uniqueExternalSyncSuffix()
		registerMockCollector(collectorID, map[string]string{"collector.os": "linux"})

		Expect(k8sClient.Create(ctx, &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: extNS},
			Spec:       fleetmanagementv1alpha1.CollectorSpec{ID: collectorID, RemoteAttributes: map[string]string{}},
		})).To(Succeed())

		Eventually(func() bool {
			c := &fleetmanagementv1alpha1.Collector{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "edge"}, c)
			return err == nil && len(c.Status.LocalAttributes) > 0
		}, extTimeout, extInterval).Should(BeTrue())

		externalSyncFakeSource.setRecords([]sources.Record{
			{"hostname": collectorID, "env": "prod"},
		})

		Expect(k8sClient.Create(ctx, &fleetmanagementv1alpha1.ExternalAttributeSync{
			ObjectMeta: metav1.ObjectMeta{Name: "close-on-success", Namespace: extNS},
			Spec: fleetmanagementv1alpha1.ExternalAttributeSyncSpec{
				Source: fleetmanagementv1alpha1.ExternalSource{
					Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
					HTTP: &fleetmanagementv1alpha1.HTTPSourceSpec{URL: "http://example/"},
				},
				Schedule: "5m",
				Selector: fleetmanagementv1alpha1.PolicySelector{CollectorIDs: []string{collectorID}},
				Mapping: fleetmanagementv1alpha1.AttributeMapping{
					CollectorIDField: "hostname",
					AttributeFields:  map[string]string{"env": "env"},
				},
			},
		})).To(Succeed())

		// Wait for the success-path reconcile to land.
		Eventually(func() int32 {
			s := &fleetmanagementv1alpha1.ExternalAttributeSync{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "close-on-success"}, s); err != nil {
				return -1
			}
			return s.Status.RecordsApplied
		}, extTimeout, extInterval).Should(Equal(int32(1)))

		// On the success path, every Fetch call must be paired with a
		// Close call. We use Eventually because the deferred Close
		// runs after the controller's status write returns, and the
		// status visibility from Eventually does not prove the defer
		// has executed yet.
		Eventually(func() bool {
			calls := externalSyncFakeSource.callCount()
			closes := externalSyncFakeSource.closeCount()
			return calls > 0 && closes == calls
		}, extTimeout, extInterval).Should(BeTrue(),
			"closeCount must equal callCount on success path; got calls=%d closes=%d",
			externalSyncFakeSource.callCount(), externalSyncFakeSource.closeCount())

		// Now flip the source to error. Each new Fetch must also be
		// paired with a Close even though the controller takes the
		// updateStatusError path.
		preCalls := externalSyncFakeSource.callCount()
		externalSyncFakeSource.err = errors.New("upstream is down")
		// Bump the spec generation to guarantee a re-reconcile.
		s := &fleetmanagementv1alpha1.ExternalAttributeSync{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "close-on-success"}, s)).To(Succeed())
		// Any schedule different from the original triggers a generation bump.
		s.Spec.Schedule = "15m"
		Expect(k8sClient.Update(ctx, s)).To(Succeed())

		Eventually(func() bool {
			calls := externalSyncFakeSource.callCount()
			closes := externalSyncFakeSource.closeCount()
			// Wait until at least one new Fetch has happened post-spec-bump
			// AND every Fetch is matched by a Close.
			return calls > preCalls && closes == calls
		}, extTimeout, extInterval).Should(BeTrue(),
			"closeCount must equal callCount on error path; got calls=%d closes=%d",
			externalSyncFakeSource.callCount(), externalSyncFakeSource.closeCount())
	})

	It("requeues at the duration schedule", func() {
		// A very short schedule so the controller fires multiple times within
		// the test window. The call counter increments on every Fetch, so
		// seeing >= 2 calls proves the controller requeued rather than stopping
		// after the first reconcile.
		ctx := context.Background()

		externalSyncFakeSource.setRecords([]sources.Record{
			{"hostname": "any-collector", "env": "prod"},
		})

		Expect(k8sClient.Create(ctx, &fleetmanagementv1alpha1.ExternalAttributeSync{
			ObjectMeta: metav1.ObjectMeta{Name: "cmdb", Namespace: extNS},
			Spec: fleetmanagementv1alpha1.ExternalAttributeSyncSpec{
				Source: fleetmanagementv1alpha1.ExternalSource{
					Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
					HTTP: &fleetmanagementv1alpha1.HTTPSourceSpec{URL: "http://example/"},
				},
				Schedule: "2s",
				Selector: fleetmanagementv1alpha1.PolicySelector{CollectorIDs: []string{"any-collector"}},
				Mapping: fleetmanagementv1alpha1.AttributeMapping{
					CollectorIDField: "hostname",
					AttributeFields:  map[string]string{"env": "env"},
				},
			},
		})).To(Succeed())

		// Wait until the fake source has been called at least twice —
		// this is the direct evidence that RequeueAfter was honoured.
		Eventually(func() int32 {
			return externalSyncFakeSource.callCount()
		}, extTimeout, extInterval).Should(BeNumerically(">=", 2))
	})

	It("recovers owned keys after a Stalled reconcile", func() {
		// This test exercises the full stall→recovery lifecycle:
		//   1. First fetch succeeds, OwnedKeys populated.
		//   2. Source goes empty; Stalled condition set, OwnedKeys preserved.
		//   3. Source returns a different record; Stalled cleared, OwnedKeys updated.
		ctx := context.Background()
		collectorID := "extsync-collector-" + uniqueExternalSyncSuffix()
		registerMockCollector(collectorID, map[string]string{"collector.os": "linux"})

		Expect(k8sClient.Create(ctx, &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: extNS},
			Spec:       fleetmanagementv1alpha1.CollectorSpec{ID: collectorID, RemoteAttributes: map[string]string{}},
		})).To(Succeed())

		Eventually(func() bool {
			c := &fleetmanagementv1alpha1.Collector{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "edge"}, c)
			return err == nil && len(c.Status.LocalAttributes) > 0
		}, extTimeout, extInterval).Should(BeTrue())

		// Step 1: populate OwnedKeys with one record.
		externalSyncFakeSource.setRecords([]sources.Record{
			{"hostname": collectorID, "env": "staging"},
		})

		Expect(k8sClient.Create(ctx, &fleetmanagementv1alpha1.ExternalAttributeSync{
			ObjectMeta: metav1.ObjectMeta{Name: "cmdb", Namespace: extNS},
			Spec: fleetmanagementv1alpha1.ExternalAttributeSyncSpec{
				Source: fleetmanagementv1alpha1.ExternalSource{
					Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
					HTTP: &fleetmanagementv1alpha1.HTTPSourceSpec{URL: "http://example/"},
				},
				Schedule: "5m",
				Selector: fleetmanagementv1alpha1.PolicySelector{CollectorIDs: []string{collectorID}},
				Mapping: fleetmanagementv1alpha1.AttributeMapping{
					CollectorIDField: "hostname",
					AttributeFields:  map[string]string{"env": "env"},
				},
				// AllowEmptyResults defaults to false — empty-result guard is active.
			},
		})).To(Succeed())

		Eventually(func() int32 {
			s := &fleetmanagementv1alpha1.ExternalAttributeSync{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "cmdb"}, s); err != nil {
				return -1
			}
			return s.Status.RecordsApplied
		}, extTimeout, extInterval).Should(Equal(int32(1)))

		// Step 2: drop to zero records and trigger a reconcile via spec bump.
		externalSyncFakeSource.setRecords(nil)
		s := &fleetmanagementv1alpha1.ExternalAttributeSync{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "cmdb"}, s)).To(Succeed())
		s.Spec.Schedule = "10m"
		Expect(k8sClient.Update(ctx, s)).To(Succeed())

		Eventually(func() string {
			s := &fleetmanagementv1alpha1.ExternalAttributeSync{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "cmdb"}, s); err != nil {
				return ""
			}
			for _, c := range s.Status.Conditions {
				if c.Type == externalSyncConditionStalled && c.Status == metav1.ConditionTrue {
					return c.Reason
				}
			}
			return ""
		}, extTimeout, extInterval).Should(Equal(externalSyncReasonStalled))

		// OwnedKeys must be preserved during the stall.
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "cmdb"}, s)).To(Succeed())
		Expect(s.Status.OwnedKeys).To(HaveLen(1), "OwnedKeys must be preserved when source returns 0 records")

		// Step 3: source returns a new record; controller must clear Stalled and
		// update OwnedKeys to reflect the new data.
		externalSyncFakeSource.setRecords([]sources.Record{
			{"hostname": collectorID, "env": "prod"},
		})
		// Trigger another reconcile via a second spec bump.
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "cmdb"}, s)).To(Succeed())
		s.Spec.Schedule = "15m"
		Expect(k8sClient.Update(ctx, s)).To(Succeed())

		Eventually(func() string {
			s := &fleetmanagementv1alpha1.ExternalAttributeSync{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "cmdb"}, s); err != nil {
				return ""
			}
			for _, c := range s.Status.Conditions {
				if c.Type == externalSyncConditionStalled && c.Status == metav1.ConditionFalse {
					return c.Reason
				}
			}
			return ""
		}, extTimeout, extInterval).Should(Not(BeEmpty()), "Stalled condition must be cleared after source recovers")

		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: extNS, Name: "cmdb"}, s)).To(Succeed())
		Expect(s.Status.OwnedKeys).To(HaveLen(1))
		Expect(s.Status.OwnedKeys[0].Attributes["env"]).To(Equal("prod"),
			"OwnedKeys must be updated to the new record after recovery")
	})
})

// findExternalSyncCondition looks up a condition by Type. Returns nil if absent.
func findExternalSyncCondition(s *fleetmanagementv1alpha1.ExternalAttributeSync, condType string) *metav1.Condition {
	return meta.FindStatusCondition(s.Status.Conditions, condType)
}

// buildMatchedCollectorList returns n Collector CRs all matching `tier=edge`,
// each with a unique ID `id-NNNNN`. Used to drive the matched-collector
// filtering step inside the ExternalAttributeSync reconciler.
func buildMatchedCollectorList(ns string, n int) []*fleetmanagementv1alpha1.Collector {
	out := make([]*fleetmanagementv1alpha1.Collector, 0, n)
	for i := range n {
		out = append(out, &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("collector-%05d", i),
				Namespace: ns,
			},
			Spec: fleetmanagementv1alpha1.CollectorSpec{
				ID:               fmt.Sprintf("id-%05d", i),
				RemoteAttributes: map[string]string{},
			},
			Status: fleetmanagementv1alpha1.CollectorStatus{
				LocalAttributes: map[string]string{"tier": "edge"},
			},
		})
	}
	return out
}

// buildRecordsForCollectors returns a fakeSource record per collector ID,
// mapping `hostname` to the collector ID and an `env` attribute the sync's
// AttributeMapping expects.
func buildRecordsForCollectors(n int) []sources.Record {
	out := make([]sources.Record, 0, n)
	for i := range n {
		out = append(out, sources.Record{
			"hostname": fmt.Sprintf("id-%05d", i),
			"env":      "prod",
		})
	}
	return out
}

// drainExternalSyncCapExceededEvents pulls events off the recorder's channel
// without blocking and returns the count whose reason is OwnedKeysExceeded.
func drainExternalSyncCapExceededEvents(rec *record.FakeRecorder) int {
	count := 0
	for {
		select {
		case ev := <-rec.Events:
			if strings.Contains(ev, externalSyncEventReasonOwnedKeysExceeded) {
				count++
			}
		default:
			return count
		}
	}
}

// E4/P1-05: maxOwnedKeys tests for ExternalAttributeSync.
//
// These tests use a fake K8s client to fan out 1000+ Collector CRs and
// drive the reconciler with a matching number of source records. They
// cover:
//   - fail-closed status when ownedKeys exceeds maxOwnedKeys
//   - no partial OwnedKeys handoff is published above the cap
//   - cap-exceeded condition CLEARED on transition back below the cap
//   - Warning event emitted on truncation
//   - No-op short-circuit doesn't preserve a stale Stalled=True after
//     same-data recovery (B3) or a stale Truncated=True after the
//     matched-set drops (B4-equivalent for owned keys)
var _ = Describe("ExternalAttributeSync owned-key cap", func() {
	const truncNS = "extsync-trunc"

	type tc struct {
		name            string
		n               int
		wantCapExceeded bool
		wantOwnedLen    int
		wantEvent       bool
		wantEventCount  int
	}

	cases := []tc{
		{name: "below cap (999)", n: 999, wantCapExceeded: false, wantOwnedLen: 999, wantEvent: false},
		{name: "at cap (1000)", n: 1000, wantCapExceeded: false, wantOwnedLen: 1000, wantEvent: false},
		{name: "just over cap (1001)", n: 1001, wantCapExceeded: true, wantOwnedLen: 0, wantEvent: true, wantEventCount: 1},
		{name: "well over cap (5000)", n: 5000, wantCapExceeded: true, wantOwnedLen: 0, wantEvent: true, wantEventCount: 1},
	}

	for _, tt := range cases {
		It("table case: "+tt.name, func() {
			ctx := context.Background()

			sync := &fleetmanagementv1alpha1.ExternalAttributeSync{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "trunc-sync",
					Namespace:  truncNS,
					Generation: 1,
				},
				Spec: fleetmanagementv1alpha1.ExternalAttributeSyncSpec{
					Source: fleetmanagementv1alpha1.ExternalSource{
						Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
						HTTP: &fleetmanagementv1alpha1.HTTPSourceSpec{URL: "http://example/"},
					},
					Schedule: "5m",
					Selector: fleetmanagementv1alpha1.PolicySelector{
						Matchers: []string{`tier="edge"`},
					},
					Mapping: fleetmanagementv1alpha1.AttributeMapping{
						CollectorIDField: "hostname",
						AttributeFields:  map[string]string{"env": "env"},
					},
				},
			}

			objs := []client.Object{sync}
			for _, c := range buildMatchedCollectorList(truncNS, tt.n) {
				objs = append(objs, c)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithStatusSubresource(&fleetmanagementv1alpha1.ExternalAttributeSync{}).
				WithObjects(objs...).
				Build()
			fakeRecorder := record.NewFakeRecorder(64)
			testSrc := &fakeSource{}
			testSrc.setRecords(buildRecordsForCollectors(tt.n))

			r := &ExternalAttributeSyncReconciler{
				Client:   fakeClient,
				Scheme:   scheme.Scheme,
				Recorder: fakeRecorder,
				Factory: func(_ fleetmanagementv1alpha1.ExternalSource, _ *corev1.Secret) (sources.Source, error) {
					return testSrc, nil
				},
			}

			_, err := r.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{Namespace: truncNS, Name: "trunc-sync"},
			})
			Expect(err).NotTo(HaveOccurred())

			got := &fleetmanagementv1alpha1.ExternalAttributeSync{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Namespace: truncNS, Name: "trunc-sync"}, got)).To(Succeed())

			Expect(got.Status.OwnedKeys).To(HaveLen(tt.wantOwnedLen),
				"OwnedKeys must be fully published under the cap and cleared above the cap")
			truncCond := findExternalSyncCondition(got, externalSyncConditionTruncated)
			Expect(truncCond).NotTo(BeNil())
			readyCond := findExternalSyncCondition(got, conditionTypeReady)
			Expect(readyCond).NotTo(BeNil())
			syncedCond := findExternalSyncCondition(got, conditionTypeSynced)
			Expect(syncedCond).NotTo(BeNil())
			if tt.wantCapExceeded {
				Expect(truncCond.Status).To(Equal(metav1.ConditionTrue))
				Expect(truncCond.Reason).To(Equal(externalSyncReasonOwnedKeysExceeded))
				Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
				Expect(readyCond.Reason).To(Equal(externalSyncReasonOwnedKeysExceeded))
				Expect(syncedCond.Status).To(Equal(metav1.ConditionFalse))
				Expect(syncedCond.Reason).To(Equal(externalSyncReasonOwnedKeysExceeded))
			} else {
				Expect(truncCond.Status).To(Equal(metav1.ConditionFalse))
				Expect(truncCond.Reason).To(Equal(externalSyncReasonNotTruncated))
				Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
				Expect(syncedCond.Status).To(Equal(metav1.ConditionTrue))
			}

			truncEvents := drainExternalSyncCapExceededEvents(fakeRecorder)
			if tt.wantEvent {
				Expect(truncEvents).To(BeNumerically(">=", tt.wantEventCount))
			} else {
				Expect(truncEvents).To(Equal(0))
			}
		})
	}

	It("clears a previous OwnedKeys claim when the computed handoff exceeds the cap", func() {
		ctx := context.Background()
		previousSuccess := metav1.NewTime(time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC))
		sync := &fleetmanagementv1alpha1.ExternalAttributeSync{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "cap-clears-previous",
				Namespace:  truncNS,
				Generation: 1,
			},
			Spec: fleetmanagementv1alpha1.ExternalAttributeSyncSpec{
				Source: fleetmanagementv1alpha1.ExternalSource{
					Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
					HTTP: &fleetmanagementv1alpha1.HTTPSourceSpec{URL: "http://example/"},
				},
				Schedule: "5m",
				Selector: fleetmanagementv1alpha1.PolicySelector{
					Matchers: []string{`tier="edge"`},
				},
				Mapping: fleetmanagementv1alpha1.AttributeMapping{
					CollectorIDField: "hostname",
					AttributeFields:  map[string]string{"env": "env"},
				},
			},
			Status: fleetmanagementv1alpha1.ExternalAttributeSyncStatus{
				ObservedGeneration: 1,
				LastSuccessTime:    &previousSuccess,
				RecordsSeen:        1,
				RecordsApplied:     1,
				OwnedKeys: []fleetmanagementv1alpha1.OwnedKeyEntry{
					{CollectorID: "id-00000", Attributes: map[string]string{"env": "prod"}},
				},
				Conditions: []metav1.Condition{
					{
						Type:               conditionTypeReady,
						Status:             metav1.ConditionTrue,
						Reason:             externalSyncReasonSynced,
						ObservedGeneration: 1,
					},
					{
						Type:               conditionTypeSynced,
						Status:             metav1.ConditionTrue,
						Reason:             externalSyncReasonSynced,
						ObservedGeneration: 1,
					},
				},
			},
		}

		objs := []client.Object{sync}
		for _, c := range buildMatchedCollectorList(truncNS, maxOwnedKeys+1) {
			objs = append(objs, c)
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&fleetmanagementv1alpha1.ExternalAttributeSync{}).
			WithObjects(objs...).
			Build()
		testSrc := &fakeSource{}
		testSrc.setRecords(buildRecordsForCollectors(maxOwnedKeys + 1))

		r := &ExternalAttributeSyncReconciler{
			Client: fakeClient,
			Scheme: scheme.Scheme,
			Factory: func(_ fleetmanagementv1alpha1.ExternalSource, _ *corev1.Secret) (sources.Source, error) {
				return testSrc, nil
			},
		}

		_, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: truncNS, Name: "cap-clears-previous"},
		})
		Expect(err).NotTo(HaveOccurred())

		got := &fleetmanagementv1alpha1.ExternalAttributeSync{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Namespace: truncNS, Name: "cap-clears-previous"}, got)).To(Succeed())
		Expect(got.Status.OwnedKeys).To(BeEmpty(), "previous handoff must be cleared above maxOwnedKeys")
		Expect(got.Status.LastSuccessTime).NotTo(BeNil())
		Expect(got.Status.LastSuccessTime.Time.Equal(previousSuccess.Time)).To(BeTrue(),
			"over-cap attempts must not advance LastSuccessTime")
		Expect(findExternalSyncCondition(got, conditionTypeReady).Reason).To(Equal(externalSyncReasonOwnedKeysExceeded))
		Expect(findExternalSyncCondition(got, conditionTypeSynced).Status).To(Equal(metav1.ConditionFalse))
	})

	It("clears cap-exceeded status when ownedKeys drops below the cap", func() {
		// Step 1: 1500 records -> fail closed with no OwnedKeys handoff.
		// Step 2: 800 records -> Truncated must flip to False and OwnedKeys
		// can be published again.
		ctx := context.Background()

		sync := &fleetmanagementv1alpha1.ExternalAttributeSync{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "trunc-clear-sync",
				Namespace:  truncNS,
				Generation: 1,
			},
			Spec: fleetmanagementv1alpha1.ExternalAttributeSyncSpec{
				Source: fleetmanagementv1alpha1.ExternalSource{
					Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
					HTTP: &fleetmanagementv1alpha1.HTTPSourceSpec{URL: "http://example/"},
				},
				Schedule: "5m",
				Selector: fleetmanagementv1alpha1.PolicySelector{
					Matchers: []string{`tier="edge"`},
				},
				Mapping: fleetmanagementv1alpha1.AttributeMapping{
					CollectorIDField: "hostname",
					AttributeFields:  map[string]string{"env": "env"},
				},
			},
		}

		objs := []client.Object{sync}
		for _, c := range buildMatchedCollectorList(truncNS, 1500) {
			objs = append(objs, c)
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&fleetmanagementv1alpha1.ExternalAttributeSync{}).
			WithObjects(objs...).
			Build()
		fakeRecorder := record.NewFakeRecorder(64)
		testSrc := &fakeSource{}
		testSrc.setRecords(buildRecordsForCollectors(1500))

		r := &ExternalAttributeSyncReconciler{
			Client:   fakeClient,
			Scheme:   scheme.Scheme,
			Recorder: fakeRecorder,
			Factory: func(_ fleetmanagementv1alpha1.ExternalSource, _ *corev1.Secret) (sources.Source, error) {
				return testSrc, nil
			},
		}

		_, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: truncNS, Name: "trunc-clear-sync"},
		})
		Expect(err).NotTo(HaveOccurred())

		got := &fleetmanagementv1alpha1.ExternalAttributeSync{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Namespace: truncNS, Name: "trunc-clear-sync"}, got)).To(Succeed())
		Expect(got.Status.OwnedKeys).To(BeEmpty())
		Expect(findExternalSyncCondition(got, externalSyncConditionTruncated).Status).To(Equal(metav1.ConditionTrue))
		Expect(findExternalSyncCondition(got, conditionTypeReady).Status).To(Equal(metav1.ConditionFalse))

		// Step 2: drop the records to 800. Generation unchanged.
		testSrc.setRecords(buildRecordsForCollectors(800))
		_, err = r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: truncNS, Name: "trunc-clear-sync"},
		})
		Expect(err).NotTo(HaveOccurred())

		got2 := &fleetmanagementv1alpha1.ExternalAttributeSync{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Namespace: truncNS, Name: "trunc-clear-sync"}, got2)).To(Succeed())
		Expect(got2.Status.OwnedKeys).To(HaveLen(800))
		truncCond2 := findExternalSyncCondition(got2, externalSyncConditionTruncated)
		Expect(truncCond2).NotTo(BeNil())
		Expect(truncCond2.Status).To(Equal(metav1.ConditionFalse),
			"Truncated condition must clear when ownedKeys drops below cap")
		Expect(findExternalSyncCondition(got2, conditionTypeReady).Status).To(Equal(metav1.ConditionTrue))
	})

	It("clears Stalled when source recovers with the SAME records as last successful run (B3)", func() {
		// Lock-in for B3: previous no-op compared only counts and owned-keys
		// content. After a Stalled run, if the source returns the SAME records
		// as the last successful run, none of those fields change — but the
		// Stalled condition must still flip from True to False.
		ctx := context.Background()

		// Step 1: first reconcile, populate OwnedKeys with 1 record.
		sync := &fleetmanagementv1alpha1.ExternalAttributeSync{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "stalled-recovery",
				Namespace:  truncNS,
				Generation: 1,
			},
			Spec: fleetmanagementv1alpha1.ExternalAttributeSyncSpec{
				Source: fleetmanagementv1alpha1.ExternalSource{
					Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
					HTTP: &fleetmanagementv1alpha1.HTTPSourceSpec{URL: "http://example/"},
				},
				Schedule: "5m",
				Selector: fleetmanagementv1alpha1.PolicySelector{
					CollectorIDs: []string{"id-00000"},
				},
				Mapping: fleetmanagementv1alpha1.AttributeMapping{
					CollectorIDField: "hostname",
					AttributeFields:  map[string]string{"env": "env"},
				},
				// AllowEmptyResults defaults to false.
			},
		}
		collector := &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: truncNS},
			Spec: fleetmanagementv1alpha1.CollectorSpec{
				ID: "id-00000", RemoteAttributes: map[string]string{},
			},
			Status: fleetmanagementv1alpha1.CollectorStatus{
				LocalAttributes: map[string]string{"tier": "edge"},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&fleetmanagementv1alpha1.ExternalAttributeSync{}).
			WithObjects(sync, collector).
			Build()
		fakeRecorder := record.NewFakeRecorder(64)
		testSrc := &fakeSource{}
		testSrc.setRecords([]sources.Record{
			{"hostname": "id-00000", "env": "prod"},
		})

		r := &ExternalAttributeSyncReconciler{
			Client:   fakeClient,
			Scheme:   scheme.Scheme,
			Recorder: fakeRecorder,
			Factory: func(_ fleetmanagementv1alpha1.ExternalSource, _ *corev1.Secret) (sources.Source, error) {
				return testSrc, nil
			},
		}

		_, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: truncNS, Name: "stalled-recovery"},
		})
		Expect(err).NotTo(HaveOccurred())

		got := &fleetmanagementv1alpha1.ExternalAttributeSync{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Namespace: truncNS, Name: "stalled-recovery"}, got)).To(Succeed())
		Expect(got.Status.OwnedKeys).To(HaveLen(1))
		Expect(got.Status.RecordsApplied).To(BeEquivalentTo(1))

		// Step 2: source returns 0 records — empty-result guard triggers
		// Stalled. Generation unchanged so the no-op short-circuit logic
		// has the chance to misbehave.
		testSrc.setRecords(nil)
		_, err = r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: truncNS, Name: "stalled-recovery"},
		})
		Expect(err).NotTo(HaveOccurred())

		got2 := &fleetmanagementv1alpha1.ExternalAttributeSync{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Namespace: truncNS, Name: "stalled-recovery"}, got2)).To(Succeed())
		stalled := findExternalSyncCondition(got2, externalSyncConditionStalled)
		Expect(stalled).NotTo(BeNil())
		Expect(stalled.Status).To(Equal(metav1.ConditionTrue),
			"Stalled must be True after empty-result guard activates")
		Expect(got2.Status.OwnedKeys).To(HaveLen(1),
			"OwnedKeys preserved by empty-result guard")

		// Step 3: source recovers with the SAME record. RecordsSeen,
		// RecordsApplied, OwnedKeys, ObservedGeneration are all unchanged
		// from step 1. The B3 fix ensures the no-op check catches the
		// Stalled=True transition and writes status to clear it.
		testSrc.setRecords([]sources.Record{
			{"hostname": "id-00000", "env": "prod"},
		})
		_, err = r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: truncNS, Name: "stalled-recovery"},
		})
		Expect(err).NotTo(HaveOccurred())

		got3 := &fleetmanagementv1alpha1.ExternalAttributeSync{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Namespace: truncNS, Name: "stalled-recovery"}, got3)).To(Succeed())
		stalled3 := findExternalSyncCondition(got3, externalSyncConditionStalled)
		Expect(stalled3).NotTo(BeNil())
		Expect(stalled3.Status).To(Equal(metav1.ConditionFalse),
			"Stalled must be cleared after source recovers, even with identical records (B3)")
		ready := findExternalSyncCondition(got3, conditionTypeReady)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Status).To(Equal(metav1.ConditionTrue),
			"Ready must be True after recovery")
	})
})
