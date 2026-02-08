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

package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/grafana/fleet-management-operator/pkg/fleetclient"
	"github.com/stretchr/testify/assert"
)

func TestIsTransientError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "FleetAPIError 500 Internal Server Error",
			err: &fleetclient.FleetAPIError{
				StatusCode: http.StatusInternalServerError,
				Operation:  "UpsertPipeline",
				Message:    "internal server error",
			},
			want: true,
		},
		{
			name: "FleetAPIError 502 Bad Gateway",
			err: &fleetclient.FleetAPIError{
				StatusCode: http.StatusBadGateway,
				Operation:  "UpsertPipeline",
				Message:    "bad gateway",
			},
			want: true,
		},
		{
			name: "FleetAPIError 503 Service Unavailable",
			err: &fleetclient.FleetAPIError{
				StatusCode: http.StatusServiceUnavailable,
				Operation:  "UpsertPipeline",
				Message:    "service unavailable",
			},
			want: true,
		},
		{
			name: "FleetAPIError 429 Too Many Requests",
			err: &fleetclient.FleetAPIError{
				StatusCode: http.StatusTooManyRequests,
				Operation:  "UpsertPipeline",
				Message:    "rate limit exceeded",
			},
			want: true,
		},
		{
			name: "FleetAPIError 408 Request Timeout",
			err: &fleetclient.FleetAPIError{
				StatusCode: http.StatusRequestTimeout,
				Operation:  "UpsertPipeline",
				Message:    "request timeout",
			},
			want: true,
		},
		{
			name: "FleetAPIError 400 Bad Request",
			err: &fleetclient.FleetAPIError{
				StatusCode: http.StatusBadRequest,
				Operation:  "UpsertPipeline",
				Message:    "validation error",
			},
			want: false,
		},
		{
			name: "FleetAPIError 401 Unauthorized",
			err: &fleetclient.FleetAPIError{
				StatusCode: http.StatusUnauthorized,
				Operation:  "UpsertPipeline",
				Message:    "unauthorized",
			},
			want: false,
		},
		{
			name: "FleetAPIError 404 Not Found",
			err: &fleetclient.FleetAPIError{
				StatusCode: http.StatusNotFound,
				Operation:  "UpsertPipeline",
				Message:    "not found",
			},
			want: false,
		},
		{
			name: "Wrapped FleetAPIError 500",
			err: fmt.Errorf("wrapped: %w", &fleetclient.FleetAPIError{
				StatusCode: http.StatusInternalServerError,
				Operation:  "UpsertPipeline",
				Message:    "internal error",
			}),
			want: true,
		},
		{
			name: "context.Canceled error",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "Generic error (network error)",
			err:  errors.New("network error"),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTransientError(tt.err)
			assert.Equal(t, tt.want, got, "isTransientError() = %v, want %v", got, tt.want)
		})
	}
}

func TestShouldRetry(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		reason string
		want   bool
	}{
		{
			name: "Validation error with transient error",
			err: &fleetclient.FleetAPIError{
				StatusCode: http.StatusInternalServerError,
				Operation:  "UpsertPipeline",
				Message:    "internal error",
			},
			reason: reasonValidationError,
			want:   false, // validation always permanent
		},
		{
			name: "SyncFailed with transient FleetAPIError 500",
			err: &fleetclient.FleetAPIError{
				StatusCode: http.StatusInternalServerError,
				Operation:  "UpsertPipeline",
				Message:    "internal error",
			},
			reason: reasonSyncFailed,
			want:   true,
		},
		{
			name: "SyncFailed with permanent FleetAPIError 400",
			err: &fleetclient.FleetAPIError{
				StatusCode: http.StatusBadRequest,
				Operation:  "UpsertPipeline",
				Message:    "validation error",
			},
			reason: reasonSyncFailed,
			want:   false,
		},
		{
			name: "DeleteFailed with transient FleetAPIError 503",
			err: &fleetclient.FleetAPIError{
				StatusCode: http.StatusServiceUnavailable,
				Operation:  "DeletePipeline",
				Message:    "service unavailable",
			},
			reason: reasonDeleteFailed,
			want:   true,
		},
		{
			name:   "SyncFailed with generic error",
			err:    errors.New("network error"),
			reason: reasonSyncFailed,
			want:   true, // unknown defaults to transient
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldRetry(tt.err, tt.reason)
			assert.Equal(t, tt.want, got, "shouldRetry() = %v, want %v", got, tt.want)
		})
	}
}
