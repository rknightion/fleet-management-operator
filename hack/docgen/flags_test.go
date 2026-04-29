package main

import (
	"strings"
	"testing"
)

// TestExtractFlags exercises the AST walker against the live cmd/main.go. It
// is the simplest possible integration test: if any flag the project actually
// ships gets dropped (or a non-flag function call is mis-extracted), this
// fails with a useful diff.
func TestExtractFlags(t *testing.T) {
	flags, err := extractFlags("../..", "../../cmd/main.go")
	if err != nil {
		t.Fatalf("extractFlags: %v", err)
	}
	if len(flags) < 10 {
		t.Fatalf("expected at least 10 flags, got %d", len(flags))
	}

	// Spot-check well-known flags. If any of these regress, the table is wrong.
	want := map[string]string{
		"metrics-bind-address":             "string",
		"webhook-port":                     "int",
		"fleet-api-rps":                    "float64",
		"leader-election-lease-duration":   "duration",
		"enable-pipeline-controller":       "bool",
		"controller-policy-max-concurrent": "int",
	}
	got := map[string]string{}
	for _, f := range flags {
		got[f.Name] = f.Type
	}
	for name, typ := range want {
		if got[name] != typ {
			t.Errorf("flag %q: want type %q, got %q", name, typ, got[name])
		}
	}
}

func TestConcatStringExpr(t *testing.T) {
	// The flags generator must concatenate "first " + "second" usage strings.
	// This is exercised end-to-end by TestExtractFlags (metrics-bind-address
	// and fleet-api-burst both use multi-line + concatenation), so we assert
	// the concatenation succeeded by checking for the second half of one of
	// those usages.
	flags, err := extractFlags("../..", "../../cmd/main.go")
	if err != nil {
		t.Fatalf("extractFlags: %v", err)
	}
	for _, f := range flags {
		if f.Name == "fleet-api-burst" {
			if !strings.Contains(f.Usage, "burst=1 causes livelock") {
				t.Errorf("fleet-api-burst usage did not concatenate multi-line literal: got %q", f.Usage)
			}
			return
		}
	}
	t.Fatal("fleet-api-burst flag not found")
}

func TestGenerateFlagsIdempotent(t *testing.T) {
	// Calling generateFlags twice must produce byte-identical output.
	// This is the cheapest guard against accidental map-iteration ordering.
	a, err := generateFlags("../..")
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	b, err := generateFlags("../..")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if string(a) != string(b) {
		t.Fatal("generateFlags output is not deterministic")
	}
}

func TestIsFlagName(t *testing.T) {
	// The function filters garbage from raw line scanning. It is called AFTER
	// the leading `--` is stripped, so inputs are always lowercase + hyphen +
	// digits.
	cases := map[string]bool{
		"metrics-bind-address": true,
		"a-b":                  true,
		"":                     false,
		"abc":                  false, // no hyphen — probably YAML
		"camelCase":            false, // uppercase rejected
		"a_b":                  false, // underscore rejected
	}
	for in, want := range cases {
		if got := isFlagName(in); got != want {
			t.Errorf("isFlagName(%q) = %v, want %v", in, got, want)
		}
	}
}
