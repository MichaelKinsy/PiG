// SPDX-License-Identifier: MIT

package upgrade

import (
	"cmp"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// getterSite is one drifted read that moves before its statement: a call of a
// Context getter, or a string read of before_agent_start's data["systemPrompt"].
type getterSite struct {
	call   ast.Expr
	getter getter
	// rule and symbol name the change, value is the base name of the variable
	// that holds the moved value, and text is the expression that reads it now.
	rule, symbol, value, text string
	// commaOK is set for a read in the ok form, `s, ok := x.(string)`: the
	// moved value stands in as `value, true`.
	commaOK bool
	// stack runs from the file down to the call. stmtAt indexes the statement
	// that holds the call and sits directly in a statement list.
	stack  []ast.Node
	stmtAt int
	// spread is set when the call compiles, its two results spread into a
	// variadic call, rather than failing for its arity.
	spread bool
}

func (s *getterSite) stmt() ast.Stmt { return s.stack[s.stmtAt].(ast.Stmt) }

var errorType = types.Universe.Lookup("error").Type()

// planGetters rewrites a Context getter whose call the compiler rejects for
// returning two results or that spreads its two results into a variadic call,
// and lists a method value of one the compiler rejects for its new signature.
func (p *filePlanner) planGetters() {
	var sites []*getterSite
	promptData := p.beforeAgentStartData()
	var stack []ast.Node
	ast.Inspect(p.file.Syntax, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, node)
		switch node := node.(type) {
		case *ast.CallExpr:
			g, drifted := p.driftedGetter(node)
			spread := false
			if !drifted {
				g, drifted = p.spreadGetter(node, stack[len(stack)-2])
				spread = drifted
			}
			if drifted {
				sites = append(sites, &getterSite{call: node, getter: g, rule: RuleContextGetters, symbol: "Context." + g.Name, value: valueName(g.Name), text: p.text(node.Pos(), node.End()), stack: append([]ast.Node(nil), stack...), stmtAt: -1, spread: spread})
			}
		case *ast.TypeAssertExpr:
			if p.stringSystemPrompt(node, promptData) {
				sites = append(sites, p.systemPromptSite(node, stack))
			}
		case *ast.SelectorExpr:
			if call, isCall := stack[len(stack)-2].(*ast.CallExpr); isCall && call.Fun == ast.Expr(node) {
				break
			}
			if g, drifted := p.driftedMethodValue(node); drifted {
				p.skip(RuleContextGetters, "Context."+g.Name, node.Pos(), "the method value is "+g.shape(g.New)+" now")
			}
		}
		return true
	})
	var usable []*getterSite
	for _, s := range sites {
		for i := len(s.stack) - 2; i > 0; i-- {
			// A select case's communication sits in its clause but is not one of
			// the clause's statements: nothing can move in front of it.
			if clause, isComm := s.stack[i-1].(*ast.CommClause); isComm && clause.Comm == s.stack[i] {
				continue
			}
			if _, isStmt := s.stack[i].(ast.Stmt); isStmt && isStatementList(s.stack[i-1]) {
				s.stmtAt = i
				break
			}
		}
		symbol := s.symbol
		switch {
		case s.stmtAt < 0:
			p.skip(s.rule, symbol, s.call.Pos(), "the call is not inside a statement list")
		case containsDrifted(s, sites):
			p.skip(s.rule, symbol, s.call.Pos(), "the call holds another getter call")
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
	if !ok || !p.isContextMethod(selector) {
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

// driftedMethodValue reports whether selector is a method value of a Context
// getter the compiler rejects where the getter's old signature is wanted.
func (p *filePlanner) driftedMethodValue(selector *ast.SelectorExpr) (getter, bool) {
	g, ok := getterByName(selector.Sel.Name)
	if !ok || !p.isContextMethod(selector) {
		return getter{}, false
	}
	for _, typeErr := range p.errors {
		if typeErr.Pos == selector.Pos() && strings.HasPrefix(typeErr.Msg, "cannot use ") {
			return g, true
		}
	}
	return getter{}, false
}

// spreadGetter reports whether call is a Context getter that is the only
// argument of parent, a call of a variadic function or of print or println, and
// that took its error after the SDK release the module requires. Its value and
// its error spread into the parameters, so it compiles against the current SDK
// and no compiler error marks it, yet the error is passed on as a value
// (fmt.Println prints it beside the value) instead of handled. A getter whose
// error the required SDK already returned is current code, which may print the
// error on purpose. A call whose parameter for the error has the type error,
// variadic or not, is the new shape: both are left alone.
func (p *filePlanner) spreadGetter(call *ast.CallExpr, parent ast.Node) (getter, bool) {
	outer, ok := parent.(*ast.CallExpr)
	if !ok || len(outer.Args) != 1 || outer.Args[0] != ast.Expr(call) || outer.Ellipsis.IsValid() {
		return getter{}, false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return getter{}, false
	}
	g, ok := getterByName(selector.Sel.Name)
	if !ok || g.Since == "" || !releaseBefore(p.sdkVersion, g.Since) || !p.isContextMethod(selector) {
		return getter{}, false
	}
	if results, ok := p.pkg.Info.TypeOf(call).(*types.Tuple); !ok || results.Len() != 2 {
		return getter{}, false
	}
	if ident, ok := ast.Unparen(outer.Fun).(*ast.Ident); ok {
		if builtin, ok := p.pkg.Info.Uses[ident].(*types.Builtin); ok {
			return g, builtin.Name() == "print" || builtin.Name() == "println"
		}
	}
	callee, ok := p.pkg.Info.Types[outer.Fun]
	if !ok || callee.IsType() || callee.Type == nil {
		return getter{}, false
	}
	signature, ok := callee.Type.Underlying().(*types.Signature)
	if !ok || !signature.Variadic() {
		return getter{}, false
	}
	// The error is the second argument: a fixed parameter, or else the
	// variadic parameter's element.
	params := signature.Params()
	var errParam types.Type
	if params.Len() > 2 {
		errParam = params.At(1).Type()
	} else if variadic, ok := params.At(params.Len() - 1).Type().Underlying().(*types.Slice); ok {
		errParam = variadic.Elem()
	}
	return g, errParam != nil && !types.Identical(errParam, errorType)
}

// releaseBefore reports whether required, the SDK version a module requires
// (v0.4.1), is an earlier release than since (0.5.0). A version it cannot read
// counts as earlier: v0.0.0 with a replace, or none in a workspace.
func releaseBefore(required, since string) bool {
	have, ok := releaseNumbers(strings.TrimPrefix(required, "v"))
	if !ok {
		return true
	}
	want, _ := releaseNumbers(since)
	return slices.Compare(have, want) < 0
}

// releaseNumbers reads the major, minor and patch numbers of 0.5.0 or
// 0.5.0-rc.1; a pre-release counts as its release.
func releaseNumbers(version string) ([]int, bool) {
	version, _, _ = strings.Cut(version, "-")
	fields := strings.Split(version, ".")
	if len(fields) != 3 {
		return nil, false
	}
	numbers := make([]int, len(fields))
	for i, field := range fields {
		n, err := strconv.Atoi(field)
		if err != nil {
			return nil, false
		}
		numbers[i] = n
	}
	return numbers, true
}

// isContextMethod reports whether selector selects a method of the SDK's Context.
func (p *filePlanner) isContextMethod(selector *ast.SelectorExpr) bool {
	selection := p.pkg.Info.Selections[selector]
	if selection == nil || selection.Kind() != types.MethodVal {
		return false
	}
	fn, ok := selection.Obj().(*types.Func)
	return ok && isSDKReceiver(fn.Signature().Recv(), "Context")
}

// localAccessors are the Context methods that read state the extension already
// holds and make no host call. A call of one that runs before a getter in a
// statement moves out with the getter, so the two keep their order.
var localAccessors = map[string]bool{"Cwd": true, "Mode": true, "HasUI": true, "Width": true, "Height": true, "ToolCallID": true}

// isLocalAccessor reports whether call is a call of a local Context accessor.
func (p *filePlanner) isLocalAccessor(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && len(call.Args) == 0 && localAccessors[selector.Sel.Name] && p.isContextMethod(selector)
}

// isSDKReceiver reports whether recv is the SDK type name, or a pointer to it.
func isSDKReceiver(recv *types.Var, name string) bool {
	if recv == nil {
		return false
	}
	t := recv.Type()
	if pointer, ok := t.(*types.Pointer); ok {
		t = pointer.Elem()
	}
	named, ok := types.Unalias(t).(*types.Named)
	return ok && named.Obj().Name() == name && named.Obj().Pkg() != nil && IsSDKPath(named.Obj().Pkg().Path())
}

func (p *filePlanner) planGetterGroup(stmt ast.Stmt, group []*getterSite) {
	skipGroup := func(reason string) {
		for _, member := range group {
			p.skip(member.rule, member.symbol, member.call.Pos(), reason)
		}
	}
	hoisted := map[ast.Expr]bool{}
	for _, s := range group {
		hoisted[s.call] = true
	}
	accessors := map[*ast.CallExpr]bool{}
	for _, s := range group {
		if reason := p.hoistBlocker(s, hoisted, accessors); reason != "" {
			skipGroup(reason)
			return
		}
	}
	results := p.enclosingResults(group[0].stack)
	if !returnsError(results) {
		// A spread call compiled with its error in hand: discarding the error
		// would lose what the call passed on.
		for _, s := range group {
			if s.getter.MustHandle || s.spread {
				skipGroup("the function does not return an error, so the error of " + s.symbol + " has nowhere to go")
				return
			}
		}
	}
	// Only a getter that fails to compile for an error it took in 0.3.0 dates
	// the file before the nullable ContextUsage fields of the same release.
	legacy := slices.ContainsFunc(group, func(s *getterSite) bool {
		return s.rule == RuleContextGetters && !s.spread && s.getter.Since == ""
	})
	errName := p.errorName(stmt.Pos())
	indent := p.indentAt(stmt.Pos())
	if assign, ok := stmt.(*ast.AssignStmt); ok && len(group) == 1 && len(accessors) == 0 && p.isPlainDefine(assign, group[0]) {
		s := group[0]
		rewrite := Rewrite{Rule: s.rule, Symbol: s.symbol, Line: p.line(s.call.Pos())}
		lines, err := p.getterLines(s.getter, assign.Lhs[0].(*ast.Ident).Name, s.text, errName, results)
		if err != nil {
			skipGroup(err.Error())
			return
		}
		p.sites = append(p.sites, site{rewrites: []Rewrite{rewrite}, edits: []edit{{start: p.offset(stmt.Pos()), end: p.offset(stmt.End()), text: strings.Join(lines, "\n"+indent)}}, legacy: legacy})
		return
	}
	// The getters and the accessor calls before them move out in source order.
	moved := make([]ast.Expr, 0, len(group)+len(accessors))
	getterAt := map[ast.Expr]*getterSite{}
	for _, s := range group {
		moved = append(moved, s.call)
		getterAt[s.call] = s
	}
	for call := range accessors {
		moved = append(moved, call)
	}
	slices.SortFunc(moved, func(a, b ast.Expr) int { return cmp.Compare(a.Pos(), b.Pos()) })
	var hoistedLines []string
	var replacements []edit
	var rewrites []Rewrite
	for _, call := range moved {
		replacement := ""
		var lines []string
		var err error
		var name string
		if s, isGetter := getterAt[call]; isGetter {
			name = p.fresh(s.value)
			lines, err = p.getterLines(s.getter, name, s.text, errName, results)
			rewrites = append(rewrites, Rewrite{Rule: s.rule, Symbol: s.symbol, Line: p.line(call.Pos())})
			replacement = name
			if s.commaOK {
				replacement = name + ", true"
			}
		} else {
			accessor := call.(*ast.CallExpr)
			name = p.fresh(valueName(accessor.Fun.(*ast.SelectorExpr).Sel.Name))
			lines, err = snippet(name + " := " + p.text(call.Pos(), call.End()))
			replacement = name
		}
		if err != nil {
			skipGroup(err.Error())
			return
		}
		hoistedLines = append(hoistedLines, lines...)
		replacements = append(replacements, edit{start: p.offset(call.Pos()), end: p.offset(call.End()), text: replacement})
	}
	// The group is one statement: its hoisted lines and replaced calls apply together.
	edits := []edit{{start: p.offset(stmt.Pos()), end: p.offset(stmt.Pos()), text: insertion(hoistedLines, indent)}}
	p.sites = append(p.sites, site{rewrites: rewrites, edits: append(edits, replacements...), legacy: legacy})
}

// isPlainDefine reports whether assign is `name := call`, or for a read in the
// ok form `name, _ := x.(string)`.
func (p *filePlanner) isPlainDefine(assign *ast.AssignStmt, s *getterSite) bool {
	if assign.Tok != token.DEFINE || len(assign.Rhs) != 1 || assign.Rhs[0] != s.call {
		return false
	}
	switch {
	case s.commaOK && len(assign.Lhs) == 2:
		if blank, ok := assign.Lhs[1].(*ast.Ident); !ok || blank.Name != "_" {
			return false
		}
	case s.commaOK || len(assign.Lhs) != 1:
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
	if returnsError(results) {
		statements.WriteString(target + ", " + errName + " := " + callText + "\nif " + errName + " != nil {\n" + p.returnStatement(results, errName) + "\n}\n")
	} else {
		// The old getter returned its zero value on failure; MustHandle getters never get here.
		statements.WriteString(target + ", _ := " + callText + " // the SDK now returns an error here: handle it\n")
	}
	if g.Pointer {
		statements.WriteString("var " + name + " string\nif " + ptr + " != nil {\n" + name + " = *" + ptr + "\n}\n")
	}
	return snippet(statements.String())
}

// returnsError reports whether a function with these results returns an error last.
func returnsError(results *types.Tuple) bool {
	return results != nil && results.Len() > 0 && types.Identical(results.At(results.Len()-1).Type(), errorType)
}

// returnStatement is `return <zero values>, err` for a function whose last
// result is error.
func (p *filePlanner) returnStatement(results *types.Tuple, errName string) string {
	values := make([]string, 0, results.Len())
	for i := range results.Len() - 1 {
		values = append(values, p.zeroValue(results.At(i).Type()))
	}
	values = append(values, errName)
	return "return " + strings.Join(values, ", ")
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
// scope or an outer err keeps its meaning. The scope is the block that holds the
// statement at pos, not the statement's own scope: an if statement's init
// declares its err inside the if.
func (p *filePlanner) errorName(pos token.Pos) string {
	scope := p.pkg.Types.Scope().Innermost(pos)
	for scope != nil && scope.Pos() == pos {
		scope = scope.Parent()
	}
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
// empty string when moving it keeps the order of evaluation. It adds to
// accessors each local Context accessor call that runs before the getter in the
// statement: it moves out with the getter.
func (p *filePlanner) hoistBlocker(s *getterSite, hoisted map[ast.Expr]bool, accessors map[*ast.CallExpr]bool) string {
	stmt := s.stmt()
	path := s.stack[s.stmtAt+1:]
	var scanned ast.Node = stmt
	first := path[0]
	switch node := stmt.(type) {
	case *ast.AssignStmt, *ast.ExprStmt, *ast.ReturnStmt, *ast.SendStmt, *ast.IncDecStmt, *ast.GoStmt, *ast.DeferStmt, *ast.DeclStmt:
	case *ast.IfStmt:
		// The init runs first, so a call in it moves out before the if.
		switch {
		case node.Init != nil && first == ast.Node(node.Init):
			scanned = node.Init
		case node.Init != nil:
			return "the if statement has an init statement"
		case first != ast.Node(node.Cond):
			return "the call is not in the if condition"
		default:
			scanned = node.Cond
		}
	case *ast.SwitchStmt:
		switch {
		case node.Init != nil && first == ast.Node(node.Init):
			scanned = node.Init
		case node.Init != nil || node.Tag == nil || first != ast.Node(node.Tag):
			return "the call is not in the switch tag"
		default:
			scanned = node.Tag
		}
	case *ast.RangeStmt:
		if first != ast.Node(node.X) {
			return "the call is not in the range expression"
		}
		scanned = node.X
	default:
		return "the statement is not one pig moves a call out of"
	}
	if conditional(path, s.call) {
		return "the call runs only when an earlier operand allows it"
	}
	reason := ""
	var open []ast.Node
	ast.Inspect(scanned, func(node ast.Node) bool {
		if node == nil {
			open = open[:len(open)-1]
			return false
		}
		if reason != "" {
			return false
		}
		if _, isLiteral := node.(*ast.FuncLit); isLiteral {
			return false
		}
		if node.Pos() < s.call.Pos() {
			switch earlier := node.(type) {
			case *ast.CallExpr:
				encloses := earlier.Pos() <= s.call.Pos() && s.call.End() <= earlier.End()
				switch {
				case encloses || hoisted[earlier] || p.isPureCall(earlier):
				case p.isLocalAccessor(earlier) && !conditional(open, earlier):
					accessors[earlier] = true
				default:
					reason = "an earlier call in the statement would run after the getter"
				}
			case *ast.UnaryExpr:
				if earlier.Op == token.ARROW {
					reason = "a channel receive before the call would run after the getter"
				}
			}
		}
		open = append(open, node)
		return true
	})
	return reason
}

// conditional reports whether node, under the nodes in path, is the right
// operand of a && or ||, which runs only when the left operand allows it.
func conditional(path []ast.Node, node ast.Node) bool {
	for i, outer := range path {
		inner := node
		if i+1 < len(path) {
			inner = path[i+1]
		}
		if binary, ok := outer.(*ast.BinaryExpr); ok && (binary.Op == token.LAND || binary.Op == token.LOR) && inner == ast.Node(binary.Y) {
			return true
		}
	}
	return false
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
