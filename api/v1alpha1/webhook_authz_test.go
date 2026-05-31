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
	"context"
	"errors"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// errSAR is a shared sentinel used by the discovery webhook tests to assert
// that a SubjectAccessReview transport error is surfaced (not swallowed).
var errSAR = errors.New("apiserver: SubjectAccessReview create failed")

// fakeReviewer is a test double for SubjectAccessReviewer. It records the SAR
// it was handed and returns a configured verdict (allow / deny) or an error.
// Shared by the PipelineDiscovery and CollectorDiscovery webhook tests.
type fakeReviewer struct {
	allow bool
	deny  bool
	err   error

	called    bool
	gotSAR    *authorizationv1.SubjectAccessReview
	callCount int
}

func (f *fakeReviewer) Create(_ context.Context, sar *authorizationv1.SubjectAccessReview) (*authorizationv1.SubjectAccessReview, error) {
	f.called = true
	f.callCount++
	f.gotSAR = sar
	if f.err != nil {
		return nil, f.err
	}
	out := sar.DeepCopy()
	out.Status = authorizationv1.SubjectAccessReviewStatus{
		Allowed: f.allow,
		Denied:  f.deny,
	}
	return out, nil
}

// ctxWithUser builds a context carrying an admission.Request whose UserInfo is
// populated, so checkCrossNamespaceCreate can read the requester identity. The
// task documents this injection pattern.
func ctxWithUser(username string, groups []string) context.Context {
	return admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UserInfo: authenticationv1.UserInfo{
				Username: username,
				UID:      "uid-" + username,
				Groups:   groups,
				Extra: map[string]authenticationv1.ExtraValue{
					"scopes.authorization.openshift.io": {"user:full"},
				},
			},
		},
	})
}

func TestCheckCrossNamespaceCreate_NilReviewerSkips(t *testing.T) {
	// Default-allow / back-compat: a nil reviewer is a no-op even with no
	// admission request in context.
	if err := checkCrossNamespaceCreate(context.Background(), nil, "team-b", "pipelines"); err != nil {
		t.Fatalf("nil reviewer must skip the SAR, got %v", err)
	}
}

func TestCheckCrossNamespaceCreate_Allowed(t *testing.T) {
	r := &fakeReviewer{allow: true}
	ctx := ctxWithUser("alice", []string{"dev"})
	if err := checkCrossNamespaceCreate(ctx, r, "team-b", "pipelines"); err != nil {
		t.Fatalf("expected allow, got %v", err)
	}
	if !r.called {
		t.Fatalf("reviewer should have been consulted")
	}
	// Assert the SAR was populated with the requester identity and the right
	// resource attributes.
	ra := r.gotSAR.Spec.ResourceAttributes
	if ra == nil {
		t.Fatalf("SAR must set ResourceAttributes")
	}
	if ra.Namespace != "team-b" || ra.Verb != "create" ||
		ra.Group != "fleetmanagement.grafana.com" || ra.Resource != "pipelines" {
		t.Errorf("unexpected ResourceAttributes: %+v", ra)
	}
	if r.gotSAR.Spec.User != "alice" || r.gotSAR.Spec.UID != "uid-alice" {
		t.Errorf("SAR user/uid not propagated: user=%q uid=%q", r.gotSAR.Spec.User, r.gotSAR.Spec.UID)
	}
	if len(r.gotSAR.Spec.Groups) != 1 || r.gotSAR.Spec.Groups[0] != "dev" {
		t.Errorf("SAR groups not propagated: %v", r.gotSAR.Spec.Groups)
	}
	if v, ok := r.gotSAR.Spec.Extra["scopes.authorization.openshift.io"]; !ok || len(v) != 1 || v[0] != "user:full" {
		t.Errorf("SAR extra not converted: %+v", r.gotSAR.Spec.Extra)
	}
}

func TestCheckCrossNamespaceCreate_DeniedNotAllowed(t *testing.T) {
	// allow=false, deny=false (authorizer has no opinion) must reject.
	r := &fakeReviewer{allow: false}
	ctx := ctxWithUser("mallory", nil)
	err := checkCrossNamespaceCreate(ctx, r, "team-b", "collectors")
	if err == nil {
		t.Fatalf("expected rejection when SAR is not allowed")
	}
	for _, want := range []string{"mallory", "team-b", "collectors", "create"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err.Error(), want)
		}
	}
}

func TestCheckCrossNamespaceCreate_ExplicitDenyRejects(t *testing.T) {
	r := &fakeReviewer{allow: false, deny: true}
	ctx := ctxWithUser("mallory", nil)
	if err := checkCrossNamespaceCreate(ctx, r, "team-b", "pipelines"); err == nil {
		t.Fatalf("expected rejection on explicit Denied")
	}
}

func TestCheckCrossNamespaceCreate_AllowedButDeniedRejects(t *testing.T) {
	// Defense in depth: if a (malformed) verdict says Allowed AND Denied, treat
	// it as a denial.
	r := &fakeReviewer{allow: true, deny: true}
	ctx := ctxWithUser("eve", nil)
	if err := checkCrossNamespaceCreate(ctx, r, "team-b", "pipelines"); err == nil {
		t.Fatalf("expected rejection when verdict is both Allowed and Denied")
	}
}

func TestCheckCrossNamespaceCreate_ReviewerErrorPropagates(t *testing.T) {
	sentinel := errors.New("apiserver: SAR create failed")
	r := &fakeReviewer{err: sentinel}
	ctx := ctxWithUser("alice", nil)
	err := checkCrossNamespaceCreate(ctx, r, "team-b", "pipelines")
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected SAR create error to propagate, got %v", err)
	}
}

func TestCheckCrossNamespaceCreate_NoRequestInContextFailsClosed(t *testing.T) {
	// With a non-nil reviewer but no admission request in context, we cannot
	// identify the requester; the check must fail closed rather than allow.
	r := &fakeReviewer{allow: true}
	if err := checkCrossNamespaceCreate(context.Background(), r, "team-b", "pipelines"); err == nil {
		t.Fatalf("expected fail-closed when no admission request is in context")
	}
	if r.called {
		t.Errorf("reviewer must not be consulted when the requester is unknown")
	}
}
