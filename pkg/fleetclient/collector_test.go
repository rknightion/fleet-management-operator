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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	collectorv1 "github.com/grafana/fleet-management-api/api/gen/proto/go/collector/v1"
	"github.com/grafana/fleet-management-api/api/gen/proto/go/collector/v1/collectorv1connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeCollectorHandler is the connect-go server-side stub used to drive the
// CollectorService client tests through the real wire protocol. It mirrors
// fakePipelineHandler in client_test.go.
type fakeCollectorHandler struct {
	collectorv1connect.UnimplementedCollectorServiceHandler

	getResponse    *collectorv1.Collector
	getErr         error
	updateResponse *collectorv1.Collector
	updateErr      error
	bulkUpdateErr  error
	listResponse   *collectorv1.Collectors
	listErr        error

	captureGetRequest        *collectorv1.GetCollectorRequest
	captureUpdateRequest     *collectorv1.UpdateCollectorRequest
	captureBulkUpdateRequest *collectorv1.BulkUpdateCollectorsRequest
	captureListRequest       *collectorv1.ListCollectorsRequest
}

func (h *fakeCollectorHandler) GetCollector(_ context.Context, req *connect.Request[collectorv1.GetCollectorRequest]) (*connect.Response[collectorv1.Collector], error) {
	h.captureGetRequest = req.Msg
	if h.getErr != nil {
		return nil, h.getErr
	}
	return connect.NewResponse(h.getResponse), nil
}

func (h *fakeCollectorHandler) UpdateCollector(_ context.Context, req *connect.Request[collectorv1.UpdateCollectorRequest]) (*connect.Response[collectorv1.Collector], error) {
	h.captureUpdateRequest = req.Msg
	if h.updateErr != nil {
		return nil, h.updateErr
	}
	return connect.NewResponse(h.updateResponse), nil
}

func (h *fakeCollectorHandler) BulkUpdateCollectors(_ context.Context, req *connect.Request[collectorv1.BulkUpdateCollectorsRequest]) (*connect.Response[collectorv1.BulkUpdateCollectorsResponse], error) {
	h.captureBulkUpdateRequest = req.Msg
	if h.bulkUpdateErr != nil {
		return nil, h.bulkUpdateErr
	}
	return connect.NewResponse(&collectorv1.BulkUpdateCollectorsResponse{}), nil
}

func (h *fakeCollectorHandler) ListCollectors(_ context.Context, req *connect.Request[collectorv1.ListCollectorsRequest]) (*connect.Response[collectorv1.Collectors], error) {
	h.captureListRequest = req.Msg
	if h.listErr != nil {
		return nil, h.listErr
	}
	return connect.NewResponse(h.listResponse), nil
}

// newCollectorTestServer wires the fake collector handler into an httptest
// server and returns a Client pointed at it.
func newCollectorTestServer(t *testing.T, h *fakeCollectorHandler) (*Client, func()) {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := collectorv1connect.NewCollectorServiceHandler(h)
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)

	client := NewClient(server.URL, "testuser", "testpass")
	return client, server.Close
}

