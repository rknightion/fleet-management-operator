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

// Package netguard centralizes the destination denylist used to defend the
// ExternalAttributeSync HTTP source against SSRF. A single implementation is
// shared by two enforcement points:
//
//  1. The admission webhook (api/v1alpha1) calls ValidateHostname so a
//     forbidden destination is rejected at create/update time.
//  2. The HTTP source dialer (pkg/sources/http) wires GuardedDialContext so
//     EVERY dial — including DNS-rebinding resolutions and redirect targets
//     that bypass admission — is re-checked against the post-resolution IP.
//
// The dialer guard is unconditional. There is intentionally no allowlist or
// feature flag: the operator never has a legitimate reason to fetch an
// external source from a loopback, private, or link-local address (the cloud
// metadata endpoint 169.254.169.254 is link-local).
package netguard

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"syscall"
)

// ErrDisallowedDestination is the sentinel wrapped by ValidateHostname and the
// guarded dialer so callers can translate it into their own user-facing error
// text via errors.Is / %w while still surfacing the specific host.
var ErrDisallowedDestination = fmt.Errorf("destination is not allowed")

// IsDisallowedIP reports whether addr is a destination the operator must never
// dial. It returns true for loopback, private (RFC1918 / ULA), link-local
// unicast (covers 169.254.169.254 cloud metadata and fe80::/10), link-local
// multicast, and the unspecified address (0.0.0.0 / ::).
//
// IPv4-mapped IPv6 addresses (::ffff:a.b.c.d) are unmapped before
// classification so an attacker cannot smuggle a private IPv4 destination
// through the v6 form.
func IsDisallowedIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsUnspecified()
}

// ValidateHostname checks a URL host component (hostname only, no port) against
// the denylist. A trailing dot is stripped and the host is lowercased before
// matching. The following are rejected:
//
//   - the bare name "localhost" and any "*.localhost" subdomain
//   - any "*.local" name (mDNS / Bonjour)
//   - any "*.svc" or "*.cluster.local" name (in-cluster Kubernetes services)
//   - any host that parses as an IP literal and is disallowed per IsDisallowedIP
//
// A nil error means the host is permitted at admission time. It does NOT
// guarantee the host resolves to a public IP at fetch time — that TOCTOU gap
// is closed by GuardedDialContext, which re-checks the resolved address on
// every dial.
func ValidateHostname(host string) error {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return fmt.Errorf("%w: host is empty", ErrDisallowedDestination)
	}

	// IP literals: classify directly. A bracketed IPv6 literal ("[::1]") may
	// arrive here when callers pass a raw host; strip the brackets so
	// netip.ParseAddr accepts it.
	literal := strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if addr, err := netip.ParseAddr(literal); err == nil {
		if IsDisallowedIP(addr) {
			return fmt.Errorf("%w: %q resolves to a disallowed IP", ErrDisallowedDestination, host)
		}
		return nil
	}

	switch {
	case host == "localhost",
		strings.HasSuffix(host, ".localhost"),
		strings.HasSuffix(host, ".local"),
		strings.HasSuffix(host, ".svc"),
		strings.HasSuffix(host, ".cluster.local"):
		return fmt.Errorf("%w: %q is an internal name", ErrDisallowedDestination, host)
	default:
		return nil
	}
}

// GuardedDialContext returns a DialContext function that refuses to connect to
// any disallowed IP. It installs a Control hook on base that fires after name
// resolution, for every dial, with the concrete post-resolution "ip:port"
// address. This single hook closes both SSRF holes that admission cannot:
//
//   - DNS rebinding: a hostname that resolved to a public IP at admission can
//     resolve to 169.254.169.254 or an RFC1918 address at fetch time.
//   - Redirects: a permitted public URL can 3xx-redirect to an internal
//     address; that redirect target is dialed through this same hook.
//
// base is mutated (its Control field is set) and reused; callers should pass a
// freshly constructed *net.Dialer carrying the desired Timeout/KeepAlive.
func GuardedDialContext(base *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	base.Control = func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			// Control always receives a resolved ip:port; a parse failure
			// means an address we cannot classify, so refuse it.
			return fmt.Errorf("%w: cannot parse dial address %q: %v", ErrDisallowedDestination, address, err)
		}
		addr, err := netip.ParseAddr(host)
		if err != nil {
			return fmt.Errorf("%w: dial address %q is not an IP literal: %v", ErrDisallowedDestination, host, err)
		}
		if IsDisallowedIP(addr) {
			return fmt.Errorf("%w: refusing to dial %q", ErrDisallowedDestination, address)
		}
		return nil
	}
	return base.DialContext
}
