package mcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"testing"
)

type fieldSet map[string]struct{}

func newFieldSet(fields ...string) fieldSet {
	set := make(fieldSet, len(fields))
	for _, field := range fields {
		if field == "" {
			continue
		}
		set[field] = struct{}{}
	}
	return set
}

func (s fieldSet) add(field string) {
	if field == "" {
		return
	}
	s[field] = struct{}{}
}

func (s fieldSet) union(other fieldSet) {
	for field := range other {
		s[field] = struct{}{}
	}
}

func (s fieldSet) clone() fieldSet {
	cloned := make(fieldSet, len(s))
	for field := range s {
		cloned[field] = struct{}{}
	}
	return cloned
}

func (s fieldSet) sorted() []string {
	fields := make([]string, 0, len(s))
	for field := range s {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

func diffFieldSets(left, right fieldSet) []string {
	var diff []string
	for field := range left {
		if _, ok := right[field]; ok {
			continue
		}
		diff = append(diff, field)
	}
	sort.Strings(diff)
	return diff
}

type handlerParamUsage struct {
	required  fieldSet
	extracted fieldSet
}

func TestToolSchemaRequiredMatchesHandlerExtraction(t *testing.T) {
	mcpDir := mcpSourceDir(t)

	toolHandlers, err := parseRegisteredToolHandlers(filepath.Join(mcpDir, "server.go"))
	if err != nil {
		t.Fatalf("parse tool registrations: %v", err)
	}

	usageByHandler, err := parseHandlerParamUsages(filepath.Join(mcpDir, "handlers.go"))
	if err != nil {
		t.Fatalf("parse handler parameter extraction: %v", err)
	}

	server := NewServer("/tmp", "/tmp/log.yaml", "")
	toolNames := server.ToolNames()
	sort.Strings(toolNames)

	if len(toolNames) < 18 {
		t.Fatalf("expected ~20 tools, got %d (%v)", len(toolNames), toolNames)
	}

	for _, toolName := range toolNames {
		tool, ok := server.GetTool(toolName)
		if !ok {
			t.Fatalf("tool %q not found", toolName)
		}

		handlerName, ok := toolHandlers[toolName]
		if !ok {
			t.Fatalf("tool %q not mapped to a handler in server.go registration", toolName)
		}

		handlerUsage, ok := usageByHandler[handlerName]
		if !ok {
			t.Fatalf("handler %q for tool %q not found in handlers.go", handlerName, toolName)
		}

		schemaRequired := newFieldSet(tool.InputSchema.Required...)

		schemaOnly := diffFieldSets(schemaRequired, handlerUsage.required)
		handlerOnly := diffFieldSets(handlerUsage.required, schemaRequired)
		if len(schemaOnly) > 0 || len(handlerOnly) > 0 {
			t.Errorf(
				"tool %q (handler %q) required-field mismatch:\n  schema only: %v\n  handler only: %v",
				toolName,
				handlerName,
				schemaOnly,
				handlerOnly,
			)
		}

		declaredSchemaFields := newFieldSet(tool.InputSchema.Required...)
		for name := range tool.InputSchema.Properties {
			declaredSchemaFields.add(name)
		}

		undeclaredExtracted := diffFieldSets(handlerUsage.extracted, declaredSchemaFields)
		if len(undeclaredExtracted) > 0 {
			t.Errorf(
				"tool %q (handler %q) extracts fields missing from schema declaration: %v",
				toolName,
				handlerName,
				undeclaredExtracted,
			)
		}
	}
}

func TestHandlerParamExtractionKnownPatterns(t *testing.T) {
	mcpDir := mcpSourceDir(t)
	usageByHandler, err := parseHandlerParamUsages(filepath.Join(mcpDir, "handlers.go"))
	if err != nil {
		t.Fatalf("parse handler parameter extraction: %v", err)
	}

	tests := []struct {
		handler           string
		required          []string
		extractedMustHave []string
	}{
		{
			handler:           "handleGet",
			required:          []string{"query"},
			extractedMustHave: []string{"format"},
		},
		{
			handler:           "handleAddTask",
			required:          []string{"id", "desc", "spec", "done", "scope"},
			extractedMustHave: []string{"agent_id", "priority", "depends", "type"},
		},
		{
			handler:           "handleClaimTask",
			required:          []string{"task_id", "agent_id"},
			extractedMustHave: []string{"task_id", "agent_id"},
		},
		{
			handler:           "handleMarkBlocked",
			required:          []string{"task_id", "agent_id", "reason", "questions"},
			extractedMustHave: []string{"questions"},
		},
		{
			handler:           "handleSubmitForReview",
			required:          []string{"task_id", "commit_sha", "agent_id"},
			extractedMustHave: []string{"task_id", "commit_sha", "agent_id"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.handler, func(t *testing.T) {
			usage, ok := usageByHandler[tt.handler]
			if !ok {
				t.Fatalf("handler %q not found", tt.handler)
			}

			wantRequired := newFieldSet(tt.required...)
			gotRequired := usage.required

			missingRequired := diffFieldSets(wantRequired, gotRequired)
			unexpectedRequired := diffFieldSets(gotRequired, wantRequired)
			if len(missingRequired) > 0 || len(unexpectedRequired) > 0 {
				t.Fatalf(
					"required fields mismatch:\n  missing: %v\n  unexpected: %v\n  got: %v",
					missingRequired,
					unexpectedRequired,
					gotRequired.sorted(),
				)
			}

			for _, field := range tt.extractedMustHave {
				if _, ok := usage.extracted[field]; ok {
					continue
				}
				t.Errorf("expected extracted fields to include %q, got %v", field, usage.extracted.sorted())
			}
		})
	}
}

func parseRegisteredToolHandlers(path string) (map[string]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}

	toolHandlers := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "registerTool" {
			return true
		}

		if len(call.Args) != 2 {
			return true
		}

		toolName, ok := extractRegisteredToolName(call.Args[0])
		if !ok {
			return true
		}

		handlerName := extractHandlerName(call.Args[1])
		if handlerName == "" {
			return true
		}

		toolHandlers[toolName] = handlerName
		return true
	})

	return toolHandlers, nil
}

