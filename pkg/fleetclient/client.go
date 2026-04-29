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
	"net/http"
	"strings"
	"time"

	connect "connectrpc.com/connect"
	"github.com/grafana/fleet-management-api/api/gen/proto/go/collector/v1/collectorv1connect"
	pipelinev1 "github.com/grafana/fleet-management-api/api/gen/proto/go/pipeline/v1"
	"github.com/grafana/fleet-management-api/api/gen/proto/go/pipeline/v1/pipelinev1connect"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"golang.org/x/time/rate"
)

// clientConfig holds optional configuration for NewClient. Populated by
// applying ClientOption values; zero fields fall back to safe defaults.
type clientConfig struct {
	rps    float64
	burst  int
	tracer trace.Tracer
}

// ClientOption configures a Fleet Management API client.
type ClientOption func(*clientConfig)

// WithRateLimit sets the sustained request rate (rps) and burst size for the
// client's rate limiter. Both values must be positive; zero or negative values
// are silently replaced by the package defaults (3 rps / burst 50).
//
// Match rps to your Fleet Management server-side api: rate setting. The
// default 3 rps matches the standard stack default; large fleets with a
// higher server-side limit can increase this accordingly.
//
// burst controls how many requests may fire immediately before the sustained
// ceiling applies. burst=1 causes livelock at scale: with a 30s HTTP timeout,
// request #(rps*30+1) in a restart wave waits exactly 30s and times out —
// indistinguishable from a Fleet API outage. burst=50 absorbs startup and
// post-restart spikes without changing the sustained throughput ceiling.
func WithRateLimit(rps float64, burst int) ClientOption {
	return func(c *clientConfig) {
		c.rps = rps
		c.burst = burst
	}
}

// WithTracer sets the OpenTelemetry tracer used to instrument outgoing Fleet
// Management API calls. When not set (the default), a noop tracer is used so
// there is zero overhead.
func WithTracer(tracer trace.Tracer) ClientOption {
	return func(c *clientConfig) { c.tracer = tracer }
}

// Client is a client for the Fleet Management API. It speaks the connect
// protocol against the PipelineService and CollectorService using shared
// rate-limit and basic-auth interceptors.
type Client struct {
	baseURL    string
	limiter    *rate.Limiter
	httpClient *http.Client
	pipeline   pipelinev1connect.PipelineServiceClient
	collector  collectorv1connect.CollectorServiceClient
}

// Limiter returns the rate.Limiter used by this client. Exposed for
// observability instrumentation (OBS category) and testing.
//
// The returned limiter is goroutine-safe for read operations (Wait, Allow,
// Reserve, Burst, Limit). Calling SetLimit or SetBurst on it at runtime is
// UNSUPPORTED and will silently break the configured rps budget — concurrent
// callers may race with the configuration change, and the rate already used
// to construct the client (e.g. the Helm chart's apiRatePerSecond) becomes
// stale. To change the rate, construct a new Client instead.
func (c *Client) Limiter() *rate.Limiter { return c.limiter }

// NewClient creates a new Fleet Management API client.
//
// baseURL may be the historical service-suffixed URL ending in
// "/pipeline.v1.PipelineService/" (which is what the operator's existing
// Secret stores) or the bare server root. The service suffix is stripped
// automatically so existing deployments keep working.
//
// Use WithRateLimit to override the default rate (3 rps) and burst (50).
// Use WithTracer to enable OpenTelemetry tracing for Fleet API calls (noop by default).
func NewClient(baseURL, username, password string, opts ...ClientOption) *Client {
	cfg := clientConfig{rps: 3, burst: 50}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.rps <= 0 {
		cfg.rps = 3
	}
	if cfg.burst <= 0 {
		cfg.burst = 50
	}

	tracer := cfg.tracer
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer("fleet-management-operator")
	}

	rootURL := normalizeBaseURL(baseURL)

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}

	limiter := rate.NewLimiter(rate.Limit(cfg.rps), cfg.burst)

	// Interceptor order matters. Each interceptor wraps the next, so the
	// outermost (first listed) sees the broadest view of the call:
	//   tracing  — must be first so the span encompasses rate-limit wait
	//              time; otherwise spans show only post-wait API latency
	//              and operators cannot see when wait time is the
	//              bottleneck.
	//   rateLimit — blocks before basic auth or the network round-trip;
	//              this is the rps gate we report on as a separate
	//              histogram (fleetAPIRateLimiterWait).
	//   basicAuth — sets the Authorization header on the outgoing request.
	//   metrics   — innermost; records duration of the actual API call
	//              (post-wait, post-auth) so request-duration metrics
	//              reflect Fleet API performance, not local queuing.
	interceptors := connect.WithInterceptors(
		tracingInterceptor(tracer, rootURL),
		rateLimitInterceptor(limiter),
		basicAuthInterceptor(username, password),
		metricsInterceptor(),
	)

	return &Client{
		baseURL:    rootURL,
		limiter:    limiter,
		httpClient: httpClient,
		pipeline:   pipelinev1connect.NewPipelineServiceClient(httpClient, rootURL, interceptors),
		collector:  collectorv1connect.NewCollectorServiceClient(httpClient, rootURL, interceptors),
	}
}

