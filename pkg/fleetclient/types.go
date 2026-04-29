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
	"fmt"
	"net/http"
	"time"
)

// Pipeline represents a Fleet Management pipeline
type Pipeline struct {
	Name       string     `json:"name"`
	Contents   string     `json:"contents"`
	Matchers   []string   `json:"matchers,omitempty"`
	Enabled    bool       `json:"enabled"`
	ID         string     `json:"id,omitempty"`
	ConfigType string     `json:"configType,omitempty"`
	Source     *Source    `json:"source,omitempty"`
	CreatedAt  *time.Time `json:"createdAt,omitempty"`
	UpdatedAt  *time.Time `json:"updatedAt,omitempty"`
}

// Source represents the origin of a pipeline
type Source struct {
	Type      string `json:"type,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

// UpsertPipelineRequest is the request to create or update a pipeline
type UpsertPipelineRequest struct {
	Pipeline     *Pipeline `json:"pipeline"`
	ValidateOnly bool      `json:"validateOnly,omitempty"`
}

// ListPipelinesRequest filters the ListPipelines call. Nil fields mean no filter.
type ListPipelinesRequest struct {
	ConfigType *string // "CONFIG_TYPE_ALLOY" or "CONFIG_TYPE_OTEL"; nil = all
	Enabled    *bool   // nil = all
}

// FleetAPIError represents an error from the Fleet Management API
type FleetAPIError struct {
	StatusCode int
	Operation  string
	Message    string
	PipelineID string // For distributed tracing, optional (empty string = not available)
	Wrapped    error  // For error chain compatibility with errors.As/errors.Is
}

func (e *FleetAPIError) Error() string {
	if e.PipelineID != "" {
		return fmt.Sprintf("%s failed (pipeline=%s, status=%d): %s", e.Operation, e.PipelineID, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("%s failed (status=%d): %s", e.Operation, e.StatusCode, e.Message)
}

// Unwrap returns the wrapped error for error chain compatibility
func (e *FleetAPIError) Unwrap() error {
	return e.Wrapped
}

// IsTransient determines if the error is transient and should be retried
func (e *FleetAPIError) IsTransient() bool {
	// 429 Too Many Requests - rate limited, retry
	if e.StatusCode == http.StatusTooManyRequests {
		return true
	}
	// 408 Request Timeout - timeout, retry
	if e.StatusCode == http.StatusRequestTimeout {
		return true
	}
	// 500-599 Server Errors - server issue, retry
	if e.StatusCode >= 500 && e.StatusCode < 600 {
		return true
	}
	// All other status codes - client error, permanent
	return false
}
