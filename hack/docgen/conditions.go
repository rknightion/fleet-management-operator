package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// conditionTriple is one (CRD, conditionType, reason) tuple emitted by code or
// listed in the doc.
type conditionTriple struct {
	CRD    string // user-facing Kind, e.g. "Pipeline"
	Type   string // "Ready" / "Synced" / "Stalled" / "Truncated"
	Reason string // PascalCase reason, e.g. "Synced", "SyncFailed"
}

// verifyConditions cross-references the conditions table in docs/conditions.md
// against meta.SetStatusCondition call sites in internal/controller/*.go.
// It is the lint half of the docs pipeline — the meaning-prose stays
// hand-written, but reason inventories must match. Returns nil on success;
// any mismatch yields a multi-line error with file:line pointers.
func verifyConditions(root, docPath string) error {
	codeTriples, codeUnresolved, err := scanConditionCalls(root)
	if err != nil {
		return err
	}
	docTriples, err := parseConditionsDoc(filepath.Join(root, docPath))
	if err != nil {
		return err
	}

	// Set semantics for diff.
	codeSet := make(map[conditionTriple]struct{}, len(codeTriples))
	for _, t := range codeTriples {
		codeSet[t] = struct{}{}
	}
	docSet := make(map[conditionTriple]struct{}, len(docTriples))
	for _, t := range docTriples {
		docSet[t] = struct{}{}
	}

	var missingFromDoc []conditionTriple
	for t := range codeSet {
		if _, ok := docSet[t]; !ok {
			missingFromDoc = append(missingFromDoc, t)
		}
	}
	var missingFromCode []conditionTriple
	for t := range docSet {
		if _, ok := codeSet[t]; !ok {
			missingFromCode = append(missingFromCode, t)
		}
	}

	if len(missingFromDoc) == 0 && len(missingFromCode) == 0 && len(codeUnresolved) == 0 {
		return nil
	}

	// Build a single human-readable error blob. CI failure messages are the
	// only place this output ever lands, so optimise for "what do I do next".
	var sb strings.Builder
	sb.WriteString("docs/conditions.md is out of sync with controller code.\n\n")
	if len(missingFromDoc) > 0 {
		sb.WriteString("Reasons emitted by code but not listed in docs/conditions.md:\n")
		sortTriples(missingFromDoc)
		for _, t := range missingFromDoc {
			fmt.Fprintf(&sb, "  - %s / Type=%s / Reason=%s\n", t.CRD, t.Type, t.Reason)
		}
		sb.WriteString("\n")
	}
	if len(missingFromCode) > 0 {
		sb.WriteString("Reasons listed in docs/conditions.md but not emitted by any code path:\n")
		sortTriples(missingFromCode)
		for _, t := range missingFromCode {
			fmt.Fprintf(&sb, "  - %s / Type=%s / Reason=%s\n", t.CRD, t.Type, t.Reason)
		}
		sb.WriteString("\n")
	}
	if len(codeUnresolved) > 0 {
		// Unresolved Reason identifiers are not a hard fail — they often
		// reflect parameterised setters whose call sites pass constants
		// statically. Surface as info so reviewers can spot-check rather
		// than reject the build.
		sb.WriteString("Note: the following Reason references could not be resolved to a string constant\n")
		sb.WriteString("by static analysis (probably function parameters). Spot-check call sites manually:\n")
		sort.Strings(codeUnresolved)
		for _, u := range codeUnresolved {
			fmt.Fprintf(&sb, "  - %s\n", u)
		}
		sb.WriteString("\n")
	}
	// If the only finding is unresolved-but-not-mismatched, treat as success
	// — it's informational. The hard-fail criteria are doc/code mismatches.
	if len(missingFromDoc) == 0 && len(missingFromCode) == 0 {
		fmt.Fprint(os.Stderr, sb.String())
		return nil
	}
	sb.WriteString("Run 'make docs' to regenerate flags / metrics / events / samples,\n")
	sb.WriteString("then update docs/conditions.md (Meaning column requires human authorship).\n")
	return fmt.Errorf("%s", sb.String())
}

