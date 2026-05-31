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
	"fmt"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// SubjectAccessReviewer decouples the discovery webhooks from the concrete
// Kubernetes clientset. It is the consumer-side interface for the
// SubjectAccessReview check that closes the cross-namespace "confused
// deputy" escalation: the operator must confirm that the user creating a
// PipelineDiscovery / CollectorDiscovery may itself write the mirrored CRs
// into the requested target namespace, rather than borrowing the operator's
// cluster-wide ServiceAccount permissions.
//
// Implementations must treat a nil receiver as a no-op so callers can pass
// nil when cross-namespace authorization enforcement is disabled, mirroring
// the existing MatcherChecker nil pattern.
//
// +kubebuilder:object:generate=false
type SubjectAccessReviewer interface {
	// Create issues a SubjectAccessReview against the API server and
	// returns the populated review (with .Status filled in) or an error.
	Create(ctx context.Context, sar *authorizationv1.SubjectAccessReview) (*authorizationv1.SubjectAccessReview, error)
}

// checkCrossNamespaceCreate runs a SubjectAccessReview that asks whether the
// admission requester may "create" the given resource (e.g. "pipelines",
// "collectors") in targetNamespace. It is the shared helper behind the
// PipelineDiscovery and CollectorDiscovery webhooks.
//
// It is only meaningful for a true cross-namespace write: callers must invoke
// it only when targetNamespace is non-empty and differs from the discovery
// CR's own namespace (a same-namespace write is already governed by the RBAC
// that let the user create the discovery CR in the first place).
//
// A nil reviewer short-circuits to allow (default-allow, back-compat) so
// callers do not need to inline the nil-guard.
func checkCrossNamespaceCreate(
	ctx context.Context,
	reviewer SubjectAccessReviewer,
	targetNamespace, resource string,
) error {
	if reviewer == nil {
		return nil
	}

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		// No admission request in context means we cannot identify the
		// requester. Fail closed: when enforcement is enabled we must not
		// authorize a cross-namespace write on behalf of an unknown user.
		return fmt.Errorf(
			"cannot authorize cross-namespace write of %s into namespace %q: no admission request in context: %w",
			resource, targetNamespace, err)
	}

	user := req.UserInfo
	sar := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: targetNamespace,
				Verb:      "create",
				Group:     "fleetmanagement.grafana.com",
				Resource:  resource,
			},
			User:   user.Username,
			Groups: user.Groups,
			UID:    user.UID,
			Extra:  convertExtra(user.Extra),
		},
	}

	reviewed, err := reviewer.Create(ctx, sar)
	if err != nil {
		return fmt.Errorf(
			"failed to verify permission to create %s in namespace %q via SubjectAccessReview: %w",
			resource, targetNamespace, err)
	}

	if reviewed == nil || !reviewed.Status.Allowed || reviewed.Status.Denied {
		reason := ""
		if reviewed != nil && reviewed.Status.Reason != "" {
			reason = ": " + reviewed.Status.Reason
		}
		return fmt.Errorf(
			"user %q is not permitted to create %s in target namespace %q "+
				"(required permission: create %s in %q)%s",
			user.Username, resource, targetNamespace, resource, targetNamespace, reason)
	}

	return nil
}

// convertExtra adapts the admission request's authentication Extra map to the
// authorization Extra map. The two packages declare structurally identical
// but distinct named types, so an explicit per-key conversion is required.
func convertExtra(in map[string]authenticationv1.ExtraValue) map[string]authorizationv1.ExtraValue {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]authorizationv1.ExtraValue, len(in))
	for k, v := range in {
		out[k] = authorizationv1.ExtraValue(v)
	}
	return out
}

// clientsetSubjectAccessReviewer adapts a function (typically
// clientset.AuthorizationV1().SubjectAccessReviews().Create) to the
// SubjectAccessReviewer interface. The function form keeps this package free
// of a direct client-go dependency for construction; cmd/main.go supplies the
// real clientset call.
//
// +kubebuilder:object:generate=false
type clientsetSubjectAccessReviewer struct {
	create func(ctx context.Context, sar *authorizationv1.SubjectAccessReview, opts metav1.CreateOptions) (*authorizationv1.SubjectAccessReview, error)
}

var _ SubjectAccessReviewer = &clientsetSubjectAccessReviewer{}

func (c *clientsetSubjectAccessReviewer) Create(ctx context.Context, sar *authorizationv1.SubjectAccessReview) (*authorizationv1.SubjectAccessReview, error) {
	return c.create(ctx, sar, metav1.CreateOptions{})
}

// NewSubjectAccessReviewer wraps a SubjectAccessReviews().Create-shaped
// function as a SubjectAccessReviewer. cmd/main.go calls this with
// clientset.AuthorizationV1().SubjectAccessReviews().Create when
// --enforce-cross-namespace-discovery-authz is set.
func NewSubjectAccessReviewer(
	create func(ctx context.Context, sar *authorizationv1.SubjectAccessReview, opts metav1.CreateOptions) (*authorizationv1.SubjectAccessReview, error),
) SubjectAccessReviewer {
	return &clientsetSubjectAccessReviewer{create: create}
}