func parseHandlerParamUsages(path string) (map[string]handlerParamUsage, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}

	helperFuncs := map[string]*ast.FuncDecl{}
	handlerFuncs := map[string]*ast.FuncDecl{}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if fn.Recv == nil {
			helperFuncs[fn.Name.Name] = fn
			continue
		}
		handlerFuncs[fn.Name.Name] = fn
	}

	helperRequired := map[string]fieldSet{}
	for helperName := range helperFuncs {
		resolveHelperRequiredFields(helperName, helperFuncs, helperRequired, map[string]bool{})
	}

	usageByHandler := map[string]handlerParamUsage{}
	for handlerName, fn := range handlerFuncs {
		usageByHandler[handlerName] = analyzeHandlerParamUsage(fn, helperRequired)
	}

	return usageByHandler, nil
}

func resolveHelperRequiredFields(
	helperName string,
	helperFuncs map[string]*ast.FuncDecl,
	cache map[string]fieldSet,
	visiting map[string]bool,
) fieldSet {
	if cached, ok := cache[helperName]; ok {
		return cached.clone()
	}
	if visiting[helperName] {
		return newFieldSet()
	}

	visiting[helperName] = true
	required := newFieldSet()

	fn, ok := helperFuncs[helperName]
	if ok && fn.Body != nil {
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			callName, args, ok := extractCall(call)
			if !ok {
				return true
			}

			if callName == "requireString" {
				if key, ok := parameterKeyArg(args); ok {
					required.add(key)
				}
				return true
			}

			if len(args) == 0 || !isParamsIdent(args[0]) {
				return true
			}

			required.union(resolveHelperRequiredFields(callName, helperFuncs, cache, visiting))
			return true
		})
	}

	delete(visiting, helperName)
	cache[helperName] = required.clone()
	return required
}

func analyzeHandlerParamUsage(fn *ast.FuncDecl, helperRequired map[string]fieldSet) handlerParamUsage {
	usage := handlerParamUsage{
		required:  newFieldSet(),
		extracted: newFieldSet(),
	}
	if fn == nil || fn.Body == nil {
		return usage
	}

	optionalSliceVars := map[string]string{}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			recordExtractStringSliceAssignment(node, usage.extracted, optionalSliceVars)

		case *ast.CallExpr:
			callName, args, ok := extractCall(node)
			if !ok {
				break
			}

			switch callName {
			case "requireString":
				if key, ok := parameterKeyArg(args); ok {
					usage.required.add(key)
					usage.extracted.add(key)
				}

			case "extractStringSlice":
				if key, ok := parameterKeyArg(args); ok {
					usage.extracted.add(key)
				}

			default:
				if len(args) == 0 || !isParamsIdent(args[0]) {
					break
				}
				if fields, ok := helperRequired[callName]; ok {
					usage.required.union(fields)
					usage.extracted.union(fields)
				}
			}

		case *ast.IndexExpr:
			if key, ok := paramsIndexKey(node); ok {
				usage.extracted.add(key)
			}

		case *ast.IfStmt:
			varName, ok := lenComparedToZero(node.Cond)
			if !ok {
				break
			}
			key, ok := optionalSliceVars[varName]
			if !ok {
				break
			}
			if ifReturnsError(node.Body) {
				usage.required.add(key)
				usage.extracted.add(key)
			}
		}
		return true
	})

	return usage
}

