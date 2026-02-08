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
	"net"

	"github.com/grafana/fleet-management-operator/pkg/fleetclient"
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