func sortTriples(t []conditionTriple) {
	sort.Slice(t, func(i, j int) bool {
		if t[i].CRD != t[j].CRD {
			return t[i].CRD < t[j].CRD
		}
		if t[i].Type != t[j].Type {
			return t[i].Type < t[j].Type
		}
		return t[i].Reason < t[j].Reason
	})
}

// scanConditionCalls walks every controller file looking for
// meta.SetStatusCondition calls. The internal/controller package is a single
// Go package so constants defined in one file are visible from another:
// pipeline_controller.go defines `conditionTypeReady` once and the other
// controllers reference it. We therefore build a package-wide const map
// before walking per-file call sites.
//
// Parameterised Reason arguments (Reason: reason where reason is a function
// parameter) are recorded as `pendingParamReason` entries during the first
// per-file walk and resolved in a second pass that traces call sites of the
// enclosing function across the whole package.
func scanConditionCalls(root string) (triples []conditionTriple, unresolved []string, err error) {
	dir := filepath.Join(root, "internal", "controller")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	pkgConsts := map[string]string{}
	type parsedFile struct {
		path string
		fset *token.FileSet
		file *ast.File
	}
	var files []parsedFile
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		full := filepath.Join(dir, name)
		fset := token.NewFileSet()
		src, err := os.ReadFile(full)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", name, err)
		}
		f, err := parser.ParseFile(fset, full, src, parser.ParseComments)
		if err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", name, err)
		}
		for k, v := range collectStringConsts(f) {
			pkgConsts[k] = v
		}
		// Only controller files emit SetStatusCondition; helpers like
		// metrics.go don't. We scan call sites only in *_controller.go to
		// keep the CRD tagging tied to a specific reconciler.
		if strings.HasSuffix(name, "_controller.go") {
			files = append(files, parsedFile{path: full, fset: fset, file: f})
		}
	}

	var pending []pendingParamReason
	fileASTs := map[string]*ast.File{}
	for _, pf := range files {
		t, p, u, err := scanCallSites(root, pf.path, pf.fset, pf.file, pkgConsts)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", filepath.Base(pf.path), err)
		}
		triples = append(triples, t...)
		pending = append(pending, p...)
		unresolved = append(unresolved, u...)
		fileASTs[pf.path] = pf.file
	}

	// Second pass: resolve parameterised Reason references via a worklist.
	// One unrolling step is not enough — see updateStatusError → setReadyCondition
	// → meta.SetStatusCondition, where the Reason hops through two parameter
	// passes before reaching a constant. The worklist iterates until the
	// resolved set stabilises. We track visited (funcName, paramIdx, file)
	// triples to bound recursion in the unlikely event of mutually-recursive
	// helpers.
	type frame struct {
		HomeCRD   string
		Type      string
		IsMethod  bool
		File      string
		FuncName  string
		ParamIdx  int
		ParamName string
		SrcLoc    string
	}
	worklist := make([]frame, 0, len(pending))
	for _, p := range pending {
		worklist = append(worklist, frame{
			HomeCRD:   p.HomeCRD,
			Type:      p.Type,
			IsMethod:  p.IsMethod,
			File:      p.File,
			FuncName:  p.FuncName,
			ParamIdx:  p.ParamIdx,
			ParamName: p.ParamName,
			SrcLoc:    p.SrcLoc,
		})
	}
	visited := map[string]struct{}{}
	visitKey := func(fr frame) string {
		// Include Type — Ready and Synced flows through the same helper but
		// the resolved triples differ by Type, so they must be processed
		// independently.
		return fmt.Sprintf("%s|%d|%s|%s|%v", fr.FuncName, fr.ParamIdx, fr.Type, fr.File, fr.IsMethod)
	}

	for len(worklist) > 0 {
		fr := worklist[0]
		worklist = worklist[1:]
		k := visitKey(fr)
		if _, seen := visited[k]; seen {
			continue
		}
		visited[k] = struct{}{}

		var scope map[string]*ast.File
		if fr.IsMethod {
			f, ok := fileASTs[fr.File]
			if !ok {
				unresolved = append(unresolved, fmt.Sprintf("%s Reason=%s (helper file not parsed)", fr.SrcLoc, fr.ParamName))
				continue
			}
			scope = map[string]*ast.File{fr.File: f}
		} else {
			scope = fileASTs
		}

		anyHit := false
		for filePath, f := range scope {
			callerCRD := fr.HomeCRD
			if !fr.IsMethod {
				callerCRD = controllerName(filepath.Base(filePath))
			}
			for _, decl := range f.Decls {
				callerFn, ok := decl.(*ast.FuncDecl)
				if !ok || callerFn.Body == nil {
					continue
				}
				callerParams := paramIndex(callerFn)
				ast.Inspect(callerFn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					if !callsFunc(call, fr.FuncName) {
						return true
					}
					if fr.ParamIdx >= len(call.Args) {
						return true
					}
					arg := call.Args[fr.ParamIdx]
					if s := resolveStringValue(arg, pkgConsts); s != "" {
						triples = append(triples, conditionTriple{CRD: callerCRD, Type: fr.Type, Reason: s})
						anyHit = true
						return true
					}
					// Argument is itself a parameter: enqueue another step.
					if id, ok := arg.(*ast.Ident); ok {
						if idx, ok := callerParams[id.Name]; ok {
							worklist = append(worklist, frame{
								HomeCRD:   callerCRD,
								Type:      fr.Type,
								IsMethod:  callerFn.Recv != nil,
								File:      filePath,
								FuncName:  callerFn.Name.Name,
								ParamIdx:  idx,
								ParamName: id.Name,
								SrcLoc:    fr.SrcLoc,
							})
							anyHit = true
						}
					}
					return true
				})
			}
		}
		if !anyHit {
			unresolved = append(unresolved, fmt.Sprintf("%s Reason=%s (no call site provided a constant)", fr.SrcLoc, fr.ParamName))
		}
	}

	return triples, unresolved, nil
}

