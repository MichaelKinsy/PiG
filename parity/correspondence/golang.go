package correspondence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
)

func ExtractGo(ctx context.Context, root, targetCommit string) (*Inventory, error) {
	if root == "" || !commitHash(targetCommit) {
		return nil, fmt.Errorf("repository root and target commit are required")
	}
	repositoryPrefix, err := gitRepositoryPrefix(ctx, root)
	if err != nil {
		return nil, err
	}
	settings, err := extractGoSettings(ctx, root, repositoryPrefix, targetCommit)
	if err != nil {
		return nil, err
	}
	settings.ProductionCallbacks, err = extractGoSettingsProductionCallbacks(ctx, root, repositoryPrefix, targetCommit, settings.Items)
	if err != nil {
		return nil, err
	}
	constants, err := extractGoPromptConstants(ctx, root, repositoryPrefix, targetCommit)
	if err != nil {
		return nil, err
	}
	functions, err := extractGoCompactionFunctions(ctx, root, repositoryPrefix, targetCommit)
	if err != nil {
		return nil, err
	}
	settingsFunctions, err := extractGoSettingsManagerFunctions(ctx, root, repositoryPrefix, targetCommit)
	if err != nil {
		return nil, err
	}
	functions = append(functions, settingsFunctions...)
	orchestration, err := extractGoSettingsOrchestration(ctx, root, repositoryPrefix, targetCommit)
	if err != nil {
		return nil, err
	}
	functions = append(functions, orchestration)
	slices.SortFunc(functions, func(left, right Function) int { return strings.Compare(left.ID, right.ID) })
	if err := addGoFunctionCallers(ctx, root, repositoryPrefix, targetCommit, functions); err != nil {
		return nil, err
	}
	inventory := &Inventory{
		Source:    SourceIdentity{Language: LanguageGo, Revision: targetCommit},
		Tables:    []DataTable{settings},
		Constants: constants,
		Functions: functions,
	}
	if err := inventory.Validate(); err != nil {
		return nil, err
	}
	return inventory, nil
}

type goFunctionCallerSpec struct {
	function string
	path     string
	caller   string
	callee   string
}

func addGoFunctionCallers(ctx context.Context, root, repositoryPrefix, targetCommit string, functions []Function) error {
	specs := []goFunctionCallerSpec{
		{function: "compact", path: "coding/session.go", caller: "compact", callee: "compaction.Compact"},
		{function: "generateTurnPrefixSummary", path: "internal/codingagent/compaction/compaction.go", caller: "Compact", callee: "generateTurnPrefixSummary"},
		{function: "mergeSettings", path: "internal/codingagent/settings.go", caller: "Load", callee: "mergeSettings"},
		{function: "setProjectTrusted", path: "cmd/pig/main.go", caller: "main", callee: "services.SettingsManager().SetProjectTrusted"},
		{function: "reload", path: "internal/codingagent/interactive_commands.go", caller: "buildSlashContext", callee: "m.opts.SettingsManager.Reload"},
		{function: "persistScopedSettings", path: "internal/codingagent/settings.go", caller: "UpdateGlobal", callee: "saveSettingsPatch"},
		{function: "saveGlobal", path: "internal/codingagent/slash_session_handlers.go", caller: "settingsHandlerTUI", callee: "sc.SettingsManager.UpdateGlobal"},
		{function: "saveProject", path: "internal/codingagent/settings.go", caller: "SetProjectPackages", callee: "sm.UpdateProject"},
		{function: "settingsOrchestration", path: "internal/codingagent/slash_session_handlers.go", caller: "settingsHandler", callee: "settingsHandlerTUI"},
	}
	indexed := make(map[string]*Function, len(functions))
	for index := range functions {
		indexed[functions[index].Name] = &functions[index]
	}
	for _, spec := range specs {
		function := indexed[spec.function]
		if function == nil {
			return fmt.Errorf("Go correspondence caller target %s not found", spec.function)
		}
		file, source, files, err := parseGoFile(ctx, root, repositoryPrefix, targetCommit, spec.path)
		if err != nil {
			return err
		}
		caller := findGoFunction(file, spec.caller)
		if caller == nil {
			return fmt.Errorf("%s: correspondence caller %s not found", spec.path, spec.caller)
		}
		var matches []*ast.CallExpr
		ast.Inspect(caller.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if ok && goNodeSource(source, files, call.Fun) == spec.callee {
				matches = append(matches, call)
			}
			return true
		})
		if len(matches) != 1 {
			return fmt.Errorf("%s#%s has %d calls to %s, want one", spec.path, spec.caller, len(matches), spec.callee)
		}
		call := matches[0]
		callSource := goNodeSource(source, files, call)
		function.Callers = append(function.Callers, FunctionCaller{
			Path: spec.path, Symbol: spec.caller, Expression: callSource,
			StartLine: files.Position(call.Pos()).Line, SourceHash: hashString(callSource),
		})
	}
	return nil
}

