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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	connect "connectrpc.com/connect"
	pipelinev1 "github.com/grafana/fleet-management-api/api/gen/proto/go/pipeline/v1"
	"github.com/grafana/fleet-management-api/api/gen/proto/go/pipeline/v1/pipelinev1connect"
	"github.com/stretchr/testify/assert"
)

// TestFleetAPIError_IsTransient tests the transient error classification logic
func TestFleetAPIError_IsTransient(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		expected   bool
	}{
		// Transient errors - should retry
		{name: "429 Too Many Requests", statusCode: http.StatusTooManyRequests, expected: true},
		{name: "408 Request Timeout", statusCode: http.StatusRequestTimeout, expected: true},
		{name: "500 Internal Server Error", statusCode: http.StatusInternalServerError, expected: true},
		{name: "502 Bad Gateway", statusCode: http.StatusBadGateway, expected: true},
		{name: "503 Service Unavailable", statusCode: http.StatusServiceUnavailable, expected: true},
		{name: "504 Gateway Timeout", statusCode: http.StatusGatewayTimeout, expected: true},

		// Permanent errors - should not retry
		{name: "400 Bad Request", statusCode: http.StatusBadRequest, expected: false},
		{name: "401 Unauthorized", statusCode: http.StatusUnauthorized, expected: false},
		{name: "403 Forbidden", statusCode: http.StatusForbidden, expected: false},
		{name: "404 Not Found", statusCode: http.StatusNotFound, expected: false},
		{name: "405 Method Not Allowed", statusCode: http.StatusMethodNotAllowed, expected: false},
		{name: "409 Conflict", statusCode: http.StatusConflict, expected: false},
		{name: "200 OK (edge case)", statusCode: http.StatusOK, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &FleetAPIError{StatusCode: tt.statusCode}
			got := err.IsTransient()
			assert.Equal(t, tt.expected, got, "IsTransient() for status code %d", tt.statusCode)
		})
	}
}

// TestFleetAPIError_Error tests the error message formatting
func TestFleetAPIError_Error(t *testing.T) {
	tests := []struct {
		name             string
		err              *FleetAPIError
		expectedContains []string
		notContains      []string
	}{
		{
			name: "With PipelineID",
			err: &FleetAPIError{
				StatusCode: 500,
				Operation:  "UpsertPipeline",
				Message:    "server error",
				PipelineID: "pipe-123",
			},
			expectedContains: []string{"pipeline=pipe-123", "status=500", "UpsertPipeline", "server error"},
			notContains:      nil,
		},
		{
			name: "Without PipelineID",
			err: &FleetAPIError{
				StatusCode: 400,
				Operation:  "DeletePipeline",
				Message:    "bad request",
			},
			expectedContains: []string{"status=400", "DeletePipeline", "bad request"},
			notContains:      []string{"pipeline="},
		},
		{
			name: "Empty message",
			err: &FleetAPIError{
				StatusCode: 404,
				Operation:  "GetPipeline",
				Message:    "",
			},
			expectedContains: []string{"status=404", "GetPipeline"},
			notContains:      []string{"pipeline="},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errStr := tt.err.Error()
			for _, expected := range tt.expectedContains {
				assert.Contains(t, errStr, expected, "Error() should contain '%s'", expected)
			}
			for _, notExpected := range tt.notContains {
				assert.NotContains(t, errStr, notExpected, "Error() should not contain '%s'", notExpected)
			}
		})
	}
}

// TestFleetAPIError_Unwrap tests the error unwrapping functionality
func TestFleetAPIError_Unwrap(t *testing.T) {
	t.Run("With wrapped error", func(t *testing.T) {
		wrappedErr := io.ErrUnexpectedEOF
		apiErr := &FleetAPIError{
			StatusCode: 500,
			Operation:  "UpsertPipeline",
			Message:    "read error",
			Wrapped:    wrappedErr,
		}

		unwrapped := apiErr.Unwrap()
		assert.Equal(t, wrappedErr, unwrapped, "Unwrap() should return the wrapped error")
		assert.Same(t, wrappedErr, unwrapped, "Unwrap() should return the exact same error instance")
	})

	t.Run("Without wrapped error", func(t *testing.T) {
		apiErr := &FleetAPIError{
			StatusCode: 400,
			Operation:  "DeletePipeline",
			Message:    "bad request",
			Wrapped:    nil,
		}

		unwrapped := apiErr.Unwrap()
		assert.Nil(t, unwrapped, "Unwrap() should return nil when no error is wrapped")
	})
}

