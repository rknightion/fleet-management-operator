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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

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

// TestUpsertPipeline_HTTPClientErrors tests HTTP client error paths with various status codes
func TestUpsertPipeline_HTTPClientErrors(t *testing.T) {
	tests := []struct {
		name               string
		statusCode         int
		responseBody       string
		expectError        bool
		expectedStatusCode int
		expectedOperation  string
		expectedIsTransient bool
		messageContains    string
	}{
		{
			name:                "400 Bad Request",
			statusCode:          http.StatusBadRequest,
			responseBody:        "invalid pipeline configuration",
			expectError:         true,
			expectedStatusCode:  http.StatusBadRequest,
			expectedOperation:   "UpsertPipeline",
			expectedIsTransient: false,
			messageContains:     "invalid pipeline configuration",
		},
		{
			name:                "401 Unauthorized",
			statusCode:          http.StatusUnauthorized,
			responseBody:        "authentication failed",
			expectError:         true,
			expectedStatusCode:  http.StatusUnauthorized,
			expectedOperation:   "UpsertPipeline",
			expectedIsTransient: false,
			messageContains:     "authentication failed",
		},
		{
			name:                "429 Too Many Requests",
			statusCode:          http.StatusTooManyRequests,
			responseBody:        "rate limit exceeded",
			expectError:         true,
			expectedStatusCode:  http.StatusTooManyRequests,
			expectedOperation:   "UpsertPipeline",
			expectedIsTransient: true,
			messageContains:     "rate limit exceeded",
		},
		{
			name:                "500 Internal Server Error",
			statusCode:          http.StatusInternalServerError,
			responseBody:        "internal error occurred",
			expectError:         true,
			expectedStatusCode:  http.StatusInternalServerError,
			expectedOperation:   "UpsertPipeline",
			expectedIsTransient: true,
			messageContains:     "internal error occurred",
		},
		{
			name:                "503 Service Unavailable",
			statusCode:          http.StatusServiceUnavailable,
			responseBody:        "service temporarily unavailable",
			expectError:         true,
			expectedStatusCode:  http.StatusServiceUnavailable,
			expectedOperation:   "UpsertPipeline",
			expectedIsTransient: true,
			messageContains:     "service temporarily unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "POST", r.Method, "Expected POST method")
				assert.Contains(t, r.URL.Path, "UpsertPipeline", "Expected UpsertPipeline endpoint")

				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			client := NewClient(server.URL+"/", "testuser", "testpass")
			req := &UpsertPipelineRequest{
				Pipeline: &Pipeline{
					Name:     "test-pipeline",
					Contents: "test contents",
					Enabled:  true,
				},
			}

			pipeline, err := client.UpsertPipeline(context.Background(), req)

			if tt.expectError {
				assert.Error(t, err, "Expected error for status code %d", tt.statusCode)
				assert.Nil(t, pipeline, "Pipeline should be nil on error")

				var fleetErr *FleetAPIError
				found := errors.As(err, &fleetErr)
				assert.True(t, found, "Error should be FleetAPIError")
				assert.Equal(t, tt.expectedStatusCode, fleetErr.StatusCode, "StatusCode mismatch")
				assert.Equal(t, tt.expectedOperation, fleetErr.Operation, "Operation mismatch")
				assert.Equal(t, tt.expectedIsTransient, fleetErr.IsTransient(), "IsTransient mismatch")
				assert.Contains(t, fleetErr.Message, tt.messageContains, "Message should contain expected text")
			} else {
				assert.NoError(t, err, "Expected no error")
				assert.NotNil(t, pipeline, "Pipeline should not be nil")
			}
		})
	}
}