func extractGoSettingsOrchestration(ctx context.Context, root, repositoryPrefix, targetCommit string) (Function, error) {
	const relativePath = "internal/codingagent/slash_session_handlers.go"
	file, source, files, err := parseGoFile(ctx, root, repositoryPrefix, targetCommit, relativePath)
	if err != nil {
		return Function{}, err
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "settingsHandlerTUI" || function.Body == nil {
			continue
		}
		return Function{
			ID: `function:` + relativePath + `#settingsHandlerTUI`, Path: relativePath,
			Name: "settingsOrchestration", Kind: "settings-orchestration", Async: false,
			CancellationInputs: []string{}, Calls: goFunctionCalls(function, source, files), Transitions: goFunctionTransitions(function, source, files), Callers: []FunctionCaller{},
			StartLine: files.Position(function.Pos()).Line, EndLine: files.Position(function.End()).Line,
			SourceHash: hashString(goNodeSource(source, files, function)),
		}, nil
	}
	return Function{}, fmt.Errorf("%s: settingsHandlerTUI function not found", relativePath)
}

func extractGoCompactionFunctions(ctx context.Context, root, repositoryPrefix, targetCommit string) ([]Function, error) {
	const relativePath = "internal/codingagent/compaction/compaction.go"
	file, source, files, err := parseGoFile(ctx, root, repositoryPrefix, targetCommit, relativePath)
	if err != nil {
		return nil, err
	}
	wanted := map[string]string{"Compact": "compact", "generateTurnPrefixSummary": "generateTurnPrefixSummary"}
	functions := make([]Function, 0, len(wanted))
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		normalizedName, wantedFunction := wanted[function.Name.Name]
		if !wantedFunction {
			continue
		}
		cancellationInputs := []string{}
		for _, field := range function.Type.Params.List {
			if goNodeSource(source, files, field.Type) != "context.Context" {
				continue
			}
			for _, name := range field.Names {
				cancellationInputs = append(cancellationInputs, name.Name)
			}
		}
		slices.Sort(cancellationInputs)
		functions = append(functions, Function{
			ID: `function:` + relativePath + `#` + function.Name.Name, Path: relativePath, Name: normalizedName,
			Kind: "compaction", Async: false, CancellationInputs: cancellationInputs, Calls: goFunctionCalls(function, source, files), Transitions: goFunctionTransitions(function, source, files), Callers: []FunctionCaller{},
			StartLine: files.Position(function.Pos()).Line, EndLine: files.Position(function.End()).Line,
			SourceHash: hashString(goNodeSource(source, files, function)),
		})
	}
	if len(functions) != len(wanted) {
		return nil, fmt.Errorf("%s: compaction function extraction incomplete", relativePath)
	}
	slices.SortFunc(functions, func(left, right Function) int { return strings.Compare(left.ID, right.ID) })
	return functions, nil
}

