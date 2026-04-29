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

package httpsource

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grafana/fleet-management-operator/pkg/sources"
)

// newSource is a small helper that builds a Source pointed at the given
// httptest server, applying any caller-supplied config tweaks. Centralizing
// construction keeps the per-test setup short.
func newSource(t *testing.T, srv *httptest.Server, mut func(*Config)) *Source {
	t.Helper()
	cfg := Config{URL: srv.URL}
	if mut != nil {
		mut(&cfg)
	}
	src, err := New(cfg)
	require.NoError(t, err)
	src.httpClient = srv.Client()
	return src
}

// TestHTTPSource_RootArray verifies that a response whose body is a
// top-level JSON array maps cleanly into one Record per element with the
// keys preserved.
func TestHTTPSource_RootArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Accept"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"a":1,"b":"x"},{"a":2,"b":"y"}]`)
	}))
	defer srv.Close()

	src := newSource(t, srv, nil)

	records, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 2)

	// Coerce via FieldString so we exercise the public coercion path the
	// controller will use.
	v0, ok := sources.FieldString(records[0], "a")
	require.True(t, ok)
	assert.Equal(t, "1", v0)
	v1, ok := sources.FieldString(records[1], "b")
	require.True(t, ok)
	assert.Equal(t, "y", v1)

	assert.Equal(t, "HTTP", src.Kind())
}

// TestHTTPSource_RecordsPath verifies that a nested array is correctly
// extracted via the dotted recordsPath spec.
func TestHTTPSource_RecordsPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"items":[{"id":"a"},{"id":"b"}]}}`)
	}))
	defer srv.Close()

	src := newSource(t, srv, func(c *Config) { c.RecordsPath = "data.items" })

	records, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 2)
	assert.Equal(t, "a", records[0]["id"])
	assert.Equal(t, "b", records[1]["id"])
}

// TestHTTPSource_Non2xx asserts that non-2xx responses surface the status
// code and a body excerpt in the error message.
func TestHTTPSource_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "internal boom: database not reachable")
	}))
	defer srv.Close()

	src := newSource(t, srv, nil)

	records, err := src.Fetch(context.Background())
	require.Error(t, err)
	assert.Nil(t, records)
	assert.Contains(t, err.Error(), "500")
	assert.Contains(t, err.Error(), "internal boom")
}

// TestHTTPSource_BearerAuth confirms that a configured bearer token is
// forwarded as an Authorization header.
func TestHTTPSource_BearerAuth(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer abc", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}))
	defer srv.Close()

	src := newSource(t, srv, func(c *Config) { c.BearerToken = "abc" })

	records, err := src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Empty(t, records)
}

// TestHTTPSource_BasicAuth confirms that username+password fall back to
// HTTP Basic auth when no bearer token is set.
func TestHTTPSource_BasicAuth(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "alice", user)
		assert.Equal(t, "s3cret", pass)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}))
	defer srv.Close()

	src := newSource(t, srv, func(c *Config) {
		c.Username = "alice"
		c.Password = "s3cret"
	})

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)
}

// TestHTTPSource_BearerOverridesBasic ensures the bearer-token branch wins
// when both forms of credentials are configured (it's the documented
// precedence and a frequent source of operator confusion).
func TestHTTPSource_BearerOverridesBasic(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		assert.False(t,
			strings.HasPrefix(r.Header.Get("Authorization"), "Basic "),
			"basic auth must not be sent when bearer token is set")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}))
	defer srv.Close()

	src := newSource(t, srv, func(c *Config) {
		c.BearerToken = "tok"
		c.Username = "ignored"
		c.Password = "ignored"
	})

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)
}

// TestHTTPSource_NonObjectsSkipped checks that scalar/array elements
// inside the records array are silently dropped, leaving only the object
// rows for the controller to consider.
func TestHTTPSource_NonObjectsSkipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[1,"two",{"valid":true},null,[1,2]]`)
	}))
	defer srv.Close()

	src := newSource(t, srv, nil)

	records, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, true, records[0]["valid"])
}

// TestHTTPSource_InvalidURL verifies New rejects malformed/incomplete URLs
// up front rather than failing later inside Fetch.
func TestHTTPSource_InvalidURL(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{name: "empty", url: ""},
		{name: "scheme-only", url: "http://"},
		{name: "no-scheme", url: "example.com"},
		{name: "ftp-scheme", url: "ftp://example.com"},
		{name: "control-character", url: "http://example.com/\x7f"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(Config{URL: tc.url})
			assert.Error(t, err, "expected New to reject %q", tc.url)
		})
	}
}

