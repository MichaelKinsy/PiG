// SPDX-License-Identifier: MIT

package upgrade

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"
	"unicode"
)

// getterSite is one drifted call of a Context getter.
type getterSite struct {
	call   *ast.CallExpr
	getter getter
	// stack runs from the file down to the call. stmtAt indexes the statement
	// that holds the call and sits directly in a statement list.
	stack  []ast.Node
	stmtAt int
}

func (s *getterSite) stmt() ast.Stmt { return s.stack[s.stmtAt].(ast.Stmt) }

var errorType = types.Universe.Lookup("error").Type()

// planGetters rewrites a Context getter whose call the compiler rejects for
// returning two results.
func (p *filePlanner) planGetters() {
	var sites []*getterSite
	var stack []ast.Node
	ast.Inspect(p.file.Syntax, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, node)
		if call, ok := node.(*ast.CallExpr); ok {
			if g, drifted := p.driftedGetter(call); drifted {
				sites = append(sites, &getterSite{call: call, getter: g, stack: append([]ast.Node(nil), stack...), stmtAt: -1})
			}
		}
		return true
	})
	var usable []*getterSite
	for _, s := range sites {
		for i := len(s.stack) - 2; i > 0; i-- {
			if _, isStmt := s.stack[i].(ast.Stmt); isStmt && isStatementList(s.stack[i-1]) {
				s.stmtAt = i
				break
			}
		}
		symbol := "Context." + s.getter.Name
		switch {
		case s.stmtAt < 0:
			p.skip(RuleContextGetters, symbol, s.call.Pos(), "the call is not inside a statement list")
		case containsDrifted(s, sites):
			p.skip(RuleContextGetters, symbol, s.call.Pos(), "the call holds another getter call")
		default:
			usable = append(usable, s)
		}
	}
	var order []ast.Stmt
	groups := map[ast.Stmt][]*getterSite{}
	for _, s := range usable {
		stmt := s.stmt()
		if _, seen := groups[stmt]; !seen {
			order = append(order, stmt)
		}
		groups[stmt] = append(groups[stmt], s)
	}
	for _, stmt := range order {
		p.planGetterGroup(stmt, groups[stmt])
	}
}

func isStatementList(node ast.Node) bool {
	switch node.(type) {
	case *ast.BlockStmt, *ast.CaseClause, *ast.CommClause:
		return true
	}
	return false
}

func containsDrifted(outer *getterSite, all []*getterSite) bool {
	for _, other := range all {
		if other != outer && outer.call.Pos() <= other.call.Pos() && other.call.End() <= outer.call.End() {
			return true
		}
	}
	return false
}

// driftedGetter reports whether call is a Context getter the compiler rejects
// for returning two results.
func (p *filePlanner) driftedGetter(call *ast.CallExpr) (getter, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return getter{}, false
	}
	g, ok := getterByName(selector.Sel.Name)
	if !ok {
		return getter{}, false
	}
	selection := p.pkg.Info.Selections[selector]
	if selection == nil || selection.Kind() != types.MethodVal {
		return getter{}, false
	}
	fn, ok := selection.Obj().(*types.Func)
	if !ok || !isSDKContext(fn.Signature().Recv()) {
		return getter{}, false
	}
	// The compiler positions an arity error at the start of the call.
	for _, typeErr := range p.errors {
		if typeErr.Pos == call.Pos() && isArityDiagnostic(typeErr.Msg) {
			return g, true
		}
	}
	return getter{}, false
}

// isSDKContext reports whether recv is the SDK's Context.
func isSDKContext(recv *types.Var) bool {
	if recv == nil {
		return false
	}
	t := recv.Type()
	if pointer, ok := t.(*types.Pointer); ok {
		t = pointer.Elem()
	}
	named, ok := types.Unalias(t).(*types.Named)
	return ok && named.Obj().Name() == "Context" && named.Obj().Pkg() != nil && IsSDKPath(named.Obj().Pkg().Path())
}

