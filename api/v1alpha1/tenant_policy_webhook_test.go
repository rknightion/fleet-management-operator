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

package v1alpha1

import (
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTenantPolicy_Validate(t *testing.T) {
	longMatcher := "team=" + strings.Repeat("a", tenantPolicyMaxMatcherLength)

	tests := []struct {
		name    string
		policy  *TenantPolicy
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid group subject",
			policy: &TenantPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "team-billing"},
				Spec: TenantPolicySpec{
					Subjects: []rbacv1.Subject{
						{Kind: rbacv1.GroupKind, Name: "team-billing-engineers"},
					},
					RequiredMatchers: []string{"team=billing"},
				},
			},
			wantErr: false,
		},
		{
			name: "valid service account subject",
			policy: &TenantPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "argocd-tenant"},
				Spec: TenantPolicySpec{
					Subjects: []rbacv1.Subject{
						{Kind: rbacv1.ServiceAccountKind, Name: "argocd", Namespace: "argocd"},
					},
					RequiredMatchers: []string{"team=billing", "team=billing-shared"},
				},
			},
			wantErr: false,
		},
		{
			name: "valid namespaceSelector",
			policy: &TenantPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "team-billing"},
				Spec: TenantPolicySpec{
					Subjects: []rbacv1.Subject{
						{Kind: rbacv1.GroupKind, Name: "team-billing-engineers"},
					},
					RequiredMatchers: []string{"team=billing"},
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"tenant": "billing"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "empty subjects",
			policy: &TenantPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "no-subjects"},
				Spec: TenantPolicySpec{
					Subjects:         []rbacv1.Subject{},
					RequiredMatchers: []string{"team=billing"},
				},
			},
			wantErr: true,
			errMsg:  "spec.subjects must contain at least one entry",
		},
		{
			name: "empty requiredMatchers",
			policy: &TenantPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "no-matchers"},
				Spec: TenantPolicySpec{
					Subjects: []rbacv1.Subject{
						{Kind: rbacv1.GroupKind, Name: "team-billing-engineers"},
					},
					RequiredMatchers: []string{},
				},
			},
			wantErr: true,
			errMsg:  "spec.requiredMatchers must contain at least one entry",
		},
		{
			name: "unknown subject kind",
			policy: &TenantPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-kind"},
				Spec: TenantPolicySpec{
					Subjects: []rbacv1.Subject{
						{Kind: "Robot", Name: "rosie"},
					},
					RequiredMatchers: []string{"team=billing"},
				},
			},
			wantErr: true,
			errMsg:  "must be one of User, Group, ServiceAccount",
		},
		{
			name: "service account missing namespace",
			policy: &TenantPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "sa-no-ns"},
				Spec: TenantPolicySpec{
					Subjects: []rbacv1.Subject{
						{Kind: rbacv1.ServiceAccountKind, Name: "argocd"},
					},
					RequiredMatchers: []string{"team=billing"},
				},
			},
			wantErr: true,
			errMsg:  "namespace is required for Kind=ServiceAccount",
		},
		{
			name: "user with namespace",
			policy: &TenantPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "user-ns"},
				Spec: TenantPolicySpec{
					Subjects: []rbacv1.Subject{
						{Kind: rbacv1.UserKind, Name: "alice", Namespace: "default"},
					},
					RequiredMatchers: []string{"team=billing"},
				},
			},
			wantErr: true,
			errMsg:  "namespace must be empty for Kind=User",
		},
		{
			name: "subject missing name",
			policy: &TenantPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "no-name"},
				Spec: TenantPolicySpec{
					Subjects: []rbacv1.Subject{
						{Kind: rbacv1.GroupKind, Name: ""},
					},
					RequiredMatchers: []string{"team=billing"},
				},
			},
			wantErr: true,
			errMsg:  "name must be set",
		},
		{
			name: "matcher with invalid syntax",
			policy: &TenantPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-matcher"},
				Spec: TenantPolicySpec{
					Subjects: []rbacv1.Subject{
						{Kind: rbacv1.GroupKind, Name: "g"},
					},
					RequiredMatchers: []string{"team==billing"},
				},
			},
			wantErr: true,
			errMsg:  "use '=' not '=='",
		},
		{
			name: "matcher exceeding 200 char cap",
			policy: &TenantPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "long-matcher"},
				Spec: TenantPolicySpec{
					Subjects: []rbacv1.Subject{
						{Kind: rbacv1.GroupKind, Name: "g"},
					},
					RequiredMatchers: []string{longMatcher},
				},
			},
			wantErr: true,
			errMsg:  "exceeds 200 character limit",
		},
		{
			name: "invalid namespaceSelector",
			policy: &TenantPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-selector"},
				Spec: TenantPolicySpec{
					Subjects: []rbacv1.Subject{
						{Kind: rbacv1.GroupKind, Name: "g"},
					},
					RequiredMatchers: []string{"team=billing"},
					NamespaceSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{Key: "tenant", Operator: "BadOp", Values: []string{"billing"}},
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "namespaceSelector is not a valid LabelSelector",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.validateTenantPolicy()
			if (err != nil) != tt.wantErr {
				t.Errorf("validateTenantPolicy() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("validateTenantPolicy() error %q does not contain %q", err.Error(), tt.errMsg)
			}
		})
	}
}
