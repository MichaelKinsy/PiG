// SPDX-License-Identifier: MIT

package checks

import (
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strings"
)

// SectionCheck flags a decision taken in one section of the host's loop and acted on in a later one.
//
// Pi runs a check and the act that depends on it as consecutive statements of one turn: nothing else runs between `if
// (this.#generation !== generation) return; site.batch.within(...)` (store open-query.ts:206-207), between `if (this.#closed)
// return; ... site.batch.within(adopt failed)` (open-query.ts:194-198), or between the last statement of an awaited function and
// the awaiter's continuation (query-reader.ts:91-94, then read-declarations.ts:43-45). A Go section (Batch.Section, Loop.Run,
// chordHost.enter) is one turn; two sections have others between them. A goroutine that blocks leaves a section boundary where
// Pi has an await, and that is the gap Pi has too; what Pi does not have is a gap between a check and its act with no await in
// it. The check moves INTO the section that acts: Batch.CheckAct for a check of state, awaitTail for a check that follows an
// await. Three shapes are flagged, each only when no wait (a receive, a Wait, a call that takes a context) lies between the
// check and the act, since Pi awaits there as well:
//
//  1. A guard (`if <state read> { return }`) before a section whose closure acts on the guarded object and does not read the
//     guarded state again: the check ran outside the section it is acted on in.
//  2. A variable a section's closure assigns from a state read, tested after the section by an `if` that guards a write to a
//     field or another section: the check ran in a section, the act in a later one.
//  3. A function that blocks (a receive, a Wait) and then reads state in a section for the value it returns (an error it hands
//     back is not such a value: its caller propagates it): its caller acts on the value in another section; return the read as
//     the tail of awaitTail instead.
//
// Blind spots, stated: the pass is intraprocedural and names a section only by its entry points (Section and CheckAct on a
// Batch, Run on a Loop, enter on a chordHost): a section entered through another wrapper, a guard or an act hidden in a callee,
// a wait hidden in a callee (a wait is a receive, a Wait or a call with a context argument written in the function itself), a
// decision kept in a field or a channel between the two sections, and a check made by one goroutine and acted on by another
// are not seen. A function documented to be called inside a section is not known to be one (a section inside a section is
// inline: its guard is fine there, and is flagged unless the site is classified). A state read is a field selection or a
// method call with no argument and no error result; a guard on anything else is not a state check to this pass. The pass cannot
// tell that Pi has an await where Go blocks in a callee: such a site is classified with `//portlint:allow sectioncheck <reason>`.
var SectionCheck = newCheck("sectioncheck",
	"a check of state made outside (or in an earlier section than) the section that acts on it: use Batch.CheckAct, awaitTail, or recheck inside the section",
	High, "ListLoadHang: a reader's wake added a section after its classification; OpenRead.load tested overtaken before the section that adopts",
	func(p *Pass) {
		sections := map[*ast.BlockStmt]bool{}
		p.Inspect.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
			if call := n.(*ast.CallExpr); p.isSectionCall(call) {
				if lit := sectionClosure(call); lit != nil {
					sections[lit.Body] = true
				}
			}
		})
		p.Funcs(func(_ string, _ *ast.FuncType, body *ast.BlockStmt) {
			if p.IsTest(body.Pos()) {
				return
			}
			(&sectionUnit{Pass: p, body: body, inSection: sections[body]}).run()
		})
	})

// isSectionCall reports whether call enters a section of the host's loop.
func (p *Pass) isSectionCall(call *ast.CallExpr) bool {
	fn := p.Callee(call)
	if fn == nil {
		return false
	}
	sig, _ := fn.Type().(*types.Signature)
	if sig == nil || sig.Recv() == nil {
		return false
	}
	recv := recvTypeName(sig.Recv().Type())
	switch fn.Name() {
	case "Section", "CheckAct":
		return recv == "Batch"
	case "Run":
		return recv == "Loop" || recv == "sectionLoop"
	case "enter":
		return recv == "chordHost"
	}
	return false
}

func recvTypeName(t types.Type) string {
	if ptr, ok := types.Unalias(t).(*types.Pointer); ok {
		t = ptr.Elem()
	}
	if n, ok := types.Unalias(t).(*types.Named); ok {
		return n.Obj().Name()
	}
	return ""
}