func recordExtractStringSliceAssignment(assign *ast.AssignStmt, extracted fieldSet, optionalSliceVars map[string]string) {
	if assign == nil || len(assign.Rhs) != 1 || len(assign.Lhs) == 0 {
		return
	}

	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok {
		return
	}

	callName, args, ok := extractCall(call)
	if !ok || callName != "extractStringSlice" {
		return
	}

	key, ok := parameterKeyArg(args)
	if !ok {
		return
	}
	extracted.add(key)

	ident, ok := assign.Lhs[0].(*ast.Ident)
	if !ok || ident.Name == "_" {
		return
	}
	optionalSliceVars[ident.Name] = key
}

func extractRegisteredToolName(expr ast.Expr) (string, bool) {
	composite, ok := expr.(*ast.CompositeLit)
	if !ok {
		return "", false
	}
	for _, elt := range composite.Elts {
		entry, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		keyIdent, ok := entry.Key.(*ast.Ident)
		if !ok || keyIdent.Name != "Name" {
			continue
		}
		return stringLiteral(entry.Value)
	}
	return "", false
}

func extractHandlerName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.SelectorExpr:
		return value.Sel.Name
	case *ast.Ident:
		return value.Name
	default:
		return ""
	}
}

func extractCall(call *ast.CallExpr) (string, []ast.Expr, bool) {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name, call.Args, true
	case *ast.SelectorExpr:
		return fn.Sel.Name, call.Args, true
	default:
		return "", nil, false
	}
}

func parameterKeyArg(args []ast.Expr) (string, bool) {
	if len(args) < 2 || !isParamsIdent(args[0]) {
		return "", false
	}
	return stringLiteral(args[1])
}

func paramsIndexKey(index *ast.IndexExpr) (string, bool) {
	if !isParamsIdent(index.X) {
		return "", false
	}
	return stringLiteral(index.Index)
}

func isParamsIdent(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "params"
}

func stringLiteral(expr ast.Expr) (string, bool) {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	unquoted, err := strconv.Unquote(literal.Value)
	if err != nil {
		return "", false
	}
	return unquoted, true
}

func lenComparedToZero(expr ast.Expr) (string, bool) {
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return "", false
	}

	switch binary.Op {
	case token.EQL, token.LEQ, token.LSS:
	default:
		return "", false
	}

	if name, ok := lenCallVar(binary.X); ok && isZeroLiteral(binary.Y) {
		return name, true
	}
	if name, ok := lenCallVar(binary.Y); ok && isZeroLiteral(binary.X) {
		return name, true
	}

	return "", false
}

func lenCallVar(expr ast.Expr) (string, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	fn, ok := call.Fun.(*ast.Ident)
	if !ok || fn.Name != "len" || len(call.Args) != 1 {
		return "", false
	}
	arg, ok := call.Args[0].(*ast.Ident)
	if !ok {
		return "", false
	}
	return arg.Name, true
}

func isZeroLiteral(expr ast.Expr) bool {
	literal, ok := expr.(*ast.BasicLit)
	return ok && literal.Kind == token.INT && literal.Value == "0"
}

func ifReturnsError(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}

	returnsErr := false
	ast.Inspect(body, func(n ast.Node) bool {
		stmt, ok := n.(*ast.ReturnStmt)
		if !ok || len(stmt.Results) == 0 {
			return true
		}
		last := stmt.Results[len(stmt.Results)-1]
		if isNil(last) {
			return true
		}
		returnsErr = true
		return false
	})

	return returnsErr
}

func isNil(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "nil"
}

// TestToolSchemaPropertiesNeverNil ensures all tools have a non-nil Properties map
// in their InputSchema. The Copilot CLI requires "properties": {} in JSON schema
// objects, so omitting it causes a 400 error.
func TestToolSchemaPropertiesNeverNil(t *testing.T) {
	server := NewServer("/tmp", "/tmp/log.yaml", "")
	for _, toolName := range server.ToolNames() {
		tool, ok := server.GetTool(toolName)
		if !ok {
			t.Fatalf("tool %q not found", toolName)
		}
		if tool.InputSchema.Properties == nil {
			t.Errorf("tool %q has nil Properties (must be non-nil for Copilot CLI compatibility)", toolName)
		}
	}
}

// TestToolSchemaArrayPropertiesHaveItems ensures all array-type properties
// include an Items field. The Copilot CLI requires "items": {"type": ...}
// on array schemas, or it returns a 400 error.
func TestToolSchemaArrayPropertiesHaveItems(t *testing.T) {
	server := NewServer("/tmp", "/tmp/log.yaml", "")
	for _, toolName := range server.ToolNames() {
		tool, ok := server.GetTool(toolName)
		if !ok {
			t.Fatalf("tool %q not found", toolName)
		}
		for propName, prop := range tool.InputSchema.Properties {
			if prop.Type == "array" && prop.Items == nil {
				t.Errorf("tool %q property %q is array but missing Items (required for Copilot CLI compatibility)", toolName, propName)
			}
		}
	}
}

func mcpSourceDir(t *testing.T) string {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(currentFile)
}