// TestFleetAPIError_ErrorsAs tests errors.As compatibility through wrapped chains
func TestFleetAPIError_ErrorsAs(t *testing.T) {
	t.Run("Single wrap", func(t *testing.T) {
		apiErr := &FleetAPIError{
			StatusCode: 500,
			Operation:  "UpsertPipeline",
			Message:    "server error",
			PipelineID: "pipe-456",
		}
		wrappedErr := fmt.Errorf("context: %w", apiErr)

		var extractedErr *FleetAPIError
		found := errors.As(wrappedErr, &extractedErr)

		assert.True(t, found, "errors.As should find FleetAPIError in wrapped chain")
		assert.Equal(t, apiErr.StatusCode, extractedErr.StatusCode, "Extracted error should have same StatusCode")
		assert.Equal(t, apiErr.Operation, extractedErr.Operation, "Extracted error should have same Operation")
		assert.Equal(t, apiErr.Message, extractedErr.Message, "Extracted error should have same Message")
		assert.Equal(t, apiErr.PipelineID, extractedErr.PipelineID, "Extracted error should have same PipelineID")
	})

	t.Run("Double wrap", func(t *testing.T) {
		apiErr := &FleetAPIError{
			StatusCode: 429,
			Operation:  "DeletePipeline",
			Message:    "rate limited",
		}
		innerWrap := fmt.Errorf("inner: %w", apiErr)
		outerWrap := fmt.Errorf("outer: %w", innerWrap)

		var extractedErr *FleetAPIError
		found := errors.As(outerWrap, &extractedErr)

		assert.True(t, found, "errors.As should find FleetAPIError through double wrap")
		assert.Equal(t, apiErr.StatusCode, extractedErr.StatusCode, "Extracted error should have same StatusCode")
		assert.Equal(t, apiErr.Operation, extractedErr.Operation, "Extracted error should have same Operation")
	})

	t.Run("Not found", func(t *testing.T) {
		regularErr := fmt.Errorf("regular error")

		var extractedErr *FleetAPIError
		found := errors.As(regularErr, &extractedErr)

		assert.False(t, found, "errors.As should not find FleetAPIError in regular error")
		assert.Nil(t, extractedErr, "extractedErr should remain nil when not found")
	})
}

// TestFleetAPIError_ErrorsIs tests errors.Is chain traversal through FleetAPIError.Unwrap
func TestFleetAPIError_ErrorsIs(t *testing.T) {
	t.Run("Direct wrapped error", func(t *testing.T) {
		wrappedErr := io.ErrUnexpectedEOF
		apiErr := &FleetAPIError{
			StatusCode: 500,
			Operation:  "UpsertPipeline",
			Message:    "read failed",
			Wrapped:    wrappedErr,
		}

		found := errors.Is(apiErr, io.ErrUnexpectedEOF)
		assert.True(t, found, "errors.Is should find io.ErrUnexpectedEOF through FleetAPIError.Unwrap")
	})

	t.Run("Further wrapped chain", func(t *testing.T) {
		baseErr := io.ErrUnexpectedEOF
		apiErr := &FleetAPIError{
			StatusCode: 500,
			Operation:  "DeletePipeline",
			Message:    "read failed",
			Wrapped:    baseErr,
		}
		outerWrap := fmt.Errorf("outer context: %w", apiErr)

		found := errors.Is(outerWrap, io.ErrUnexpectedEOF)
		assert.True(t, found, "errors.Is should traverse through outer wrap and FleetAPIError.Unwrap")
	})

	t.Run("Not wrapped", func(t *testing.T) {
		apiErr := &FleetAPIError{
			StatusCode: 404,
			Operation:  "GetPipeline",
			Message:    "not found",
			Wrapped:    nil,
		}

		found := errors.Is(apiErr, io.ErrUnexpectedEOF)
		assert.False(t, found, "errors.Is should not find error when nothing is wrapped")
	})

	t.Run("Different wrapped error", func(t *testing.T) {
		apiErr := &FleetAPIError{
			StatusCode: 500,
			Operation:  "UpsertPipeline",
			Message:    "closed",
			Wrapped:    io.ErrClosedPipe,
		}

		found := errors.Is(apiErr, io.ErrUnexpectedEOF)
		assert.False(t, found, "errors.Is should not find io.ErrUnexpectedEOF when different error is wrapped")
	})
}