// sectionClosure is the function literal a section call runs: its last argument.
func sectionClosure(call *ast.CallExpr) *ast.FuncLit {
	if len(call.Args) == 0 {
		return nil
	}
	lit, _ := ast.Unparen(call.Args[len(call.Args)-1]).(*ast.FuncLit)
	return lit
}

// sectionUnit is the analysis of one function body.
type sectionUnit struct {
	*Pass
	body *ast.BlockStmt
	// inSection is whether the body is itself the closure of a section: a section called in it is inline, one turn.
	inSection bool
	// taint maps a local variable to the state reads its value was made of, taintRoots to the variables those reads started at.
	taint      map[types.Object][]string
	taintRoots map[types.Object][]types.Object
}

func (u *sectionUnit) run() {
	if u.inSection {
		return
	}
	u.taint = map[types.Object][]string{}
	u.taintRoots = map[types.Object][]types.Object{}
	u.collectTaint()
	var stack []ast.Node
	ast.Inspect(u.body, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if _, ok := n.(*ast.FuncLit); ok {
			// A nested literal is its own unit (Funcs visits it); a section's closure is analyzed through its call.
			return false
		}
		stack = append(stack, n)
		if call, ok := n.(*ast.CallExpr); ok && u.isSectionCall(call) && !u.isCheckAct(call) {
			if lit := sectionClosure(call); lit != nil {
				u.guardBefore(call, lit, stack)
				u.verdictAfter(call, lit, stack)
				u.resultAfterBlock(call, lit)
			}
		}
		return true
	})
}

// isCheckAct is whether call is the primitive itself: its check and its act are one section by construction.
func (u *sectionUnit) isCheckAct(call *ast.CallExpr) bool {
	fn := u.Callee(call)
	return fn != nil && fn.Name() == "CheckAct"
}

// collectTaint records, for each local assigned from state reads, the reads and the variables they started at.
func (u *sectionUnit) collectTaint() {
	ast.Inspect(u.body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		var reads []string
		var roots []types.Object
		for _, rhs := range assign.Rhs {
			if recv, ok := ast.Unparen(rhs).(*ast.UnaryExpr); ok && recv.Op == token.ARROW {
				// A receive is where Pi awaits: what it yields is a result of the wait, not a read of state.
				continue
			}
			reads = append(reads, u.stateReads(rhs)...)
			for obj := range u.rootObjects(rhs) {
				roots = append(roots, obj)
			}
		}
		if len(reads) == 0 {
			return true
		}
		for _, lhs := range assign.Lhs {
			if id, ok := lhs.(*ast.Ident); ok {
				if obj := u.TypesInfo.ObjectOf(id); obj != nil && !isErrorType(obj.Type()) {
					u.taint[obj] = append(u.taint[obj], reads...)
					u.taintRoots[obj] = append(u.taintRoots[obj], roots...)
				}
			}
		}
		return true
	})
}

// stateReads lists the state reads in expr: the text of each field selection rooted at a variable and of each method call with
// no argument and no error result (InSection is the question whether a section is entered, not a read of state), and the reads
// a local used in it was made of.
func (u *sectionUnit) stateReads(expr ast.Node) []string { return u.reads(expr, false) }

// operandReads is stateReads for a condition: what the condition tests, not what it hands to a call as an argument.
func (u *sectionUnit) operandReads(cond ast.Node) []string { return u.reads(cond, true) }

func (u *sectionUnit) reads(expr ast.Node, operandsOnly bool) []string {
	var reads []string
	ast.Inspect(expr, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr:
			if len(n.Args) == 0 {
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name != "InSection" && u.isMethodValue(sel) && !u.returnsError(n) && u.rootedAtVariable(sel.X) {
					reads = append(reads, types.ExprString(n.Fun)+"()")
				}
			}
			if operandsOnly {
				reads = append(reads, u.reads(n.Fun, true)...)
				return false
			}
		case *ast.SelectorExpr:
			if s := u.TypesInfo.Selections[n]; s != nil && s.Kind() == types.FieldVal && u.rootedAtVariable(n.X) && u.shared(n.X) {
				reads = append(reads, types.ExprString(n))
			}
		case *ast.Ident:
			if obj := u.TypesInfo.ObjectOf(n); obj != nil {
				reads = append(reads, u.taint[obj]...)
			}
		}
		return true
	})
	return reads
}

