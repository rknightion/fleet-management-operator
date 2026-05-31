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

package netguard

import (
	"errors"
	"net"
	"net/netip"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsDisallowedIP(t *testing.T) {
	cases := []struct {
		name string
		addr string
		want bool
	}{
		// Loopback.
		{name: "v4 loopback 127.0.0.1", addr: "127.0.0.1", want: true},
		{name: "v4 loopback 127.0.0.53", addr: "127.0.0.53", want: true},
		{name: "v6 loopback ::1", addr: "::1", want: true},

		// Unspecified.
		{name: "v4 unspecified 0.0.0.0", addr: "0.0.0.0", want: true},
		{name: "v6 unspecified ::", addr: "::", want: true},

		// RFC1918 private.
		{name: "rfc1918 10/8", addr: "10.0.0.10", want: true},
		{name: "rfc1918 172.16/12", addr: "172.16.5.5", want: true},
		{name: "rfc1918 192.168/16", addr: "192.168.1.1", want: true},
		{name: "v6 ULA fc00::/7", addr: "fd00::1", want: true},

		// Link-local unicast — includes the cloud metadata endpoint.
		{name: "link-local 169.254.0.1", addr: "169.254.0.1", want: true},
		{name: "cloud metadata 169.254.169.254", addr: "169.254.169.254", want: true},
		{name: "v6 link-local fe80::", addr: "fe80::1", want: true},

		// Link-local multicast.
		{name: "v4 link-local multicast 224.0.0.1", addr: "224.0.0.1", want: true},
		{name: "v6 link-local multicast ff02::1", addr: "ff02::1", want: true},

		// IPv4-mapped IPv6 must be unmapped before classifying.
		{name: "v4-mapped loopback ::ffff:127.0.0.1", addr: "::ffff:127.0.0.1", want: true},
		{name: "v4-mapped metadata ::ffff:169.254.169.254", addr: "::ffff:169.254.169.254", want: true},
		{name: "v4-mapped private ::ffff:10.0.0.1", addr: "::ffff:10.0.0.1", want: true},
		{name: "v4-mapped public ::ffff:8.8.8.8", addr: "::ffff:8.8.8.8", want: false},

		// Public addresses must be allowed.
		{name: "public v4 8.8.8.8", addr: "8.8.8.8", want: false},
		{name: "public v4 1.1.1.1", addr: "1.1.1.1", want: false},
		{name: "public v6 2606:4700::1111", addr: "2606:4700::1111", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr, err := netip.ParseAddr(tc.addr)
			require.NoError(t, err)
			assert.Equal(t, tc.want, IsDisallowedIP(addr))
		})
	}
}

func TestValidateHostname(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		wantErr bool
	}{
		// Allowed public hostnames.
		{name: "public hostname", host: "api.example.com", wantErr: false},
		{name: "public hostname trailing dot", host: "api.example.com.", wantErr: false},
		{name: "public hostname uppercase", host: "API.Example.COM", wantErr: false},
		{name: "public ip literal", host: "8.8.8.8", wantErr: false},
		{name: "public v6 literal bracketed", host: "[2606:4700::1111]", wantErr: false},

		// Internal-name suffix rules.
		{name: "localhost", host: "localhost", wantErr: true},
		{name: "localhost trailing dot", host: "localhost.", wantErr: true},
		{name: "sub.localhost", host: "service.localhost", wantErr: true},
		{name: "dot-local mDNS", host: "printer.local", wantErr: true},
		{name: "dot-svc cluster", host: "cmdb.default.svc", wantErr: true},
		{name: "dot-cluster-local", host: "cmdb.default.svc.cluster.local", wantErr: true},

		// IP literals that are disallowed.
		{name: "loopback literal", host: "127.0.0.1", wantErr: true},
		{name: "private literal", host: "10.0.0.10", wantErr: true},
		{name: "link-local metadata literal", host: "169.254.169.254", wantErr: true},
		{name: "v6 loopback literal bracketed", host: "[::1]", wantErr: true},
		{name: "v4-mapped private literal", host: "::ffff:10.0.0.1", wantErr: true},

		// Empty.
		{name: "empty host", host: "", wantErr: true},
		{name: "whitespace host", host: "   ", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateHostname(tc.host)
			if tc.wantErr {
				require.Error(t, err)
				assert.True(t, errors.Is(err, ErrDisallowedDestination),
					"error should wrap ErrDisallowedDestination, got %v", err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestGuardedDialContext_ControlRefusesDisallowed exercises the Control hook
// directly: it must reject post-resolution ip:port targets that classify as
// disallowed, and accept public ones. Driving Control by hand avoids opening
// real sockets and keeps the test hermetic.
func TestGuardedDialContext_ControlRefusesDisallowed(t *testing.T) {
	base := &net.Dialer{}
	// GuardedDialContext installs base.Control as a side effect.
	dial := GuardedDialContext(base)
	require.NotNil(t, dial)
	require.NotNil(t, base.Control, "GuardedDialContext must install a Control hook")

	cases := []struct {
		name    string
		address string
		wantErr bool
	}{
		{name: "loopback refused", address: "127.0.0.1:80", wantErr: true},
		{name: "metadata refused", address: "169.254.169.254:80", wantErr: true},
		{name: "private refused", address: "10.0.0.5:443", wantErr: true},
		{name: "v6 loopback refused", address: "[::1]:80", wantErr: true},
		{name: "v4-mapped private refused", address: "[::ffff:10.0.0.1]:80", wantErr: true},
		{name: "public allowed", address: "8.8.8.8:443", wantErr: false},
		{name: "public v6 allowed", address: "[2606:4700::1111]:443", wantErr: false},
		{name: "unparseable refused", address: "not-an-address", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := base.Control("tcp", tc.address, syscall.RawConn(nil))
			if tc.wantErr {
				require.Error(t, err)
				assert.True(t, errors.Is(err, ErrDisallowedDestination),
					"error should wrap ErrDisallowedDestination, got %v", err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
