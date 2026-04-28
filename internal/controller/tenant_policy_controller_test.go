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
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
)

var tenantPolicyTestCounter atomic.Uint64

func tpUniqueName(prefix string) string {
	return prefix + "-" + uniqueSuffixFromCounter(&tenantPolicyTestCounter)
}

// uniqueSuffixFromCounter mirrors uniqueSuffix() but on a private counter
// so TenantPolicy specs do not collide with policy spec namespaces.
func uniqueSuffixFromCounter(c *atomic.Uint64) string {
	return formatUint(c.Add(1))
}

func formatUint(v uint64) string {
	if v == 0 {
		return "0"
	}
	digits := make([]byte, 0, 20)
	for v > 0 {
		digits = append(digits, byte('0'+v%10))
		v /= 10
	}
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}

var _ = Describe("TenantPolicy Controller", func() {
	const (
		tpTimeout  = 10 * time.Second
		tpInterval = 250 * time.Millisecond
	)

	tpCondition := func(p *fleetmanagementv1alpha1.TenantPolicy, t string) *metav1.Condition {
		return meta.FindStatusCondition(p.Status.Conditions, t)
	}

	It("sets Ready=True and Valid=True for a well-formed policy", func() {
		ctx := context.Background()

		name := tpUniqueName("valid")
		tp := &fleetmanagementv1alpha1.TenantPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: fleetmanagementv1alpha1.TenantPolicySpec{
				Subjects: []rbacv1.Subject{
					{Kind: rbacv1.GroupKind, Name: "team-a"},
				},
				RequiredMatchers: []string{`team="team-a"`},
			},
		}
		Expect(k8sClient.Create(ctx, tp)).To(Succeed())

		Eventually(func(g Gomega) {
			got := &fleetmanagementv1alpha1.TenantPolicy{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, got)).To(Succeed())
			g.Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
			g.Expect(got.Status.BoundSubjectCount).To(BeEquivalentTo(1))

			ready := tpCondition(got, conditionTypeReady)
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(ready.Reason).To(Equal(tenantPolicyReasonValid))

			valid := tpCondition(got, tenantPolicyConditionTypeValid)
			g.Expect(valid).NotTo(BeNil())
			g.Expect(valid.Status).To(Equal(metav1.ConditionTrue))
		}, tpTimeout, tpInterval).Should(Succeed())
	})

	It("sets Ready=False/Valid=False/Reason=ParseError for an invalid matcher syntax", func() {
		ctx := context.Background()

		name := tpUniqueName("invalid")
		tp := &fleetmanagementv1alpha1.TenantPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: fleetmanagementv1alpha1.TenantPolicySpec{
				Subjects: []rbacv1.Subject{
					{Kind: rbacv1.GroupKind, Name: "team-b"},
					{Kind: rbacv1.UserKind, Name: "alice"},
				},
				// "key==value" uses '==' which the matcher syntax validator
				// rejects ("use '=' not '=='"). Schema validation does not
				// catch this so the reconciler is the only signal.
				RequiredMatchers: []string{`team=="team-b"`},
			},
		}
		Expect(k8sClient.Create(ctx, tp)).To(Succeed())

		Eventually(func(g Gomega) {
			got := &fleetmanagementv1alpha1.TenantPolicy{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, got)).To(Succeed())
			g.Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
			g.Expect(got.Status.BoundSubjectCount).To(BeEquivalentTo(2))

			ready := tpCondition(got, conditionTypeReady)
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(ready.Reason).To(Equal(tenantPolicyReasonParseError))

			valid := tpCondition(got, tenantPolicyConditionTypeValid)
			g.Expect(valid).NotTo(BeNil())
			g.Expect(valid.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(valid.Reason).To(Equal(tenantPolicyReasonParseError))
			g.Expect(valid.Message).To(ContainSubstring("requiredMatchers[0]"))
		}, tpTimeout, tpInterval).Should(Succeed())
	})

	It("bumps observedGeneration when the spec changes", func() {
		ctx := context.Background()

		name := tpUniqueName("regen")
		tp := &fleetmanagementv1alpha1.TenantPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: fleetmanagementv1alpha1.TenantPolicySpec{
				Subjects: []rbacv1.Subject{
					{Kind: rbacv1.GroupKind, Name: "team-c"},
				},
				RequiredMatchers: []string{`team="team-c"`},
			},
		}
		Expect(k8sClient.Create(ctx, tp)).To(Succeed())

		var initialGen int64
		Eventually(func(g Gomega) {
			got := &fleetmanagementv1alpha1.TenantPolicy{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, got)).To(Succeed())
			g.Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
			initialGen = got.Generation
		}, tpTimeout, tpInterval).Should(Succeed())

		// Add a second subject and confirm BoundSubjectCount and
		// ObservedGeneration both update.
		Eventually(func() error {
			got := &fleetmanagementv1alpha1.TenantPolicy{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, got); err != nil {
				return err
			}
			got.Spec.Subjects = append(got.Spec.Subjects, rbacv1.Subject{
				Kind: rbacv1.UserKind, Name: "bob",
			})
			return k8sClient.Update(ctx, got)
		}, tpTimeout, tpInterval).Should(Succeed())

		Eventually(func(g Gomega) {
			got := &fleetmanagementv1alpha1.TenantPolicy{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, got)).To(Succeed())
			g.Expect(got.Generation).To(BeNumerically(">", initialGen))
			g.Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
			g.Expect(got.Status.BoundSubjectCount).To(BeEquivalentTo(2))
		}, tpTimeout, tpInterval).Should(Succeed())
	})
})
