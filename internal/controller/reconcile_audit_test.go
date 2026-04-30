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
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestStatusUpdatesUseSubresource verifies RECON-02: All status updates use Status().Update()
// and never r.Update() on status fields.
//
// This test parses pipeline_controller.go AST to ensure:
// - Exactly 3 Status().Update() calls exist (success, read-only, and error paths)
// - Exactly 2 r.Update() calls exist (finalizer add and remove only)
// - No additional r.Update() calls that might incorrectly modify status
func TestStatusUpdatesUseSubresource(t *testing.T) {
	// Parse the controller source file
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "pipeline_controller.go", nil, parser.ParseComments)
	assert.NoError(t, err, "failed to parse pipeline_controller.go")

	statusUpdateCount := 0
	directUpdateCount := 0
	updateCallPositions := []string{}

	// Walk the AST looking for Update() method calls
	ast.Inspect(node, func(n ast.Node) bool {
		callExpr, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		// Check if it's a method call (SelectorExpr)
		selExpr, ok := callExpr.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		// Check if the method name is "Update"
		if selExpr.Sel.Name == "Update" {
			position := fset.Position(selExpr.Pos())

			// Check if the receiver is Status() - indicates r.Status().Update()
			if innerSel, ok := selExpr.X.(*ast.CallExpr); ok {
				if innerSelExpr, ok := innerSel.Fun.(*ast.SelectorExpr); ok {
					if innerSelExpr.Sel.Name == "Status" {
						statusUpdateCount++
						updateCallPositions = append(updateCallPositions,
							position.String()+" [Status().Update()]")
					}
				}
			} else {
				// Direct r.Update() call
				directUpdateCount++
				updateCallPositions = append(updateCallPositions,
					position.String()+" [r.Update()]")
			}
		}

		return true
	})

	// Assert exactly 3 Status().Update() calls (success, read-only, and error paths)
	assert.Equal(t, 3, statusUpdateCount,
		"RECON-02 violation: Expected exactly 3 Status().Update() calls (success, read-only, and error paths), found %d. "+
			"All Update() calls: %v",
		statusUpdateCount, updateCallPositions)

	// Assert exactly 2 r.Update() calls (finalizer add and remove)
	assert.Equal(t, 2, directUpdateCount,
		"RECON-02 violation: Expected exactly 2 r.Update() calls (finalizer operations only), found %d. "+
			"All Update() calls: %v. "+
			"Status updates must use Status().Update() to avoid triggering spec-change watch events.",
		directUpdateCount, updateCallPositions)

	t.Logf("RECON-02 verified: Found %d Status().Update() calls and %d r.Update() calls (finalizer only)",
		statusUpdateCount, directUpdateCount)
}

// TestNoRedundantGetAfterUpsert verifies RECON-04: No redundant Get() after UpsertPipeline.
//
// UpsertPipeline returns the full Pipeline object, so there's no need to call GetPipeline.
// This test ensures the return value is used and no Get() follows UpsertPipeline in the same function.
func TestNoRedundantGetAfterUpsert(t *testing.T) {
	// Parse the controller source file
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "pipeline_controller.go", nil, parser.ParseComments)
	assert.NoError(t, err, "failed to parse pipeline_controller.go")

	violations := []string{}

	// Walk the AST to find functions that call UpsertPipeline
	ast.Inspect(node, func(n ast.Node) bool {
		funcDecl, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}

		// Track if we've seen UpsertPipeline in this function
		seenUpsert := false
		upsertAssigned := false

		// Inspect function body
		ast.Inspect(funcDecl.Body, func(fn ast.Node) bool {
			switch node := fn.(type) {
			case *ast.AssignStmt:
				// Check if RHS is UpsertPipeline call
				for _, rhs := range node.Rhs {
					if callExpr, ok := rhs.(*ast.CallExpr); ok {
						if selExpr, ok := callExpr.Fun.(*ast.SelectorExpr); ok {
							if selExpr.Sel.Name == "UpsertPipeline" {
								seenUpsert = true
								// Check if assigned to a variable (not discarded)
								if len(node.Lhs) > 0 {
									upsertAssigned = true
								}
							}
						}
					}
				}

			case *ast.CallExpr:
				// If we've seen UpsertPipeline, check for Get() calls after it
				if seenUpsert {
					if selExpr, ok := node.Fun.(*ast.SelectorExpr); ok {
						if selExpr.Sel.Name == "Get" {
							// Check if it's r.Get (K8s API call)
							if ident, ok := selExpr.X.(*ast.Ident); ok {
								if ident.Name == "r" {
									position := fset.Position(node.Pos())
									violations = append(violations,
										fmt.Sprintf("%s: r.Get() found after UpsertPipeline in %s()",
											position, funcDecl.Name.Name))
								}
							}
						}
					}
				}
			}
			return true
		})

		// Check if UpsertPipeline return value is assigned
		if seenUpsert && !upsertAssigned {
			violations = append(violations,
				fmt.Sprintf("%s(): UpsertPipeline return value not assigned",
					funcDecl.Name.Name))
		}

		return true
	})

	assert.Empty(t, violations,
		"RECON-04 violation: Redundant Get() found after UpsertPipeline. "+
			"UpsertPipeline returns the full Pipeline object; no additional Get() is needed. "+
			"Violations: %v", violations)

	t.Log("RECON-04 verified: No redundant Get() calls after UpsertPipeline")
}

