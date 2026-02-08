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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/time/rate"
)

// Client is a client for the Fleet Management Pipeline API
type Client struct {
	baseURL    string
	httpClient *http.Client
	limiter    *rate.Limiter
	username   string
	password   string
}

// NewClient creates a new Fleet Management API client
func NewClient(baseURL, username, password string) *Client {
	return &Client{
		baseURL:  baseURL,
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				IdleConnTimeout:     90 * time.Second,
				TLSHandshakeTimeout: 10 * time.Second,
			},
		},
		// Fleet Management API rate limit: 3 requests per second
		limiter: rate.NewLimiter(rate.Limit(3), 1),
	}
}

// UpsertPipeline creates or updates a pipeline
func (c *Client) UpsertPipeline(ctx context.Context, req *UpsertPipelineRequest) (*Pipeline, error) {
	// Wait for rate limiter
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter error: %w", err)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"UpsertPipeline", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.SetBasicAuth(c.username, c.password)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, &FleetAPIError{
				StatusCode: resp.StatusCode,
				Operation:  "UpsertPipeline",
				Message:    fmt.Sprintf("HTTP %d (failed to read response body: %v)", resp.StatusCode, err),
				Wrapped:    err,
			}
		}
		return nil, &FleetAPIError{
			StatusCode: resp.StatusCode,
			Operation:  "UpsertPipeline",
			Message:    string(bodyBytes),
		}
	}

	var pipeline Pipeline
	if err := json.NewDecoder(resp.Body).Decode(&pipeline); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &pipeline, nil
}

// DeletePipeline deletes a pipeline by ID
func (c *Client) DeletePipeline(ctx context.Context, id string) error {
	// Wait for rate limiter
	if err := c.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limiter error: %w", err)
	}

	req := map[string]string{"id": id}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"DeletePipeline", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.SetBasicAuth(c.username, c.password)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// 404 is treated as success (pipeline already deleted)
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return &FleetAPIError{
				StatusCode: resp.StatusCode,
				Operation:  "DeletePipeline",
				Message:    fmt.Sprintf("HTTP %d (failed to read response body: %v)", resp.StatusCode, err),
				PipelineID: id,
				Wrapped:    err,
			}
		}
		return &FleetAPIError{
			StatusCode: resp.StatusCode,
			Operation:  "DeletePipeline",
			Message:    string(bodyBytes),
			PipelineID: id,
		}
	}

	return nil
}