func extractGoSettingsManagerFunctions(ctx context.Context, root, repositoryPrefix, targetCommit string) ([]Function, error) {
	const relativePath = "internal/codingagent/settings.go"
	file, source, files, err := parseGoFile(ctx, root, repositoryPrefix, targetCommit, relativePath)
	if err != nil {
		return nil, err
	}
	wanted := map[string]string{
		"mergeSettings": "mergeSettings", "SetProjectTrusted": "setProjectTrusted", "Reload": "reload",
		"ApplyOverrides": "applyOverrides", "saveSettingsPatch": "persistScopedSettings",
		"UpdateGlobal": "saveGlobal", "UpdateProject": "saveProject",
	}
	functions := make([]Function, 0, len(wanted))
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		normalizedName, found := wanted[function.Name.Name]
		if !found {
			continue
		}
		functions = append(functions, Function{
			ID: `function:` + relativePath + `#` + function.Name.Name, Path: relativePath, Name: normalizedName,
			Kind: "settings-manager", Async: false, CancellationInputs: []string{}, Calls: goFunctionCalls(function, source, files), Transitions: goFunctionTransitions(function, source, files), Callers: []FunctionCaller{},
			StartLine: files.Position(function.Pos()).Line, EndLine: files.Position(function.End()).Line,
			SourceHash: hashString(goNodeSource(source, files, function)),
		})
	}
	if len(functions) != len(wanted) {
		return nil, fmt.Errorf("%s: settings manager function extraction incomplete", relativePath)
	}
	slices.SortFunc(functions, func(left, right Function) int { return strings.Compare(left.ID, right.ID) })
	return functions, nil
}

func goFunctionCalls(function *ast.FuncDecl, source []byte, files *token.FileSet) []FunctionCall {
	return goNodeCalls(function.Body, source, files)
}

func goNodeCalls(root ast.Node, source []byte, files *token.FileSet) []FunctionCall {
	calls := []FunctionCall{}
	conditions := []string{}
	var visit func(ast.Node)
	visit = func(node ast.Node) {
		if node == nil {
			return
		}
		if statement, ok := node.(*ast.IfStmt); ok {
			if statement.Init != nil {
				visit(statement.Init)
			}
			visit(statement.Cond)
			condition := goNodeSource(source, files, statement.Cond)
			conditions = append(conditions, condition)
			visit(statement.Body)
			conditions = conditions[:len(conditions)-1]
			if statement.Else != nil {
				conditions = append(conditions, "else:"+condition)
				visit(statement.Else)
				conditions = conditions[:len(conditions)-1]
			}
			return
		}
		if call, ok := node.(*ast.CallExpr); ok {
			arguments := make([]string, len(call.Args))
			for index, argument := range call.Args {
				arguments[index] = goNodeSource(source, files, argument)
			}
			calls = append(calls, FunctionCall{
				Ordinal: len(calls) + 1, Callee: goNodeSource(source, files, call.Fun), Awaited: false,
				Arguments: arguments, Conditions: slices.Clone(conditions), StartLine: files.Position(call.Pos()).Line,
			})
		}
		ast.Inspect(node, func(child ast.Node) bool {
			if child == nil || child == node {
				return true
			}
			visit(child)
			return false
		})
	}
	visit(root)
	return calls
}

func goFunctionTransitions(function *ast.FuncDecl, source []byte, files *token.FileSet) []FunctionTransition {
	return goNodeTransitions(function.Body, source, files)
}