func (p *filePlanner) planGetterGroup(stmt ast.Stmt, group []*getterSite) {
	hoisted := map[*ast.CallExpr]bool{}
	for _, s := range group {
		hoisted[s.call] = true
	}
	for _, s := range group {
		if reason := p.hoistBlocker(s, hoisted); reason != "" {
			for _, member := range group {
				p.skip(RuleContextGetters, "Context."+member.getter.Name, member.call.Pos(), reason)
			}
			return
		}
	}
	results := p.enclosingResults(group[0].stack)
	errName := p.errorName(stmt.Pos())
	indent := p.indentAt(stmt.Pos())
	var hoistedLines []string
	var replacements []edit
	var sites []site
	var rewrites []Rewrite
	for _, s := range group {
		rewrite := Rewrite{Rule: RuleContextGetters, Symbol: "Context." + s.getter.Name, Line: p.line(s.call.Pos())}
		callText := p.text(s.call.Pos(), s.call.End())
		if assign, ok := stmt.(*ast.AssignStmt); ok && len(group) == 1 && p.isPlainDefine(assign, s.call) {
			name := assign.Lhs[0].(*ast.Ident).Name
			lines, err := p.getterLines(s.getter, name, callText, errName, results)
			if err != nil {
				p.skip(RuleContextGetters, rewrite.Symbol, s.call.Pos(), err.Error())
				return
			}
			text := strings.Join(lines, "\n"+indent)
			sites = append(sites, site{rewrites: []Rewrite{rewrite}, edits: []edit{{start: p.offset(stmt.Pos()), end: p.offset(stmt.End()), text: text}}})
			continue
		}
		name := p.fresh(valueName(s.getter.Name))
		lines, err := p.getterLines(s.getter, name, callText, errName, results)
		if err != nil {
			p.skip(RuleContextGetters, rewrite.Symbol, s.call.Pos(), err.Error())
			return
		}
		hoistedLines = append(hoistedLines, lines...)
		replacements = append(replacements, edit{start: p.offset(s.call.Pos()), end: p.offset(s.call.End()), text: name})
		rewrites = append(rewrites, rewrite)
	}
	if len(hoistedLines) > 0 {
		// The group is one statement: its hoisted lines and replaced calls apply together.
		edits := []edit{{start: p.offset(stmt.Pos()), end: p.offset(stmt.Pos()), text: insertion(hoistedLines, indent)}}
		p.sites = append(p.sites, site{rewrites: rewrites, edits: append(edits, replacements...)})
		return
	}
	p.sites = append(p.sites, sites...)
}

// isPlainDefine reports whether assign is `name := call`.
func (p *filePlanner) isPlainDefine(assign *ast.AssignStmt, call *ast.CallExpr) bool {
	if assign.Tok != token.DEFINE || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 || assign.Rhs[0] != ast.Expr(call) {
		return false
	}
	name, ok := assign.Lhs[0].(*ast.Ident)
	return ok && name.Name != "_"
}

// getterLines writes the statements that call the getter, return the error in
// the handler's style, and leave the old value in name.
func (p *filePlanner) getterLines(g getter, name, callText, errName string, results *types.Tuple) ([]string, error) {
	target, ptr := name, ""
	if g.Pointer {
		ptr = p.fresh(name + "Ptr")
		target = ptr
	}
	var statements strings.Builder
	returns, canReturn := p.returnStatement(results, errName)
	if canReturn {
		statements.WriteString(target + ", " + errName + " := " + callText + "\nif " + errName + " != nil {\n" + returns + "\n}\n")
	} else {
		statements.WriteString(target + ", _ := " + callText + " // the SDK now returns an error here: handle it\n")
	}
	if g.Pointer {
		statements.WriteString("var " + name + " string\nif " + ptr + " != nil {\n" + name + " = *" + ptr + "\n}\n")
	}
	return snippet(statements.String())
}

// returnStatement is `return <zero values>, err` for a function whose last
// result is error.
func (p *filePlanner) returnStatement(results *types.Tuple, errName string) (string, bool) {
	if results == nil || results.Len() == 0 || !types.Identical(results.At(results.Len()-1).Type(), errorType) {
		return "", false
	}
	values := make([]string, 0, results.Len())
	for i := 0; i < results.Len()-1; i++ {
		values = append(values, p.zeroValue(results.At(i).Type()))
	}
	values = append(values, errName)
	return "return " + strings.Join(values, ", "), true
}

func (p *filePlanner) zeroValue(t types.Type) string {
	qualify := func(pkg *types.Package) string { return p.qualifier(pkg) }
	switch u := types.Unalias(t).Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsBoolean != 0:
			return "false"
		case u.Info()&types.IsString != 0:
			return `""`
		case u.Info()&types.IsNumeric != 0:
			return "0"
		}
		return "nil"
	case *types.Struct, *types.Array:
		return types.TypeString(t, qualify) + "{}"
	case *types.TypeParam:
		return "*new(" + types.TypeString(t, qualify) + ")"
	}
	return "nil"
}