// fakePipelineHandler is a configurable connect-go server handler used to
// drive client tests through the real wire protocol.
type fakePipelineHandler struct {
	pipelinev1connect.UnimplementedPipelineServiceHandler
	upsertResponse *pipelinev1.Pipeline
	upsertErr      error
	deleteErr      error
	captureRequest *pipelinev1.UpsertPipelineRequest
}

func (h *fakePipelineHandler) UpsertPipeline(_ context.Context, req *connect.Request[pipelinev1.UpsertPipelineRequest]) (*connect.Response[pipelinev1.Pipeline], error) {
	h.captureRequest = req.Msg
	if h.upsertErr != nil {
		return nil, h.upsertErr
	}
	return connect.NewResponse(h.upsertResponse), nil
}

func (h *fakePipelineHandler) DeletePipeline(_ context.Context, _ *connect.Request[pipelinev1.DeletePipelineRequest]) (*connect.Response[pipelinev1.DeletePipelineResponse], error) {
	if h.deleteErr != nil {
		return nil, h.deleteErr
	}
	return connect.NewResponse(&pipelinev1.DeletePipelineResponse{}), nil
}

// newConnectTestServer wires the fake handler into an httptest server and
// returns a Client pointed at it plus a cleanup function.
func newConnectTestServer(t *testing.T, h *fakePipelineHandler) (*Client, func()) {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := pipelinev1connect.NewPipelineServiceHandler(h)
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)

	client := NewClient(server.URL, "testuser", "testpass")
	return client, server.Close
}

// TestUpsertPipeline_ConnectErrors verifies that connect-go error codes from
// the server map back to the HTTP-status-coded FleetAPIError contract that
// internal/controller/errors.go relies on.
func TestUpsertPipeline_ConnectErrors(t *testing.T) {
	tests := []struct {
		name           string
		connectCode    connect.Code
		message        string
		wantStatusCode int
		wantTransient  bool
	}{
		{"InvalidArgument -> 400", connect.CodeInvalidArgument, "invalid pipeline configuration", http.StatusBadRequest, false},
		{"Unauthenticated -> 401", connect.CodeUnauthenticated, "authentication failed", http.StatusUnauthorized, false},
		{"PermissionDenied -> 403", connect.CodePermissionDenied, "insufficient permissions", http.StatusForbidden, false},
		{"NotFound -> 404", connect.CodeNotFound, "pipeline gone", http.StatusNotFound, false},
		{"ResourceExhausted -> 429", connect.CodeResourceExhausted, "rate limit exceeded", http.StatusTooManyRequests, true},
		{"Internal -> 500", connect.CodeInternal, "internal error occurred", http.StatusInternalServerError, true},
		{"Unavailable -> 503", connect.CodeUnavailable, "service temporarily unavailable", http.StatusServiceUnavailable, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &fakePipelineHandler{
				upsertErr: connect.NewError(tt.connectCode, errors.New(tt.message)),
			}
			client, cleanup := newConnectTestServer(t, handler)
			defer cleanup()

			req := &UpsertPipelineRequest{
				Pipeline: &Pipeline{
					Name:     "test-pipeline",
					Contents: "test contents",
					Enabled:  true,
				},
			}

			pipeline, err := client.UpsertPipeline(context.Background(), req)
			assert.Error(t, err)
			assert.Nil(t, pipeline)

			var fleetErr *FleetAPIError
			assert.True(t, errors.As(err, &fleetErr), "Error should be FleetAPIError")
			assert.Equal(t, tt.wantStatusCode, fleetErr.StatusCode, "StatusCode mismatch")
			assert.Equal(t, "UpsertPipeline", fleetErr.Operation, "Operation mismatch")
			assert.Equal(t, tt.wantTransient, fleetErr.IsTransient(), "IsTransient mismatch")
			assert.Contains(t, fleetErr.Message, tt.message, "Message should contain expected text")
		})
	}
}