// TestGetCollector_Success round-trips a fully-populated proto Collector
// through the client and asserts every wire-public field maps back correctly.
func TestGetCollector_Success(t *testing.T) {
	enabled := true
	createdAt := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 2, 20, 14, 30, 0, 0, time.UTC)
	markedInactiveAt := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)

	expectedProto := &collectorv1.Collector{
		Id:               "collector-abc",
		Name:             "test-collector",
		Enabled:          &enabled,
		RemoteAttributes: map[string]string{"env": "prod", "team": "core"},
		LocalAttributes:  map[string]string{"os": "linux"},
		CollectorType:    collectorv1.CollectorType_COLLECTOR_TYPE_ALLOY,
		CreatedAt:        timestamppb.New(createdAt),
		UpdatedAt:        timestamppb.New(updatedAt),
		MarkedInactiveAt: timestamppb.New(markedInactiveAt),
	}

	handler := &fakeCollectorHandler{getResponse: expectedProto}
	client, cleanup := newCollectorTestServer(t, handler)
	defer cleanup()

	collector, err := client.GetCollector(context.Background(), "collector-abc")
	assert.NoError(t, err)
	assert.NotNil(t, collector)

	assert.Equal(t, "collector-abc", collector.ID)
	assert.Equal(t, "test-collector", collector.Name)
	if assert.NotNil(t, collector.Enabled) {
		assert.True(t, *collector.Enabled)
	}
	assert.Equal(t, map[string]string{"env": "prod", "team": "core"}, collector.RemoteAttributes)
	assert.Equal(t, map[string]string{"os": "linux"}, collector.LocalAttributes)
	assert.Equal(t, "COLLECTOR_TYPE_ALLOY", collector.CollectorType)
	if assert.NotNil(t, collector.CreatedAt) {
		assert.True(t, collector.CreatedAt.Equal(createdAt))
	}
	if assert.NotNil(t, collector.UpdatedAt) {
		assert.True(t, collector.UpdatedAt.Equal(updatedAt))
	}
	if assert.NotNil(t, collector.MarkedInactiveAt) {
		assert.True(t, collector.MarkedInactiveAt.Equal(markedInactiveAt))
	}

	// The outgoing request should have carried the requested ID.
	assert.NotNil(t, handler.captureGetRequest)
	assert.Equal(t, "collector-abc", handler.captureGetRequest.GetId())
}

// TestGetCollector_ConnectErrors verifies connect codes map to FleetAPIError
// with the expected HTTP status codes and IsTransient classification.
func TestGetCollector_ConnectErrors(t *testing.T) {
	tests := []struct {
		name           string
		connectCode    connect.Code
		message        string
		wantStatusCode int
		wantTransient  bool
	}{
		{"NotFound -> 404", connect.CodeNotFound, "collector not found", http.StatusNotFound, false},
		{"InvalidArgument -> 400", connect.CodeInvalidArgument, "bad collector id", http.StatusBadRequest, false},
		{"ResourceExhausted -> 429", connect.CodeResourceExhausted, "rate limited", http.StatusTooManyRequests, true},
		{"Internal -> 500", connect.CodeInternal, "internal server error", http.StatusInternalServerError, true},
		{"Unavailable -> 503", connect.CodeUnavailable, "service unavailable", http.StatusServiceUnavailable, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &fakeCollectorHandler{
				getErr: connect.NewError(tt.connectCode, errors.New(tt.message)),
			}
			client, cleanup := newCollectorTestServer(t, handler)
			defer cleanup()

			collector, err := client.GetCollector(context.Background(), "collector-xyz")
			assert.Error(t, err)
			assert.Nil(t, collector)

			var fleetErr *FleetAPIError
			assert.True(t, errors.As(err, &fleetErr), "Error should be FleetAPIError")
			assert.Equal(t, tt.wantStatusCode, fleetErr.StatusCode, "StatusCode mismatch")
			assert.Equal(t, "GetCollector", fleetErr.Operation)
			assert.Equal(t, tt.wantTransient, fleetErr.IsTransient(), "IsTransient mismatch")
			assert.Equal(t, "collector-xyz", fleetErr.PipelineID, "Collector id should populate PipelineID slot")
			assert.Contains(t, fleetErr.Message, tt.message)
		})
	}
}

// TestUpdateCollector_Success verifies request-body fields propagate through
// the proto conversion and the response decodes back to wire-public form.
func TestUpdateCollector_Success(t *testing.T) {
	enabled := false
	respProto := &collectorv1.Collector{
		Id:               "collector-1",
		Name:             "renamed",
		Enabled:          &enabled,
		RemoteAttributes: map[string]string{"env": "staging"},
		CollectorType:    collectorv1.CollectorType_COLLECTOR_TYPE_OTEL,
	}

	handler := &fakeCollectorHandler{updateResponse: respProto}
	client, cleanup := newCollectorTestServer(t, handler)
	defer cleanup()

	enabledIn := false
	in := &Collector{
		ID:               "collector-1",
		Name:             "renamed",
		Enabled:          &enabledIn,
		RemoteAttributes: map[string]string{"env": "staging"},
		LocalAttributes:  map[string]string{"host": "node-1"},
		CollectorType:    "COLLECTOR_TYPE_OTEL",
	}

	out, err := client.UpdateCollector(context.Background(), in)
	assert.NoError(t, err)
	assert.NotNil(t, out)
	assert.Equal(t, "collector-1", out.ID)
	assert.Equal(t, "renamed", out.Name)
	if assert.NotNil(t, out.Enabled) {
		assert.False(t, *out.Enabled)
	}
	assert.Equal(t, "COLLECTOR_TYPE_OTEL", out.CollectorType)

	// Request body fields propagated correctly.
	assert.NotNil(t, handler.captureUpdateRequest)
	captured := handler.captureUpdateRequest.GetCollector()
	if assert.NotNil(t, captured) {
		assert.Equal(t, "collector-1", captured.GetId())
		assert.Equal(t, "renamed", captured.GetName())
		if assert.NotNil(t, captured.Enabled) {
			assert.False(t, *captured.Enabled)
		}
		assert.Equal(t, map[string]string{"env": "staging"}, captured.GetRemoteAttributes())
		assert.Equal(t, map[string]string{"host": "node-1"}, captured.GetLocalAttributes())
		assert.Equal(t, collectorv1.CollectorType_COLLECTOR_TYPE_OTEL, captured.GetCollectorType())
	}
}

