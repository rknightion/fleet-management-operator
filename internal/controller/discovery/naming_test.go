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

package discovery

import (
	"strings"
	"testing"
)

func TestSanitizedName(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		wantName  string
		wantLossy bool
	}{
		{
			name:      "pure DNS-1123 id is unchanged",
			id:        "edge-host-42",
			wantName:  "edge-host-42",
			wantLossy: false,
		},
		{
			name:      "uppercase is lowercased and lossy",
			id:        "Edge-Host-42",
			wantName:  "edge-host-42",
			wantLossy: true,
		},
		{
			name:      "dots become hyphens",
			id:        "host.example.com",
			wantName:  "host-example-com",
			wantLossy: true,
		},
		{
			name:      "underscores become hyphens",
			id:        "edge_host_42",
			wantName:  "edge-host-42",
			wantLossy: true,
		},
		{
			name:      "slashes become hyphens",
			id:        "team/edge/42",
			wantName:  "team-edge-42",
			wantLossy: true,
		},
		{
			name:      "runs of invalid chars collapse to a single hyphen",
			id:        "host..example",
			wantName:  "host-example",
			wantLossy: true,
		},
		{
			name:      "leading invalid char is trimmed",
			id:        ".edge",
			wantName:  "edge",
			wantLossy: true,
		},
		{
			name:      "trailing invalid char is trimmed",
			id:        "edge.",
			wantName:  "edge",
			wantLossy: true,
		},
		{
			name:      "empty input produces empty result and is lossy",
			id:        "",
			wantName:  "",
			wantLossy: true,
		},
		{
			name:      "all-special-chars input produces empty result and is lossy",
			id:        ".../___",
			wantName:  "",
			wantLossy: true,
		},
		{
			name:      "long id is truncated to sanitizedBaseMaxLen",
			id:        strings.Repeat("a", sanitizedBaseMaxLen+10),
			wantName:  strings.Repeat("a", sanitizedBaseMaxLen),
			wantLossy: true,
		},
		{
			name:      "uuid-style id with dashes only is unchanged",
			id:        "0a1b2c3d-4e5f-6789-abcd-ef0123456789",
			wantName:  "0a1b2c3d-4e5f-6789-abcd-ef0123456789",
			wantLossy: false,
		},
		{
			name:      "trailing hyphen after truncation is trimmed",
			id:        strings.Repeat("a", sanitizedBaseMaxLen-1) + ".bbbb",
			wantName:  strings.Repeat("a", sanitizedBaseMaxLen-1),
			wantLossy: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, lossy := SanitizedName(tt.id)
			if got != tt.wantName {
				t.Errorf("SanitizedName(%q) name = %q, want %q", tt.id, got, tt.wantName)
			}
			if lossy != tt.wantLossy {
				t.Errorf("SanitizedName(%q) lossy = %v, want %v", tt.id, lossy, tt.wantLossy)
			}
		})
	}
}

func TestSanitizedNameOutputAlwaysValid(t *testing.T) {
	// Every output of SanitizedName must either be empty or pass the
	// IsValidDNS1123 check. Empty is the explicit lossy signal — the
	// controller pairs it with HashedName to produce a usable name.
	inputs := []string{
		"",
		"abc",
		"ABC",
		"host.example.com",
		"...",
		"_",
		strings.Repeat("X", 200),
		"--leading-hyphens--",
		"a---b",
		"0",
		"☃",
	}

	for _, id := range inputs {
		got, _ := SanitizedName(id)
		if got != "" && !IsValidDNS1123(got) {
			t.Errorf("SanitizedName(%q) = %q is not a valid DNS-1123 label", id, got)
		}
	}
}

func TestHashedName(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{name: "pure DNS-1123 id", id: "edge-host-42"},
		{name: "uppercase id", id: "Edge-Host"},
		{name: "id with dots", id: "host.example.com"},
		{name: "all-special-chars", id: ".../___"},
		{name: "very long id", id: strings.Repeat("Abc.", 30)},
		{name: "empty", id: ""},
		{name: "unicode", id: "snowman-☃"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HashedName(tt.id)
			if !IsValidDNS1123(got) {
				t.Errorf("HashedName(%q) = %q is not a valid DNS-1123 label", tt.id, got)
			}

			// Determinism: two calls produce the same output.
			got2 := HashedName(tt.id)
			if got != got2 {
				t.Errorf("HashedName(%q) is not deterministic: %q vs %q", tt.id, got, got2)
			}

			// Suffix shape: ends with "-<5 hex chars>".
			if len(got) < hashSuffixLen+1 || got[len(got)-hashSuffixLen-1] != '-' {
				t.Errorf("HashedName(%q) = %q does not end with a 5-char hex suffix", tt.id, got)
			}

			// Empty / all-special id falls back to fallbackBase.
			if (tt.id == "" || tt.id == ".../___") && !strings.HasPrefix(got, fallbackBase+"-") {
				t.Errorf("HashedName(%q) = %q expected to start with %q-", tt.id, got, fallbackBase)
			}
		})
	}
}

func TestHashedNameDistinguishesCollidingSanitizations(t *testing.T) {
	// "Abc" and "abc" both sanitize to "abc". HashedName must produce
	// distinct names so the controller can store both as separate CRs.
	a := HashedName("Abc")
	b := HashedName("abc")
	if a == b {
		t.Errorf("HashedName collided for distinct ids: %q (Abc) == %q (abc)", a, b)
	}

	// "host.example" and "host-example" sanitize identically.
	c := HashedName("host.example")
	d := HashedName("host-example")
	if c == d {
		t.Errorf("HashedName collided for distinct ids: %q (host.example) == %q (host-example)", c, d)
	}
}

func TestIsValidDNS1123(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"empty", "", false},
		{"too long", strings.Repeat("a", dns1123MaxLabelLen+1), false},
		{"max length", strings.Repeat("a", dns1123MaxLabelLen), true},
		{"plain", "abc", true},
		{"with hyphen", "abc-def", true},
		{"leading hyphen", "-abc", false},
		{"trailing hyphen", "abc-", false},
		{"uppercase", "Abc", false},
		{"contains dot", "abc.def", false},
		{"contains underscore", "abc_def", false},
		{"single char", "a", true},
		{"single digit", "0", true},
		{"single hyphen", "-", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidDNS1123(tt.input); got != tt.want {
				t.Errorf("IsValidDNS1123(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