func TestHTTPSource_RejectsPlaintextCredentials(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{
			name: "bearer token",
			cfg:  Config{URL: "http://example.com", BearerToken: "abc"},
		},
		{
			name: "basic auth",
			cfg:  Config{URL: "http://example.com", Username: "alice", Password: "secret"},
		},
		{
			name: "url userinfo",
			cfg:  Config{URL: "http://alice:secret@example.com"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "https")
		})
	}
}

// TestHTTPSource_RecordsPathMissingKey confirms that the missing key name
// shows up in the error so operators can immediately see which path step
// is wrong.
func TestHTTPSource_RecordsPathMissingKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"results":{}}}`)
	}))
	defer srv.Close()

	src := newSource(t, srv, func(c *Config) { c.RecordsPath = "data.results.items" })

	_, err := src.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "items")
	assert.Contains(t, err.Error(), "data.results.items")
}

// TestHTTPSource_RecordsPathTerminalNotArray exercises the "found the key
// but it's the wrong shape" branch, which is the second-most-common spec
// error after a missing key.
func TestHTTPSource_RecordsPathTerminalNotArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"items":"not-an-array"}}`)
	}))
	defer srv.Close()

	src := newSource(t, srv, func(c *Config) { c.RecordsPath = "data.items" })

	_, err := src.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "data.items")
	assert.Contains(t, err.Error(), "string")
}

