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

package tenant

import (
	"context"
	"errors"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	authnv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(fleetmanagementv1alpha1.AddToScheme(scheme))
	return scheme
}

func ctxWithUser(t *testing.T, info authnv1.UserInfo) context.Context {
	t.Helper()
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{UserInfo: info},
	}
	return admission.NewContextWithRequest(context.Background(), req)
}

func TestChecker_NilCheckerAllows(t *testing.T) {
	var c *Checker
	if err := c.Check(context.Background(), "default", []string{"team=billing"}); err != nil {
		t.Fatalf("nil Checker should be a no-op, got %v", err)
	}
}

func TestChecker_NoAdmissionRequestAllows(t *testing.T) {
	scheme := newScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := NewChecker(cl)
	if err := c.Check(context.Background(), "default", []string{"team=billing"}); err != nil {
		t.Fatalf("missing admission.Request should be a no-op, got %v", err)
	}
}

func TestChecker_NoMatchingPolicyAllows(t *testing.T) {
	scheme := newScheme(t)
	policies := []runtime.Object{
		&fleetmanagementv1alpha1.TenantPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "team-billing"},
			Spec: fleetmanagementv1alpha1.TenantPolicySpec{
				Subjects:         []rbacv1.Subject{{Kind: rbacv1.GroupKind, Name: "billing"}},
				RequiredMatchers: []string{"team=billing"},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(policies...).Build()
	c := NewChecker(cl)

	ctx := ctxWithUser(t, authnv1.UserInfo{
		Username: "alice",
		Groups:   []string{"unrelated-group"},
	})
	if err := c.Check(ctx, "default", []string{}); err != nil {
		t.Fatalf("user not in any policy subject should be allowed, got %v", err)
	}
}

func TestChecker_GroupMatchRequiresMatcher(t *testing.T) {
	scheme := newScheme(t)
	policies := []runtime.Object{
		&fleetmanagementv1alpha1.TenantPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "team-billing"},
			Spec: fleetmanagementv1alpha1.TenantPolicySpec{
				Subjects:         []rbacv1.Subject{{Kind: rbacv1.GroupKind, Name: "team-billing-engineers"}},
				RequiredMatchers: []string{"team=billing"},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(policies...).Build()
	c := NewChecker(cl)

	ctx := ctxWithUser(t, authnv1.UserInfo{
		Username: "alice",
		Groups:   []string{"team-billing-engineers"},
	})

	if err := c.Check(ctx, "default", []string{"team=billing", "env=prod"}); err != nil {
		t.Fatalf("CR with required matcher should pass, got %v", err)
	}

	err := c.Check(ctx, "default", []string{"env=prod"})
	if err == nil {
		t.Fatalf("CR missing required matcher should be rejected")
	}
	if !strings.Contains(err.Error(), "team=billing") {
		t.Fatalf("error should mention required matcher, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "team-billing") {
		t.Fatalf("error should mention policy name, got %q", err.Error())
	}
}

func TestChecker_UserMatch(t *testing.T) {
	scheme := newScheme(t)
	policies := []runtime.Object{
		&fleetmanagementv1alpha1.TenantPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "alice-policy"},
			Spec: fleetmanagementv1alpha1.TenantPolicySpec{
				Subjects:         []rbacv1.Subject{{Kind: rbacv1.UserKind, Name: "alice@example.com"}},
				RequiredMatchers: []string{"team=billing"},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(policies...).Build()
	c := NewChecker(cl)

	ctx := ctxWithUser(t, authnv1.UserInfo{Username: "alice@example.com"})
	if err := c.Check(ctx, "default", []string{"team=billing"}); err != nil {
		t.Fatalf("matched user with required matcher should pass, got %v", err)
	}
	if err := c.Check(ctx, "default", []string{"team=other"}); err == nil {
		t.Fatalf("matched user without required matcher should be rejected")
	}
}

func TestChecker_ServiceAccountMatch(t *testing.T) {
	scheme := newScheme(t)
	policies := []runtime.Object{
		&fleetmanagementv1alpha1.TenantPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "argocd-billing"},
			Spec: fleetmanagementv1alpha1.TenantPolicySpec{
				Subjects: []rbacv1.Subject{
					{Kind: rbacv1.ServiceAccountKind, Name: "argocd", Namespace: "argocd"},
				},
				RequiredMatchers: []string{"team=billing"},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(policies...).Build()
	c := NewChecker(cl)

	saCtx := ctxWithUser(t, authnv1.UserInfo{
		Username: "system:serviceaccount:argocd:argocd",
		Groups:   []string{"system:serviceaccounts:argocd", "system:authenticated"},
	})
	if err := c.Check(saCtx, "default", []string{"team=billing"}); err != nil {
		t.Fatalf("matching SA with required matcher should pass, got %v", err)
	}
	if err := c.Check(saCtx, "default", []string{}); err == nil {
		t.Fatalf("matching SA missing required matcher should be rejected")
	}

	// Different SA, same name but different namespace: should NOT match.
	otherSAStrong := ctxWithUser(t, authnv1.UserInfo{
		Username: "system:serviceaccount:other-ns:argocd",
	})
	if err := c.Check(otherSAStrong, "default", []string{}); err != nil {
		t.Fatalf("non-matching SA should be allowed (no policy applies), got %v", err)
	}
}

func TestChecker_MultiPolicyUnion(t *testing.T) {
	scheme := newScheme(t)
	policies := []runtime.Object{
		&fleetmanagementv1alpha1.TenantPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "team-billing"},
			Spec: fleetmanagementv1alpha1.TenantPolicySpec{
				Subjects:         []rbacv1.Subject{{Kind: rbacv1.GroupKind, Name: "team-billing"}},
				RequiredMatchers: []string{"team=billing"},
			},
		},
		&fleetmanagementv1alpha1.TenantPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "shared-infra"},
			Spec: fleetmanagementv1alpha1.TenantPolicySpec{
				Subjects:         []rbacv1.Subject{{Kind: rbacv1.GroupKind, Name: "shared-infra-owners"}},
				RequiredMatchers: []string{"team=shared"},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(policies...).Build()
	c := NewChecker(cl)

	// User is in BOTH groups — union of required matchers includes both.
	ctx := ctxWithUser(t, authnv1.UserInfo{
		Username: "alice",
		Groups:   []string{"team-billing", "shared-infra-owners"},
	})

	if err := c.Check(ctx, "default", []string{"team=shared"}); err != nil {
		t.Fatalf("union should allow either required matcher, got %v", err)
	}
	if err := c.Check(ctx, "default", []string{"team=billing"}); err != nil {
		t.Fatalf("union should allow either required matcher, got %v", err)
	}
	if err := c.Check(ctx, "default", []string{"env=prod"}); err == nil {
		t.Fatalf("CR missing every union element should be rejected")
	}
}

// TestChecker_NamespaceFetchErrorFailsClosed pins P1-07: any non-nil error
// from the namespace Get (NotFound, Forbidden, ServerTimeout, etc.) must
// fail closed because the checker cannot prove the namespace selector does
// not apply.
func TestChecker_NamespaceFetchErrorFailsClosed(t *testing.T) {
	scheme := newScheme(t)

	policy := &fleetmanagementv1alpha1.TenantPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "team-billing"},
		Spec: fleetmanagementv1alpha1.TenantPolicySpec{
			Subjects:         []rbacv1.Subject{{Kind: rbacv1.GroupKind, Name: "team-billing"}},
			RequiredMatchers: []string{"team=billing"},
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"tenant": "billing"},
			},
		},
	}

	// Inject a generic apiserver error on Namespace Get — emulates a
	// transient server-side failure that is neither NotFound nor Forbidden.
	transientErr := errors.New("etcdserver: request timed out")
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(policy).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*corev1.Namespace); ok {
					return transientErr
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).
		Build()
	c := NewChecker(cl)

	ctx := ctxWithUser(t, authnv1.UserInfo{
		Username: "alice",
		Groups:   []string{"team-billing"},
	})

	// The CR has no required matcher and the namespace fetch fails, so the
	// checker must fail closed instead of treating the policy as
	// non-applicable.
	err := c.Check(ctx, "billing", []string{})
	if err == nil {
		t.Fatalf("namespace fetch error must fail closed and reject the request")
	}
	if !strings.Contains(err.Error(), `failed to get namespace "billing"`) {
		t.Fatalf("error should mention namespace lookup failure, got %q", err.Error())
	}

	matched, matchErr := c.Matches(ctx, "billing")
	if matchErr == nil {
		t.Fatalf("Matches must also fail closed on namespace lookup errors")
	}
	if matched {
		t.Fatalf("Matches should not report true when namespace lookup failed")
	}
}

