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
	"golang.org/x/time/rate"
)

// Client is a client for the Fleet Management API. It speaks the connect
// protocol against the PipelineService and CollectorService using shared
// rate-limit and basic-auth interceptors.
type Client struct {
	baseURL   string
	limiter   *rate.Limiter
	pipeline  pipelinev1connect.PipelineServiceClient
	collector collectorv1connect.CollectorServiceClient
}

// NewClient creates a new Fleet Management API client.
//
// baseURL may be the historical service-suffixed URL ending in
// "/pipeline.v1.PipelineService/" (which is what the operator's existing
// Secret stores) or the bare server root. The service suffix is stripped
// automatically so existing deployments keep working.
func NewClient(baseURL, username, password string) *Client {
	rootURL := normalizeBaseURL(baseURL)

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}

	// Fleet Management API rate limit: 3 req/s for management endpoints. This
	// is shared across both PipelineService and (future) CollectorService.
	limiter := rate.NewLimiter(rate.Limit(3), 1)

	interceptors := connect.WithInterceptors(
		rateLimitInterceptor(limiter),
		basicAuthInterceptor(username, password),
	)

	return &Client{
		baseURL:   rootURL,
		limiter:   limiter,
		pipeline:  pipelinev1connect.NewPipelineServiceClient(httpClient, rootURL, interceptors),
		collector: collectorv1connect.NewCollectorServiceClient(httpClient, rootURL, interceptors),
	}
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
		if strings.HasSuffix(trimmed, suffix) {
			trimmed = strings.TrimSuffix(trimmed, suffix)
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