func (u *sectionUnit) isMethodValue(sel *ast.SelectorExpr) bool {
	s := u.TypesInfo.Selections[sel]
	return s != nil && s.Kind() == types.MethodVal
}

func isErrorType(t types.Type) bool { return types.Identical(t, types.Universe.Lookup("error").Type()) }

func (u *sectionUnit) returnsError(call *ast.CallExpr) bool {
	t := u.TypesInfo.TypeOf(call)
	if t == nil {
		return false
	}
	if tuple, ok := t.(*types.Tuple); ok {
		for v := range tuple.Variables() {
			if types.Identical(v.Type(), types.Universe.Lookup("error").Type()) {
				return true
			}
		}
		return false
	}
	return types.Identical(t, types.Universe.Lookup("error").Type())
}

// rootedAtVariable is whether the selector chain starts at a local variable, parameter or receiver (not a package).
func (u *sectionUnit) rootedAtVariable(e ast.Expr) bool {
	root := rootIdent(e)
	if root == nil {
		return false
	}
	_, ok := u.TypesInfo.ObjectOf(root).(*types.Var)
	return ok
}

// shared is whether x is reached through a pointer, an interface, a map or a slice: a field of a struct value that a function
// holds (a parameter passed by value, a local) is its own copy, not state another section can change.
func (u *sectionUnit) shared(x ast.Expr) bool {
	for {
		switch t := u.TypesInfo.TypeOf(x).Underlying().(type) {
		case *types.Pointer, *types.Interface, *types.Map, *types.Slice, *types.Chan:
			return true
		default:
			_ = t
		}
		switch e := ast.Unparen(x).(type) {
		case *ast.SelectorExpr:
			x = e.X
		case *ast.IndexExpr:
			x = e.X
		case *ast.CallExpr:
			return true
		default:
			return false
		}
	}
}

// rootObjects lists the variables expr reads from: the root of each selector chain, each identifier, and the roots behind a
// tainted local.
func (u *sectionUnit) rootObjects(expr ast.Node) map[types.Object]bool {
	roots := map[types.Object]bool{}
	ast.Inspect(expr, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.Ident:
			if obj := u.TypesInfo.ObjectOf(n); obj != nil {
				roots[obj] = true
				for _, r := range u.taintRoots[obj] {
					roots[r] = true
				}
			}
		}
		return true
	})
	return roots
}

func rootIdent(e ast.Expr) *ast.Ident {
	for {
		switch x := ast.Unparen(e).(type) {
		case *ast.SelectorExpr:
			e = x.X
		case *ast.CallExpr:
			e = x.Fun
		case *ast.IndexExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.Ident:
			return x
		default:
			return nil
		}
	}
}

// terminates is whether a block ends by leaving the function or the loop: a guard.
func terminates(b *ast.BlockStmt) bool {
	if b == nil || len(b.List) == 0 {
		return false
	}
	switch last := b.List[len(b.List)-1].(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt:
		return last.Tok == token.BREAK || last.Tok == token.CONTINUE || last.Tok == token.GOTO
	case *ast.ExprStmt:
		call, ok := last.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		id, ok := call.Fun.(*ast.Ident)
		return ok && id.Name == "panic"
	}
	return false
}

// listsOf lists, innermost first, the statement lists the stack passes through and the index of the statement it passes by.
func listsOf(stack []ast.Node) (lists [][]ast.Stmt, indexes []int) {
	for i := len(stack) - 2; i >= 0; i-- {
		var list []ast.Stmt
		switch b := stack[i].(type) {
		case *ast.BlockStmt:
			list = b.List
		case *ast.CaseClause:
			list = b.Body
		case *ast.CommClause:
			list = b.Body
		default:
			continue
		}
		if j := slices.IndexFunc(list, func(s ast.Stmt) bool { return s == stack[i+1] }); j >= 0 {
			lists = append(lists, list)
			indexes = append(indexes, j)
		}
	}
	return lists, indexes
}

