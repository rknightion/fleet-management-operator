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
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
	"github.com/grafana/fleet-management-operator/pkg/sources"
)

// fakeSource is a test-controlled implementation of sources.Source. Tests
// configure the records-to-return and observe call counts; the controller
// uses it via a SourceFactory closure.
type fakeSource struct {
	mu      sync.Mutex
	records []sources.Record
	err     error
	calls   int32
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

func (f *fakeSource) setRecords(rs []sources.Record) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = rs
}

func (f *fakeSource) callCount() int32 {
	return atomic.LoadInt32(&f.calls)
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
})