// TestUpsertPipeline_Success tests successful UpsertPipeline response
func TestUpsertPipeline_Success(t *testing.T) {
	expectedPipeline := &Pipeline{
		Name:       "test-pipeline",
		Contents:   "test contents",
		Enabled:    true,
		ID:         "pipeline-123",
		ConfigType: "Alloy",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method, "Expected POST method")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(expectedPipeline)
	}))
	defer server.Close()

	client := NewClient(server.URL+"/", "testuser", "testpass")
	req := &UpsertPipelineRequest{
		Pipeline: &Pipeline{
			Name:     "test-pipeline",
			Contents: "test contents",
			Enabled:  true,
		},
	}

	pipeline, err := client.UpsertPipeline(context.Background(), req)

	assert.NoError(t, err, "Expected no error on success")
	assert.NotNil(t, pipeline, "Pipeline should not be nil")
	assert.Equal(t, expectedPipeline.ID, pipeline.ID, "Pipeline ID should match")
	assert.Equal(t, expectedPipeline.Name, pipeline.Name, "Pipeline Name should match")
	assert.Equal(t, expectedPipeline.ConfigType, pipeline.ConfigType, "ConfigType should match")
}

// TestDeletePipeline_HTTPClientErrors tests DeletePipeline error paths
func TestDeletePipeline_HTTPClientErrors(t *testing.T) {
	tests := []struct {
		name               string
		statusCode         int
		responseBody       string
		expectError        bool
		expectedStatusCode int
		expectedOperation  string
		expectedIsTransient bool
		pipelineIDSet      bool
	}{
		{
			name:                "404 Not Found (success case)",
			statusCode:          http.StatusNotFound,
			responseBody:        "pipeline not found",
			expectError:         false,
			expectedStatusCode:  0,
			expectedOperation:   "",
			expectedIsTransient: false,
			pipelineIDSet:       false,
		},
		{
			name:                "200 OK (success case)",
			statusCode:          http.StatusOK,
			responseBody:        "deleted",
			expectError:         false,
			expectedStatusCode:  0,
			expectedOperation:   "",
			expectedIsTransient: false,
			pipelineIDSet:       false,
		},
		{
			name:                "500 Internal Server Error",
			statusCode:          http.StatusInternalServerError,
			responseBody:        "server error during deletion",
			expectError:         true,
			expectedStatusCode:  http.StatusInternalServerError,
			expectedOperation:   "DeletePipeline",
			expectedIsTransient: true,
			pipelineIDSet:       true,
		},
		{
			name:                "401 Unauthorized",
			statusCode:          http.StatusUnauthorized,
			responseBody:        "authentication failed",
			expectError:         true,
			expectedStatusCode:  http.StatusUnauthorized,
			expectedOperation:   "DeletePipeline",
			expectedIsTransient: false,
			pipelineIDSet:       true,
		},
		{
			name:                "403 Forbidden",
			statusCode:          http.StatusForbidden,
			responseBody:        "insufficient permissions",
			expectError:         true,
			expectedStatusCode:  http.StatusForbidden,
			expectedOperation:   "DeletePipeline",
			expectedIsTransient: false,
			pipelineIDSet:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "POST", r.Method, "Expected POST method")
				assert.Contains(t, r.URL.Path, "DeletePipeline", "Expected DeletePipeline endpoint")

				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			client := NewClient(server.URL+"/", "testuser", "testpass")
			testPipelineID := "test-pipeline-id-456"

			err := client.DeletePipeline(context.Background(), testPipelineID)

			if tt.expectError {
				assert.Error(t, err, "Expected error for status code %d", tt.statusCode)

				var fleetErr *FleetAPIError
				found := errors.As(err, &fleetErr)
				assert.True(t, found, "Error should be FleetAPIError")
				assert.Equal(t, tt.expectedStatusCode, fleetErr.StatusCode, "StatusCode mismatch")
				assert.Equal(t, tt.expectedOperation, fleetErr.Operation, "Operation mismatch")
				assert.Equal(t, tt.expectedIsTransient, fleetErr.IsTransient(), "IsTransient mismatch")

				if tt.pipelineIDSet {
					assert.Equal(t, testPipelineID, fleetErr.PipelineID, "PipelineID should be set")
					assert.Contains(t, fleetErr.Error(), testPipelineID, "Error message should contain PipelineID")
				}
			} else {
				assert.NoError(t, err, "Expected no error for status code %d", tt.statusCode)
			}
		})
	}
}