// enclosingResults returns the results of the function that holds the node at
// the end of stack.
func (p *filePlanner) enclosingResults(stack []ast.Node) *types.Tuple {
	for i := len(stack) - 1; i >= 0; i-- {
		switch node := stack[i].(type) {
		case *ast.FuncLit:
			if sig, ok := p.pkg.Info.Types[node].Type.(*types.Signature); ok {
				return sig.Results()
			}
			return nil
		case *ast.FuncDecl:
			if fn, ok := p.pkg.Info.Defs[node.Name].(*types.Func); ok {
				return fn.Signature().Results()
			}
			return nil
		}
	}
	return nil
}

// errorName picks the name of the error variable at pos: err when it is free
// or already an error in this scope, else a fresh name, so a later err in the
// scope or an outer err keeps its meaning.
func (p *filePlanner) errorName(pos token.Pos) string {
	scope := p.pkg.Types.Scope().Innermost(pos)
	if scope == nil {
		return p.fresh("getErr")
	}
	owner, object := scope.LookupParent("err", pos)
	switch {
	case object == nil && scope.Lookup("err") == nil:
		return "err"
	case object != nil && owner == scope:
		if variable, ok := object.(*types.Var); ok && types.Identical(variable.Type(), errorType) {
			return "err"
		}
	}
	return p.fresh("getErr")
}

// valueName names the variable that holds a getter's value: GetSessionID ->
// sessionID, IsIdle -> idle, HasPendingMessages -> hasPendingMessages.
func valueName(method string) string {
	name := method
	switch {
	case strings.HasPrefix(name, "Get"):
		name = strings.TrimPrefix(name, "Get")
	case strings.HasPrefix(name, "Is"):
		name = strings.TrimPrefix(name, "Is")
	}
	runes := []rune(name)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

// hoistBlocker returns why the call cannot move before its statement, or the
// empty string when moving it keeps the order of evaluation.
func (p *filePlanner) hoistBlocker(s *getterSite, hoisted map[*ast.CallExpr]bool) string {
	stmt := s.stmt()
	path := s.stack[s.stmtAt+1:]
	var scanned ast.Node = stmt
	first := path[0]
	switch node := stmt.(type) {
	case *ast.AssignStmt, *ast.ExprStmt, *ast.ReturnStmt, *ast.SendStmt, *ast.IncDecStmt, *ast.GoStmt, *ast.DeferStmt, *ast.DeclStmt:
	case *ast.IfStmt:
		if node.Init != nil {
			return "the if statement has an init statement"
		}
		if first != ast.Node(node.Cond) {
			return "the call is not in the if condition"
		}
		scanned = node.Cond
	case *ast.SwitchStmt:
		if node.Init != nil || node.Tag == nil || first != ast.Node(node.Tag) {
			return "the call is not in the switch tag"
		}
		scanned = node.Tag
	case *ast.RangeStmt:
		if first != ast.Node(node.X) {
			return "the call is not in the range expression"
		}
		scanned = node.X
	default:
		return "the statement is not one pig moves a call out of"
	}
	for i := 0; i+1 < len(path); i++ {
		if binary, ok := path[i].(*ast.BinaryExpr); ok && (binary.Op == token.LAND || binary.Op == token.LOR) && path[i+1] == ast.Node(binary.Y) {
			return "the call runs only when an earlier operand allows it"
		}
	}
	reason := ""
	ast.Inspect(scanned, func(node ast.Node) bool {
		if reason != "" || node == nil {
			return false
		}
		if _, isLiteral := node.(*ast.FuncLit); isLiteral {
			return false
		}
		if node.Pos() >= s.call.Pos() {
			return true
		}
		switch earlier := node.(type) {
		case *ast.CallExpr:
			encloses := earlier.Pos() <= s.call.Pos() && s.call.End() <= earlier.End()
			if !encloses && !hoisted[earlier] && !p.isPureCall(earlier) {
				reason = "an earlier call in the statement would run after the getter"
			}
		case *ast.UnaryExpr:
			if earlier.Op == token.ARROW {
				reason = "a channel receive before the call would run after the getter"
			}
		}
		return true
	})
	return reason
}

// isPureCall reports whether a call has no effect: a conversion or a pure builtin.
func (p *filePlanner) isPureCall(call *ast.CallExpr) bool {
	if tv, ok := p.pkg.Info.Types[call.Fun]; ok && tv.IsType() {
		return true
	}
	ident, ok := call.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	if builtin, ok := p.pkg.Info.Uses[ident].(*types.Builtin); ok {
		switch builtin.Name() {
		case "len", "cap", "min", "max", "real", "imag", "complex", "new", "make":
			return true
		}
	}
	return false
}
