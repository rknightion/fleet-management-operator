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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
	"github.com/grafana/fleet-management-operator/pkg/fleetclient"
)

var policyTestCounter atomic.Uint64

// uniqueSuffix returns a short, monotonic suffix unique within a test run.
// We use it to keep namespaces and collector IDs distinct across specs.
func uniqueSuffix() string {
	return fmt.Sprintf("%d", policyTestCounter.Add(1))
}

var _ = Describe("RemoteAttributePolicy Controller", func() {
	const (
		policyTimeout  = 10 * time.Second
		policyInterval = 250 * time.Millisecond
	)

	// Each test gets its own namespace so concurrent specs don't clobber
	// each other's collectors / policies.
	var policyNS string

	BeforeEach(func() {
		ctx := context.Background()
		policyNS = "policy-" + uniqueSuffix()
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: policyNS},
		})).To(Succeed())
		// The collector mock is shared with the collector controller. Reset
		// it so leftover state from a previous spec doesn't bleed in.
		collectorMock.reset()
	})

	It("matches collectors using spec.selector.matchers and ignores non-matching ones", func() {
		ctx := context.Background()

		eastID := "collector-east-" + uniqueSuffix()
		westID := "collector-west-" + uniqueSuffix()

		// Pre-register the collectors in the Fleet mock with distinct
		// region values BEFORE creating the K8s resources. The collector
		// controller's first GetCollector returns these LocalAttributes
		// and writes them to status, which is what the policy controller
		// reads when evaluating its selector.
		registerMockCollector(eastID, map[string]string{"region": "us-east-1", "collector.os": "linux"})
		registerMockCollector(westID, map[string]string{"region": "us-west-2", "collector.os": "linux"})

		Expect(k8sClient.Create(ctx, &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{Name: "east", Namespace: policyNS},
			Spec:       fleetmanagementv1alpha1.CollectorSpec{ID: eastID, RemoteAttributes: map[string]string{}},
		})).To(Succeed())
		Expect(k8sClient.Create(ctx, &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{Name: "west", Namespace: policyNS},
			Spec:       fleetmanagementv1alpha1.CollectorSpec{ID: westID, RemoteAttributes: map[string]string{}},
		})).To(Succeed())

		policyName := "match-east-only"
		policy := &fleetmanagementv1alpha1.RemoteAttributePolicy{
			ObjectMeta: metav1.ObjectMeta{Name: policyName, Namespace: policyNS},
			Spec: fleetmanagementv1alpha1.RemoteAttributePolicySpec{
				Selector: fleetmanagementv1alpha1.PolicySelector{
					Matchers: []string{`region="us-east-1"`},
				},
				Attributes: map[string]string{"team": "platform"},
			},
		}
		Expect(k8sClient.Create(ctx, policy)).To(Succeed())

		Eventually(func(g Gomega) {
			got := &fleetmanagementv1alpha1.RemoteAttributePolicy{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: policyName, Namespace: policyNS}, got)).To(Succeed())
			g.Expect(got.Status.MatchedCollectorIDs).To(ConsistOf(eastID))
			g.Expect(got.Status.MatchedCount).To(BeEquivalentTo(1))
			g.Expect(readyCondition(got)).To(Equal(metav1.ConditionTrue))
			g.Expect(readyReason(got)).To(Equal(policyReasonMatched))
		}, policyTimeout, policyInterval).Should(Succeed())
	})

	It("matches collectors via spec.selector.collectorIDs explicit list", func() {
		ctx := context.Background()

		listedID := "collector-listed-" + uniqueSuffix()
		otherID := "collector-other-" + uniqueSuffix()

		Expect(k8sClient.Create(ctx, &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{Name: "listed", Namespace: policyNS},
			Spec:       fleetmanagementv1alpha1.CollectorSpec{ID: listedID, RemoteAttributes: map[string]string{}},
		})).To(Succeed())
		Expect(k8sClient.Create(ctx, &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: policyNS},
			Spec:       fleetmanagementv1alpha1.CollectorSpec{ID: otherID, RemoteAttributes: map[string]string{}},
		})).To(Succeed())

		policyName := "explicit-id-list"
		policy := &fleetmanagementv1alpha1.RemoteAttributePolicy{
			ObjectMeta: metav1.ObjectMeta{Name: policyName, Namespace: policyNS},
			Spec: fleetmanagementv1alpha1.RemoteAttributePolicySpec{
				Selector: fleetmanagementv1alpha1.PolicySelector{
					CollectorIDs: []string{listedID},
				},
				Attributes: map[string]string{"team": "platform"},
			},
		}
		Expect(k8sClient.Create(ctx, policy)).To(Succeed())

		Eventually(func() []string {
			got := &fleetmanagementv1alpha1.RemoteAttributePolicy{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: policyName, Namespace: policyNS}, got); err != nil {
				return nil
			}
			return got.Status.MatchedCollectorIDs
		}, policyTimeout, policyInterval).Should(ConsistOf(listedID))
	})

	It("treats an empty selector as matching nothing", func() {
		ctx := context.Background()

		Expect(k8sClient.Create(ctx, &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: policyNS},
			Spec:       fleetmanagementv1alpha1.CollectorSpec{ID: "id-alpha-" + uniqueSuffix(), RemoteAttributes: map[string]string{}},
		})).To(Succeed())

		policyName := "empty-selector"
		policy := &fleetmanagementv1alpha1.RemoteAttributePolicy{
			ObjectMeta: metav1.ObjectMeta{Name: policyName, Namespace: policyNS},
			Spec: fleetmanagementv1alpha1.RemoteAttributePolicySpec{
				Selector:   fleetmanagementv1alpha1.PolicySelector{},
				Attributes: map[string]string{"team": "platform"},
			},
		}
		Expect(k8sClient.Create(ctx, policy)).To(Succeed())

		Eventually(func(g Gomega) {
			got := &fleetmanagementv1alpha1.RemoteAttributePolicy{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: policyName, Namespace: policyNS}, got)).To(Succeed())
			g.Expect(readyReason(got)).To(Equal(policyReasonNoMatch))
			g.Expect(readyCondition(got)).To(Equal(metav1.ConditionFalse))
			g.Expect(got.Status.MatchedCollectorIDs).To(BeEmpty())
			g.Expect(got.Status.MatchedCount).To(BeEquivalentTo(0))
		}, policyTimeout, policyInterval).Should(Succeed())
	})

	It("re-reconciles when a new matching Collector is added", func() {
		ctx := context.Background()

		policyName := "watch-fanout"
		policy := &fleetmanagementv1alpha1.RemoteAttributePolicy{
			ObjectMeta: metav1.ObjectMeta{Name: policyName, Namespace: policyNS},
			Spec: fleetmanagementv1alpha1.RemoteAttributePolicySpec{
				Selector: fleetmanagementv1alpha1.PolicySelector{
					Matchers: []string{`tier="edge"`},
				},
				Attributes: map[string]string{"team": "platform"},
			},
		}
		Expect(k8sClient.Create(ctx, policy)).To(Succeed())

		// Wait for the first reconcile to record the no-match state.
		Eventually(func() string {
			got := &fleetmanagementv1alpha1.RemoteAttributePolicy{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: policyName, Namespace: policyNS}, got); err != nil {
				return ""
			}
			return readyReason(got)
		}, policyTimeout, policyInterval).Should(Equal(policyReasonNoMatch))

		// Now add a Collector that matches. Pre-register its mock state
		// so the collector controller writes the right LocalAttributes to
		// status, which the policy controller will then evaluate.
		newID := "edge-collector-" + uniqueSuffix()
		registerMockCollector(newID, map[string]string{"tier": "edge", "collector.os": "linux"})
		Expect(k8sClient.Create(ctx, &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{Name: "edge1", Namespace: policyNS},
			Spec:       fleetmanagementv1alpha1.CollectorSpec{ID: newID, RemoteAttributes: map[string]string{}},
		})).To(Succeed())

		Eventually(func() []string {
			got := &fleetmanagementv1alpha1.RemoteAttributePolicy{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: policyName, Namespace: policyNS}, got); err != nil {
				return nil
			}
			return got.Status.MatchedCollectorIDs
		}, policyTimeout, policyInterval).Should(ConsistOf(newID))
	})

	It("skips reconcile when ObservedGeneration matches the current generation", func() {
		ctx := context.Background()

		policyName := "no-op-skip"
		policy := &fleetmanagementv1alpha1.RemoteAttributePolicy{
			ObjectMeta: metav1.ObjectMeta{Name: policyName, Namespace: policyNS},
			Spec: fleetmanagementv1alpha1.RemoteAttributePolicySpec{
				Selector:   fleetmanagementv1alpha1.PolicySelector{},
				Attributes: map[string]string{"team": "platform"},
			},
		}
		Expect(k8sClient.Create(ctx, policy)).To(Succeed())

		// Wait for the suite-managed reconciler to populate status.
		Eventually(func() int64 {
			got := &fleetmanagementv1alpha1.RemoteAttributePolicy{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: policyName, Namespace: policyNS}, got); err != nil {
				return -1
			}
			return got.Status.ObservedGeneration
		}, policyTimeout, policyInterval).Should(Equal(int64(1)))

		got := &fleetmanagementv1alpha1.RemoteAttributePolicy{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: policyName, Namespace: policyNS}, got)).To(Succeed())
		firstTransition := readyTransitionTime(got)
		Expect(firstTransition.IsZero()).To(BeFalse(), "expected Ready condition to be set after first reconcile")

		// Manually invoke Reconcile a second time without changing the
		// spec. ObservedGeneration == Generation, so the controller must
		// short-circuit. Result must be no-op (no error), and the Ready
		// condition LastTransitionTime must not change.
		probe := &RemoteAttributePolicyReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
		result, err := probe.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: policyNS, Name: policyName},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeFalse())
		Expect(result.RequeueAfter).To(BeZero())

		got2 := &fleetmanagementv1alpha1.RemoteAttributePolicy{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: policyName, Namespace: policyNS}, got2)).To(Succeed())
		Expect(readyTransitionTime(got2).Time.Equal(firstTransition.Time)).To(BeTrue(),
			"second reconcile with unchanged spec must not bump the Ready transition time")
	})
})

