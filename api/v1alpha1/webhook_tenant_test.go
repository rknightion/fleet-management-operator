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
	"reflect"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// teamBillingNS is the namespace used in this file's table-driven tests
// to assert the validator wrapper passes the right namespace through to
// the tenant checker.
const teamBillingNS = "team-billing"

// fakeChecker records its inputs and returns a configured error. The
// validator wrappers should pass the CR's namespace and the right matcher
// list (Pipeline.Spec.Matchers, Spec.Selector.Matchers, etc.) through to
// the checker.
//
// matches gates the C4 collectorIDs guard: tests can simulate "this user is
// under a TenantPolicy" by setting matches=true. matchesErr lets a test
// inject a Matches() failure independently of Check().
type fakeChecker struct {
	err            error
	called         bool
	calledNs       string
	calledMatchers []string

	matches        bool
	matchesErr     error
	matchesCalled  bool
	matchesCalledN string
}

func (f *fakeChecker) Check(_ context.Context, ns string, matchers []string) error {
	f.called = true
	f.calledNs = ns
	f.calledMatchers = matchers
	return f.err
}

func (f *fakeChecker) Matches(_ context.Context, ns string) (bool, error) {
	f.matchesCalled = true
	f.matchesCalledN = ns
	return f.matches, f.matchesErr
}

func TestPipelineValidator_NilCheckerSkipsTenantStep(t *testing.T) {
	v := &pipelineValidator{checker: nil}
	p := &Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"},
		Spec: PipelineSpec{
			Contents:   "prometheus.scrape \"x\" {}",
			ConfigType: ConfigTypeAlloy,
			Enabled:    boolPtr(true),
			Matchers:   []string{"team=billing"},
		},
	}
	if _, err := v.ValidateCreate(context.Background(), p); err != nil {
		t.Fatalf("create with nil checker should pass, got %v", err)
	}
}

func TestPipelineValidator_ChecksMatchersAndRejectsOnError(t *testing.T) {
	deny := errors.New("denied by tenant policy")
	c := &fakeChecker{err: deny}
	v := &pipelineValidator{checker: c}

	p := &Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: teamBillingNS},
		Spec: PipelineSpec{
			Contents:   "prometheus.scrape \"x\" {}",
			ConfigType: ConfigTypeAlloy,
			Enabled:    boolPtr(true),
			Matchers:   []string{"team=other", "env=prod"},
		},
	}

	_, err := v.ValidateCreate(context.Background(), p)
	if !errors.Is(err, deny) {
		t.Fatalf("expected denial from tenant checker, got %v", err)
	}
	if !c.called {
		t.Fatalf("checker should have been called")
	}
	if c.calledNs != teamBillingNS {
		t.Errorf("expected namespace team-billing, got %q", c.calledNs)
	}
	want := []string{"team=other", "env=prod"}
	if !reflect.DeepEqual(c.calledMatchers, want) {
		t.Errorf("matchers passed to checker = %v, want %v", c.calledMatchers, want)
	}
}

func TestPipelineValidator_ChecksMatchersOnUpdate(t *testing.T) {
	c := &fakeChecker{}
	v := &pipelineValidator{checker: c}

	old := &Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: teamBillingNS},
		Spec: PipelineSpec{
			Contents:   "prometheus.scrape \"x\" {}",
			ConfigType: ConfigTypeAlloy,
			Enabled:    boolPtr(true),
			Matchers:   []string{"team=billing"},
		},
	}
	updated := old.DeepCopy()
	updated.Spec.Matchers = []string{"team=billing", "env=prod"}

	if _, err := v.ValidateUpdate(context.Background(), old, updated); err != nil {
		t.Fatalf("update should pass with checker returning nil, got %v", err)
	}
	if !c.called {
		t.Fatalf("checker should have been called on update")
	}
	want := []string{"team=billing", "env=prod"}
	if !reflect.DeepEqual(c.calledMatchers, want) {
		t.Errorf("update matchers = %v, want %v", c.calledMatchers, want)
	}
}