// callsFunc reports whether call invokes the named function. Both
// `r.helper(...)` (method) and `helper(...)` (plain func) match by trailing
// identifier, since the package consts are unambiguous.
func callsFunc(call *ast.CallExpr, name string) bool {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name == name
	case *ast.SelectorExpr:
		return fn.Sel.Name == name
	}
	return false
}

// pendingParamReason records a meta.SetStatusCondition call whose Reason
// argument is a parameter of the enclosing function. Resolution happens in a
// second pass that walks call sites of that function. Scope depends on
// whether the helper is a receiver method or a plain function:
//
//   - Receiver method (e.g. `(r *PipelineReconciler) updateStatusError`):
//     each controller has its own version under the same name; only callers
//     in the same file can possibly hit this declaration. Attribute reasons
//     to the helper's home CRD.
//
//   - Plain function (e.g. `setReadyCondition` defined in
//     external_sync_controller.go and called from
//     collector_discovery_controller.go): callers may live anywhere in the
//     package; attribute the resolved reason to the CALLER's CRD.
type pendingParamReason struct {
	HomeCRD   string // helper's home CRD (used for receiver-method callers)
	Type      string // already-resolved condition Type
	File      string // absolute path of the helper's home file
	IsMethod  bool   // true if helper has a receiver
	FuncName  string // enclosing function's name
	ParamName string // identifier used for Reason
	ParamIdx  int    // position of ParamName in the parameter list (0-based)
	SrcLoc    string // file:line of the SetStatusCondition call
}

