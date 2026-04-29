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
	"net/url"
	"strings"

	connect "connectrpc.com/connect"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// peerNameFromBaseURL extracts the host portion of the configured Fleet
// Management base URL for use as the OTel net.peer.name attribute. If parsing
// fails, the raw value is returned (with any scheme stripped) so the attribute
// is never empty for a non-empty input.
func peerNameFromBaseURL(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		return u.Hostname()
	}
	// Best-effort fallback: strip scheme, then any path/port suffix.
	trimmed := baseURL
	if i := strings.Index(trimmed, "://"); i >= 0 {
		trimmed = trimmed[i+3:]
	}
	if i := strings.Index(trimmed, "/"); i >= 0 {
		trimmed = trimmed[:i]
	}
	if i := strings.Index(trimmed, ":"); i >= 0 {
		trimmed = trimmed[:i]
	}
	return trimmed
}

// splitProcedure splits a connect procedure string of the form
// "/pkg.v1.ServiceName/MethodName" into (service, method) so it can be
// reported as the OTel-semconv rpc.service and rpc.method attributes
// instead of crammed into a single rpc.method label.
func splitProcedure(procedure string) (service, method string) {
	trimmed := strings.TrimPrefix(procedure, "/")
	idx := strings.LastIndex(trimmed, "/")
	if idx < 0 {
		// Unrecognised shape; fall back to reporting the whole string
		// as the method so we never lose information.
		return "", trimmed
	}
	return trimmed[:idx], trimmed[idx+1:]
}

// tracingInterceptor returns a unary client interceptor that creates an
// OpenTelemetry span for each outgoing Fleet Management API call. When a noop
// tracer is supplied (the default) the interceptor adds no overhead beyond a
// nil-safe function call.
//
// peerName is attached as net.peer.name on every span and is computed once at
// client construction so we do not re-parse the base URL on every call.
func tracingInterceptor(tracer trace.Tracer, baseURL string) connect.UnaryInterceptorFunc {
	peerName := peerNameFromBaseURL(baseURL)
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			procedure := req.Spec().Procedure
			service, method := splitProcedure(procedure)
			attrs := []attribute.KeyValue{
				// rpc.system identifies the RPC framework. "connect" is
				// the documented OTel value for connect-go.
				attribute.String("rpc.system", "connect"),
				attribute.String("rpc.service", service),
				attribute.String("rpc.method", method),
			}
			if peerName != "" {
				attrs = append(attrs, attribute.String("net.peer.name", peerName))
			}
			ctx, span := tracer.Start(ctx, procedure,
				trace.WithSpanKind(trace.SpanKindClient),
				trace.WithAttributes(attrs...),
			)
			defer span.End()
			resp, err := next(ctx, req)
			if err != nil {
				// Mirror what metrics.go does: extract the connect status
				// code (CodeOf returns CodeUnknown for non-connect errors,
				// which is still useful information).
				span.SetAttributes(attribute.String("rpc.connect.status_code", connect.CodeOf(err).String()))
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			}
			return resp, err
		}
	}
}
