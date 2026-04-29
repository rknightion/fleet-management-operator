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
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestNoCacheBypassingListCalls verifies that the controller does not use List() operations
// in the reconciliation code, which would either bypass the cache (if using a direct client)
// or load all resources into memory unnecessarily.
//
// This test parses the pipeline_controller.go AST to detect any List() method calls.
func TestNoCacheBypassingListCalls(t *testing.T) {
	// Parse the controller source file
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "pipeline_controller.go", nil, parser.ParseComments)
	assert.NoError(t, err, "failed to parse pipeline_controller.go")

	// Track List() calls found
	listCalls := []string{}

	// Walk the AST looking for List() method calls
	ast.Inspect(node, func(n ast.Node) bool {
		// Look for CallExpr nodes (function/method calls)
		callExpr, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		// Check if it's a method call (SelectorExpr)
		selExpr, ok := callExpr.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		// Check if the method name is "List"
		if selExpr.Sel.Name == "List" {
			// Record the position for debugging
			position := fset.Position(selExpr.Pos())
			listCalls = append(listCalls, position.String())
		}

		return true
	})

	// Assert no List() calls were found
	assert.Empty(t, listCalls, "found List() calls in controller reconciliation code at: %v. "+
		"List() operations should be avoided in reconciliation because they either bypass the cache "+
		"or load all resources into memory unnecessarily. Use single-resource Get() instead.", listCalls)
}

// TestReconcilerUsesManagerClient verifies that the PipelineReconciler embeds client.Client
// interface type, which is satisfied by mgr.GetClient() (a cached client).
//
// The integration test in suite_test.go (lines 99-100) verifies that mgr.GetClient() is
// actually assigned at runtime.
func TestReconcilerUsesManagerClient(t *testing.T) {
	// Use reflection to verify PipelineReconciler has a Client field of type client.Client
	reconcilerType := reflect.TypeFor[PipelineReconciler]()

	// Find the Client field
	clientField, found := reconcilerType.FieldByName("Client")
	assert.True(t, found, "PipelineReconciler must have a Client field")

	// Verify it's the client.Client interface type
	expectedType := reflect.TypeFor[client.Client]()
	assert.Equal(t, expectedType, clientField.Type,
		"PipelineReconciler.Client must be of type client.Client interface. "+
			"This ensures the controller uses the cached client from mgr.GetClient().")

	// Note: The actual assignment of mgr.GetClient() to reconciler.Client is verified
	// in the integration test suite_test.go lines 99-100 where the controller is created.
	t.Log("PipelineReconciler.Client type verified. Runtime assignment verified in suite_test.go:99-100")
}

// TestCacheAuditDocumentation verifies that the cache usage documentation comments
// added in Task 1 are present in the controller code. This ensures the documentation
// is not accidentally removed during refactoring.
func TestCacheAuditDocumentation(t *testing.T) {
	// Read the controller source file
	content, err := os.ReadFile("pipeline_controller.go")
	assert.NoError(t, err, "failed to read pipeline_controller.go")

	fileContent := string(content)

	// Define expected documentation markers
	expectedMarkers := []struct {
		marker      string
		description string
	}{
		{
			marker:      "Cache Usage Audit:",
			description: "package-level cache usage audit summary",
		},
		{
			marker:      "Cache: This Get() reads from the informer cache",
			description: "Get() operation cache documentation in Reconcile()",
		},
		{
			marker:      "Cache: Update() writes directly to the API server",
			description: "Update() operation cache documentation for finalizer addition",
		},
		{
			marker:      "Cache: Update() writes directly to the API server. Once finalizer is removed",
			description: "Update() operation cache documentation for finalizer removal",
		},
		{
			marker:      "Cache: Status().Update() writes directly to the API server status subresource",
			description: "Status().Update() cache documentation in updateStatusSuccess()",
		},
		{
			marker:      "Cache: For(&Pipeline{}) establishes the informer watch",
			description: "SetupWithManager() informer watch documentation",
		},
	}

	// Verify each marker is present
	for _, expected := range expectedMarkers {
		assert.Contains(t, fileContent, expected.marker,
			"missing cache documentation: %s. This comment documents cache behavior for auditing purposes.",
			expected.description)
	}

	// Count total "Cache:" markers (should be at least 6 from the markers above, plus potentially
	// one more in updateStatusError)
	cacheMarkerCount := strings.Count(fileContent, "Cache:")
	assert.GreaterOrEqual(t, cacheMarkerCount, 6,
		"expected at least 6 'Cache:' documentation comments in pipeline_controller.go")

	t.Logf("Found %d cache documentation markers in pipeline_controller.go", cacheMarkerCount)
}