func goNodeTransitions(root ast.Node, source []byte, files *token.FileSet) []FunctionTransition {
	transitions := []FunctionTransition{}
	conditions := []string{}
	add := func(kind, target, expression string, node ast.Node) {
		transitions = append(transitions, FunctionTransition{
			Ordinal: len(transitions) + 1, Kind: kind, Target: target, Expression: expression,
			Conditions: slices.Clone(conditions), StartLine: files.Position(node.Pos()).Line,
		})
	}
	var visit func(ast.Node)
	visit = func(node ast.Node) {
		if node == nil {
			return
		}
		if statement, ok := node.(*ast.IfStmt); ok {
			if statement.Init != nil {
				visit(statement.Init)
			}
			visit(statement.Cond)
			condition := goNodeSource(source, files, statement.Cond)
			conditions = append(conditions, condition)
			visit(statement.Body)
			conditions = conditions[:len(conditions)-1]
			if statement.Else != nil {
				conditions = append(conditions, "else:"+condition)
				visit(statement.Else)
				conditions = conditions[:len(conditions)-1]
			}
			return
		}
		switch value := node.(type) {
		case *ast.AssignStmt:
			kind := "update"
			if value.Tok == token.DEFINE {
				kind = "bind"
			}
			for index, target := range value.Lhs {
				expression := goNodeSource(source, files, value)
				if len(value.Lhs) == len(value.Rhs) {
					expression = goNodeSource(source, files, value.Rhs[index])
				}
				add(kind, goNodeSource(source, files, target), expression, value)
			}
		case *ast.DeclStmt:
			declaration, ok := value.Decl.(*ast.GenDecl)
			if ok {
				for _, specification := range declaration.Specs {
					item, ok := specification.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for index, target := range item.Names {
						expression := goNodeSource(source, files, item)
						if len(item.Names) == len(item.Values) {
							expression = goNodeSource(source, files, item.Values[index])
						}
						add("bind", target.Name, expression, item)
					}
				}
			}
		case *ast.IncDecStmt:
			add("update", goNodeSource(source, files, value.X), goNodeSource(source, files, value), value)
		case *ast.ReturnStmt:
			expressions := make([]string, len(value.Results))
			for index, result := range value.Results {
				expressions[index] = goNodeSource(source, files, result)
			}
			expression := strings.Join(expressions, ", ")
			if expression == "" {
				expression = "return"
			}
			add("return", "", expression, value)
		}
		ast.Inspect(node, func(child ast.Node) bool {
			if child == nil || child == node {
				return true
			}
			visit(child)
			return false
		})
	}
	visit(root)
	return transitions
}

