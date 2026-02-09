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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestResyncPeriodDocumented verifies that the resync period configuration is intentionally
// disabled (nil SyncPeriod) and documented in cmd/main.go.
//
// WATCH-01: Resync period should be nil for watch-driven controllers with no external drift.
func TestResyncPeriodDocumented(t *testing.T) {
	// Read cmd/main.go
	content, err := os.ReadFile("../../cmd/main.go")
	assert.NoError(t, err, "failed to read cmd/main.go")

	fileContent := string(content)

	// Verify "Watch:" documentation exists explaining resync period rationale
	assert.Contains(t, fileContent, "Watch: (WATCH-01) Resync period configuration",
		"missing Watch documentation for resync period in cmd/main.go")

	// Parse cmd/main.go to verify SyncPeriod is NOT explicitly set
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "../../cmd/main.go", nil, parser.ParseComments)
	assert.NoError(t, err, "failed to parse cmd/main.go")

	// Track if SyncPeriod is set in ctrl.Options
	syncPeriodSet := false

	// Walk the AST looking for ctrl.Options struct literal
	ast.Inspect(node, func(n ast.Node) bool {
		// Look for composite literals (struct initialization)
		compLit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}

		// Check if it's a ctrl.Options struct
		selExpr, ok := compLit.Type.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		// Verify it's ctrl.Options (not other structs)
		if ident, ok := selExpr.X.(*ast.Ident); ok && ident.Name == "ctrl" && selExpr.Sel.Name == "Options" {
			// Check each field in the struct literal
			for _, elt := range compLit.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					if keyIdent, ok := kv.Key.(*ast.Ident); ok && keyIdent.Name == "SyncPeriod" {
						syncPeriodSet = true
					}
				}
			}
		}

		return true
	})

	// Assert SyncPeriod is NOT set (meaning nil/disabled, which is intentional)
	assert.False(t, syncPeriodSet,
		"SyncPeriod should NOT be explicitly set in ctrl.Options. "+
			"The nil/default (no periodic resync) is the correct configuration for this watch-driven controller.")

	t.Log("Resync period confirmed disabled (nil) and documented in cmd/main.go")
}

// TestWorkqueueRateLimiterDocumented verifies that the workqueue rate limiter configuration
// is intentionally using controller-runtime defaults and is documented.
//
// WATCH-02: Default rate limiter is appropriate for low-moderate volume with Fleet API rate limiting.
func TestWorkqueueRateLimiterDocumented(t *testing.T) {
	// Read pipeline_controller.go
	content, err := os.ReadFile("pipeline_controller.go")
	assert.NoError(t, err, "failed to read pipeline_controller.go")

	fileContent := string(content)

	// Verify "Watch:" documentation exists explaining workqueue rate limiter
	assert.Contains(t, fileContent, "Watch: (WATCH-02) Workqueue rate limiter configuration",
		"missing Watch documentation for workqueue rate limiter in SetupWithManager")

	// Verify documentation mentions the default rate limiter
	assert.Contains(t, fileContent, "DefaultTypedControllerRateLimiter",
		"documentation should mention DefaultTypedControllerRateLimiter")

	// Parse pipeline_controller.go to verify WithOptions is NOT used
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "pipeline_controller.go", nil, parser.ParseComments)
	assert.NoError(t, err, "failed to parse pipeline_controller.go")

	// Track if WithOptions is called in the controller builder
	withOptionsFound := false

	// Walk the AST looking for WithOptions method calls
	ast.Inspect(node, func(n ast.Node) bool {
		// Look for method calls
		callExpr, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		// Check if it's a method call (SelectorExpr)
		selExpr, ok := callExpr.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		// Check if the method name is "WithOptions"
		if selExpr.Sel.Name == "WithOptions" {
			withOptionsFound = true
		}

		return true
	})

	// Assert WithOptions is NOT used (confirming defaults are intentional)
	assert.False(t, withOptionsFound,
		"WithOptions should NOT be called in SetupWithManager. "+
			"The controller-runtime default rate limiter is the correct configuration.")

	t.Log("Workqueue rate limiter confirmed using defaults and documented")
}