// guardBefore is shape 1.
func (u *sectionUnit) guardBefore(call *ast.CallExpr, lit *ast.FuncLit, stack []ast.Node) {
	lists, indexes := listsOf(stack)
	inside := map[string]bool{}
	for _, t := range u.stateReads(lit.Body) {
		inside[t] = true
	}
	touched := u.rootObjects(lit.Body)
	for k, list := range lists {
		for _, stmt := range list[:indexes[k]] {
			guard, ok := stmt.(*ast.IfStmt)
			if !ok || guard.Else != nil || guard.Init != nil || !terminates(guard.Body) {
				continue
			}
			reads := u.operandReads(guard.Cond)
			if len(reads) == 0 || asksInSection(guard.Cond) || slices.ContainsFunc(reads, func(t string) bool { return inside[t] }) {
				continue
			}
			if !u.sharesRoot(guard.Cond, touched) || u.blockingBetween(guard.End(), call.Pos()) {
				continue
			}
			u.Reportf(call.Pos(), "this section acts on %s, which the guard at line %d checked outside it; check inside the section (Batch.CheckAct)", reads[0], u.Fset.Position(guard.Pos()).Line)
			return
		}
	}
}

// asksInSection is whether cond asks whether a section is entered already: the idiom that runs inline when it is and takes a
// section when it is not, which is the check and the act of one section either way.
func asksInSection(cond ast.Node) bool {
	found := false
	ast.Inspect(cond, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "InSection" {
				found = true
			}
		}
		return !found
	})
	return found
}

// sharesRoot is whether the guard reads state from a variable the section's closure uses.
func (u *sectionUnit) sharesRoot(cond ast.Node, touched map[types.Object]bool) bool {
	for obj := range u.rootObjects(cond) {
		if _, ok := obj.(*types.Var); ok && touched[obj] {
			return true
		}
	}
	return false
}

// verdictAfter is shape 2.
func (u *sectionUnit) verdictAfter(call *ast.CallExpr, lit *ast.FuncLit, stack []ast.Node) {
	if len(u.stateReads(lit.Body)) == 0 {
		return
	}
	assigned := u.assignedOutside(lit, false)
	if len(assigned) == 0 || u.actsInside(lit) {
		return
	}
	lists, indexes := listsOf(stack)
	if len(lists) == 0 {
		return
	}
	list, from := lists[0], indexes[0]
	for i, stmt := range list[from+1:] {
		guard, ok := stmt.(*ast.IfStmt)
		if !ok || !mentionsAny(u.Pass, guard.Cond, assigned, false) {
			continue
		}
		var acts []ast.Node
		for _, s := range guard.Body.List {
			acts = append(acts, s)
		}
		if terminates(guard.Body) && guard.Else == nil {
			for _, s := range list[from+1+i+1:] {
				acts = append(acts, s)
			}
		}
		if act := u.firstAct(acts); act != token.NoPos && !u.blockingBetween(call.End(), act) {
			u.Reportf(guard.Pos(), "the section at line %d decided this from state, and what depends on it runs after the section: one section must check and act (Batch.CheckAct)", u.Fset.Position(call.Pos()).Line)
			return
		}
	}
}

// actsInside is whether the section's closure does more than read: it writes a field, or calls something that takes an argument, has
// an error to return or is a statement of its own. Such a section already acted on what it decided (the check and the act are its
// one turn); a verdict tested after it only routes the result, which is what the check-and-act cures do.
func (u *sectionUnit) actsInside(lit *ast.FuncLit) bool {
	acts := false
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				if _, ok := lhs.(*ast.SelectorExpr); ok {
					acts = true
				}
			}
		case *ast.IncDecStmt:
			if _, ok := n.X.(*ast.SelectorExpr); ok {
				acts = true
			}
		case *ast.ExprStmt:
			if _, ok := n.X.(*ast.CallExpr); ok {
				acts = true
			}
		case *ast.CallExpr:
			if tv, ok := u.TypesInfo.Types[n.Fun]; ok && (tv.IsType() || tv.IsBuiltin()) {
				return true
			}
			if len(n.Args) > 0 || u.returnsError(n) {
				acts = true
			}
		}
		return !acts
	})
	return acts
}

// assignedOutside lists the variables declared outside lit that its body assigns.
func (u *sectionUnit) assignedOutside(lit *ast.FuncLit, errors bool) map[types.Object]bool {
	out := map[types.Object]bool{}
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || assign.Tok == token.DEFINE {
			return true
		}
		for _, lhs := range assign.Lhs {
			if id, ok := lhs.(*ast.Ident); ok {
				if obj, ok := u.TypesInfo.ObjectOf(id).(*types.Var); ok && obj.Pos() < lit.Pos() && (errors || !isErrorType(obj.Type())) {
					out[obj] = true
				}
			}
		}
		return true
	})
	return out
}

