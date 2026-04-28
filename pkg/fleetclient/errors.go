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
	"errors"
	"net/http"

	connect "connectrpc.com/connect"
)

// connectErrToFleetErr converts a connect-go error into a *FleetAPIError with
// an HTTP-style status code. Non-connect errors (network failures, context
// cancellation) are returned unchanged so the caller can identify them via
// errors.Is/errors.As.
//
// The HTTP status code mapping preserves the contract that the existing
// controller error classification (internal/controller/errors.go) relies on:
// 400 for validation errors, 404 for not-found, 429 for rate limits, 5xx for
// transient server failures.
func connectErrToFleetErr(err error, op, pipelineID string) error {
	if err == nil {
		return nil
	}

	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return err
	}

	return &FleetAPIError{
		StatusCode: connectCodeToHTTPStatus(connectErr.Code()),
		Operation:  op,
		Message:    connectErr.Message(),
		PipelineID: pipelineID,
		Wrapped:    err,
	}
}

// connectCodeToHTTPStatus maps a connect-go code to its canonical HTTP status,
// following the connect/gRPC -> HTTP convention documented at
// https://connectrpc.com/docs/protocol#error-codes.
func connectCodeToHTTPStatus(code connect.Code) int {
	switch code {
	case connect.CodeCanceled:
		return http.StatusRequestTimeout
	case connect.CodeUnknown:
		return http.StatusInternalServerError
	case connect.CodeInvalidArgument:
		return http.StatusBadRequest
	case connect.CodeDeadlineExceeded:
		return http.StatusRequestTimeout
	case connect.CodeNotFound:
		return http.StatusNotFound
	case connect.CodeAlreadyExists:
		return http.StatusConflict
	case connect.CodePermissionDenied:
		return http.StatusForbidden
	case connect.CodeResourceExhausted:
		return http.StatusTooManyRequests
	case connect.CodeFailedPrecondition:
		return http.StatusPreconditionFailed
	case connect.CodeAborted:
		return http.StatusConflict
	case connect.CodeOutOfRange:
		return http.StatusBadRequest
	case connect.CodeUnimplemented:
		return http.StatusNotImplemented
	case connect.CodeInternal:
		return http.StatusInternalServerError
	case connect.CodeUnavailable:
		return http.StatusServiceUnavailable
	case connect.CodeDataLoss:
		return http.StatusInternalServerError
	case connect.CodeUnauthenticated:
		return http.StatusUnauthorized
	default:
		return http.StatusInternalServerError
	}
}