// TestBulkUpdateCollectors_Operations exercises a sequence of ADD + REPLACE +
// REMOVE operations against multiple collector IDs and verifies the entire
// payload reaches the server intact.
func TestBulkUpdateCollectors_Operations(t *testing.T) {
	handler := &fakeCollectorHandler{}
	client, cleanup := newCollectorTestServer(t, handler)
	defer cleanup()

	ids := []string{"c1", "c2", "c3"}
	ops := []*Operation{
		{Op: OpAdd, Path: "/remote_attributes/env", Value: "prod"},
		{Op: OpReplace, Path: "/remote_attributes/team", Value: "platform", OldValue: "core"},
		{Op: OpRemove, Path: "/remote_attributes/legacy"},
	}

	err := client.BulkUpdateCollectors(context.Background(), ids, ops)
	assert.NoError(t, err)

	if assert.NotNil(t, handler.captureBulkUpdateRequest) {
		assert.Equal(t, ids, handler.captureBulkUpdateRequest.GetIds())

		gotOps := handler.captureBulkUpdateRequest.GetOps()
		if assert.Len(t, gotOps, 3) {
			// ADD
			assert.Equal(t, collectorv1.Operation_ADD, gotOps[0].GetOp())
			assert.Equal(t, "/remote_attributes/env", gotOps[0].GetPath())
			assert.Equal(t, "prod", gotOps[0].GetValue())

			// REPLACE
			assert.Equal(t, collectorv1.Operation_REPLACE, gotOps[1].GetOp())
			assert.Equal(t, "/remote_attributes/team", gotOps[1].GetPath())
			assert.Equal(t, "platform", gotOps[1].GetValue())
			assert.Equal(t, "core", gotOps[1].GetOldValue())

			// REMOVE - Value should be unset (nil pointer) when the caller
			// didn't supply one. GetValue will return "" for the unset case,
			// but the underlying Value pointer must be nil.
			assert.Equal(t, collectorv1.Operation_REMOVE, gotOps[2].GetOp())
			assert.Equal(t, "/remote_attributes/legacy", gotOps[2].GetPath())
			assert.Nil(t, gotOps[2].Value, "REMOVE without explicit value should leave Value unset")
		}
	}
}

func TestOperationToProto_PreservesExplicitEmptyValueForWriteOps(t *testing.T) {
	add := operationToProto(&Operation{Op: OpAdd, Path: "/remote_attributes/empty", Value: ""})
	require.NotNil(t, add.Value, "ADD must preserve explicit empty string value")
	assert.Equal(t, "", add.GetValue())

	replace := operationToProto(&Operation{Op: OpReplace, Path: "/remote_attributes/empty", Value: ""})
	require.NotNil(t, replace.Value, "REPLACE must preserve explicit empty string value")
	assert.Equal(t, "", replace.GetValue())

	remove := operationToProto(&Operation{Op: OpRemove, Path: "/remote_attributes/empty"})
	assert.Nil(t, remove.Value, "REMOVE without explicit value should leave Value unset")
}

