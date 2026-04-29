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
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
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

// DeletePipelineRequest is the request to delete a pipeline
type DeletePipelineRequest struct {
	ID string `json:"id"`
}

// MockAPI is a mock Fleet Management API server
type MockAPI struct {
	pipelines sync.Map
	idCounter atomic.Int64
}

// NewMockAPI creates a new mock API server
func NewMockAPI() *MockAPI {
	m := &MockAPI{}
	m.idCounter.Store(1000) // Start IDs at 1000
	return m
}

func (m *MockAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Log all requests
	log.Printf("%s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)

	// Health check endpoint
	if r.Method == "GET" && r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	// All other endpoints require basic auth
	username, password, ok := r.BasicAuth()
	if !ok || username == "" || password == "" {
		log.Printf("Missing or invalid basic auth header")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		return
	}

	// Route based on path
	switch r.URL.Path {
	case "/pipeline.v1.PipelineService/UpsertPipeline":
		m.handleUpsertPipeline(w, r)
	case "/pipeline.v1.PipelineService/DeletePipeline":
		m.handleDeletePipeline(w, r)
	default:
		log.Printf("Unknown path: %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	}
}

func (m *MockAPI) handleUpsertPipeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req UpsertPipelineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("Failed to decode upsert request: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"error":"invalid request: %v"}`, err)))
		return
	}

	if req.Pipeline == nil {
		log.Printf("Missing pipeline in request")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"missing pipeline"}`))
		return
	}

	pipeline := req.Pipeline
	log.Printf("Upserting pipeline: name=%s, enabled=%v, configType=%s", pipeline.Name, pipeline.Enabled, pipeline.ConfigType)

	// Check if pipeline already exists by name
	var existingID string
	m.pipelines.Range(func(key, value interface{}) bool {
		p := value.(*Pipeline)
		if p.Name == pipeline.Name {
			existingID = p.ID
			return false // Stop iteration
		}
		return true
	})

	// Generate or reuse ID
	var id string
	now := time.Now().UTC()
	if existingID != "" {
		// Update existing pipeline - reuse ID
		id = existingID
		pipeline.ID = id
		pipeline.UpdatedAt = &now
		// Preserve CreatedAt from existing
		if existing, ok := m.pipelines.Load(id); ok {
			pipeline.CreatedAt = existing.(*Pipeline).CreatedAt
		}
		log.Printf("Updating existing pipeline with ID: %s", id)
	} else {
		// Create new pipeline - generate ID
		id = fmt.Sprintf("%d", m.idCounter.Add(1))
		pipeline.ID = id
		pipeline.CreatedAt = &now
		pipeline.UpdatedAt = &now
		log.Printf("Creating new pipeline with ID: %s", id)
	}

	// Store pipeline
	m.pipelines.Store(id, pipeline)

	// Return pipeline
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(pipeline); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

func (m *MockAPI) handleDeletePipeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req DeletePipelineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("Failed to decode delete request: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"error":"invalid request: %v"}`, err)))
		return
	}

	if req.ID == "" {
		log.Printf("Missing ID in delete request")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"missing id"}`))
		return
	}

	log.Printf("Deleting pipeline ID: %s", req.ID)

	// Delete from store (idempotent - no error if not found)
	m.pipelines.Delete(req.ID)

	// Always return success (idempotent delete)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	api := NewMockAPI()

	addr := fmt.Sprintf(":%s", port)
	log.Printf("Mock Fleet Management API starting on %s", addr)
	log.Printf("Endpoints:")
	log.Printf("  POST /pipeline.v1.PipelineService/UpsertPipeline")
	log.Printf("  POST /pipeline.v1.PipelineService/DeletePipeline")
	log.Printf("  GET  /healthz")

	certFile := os.Getenv("TLS_CERT_FILE")
	keyFile := os.Getenv("TLS_KEY_FILE")
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			log.Fatalf("TLS_CERT_FILE and TLS_KEY_FILE must both be set when enabling TLS")
		}
		if err := http.ListenAndServeTLS(addr, certFile, keyFile, api); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
		return
	}
	if err := http.ListenAndServe(addr, api); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
