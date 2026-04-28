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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// teamBillingNS is the namespace used in this file's table-driven tests
// to assert the validator wrapper passes the right namespace through to
// the tenant checker.
const teamBillingNS = "team-billing"

// fakeChecker records its inputs and returns a configured error. The
// validator wrappers should pass the CR's namespace and the right matcher
// list (Pipeline.Spec.Matchers, Spec.Selector.Matchers, etc.) through to
// the checker.
type fakeChecker struct {
	err            error
	called         bool
	calledNs       string
	calledMatchers []string
}

func (f *fakeChecker) Check(_ context.Context, ns string, matchers []string) error {
	f.called = true
	f.calledNs = ns
	f.calledMatchers = matchers
	return f.err
}

func TestPipelineValidator_NilCheckerSkipsTenantStep(t *testing.T) {
	v := &pipelineValidator{checker: nil}
	p := &Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"},
		Spec: PipelineSpec{
			Contents:   "prometheus.scrape \"x\" {}",
			ConfigType: ConfigTypeAlloy,
			Enabled:    true,
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
			Enabled:    true,
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
			Enabled:    true,
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
			Enabled:    true,
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