func extractGoSettingsProductionCallbacks(ctx context.Context, root, repositoryPrefix, targetCommit string, items []DataItem) ([]ProductionCallback, error) {
	const handlerPath = "internal/codingagent/slash_session_handlers.go"
	handlerFile, handlerSource, handlerFiles, err := parseGoFile(ctx, root, repositoryPrefix, targetCommit, handlerPath)
	if err != nil {
		return nil, err
	}
	handler := findGoFunction(handlerFile, "settingsHandlerTUI")
	if handler == nil {
		return nil, fmt.Errorf("%s: settingsHandlerTUI function not found", handlerPath)
	}
	persistenceCalls := findGoCallStatements(handler.Body, "UpdateGlobal")
	dispatchCalls := findGoCallStatements(handler.Body, "OnSettingApplied")
	warningsPersistence := findGoCallStatements(handler.Body, "SetWarnings")
	if len(persistenceCalls) != 1 || len(dispatchCalls) != 2 || len(warningsPersistence) != 1 {
		return nil, fmt.Errorf("%s: settingsHandlerTUI persistence or callback dispatch not found", handlerPath)
	}
	persistenceSegment := goEffectSegment("persistence", handlerPath, persistenceCalls[0], handlerSource, handlerFiles)
	dispatchSegment := goEffectSegment("runtime-dispatch", handlerPath, dispatchCalls[1], handlerSource, handlerFiles)
	warningsPersistenceSegment := goEffectSegment("persistence", handlerPath, warningsPersistence[0], handlerSource, handlerFiles)
	warningsDispatchSegment := goEffectSegment("runtime-dispatch", handlerPath, dispatchCalls[0], handlerSource, handlerFiles)

	const runtimePath = "internal/codingagent/interactive_commands.go"
	runtimeFile, runtimeSource, runtimeFiles, err := parseGoFile(ctx, root, repositoryPrefix, targetCommit, runtimePath)
	if err != nil {
		return nil, err
	}
	runtimeFunction := findGoFunction(runtimeFile, "buildSlashContext")
	if runtimeFunction == nil {
		return nil, fmt.Errorf("%s: buildSlashContext function not found", runtimePath)
	}
	runtimeLiteral := findGoFieldFunctionLiteral(runtimeFunction.Body, "OnSettingApplied")
	if runtimeLiteral == nil {
		return nil, fmt.Errorf("%s: OnSettingApplied callback not found", runtimePath)
	}
	runtimeCases := make(map[string]*ast.CaseClause)
	var runtimeSwitch *ast.SwitchStmt
	ast.Inspect(runtimeLiteral.Body, func(node ast.Node) bool {
		statement, ok := node.(*ast.SwitchStmt)
		if !ok || goNodeSource(runtimeSource, runtimeFiles, statement.Tag) != "id" {
			return true
		}
		runtimeSwitch = statement
		for _, item := range statement.Body.List {
			clause := item.(*ast.CaseClause)
			for _, expression := range clause.List {
				literal, ok := expression.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				id, unquoteErr := strconv.Unquote(literal.Value)
				if unquoteErr == nil {
					runtimeCases[id] = clause
				}
			}
		}
		return false
	})
	if runtimeSwitch == nil {
		return nil, fmt.Errorf("%s: OnSettingApplied id switch not found", runtimePath)
	}
	var common ast.Node
	for _, statement := range runtimeLiteral.Body.List {
		if statement.Pos() <= runtimeSwitch.End() || !goNodeHasCall(statement, "Get") {
			continue
		}
		common = statement
		break
	}
	if common == nil {
		return nil, fmt.Errorf("%s: OnSettingApplied common settings refresh not found", runtimePath)
	}
	commonSegment := goEffectSegment("runtime-common", runtimePath, common, runtimeSource, runtimeFiles)

	callbacks := make([]ProductionCallback, 0, len(items))
	for _, item := range items {
		segments := []EffectSegment{persistenceSegment, dispatchSegment}
		if item.ID == "warnings" {
			segments = []EffectSegment{warningsPersistenceSegment, warningsDispatchSegment}
		}
		if runtimeCase := runtimeCases[item.ID]; runtimeCase != nil {
			segments = append(segments, goEffectSegment("runtime-case", runtimePath, runtimeCase, runtimeSource, runtimeFiles))
		}
		segments = append(segments, commonSegment)
		callbacks = append(callbacks, ProductionCallback{ID: item.ID, Handler: "OnSettingApplied", Segments: segments})
	}
	slices.SortFunc(callbacks, func(left, right ProductionCallback) int { return strings.Compare(left.ID, right.ID) })
	return callbacks, nil
}

func findGoFunction(file *ast.File, name string) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name && function.Body != nil {
			return function
		}
	}
	return nil
}

func findGoCallStatements(root ast.Node, terminal string) []ast.Node {
	var result []ast.Node
	ast.Inspect(root, func(node ast.Node) bool {
		if node == nil {
			return false
		}
		switch statement := node.(type) {
		case *ast.IfStmt:
			if goNodeHasCall(statement.Init, terminal) || goNodeHasCall(statement.Cond, terminal) {
				result = append(result, node)
				return false
			}
		case *ast.ExprStmt:
			if goNodeHasCall(statement.X, terminal) {
				result = append(result, node)
				return false
			}
		}
		return true
	})
	return result
}

func findGoFieldFunctionLiteral(root ast.Node, fieldName string) *ast.FuncLit {
	var result *ast.FuncLit
	ast.Inspect(root, func(node ast.Node) bool {
		if result != nil {
			return false
		}
		field, ok := node.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		name, ok := field.Key.(*ast.Ident)
		if !ok || name.Name != fieldName {
			return true
		}
		result, _ = field.Value.(*ast.FuncLit)
		return false
	})
	return result
}

