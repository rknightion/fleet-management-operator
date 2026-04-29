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

// Package httpsource implements the sources.Source interface backed by an
// HTTP/JSON endpoint. The package is intentionally decoupled from
// api/v1alpha1 so it stays unit-testable without a Kubernetes client; the
// controller adapts an HTTPSourceSpec into a Config at construction time.
package httpsource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/grafana/fleet-management-operator/pkg/sources"
)

// defaultTimeout is the per-request timeout applied when Config.Timeout is
// zero. Mirrors pkg/fleetclient so operator-wide HTTP behavior stays
// consistent.
const defaultTimeout = 30 * time.Second

// bodyExcerptLimit caps the byte length included in non-2xx error messages
// so a multi-MB error page does not balloon log output.
const bodyExcerptLimit = 200

// successfulBodyLimit caps successful JSON response bodies. External sources
// are customer-controlled network endpoints; bounding the read protects the
// reconciler from large-response memory pressure while preserving the existing
// non-2xx excerpt behavior.
const successfulBodyLimit int64 = 10 * 1024 * 1024

// Config is the typed construction input for an HTTP/JSON Source.
//
// The controller adapts v1alpha1.HTTPSourceSpec plus secret material into a
// Config; keeping this struct decoupled means the package has no
// k8s.io/api/core/v1 dependency and can be exercised directly from tests.
type Config struct {
	// URL is the fully-qualified endpoint to fetch records from. Required.
	URL string

	// Method is the HTTP verb (GET or POST). Empty defaults to GET.
	Method string

	// RecordsPath is a dotted path identifying the array of records inside
	// the response body. Empty means the response root is the array.
	RecordsPath string

	// BearerToken, when non-empty, is sent as Authorization: Bearer <token>.
	// Takes precedence over Username/Password.
	BearerToken string

	// Username/Password, when both non-empty (and BearerToken is empty),
	// are sent as HTTP Basic auth.
	Username string
	Password string

	// Headers is a set of additional static request headers. Authorization
	// and Accept supplied here are overridden by the auth/accept logic.
	Headers map[string]string

	// Timeout is the per-request HTTP client timeout. Zero means use the
	// package default (30s).
	Timeout time.Duration
}

// Source is the HTTP/JSON sources.Source implementation.
type Source struct {
	cfg        Config
	httpClient *http.Client
	// redactedURL is cfg.URL run through (*url.URL).Redacted at construction
	// time. Errors and log messages embed THIS string rather than cfg.URL so
	// userinfo (e.g. https://user:tok@host/path) can never leak into operator
	// events or controller logs. Reusing the parsed-once form avoids
	// re-parsing on every Fetch error.
	redactedURL string
}

// Compile-time interface check.
var _ sources.Source = (*Source)(nil)

// New validates cfg and constructs a Source. The HTTP client is built once
// here and reused across Fetch calls.
func New(cfg Config) (*Source, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, fmt.Errorf("httpsource: URL is required")
	}
	parsed, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("httpsource: invalid URL %q: %w", cfg.URL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("httpsource: URL scheme must be http or https, got %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("httpsource: URL must include a host: %q", cfg.URL)
	}
	if parsed.Scheme != "https" && (configHasCredentials(cfg) || parsed.User != nil) {
		return nil, fmt.Errorf("httpsource: URL scheme must be https when credentials are configured")
	}

	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	switch method {
	case "":
		method = http.MethodGet
	case http.MethodGet, http.MethodPost:
		// allowed
	default:
		return nil, fmt.Errorf("httpsource: unsupported method %q (only GET and POST are allowed)", cfg.Method)
	}
	cfg.Method = method

	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}

	httpClient := &http.Client{
		Timeout: cfg.Timeout,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}

	return &Source{
		cfg:         cfg,
		httpClient:  httpClient,
		redactedURL: parsed.Redacted(),
	}, nil
}

// Close releases any resources the Source holds. The HTTP client uses
// http.DefaultTransport-style connection pooling, but the pool is owned by
// the Transport assigned in New — closing idle connections is the most we
// can do here, and it is best-effort: a Source that never performed a
// Fetch has no idle connections to close. Returning nil keeps the
// interface contract simple.
func (s *Source) Close() error {
	if s.httpClient != nil {
		if t, ok := s.httpClient.Transport.(*http.Transport); ok {
			t.CloseIdleConnections()
		}
	}
	return nil
}

// Kind returns the stable identifier matching v1alpha1.ExternalSourceKindHTTP.
func (s *Source) Kind() string { return "HTTP" }