func TestPipelineValidator_SpecValidationRunsBeforeTenantCheck(t *testing.T) {
	// A pipeline with empty contents fails spec validation. The tenant
	// checker should NOT be consulted in that case — there's no point
	// asking the tenant layer about a CR that's already invalid.
	c := &fakeChecker{}
	v := &pipelineValidator{checker: c}

	p := &Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"},
		Spec: PipelineSpec{
			Contents:   "",
			ConfigType: ConfigTypeAlloy,
			Enabled:    boolPtr(true),
		},
	}
	if _, err := v.ValidateCreate(context.Background(), p); err == nil {
		t.Fatalf("expected spec validation to reject empty contents")
	}
	if c.called {
		t.Fatalf("tenant checker should not be called when spec is invalid")
	}
}

func TestRemoteAttributePolicyValidator_PassesSelectorMatchers(t *testing.T) {
	c := &fakeChecker{}
	v := &remoteAttributePolicyValidator{checker: c}

	p := &RemoteAttributePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: teamBillingNS},
		Spec: RemoteAttributePolicySpec{
			Attributes: map[string]string{"k": "v"},
			Selector: PolicySelector{
				Matchers: []string{"team=billing"},
			},
		},
	}
	if _, err := v.ValidateCreate(context.Background(), p); err != nil {
		t.Fatalf("expected valid policy to pass, got %v", err)
	}
	want := []string{"team=billing"}
	if !reflect.DeepEqual(c.calledMatchers, want) {
		t.Errorf("expected selector matchers passed through, got %v", c.calledMatchers)
	}
	if c.calledNs != teamBillingNS {
		t.Errorf("expected namespace team-billing, got %q", c.calledNs)
	}
}

func TestExternalAttributeSyncValidator_PassesSelectorMatchers(t *testing.T) {
	c := &fakeChecker{}
	v := &externalAttributeSyncValidator{checker: c}

	es := &ExternalAttributeSync{
		ObjectMeta: metav1.ObjectMeta{Name: "es", Namespace: teamBillingNS},
		Spec: ExternalAttributeSyncSpec{
			Schedule: "5m",
			Source: ExternalSource{
				Kind: ExternalSourceKindHTTP,
				HTTP: &HTTPSourceSpec{URL: "https://example.com/cmdb", Method: "GET"},
			},
			Mapping: AttributeMapping{
				CollectorIDField: "id",
				AttributeFields:  map[string]string{"team": "team"},
			},
			Selector: PolicySelector{
				Matchers: []string{"team=billing"},
			},
		},
	}
	if _, err := v.ValidateCreate(context.Background(), es); err != nil {
		t.Fatalf("expected valid sync to pass, got %v", err)
	}
	want := []string{"team=billing"}
	if !reflect.DeepEqual(c.calledMatchers, want) {
		t.Errorf("expected selector matchers passed through, got %v", c.calledMatchers)
	}
	if c.calledNs != teamBillingNS {
		t.Errorf("expected namespace team-billing, got %q", c.calledNs)
	}
}

func TestRemoteAttributePolicyValidator_NilCheckerSkipsTenantStep(t *testing.T) {
	v := &remoteAttributePolicyValidator{checker: nil}
	p := &RemoteAttributePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"},
		Spec: RemoteAttributePolicySpec{
			Attributes: map[string]string{"k": "v"},
			Selector: PolicySelector{
				Matchers: []string{"team=billing"},
			},
		},
	}
	if _, err := v.ValidateCreate(context.Background(), p); err != nil {
		t.Fatalf("create with nil checker should pass, got %v", err)
	}
}

func TestRemoteAttributePolicyValidator_CheckerErrorCausesRejection(t *testing.T) {
	deny := errors.New("denied by tenant policy")
	c := &fakeChecker{err: deny}
	v := &remoteAttributePolicyValidator{checker: c}

	p := &RemoteAttributePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: teamBillingNS},
		Spec: RemoteAttributePolicySpec{
			Attributes: map[string]string{"k": "v"},
			Selector: PolicySelector{
				Matchers: []string{"team=billing"},
			},
		},
	}
	_, err := v.ValidateCreate(context.Background(), p)
	if !errors.Is(err, deny) {
		t.Fatalf("expected denial from tenant checker, got %v", err)
	}
}

