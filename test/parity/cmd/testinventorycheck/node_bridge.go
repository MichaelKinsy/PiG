package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// nodeBridgeHarnessPath is the Go test that runs the vendored upstream TUI tests against the shipped Node bridge.
const nodeBridgeHarnessPath = "coding/extension/host/subprocess/runtime_node_tui_upstream_test.go"

const nodeHarnessTest = "TestNodeVendoredTuiUpstreamTests"

const nodeHarnessEvidence = nodeBridgeHarnessPath + "#" + nodeHarnessTest

// citesNodeHarness reports whether an evidence reference can resolve to TestNodeVendoredTuiUpstreamTests. checkReferences accepts a reference to the harness file when its fragment is missing or occurs anywhere in the file, so a missing fragment, or one that occurs in the harness function's source (its name, a subtest name, or any other text of its body), is a harness citation. A fragment that occurs only outside that function, such as a sibling test's name, is not.
func citesNodeHarness(evidence []string, harnessFunc string) bool {
	return slices.ContainsFunc(evidence, func(reference string) bool {
		path, fragment, _ := strings.Cut(reference, "#")
		return path == nodeBridgeHarnessPath && strings.Contains(harnessFunc, fragment)
	})
}

// nodeHarness is the parsed TestNodeVendoredTuiUpstreamTests: the upstream test names it loops over, its source text, and the syntax nodes that decide whether each name runs.
type nodeHarness struct {
	names  []string
	source string
	fn     *ast.FuncDecl
	loop   *ast.RangeStmt
	value  string
}

// parseNodeHarness parses the Go source and reads the string-literal list of the loop in TestNodeVendoredTuiUpstreamTests that passes its range value to t.Run, so comments, strings and unrelated code elsewhere in the file cannot stand in for the executable registration.
func parseNodeHarness(harness []byte) (nodeHarness, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, nodeBridgeHarnessPath, harness, parser.SkipObjectResolution)
	if err != nil {
		return nodeHarness{}, fmt.Errorf("parse %s: %w", nodeBridgeHarnessPath, err)
	}
	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if candidate, ok := decl.(*ast.FuncDecl); ok && candidate.Recv == nil && candidate.Name.Name == nodeHarnessTest && candidate.Body != nil {
			fn = candidate
		}
	}
	if fn == nil {
		return nodeHarness{}, fmt.Errorf("cannot find %s in %s", nodeHarnessTest, nodeBridgeHarnessPath)
	}
	h := nodeHarness{fn: fn, source: string(harness[fset.Position(fn.Pos()).Offset:fset.Position(fn.End()).Offset])}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		loop, ok := n.(*ast.RangeStmt)
		if !ok || h.loop != nil {
			return h.loop == nil
		}
		list, ok := loop.X.(*ast.CompositeLit)
		value, isIdent := loop.Value.(*ast.Ident)
		if !ok || !isIdent || runSubtestNamed(loop.Body, value.Name) == nil {
			return true
		}
		h.loop, h.value = loop, value.Name
		for _, element := range list.Elts {
			literal, ok := element.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			if name, err := strconv.Unquote(literal.Value); err == nil {
				h.names = append(h.names, name)
			}
		}
		return false
	})
	if h.loop == nil {
		return nodeHarness{}, fmt.Errorf("cannot find the upstream test list looped over with t.Run in %s", nodeHarnessTest)
	}
	return h, nil
}

// isRunOf reports whether call is t.Run with the identifier as its first argument, where t is the test's own *testing.T; another receiver's Run does not start a subtest.
func isRunOf(call *ast.CallExpr, name string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Run" || len(call.Args) == 0 {
		return false
	}
	if receiver, isIdent := selector.X.(*ast.Ident); !isIdent || receiver.Name != "t" {
		return false
	}
	arg, isIdent := call.Args[0].(*ast.Ident)
	return isIdent && arg.Name == name
}

// runSubtestNamed returns the first call in body to a Run method with the identifier as its first argument, or nil.
func runSubtestNamed(body ast.Node, name string) *ast.CallExpr {
	var run *ast.CallExpr
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && run == nil && isRunOf(call, name) {
			run = call
		}
		return run == nil
	})
	return run
}

// isSkip reports whether selector names a testing Skip, SkipNow or Skipf method, whether it is called directly or taken as a method value and called later.
func isSkip(selector *ast.SelectorExpr) bool {
	return selector.Sel.Name == "Skip" || selector.Sel.Name == "SkipNow" || selector.Sel.Name == "Skipf"
}

// escapeWalk reports each construct under a node that can end a test before it runs Node. A Skip selector, or a use of the test's `t` as a value (passing it to a helper that may skip), is reported anywhere, including in nested function literals, which may be invoked. A return or goto, and outside the subtest function any branch statement, is reported only outside nested function literals, where it leaves the test function itself. Inside the subtest function a skip, return or goto carries the `<value> == "<name>"` branches that enclose it.
type escapeWalk struct {
	value   string
	subtest bool
	skip    *ast.FuncLit
	report  func(kind string, gates []string)
}