// TestBulkUpdateCollectors_Errors covers an error path through the bulk RPC.
func TestBulkUpdateCollectors_Errors(t *testing.T) {
	tests := []struct {
		name           string
		connectCode    connect.Code
		message        string
		wantStatusCode int
		wantTransient  bool
	}{
		{"InvalidArgument -> 400", connect.CodeInvalidArgument, "invalid op path", http.StatusBadRequest, false},
		{"NotFound -> 404", connect.CodeNotFound, "collector missing", http.StatusNotFound, false},
		{"Internal -> 500", connect.CodeInternal, "boom", http.StatusInternalServerError, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &fakeCollectorHandler{
				bulkUpdateErr: connect.NewError(tt.connectCode, errors.New(tt.message)),
			}
			client, cleanup := newCollectorTestServer(t, handler)
			defer cleanup()

			err := client.BulkUpdateCollectors(
				context.Background(),
				[]string{"c1"},
				[]*Operation{{Op: OpAdd, Path: "/remote_attributes/x", Value: "y"}},
			)
			assert.Error(t, err)

			var fleetErr *FleetAPIError
			assert.True(t, errors.As(err, &fleetErr), "Error should be FleetAPIError")
			assert.Equal(t, tt.wantStatusCode, fleetErr.StatusCode)
			assert.Equal(t, "BulkUpdateCollectors", fleetErr.Operation)
			assert.Equal(t, tt.wantTransient, fleetErr.IsTransient())
			assert.Contains(t, fleetErr.Message, tt.message)
		})
	}
}

// TestListCollectors_WithMatchers verifies that matchers propagate to the
// outgoing request and that multiple collectors in the response decode
// correctly.
func TestListCollectors_WithMatchers(t *testing.T) {
	enabled1 := true
	enabled2 := false
	respProto := &collectorv1.Collectors{
		Collectors: []*collectorv1.Collector{
			{
				Id:            "c1",
				Name:          "alpha",
				Enabled:       &enabled1,
				CollectorType: collectorv1.CollectorType_COLLECTOR_TYPE_ALLOY,
			},
			{
				Id:            "c2",
				Name:          "beta",
				Enabled:       &enabled2,
				CollectorType: collectorv1.CollectorType_COLLECTOR_TYPE_OTEL,
			},
			{
				Id:   "c3",
				Name: "gamma",
				// No Enabled, no CollectorType -> wire-public should leave
				// Enabled nil and CollectorType "".
			},
		},
	}

	handler := &fakeCollectorHandler{listResponse: respProto}
	client, cleanup := newCollectorTestServer(t, handler)
	defer cleanup()

	matchers := []string{`collector.os="linux"`, `team!="team-a"`}
	collectors, err := client.ListCollectors(context.Background(), matchers)
	assert.NoError(t, err)
	assert.Len(t, collectors, 3)

	assert.Equal(t, "c1", collectors[0].ID)
	assert.Equal(t, "alpha", collectors[0].Name)
	if assert.NotNil(t, collectors[0].Enabled) {
		assert.True(t, *collectors[0].Enabled)
	}
	assert.Equal(t, "COLLECTOR_TYPE_ALLOY", collectors[0].CollectorType)

	assert.Equal(t, "c2", collectors[1].ID)
	if assert.NotNil(t, collectors[1].Enabled) {
		assert.False(t, *collectors[1].Enabled)
	}
	assert.Equal(t, "COLLECTOR_TYPE_OTEL", collectors[1].CollectorType)

	assert.Equal(t, "c3", collectors[2].ID)
	assert.Nil(t, collectors[2].Enabled, "Enabled should remain nil when proto leaves it unset")
	assert.Equal(t, "", collectors[2].CollectorType)

	// Matchers propagated.
	if assert.NotNil(t, handler.captureListRequest) {
		assert.Equal(t, matchers, handler.captureListRequest.GetMatchers())
	}
}

// TestNormalizeBaseURL_Collector extends the suffix-stripping coverage to
// confirm a baseURL ending in the CollectorService suffix is also normalized
// to the bare server root.
func TestNormalizeBaseURL_Collector(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"with collector service suffix", "https://api.example/collector.v1.CollectorService/", "https://api.example"},
		{"with collector service suffix no trailing slash", "https://api.example/collector.v1.CollectorService", "https://api.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, normalizeBaseURL(tt.in))
		})
	}
}