func TestRemoteAttributePolicyValidator_CheckerCalledOnUpdate(t *testing.T) {
	c := &fakeChecker{}
	v := &remoteAttributePolicyValidator{checker: c}

	old := &RemoteAttributePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: teamBillingNS},
		Spec: RemoteAttributePolicySpec{
			Attributes: map[string]string{"k": "v"},
			Selector:   PolicySelector{Matchers: []string{"team=billing"}},
		},
	}
	updated := old.DeepCopy()
	updated.Spec.Selector.Matchers = []string{"team=billing", "env=prod"}

	if _, err := v.ValidateUpdate(context.Background(), old, updated); err != nil {
		t.Fatalf("update should pass with checker returning nil, got %v", err)
	}
	if !c.called {
		t.Fatalf("checker should have been called on update")
	}
	if c.calledNs != teamBillingNS {
		t.Errorf("expected namespace team-billing, got %q", c.calledNs)
	}
	want := []string{"team=billing", "env=prod"}
	if !reflect.DeepEqual(c.calledMatchers, want) {
		t.Errorf("update matchers = %v, want %v", c.calledMatchers, want)
	}
}

func TestExternalAttributeSyncValidator_NilCheckerSkipsTenantStep(t *testing.T) {
	v := &externalAttributeSyncValidator{checker: nil}
	es := &ExternalAttributeSync{
		ObjectMeta: metav1.ObjectMeta{Name: "es", Namespace: "default"},
		Spec: ExternalAttributeSyncSpec{
			Schedule: "5m",
			Source: ExternalSource{
				Kind: ExternalSourceKindHTTP,
				HTTP: &HTTPSourceSpec{URL: "https://example.com/cmdb", Method: "GET"},
			},
			Mapping: AttributeMapping{
				CollectorIDField: "id",
				AttributeFields:  map[string]string{"team": "team"},
			},
			Selector: PolicySelector{Matchers: []string{"team=billing"}},
		},
	}
	if _, err := v.ValidateCreate(context.Background(), es); err != nil {
		t.Fatalf("create with nil checker should pass, got %v", err)
	}
}

func TestExternalAttributeSyncValidator_CheckerErrorCausesRejection(t *testing.T) {
	deny := errors.New("denied by tenant policy")
	c := &fakeChecker{err: deny}
	v := &externalAttributeSyncValidator{checker: c}

	es := &ExternalAttributeSync{
		ObjectMeta: metav1.ObjectMeta{Name: "es", Namespace: teamBillingNS},
		Spec: ExternalAttributeSyncSpec{
			Schedule: "5m",
			Source: ExternalSource{
				Kind: ExternalSourceKindHTTP,
				HTTP: &HTTPSourceSpec{URL: "https://example.com/cmdb", Method: "GET"},
			},
			Mapping: AttributeMapping{
				CollectorIDField: "id",
				AttributeFields:  map[string]string{"team": "team"},
			},
			Selector: PolicySelector{Matchers: []string{"team=billing"}},
		},
	}
	_, err := v.ValidateCreate(context.Background(), es)
	if !errors.Is(err, deny) {
		t.Fatalf("expected denial from tenant checker, got %v", err)
	}
}

