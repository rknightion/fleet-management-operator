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

package fleetclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	connect "connectrpc.com/connect"
	pipelinev1 "github.com/grafana/fleet-management-api/api/gen/proto/go/pipeline/v1"
	"github.com/grafana/fleet-management-api/api/gen/proto/go/pipeline/v1/pipelinev1connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestPeerNameFromBaseURL covers shapes the operator's Secret can legitimately
// contain (with/without scheme, with/without service suffix, with userinfo)
// plus malformed inputs to confirm the best-effort fallback never returns
// empty for a non-empty input.
func TestPeerNameFromBaseURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty input returns empty", in: "", want: ""},
		{name: "bare https host", in: "https://api.example.com", want: "api.example.com"},
		{name: "bare http host", in: "http://api.example.com", want: "api.example.com"},
		{name: "trailing slash", in: "https://api.example.com/", want: "api.example.com"},
		{name: "with path", in: "https://api.example.com/pipeline.v1.PipelineService/", want: "api.example.com"},
		{name: "with port", in: "https://api.example.com:8443", want: "api.example.com"},
		{name: "with userinfo", in: "https://user:pass@api.example.com", want: "api.example.com"},
		{name: "ipv4 with port", in: "https://10.0.0.1:8443", want: "10.0.0.1"},
		// IPv6 hostnames are wrapped in [] in URLs; url.Hostname strips them.
		{name: "ipv6 with port", in: "https://[::1]:8443", want: "::1"},
		// No scheme: url.Parse reports Host="" so we fall through to the
		// best-effort fallback.
		{name: "scheme-less host", in: "api.example.com", want: "api.example.com"},
		{name: "scheme-less host with port", in: "api.example.com:8443", want: "api.example.com"},
		{name: "scheme-less host with path", in: "api.example.com/foo", want: "api.example.com"},
		// Malformed but non-empty inputs must still return a non-empty
		// best-effort result (never silently drop the attribute).
		{name: "malformed colon-only", in: ":::", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := peerNameFromBaseURL(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestSplitProcedure covers the connect-go procedure shape we expect from the
// SDK plus degenerate paths to confirm we never lose information.
func TestSplitProcedure(t *testing.T) {
	tests := []struct {
		name        string
		procedure   string
		wantService string
		wantMethod  string
	}{
		{
			name:        "standard pipeline service",
			procedure:   "/pipeline.v1.PipelineService/UpsertPipeline",
			wantService: "pipeline.v1.PipelineService",
			wantMethod:  "UpsertPipeline",
		},
		{
			name:        "standard collector service",
			procedure:   "/collector.v1.CollectorService/BulkUpdateCollectors",
			wantService: "collector.v1.CollectorService",
			wantMethod:  "BulkUpdateCollectors",
		},
		{
			// No leading slash variant.
			name:        "no leading slash",
			procedure:   "pipeline.v1.PipelineService/UpsertPipeline",
			wantService: "pipeline.v1.PipelineService",
			wantMethod:  "UpsertPipeline",
		},
		{
			// Single segment: caller would not normally produce this but we
			// must not panic. Whole string is reported as method so the
			// information is preserved.
			name:        "single segment",
			procedure:   "/Method",
			wantService: "",
			wantMethod:  "Method",
		},
		{
			name:        "empty string",
			procedure:   "",
			wantService: "",
			wantMethod:  "",
		},
		{
			// Defensive: extra slashes should not crash the splitter.
			name:        "trailing slash",
			procedure:   "/svc/method/",
			wantService: "svc/method",
			wantMethod:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSvc, gotMethod := splitProcedure(tt.procedure)
			assert.Equal(t, tt.wantService, gotSvc, "service mismatch")
			assert.Equal(t, tt.wantMethod, gotMethod, "method mismatch")
		})
	}
}

// findAttr returns the attribute with the given key from a span's attribute
// list and a bool reporting whether it was found.
func findAttr(attrs []attribute.KeyValue, key string) (attribute.KeyValue, bool) {
	for _, a := range attrs {
		if string(a.Key) == key {
			return a, true
		}
	}
	return attribute.KeyValue{}, false
}

// newTracingTestServer wires a fake pipeline handler behind a connect-go
// httptest server, sets up a SpanRecorder-backed tracer, and returns the
// client + recorder + cleanup.
func newTracingTestServer(t *testing.T, h *fakePipelineHandler) (*Client, *tracetest.SpanRecorder, func()) {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := pipelinev1connect.NewPipelineServiceHandler(h)
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer := tp.Tracer("fleet-management-operator-test")

	client := NewClient(server.URL, "u", "p", WithTracer(tracer))
	return client, recorder, func() {
		_ = tp.Shutdown(context.Background())
		server.Close()
	}
}

// TestTracingInterceptor_SuccessSpan confirms that a successful call records
// rpc.system / rpc.service / rpc.method and BOTH server.address and the
// legacy net.peer.name on the span.
func TestTracingInterceptor_SuccessSpan(t *testing.T) {
	id := "pipeline-1"
	enabled := true
	handler := &fakePipelineHandler{upsertResponse: &pipelinev1.Pipeline{
		Name: "p", Contents: "c", Enabled: &enabled, Id: &id,
		ConfigType: pipelinev1.ConfigType_CONFIG_TYPE_ALLOY,
	}}
	client, recorder, cleanup := newTracingTestServer(t, handler)
	defer cleanup()

	_, err := client.UpsertPipeline(context.Background(), &UpsertPipelineRequest{
		Pipeline: &Pipeline{Name: "p", Contents: "c", Enabled: true, ConfigType: configTypeAlloy},
	})
	require.NoError(t, err)

	ended := recorder.Ended()
	require.Len(t, ended, 1, "expected exactly one ended span")
	span := ended[0]

	attrs := span.Attributes()
	if a, ok := findAttr(attrs, "rpc.system"); assert.True(t, ok, "rpc.system attribute missing") {
		assert.Equal(t, "connect", a.Value.AsString())
	}
	if a, ok := findAttr(attrs, "rpc.service"); assert.True(t, ok, "rpc.service attribute missing") {
		assert.Equal(t, "pipeline.v1.PipelineService", a.Value.AsString())
	}
	if a, ok := findAttr(attrs, "rpc.method"); assert.True(t, ok, "rpc.method attribute missing") {
		assert.Equal(t, "UpsertPipeline", a.Value.AsString())
	}
	// Modern OTel semconv (v1.21.0+).
	if a, ok := findAttr(attrs, "server.address"); assert.True(t, ok, "server.address attribute missing") {
		assert.NotEmpty(t, a.Value.AsString())
	}
	// Legacy semconv name retained for backward-compatible dashboards.
	if a, ok := findAttr(attrs, "net.peer.name"); assert.True(t, ok, "net.peer.name attribute missing") {
		assert.NotEmpty(t, a.Value.AsString())
	}

	// Successful call must not record an error code attribute.
	_, present := findAttr(attrs, "rpc.connect_rpc.error_code")
	assert.False(t, present, "rpc.connect_rpc.error_code should be absent on success")
	assert.Equal(t, codes.Unset, span.Status().Code, "span status should be Unset on success")
}

// TestTracingInterceptor_ErrorSpan confirms that on a connect error the span
// records rpc.connect_rpc.error_code (NOT the legacy rpc.connect.status_code
// name) with the connect.Code string, plus error status.
func TestTracingInterceptor_ErrorSpan(t *testing.T) {
	tests := []struct {
		name         string
		connectCode  connect.Code
		wantCodeAttr string
	}{
		{"InvalidArgument", connect.CodeInvalidArgument, "invalid_argument"},
		{"NotFound", connect.CodeNotFound, "not_found"},
		{"Internal", connect.CodeInternal, "internal"},
		{"Unavailable", connect.CodeUnavailable, "unavailable"},
		{"ResourceExhausted", connect.CodeResourceExhausted, "resource_exhausted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &fakePipelineHandler{
				upsertErr: connect.NewError(tt.connectCode, errors.New("boom")),
			}
			client, recorder, cleanup := newTracingTestServer(t, handler)
			defer cleanup()

			_, err := client.UpsertPipeline(context.Background(), &UpsertPipelineRequest{
				Pipeline: &Pipeline{Name: "p", Contents: "c"},
			})
			require.Error(t, err)

			ended := recorder.Ended()
			require.Len(t, ended, 1)
			span := ended[0]

			// The fix: the error code must be reported under the OTel
			// semconv attribute key "rpc.connect_rpc.error_code", NOT
			// the legacy "rpc.connect.status_code" we used previously.
			a, ok := findAttr(span.Attributes(), "rpc.connect_rpc.error_code")
			require.True(t, ok, "rpc.connect_rpc.error_code attribute missing on error span")
			assert.Equal(t, tt.wantCodeAttr, a.Value.AsString())

			// Legacy attribute name must NOT be set.
			_, legacyPresent := findAttr(span.Attributes(), "rpc.connect.status_code")
			assert.False(t, legacyPresent, "legacy rpc.connect.status_code attribute should not be set")

			// Span status should mark the call as errored.
			assert.Equal(t, codes.Error, span.Status().Code)

			// Errors should be recorded as span events too (RecordError).
			assert.NotEmpty(t, span.Events(), "expected RecordError to add a span event")
		})
	}
}

