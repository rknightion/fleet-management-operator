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
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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

// findCondition looks up a condition by Type. Returns nil if not present.
func findPolicyCondition(p *fleetmanagementv1alpha1.RemoteAttributePolicy, condType string) *metav1.Condition {
	return meta.FindStatusCondition(p.Status.Conditions, condType)
}

// buildMatchingCollectorList returns n Collector objects all carrying
// `tier=edge` so they all match a policy with that matcher. Each
// Collector ID is suffixed with its index so the matched-set is
// non-degenerate (1000+ distinct IDs).
func buildMatchingCollectorList(ns string, n int) []*fleetmanagementv1alpha1.Collector {
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

// E4: Truncation tests for RemoteAttributePolicy.
//
// These tests use a fake K8s client (not envtest) so we can quickly fan
// out 1000+ Collector CRs without paying the apiserver round-trip cost
// per object. Each test reconciles the Policy directly and inspects the
// resulting status.
//
// Coverage:
//   - Truncated condition set when matched-set exceeds maxMatchedIDs
//   - Truncated condition CLEARED on transition back below the cap
//   - MatchedCount always reflects the FULL count, not the capped slice
//   - Warning event emitted on truncation
//   - No-op short-circuit doesn't preserve a stale Truncated=True (B4)
var _ = Describe("RemoteAttributePolicy Truncation", func() {
	const policyNS = "policy-trunc"

	type tc struct {
		name           string
		matchedCount   int
		wantTruncated  bool
		wantStatusLen  int
		wantEvent      bool
		wantEventCount int
	}

	cases := []tc{
		{name: "below cap (999)", matchedCount: 999, wantTruncated: false, wantStatusLen: 999, wantEvent: false},
		{name: "at cap (1000)", matchedCount: 1000, wantTruncated: false, wantStatusLen: 1000, wantEvent: false},
		{name: "just over cap (1001)", matchedCount: 1001, wantTruncated: true, wantStatusLen: maxMatchedIDs, wantEvent: true, wantEventCount: 1},
		{name: "well over cap (5000)", matchedCount: 5000, wantTruncated: true, wantStatusLen: maxMatchedIDs, wantEvent: true, wantEventCount: 1},
	}

	for _, tt := range cases {
		It("table case: "+tt.name, func() {
			ctx := context.Background()

			policy := &fleetmanagementv1alpha1.RemoteAttributePolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "trunc-policy",
					Namespace:  policyNS,
					Generation: 1,
				},
				Spec: fleetmanagementv1alpha1.RemoteAttributePolicySpec{
					Selector: fleetmanagementv1alpha1.PolicySelector{
						Matchers: []string{`tier="edge"`},
					},
					Attributes: map[string]string{"team": "platform"},
				},
			}

			objs := []client.Object{policy}
			for _, c := range buildMatchingCollectorList(policyNS, tt.matchedCount) {
				objs = append(objs, c)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithStatusSubresource(&fleetmanagementv1alpha1.RemoteAttributePolicy{}).
				WithObjects(objs...).
				Build()

			fakeRecorder := record.NewFakeRecorder(64)
			r := &RemoteAttributePolicyReconciler{
				Client:   fakeClient,
				Scheme:   scheme.Scheme,
				Recorder: fakeRecorder,
			}

			_, err := r.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{Namespace: policyNS, Name: "trunc-policy"},
			})
			Expect(err).NotTo(HaveOccurred())

			got := &fleetmanagementv1alpha1.RemoteAttributePolicy{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Namespace: policyNS, Name: "trunc-policy"}, got)).To(Succeed())

			// MatchedCount must always reflect the FULL count, not the cap.
			Expect(got.Status.MatchedCount).To(BeEquivalentTo(tt.matchedCount))
			Expect(got.Status.MatchedCollectorIDs).To(HaveLen(tt.wantStatusLen))

			truncCond := findPolicyCondition(got, policyConditionTypeTruncated)
			Expect(truncCond).NotTo(BeNil())
			if tt.wantTruncated {
				Expect(truncCond.Status).To(Equal(metav1.ConditionTrue))
				Expect(truncCond.Reason).To(Equal(policyConditionReasonTruncated))
			} else {
				Expect(truncCond.Status).To(Equal(metav1.ConditionFalse))
				Expect(truncCond.Reason).To(Equal(policyConditionReasonNotTruncated))
			}

			// Drain events. A Truncated reason should appear iff truncated.
			truncEvents := drainTruncatedEvents(fakeRecorder)
			if tt.wantEvent {
				Expect(truncEvents).To(BeNumerically(">=", tt.wantEventCount),
					"expected at least %d Truncated event(s), got %d", tt.wantEventCount, truncEvents)
			} else {
				Expect(truncEvents).To(Equal(0), "did not expect a Truncated event")
			}
		})
	}

	It("clears Truncated condition when matched-set drops below the cap", func() {
		// Step 1: 1500 matching collectors -> Truncated=True.
		// Step 2: drop to 800 collectors and re-reconcile -> Truncated=False.
		// This is the B4 lock-in: previous no-op compared only the capped
		// slice, so a transition where the new (untruncated) IDs happened
		// to equal the old capped slice would leave Truncated=True.
		ctx := context.Background()

		policy := &fleetmanagementv1alpha1.RemoteAttributePolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "trunc-clear",
				Namespace:  policyNS,
				Generation: 1,
			},
			Spec: fleetmanagementv1alpha1.RemoteAttributePolicySpec{
				Selector: fleetmanagementv1alpha1.PolicySelector{
					Matchers: []string{`tier="edge"`},
				},
				Attributes: map[string]string{"team": "platform"},
			},
		}

		// Initial fan-out: 1500 collectors > maxMatchedIDs.
		initial := buildMatchingCollectorList(policyNS, 1500)
		objs := []client.Object{policy}
		for _, c := range initial {
			objs = append(objs, c)
		}
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&fleetmanagementv1alpha1.RemoteAttributePolicy{}).
			WithObjects(objs...).
			Build()
		fakeRecorder := record.NewFakeRecorder(64)
		r := &RemoteAttributePolicyReconciler{
			Client:   fakeClient,
			Scheme:   scheme.Scheme,
			Recorder: fakeRecorder,
		}

		_, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: policyNS, Name: "trunc-clear"},
		})
		Expect(err).NotTo(HaveOccurred())

		got := &fleetmanagementv1alpha1.RemoteAttributePolicy{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Namespace: policyNS, Name: "trunc-clear"}, got)).To(Succeed())
		Expect(got.Status.MatchedCount).To(BeEquivalentTo(1500))
		Expect(got.Status.MatchedCollectorIDs).To(HaveLen(maxMatchedIDs))
		truncCond := findPolicyCondition(got, policyConditionTypeTruncated)
		Expect(truncCond).NotTo(BeNil())
		Expect(truncCond.Status).To(Equal(metav1.ConditionTrue))

		// Step 2: delete collectors above index 800 so MatchedCount=800,
		// well below the cap. Re-reconcile (Generation unchanged — B4
		// scenario: spec didn't change but matched-set did).
		for i := 800; i < 1500; i++ {
			c := initial[i]
			Expect(fakeClient.Delete(ctx, c)).To(Succeed())
		}
		_, err = r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: policyNS, Name: "trunc-clear"},
		})
		Expect(err).NotTo(HaveOccurred())

		got2 := &fleetmanagementv1alpha1.RemoteAttributePolicy{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Namespace: policyNS, Name: "trunc-clear"}, got2)).To(Succeed())
		Expect(got2.Status.MatchedCount).To(BeEquivalentTo(800))
		Expect(got2.Status.MatchedCollectorIDs).To(HaveLen(800))
		truncCond2 := findPolicyCondition(got2, policyConditionTypeTruncated)
		Expect(truncCond2).NotTo(BeNil())
		Expect(truncCond2.Status).To(Equal(metav1.ConditionFalse),
			"Truncated condition must be cleared when matched-set drops below cap (B4)")
		Expect(truncCond2.Reason).To(Equal(policyConditionReasonNotTruncated))
	})
})

// drainTruncatedEvents pulls events off a fake recorder's Events channel
// without blocking and returns the count of Warning events whose message
// contains "Truncated".
func drainTruncatedEvents(rec *record.FakeRecorder) int {
	count := 0
	for {
		select {
		case ev := <-rec.Events:
			if containsTruncated(ev) {
				count++
			}
		default:
			return count
		}
	}
}

// containsTruncated reports whether the (FakeRecorder format) event string
// contains the substring "Truncated".
func containsTruncated(s string) bool {
	return strings.Contains(s, "Truncated")
}