// mentionsAny is whether expr names one of objs: as an operand, and with deep as an argument of a call too.
func mentionsAny(p *Pass, expr ast.Node, objs map[types.Object]bool, deep bool) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			return deep
		case *ast.Ident:
			if objs[p.TypesInfo.ObjectOf(n)] {
				found = true
			}
		}
		return !found
	})
	return found
}

// firstAct is the position of the first write of a field, entry of a section or start of a goroutine in nodes, or NoPos.
func (u *sectionUnit) firstAct(nodes []ast.Node) token.Pos {
	found := token.NoPos
	for _, node := range nodes {
		ast.Inspect(node, func(n ast.Node) bool {
			if found != token.NoPos {
				return false
			}
			switch n := n.(type) {
			case *ast.FuncLit:
				return false
			case *ast.AssignStmt:
				for _, lhs := range n.Lhs {
					if _, ok := lhs.(*ast.SelectorExpr); ok && found == token.NoPos {
						found = n.Pos()
					}
				}
			case *ast.IncDecStmt:
				if _, ok := n.X.(*ast.SelectorExpr); ok {
					found = n.Pos()
				}
			case *ast.GoStmt:
				found = n.Pos()
			case *ast.CallExpr:
				if u.isSectionCall(n) {
					found = n.Pos()
				}
			}
			return found == token.NoPos
		})
		if found != token.NoPos {
			break
		}
	}
	return found
}

// blockingBetween is whether the body waits between from and to: a receive, a Wait, or a call that takes a context (the
// repository's mark of a call that blocks, outside a section). Pi awaits there too, so a gap that holds one is Pi's own.
func (u *sectionUnit) blockingBetween(from, to token.Pos) bool {
	found := false
	ast.Inspect(u.body, func(n ast.Node) bool {
		if n == nil || found || n.End() <= from || n.Pos() >= to {
			return false
		}
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.UnaryExpr:
			if n.Op == token.ARROW {
				found = true
			}
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Wait" {
				found = true
			}
			if n.Pos() >= from && u.takesContext(n) && !isConstructor(n) {
				found = true
			}
		}
		return !found
	})
	return found
}

// isConstructor is whether call is a function named new... or New...: it builds a value and waits for nothing.
func isConstructor(call *ast.CallExpr) bool {
	var name string
	switch f := call.Fun.(type) {
	case *ast.Ident:
		name = f.Name
	case *ast.SelectorExpr:
		name = f.Sel.Name
	}
	return strings.HasPrefix(name, "new") || strings.HasPrefix(name, "New")
}

func (u *sectionUnit) takesContext(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		if t := u.TypesInfo.TypeOf(arg); t != nil && NamedIs(t, "context", "Context") {
			return true
		}
	}
	return false
}

// resultAfterBlock is shape 3.
func (u *sectionUnit) resultAfterBlock(call *ast.CallExpr, lit *ast.FuncLit) {
	if len(u.stateReads(lit.Body)) == 0 {
		return
	}
	assigned := u.assignedOutside(lit, true)
	if len(assigned) == 0 || !u.blocksBefore(call.Pos()) {
		return
	}
	reported := false
	ast.Inspect(u.body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			if n.Pos() > call.End() && !reported {
				for _, r := range n.Results {
					// An error handed back is the end of the call, which its caller propagates: a decision it acts on is a value.
					if t := u.TypesInfo.TypeOf(r); t != nil && isErrorType(t) {
						continue
					}
					if mentionsAny(u.Pass, r, assigned, true) {
						u.Reportf(call.Pos(), "this function blocks and then reads state in a section for the value it returns: its caller acts on it in another section; return the read as awaitTail's tail")
						reported = true
						break
					}
				}
			}
		}
		return !reported
	})
}

// blocksBefore is whether the body has a receive or a Wait before pos.
func (u *sectionUnit) blocksBefore(pos token.Pos) bool {
	found := false
	ast.Inspect(u.body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.UnaryExpr:
			if n.Op == token.ARROW && n.Pos() < pos {
				found = true
			}
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Wait" && n.Pos() < pos {
				found = true
			}
		}
		return !found
	})
	return found
}