func scanCallSites(root, path string, fset *token.FileSet, file *ast.File, consts map[string]string) ([]conditionTriple, []pendingParamReason, []string, error) {
	relPath, err := filepath.Rel(root, path)
	if err != nil {
		relPath = path
	}
	crd := controllerName(filepath.Base(path))

	var triples []conditionTriple
	var pending []pendingParamReason
	var unresolved []string

	// Visit each FuncDecl so we know the enclosing function (and its params)
	// for any nested SetStatusCondition call.
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		params := paramIndex(fn)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "meta" || sel.Sel.Name != "SetStatusCondition" {
				return true
			}
			if len(call.Args) < 2 {
				return true
			}
			opts, ok := call.Args[1].(*ast.CompositeLit)
			if !ok {
				return true
			}
			var typeStr, reasonStr string
			var unresolvedReasonIdent string
			for _, elt := range opts.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				switch key.Name {
				case "Type":
					typeStr = resolveStringValue(kv.Value, consts)
				case "Reason":
					if s := resolveStringValue(kv.Value, consts); s != "" {
						reasonStr = s
					} else if id, ok := kv.Value.(*ast.Ident); ok {
						unresolvedReasonIdent = id.Name
					}
				}
			}
			if typeStr == "" {
				pos := fset.Position(call.Pos())
				unresolved = append(unresolved, fmt.Sprintf("%s:%d Type expression unresolved", relPath, pos.Line))
				return true
			}
			if reasonStr != "" {
				triples = append(triples, conditionTriple{CRD: crd, Type: typeStr, Reason: reasonStr})
				return true
			}
			if unresolvedReasonIdent == "" {
				return true
			}
			// If the unresolved Reason is a local variable in the enclosing
			// function whose assignments are all to string constants (the
			// pattern in setStalledCondition: `reason := A; if … { reason = B }`),
			// emit one triple per possible value.
			if vals := resolveLocalVarAssignments(fn, unresolvedReasonIdent, consts); len(vals) > 0 {
				for _, v := range vals {
					triples = append(triples, conditionTriple{CRD: crd, Type: typeStr, Reason: v})
				}
				return true
			}
			// If the unresolved Reason matches a parameter of the enclosing
			// function, queue it for inter-procedural resolution. Receiver
			// methods are file-scoped; plain functions are package-scoped
			// (and attributed to the caller's file).
			if idx, ok := params[unresolvedReasonIdent]; ok {
				pos := fset.Position(call.Pos())
				pending = append(pending, pendingParamReason{
					HomeCRD:   crd,
					Type:      typeStr,
					File:      path,
					IsMethod:  fn.Recv != nil,
					FuncName:  fn.Name.Name,
					ParamName: unresolvedReasonIdent,
					ParamIdx:  idx,
					SrcLoc:    fmt.Sprintf("%s:%d", relPath, pos.Line),
				})
				return true
			}
			pos := fset.Position(call.Pos())
			unresolved = append(unresolved, fmt.Sprintf("%s:%d Reason=%s (not a constant or parameter)", relPath, pos.Line, unresolvedReasonIdent))
			return true
		})
	}
	return triples, pending, unresolved, nil
}

// resolveLocalVarAssignments collects every distinct constant value assigned
// to a local variable inside the function body. Handles `name := const` and
// `name = const`. Used for the setStalledCondition pattern where the Reason
// flips between two constants depending on a flag. Returns nil if any
// assignment is non-constant — partial coverage would be misleading.
func resolveLocalVarAssignments(fn *ast.FuncDecl, name string, consts map[string]string) []string {
	if fn.Body == nil {
		return nil
	}
	values := map[string]struct{}{}
	allConst := true
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range v.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || id.Name != name {
					continue
				}
				if i >= len(v.Rhs) {
					continue
				}
				if s := resolveStringValue(v.Rhs[i], consts); s != "" {
					values[s] = struct{}{}
				} else {
					allConst = false
				}
			}
		}
		return true
	})
	if !allConst || len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for v := range values {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// paramIndex returns a map of paramName → 0-based position for non-receiver
// arguments. Variadic last params are treated like a regular param at their
// declared position.
func paramIndex(fn *ast.FuncDecl) map[string]int {
	out := map[string]int{}
	if fn.Type == nil || fn.Type.Params == nil {
		return out
	}
	idx := 0
	for _, field := range fn.Type.Params.List {
		// One Field can declare multiple named params: `a, b string`.
		for _, name := range field.Names {
			out[name.Name] = idx
			idx++
		}
		if len(field.Names) == 0 {
			idx++ // anonymous parameter
		}
	}
	return out
}

// collectStringConsts gathers every package-level const declaration whose RHS
// is a string literal. Both Type and Reason identifiers live in these blocks
// across the controllers — we don't need a per-name filter.
func collectStringConsts(file *ast.File) map[string]string {
	out := map[string]string{}
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, n := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				if s, ok := stringLit(vs.Values[i]); ok {
					out[n.Name] = s
				}
			}
		}
	}
	return out
}

