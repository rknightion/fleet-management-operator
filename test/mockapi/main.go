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

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"

	"connectrpc.com/connect"
	pipelinev1 "github.com/grafana/fleet-management-api/api/gen/proto/go/pipeline/v1"
	"github.com/grafana/fleet-management-api/api/gen/proto/go/pipeline/v1/pipelinev1connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// MockAPI is a connect-go compatible mock Fleet Management API server.
type MockAPI struct {
	pipelinev1connect.UnimplementedPipelineServiceHandler

	pipelines sync.Map
	idCounter atomic.Int64
}

// NewMockAPI creates a new mock API server.
func NewMockAPI() *MockAPI {
	m := &MockAPI{}
	m.idCounter.Store(1000)
	return m
}

// UpsertPipeline creates or updates a pipeline by name and returns the server view.
func (m *MockAPI) UpsertPipeline(
	_ context.Context,
	req *connect.Request[pipelinev1.UpsertPipelineRequest],
) (*connect.Response[pipelinev1.Pipeline], error) {
	in := req.Msg.GetPipeline()
	if in == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("missing pipeline"))
	}

	pipeline := proto.Clone(in).(*pipelinev1.Pipeline)
	log.Printf("Upserting pipeline: name=%s, enabled=%v, configType=%s",
		pipeline.GetName(), pipeline.GetEnabled(), pipeline.GetConfigType().String())

	var existing *pipelinev1.Pipeline
	m.pipelines.Range(func(_, value any) bool {
		p := value.(*pipelinev1.Pipeline)
		if p.GetName() == pipeline.GetName() {
			existing = p
			return false
		}
		return true
	})

	now := timestamppb.Now()
	if existing != nil {
		id := existing.GetId()
		pipeline.Id = &id
		if existing.GetCreatedAt() != nil {
			pipeline.CreatedAt = existing.GetCreatedAt()
		} else {
			pipeline.CreatedAt = now
		}
		pipeline.UpdatedAt = now
		log.Printf("Updating existing pipeline with ID: %s", id)
	} else {
		id := fmt.Sprintf("%d", m.idCounter.Add(1))
		pipeline.Id = &id
		pipeline.CreatedAt = now
		pipeline.UpdatedAt = now
		log.Printf("Creating new pipeline with ID: %s", id)
	}

	if !req.Msg.GetValidateOnly() {
		m.pipelines.Store(pipeline.GetId(), proto.Clone(pipeline).(*pipelinev1.Pipeline))
	}

	return connect.NewResponse(pipeline), nil
}

// GetPipeline returns a stored pipeline by ID.
func (m *MockAPI) GetPipeline(
	_ context.Context,
	req *connect.Request[pipelinev1.GetPipelineRequest],
) (*connect.Response[pipelinev1.Pipeline], error) {
	if req.Msg.GetId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("missing id"))
	}

	value, ok := m.pipelines.Load(req.Msg.GetId())
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("pipeline not found"))
	}
	return connect.NewResponse(proto.Clone(value.(*pipelinev1.Pipeline)).(*pipelinev1.Pipeline)), nil
}

// DeletePipeline removes a pipeline by ID. It is idempotent.
func (m *MockAPI) DeletePipeline(
	_ context.Context,
	req *connect.Request[pipelinev1.DeletePipelineRequest],
) (*connect.Response[pipelinev1.DeletePipelineResponse], error) {
	if req.Msg.GetId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("missing id"))
	}

	log.Printf("Deleting pipeline ID: %s", req.Msg.GetId())
	m.pipelines.Delete(req.Msg.GetId())
	return connect.NewResponse(&pipelinev1.DeletePipelineResponse{}), nil
}

// ListPipelines returns all stored pipelines, with the filters used by the operator.
func (m *MockAPI) ListPipelines(
	_ context.Context,
	req *connect.Request[pipelinev1.ListPipelinesRequest],
) (*connect.Response[pipelinev1.Pipelines], error) {
	out := &pipelinev1.Pipelines{}
	m.pipelines.Range(func(_, value any) bool {
		p := value.(*pipelinev1.Pipeline)
		if req.Msg.ConfigType != nil && p.GetConfigType() != req.Msg.GetConfigType() {
			return true
		}
		if req.Msg.Enabled != nil && p.GetEnabled() != req.Msg.GetEnabled() {
			return true
		}
		out.Pipelines = append(out.Pipelines, proto.Clone(p).(*pipelinev1.Pipeline))
		return true
	})
	return connect.NewResponse(out), nil
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	api := NewMockAPI()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	path, handler := pipelinev1connect.NewPipelineServiceHandler(api)
	mux.Handle(path, logRequests(handler))

	addr := fmt.Sprintf(":%s", port)
	log.Printf("Mock Fleet Management API starting on %s", addr)
	log.Printf("Endpoints:")
	log.Printf("  %s", pipelinev1connect.PipelineServiceGetPipelineProcedure)
	log.Printf("  %s", pipelinev1connect.PipelineServiceUpsertPipelineProcedure)
	log.Printf("  %s", pipelinev1connect.PipelineServiceDeletePipelineProcedure)
	log.Printf("  %s", pipelinev1connect.PipelineServiceListPipelinesProcedure)
	log.Printf("  GET  /healthz")

	certFile := os.Getenv("TLS_CERT_FILE")
	keyFile := os.Getenv("TLS_KEY_FILE")
	if certFile != "" && keyFile != "" {
		log.Printf("Starting HTTPS server with cert=%s key=%s", certFile, keyFile)
		if err := http.ListenAndServeTLS(addr, certFile, keyFile, mux); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}
