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
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
	"github.com/grafana/fleet-management-operator/pkg/fleetclient"
)

var discoveryTestCounter atomic.Uint64

func uniqueDiscoverySuffix() string {
	return fmt.Sprintf("%d", discoveryTestCounter.Add(1))
}

// fleetCollector is a small constructor so test setup reads cleanly.
func fleetCollector(id string, opts ...func(*fleetclient.Collector)) *fleetclient.Collector {
	c := &fleetclient.Collector{ID: id}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func withInactive(t time.Time) func(*fleetclient.Collector) {
	return func(c *fleetclient.Collector) { c.MarkedInactiveAt = &t }
}

var _ = Describe("CollectorDiscovery Controller", func() {
	const (
		discoveryTimeout    = 15 * time.Second
		discoveryInterval   = 250 * time.Millisecond
		shortPoll           = "1s"
		discoveryNamePrefix = "cd-"
	)

	var (
		discoveryNS string
		cdName      string
	)

	BeforeEach(func() {
		ctx := context.Background()
		suffix := uniqueDiscoverySuffix()
		discoveryNS = "discovery-" + suffix
		cdName = discoveryNamePrefix + suffix
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: discoveryNS},
		})).To(Succeed())

		// Reset shared mock so each test starts clean.
		collectorMock.reset()
	})

	createDiscovery := func(ctx context.Context, spec fleetmanagementv1alpha1.CollectorDiscoverySpec) *fleetmanagementv1alpha1.CollectorDiscovery {
		cd := &fleetmanagementv1alpha1.CollectorDiscovery{
			ObjectMeta: metav1.ObjectMeta{Name: cdName, Namespace: discoveryNS},
			Spec:       spec,
		}
		Expect(k8sClient.Create(ctx, cd)).To(Succeed())
		return cd
	}

	getDiscovery := func(ctx context.Context) *fleetmanagementv1alpha1.CollectorDiscovery {
		cd := &fleetmanagementv1alpha1.CollectorDiscovery{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: discoveryNS, Name: cdName}, cd)).To(Succeed())
		return cd
	}

	listManagedCRsIn := func(ctx context.Context, ns string) []fleetmanagementv1alpha1.Collector {
		var list fleetmanagementv1alpha1.CollectorList
		Expect(k8sClient.List(ctx, &list,
			client.InNamespace(ns),
			client.MatchingLabels{fleetmanagementv1alpha1.DiscoveryNameLabel: cdName},
		)).To(Succeed())
		return list.Items
	}
	listManagedCRs := func(ctx context.Context) []fleetmanagementv1alpha1.Collector {
		return listManagedCRsIn(ctx, discoveryNS)
	}

	It("creates a Collector CR for each Fleet collector matched by the selector", func() {
		ctx := context.Background()

		collectorMock.setListResult([]*fleetclient.Collector{
			fleetCollector("edge-1"),
			fleetCollector("edge-2"),
		})

		createDiscovery(ctx, fleetmanagementv1alpha1.CollectorDiscoverySpec{
			PollInterval: shortPoll,
			Selector:     fleetmanagementv1alpha1.PolicySelector{Matchers: []string{"env=prod"}},
		})

		Eventually(func() int {
			return len(listManagedCRs(ctx))
		}, discoveryTimeout, discoveryInterval).Should(Equal(2))

		// Verify the labels and annotations on each CR.
		crs := listManagedCRs(ctx)
		for _, cr := range crs {
			Expect(cr.Labels).To(HaveKeyWithValue(fleetmanagementv1alpha1.DiscoveryNameLabel, cdName))
			Expect(cr.Labels).To(HaveKeyWithValue(fleetmanagementv1alpha1.DiscoveryNamespaceLabel, discoveryNS))
			Expect(cr.Annotations).To(HaveKeyWithValue(
				fleetmanagementv1alpha1.DiscoveredByAnnotation,
				discoveryNS+"/"+cdName,
			))
			Expect(cr.Annotations).To(HaveKey(fleetmanagementv1alpha1.FleetCollectorIDAnnotation))
			Expect(cr.Spec.ID).NotTo(BeEmpty())
		}

		// Status reflects observed and managed counts. Eventually
		// (rather than Expect) because the controller-runtime cache
		// lag between Create and List can undercount managed on the
		// first poll — the next poll converges.
		Eventually(func() *fleetmanagementv1alpha1.CollectorDiscovery {
			return getDiscovery(ctx)
		}, discoveryTimeout, discoveryInterval).Should(SatisfyAll(
			HaveField("Status.CollectorsObserved", int32(2)),
			HaveField("Status.CollectorsManaged", int32(2)),
		))
	})

	It("preserves user edits to spec.remoteAttributes on subsequent polls", func() {
		ctx := context.Background()

		collectorMock.setListResult([]*fleetclient.Collector{
			fleetCollector("edge-edit"),
		})

		createDiscovery(ctx, fleetmanagementv1alpha1.CollectorDiscoverySpec{
			PollInterval: shortPoll,
		})

		// Wait for the CR to appear.
		var crKey types.NamespacedName
		Eventually(func() bool {
			crs := listManagedCRs(ctx)
			if len(crs) == 1 {
				crKey = types.NamespacedName{Namespace: crs[0].Namespace, Name: crs[0].Name}
				return true
			}
			return false
		}, discoveryTimeout, discoveryInterval).Should(BeTrue())

		// Edit the CR to add user-managed remote attributes.
		Eventually(func() error {
			cr := &fleetmanagementv1alpha1.Collector{}
			if err := k8sClient.Get(ctx, crKey, cr); err != nil {
				return err
			}
			cr.Spec.RemoteAttributes = map[string]string{"team": "platform"}
			return k8sClient.Update(ctx, cr)
		}, discoveryTimeout, discoveryInterval).Should(Succeed())

		// Wait for at least one more poll (status.lastSyncTime advances).
		initialSync := getDiscovery(ctx).Status.LastSyncTime
		Eventually(func() bool {
			latest := getDiscovery(ctx).Status.LastSyncTime
			return latest != nil && (initialSync == nil || latest.After(initialSync.Time))
		}, discoveryTimeout, discoveryInterval).Should(BeTrue())

		// User edit must survive.
		cr := &fleetmanagementv1alpha1.Collector{}
		Expect(k8sClient.Get(ctx, crKey, cr)).To(Succeed())
		Expect(cr.Spec.RemoteAttributes).To(HaveKeyWithValue("team", "platform"))
	})

	It("with OnCollectorRemoved=Keep, a vanished collector is marked stale and the CR remains", func() {
		ctx := context.Background()

		collectorMock.setListResult([]*fleetclient.Collector{
			fleetCollector("edge-keep"),
		})

		createDiscovery(ctx, fleetmanagementv1alpha1.CollectorDiscoverySpec{
			PollInterval: shortPoll,
			Policy: fleetmanagementv1alpha1.DiscoveryPolicy{
				OnCollectorRemoved: fleetmanagementv1alpha1.DiscoveryOnRemovedKeep,
			},
		})

		Eventually(func() int {
			return len(listManagedCRs(ctx))
		}, discoveryTimeout, discoveryInterval).Should(Equal(1))

		// Now empty the list — the collector "vanished" from Fleet.
		collectorMock.setListResult(nil)

		// CR remains; status reports it as stale; CR has the stale annotation.
		Eventually(func() []string {
			return getDiscovery(ctx).Status.StaleCollectors
		}, discoveryTimeout, discoveryInterval).Should(ConsistOf("edge-keep"))

		crs := listManagedCRs(ctx)
		Expect(crs).To(HaveLen(1))
		Expect(crs[0].Annotations).To(HaveKeyWithValue(fleetmanagementv1alpha1.DiscoveryStaleAnnotation, "true"))
	})

	It("with OnCollectorRemoved=Delete, a vanished collector is removed", func() {
		ctx := context.Background()

		collectorMock.setListResult([]*fleetclient.Collector{
			fleetCollector("edge-delete"),
		})

		createDiscovery(ctx, fleetmanagementv1alpha1.CollectorDiscoverySpec{
			PollInterval: shortPoll,
			Policy: fleetmanagementv1alpha1.DiscoveryPolicy{
				OnCollectorRemoved: fleetmanagementv1alpha1.DiscoveryOnRemovedDelete,
			},
		})

		Eventually(func() int {
			return len(listManagedCRs(ctx))
		}, discoveryTimeout, discoveryInterval).Should(Equal(1))

		collectorMock.setListResult(nil)

		Eventually(func() int {
			return len(listManagedCRs(ctx))
		}, discoveryTimeout, discoveryInterval).Should(Equal(0))
	})

	It("a returning collector clears its own stale annotation", func() {
		ctx := context.Background()

		collectorMock.setListResult([]*fleetclient.Collector{
			fleetCollector("edge-flap"),
		})

		createDiscovery(ctx, fleetmanagementv1alpha1.CollectorDiscoverySpec{
			PollInterval: shortPoll,
		})

		Eventually(func() int {
			return len(listManagedCRs(ctx))
		}, discoveryTimeout, discoveryInterval).Should(Equal(1))

		collectorMock.setListResult(nil)
		Eventually(func() bool {
			crs := listManagedCRs(ctx)
			return len(crs) == 1 && crs[0].Annotations[fleetmanagementv1alpha1.DiscoveryStaleAnnotation] == "true"
		}, discoveryTimeout, discoveryInterval).Should(BeTrue())

		// Collector returns to Fleet.
		collectorMock.setListResult([]*fleetclient.Collector{
			fleetCollector("edge-flap"),
		})
		Eventually(func() bool {
			crs := listManagedCRs(ctx)
			return len(crs) == 1 && crs[0].Annotations[fleetmanagementv1alpha1.DiscoveryStaleAnnotation] == ""
		}, discoveryTimeout, discoveryInterval).Should(BeTrue())

		Expect(getDiscovery(ctx).Status.StaleCollectors).To(BeEmpty())
	})

	It("skips a manually-managed CR with the same name and records a conflict", func() {
		ctx := context.Background()

		// Pre-create a manually-managed Collector CR (no discovery label).
		manual := &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{Name: "edge-manual", Namespace: discoveryNS},
			Spec:       fleetmanagementv1alpha1.CollectorSpec{ID: "edge-manual", RemoteAttributes: map[string]string{"team": "ops"}},
		}
		Expect(k8sClient.Create(ctx, manual)).To(Succeed())

		collectorMock.setListResult([]*fleetclient.Collector{
			fleetCollector("edge-manual"),
		})

		createDiscovery(ctx, fleetmanagementv1alpha1.CollectorDiscoverySpec{
			PollInterval: shortPoll,
		})

		// Conflict surfaces in status.
		Eventually(func() []fleetmanagementv1alpha1.DiscoveryConflict {
			return getDiscovery(ctx).Status.Conflicts
		}, discoveryTimeout, discoveryInterval).Should(ContainElement(
			HaveField("Reason", fleetmanagementv1alpha1.DiscoveryConflictNotOwned),
		))

		// Manual CR is unchanged.
		fresh := &fleetmanagementv1alpha1.Collector{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: discoveryNS, Name: "edge-manual"}, fresh)).To(Succeed())
		Expect(fresh.Labels).NotTo(HaveKey(fleetmanagementv1alpha1.DiscoveryNameLabel))
		Expect(fresh.Spec.RemoteAttributes).To(HaveKeyWithValue("team", "ops"))
	})

	It("filters MarkedInactiveAt collectors by default", func() {
		ctx := context.Background()

		now := time.Now()
		collectorMock.setListResult([]*fleetclient.Collector{
			fleetCollector("active"),
			fleetCollector("inactive", withInactive(now)),
		})

		createDiscovery(ctx, fleetmanagementv1alpha1.CollectorDiscoverySpec{
			PollInterval: shortPoll,
		})

		Eventually(func() []string {
			ids := []string{}
			for _, cr := range listManagedCRs(ctx) {
				ids = append(ids, cr.Spec.ID)
			}
			return ids
		}, discoveryTimeout, discoveryInterval).Should(ConsistOf("active"))
	})

	It("includes inactive collectors when IncludeInactive=true", func() {
		ctx := context.Background()

		now := time.Now()
		collectorMock.setListResult([]*fleetclient.Collector{
			fleetCollector("active2"),
			fleetCollector("inactive2", withInactive(now)),
		})

		createDiscovery(ctx, fleetmanagementv1alpha1.CollectorDiscoverySpec{
			PollInterval:    shortPoll,
			IncludeInactive: true,
		})

		Eventually(func() []string {
			ids := []string{}
			for _, cr := range listManagedCRs(ctx) {
				ids = append(ids, cr.Spec.ID)
			}
			return ids
		}, discoveryTimeout, discoveryInterval).Should(ConsistOf("active2", "inactive2"))
	})

	It("intersects with selector.collectorIDs when set", func() {
		ctx := context.Background()

		collectorMock.setListResult([]*fleetclient.Collector{
			fleetCollector("edge-a"),
			fleetCollector("edge-b"),
			fleetCollector("edge-c"),
		})

		createDiscovery(ctx, fleetmanagementv1alpha1.CollectorDiscoverySpec{
			PollInterval: shortPoll,
			Selector: fleetmanagementv1alpha1.PolicySelector{
				CollectorIDs: []string{"edge-a", "edge-c"},
			},
		})

		Eventually(func() []string {
			ids := []string{}
			for _, cr := range listManagedCRs(ctx) {
				ids = append(ids, cr.Spec.ID)
			}
			return ids
		}, discoveryTimeout, discoveryInterval).Should(ConsistOf("edge-a", "edge-c"))
	})

	It("creates a CR with hash-suffixed name for an id that needs sanitization", func() {
		ctx := context.Background()

		collectorMock.setListResult([]*fleetclient.Collector{
			fleetCollector("Host.Example.COM"),
		})

		createDiscovery(ctx, fleetmanagementv1alpha1.CollectorDiscoverySpec{
			PollInterval: shortPoll,
		})

		Eventually(func() int {
			return len(listManagedCRs(ctx))
		}, discoveryTimeout, discoveryInterval).Should(Equal(1))

		crs := listManagedCRs(ctx)
		Expect(crs[0].Spec.ID).To(Equal("Host.Example.COM"))
		// Name should be DNS-1123 with a hash suffix (so different from the
		// raw sanitized "host-example-com").
		Expect(crs[0].Name).NotTo(Equal("host-example-com"))
		Expect(crs[0].Annotations).To(HaveKeyWithValue(
			fleetmanagementv1alpha1.FleetCollectorIDAnnotation,
			"Host.Example.COM",
		))
	})

	It("targets a different namespace via spec.targetNamespace", func() {
		ctx := context.Background()

		// Create the target namespace.
		targetNS := "discovery-target-" + uniqueDiscoverySuffix()
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: targetNS},
		})).To(Succeed())

		collectorMock.setListResult([]*fleetclient.Collector{
			fleetCollector("edge-target"),
		})

		createDiscovery(ctx, fleetmanagementv1alpha1.CollectorDiscoverySpec{
			PollInterval:    shortPoll,
			TargetNamespace: targetNS,
		})

		Eventually(func() int {
			return len(listManagedCRsIn(ctx, targetNS))
		}, discoveryTimeout, discoveryInterval).Should(Equal(1))

		// Nothing in the discovery's own namespace.
		Expect(listManagedCRs(ctx)).To(BeEmpty())
	})

	It("scopes same-named discoveries by source namespace in a shared target namespace", func() {
		ctx := context.Background()

		targetNS := "discovery-shared-target-" + uniqueDiscoverySuffix()
		otherDiscoveryNS := "discovery-other-" + uniqueDiscoverySuffix()
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: targetNS},
		})).To(Succeed())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: otherDiscoveryNS},
		})).To(Succeed())

		collectorMock.setListResult([]*fleetclient.Collector{
			fleetCollector("edge-a"),
		})

		createDiscovery(ctx, fleetmanagementv1alpha1.CollectorDiscoverySpec{
			PollInterval:    "1h",
			TargetNamespace: targetNS,
		})

		Eventually(func() int {
			return len(listManagedCRsIn(ctx, targetNS))
		}, discoveryTimeout, discoveryInterval).Should(Equal(1))

		collectorMock.setListResult([]*fleetclient.Collector{
			fleetCollector("edge-b"),
		})

		other := &fleetmanagementv1alpha1.CollectorDiscovery{
			ObjectMeta: metav1.ObjectMeta{Name: cdName, Namespace: otherDiscoveryNS},
			Spec: fleetmanagementv1alpha1.CollectorDiscoverySpec{
				PollInterval:    "1h",
				TargetNamespace: targetNS,
			},
		}
		Expect(k8sClient.Create(ctx, other)).To(Succeed())

		Eventually(func() int {
			return len(listManagedCRsIn(ctx, targetNS))
		}, discoveryTimeout, discoveryInterval).Should(Equal(2))

		edgeA := &fleetmanagementv1alpha1.Collector{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: targetNS, Name: "edge-a"}, edgeA)).To(Succeed())
		Expect(edgeA.Labels).To(HaveKeyWithValue(fleetmanagementv1alpha1.DiscoveryNamespaceLabel, discoveryNS))
		Expect(edgeA.Annotations).NotTo(HaveKey(fleetmanagementv1alpha1.DiscoveryStaleAnnotation))

		edgeB := &fleetmanagementv1alpha1.Collector{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: targetNS, Name: "edge-b"}, edgeB)).To(Succeed())
		Expect(edgeB.Labels).To(HaveKeyWithValue(fleetmanagementv1alpha1.DiscoveryNamespaceLabel, otherDiscoveryNS))

		collectorMock.setListResult(nil)
		Eventually(func() error {
			latest := &fleetmanagementv1alpha1.CollectorDiscovery{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: otherDiscoveryNS, Name: cdName}, latest); err != nil {
				return err
			}
			latest.Spec.IncludeInactive = true
			return k8sClient.Update(ctx, latest)
		}, discoveryTimeout, discoveryInterval).Should(Succeed())

		Eventually(func() string {
			cr := &fleetmanagementv1alpha1.Collector{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: targetNS, Name: "edge-b"}, cr); err != nil {
				return ""
			}
			return cr.Annotations[fleetmanagementv1alpha1.DiscoveryStaleAnnotation]
		}, discoveryTimeout, discoveryInterval).Should(Equal(fleetmanagementv1alpha1.DiscoveryStaleAnnotationValue))

		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: targetNS, Name: "edge-a"}, edgeA)).To(Succeed())
		Expect(edgeA.Annotations).NotTo(HaveKey(fleetmanagementv1alpha1.DiscoveryStaleAnnotation))
	})

	It("requeues with an error condition when ListCollectors fails", func() {
		ctx := context.Background()

		collectorMock.mu.Lock()
		collectorMock.listErr = fmt.Errorf("simulated network failure")
		collectorMock.mu.Unlock()

		createDiscovery(ctx, fleetmanagementv1alpha1.CollectorDiscoverySpec{
			PollInterval: shortPoll,
		})

		Eventually(func() metav1.ConditionStatus {
			cd := getDiscovery(ctx)
			for _, c := range cd.Status.Conditions {
				if c.Type == conditionTypeReady {
					return c.Status
				}
			}
			return ""
		}, discoveryTimeout, discoveryInterval).Should(Equal(metav1.ConditionFalse))
	})

	It("orphans (does not cascade-delete) discovered CRs when the CD is deleted", func() {
		ctx := context.Background()

		collectorMock.setListResult([]*fleetclient.Collector{
			fleetCollector("edge-orphan"),
		})

		cd := createDiscovery(ctx, fleetmanagementv1alpha1.CollectorDiscoverySpec{
			PollInterval: shortPoll,
		})

		Eventually(func() int {
			return len(listManagedCRs(ctx))
		}, discoveryTimeout, discoveryInterval).Should(Equal(1))

		Expect(k8sClient.Delete(ctx, cd)).To(Succeed())

		// Wait for the CD to be gone.
		Eventually(func() bool {
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: discoveryNS, Name: cdName},
				&fleetmanagementv1alpha1.CollectorDiscovery{})
			return apierrors.IsNotFound(err)
		}, discoveryTimeout, discoveryInterval).Should(BeTrue())

		// Mirrored CR survives — orphan-on-delete by design.
		Consistently(func() int {
			return len(listManagedCRs(ctx))
		}, 2*time.Second, 250*time.Millisecond).Should(Equal(1))
	})
})