// resolveStringValue handles the two shapes Type/Reason fields take in this
// codebase: a string literal, or an identifier that resolves to a constant
// in the same file.
func resolveStringValue(e ast.Expr, consts map[string]string) string {
	switch v := e.(type) {
	case *ast.BasicLit:
		if s, ok := stringLit(v); ok {
			return s
		}
	case *ast.Ident:
		if s, ok := consts[v.Name]; ok {
			return s
		}
	}
	return ""
}

// parseConditionsDoc reads docs/conditions.md and returns the (CRD, Type,
// Reason) triples implied by its tables. The doc has two structural parts:
//
//   - "## Condition types" — a single table whose rows are (CRD, Type, Meaning).
//   - "### <CRD>" headers — each followed by a per-CRD reasons table whose
//     rows are (Reason, "Used on" types, Meaning).
//
// We pivot the second-shape rows from "Used on" into one triple per Type so
// that code emitting Reason=Synced for both Ready and Synced is fully covered.
func parseConditionsDoc(path string) ([]conditionTriple, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")

	// Phase 1: collect declared types per CRD from the "## Condition types"
	// table. Used to validate that every code-emitted (CRD, Type) is also a
	// declared type. (We currently fold this into the per-Reason rows below;
	// the table itself is not separately walked because the Reason-tables
	// are the source of truth for verification.)

	// Phase 2: walk "### <CRD>" sections, parse the first markdown table
	// after each header, extract Reason + Used-on. Anything else is prose.
	var triples []conditionTriple
	crdRe := regexp.MustCompile(`^###\s+([A-Za-z]+)\s*\(`)
	currentCRD := ""
	inTable := false
	headerSeen := false
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if m := crdRe.FindStringSubmatch(line); m != nil {
			currentCRD = m[1]
			inTable = false
			headerSeen = false
			continue
		}
		if currentCRD == "" {
			continue
		}
		// Markdown table rows start with `|`. The first such row is the
		// header (Reason | Used on | Meaning); the second is the divider;
		// rows from the third onward are data.
		if strings.HasPrefix(strings.TrimSpace(line), "|") {
			cells := splitTableRow(line)
			if !headerSeen {
				headerSeen = true
				continue
			}
			if !inTable {
				inTable = true // divider line
				continue
			}
			if len(cells) < 2 {
				continue
			}
			reason := strings.Trim(cells[0], "` ")
			usedOn := cells[1]
			for _, t := range parseUsedOnList(usedOn) {
				triples = append(triples, conditionTriple{CRD: currentCRD, Type: t, Reason: reason})
			}
		} else if inTable {
			// Blank line or non-table content — table is over.
			inTable = false
			headerSeen = false
		}
	}
	return triples, nil
}

// splitTableRow tokenises a markdown table row (`| a | b | c |`) into cells.
// The leading and trailing pipes produce empty strings which are dropped.
func splitTableRow(line string) []string {
	parts := strings.Split(line, "|")
	out := make([]string, 0, len(parts))
	for i, p := range parts {
		// Drop the empty cells produced by leading/trailing pipes.
		if (i == 0 || i == len(parts)-1) && strings.TrimSpace(p) == "" {
			continue
		}
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// parseUsedOnList tokenises "Ready, Synced" / "Truncated" / "Ready, Valid" cells
// into a list of condition Type names. Backticks and stray punctuation are
// stripped because the doc inconsistently quotes them.
func parseUsedOnList(s string) []string {
	s = strings.ReplaceAll(s, "`", "")
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
