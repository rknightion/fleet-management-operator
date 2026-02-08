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
	"net"
	"net/http"
	"strings"

	"github.com/go-logr/logr"
	"github.com/grafana/fleet-management-operator/pkg/fleetclient"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// isTransientError determines if an error should be retried with exponential backoff
func isTransientError(err error) bool {
	// Check for FleetAPIError with IsTransient method
	var apiErr *fleetclient.FleetAPIError
	if errors.As(err, &apiErr) {
		return apiErr.IsTransient()
	}

	// Context cancellation is not transient
	if errors.Is(err, context.Canceled) {
		return false
	}

	// Network timeouts are transient
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// Default: treat unknown errors as transient (safe default)
	return true
}

// shouldRetry determines if reconciliation should retry after an error
func shouldRetry(err error, reason string) bool {
	// Validation errors should not retry
	if reason == reasonValidationError {
		return false
	}

	return isTransientError(err)
}

// formatConditionMessage maps errors to user-friendly status condition messages with troubleshooting hints
func formatConditionMessage(reason string, err error) string {
	// Check for specific wrapped error patterns first (before unwrapping with errors.As)
	errMsg := err.Error()

	// Special case: 404 after recreation attempt (wrapped error from handleAPIError)
	if strings.Contains(errMsg, "recreation failed") {
		return "Pipeline not found in Fleet Management and recreation failed. It may have been deleted externally. Verify pipeline name is unique and check controller logs."
	}

	// Check for FleetAPIError and provide specific guidance based on HTTP status
	var apiErr *fleetclient.FleetAPIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusBadRequest:
			return fmt.Sprintf("Configuration validation failed: %s. Review spec.contents for syntax errors.", apiErr.Message)
		case http.StatusUnauthorized:
			return "Authentication failed. Verify Fleet Management credentials in the referenced Secret."
		case http.StatusForbidden:
			return "Permission denied. Fleet Management access token requires pipeline:write permission."
		case http.StatusNotFound:
			return "Pipeline not found in Fleet Management. It may have been deleted externally."
		case http.StatusTooManyRequests:
			return "Rate limited by Fleet Management API. Retry will occur automatically after delay."
		case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
			return fmt.Sprintf("Fleet Management API unavailable (HTTP %d). Retry will occur automatically.", apiErr.StatusCode)
		default:
			return fmt.Sprintf("API error (HTTP %d): %s", apiErr.StatusCode, apiErr.Message)
		}
	}

	// Check for context timeout
	if errors.Is(err, context.DeadlineExceeded) {
		return "Connection timeout. Check network connectivity to Fleet Management API."
	}

	// Check for network timeout
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "Network timeout. Check Fleet Management API endpoint is reachable."
	}

	// Generic fallback
	return fmt.Sprintf("Sync failed: %v. Check controller logs for details.", err)
}

// loggerFor returns a resource-scoped logger with pipeline namespace and name
func loggerFor(ctx context.Context, ns, name string) logr.Logger {
	return logf.FromContext(ctx).WithValues(
		"namespace", ns,
		"name", name,
	)
}