func goNodeHasCall(root ast.Node, terminal string) bool {
	if root == nil {
		return false
	}
	found := false
	ast.Inspect(root, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch function := call.Fun.(type) {
		case *ast.Ident:
			found = function.Name == terminal
		case *ast.SelectorExpr:
			found = function.Sel.Name == terminal
		}
		return !found
	})
	return found
}

func goEffectSegment(role, path string, node ast.Node, source []byte, files *token.FileSet) EffectSegment {
	effects := goSemanticEffects(node, source, files)
	return EffectSegment{
		Role: role, Path: path,
		StartLine: files.Position(node.Pos()).Line, EndLine: files.Position(node.End()).Line,
		SourceHash: hashString(goNodeSource(source, files, node)), Reads: effects.Reads, Writes: effects.Writes,
		Calls: goNodeCalls(node, source, files), Transitions: goNodeTransitions(node, source, files),
	}
}

func extractGoSettings(ctx context.Context, root, repositoryPrefix, targetCommit string) (DataTable, error) {
	const relativePath = "internal/codingagent/slash_session_handlers.go"
	file, source, files, err := parseGoFile(ctx, root, repositoryPrefix, targetCommit, relativePath)
	if err != nil {
		return DataTable{}, err
	}
	var function *ast.FuncDecl
	for _, declaration := range file.Decls {
		candidate, ok := declaration.(*ast.FuncDecl)
		if ok && candidate.Name.Name == "settingsItems" {
			function = candidate
			break
		}
	}
	if function == nil || function.Body == nil {
		return DataTable{}, fmt.Errorf("%s: settingsItems function not found", relativePath)
	}
	var literal *ast.CompositeLit
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if literal != nil {
			return false
		}
		statement, ok := node.(*ast.ReturnStmt)
		if !ok || len(statement.Results) != 1 {
			return true
		}
		literal, _ = statement.Results[0].(*ast.CompositeLit)
		return literal == nil
	})
	if literal == nil {
		return DataTable{}, fmt.Errorf("%s: settingsItems return literal not found", relativePath)
	}
	items := make([]DataItem, 0, len(literal.Elts))
	callbacks := make([]DispatchCase, 0, len(literal.Elts))
	for _, element := range literal.Elts {
		itemLiteral, ok := element.(*ast.CompositeLit)
		if !ok {
			return DataTable{}, fmt.Errorf("%s: settingsItems contains non-literal item", relativePath)
		}
		fields := keyedGoFields(itemLiteral)
		id, err := goStringField(fields, "id")
		if err != nil {
			return DataTable{}, fmt.Errorf("%s: %w", relativePath, err)
		}
		label, err := goStringField(fields, "label")
		if err != nil {
			return DataTable{}, fmt.Errorf("%s: setting %s: %w", relativePath, id, err)
		}
		description, descriptionExpression, err := goStringOrExpression(fields["desc"], source, files)
		if err != nil {
			return DataTable{}, fmt.Errorf("%s: setting %s: %w", relativePath, id, err)
		}
		values, valuesExpression, err := goStringSlice(fields["values"], source, files)
		if err != nil {
			return DataTable{}, fmt.Errorf("%s: setting %s values: %w", relativePath, id, err)
		}
		get := fields["get"]
		apply := fields["apply"]
		if get == nil || apply == nil {
			return DataTable{}, fmt.Errorf("%s: setting %s lacks get or apply", relativePath, id)
		}
		gate := ""
		if fields["gated"] != nil {
			gate = goNodeSource(source, files, fields["gated"])
		}
		itemSource := goNodeSource(source, files, itemLiteral)
		currentEffects := goSemanticEffects(get, source, files)
		items = append(items, DataItem{
			ID: id, Label: label, Description: description, DescriptionExpression: descriptionExpression,
			CurrentValueExpression: goNodeSource(source, files, get),
			CurrentReads:           currentEffects.Reads,
			CurrentWrites:          currentEffects.Writes,
			CurrentCalls:           currentEffects.Calls,
			Values:                 values, ValuesExpression: valuesExpression,
			Gate: gate, Path: relativePath,
			StartLine:  files.Position(itemLiteral.Pos()).Line,
			EndLine:    files.Position(itemLiteral.End()).Line,
			SourceHash: hashString(itemSource),
		})
		callback := goSemanticEffects(apply, source, files)
		callback.ID = id
		callbacks = append(callbacks, callback)
	}
	slices.SortFunc(callbacks, func(left, right DispatchCase) int { return strings.Compare(left.ID, right.ID) })
	return DataTable{
		ID: "table:settings-selector", Path: relativePath, Owner: "settingsItems",
		SourceHash: hashString(goNodeSource(source, files, function)), OrderProfile: "all-capabilities",
		Items: items, Callbacks: callbacks, ProductionCallbacks: []ProductionCallback{},
	}, nil
}