// TestObservedGenerationGuard verifies RECON-03: ObservedGeneration pattern is correctly implemented.
//
// The controller must:
// 1. Check pipeline.Status.ObservedGeneration == pipeline.Generation to skip unnecessary reconciliation
// 2. Set ObservedGeneration in both success and error paths
// 3. Place the check AFTER finalizer addition but BEFORE reconcileNormal
func TestObservedGenerationGuard(t *testing.T) {
	// Read the controller source file
	content, err := os.ReadFile("pipeline_controller.go")
	assert.NoError(t, err, "failed to read pipeline_controller.go")

	fileContent := string(content)

	// 1. Verify ObservedGeneration check exists in Reconcile()
	observedGenCheck := "pipeline.Status.ObservedGeneration == pipeline.Generation"
	assert.Contains(t, fileContent, observedGenCheck,
		"RECON-03 violation: Missing ObservedGeneration check in Reconcile(). "+
			"This guard prevents unnecessary reconciliation when spec hasn't changed.")

	// 2. Verify the check is positioned correctly (after finalizer, before reconcileNormal)
	// Extract the Reconcile function to check ordering
	reconcileStart := strings.Index(fileContent, "func (r *PipelineReconciler) Reconcile(")
	assert.Greater(t, reconcileStart, 0, "Could not find Reconcile() function")

	reconcileEnd := strings.Index(fileContent[reconcileStart:], "\n}\n")
	reconcileBody := fileContent[reconcileStart : reconcileStart+reconcileEnd]

	finalizerIndex := strings.Index(reconcileBody, "ContainsFinalizer(pipeline, pipelineFinalizer)")
	observedGenIndex := strings.Index(reconcileBody, observedGenCheck)
	// B5 added a *string outcome out-parameter to reconcileNormal so the
	// deferred metric counter can record the precise terminal reason. Match
	// the open-paren prefix to stay tolerant of trailing arguments.
	reconcileNormalIndex := strings.Index(reconcileBody, "reconcileNormal(ctx, pipeline")

	assert.Greater(t, observedGenIndex, finalizerIndex,
		"RECON-03 violation: ObservedGeneration check must come AFTER finalizer addition. "+
			"Current order is incorrect.")
	assert.Greater(t, reconcileNormalIndex, observedGenIndex,
		"RECON-03 violation: ObservedGeneration check must come BEFORE reconcileNormal(). "+
			"Current order is incorrect.")

	// 3. Verify ObservedGeneration is SET in success path
	successSetPattern := "pipeline.Status.ObservedGeneration = pipeline.Generation"
	successFuncStart := strings.Index(fileContent, "func (r *PipelineReconciler) updateStatusSuccess(")
	assert.Greater(t, successFuncStart, 0, "Could not find updateStatusSuccess() function")

	successFuncEnd := strings.Index(fileContent[successFuncStart:], "\nfunc ")
	if successFuncEnd < 0 {
		successFuncEnd = len(fileContent) - successFuncStart
	}
	successFuncBody := fileContent[successFuncStart : successFuncStart+successFuncEnd]

	assert.Contains(t, successFuncBody, successSetPattern,
		"RECON-03 violation: updateStatusSuccess() must set pipeline.Status.ObservedGeneration = pipeline.Generation")

	// 4. Verify ObservedGeneration is SET in error path
	errorFuncStart := strings.Index(fileContent, "func (r *PipelineReconciler) updateStatusError(")
	assert.Greater(t, errorFuncStart, 0, "Could not find updateStatusError() function")

	errorFuncEnd := strings.Index(fileContent[errorFuncStart:], "\nfunc ")
	if errorFuncEnd < 0 {
		errorFuncEnd = len(fileContent) - errorFuncStart
	}
	errorFuncBody := fileContent[errorFuncStart : errorFuncStart+errorFuncEnd]

	assert.Contains(t, errorFuncBody, successSetPattern,
		"RECON-03 violation: updateStatusError() must set pipeline.Status.ObservedGeneration = pipeline.Generation")

	t.Log("RECON-03 verified: ObservedGeneration pattern correctly implemented (check exists, correct position, set in both paths)")
}