func TestExternalAttributeSyncValidator_CheckerCalledOnUpdate(t *testing.T) {
	c := &fakeChecker{}
	v := &externalAttributeSyncValidator{checker: c}

	old := &ExternalAttributeSync{
		ObjectMeta: metav1.ObjectMeta{Name: "es", Namespace: teamBillingNS},
		Spec: ExternalAttributeSyncSpec{
			Schedule: "5m",
			Source: ExternalSource{
				Kind: ExternalSourceKindHTTP,
				HTTP: &HTTPSourceSpec{URL: "https://example.com/cmdb", Method: "GET"},
			},
			Mapping: AttributeMapping{
				CollectorIDField: "id",
				AttributeFields:  map[string]string{"team": "team"},
			},
			Selector: PolicySelector{Matchers: []string{"team=billing"}},
		},
	}
	updated := old.DeepCopy()
	updated.Spec.Selector.Matchers = []string{"team=billing", "env=prod"}

	if _, err := v.ValidateUpdate(context.Background(), old, updated); err != nil {
		t.Fatalf("update should pass with checker returning nil, got %v", err)
	}
	if !c.called {
		t.Fatalf("checker should have been called on update")
	}
	if c.calledNs != teamBillingNS {
		t.Errorf("expected namespace team-billing, got %q", c.calledNs)
	}
	want := []string{"team=billing", "env=prod"}
	if !reflect.DeepEqual(c.calledMatchers, want) {
		t.Errorf("update matchers = %v, want %v", c.calledMatchers, want)
	}
}

// --- C4 collectorIDs guard tests --------------------------------------
//
// These pin the C4 behavior: a user under a TenantPolicy may not bypass
// matcher-based scope by listing collectors directly under
// spec.selector.collectorIDs. The guard runs only when the matcher Check
// passed AND a TenantPolicy actually applied to the user.
//
// TODO(v2): close collectorIDs bypass gap end-to-end once
// MatcherChecker.Check sees the full selector (matchers + collectorIDs)
// and reasons over both — these tests then become defense-in-depth.

func TestRemoteAttributePolicyValidator_CollectorIDsRejectedUnderTenantPolicy(t *testing.T) {
	c := &fakeChecker{matches: true}
	v := &remoteAttributePolicyValidator{checker: c}

	p := &RemoteAttributePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: teamBillingNS},
		Spec: RemoteAttributePolicySpec{
			Attributes: map[string]string{"k": "v"},
			Selector: PolicySelector{
				Matchers:     []string{"team=billing"},
				CollectorIDs: []string{"col-1", "col-2"},
			},
		},
	}
	_, err := v.ValidateCreate(context.Background(), p)
	if err == nil {
		t.Fatalf("expected rejection when user under TenantPolicy uses collectorIDs")
	}

	var fieldErr *field.Error
	if !errors.As(err, &fieldErr) {
		t.Fatalf("expected returned error to wrap a *field.Error, got %T: %v", err, err)
	}
	if fieldErr.Type != field.ErrorTypeForbidden {
		t.Errorf("expected field.ErrorTypeForbidden, got %v", fieldErr.Type)
	}
	if got, want := fieldErr.Field, "spec.selector.collectorIDs"; got != want {
		t.Errorf("expected field path %q, got %q", want, got)
	}
	if !strings.Contains(err.Error(), "TenantPolicy") {
		t.Errorf("error message should mention TenantPolicy, got %q", err.Error())
	}
}

func TestRemoteAttributePolicyValidator_CollectorIDsAllowedWithoutTenantPolicy(t *testing.T) {
	// matches=false simulates "no policy applies" — the default-allow case.
	// CollectorIDs must not be rejected when the user is not tenanted.
	c := &fakeChecker{matches: false}
	v := &remoteAttributePolicyValidator{checker: c}

	p := &RemoteAttributePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"},
		Spec: RemoteAttributePolicySpec{
			Attributes: map[string]string{"k": "v"},
			Selector: PolicySelector{
				CollectorIDs: []string{"col-1"},
			},
		},
	}
	if _, err := v.ValidateCreate(context.Background(), p); err != nil {
		t.Fatalf("collectorIDs should be allowed when no TenantPolicy applies, got %v", err)
	}
	if !c.matchesCalled {
		t.Errorf("Matches should have been consulted because collectorIDs is non-empty")
	}
}

func TestRemoteAttributePolicyValidator_CollectorIDsGuardSkipsWhenMatchersOnly(t *testing.T) {
	// matches=true would normally trigger the guard, but with no
	// CollectorIDs there is nothing to gate — Matches must not even be
	// consulted (it's a wasted apiserver round-trip).
	c := &fakeChecker{matches: true}
	v := &remoteAttributePolicyValidator{checker: c}

	p := &RemoteAttributePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: teamBillingNS},
		Spec: RemoteAttributePolicySpec{
			Attributes: map[string]string{"k": "v"},
			Selector:   PolicySelector{Matchers: []string{"team=billing"}},
		},
	}
	if _, err := v.ValidateCreate(context.Background(), p); err != nil {
		t.Fatalf("matchers-only selector should pass, got %v", err)
	}
	if c.matchesCalled {
		t.Errorf("Matches should NOT have been consulted when CollectorIDs is empty")
	}
}