func (w escapeWalk) walk(n ast.Node, gates []string, nested bool) {
	ast.Inspect(n, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			if n != w.skip {
				w.walk(n.Body, gates, true)
			}
			return false
		case *ast.Field:
			return false
		case *ast.IfStmt:
			if !w.subtest {
				return true
			}
			if n.Init != nil {
				w.walk(n.Init, gates, nested)
			}
			w.walk(n.Cond, gates, nested)
			inner := gates
			if name, ok := nameGate(n.Cond, w.value); ok {
				inner = append(slices.Clone(gates), name)
			}
			w.walk(n.Body, inner, nested)
			if n.Else != nil {
				w.walk(n.Else, gates, nested)
			}
			return false
		case *ast.ReturnStmt:
			if !nested {
				w.report("return", gates)
			}
		case *ast.BranchStmt:
			if !nested && (!w.subtest || n.Tok == token.GOTO) {
				w.report(n.Tok.String(), gates)
			}
		case *ast.SelectorExpr:
			if isSkip(n) {
				w.report("skip", gates)
			}
			if receiver, ok := n.X.(*ast.Ident); !ok || receiver.Name != "t" {
				w.walk(n.X, gates, nested)
			}
			return false
		case *ast.Ident:
			if n.Name == "t" {
				w.report("passing t", gates)
			}
		}
		return true
	})
}

// valueRebindings reports each place in the test that assigns, redeclares or takes the address of the loop value, so a registered name could run another upstream file, or none, under its own subtest name.
func (h nodeHarness) valueRebindings() []string {
	var problems []string
	rebound := func(expr ast.Expr, how string) {
		if ident, ok := expr.(*ast.Ident); ok && ident.Name == h.value {
			problems = append(problems, fmt.Sprintf("%s: the loop value %q is %s, so a registered name can run another upstream file or none", nodeHarnessTest, h.value, how))
		}
	}
	ast.Inspect(h.fn.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				rebound(lhs, "reassigned or shadowed")
			}
		case *ast.RangeStmt:
			if n != h.loop {
				rebound(n.Key, "shadowed")
				rebound(n.Value, "shadowed")
			}
		case *ast.ValueSpec:
			for _, name := range n.Names {
				rebound(name, "shadowed")
			}
		case *ast.Field:
			for _, name := range n.Names {
				rebound(name, "shadowed")
			}
		case *ast.UnaryExpr:
			if n.Op == token.AND {
				rebound(n.X, "addressed")
			}
		}
		return true
	})
	return problems
}

// nameGate returns the string literal when cond is exactly `<value> == "<literal>"`.
func nameGate(cond ast.Expr, value string) (string, bool) {
	binary, ok := cond.(*ast.BinaryExpr)
	if !ok || binary.Op != token.EQL {
		return "", false
	}
	for _, pair := range [][2]ast.Expr{{binary.X, binary.Y}, {binary.Y, binary.X}} {
		ident, isIdent := pair[0].(*ast.Ident)
		literal, isLiteral := pair[1].(*ast.BasicLit)
		if !isIdent || ident.Name != value || !isLiteral || literal.Kind != token.STRING {
			continue
		}
		if name, err := strconv.Unquote(literal.Value); err == nil {
			return name, true
		}
	}
	return "", false
}

// callsMethod reports whether stmt is a statement whose top-level expression is a call to the named method, as `x := f.M()` or `f.M()`.
func callsMethod(stmt ast.Stmt, method string) bool {
	var expr ast.Expr
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		expr = s.X
	case *ast.AssignStmt:
		if len(s.Rhs) == 1 {
			expr = s.Rhs[0]
		}
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == method
}

