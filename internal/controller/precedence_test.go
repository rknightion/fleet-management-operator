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

// This file contains cross-layer integration tests that prove the documented
// precedence — ExternalAttributeSync > Collector spec > RemoteAttributePolicy —
// works end-to-end. Each spec runs against the suite-managed manager so all
// four reconcilers participate, the watches are wired exactly as in
// production, and the assertions look at the Collector's effective status
// rather than at an isolated unit.

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

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
	"github.com/grafana/fleet-management-operator/pkg/sources"
)

var precedenceTestCounter atomic.Uint64

func uniquePrecedenceSuffix() string {
	return fmt.Sprintf("%d", precedenceTestCounter.Add(1))
}

var _ = Describe("Cross-layer attribute precedence", func() {
	const (
		precedenceTimeout  = 15 * time.Second
		precedenceInterval = 250 * time.Millisecond
	)

	var (
		precedenceNS string
		collectorID  string
	)

	BeforeEach(func() {
		ctx := context.Background()
		suffix := uniquePrecedenceSuffix()
		precedenceNS = "precedence-" + suffix
		collectorID = "edge-" + suffix
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: precedenceNS},
		})).To(Succeed())

		// Reset shared mocks so each test starts from a clean slate.
		collectorMock.reset()
		externalSyncFakeSource = &fakeSource{}

		// Pre-register the collector with localAttributes so policy
		// matchers based on attributes (not just IDs) can fire.
		registerMockCollector(collectorID, map[string]string{
			"region": "us-east-1", "collector.os": "linux",
		})
	})

	collectorEffective := func(ctx context.Context) map[string]string {
		c := &fleetmanagementv1alpha1.Collector{}
		err := k8sClient.Get(ctx, types.NamespacedName{Namespace: precedenceNS, Name: "edge"}, c)
		if err != nil {
			return nil
		}
		return c.Status.EffectiveRemoteAttributes
	}

	collectorOwnerKindFor := func(ctx context.Context, key string) fleetmanagementv1alpha1.AttributeOwnerKind {
		c := &fleetmanagementv1alpha1.Collector{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: precedenceNS, Name: "edge"}, c); err != nil {
			return ""
		}
		for _, o := range c.Status.AttributeOwners {
			if o.Key == key {
				return o.OwnerKind
			}
		}
		return ""
	}

	createCollector := func(ctx context.Context, attrs map[string]string) *fleetmanagementv1alpha1.Collector {
		c := &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: precedenceNS},
			Spec: fleetmanagementv1alpha1.CollectorSpec{
				ID:               collectorID,
				RemoteAttributes: attrs,
			},
		}
		Expect(k8sClient.Create(ctx, c)).To(Succeed())
		return c
	}

	createPolicy := func(ctx context.Context, name string, priority int32, attrs map[string]string, matchers []string) *fleetmanagementv1alpha1.RemoteAttributePolicy {
		p := &fleetmanagementv1alpha1.RemoteAttributePolicy{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: precedenceNS},
			Spec: fleetmanagementv1alpha1.RemoteAttributePolicySpec{
				Selector: fleetmanagementv1alpha1.PolicySelector{
					Matchers: matchers,
				},
				Attributes: attrs,
				Priority:   priority,
			},
		}
		Expect(k8sClient.Create(ctx, p)).To(Succeed())
		return p
	}

	createExternalSync := func(ctx context.Context, name string, records []sources.Record, mapping fleetmanagementv1alpha1.AttributeMapping) *fleetmanagementv1alpha1.ExternalAttributeSync {
		externalSyncFakeSource.setRecords(records)
		s := &fleetmanagementv1alpha1.ExternalAttributeSync{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: precedenceNS},
			Spec: fleetmanagementv1alpha1.ExternalAttributeSyncSpec{
				Source: fleetmanagementv1alpha1.ExternalSource{
					Kind: fleetmanagementv1alpha1.ExternalSourceKindHTTP,
					HTTP: &fleetmanagementv1alpha1.HTTPSourceSpec{URL: "http://example/"},
				},
				Schedule: "5m",
				Selector: fleetmanagementv1alpha1.PolicySelector{
					CollectorIDs: []string{collectorID},
				},
				Mapping: mapping,
			},
		}
		Expect(k8sClient.Create(ctx, s)).To(Succeed())
		return s
	}

	It("Collector spec wins over a matching Policy on shared keys; Policy supplies the rest", func() {
		ctx := context.Background()

		createPolicy(ctx, "linux-defaults", 0, map[string]string{
			"env":  "staging", // overridden by Collector spec
			"team": "platform",
		}, []string{"region=us-east-1"})

		createCollector(ctx, map[string]string{"env": "prod"})

		Eventually(func() map[string]string { return collectorEffective(ctx) },
			precedenceTimeout, precedenceInterval,
		).Should(Equal(map[string]string{
			"env":  "prod",
			"team": "platform",
		}))

		Expect(collectorOwnerKindFor(ctx, "env")).To(Equal(fleetmanagementv1alpha1.AttributeOwnerCollector))
		Expect(collectorOwnerKindFor(ctx, "team")).To(Equal(fleetmanagementv1alpha1.AttributeOwnerRemoteAttributePolicy))
	})

	It("ExternalAttributeSync wins over Collector spec on shared keys", func() {
		ctx := context.Background()

		createExternalSync(ctx, "cmdb",
			[]sources.Record{{"hostname": collectorID, "env": "from-cmdb"}},
			fleetmanagementv1alpha1.AttributeMapping{
				CollectorIDField: "hostname",
				AttributeFields:  map[string]string{"env": "env"},
			},
		)

		createCollector(ctx, map[string]string{"env": "prod"})

		Eventually(func() string { return collectorEffective(ctx)["env"] },
			precedenceTimeout, precedenceInterval,
		).Should(Equal("from-cmdb"))

		Expect(collectorOwnerKindFor(ctx, "env")).To(Equal(fleetmanagementv1alpha1.AttributeOwnerExternalAttributeSync))
	})

	It("Higher Priority Policy wins among multiple matching Policies", func() {
		ctx := context.Background()

		createPolicy(ctx, "low-prio", 0, map[string]string{"env": "loser"},
			[]string{"region=us-east-1"})
		createPolicy(ctx, "high-prio", 100, map[string]string{"env": "winner"},
			[]string{"region=us-east-1"})

		createCollector(ctx, nil)

		Eventually(func() string { return collectorEffective(ctx)["env"] },
			precedenceTimeout, precedenceInterval,
		).Should(Equal("winner"))
	})

	It("Deleting a Policy that uniquely owned a key REMOVEs it from Fleet", func() {
		ctx := context.Background()

		policy := createPolicy(ctx, "team-default", 0, map[string]string{"team": "platform"},
			[]string{"region=us-east-1"})
		createCollector(ctx, nil)

		// Wait for the team key to be applied.
		Eventually(func() string { return collectorEffective(ctx)["team"] },
			precedenceTimeout, precedenceInterval,
		).Should(Equal("platform"))

		preDeleteCount := collectorMockBulkUpdateCount()

		Expect(k8sClient.Delete(ctx, policy)).To(Succeed())

		// Wait for the policy CR to be fully gone (the watch fires on deletion).
		Eventually(func() bool {
			err := k8sClient.Get(ctx,
				types.NamespacedName{Namespace: precedenceNS, Name: "team-default"},
				&fleetmanagementv1alpha1.RemoteAttributePolicy{})
			return apierrors.IsNotFound(err)
		}, precedenceTimeout, precedenceInterval).Should(BeTrue())

		// And then for the Collector controller to issue the REMOVE op.
		Eventually(func() bool {
			_, present := collectorEffective(ctx)["team"]
			return !present
		}, precedenceTimeout, precedenceInterval).Should(BeTrue(), "team key should be removed from effective state")

		Expect(collectorMockBulkUpdateCount()).To(BeNumerically(">", preDeleteCount),
			"expected at least one BulkUpdateCollectors call after policy deletion")
	})
})