func extractGoPromptConstants(ctx context.Context, root, repositoryPrefix, targetCommit string) ([]Constant, error) {
	paths := []string{
		"internal/codingagent/compaction/compaction.go",
		"internal/codingagent/compaction/utils.go",
	}
	var constants []Constant
	for _, relativePath := range paths {
		file, source, files, err := parseGoFile(ctx, root, repositoryPrefix, targetCommit, relativePath)
		if err != nil {
			return nil, err
		}
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, specification := range general.Specs {
				value, ok := specification.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for index, name := range value.Names {
					if !strings.Contains(strings.ToLower(name.Name), "prompt") {
						continue
					}
					if index >= len(value.Values) {
						return nil, fmt.Errorf("%s: prompt %s has no explicit value", relativePath, name.Name)
					}
					literal, ok := value.Values[index].(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						return nil, fmt.Errorf("%s: prompt %s is not a static string", relativePath, name.Name)
					}
					decoded, err := strconv.Unquote(literal.Value)
					if err != nil {
						return nil, fmt.Errorf("%s: prompt %s: %w", relativePath, name.Name, err)
					}
					constants = append(constants, Constant{
						ID:   `constant:` + relativePath + `#` + name.Name,
						Path: relativePath, Name: name.Name, Value: decoded,
						UTF16Length: len(utf16.Encode([]rune(decoded))),
						SourceHash:  hashString(goNodeSource(source, files, value)), ValueHash: hashString(decoded),
						StartLine: files.Position(value.Pos()).Line, EndLine: files.Position(value.End()).Line,
					})
				}
			}
		}
	}
	slices.SortFunc(constants, func(left, right Constant) int { return strings.Compare(left.ID, right.ID) })
	return constants, nil
}

func parseGoFile(ctx context.Context, root, repositoryPrefix, targetCommit, relativePath string) (*ast.File, []byte, *token.FileSet, error) {
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	object, err := resolveCommitPath(ctx, root, targetCommit, repositoryPrefix, relativePath)
	if err != nil {
		return nil, nil, nil, err
	}
	command := exec.CommandContext(ctx, "git", "show", object)
	command.Dir = root
	stdout := newBoundedBuffer(extractorOutputLimit)
	stderr := newBoundedBuffer(extractorOutputLimit)
	command.Stdout = stdout
	command.Stderr = stderr
	err = command.Run()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read %s at %s: %w: %s", relativePath, targetCommit, err, stderr.String())
	}
	if stdout.overflow || stderr.overflow {
		return nil, nil, nil, fmt.Errorf("read %s at %s exceeded %d bytes", relativePath, targetCommit, extractorOutputLimit)
	}
	source := stdout.Bytes()
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, path, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse %s: %w", relativePath, err)
	}
	return file, source, files, nil
}