func TestExternalAttributeSyncValidator_CollectorIDsRejectedUnderTenantPolicy(t *testing.T) {
	c := &fakeChecker{matches: true}
	v := &externalAttributeSyncValidator{checker: c}

	es := &ExternalAttributeSync{
		ObjectMeta: metav1.ObjectMeta{Name: "es", Namespace: teamBillingNS},
		Spec: ExternalAttributeSyncSpec{
			Schedule: "5m",
			Source: ExternalSource{
				Kind: ExternalSourceKindHTTP,
				HTTP: &HTTPSourceSpec{URL: "https://example.com/cmdb", Method: "GET"},
			},
			Mapping: AttributeMapping{
				CollectorIDField: "id",
				AttributeFields:  map[string]string{"team": "team"},
			},
			Selector: PolicySelector{
				Matchers:     []string{"team=billing"},
				CollectorIDs: []string{"col-1"},
			},
		},
	}
	_, err := v.ValidateCreate(context.Background(), es)
	if err == nil {
		t.Fatalf("expected rejection when user under TenantPolicy uses collectorIDs")
	}

	var fieldErr *field.Error
	if !errors.As(err, &fieldErr) {
		t.Fatalf("expected returned error to wrap a *field.Error, got %T: %v", err, err)
	}
	if fieldErr.Type != field.ErrorTypeForbidden {
		t.Errorf("expected field.ErrorTypeForbidden, got %v", fieldErr.Type)
	}
	if got, want := fieldErr.Field, "spec.selector.collectorIDs"; got != want {
		t.Errorf("expected field path %q, got %q", want, got)
	}
}

func TestExternalAttributeSyncValidator_CollectorIDsAllowedWithoutTenantPolicy(t *testing.T) {
	c := &fakeChecker{matches: false}
	v := &externalAttributeSyncValidator{checker: c}

	es := &ExternalAttributeSync{
		ObjectMeta: metav1.ObjectMeta{Name: "es", Namespace: "default"},
		Spec: ExternalAttributeSyncSpec{
			Schedule: "5m",
			Source: ExternalSource{
				Kind: ExternalSourceKindHTTP,
				HTTP: &HTTPSourceSpec{URL: "https://example.com/cmdb", Method: "GET"},
			},
			Mapping: AttributeMapping{
				CollectorIDField: "id",
				AttributeFields:  map[string]string{"team": "team"},
			},
			Selector: PolicySelector{CollectorIDs: []string{"col-1"}},
		},
	}
	if _, err := v.ValidateCreate(context.Background(), es); err != nil {
		t.Fatalf("collectorIDs should be allowed when no TenantPolicy applies, got %v", err)
	}
}

func TestRunTenantChecks_NilCheckerIsNoOp(t *testing.T) {
	if err := runTenantChecks(context.Background(), nil, "ns", []string{"team=billing"}, []string{"col-1"}); err != nil {
		t.Fatalf("nil checker should make runTenantChecks a no-op, got %v", err)
	}
}

func TestRunTenantChecks_PropagatesMatchesError(t *testing.T) {
	// If Matches() fails, runTenantChecks must propagate the error rather
	// than silently allowing the request — defense in depth against an
	// apiserver hiccup masking the guard.
	matchesErr := errors.New("apiserver: timeout listing TenantPolicy")
	c := &fakeChecker{matches: false, matchesErr: matchesErr}
	err := runTenantChecks(context.Background(), c, "ns", []string{"team=billing"}, []string{"col-1"})
	if !errors.Is(err, matchesErr) {
		t.Fatalf("expected Matches error to propagate, got %v", err)
	}
}