// TestChecker_MatchesReturnsTrueWhenPolicyApplies pins the C4 underpinning:
// Matches must report true when at least one TenantPolicy matches the
// requesting user (subject + namespace selector). The webhook helpers use
// this signal to decide whether to apply the collectorIDs guard.
func TestChecker_MatchesReturnsTrueWhenPolicyApplies(t *testing.T) {
	scheme := newScheme(t)
	policies := []runtime.Object{
		&fleetmanagementv1alpha1.TenantPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "team-billing"},
			Spec: fleetmanagementv1alpha1.TenantPolicySpec{
				Subjects:         []rbacv1.Subject{{Kind: rbacv1.GroupKind, Name: "team-billing"}},
				RequiredMatchers: []string{"team=billing"},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(policies...).Build()
	c := NewChecker(cl)

	ctx := ctxWithUser(t, authnv1.UserInfo{
		Username: "alice",
		Groups:   []string{"team-billing"},
	})
	matched, err := c.Matches(ctx, "default")
	if err != nil {
		t.Fatalf("Matches returned error: %v", err)
	}
	if !matched {
		t.Fatalf("expected Matches=true for user in policy subject group")
	}
}

func TestChecker_MatchesReturnsFalseWhenNoPolicyApplies(t *testing.T) {
	scheme := newScheme(t)
	policies := []runtime.Object{
		&fleetmanagementv1alpha1.TenantPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "team-billing"},
			Spec: fleetmanagementv1alpha1.TenantPolicySpec{
				Subjects:         []rbacv1.Subject{{Kind: rbacv1.GroupKind, Name: "team-billing"}},
				RequiredMatchers: []string{"team=billing"},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(policies...).Build()
	c := NewChecker(cl)

	ctx := ctxWithUser(t, authnv1.UserInfo{
		Username: "alice",
		Groups:   []string{"unrelated-group"},
	})
	matched, err := c.Matches(ctx, "default")
	if err != nil {
		t.Fatalf("Matches returned error: %v", err)
	}
	if matched {
		t.Fatalf("expected Matches=false for user not in any policy subject")
	}
}

func TestChecker_MatchesNilCheckerAndNoAdmissionRequest(t *testing.T) {
	var nilC *Checker
	if matched, err := nilC.Matches(context.Background(), "default"); err != nil || matched {
		t.Fatalf("nil Checker.Matches must be (false, nil), got (%v, %v)", matched, err)
	}

	scheme := newScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := NewChecker(cl)
	if matched, err := c.Matches(context.Background(), "default"); err != nil || matched {
		t.Fatalf("Matches without admission.Request must be (false, nil), got (%v, %v)", matched, err)
	}
}

func TestChecker_NamespaceSelector(t *testing.T) {
	scheme := newScheme(t)

	billingNS := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "billing",
			Labels: map[string]string{"tenant": "billing"},
		},
	}
	otherNS := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "other",
			Labels: map[string]string{"tenant": "other"},
		},
	}

	policies := []runtime.Object{
		&fleetmanagementv1alpha1.TenantPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "team-billing"},
			Spec: fleetmanagementv1alpha1.TenantPolicySpec{
				Subjects:         []rbacv1.Subject{{Kind: rbacv1.GroupKind, Name: "team-billing"}},
				RequiredMatchers: []string{"team=billing"},
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"tenant": "billing"},
				},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(policies...).
		WithRuntimeObjects(billingNS, otherNS).
		Build()
	c := NewChecker(cl)

	ctx := ctxWithUser(t, authnv1.UserInfo{
		Username: "alice",
		Groups:   []string{"team-billing"},
	})

	// In billing namespace: policy applies. Without required matcher → reject.
	if err := c.Check(ctx, "billing", []string{}); err == nil {
		t.Fatalf("policy should apply in matching namespace")
	}

	// In other namespace: policy does NOT apply (selector mismatch).
	if err := c.Check(ctx, "other", []string{}); err != nil {
		t.Fatalf("policy should not apply in non-matching namespace, got %v", err)
	}
}