// TestHTTPSource_RootArrayWrongShape exercises the empty-recordsPath
// branch when the response root is an object instead of an array — the
// error must hint at setting recordsPath.
func TestHTTPSource_RootArrayWrongShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[]}`)
	}))
	defer srv.Close()

	src := newSource(t, srv, nil)
	_, err := src.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recordsPath")
}

// TestHTTPSource_BodyExcerptLimit verifies that the error path truncates
// large bodies; without this guard a 5MB error page would land in the
// controller log line by line.
func TestHTTPSource_BodyExcerptLimit(t *testing.T) {
	const bodySize = 5 * 1024 * 1024 // 5MB
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		// Stream a 5MB body of 'X' bytes — io.LimitReader on the source
		// side is the safety net.
		buf := make([]byte, 1024)
		for i := range buf {
			buf[i] = 'X'
		}
		written := 0
		for written < bodySize {
			n, err := w.Write(buf)
			if err != nil {
				return
			}
			written += n
		}
	}))
	defer srv.Close()

	src := newSource(t, srv, nil)

	_, err := src.Fetch(context.Background())
	require.Error(t, err)
	// Find the excerpt portion of the error and assert its length.
	const marker = "status 400: "
	idx := strings.Index(err.Error(), marker)
	require.GreaterOrEqual(t, idx, 0, "error message must include status marker")
	excerpt := err.Error()[idx+len(marker):]
	assert.LessOrEqual(t, len(excerpt), bodyExcerptLimit,
		"excerpt must be capped at %d bytes, got %d", bodyExcerptLimit, len(excerpt))
}

func TestHTTPSource_SuccessBodyLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("["))
		_, _ = io.Copy(w, strings.NewReader(strings.Repeat(" ", int(successfulBodyLimit)+1)))
	}))
	defer srv.Close()

	src := newSource(t, srv, nil)

	records, err := src.Fetch(context.Background())
	require.Error(t, err)
	assert.Nil(t, records)
	assert.Contains(t, err.Error(), "response body exceeds")
}

// TestHTTPSource_ContextCancellation ensures Fetch propagates ctx
// cancellation rather than blocking on the in-flight request.
func TestHTTPSource_ContextCancellation(t *testing.T) {
	released := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until the test releases us OR the client disconnects.
		select {
		case <-released:
		case <-r.Context().Done():
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}))
	defer srv.Close()
	defer close(released)

	src := newSource(t, srv, nil)

	ctx, cancel := context.WithCancel(context.Background())

	var (
		fetchErr error
		wg       sync.WaitGroup
	)
	wg.Go(func() {
		_, fetchErr = src.Fetch(ctx)
	})

	// Give the server enough time to enter the handler before we cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()

	require.Error(t, fetchErr)
	assert.True(t,
		strings.Contains(fetchErr.Error(), context.Canceled.Error()) ||
			strings.Contains(fetchErr.Error(), "context canceled"),
		"expected context cancellation in error, got %q", fetchErr.Error())
}

// TestHTTPSource_PostMethod verifies the method override is respected end
// to end.
func TestHTTPSource_PostMethod(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}))
	defer srv.Close()

	src := newSource(t, srv, func(c *Config) { c.Method = "post" })

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)
}

// TestHTTPSource_StaticHeadersForwarded confirms that Headers entries are
// included in the request, while Accept is always normalized to JSON.
func TestHTTPSource_StaticHeadersForwarded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "value-1", r.Header.Get("X-Custom-One"))
		assert.Equal(t, "value-2", r.Header.Get("X-Custom-Two"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}))
	defer srv.Close()

	src := newSource(t, srv, func(c *Config) {
		c.Headers = map[string]string{
			"X-Custom-One": "value-1",
			"X-Custom-Two": "value-2",
			// Caller-supplied Accept must be overridden to JSON to keep
			// the source contract honest.
			"Accept": "text/plain",
		}
	})

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)
}

// TestHTTPSource_NewDefaults locks in the documented defaults for Method
// and Timeout so a future "let's tune the timeout" change can't silently
// regress the contract.
func TestHTTPSource_NewDefaults(t *testing.T) {
	src, err := New(Config{URL: "http://example.com"})
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, src.cfg.Method)
	assert.Equal(t, defaultTimeout, src.cfg.Timeout)
	assert.Equal(t, defaultTimeout, src.httpClient.Timeout)
}

// TestHTTPSource_EmptyArray exercises the documented "empty results are
// valid" path so the controller's empty-result safety guard is the only
// place that decision is made.
func TestHTTPSource_EmptyArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}))
	defer srv.Close()

	src := newSource(t, srv, nil)

	records, err := src.Fetch(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, records, "should return non-nil empty slice")
	assert.Empty(t, records)
}

// Sanity: ensure that a wildly bogus URL produces a clean error from
// Fetch (not New) when the parser accepts it but DNS/connect fails. This
// keeps the error message format honest for the controller's events.
func TestHTTPSource_FetchNetworkError(t *testing.T) {
	src, err := New(Config{URL: "http://127.0.0.1:1"}) // port 1 is reserved
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err = src.Fetch(ctx)
	require.Error(t, err)
	// Either a connect refused or context deadline — both are fine, we
	// just need a clear "GET <url>:" prefix.
	assert.True(t,
		strings.Contains(err.Error(), "GET http://127.0.0.1:1"),
		"expected method+URL prefix in error, got %q", err.Error())
}

// TestHTTPSource_FetchErrorRedactsURLCredentials guards S2: if a URL is
// configured with userinfo (basic auth in the URL itself, which is
// supported by url.Parse), error messages MUST NOT echo the bare
// password. (*url.URL).Redacted replaces it with "xxxxx", and the source
// must use that form in every Fetch error path so the controller's
// kubectl-visible events do not leak credentials.
func TestHTTPSource_FetchErrorRedactsURLCredentials(t *testing.T) {
	t.Run("network-error", func(t *testing.T) {
		// Port 1 is reserved — connect refused / timeout produces the
		// "do request" branch that previously embedded cfg.URL verbatim.
		src, err := New(Config{URL: "https://user:s3cret-token@127.0.0.1:1/path"})
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		_, err = src.Fetch(ctx)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "s3cret-token",
			"password from URL must not appear in error message")
		assert.Contains(t, err.Error(), "xxxxx",
			"redacted password marker must appear in error message")
		// Host should still be there so operators can identify the target.
		assert.Contains(t, err.Error(), "127.0.0.1:1")
	})

	t.Run("non-2xx-error", func(t *testing.T) {
		// Build a server, then synthesize a Source with redactedURL set
		// to point at it but cfg.URL carrying a credential. We can't use
		// httptest.NewServer + url-with-userinfo directly because Go's
		// http.Client strips userinfo before sending — so we build the
		// Source via New on a credentialed URL pointing at the test
		// server.
		var hits int
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits++
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, "server boom")
		}))
		defer srv.Close()

		// Inject userinfo into the test server URL so the redacted form
		// is genuinely different from the raw form.
		credentialed := strings.Replace(srv.URL, "https://", "https://user:s3cret-token@", 1)
		src, err := New(Config{URL: credentialed})
		require.NoError(t, err)
		src.httpClient = srv.Client()

		_, err = src.Fetch(context.Background())
		require.Error(t, err)
		assert.GreaterOrEqual(t, hits, 1, "request must reach the server")
		assert.NotContains(t, err.Error(), "s3cret-token",
			"password from URL must not appear in non-2xx error")
		assert.Contains(t, err.Error(), "xxxxx",
			"redacted password marker must appear in non-2xx error")
		assert.Contains(t, err.Error(), "status 500")
	})
}

// TestHTTPSource_Close confirms Close is callable and returns nil even on
// a Source whose httpClient never made a request. The interface contract
// requires Close to be a no-op-safe operation so the controller's
// defer-Close pattern works on every reconcile path.
func TestHTTPSource_Close(t *testing.T) {
	src, err := New(Config{URL: "http://example.com/"})
	require.NoError(t, err)
	assert.NoError(t, src.Close())
	// Idempotent: a second Close must also succeed.
	assert.NoError(t, src.Close())
}

// Compile-time verification: ensure assert/require imports are not
// orphaned if a future edit deletes the only test that uses them.
var _ = fmt.Sprintf