// Fetch performs a single HTTP request, decodes the JSON body, walks
// RecordsPath, and returns one Record per array object element. Non-object
// elements are skipped silently — the controller's RequiredKeys check is
// the authoritative "is this record usable" gate.
func (s *Source) Fetch(ctx context.Context) ([]sources.Record, error) {
	req, err := http.NewRequestWithContext(ctx, s.cfg.Method, s.cfg.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("httpsource: build request: %w", err)
	}

	// Static headers first so the auth/accept logic below can override.
	for k, v := range s.cfg.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")

	switch {
	case s.cfg.BearerToken != "":
		req.Header.Set("Authorization", "Bearer "+s.cfg.BearerToken)
	case s.cfg.Username != "" && s.cfg.Password != "":
		req.SetBasicAuth(s.cfg.Username, s.cfg.Password)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		// Use redactedURL (not cfg.URL) so userinfo never leaks into
		// the controller's events / logs. (*url.URL).Redacted replaces
		// any password component with "xxxxx".
		return nil, fmt.Errorf("httpsource: %s %s: %w", s.cfg.Method, s.redactedURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt := readBodyExcerpt(resp.Body, bodyExcerptLimit)
		return nil, fmt.Errorf(
			"httpsource: %s %s: status %d: %s",
			s.cfg.Method, s.redactedURL, resp.StatusCode, excerpt,
		)
	}

	body, err := readLimitedBody(resp.Body, successfulBodyLimit)
	if err != nil {
		return nil, err
	}

	// Decode the bounded body as a single any so we can walk RecordsPath
	// uniformly.
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("httpsource: decode JSON: %w", err)
	}

	arr, err := walkRecordsPath(decoded, s.cfg.RecordsPath)
	if err != nil {
		return nil, err
	}

	out := make([]sources.Record, 0, len(arr))
	for _, elem := range arr {
		obj, ok := elem.(map[string]any)
		if !ok {
			// Non-object array entries are skipped; the controller's
			// RequiredKeys check decides usability for object records.
			continue
		}
		out = append(out, sources.Record(obj))
	}
	return out, nil
}

func configHasCredentials(cfg Config) bool {
	return cfg.BearerToken != "" || cfg.Username != "" || cfg.Password != ""
}

func readLimitedBody(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("httpsource: response body limit must be positive")
	}
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("httpsource: read response body: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("httpsource: response body exceeds %d byte limit", limit)
	}
	return body, nil
}

// walkRecordsPath descends into decoded along the dotted recordsPath. An
// empty path requires decoded itself to be a JSON array. For non-empty
// paths every step except the last must be a JSON object; the final step
// must yield a JSON array. Errors name the step that failed so operators
// can fix the spec quickly.
func walkRecordsPath(decoded any, recordsPath string) ([]any, error) {
	recordsPath = strings.TrimSpace(recordsPath)
	if recordsPath == "" {
		arr, ok := decoded.([]any)
		if !ok {
			return nil, fmt.Errorf(
				"httpsource: response root is not a JSON array (got %s); set recordsPath to descend into the body",
				jsonKindName(decoded),
			)
		}
		return arr, nil
	}

	steps := strings.Split(recordsPath, ".")
	current := decoded
	for i, step := range steps {
		if step == "" {
			return nil, fmt.Errorf("httpsource: recordsPath %q has an empty key at position %d", recordsPath, i)
		}
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf(
				"httpsource: recordsPath %q: expected object at step %q, got %s",
				recordsPath, step, jsonKindName(current),
			)
		}
		next, present := obj[step]
		if !present {
			return nil, fmt.Errorf(
				"httpsource: recordsPath %q: key %q not found in response",
				recordsPath, step,
			)
		}
		current = next
	}

	arr, ok := current.([]any)
	if !ok {
		return nil, fmt.Errorf(
			"httpsource: recordsPath %q: terminal value is not a JSON array, got %s",
			recordsPath, jsonKindName(current),
		)
	}
	return arr, nil
}

// readBodyExcerpt pulls up to limit bytes from r and returns a printable
// excerpt suitable for embedding in error messages. Wrapping in
// io.LimitReader guards against multi-GB error responses.
func readBodyExcerpt(r io.Reader, limit int64) string {
	if limit <= 0 {
		return ""
	}
	limited := io.LimitReader(r, limit)
	buf, err := io.ReadAll(limited)
	if err != nil {
		// Best-effort: include what we managed to read.
		return strings.TrimSpace(string(buf))
	}
	excerpt := strings.TrimSpace(string(buf))
	// Collapse newlines and tabs so the excerpt fits cleanly in one log line.
	excerpt = strings.ReplaceAll(excerpt, "\n", " ")
	excerpt = strings.ReplaceAll(excerpt, "\r", " ")
	excerpt = strings.ReplaceAll(excerpt, "\t", " ")
	return excerpt
}

// jsonKindName returns a human-readable name for the dynamic JSON kind of
// v, used in error messages so operators can see why a path step failed.
func jsonKindName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64, float32, int, int32, int64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}
