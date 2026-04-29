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

// Package tenant implements the K8s-RBAC-driven tenant policy check that
// gates Pipeline / RemoteAttributePolicy / ExternalAttributeSync admission.
//
// Concept: a cluster-scoped TenantPolicy CR binds K8s subjects (groups,
// users, service accounts) to a set of required matchers. When any
// TenantPolicy matches the requesting user, the CR being created or
// updated must include at least one of the union of those policies'
// required matchers in its own matcher set. When no policy matches, the
// request is allowed (default-allow, opt-in tightening).
package tenant

import (
	"context"
	"fmt"
	"slices"
	"strings"

	authnv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
)

// serviceAccountUsernamePrefix is the canonical prefix the K8s API server
// puts in front of service-account UserInfo.Username values, e.g.
// "system:serviceaccount:argocd:argocd-billing".
const serviceAccountUsernamePrefix = "system:serviceaccount:"

// +kubebuilder:rbac:groups=fleetmanagement.grafana.com,resources=tenantpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

// Checker evaluates tenant policy against an admission request. A nil
// Checker is a valid no-op — call sites can pass nil when tenant policy
// enforcement is disabled and skip every other guard.
type Checker struct {
	client client.Reader
}

// NewChecker returns a Checker backed by the given reader. The reader
// must serve TenantPolicy and Namespace resources; in production this is
// the manager's cached client.
func NewChecker(c client.Reader) *Checker {
	if c == nil {
		return nil
	}
	return &Checker{client: c}
}

// Check evaluates tenant policy for the admission request carried in ctx.
//
// It returns nil when:
//   - the Checker is nil (enforcement disabled),
//   - no admission.Request is in ctx (non-admission caller; the CRD
//     controllers do not need their own status updates checked),
//   - no TenantPolicy subject matches the request's UserInfo, or
//   - at least one required matcher from a matching policy appears in
//     `matchers`.
//
// It returns an error when one or more policies match the user but none
// of their required matchers appear in `matchers`.
//
// `namespace` is the CR's namespace, used to evaluate
// TenantPolicy.spec.namespaceSelector.
// `matchers` is the CR's effective matcher list (Pipeline.spec.matchers
// or PolicySelector.matchers depending on the CR type).
func (c *Checker) Check(ctx context.Context, namespace string, matchers []string) error {
	if c == nil {
		return nil
	}

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		// No admission request in context — likely a controller-side caller.
		// Tenant policy is admission-only; let the request through.
		return nil
	}

	policies := &fleetmanagementv1alpha1.TenantPolicyList{}
	if err := c.client.List(ctx, policies); err != nil {
		return fmt.Errorf("failed to list TenantPolicy resources: %w", err)
	}

	matched := make([]fleetmanagementv1alpha1.TenantPolicy, 0, len(policies.Items))
	for _, p := range policies.Items {
		if !subjectMatchesUser(p.Spec.Subjects, req.UserInfo) {
			continue
		}
		ok, nsErr := c.namespaceMatches(ctx, p.Spec.NamespaceSelector, namespace)
		if nsErr != nil {
			return nsErr
		}
		if !ok {
			continue
		}
		matched = append(matched, p)
	}

	if len(matched) == 0 {
		return nil
	}

	// Union the required matchers across every matching policy. The CR
	// satisfies the check by including any one element of the union.
	allowed := make(map[string]struct{})
	for _, p := range matched {
		for _, m := range p.Spec.RequiredMatchers {
			allowed[m] = struct{}{}
		}
	}

	for _, m := range matchers {
		if _, ok := allowed[m]; ok {
			return nil
		}
	}

	policyNames := make([]string, 0, len(matched))
	for _, p := range matched {
		policyNames = append(policyNames, p.Name)
	}
	allowedList := make([]string, 0, len(allowed))
	for m := range allowed {
		allowedList = append(allowedList, m)
	}
	return fmt.Errorf(
		"tenant policy denies this request: matchers must include at least one of [%s] (required by TenantPolicy: %s)",
		strings.Join(allowedList, ", "),
		strings.Join(policyNames, ", "),
	)
}

// Matches reports whether at least one TenantPolicy currently matches the
// requesting user — both subject and namespace selector. It is the signal
// the RemoteAttributePolicy / ExternalAttributeSync webhooks use to gate
// the collectorIDs guard: when tenancy applies and the user supplied
// spec.selector.collectorIDs, those webhooks reject the request to keep
// the matcher-based scope from being bypassed.
//
// A nil Checker or missing admission.Request in ctx returns (false, nil) —
// "tenancy does not apply", which is the default-allow position.
// Namespace-fetch errors fail-open via namespaceMatches; they do not
// surface as Matches errors.
func (c *Checker) Matches(ctx context.Context, namespace string) (bool, error) {
	if c == nil {
		return false, nil
	}

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return false, nil
	}

	policies := &fleetmanagementv1alpha1.TenantPolicyList{}
	if err := c.client.List(ctx, policies); err != nil {
		return false, fmt.Errorf("failed to list TenantPolicy resources: %w", err)
	}

	for _, p := range policies.Items {
		if !subjectMatchesUser(p.Spec.Subjects, req.UserInfo) {
			continue
		}
		ok, nsErr := c.namespaceMatches(ctx, p.Spec.NamespaceSelector, namespace)
		if nsErr != nil {
			return false, nsErr
		}
		if ok {
			return true, nil
		}
	}

	return false, nil
}

// subjectMatchesUser returns true when any subject in `subjects` lines up
// with `info`. Group subjects match against UserInfo.Groups; User subjects
// match Username; ServiceAccount subjects match the canonical
// `system:serviceaccount:<ns>:<name>` username form.
func subjectMatchesUser(subjects []rbacv1.Subject, info authnv1.UserInfo) bool {
	for _, s := range subjects {
		switch s.Kind {
		case rbacv1.UserKind:
			if s.Name != "" && s.Name == info.Username {
				return true
			}
		case rbacv1.GroupKind:
			if slices.Contains(info.Groups, s.Name) {
				return true
			}
		case rbacv1.ServiceAccountKind:
			expected := serviceAccountUsernamePrefix + s.Namespace + ":" + s.Name
			if expected == info.Username {
				return true
			}
		}
	}
	return false
}

// namespaceMatches resolves whether the policy's NamespaceSelector covers
// the CR's namespace. A nil selector means "all namespaces"; an empty
// LabelSelector also matches everything (per K8s convention).
//
// Namespace-fetch errors are uniformly treated as "policy does not apply"
// (fail-open). The alternative — failing the admission request because a
// transient apiserver hiccup masked a label-selector evaluation — would
// block legitimate requests indefinitely whenever the cluster's
// namespace cache is unhealthy. NotFound, Forbidden, ServerTimeout, and
// any other error path collapse to the same outcome here. Errors are
// logged at warning level so operators can correlate.
func (c *Checker) namespaceMatches(ctx context.Context, sel *metav1.LabelSelector, namespace string) (bool, error) {
	if sel == nil {
		return true, nil
	}
	selector, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil {
		return false, fmt.Errorf("invalid namespaceSelector on TenantPolicy: %w", err)
	}
	if selector.Empty() {
		return true, nil
	}

	ns := &corev1.Namespace{}
	if err := c.client.Get(ctx, client.ObjectKey{Name: namespace}, ns); err != nil {
		logf.FromContext(ctx).Info(
			"namespace fetch failed; treating policy as non-applicable",
			"namespace", namespace,
			"error", err.Error(),
		)
		return false, nil
	}
	return selector.Matches(labels.Set(ns.Labels)), nil
}