// TestExponentialBackoffConfigured verifies that the controller uses the four return patterns
// to correctly trigger exponential backoff for different error types.
//
// WATCH-03: Four return patterns (error, Requeue, RequeueAfter, nil) handle all error scenarios.
func TestExponentialBackoffConfigured(t *testing.T) {
	// Read pipeline_controller.go
	content, err := os.ReadFile("pipeline_controller.go")
	assert.NoError(t, err, "failed to read pipeline_controller.go")

	fileContent := string(content)

	// Verify "Watch:" documentation exists explaining exponential backoff patterns
	assert.Contains(t, fileContent, "Watch: (WATCH-03) Exponential backoff configuration",
		"missing Watch documentation for exponential backoff in updateStatusError")

	// Parse pipeline_controller.go
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "pipeline_controller.go", nil, parser.ParseComments)
	assert.NoError(t, err, "failed to parse pipeline_controller.go")

	// Track return patterns found
	returnPatterns := map[string]bool{
		"error":        false, // Returns error (triggers exponential backoff)
		"Requeue":      false, // Returns Requeue: true (requeue without failure count)
		"RequeueAfter": false, // Returns RequeueAfter (timed requeue)
		"nilError":     false, // Returns nil error with empty Result (no requeue)
	}

	// Walk the AST looking for return statements in reconciliation functions
	ast.Inspect(node, func(n ast.Node) bool {
		// Look for return statements
		retStmt, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}

		// Analyze return statement patterns
		if len(retStmt.Results) == 2 {
			// ctrl.Result{}, error pattern

			// Check second return value (error)
			secondRet := retStmt.Results[1]

			// Pattern 4: ctrl.Result{}, nil
			if ident, ok := secondRet.(*ast.Ident); ok && ident.Name == "nil" {
				returnPatterns["nilError"] = true
			}

			// Pattern 1: ctrl.Result{}, err / originalErr
			if ident, ok := secondRet.(*ast.Ident); ok && (ident.Name == "err" || ident.Name == "originalErr") {
				returnPatterns["error"] = true
			}

			// Check first return value (ctrl.Result{...})
			if compLit, ok := retStmt.Results[0].(*ast.CompositeLit); ok {
				for _, elt := range compLit.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok {
						if keyIdent, ok := kv.Key.(*ast.Ident); ok {
							// Pattern 2: Requeue: true
							if keyIdent.Name == "Requeue" {
								returnPatterns["Requeue"] = true
							}
							// Pattern 3: RequeueAfter: ...
							if keyIdent.Name == "RequeueAfter" {
								returnPatterns["RequeueAfter"] = true
							}
						}
					}
				}
			}
		}

		return true
	})

	// Verify all four patterns are present
	assert.True(t, returnPatterns["error"],
		"controller should return error to trigger exponential backoff for transient errors")
	assert.True(t, returnPatterns["Requeue"],
		"controller should return Requeue: true for status conflicts (no failure penalty)")
	assert.True(t, returnPatterns["RequeueAfter"],
		"controller should return RequeueAfter for rate limit errors (fixed delay)")
	assert.True(t, returnPatterns["nilError"],
		"controller should return nil error for validation errors (no automatic retry)")

	t.Logf("All four exponential backoff patterns verified: %+v", returnPatterns)
}