// --- test helpers ---

// readyCondition returns the Status of the Ready condition (or empty).
func readyCondition(p *fleetmanagementv1alpha1.RemoteAttributePolicy) metav1.ConditionStatus {
	for _, c := range p.Status.Conditions {
		if c.Type == conditionTypeReady {
			return c.Status
		}
	}
	return ""
}

// readyReason returns the Reason of the Ready condition (or empty).
func readyReason(p *fleetmanagementv1alpha1.RemoteAttributePolicy) string {
	for _, c := range p.Status.Conditions {
		if c.Type == conditionTypeReady {
			return c.Reason
		}
	}
	return ""
}

// readyTransitionTime returns the LastTransitionTime of the Ready
// condition. Used to prove no second status update happened on a no-op
// reconcile.
func readyTransitionTime(p *fleetmanagementv1alpha1.RemoteAttributePolicy) metav1.Time {
	for _, c := range p.Status.Conditions {
		if c.Type == conditionTypeReady {
			return c.LastTransitionTime
		}
	}
	return metav1.Time{}
}

// registerMockCollector pre-populates the shared collector mock with a
// Fleet-side record for the given collector ID, including the specific
// LocalAttributes the test expects to see propagated into status. Call
// this BEFORE creating the K8s Collector resource so the controller's
// first GetCollector returns the right shape.
func registerMockCollector(id string, localAttrs map[string]string) {
	GinkgoHelper()
	now := time.Now()
	collectorMock.register(&fleetclient.Collector{
		ID:               id,
		RemoteAttributes: map[string]string{},
		LocalAttributes:  localAttrs,
		CollectorType:    "COLLECTOR_TYPE_ALLOY",
		CreatedAt:        &now,
		UpdatedAt:        &now,
	})
}