func gitRepositoryPrefix(ctx context.Context, root string) (string, error) {
	command := exec.CommandContext(ctx, "git", "rev-parse", "--show-prefix")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("resolve repository prefix: %w", err)
	}
	prefix := strings.TrimSpace(string(output))
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return prefix, nil
}

func commitHash(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, digit := range value {
		if !strings.ContainsRune("0123456789abcdef", digit) {
			return false
		}
	}
	return true
}

func keyedGoFields(literal *ast.CompositeLit) map[string]ast.Expr {
	fields := make(map[string]ast.Expr, len(literal.Elts))
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		name, ok := field.Key.(*ast.Ident)
		if ok {
			fields[name.Name] = field.Value
		}
	}
	return fields
}

func goStringField(fields map[string]ast.Expr, name string) (string, error) {
	literal, ok := fields[name].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", fmt.Errorf("field %s is not a string literal", name)
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return "", err
	}
	return value, nil
}

func goStringOrExpression(expression ast.Expr, source []byte, files *token.FileSet) (string, string, error) {
	if expression == nil {
		return "", "", fmt.Errorf("missing string expression")
	}
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", goNodeSource(source, files, expression), nil
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return "", "", err
	}
	return value, "", nil
}

func goStringSlice(expression ast.Expr, source []byte, files *token.FileSet) ([]string, string, error) {
	literal, ok := expression.(*ast.CompositeLit)
	if !ok {
		if expression == nil {
			return nil, "", fmt.Errorf("missing values")
		}
		return []string{}, goNodeSource(source, files, expression), nil
	}
	values := make([]string, 0, len(literal.Elts))
	for _, element := range literal.Elts {
		item, ok := element.(*ast.BasicLit)
		if !ok || item.Kind != token.STRING {
			return nil, "", fmt.Errorf("contains non-string literal")
		}
		value, err := strconv.Unquote(item.Value)
		if err != nil {
			return nil, "", err
		}
		values = append(values, value)
	}
	return values, "", nil
}

func goSemanticEffects(node ast.Node, source []byte, files *token.FileSet) DispatchCase {
	calls := make(map[string]struct{})
	writes := make(map[string]struct{})
	writePositions := make(map[token.Pos]struct{})
	reads := make(map[string]struct{})
	ast.Inspect(node, func(candidate ast.Node) bool {
		switch value := candidate.(type) {
		case *ast.CallExpr:
			call := value
			calls[goNodeSource(source, files, call.Fun)] = struct{}{}
		case *ast.AssignStmt:
			for _, expression := range value.Lhs {
				if identifier, ok := expression.(*ast.Ident); ok && identifier.Name == "_" {
					continue
				}
				switch expression.(type) {
				case *ast.Ident, *ast.SelectorExpr, *ast.IndexExpr:
					writes[goNodeSource(source, files, expression)] = struct{}{}
					writePositions[expression.Pos()] = struct{}{}
				}
			}
		case *ast.IncDecStmt:
			switch value.X.(type) {
			case *ast.Ident, *ast.SelectorExpr, *ast.IndexExpr:
				writes[goNodeSource(source, files, value.X)] = struct{}{}
				writePositions[value.X.Pos()] = struct{}{}
			}
		}
		return true
	})
	ast.Inspect(node, func(candidate ast.Node) bool {
		selector, ok := candidate.(*ast.SelectorExpr)
		if ok {
			if _, written := writePositions[selector.Pos()]; !written {
				reads[goNodeSource(source, files, selector)] = struct{}{}
			}
		}
		return true
	})
	return DispatchCase{Reads: sortedMapKeys(reads), Writes: sortedMapKeys(writes), Calls: sortedMapKeys(calls)}
}

func sortedMapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func goNodeSource(source []byte, files *token.FileSet, node ast.Node) string {
	file := files.File(node.Pos())
	if file == nil {
		return ""
	}
	start := file.Offset(node.Pos())
	end := file.Offset(node.End())
	return string(source[start:end])
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}