// Close releases idle connections held by the HTTP transport. Call this when
// the client will no longer be used (e.g. on manager shutdown) to ensure
// clean shutdown without waiting for idle-connection timeouts.
func (c *Client) Close() {
	c.httpClient.CloseIdleConnections()
}

// normalizeBaseURL trims trailing slashes and any known service suffix so that
// a user-supplied baseURL of either form resolves to the bare server root that
// connect-go's client constructors expect.
func normalizeBaseURL(baseURL string) string {
	trimmed := strings.TrimRight(baseURL, "/")
	for _, svc := range []string{
		pipelinev1connect.PipelineServiceName,
		collectorv1connect.CollectorServiceName,
	} {
		suffix := "/" + svc
		if before, ok := strings.CutSuffix(trimmed, suffix); ok {
			trimmed = before
			break
		}
	}
	return trimmed
}

// UpsertPipeline creates a new pipeline or updates an existing one and returns
// the server's view of the result.
func (c *Client) UpsertPipeline(ctx context.Context, req *UpsertPipelineRequest) (*Pipeline, error) {
	protoReq := &pipelinev1.UpsertPipelineRequest{
		Pipeline:     pipelineToProto(req.Pipeline),
		ValidateOnly: req.ValidateOnly,
	}

	resp, err := c.pipeline.UpsertPipeline(ctx, connect.NewRequest(protoReq))
	if err != nil {
		return nil, connectErrToFleetErr(err, "UpsertPipeline", "")
	}

	return pipelineFromProto(resp.Msg), nil
}

// DeletePipeline deletes a pipeline by ID. A 404 is treated as success because
// the resource is already gone — this matches the existing controller's
// finalizer expectations.
func (c *Client) DeletePipeline(ctx context.Context, id string) error {
	protoReq := &pipelinev1.DeletePipelineRequest{Id: id}

	_, err := c.pipeline.DeletePipeline(ctx, connect.NewRequest(protoReq))
	if err == nil {
		return nil
	}

	fleetErr := connectErrToFleetErr(err, "DeletePipeline", id)
	if apiErr, ok := fleetErr.(*FleetAPIError); ok && apiErr.StatusCode == http.StatusNotFound {
		return nil
	}
	return fleetErr
}

// ListPipelines returns all pipelines matching the supplied filters.
// Pagination note: the Fleet SDK's ListPipelinesRequest does not yet expose
// page_token / page_size; responses for broad selectors may be large.
func (c *Client) ListPipelines(ctx context.Context, req *ListPipelinesRequest) ([]*Pipeline, error) {
	protoReq := &pipelinev1.ListPipelinesRequest{}
	if req != nil && req.ConfigType != nil {
		ct := configTypeStringToProto(*req.ConfigType)
		protoReq.ConfigType = &ct
	}
	if req != nil && req.Enabled != nil {
		protoReq.Enabled = req.Enabled
	}
	resp, err := c.pipeline.ListPipelines(ctx, connect.NewRequest(protoReq))
	if err != nil {
		return nil, connectErrToFleetErr(err, "ListPipelines", "")
	}
	protoPipelines := resp.Msg.GetPipelines()
	out := make([]*Pipeline, 0, len(protoPipelines))
	for _, pp := range protoPipelines {
		out = append(out, pipelineFromProto(pp))
	}
	return out, nil
}