// TestNoWatchStormPatterns verifies that the controller watches ONLY the Pipeline CRD
// with no secondary resource watches that could cause watch storm scenarios.
//
// WATCH-04: Single For() watch with no Owns() or Watches() prevents feedback loops.
func TestNoWatchStormPatterns(t *testing.T) {
	// Read pipeline_controller.go
	content, err := os.ReadFile("pipeline_controller.go")
	assert.NoError(t, err, "failed to read pipeline_controller.go")

	fileContent := string(content)

	// Verify "Watch:" documentation exists explaining storm prevention
	assert.Contains(t, fileContent, "Watch: (WATCH-04) Watch storm prevention",
		"missing Watch documentation for watch storm prevention in SetupWithManager")

	// Parse pipeline_controller.go
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "pipeline_controller.go", nil, parser.ParseComments)
	assert.NoError(t, err, "failed to parse pipeline_controller.go")

	// Track controller builder method calls
	builderCalls := map[string]int{
		"For":     0,
		"Owns":    0,
		"Watches": 0,
	}

	// Walk the AST looking for SetupWithManager function
	ast.Inspect(node, func(n ast.Node) bool {
		// Look for method calls
		callExpr, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		// Check if it's a method call (SelectorExpr)
		selExpr, ok := callExpr.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		// Count For(), Owns(), Watches() calls
		methodName := selExpr.Sel.Name
		if methodName == "For" || methodName == "Owns" || methodName == "Watches" {
			builderCalls[methodName]++
		}

		return true
	})

	// Verify exactly 1 For() call
	assert.Equal(t, 1, builderCalls["For"],
		"controller should have exactly 1 For() call watching Pipeline CRD")

	// Verify NO Owns() calls
	assert.Equal(t, 0, builderCalls["Owns"],
		"controller should have NO Owns() calls. Secondary resource watches can cause watch storms.")

	// Verify NO Watches() calls
	assert.Equal(t, 0, builderCalls["Watches"],
		"controller should have NO Watches() calls. Secondary resource watches can cause watch storms.")

	t.Logf("Watch pattern verified: For=%d, Owns=%d, Watches=%d (storm-free)",
		builderCalls["For"], builderCalls["Owns"], builderCalls["Watches"])
}

// TestWatchAuditDocumentation verifies that the watch pattern documentation comments
// are present in both pipeline_controller.go and cmd/main.go. This ensures the documentation
// is not accidentally removed during refactoring.
func TestWatchAuditDocumentation(t *testing.T) {
	// Read pipeline_controller.go
	controllerContent, err := os.ReadFile("pipeline_controller.go")
	assert.NoError(t, err, "failed to read pipeline_controller.go")

	controllerFileContent := string(controllerContent)

	// Read cmd/main.go
	mainContent, err := os.ReadFile("../../cmd/main.go")
	assert.NoError(t, err, "failed to read cmd/main.go")

	mainFileContent := string(mainContent)

	// Verify package-level Watch Pattern Audit summary
	assert.Contains(t, controllerFileContent, "Watch Pattern Audit:",
		"missing package-level Watch Pattern Audit summary in pipeline_controller.go")

	// Define expected documentation markers in pipeline_controller.go
	expectedControllerMarkers := []struct {
		marker      string
		description string
	}{
		{
			marker:      "Watch: (WATCH-02) Workqueue rate limiter configuration",
			description: "workqueue rate limiter documentation in SetupWithManager",
		},
		{
			marker:      "Watch: (WATCH-03) Exponential backoff configuration",
			description: "exponential backoff documentation in updateStatusError",
		},
		{
			marker:      "Watch: (WATCH-04) Watch storm prevention",
			description: "watch storm prevention documentation in SetupWithManager",
		},
	}

	// Verify each marker in pipeline_controller.go
	for _, expected := range expectedControllerMarkers {
		assert.Contains(t, controllerFileContent, expected.marker,
			"missing watch documentation: %s", expected.description)
	}

	// Verify cmd/main.go marker
	assert.Contains(t, mainFileContent, "Watch: (WATCH-01) Resync period configuration",
		"missing resync period documentation in cmd/main.go")

	// Count total "Watch:" markers in pipeline_controller.go
	// Note: WATCH-01 is in cmd/main.go, so pipeline_controller.go has WATCH-02, WATCH-03, WATCH-04 = 3 markers
	controllerWatchCount := strings.Count(controllerFileContent, "Watch:")
	assert.GreaterOrEqual(t, controllerWatchCount, 3,
		"expected at least 3 'Watch:' documentation comments in pipeline_controller.go "+
			"(WATCH-02, WATCH-03, WATCH-04)")

	// Count total "Watch:" markers in cmd/main.go
	mainWatchCount := strings.Count(mainFileContent, "Watch:")
	assert.GreaterOrEqual(t, mainWatchCount, 1,
		"expected at least 1 'Watch:' documentation comment in cmd/main.go (WATCH-01)")

	t.Logf("Found %d watch documentation markers in pipeline_controller.go", controllerWatchCount)
	t.Logf("Found %d watch documentation markers in cmd/main.go", mainWatchCount)
}