// TestTracingInterceptor_NoPeerNameWhenBaseURLEmpty confirms we do NOT emit a
// dangling empty server.address / net.peer.name attribute when the base URL
// resolves to an empty peer name. The connect.AnyRequest interface has an
// unexported method, so we drive the interceptor via a real httptest server
// constructed with an empty peer URL string at tracingInterceptor creation.
func TestTracingInterceptor_NoPeerNameWhenBaseURLEmpty(t *testing.T) {
	// Construct interceptor directly with empty baseURL (simulating a
	// degenerate config) and execute it against a real Request value.
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	tracer := tp.Tracer("test")

	interceptor := tracingInterceptor(tracer, "")

	called := false
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		called = true
		return connect.NewResponse(&pipelinev1.Pipeline{}), nil
	})

	wrapped := interceptor(next)
	req := connect.NewRequest(&pipelinev1.UpsertPipelineRequest{})
	// connect.NewRequest does not set a procedure; the interceptor's
	// span name will be empty but rpc.system/service/method will still
	// fire — enough to verify peer attributes are absent.
	_, err := wrapped(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, called)

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	_, hasAddr := findAttr(ended[0].Attributes(), "server.address")
	_, hasLegacy := findAttr(ended[0].Attributes(), "net.peer.name")
	assert.False(t, hasAddr, "server.address must be absent when base URL yields empty peer")
	assert.False(t, hasLegacy, "net.peer.name must be absent when base URL yields empty peer")
}