// TestFinalizerMinimalAPICalls verifies RECON-05: reconcileDelete makes exactly 1 K8s API call.
//
// The deletion path should:
// - Use the Pipeline object passed in (from the Get() in Reconcile)
// - Call DeletePipeline (Fleet API, not K8s API)
// - Call r.Update() once to remove finalizer
//
// No additional Get() or other K8s API calls should exist in reconcileDelete().
func TestFinalizerMinimalAPICalls(t *testing.T) {
	// Parse the controller source file
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "pipeline_controller.go", nil, parser.ParseComments)
	assert.NoError(t, err, "failed to parse pipeline_controller.go")

	// Find reconcileDelete function
	var reconcileDeleteFunc *ast.FuncDecl
	ast.Inspect(node, func(n ast.Node) bool {
		if funcDecl, ok := n.(*ast.FuncDecl); ok {
			if funcDecl.Name.Name == "reconcileDelete" {
				reconcileDeleteFunc = funcDecl
				return false // Stop searching
			}
		}
		return true
	})

	assert.NotNil(t, reconcileDeleteFunc, "Could not find reconcileDelete() function")

	// Count K8s API calls in reconcileDelete
	k8sAPICalls := []string{}
	ast.Inspect(reconcileDeleteFunc.Body, func(n ast.Node) bool {
		callExpr, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		selExpr, ok := callExpr.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		// Check if receiver is "r" (indicates controller K8s API call)
		if ident, ok := selExpr.X.(*ast.Ident); ok {
			if ident.Name == "r" {
				methodName := selExpr.Sel.Name
				// K8s API methods (not FleetClient methods)
				k8sMethods := []string{"Get", "Update", "Create", "Delete", "Patch", "List"}
				for _, k8sMethod := range k8sMethods {
					if methodName == k8sMethod {
						position := fset.Position(selExpr.Pos())
						k8sAPICalls = append(k8sAPICalls, fmt.Sprintf("%s at %s", methodName, position))
					}
				}
			}
		}

		// Also check for Status().Update()
		if innerCall, ok := selExpr.X.(*ast.CallExpr); ok {
			if innerSel, ok := innerCall.Fun.(*ast.SelectorExpr); ok {
				if innerSel.Sel.Name == "Status" && selExpr.Sel.Name == "Update" {
					position := fset.Position(selExpr.Pos())
					k8sAPICalls = append(k8sAPICalls, fmt.Sprintf("Status().Update() at %s", position))
				}
			}
		}

		return true
	})

	// Assert exactly 1 K8s API call (Update to remove finalizer)
	assert.Equal(t, 1, len(k8sAPICalls),
		"RECON-05 violation: reconcileDelete() should make exactly 1 K8s API call (Update to remove finalizer). "+
			"Found %d calls: %v. "+
			"The Pipeline object is passed in (no Get needed), and finalizer removal is the final operation.",
		len(k8sAPICalls), k8sAPICalls)

	// Verify it's an Update call
	if len(k8sAPICalls) == 1 {
		assert.Contains(t, k8sAPICalls[0], "Update",
			"RECON-05 violation: The single K8s API call in reconcileDelete() should be Update() for finalizer removal")
	}

	t.Logf("RECON-05 verified: reconcileDelete() makes exactly 1 K8s API call: %v", k8sAPICalls)
}

// TestReconcileJustificationDocumentation verifies that all "Reconcile:" documentation
// markers added in Task 1 are present. This ensures the documentation is not accidentally
// removed during refactoring.
func TestReconcileJustificationDocumentation(t *testing.T) {
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
			marker:      "Reconcile Loop Audit:",
			description: "package-level reconcile loop audit summary",
		},
		{
			marker:      "Reconcile: Required entry point - fetch the resource that triggered reconciliation",
			description: "Get() operation reconcile justification in Reconcile()",
		},
		{
			marker:      "Reconcile: Finalizer must be persisted before any Fleet Management API call",
			description: "Update() operation reconcile justification for finalizer addition",
		},
		{
			marker:      "Reconcile: Finalizer removal is the final K8s API call in deletion",
			description: "Update() operation reconcile justification for finalizer removal",
		},
		{
			marker:      "Reconcile: Status subresource update after successful Fleet Management sync",
			description: "Status().Update() reconcile justification in updateStatusSuccess()",
		},
		{
			marker:      "Reconcile: Status subresource update to record error condition",
			description: "Status().Update() reconcile justification in updateStatusError()",
		},
	}

	// Verify each marker is present
	for _, expected := range expectedMarkers {
		assert.Contains(t, fileContent, expected.marker,
			"missing reconcile documentation: %s. This comment justifies why the API call cannot be eliminated.",
			expected.description)
	}

	// Count total "Reconcile:" markers (should be exactly 5 for the API calls)
	reconcileMarkerCount := strings.Count(fileContent, "Reconcile:")
	assert.Equal(t, 5, reconcileMarkerCount,
		"expected exactly 5 'Reconcile:' documentation comments in pipeline_controller.go (one per K8s API call)")

	t.Logf("Found %d reconcile justification markers and 1 Reconcile Loop Audit summary in pipeline_controller.go",
		reconcileMarkerCount)
}