// executionProblems reports harness structure that can leave a registered upstream test name without an executed Node run while `go test` still exits 0. Registration proves a name is in the list, not that its subtest runs and reaches Node. The harness is therefore held to a shape in which the only per-name escape is a `<value> == "<name>"` branch for a name whose mapping row is not ported: the loop is a direct statement of the test, t.Run is a direct statement of the loop, nothing outside the subtest returns, branches, skips or hands t to other code, the subtest runs Node unconditionally, a skip, return, goto or use of t as a value inside the subtest sits in the body (not the else) of such a name branch, and the loop value is never rebound. A `t.Skip`, skipping helper, `continue`, `goto`, `return` or renamed file for a ported row therefore fails the gate.
func (h nodeHarness) executionProblems(byPath map[string]mappingEntry) []string {
	var problems []string
	if !slices.Contains(h.fn.Body.List, ast.Stmt(h.loop)) {
		problems = append(problems, nodeHarnessTest+": the upstream test loop is nested in another statement, so its subtests can be skipped as a group")
	}
	var run *ast.CallExpr
	for _, stmt := range h.loop.Body.List {
		if expr, ok := stmt.(*ast.ExprStmt); ok {
			if call, ok := expr.X.(*ast.CallExpr); ok && isRunOf(call, h.value) {
				run = call
			}
		}
	}
	if run == nil {
		return append(problems, nodeHarnessTest+": t.Run is not a direct statement of the upstream test loop, so a condition can skip a name")
	}
	closure, ok := run.Args[len(run.Args)-1].(*ast.FuncLit)
	if len(run.Args) != 2 || !ok {
		return append(problems, nodeHarnessTest+": t.Run does not take an inline subtest function")
	}
	// Outside the subtest function nothing may return, branch, skip or hand t to other code. Other function literals may be invoked, so a skip in them counts, but their returns and branches never end this test.
	outside := escapeWalk{value: h.value, skip: closure, report: func(kind string, _ []string) {
		switch kind {
		case "return", "skip", "passing t":
		default:
			kind = "branch statement"
		}
		problems = append(problems, nodeHarnessTest+": "+kind+" outside the subtest function skips upstream subtests")
	}}
	outside.walk(h.fn.Body, nil, false)
	runsNode := slices.ContainsFunc(closure.Body.List, func(stmt ast.Stmt) bool { return callsMethod(stmt, "CombinedOutput") })
	if !runsNode {
		problems = append(problems, nodeHarnessTest+": the subtest function does not run Node (cmd.CombinedOutput) unconditionally")
	}
	inside := escapeWalk{value: h.value, subtest: true, report: func(kind string, gates []string) {
		problems = append(problems, h.gateProblem(kind, gates, byPath)...)
	}}
	inside.walk(closure.Body, nil, false)
	return append(problems, h.valueRebindings()...)
}

// gateProblem reports a return, goto, skip or use of t as a value in the subtest function that applies to a ported row or to every name.
func (h nodeHarness) gateProblem(kind string, gates []string, byPath map[string]mappingEntry) []string {
	if len(gates) == 0 {
		return []string{fmt.Sprintf("%s: %s in the subtest function applies to every upstream test, so no name is guaranteed to run", nodeHarnessTest, kind)}
	}
	var problems []string
	for _, name := range gates {
		if byPath["packages/tui/test/"+name+".test.ts"].Disposition == "ported" {
			problems = append(problems, fmt.Sprintf("%s: %s for %q lets the ported row pass without executing its upstream tests", nodeHarnessTest, kind, name))
		}
	}
	return problems
}

// nodeBridgeHarnessProblems compares the upstream TUI test names TestNodeVendoredTuiUpstreamTests registers (parsed from its source) with the mapping. The mapping is the independent denominator: every non-designed-out row citing the harness must be registered, or deleting a name silently removes that row's only executable evidence. A registered file must not be designed-out, because that skips evidence verification for a test that still exercises shipped code.
func nodeBridgeHarnessProblems(harness []byte, m mapping) []string {
	h, err := parseNodeHarness(harness)
	if err != nil {
		return []string{err.Error()}
	}
	if len(h.names) == 0 {
		return []string{"upstream test list is empty"}
	}
	harnessFunc := h.source
	byPath := map[string]mappingEntry{}
	for _, entry := range m.Entries {
		byPath[entry.Path] = entry
	}
	registered := map[string]bool{}
	problems := h.executionProblems(byPath)
	for _, name := range h.names {
		path := "packages/tui/test/" + name + ".test.ts"
		registered[path] = true
		entry, ok := byPath[path]
		if !ok {
			problems = append(problems, path+" runs in TestNodeVendoredTuiUpstreamTests but has no mapping entry")
			continue
		}
		if entry.Disposition == "designed-out" {
			problems = append(problems, path+" is designed-out although TestNodeVendoredTuiUpstreamTests runs it against shipped code; classify the Node-bridge obligation as ported/partial so its evidence is verified")
		}
	}
	for _, entry := range m.Entries {
		if entry.Disposition != "designed-out" && citesNodeHarness(entry.Evidence, harnessFunc) && !registered[entry.Path] {
			problems = append(problems, entry.Path+" cites "+nodeHarnessEvidence+" as evidence but the harness does not register it, so no subtest runs it")
		}
	}
	return problems
}

// checkNodeBridgeHarness applies nodeBridgeHarnessProblems to the harness source under repoRoot and the mapping at mappingPath. It runs in the command itself so every gate invocation (test-inventory, test-inventory-strict, test-porting-release) enforces it; a missing harness file is an error, not a skip.
func checkNodeBridgeHarness(mappingPath, repoRoot string) error {
	var m mapping
	if err := decodeJSON(mappingPath, &m); err != nil {
		return err
	}
	harness, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(nodeBridgeHarnessPath)))
	if err != nil {
		return fmt.Errorf("read Node-bridge harness: %w", err)
	}
	if problems := nodeBridgeHarnessProblems(harness, m); len(problems) > 0 {
		return fmt.Errorf("Node-bridge harness registration:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}