// TestUpsertPipeline_Success confirms request fields propagate to proto and
// the proto response decodes back to the wire-public Pipeline shape.
func TestUpsertPipeline_Success(t *testing.T) {
	id := "pipeline-123"
	enabled := true
	expectedProto := &pipelinev1.Pipeline{
		Name:       "test-pipeline",
		Contents:   "test contents",
		Enabled:    &enabled,
		Id:         &id,
		ConfigType: pipelinev1.ConfigType_CONFIG_TYPE_ALLOY,
	}

	handler := &fakePipelineHandler{upsertResponse: expectedProto}
	client, cleanup := newConnectTestServer(t, handler)
	defer cleanup()

	req := &UpsertPipelineRequest{
		Pipeline: &Pipeline{
			Name:       "test-pipeline",
			Contents:   "test contents",
			Enabled:    true,
			ConfigType: configTypeAlloy,
		},
	}

	pipeline, err := client.UpsertPipeline(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, pipeline)
	assert.Equal(t, "pipeline-123", pipeline.ID)
	assert.Equal(t, "test-pipeline", pipeline.Name)
	assert.Equal(t, configTypeAlloy, pipeline.ConfigType)
	assert.True(t, pipeline.Enabled)

	// Outgoing request should have carried our fields.
	assert.NotNil(t, handler.captureRequest)
	assert.Equal(t, "test-pipeline", handler.captureRequest.GetPipeline().GetName())
	assert.Equal(t, pipelinev1.ConfigType_CONFIG_TYPE_ALLOY, handler.captureRequest.GetPipeline().GetConfigType())
}

// TestDeletePipeline_404IsSuccess preserves the historical contract that a
// 404 from DeletePipeline is treated as success at the client layer.
func TestDeletePipeline_404IsSuccess(t *testing.T) {
	handler := &fakePipelineHandler{
		deleteErr: connect.NewError(connect.CodeNotFound, errors.New("pipeline not found")),
	}
	client, cleanup := newConnectTestServer(t, handler)
	defer cleanup()

	err := client.DeletePipeline(context.Background(), "test-id")
	assert.NoError(t, err, "404 should be masked as success")
}

// TestDeletePipeline_ConnectErrors covers the non-404 error paths where the
// caller must see a FleetAPIError with the right status code and PipelineID.
func TestDeletePipeline_ConnectErrors(t *testing.T) {
	tests := []struct {
		name           string
		connectCode    connect.Code
		message        string
		wantStatusCode int
		wantTransient  bool
	}{
		{"Internal -> 500", connect.CodeInternal, "server error during deletion", http.StatusInternalServerError, true},
		{"Unauthenticated -> 401", connect.CodeUnauthenticated, "authentication failed", http.StatusUnauthorized, false},
		{"PermissionDenied -> 403", connect.CodePermissionDenied, "insufficient permissions", http.StatusForbidden, false},
		{"Unavailable -> 503", connect.CodeUnavailable, "service temporarily unavailable", http.StatusServiceUnavailable, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &fakePipelineHandler{
				deleteErr: connect.NewError(tt.connectCode, errors.New(tt.message)),
			}
			client, cleanup := newConnectTestServer(t, handler)
			defer cleanup()

			err := client.DeletePipeline(context.Background(), "test-pipeline-id-456")
			assert.Error(t, err)

			var fleetErr *FleetAPIError
			assert.True(t, errors.As(err, &fleetErr), "Error should be FleetAPIError")
			assert.Equal(t, tt.wantStatusCode, fleetErr.StatusCode)
			assert.Equal(t, "DeletePipeline", fleetErr.Operation)
			assert.Equal(t, tt.wantTransient, fleetErr.IsTransient())
			assert.Equal(t, "test-pipeline-id-456", fleetErr.PipelineID)
			assert.Contains(t, fleetErr.Error(), "test-pipeline-id-456", "Error message should contain PipelineID")
		})
	}
}

// TestDeletePipeline_Success confirms the happy path returns nil.
func TestDeletePipeline_Success(t *testing.T) {
	handler := &fakePipelineHandler{}
	client, cleanup := newConnectTestServer(t, handler)
	defer cleanup()

	err := client.DeletePipeline(context.Background(), "test-id")
	assert.NoError(t, err)
}

// TestNormalizeBaseURL exercises the backward-compat suffix stripping that
// lets existing Secrets (which include "/pipeline.v1.PipelineService/") keep
// working with the connect-go client constructors.
func TestNormalizeBaseURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare root", "https://api.example", "https://api.example"},
		{"trailing slash only", "https://api.example/", "https://api.example"},
		{"with pipeline service suffix", "https://api.example/pipeline.v1.PipelineService/", "https://api.example"},
		{"with pipeline service suffix no trailing slash", "https://api.example/pipeline.v1.PipelineService", "https://api.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, normalizeBaseURL(tt.in))
		})
	}
}
