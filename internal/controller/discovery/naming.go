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

// Package discovery contains helpers for the CollectorDiscovery
// controller — primarily the name-sanitizer that converts arbitrary
// Fleet Management collector IDs into DNS-1123-safe Kubernetes resource
// names.
package discovery

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// dns1123MaxLabelLen is the Kubernetes DNS-1123 label-name limit. We
// never produce a name longer than this; the truncated base plus the
// hash suffix always fits.
const dns1123MaxLabelLen = 63

// sanitizedBaseMaxLen is the longest sanitized base we keep before the
// optional "-<hash>" suffix. 53 chars + "-" + 5-char hash = 59 chars,
// safely under dns1123MaxLabelLen.
const sanitizedBaseMaxLen = 53

// hashSuffixLen is the length of the hex hash appended on collision /
// lossy sanitization. 5 hex chars = 20 bits ≈ 1-in-1M collision odds
// per pair, plenty for fleet sizes the operator targets.
const hashSuffixLen = 5

// fallbackBase is used when sanitization produces an empty string
// (e.g., id was all special characters). Combined with the hash suffix
// it yields a valid DNS-1123 name like "c-a1b2c".
const fallbackBase = "c"

// SanitizedName transforms id into a DNS-1123-safe label form: lower
// case alphanumerics and hyphens, no leading/trailing hyphen, no
// adjacent hyphens, max sanitizedBaseMaxLen characters.
//
// lossy is true whenever the transformation altered the input at all
// — the caller should use HashedName in that case so two distinct ids
// that sanitize to the same form get distinct CR names. Empty output
// is always lossy.
//
// The lossy signal is conservative: it does not prove a collision will
// occur, only that the transformation isn't reversible. The controller
// pairs lossy with an existing-CR check before deciding to hash-suffix.
func SanitizedName(id string) (name string, lossy bool) {
	lower := strings.ToLower(id)

	var b strings.Builder
	b.Grow(len(lower))
	prevDash := false
	for _, r := range lower {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")

	if len(out) > sanitizedBaseMaxLen {
		out = strings.TrimRight(out[:sanitizedBaseMaxLen], "-")
	}

	lossy = out != id || out == ""
	return out, lossy
}

// HashedName returns SanitizedName(id) plus a deterministic 5-char hex
// suffix derived from sha256(id). When the sanitized base is empty
// (all-special-character id), the constant fallbackBase is used so the
// output is always a valid DNS-1123 label.
//
// HashedName is deterministic for a given id, so repeated reconciles
// produce the same name and the controller's existence check is stable.
func HashedName(id string) string {
	base, _ := SanitizedName(id)
	if base == "" {
		base = fallbackBase
	}
	sum := sha256.Sum256([]byte(id))
	return base + "-" + hex.EncodeToString(sum[:])[:hashSuffixLen]
}

// IsValidDNS1123 returns true if name fits the DNS-1123 label rules
// the controller relies on (alphanumeric and hyphens only, starts and
// ends with alphanumeric, max dns1123MaxLabelLen chars). Used as a
// final defensive check before creating a CR — both SanitizedName and
// HashedName produce valid output, but a defensive check costs nothing
// and protects against future helper changes.
func IsValidDNS1123(name string) bool {
	if name == "" || len(name) > dns1123MaxLabelLen {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			// ok
		case r == '-':
			if i == 0 || i == len(name)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
