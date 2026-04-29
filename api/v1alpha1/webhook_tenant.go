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

	"k8s.io/apimachinery/pkg/util/validation/field"
)

// MatcherChecker decouples the Pipeline / RemoteAttributePolicy /
// ExternalAttributeSync webhooks from the concrete tenant.Checker so this
// package does not need to import internal/tenant (which would form an
// import cycle).
//
// Implementations must treat a nil receiver as a no-op so callers can pass
// nil when tenant policy enforcement is disabled.
//
// +kubebuilder:object:generate=false
type MatcherChecker interface {
	// Check evaluates tenant policy for the admission request in ctx and
	// the CR's namespace + matcher list. Returns nil to allow, non-nil to
	// reject with a user-facing error message.
	Check(ctx context.Context, namespace string, matchers []string) error

	// Matches reports whether at least one TenantPolicy currently matches
	// the requesting user (subject and namespace selector). It is the
	// signal the RemoteAttributePolicy / ExternalAttributeSync webhooks
	// use to decide whether the user's request is governed by tenancy at
	// all — distinguishing "default-allow because no policy applies" from
	// "policy applied and Check passed". When tenancy applies, those
	// webhooks reject any request that uses spec.selector.collectorIDs to
	// escape the matcher-based scope.
	//
	// Implementations must return (false, nil) when the receiver is nil
	// or no admission.Request lives in ctx, mirroring Check.
	Matches(ctx context.Context, namespace string) (bool, error)
}

// runTenantChecks is the shared admission helper for the
// RemoteAttributePolicy and ExternalAttributeSync validators. It runs the
// matcher-based Check first; on success, when the user's selector also
// names explicit collectorIDs and at least one TenantPolicy actually
// applies to the requesting user, it rejects the request. Without this
// guard a tenant-restricted user could escape their matcher scope by
// listing collectors directly under spec.selector.collectorIDs.
//
// A nil checker short-circuits the entire helper so callers do not need
// to inline the nil-guard.
func runTenantChecks(ctx context.Context, checker MatcherChecker, namespace string, matchers, collectorIDs []string) error {
	if checker == nil {
		return nil
	}
	if err := checker.Check(ctx, namespace, matchers); err != nil {
		return err
	}
	if len(collectorIDs) == 0 {
		return nil
	}
	matched, err := checker.Matches(ctx, namespace)
	if err != nil {
		return err
	}
	if !matched {
		return nil
	}
	return field.Forbidden(
		field.NewPath("spec", "selector", "collectorIDs"),
		"users under a TenantPolicy may not select collectors by ID; use matchers instead",
	)
}
