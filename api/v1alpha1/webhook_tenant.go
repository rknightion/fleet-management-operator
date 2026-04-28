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

import "context"

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
}
