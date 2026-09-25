// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"go/token"
	"regexp"
	"slices"
)

const discardedIOErrorName = "discarded-io-error"

// discardedIOError flags a persistence or file-system error that is thrown
// away: a bare `sess.AppendLabelChange(...)` statement, `_ =
// os.WriteFile(...)`, `id, _ := in.AppendMessage(msg)`, or `if _, err :=
// in.AppendMessage(msg); err == nil { ... }` with no else branch.
// A full disk or a deleted session directory then loses every later message
// while the UI shows a healthy run (AGENT-05); Pi's appendFileSync throws
// and the run fails visibly.
var discardedIOError = check{
	Name:    discardedIOErrorName,
	Doc:     "persistence or file-system error discarded with _ or an else-less err == nil",
	Applies: func(rel string) bool { return inHot(rel) && !additiveOnly(rel) },
	Run:     runDiscardedIOError,
}

// ioCallRe matches calls that persist state: whole-file writes, renames,
// directory creation, fsync, and session or settings Append/Save/Persist
// methods. Plain Write calls (hashes, terminals, HTTP bodies) and cleanup
// removals are left out; their errors rarely lose user data.
var ioCallRe = regexp.MustCompile(`^(?:WriteFile|Rename|Mkdir|MkdirAll|Sync|Truncate|(?:Append|Save|Persist)[A-Z]\w*)$`)

func runDiscardedIOError(fc *fileCtx) []Hit {
	var hits []Hit
	report := func(pos token.Pos, stack []ast.Node, msg string) {
		if h, ok := fc.hit(discardedIOErrorName, pos, stack, msg); ok {
			hits = append(hits, h)
		}
	}
	inspect(fc.File, func(n ast.Node, stack []ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if call := ioCall(n); call != nil && (allBlank(n.Lhs) || trailingBlank(n.Lhs) && fc.errorCall(call)) {
				report(n.Pos(), stack, calleeName(call)+" error is discarded")
			}
		case *ast.ExprStmt:
			if call, ok := n.X.(*ast.CallExpr); ok && ioCallRe.MatchString(calleeName(call)) && fc.errorCall(call) {
				report(n.Pos(), stack, calleeName(call)+" result, including its error, is ignored")
			}
		case *ast.IfStmt:
			if as, ok := n.Init.(*ast.AssignStmt); ok && n.Else == nil && comparesErrNil(n.Cond, token.EQL) {
				if call := ioCall(as); call != nil && !returnsAssignedErrorAfter(stack, n, assignedErrorName(as)) {
					report(n.Pos(), stack, calleeName(call)+" error only gates the success branch; the failure is dropped")
				}
			}
		}
		return true
	})
	return hits
}

func assignedErrorName(as *ast.AssignStmt) string {
	if len(as.Lhs) == 0 {
		return ""
	}
	id, _ := as.Lhs[len(as.Lhs)-1].(*ast.Ident)
	if id == nil || id.Name == "_" {
		return ""
	}
	return id.Name
}

// returnsAssignedErrorAfter reports the narrow success-gating form where the
// same error is returned unchanged later in the surrounding block. The
// statements between the if and return may perform cleanup, but an assignment
// to the error invalidates the proof.
func returnsAssignedErrorAfter(stack []ast.Node, current *ast.IfStmt, name string) bool {
	if name == "" {
		return false
	}
	for _, s := range slices.Backward(stack) {
		block, ok := s.(*ast.BlockStmt)
		if !ok {
			continue
		}
		for j, statement := range block.List {
			if current.Pos() < statement.Pos() || current.End() > statement.End() {
				continue
			}
			for _, later := range block.List[j+1:] {
				if returnedName(later, name) {
					return true
				}
				if assignsName(later, name) {
					return false
				}
			}
			break
		}
	}
	return false
}

func returnedName(statement ast.Stmt, name string) bool {
	ret, ok := statement.(*ast.ReturnStmt)
	if !ok {
		return false
	}
	for _, result := range ret.Results {
		if id, ok := result.(*ast.Ident); ok && id.Name == name {
			return true
		}
	}
	return false
}

func assignsName(node ast.Node, name string) bool {
	assigned := false
	ast.Inspect(node, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range as.Lhs {
			if id, ok := lhs.(*ast.Ident); ok && id.Name == name {
				assigned = true
				return false
			}
		}
		return true
	})
	return assigned
}

func ioCall(as *ast.AssignStmt) *ast.CallExpr {
	if len(as.Rhs) != 1 {
		return nil
	}
	call, ok := as.Rhs[0].(*ast.CallExpr)
	if !ok || !ioCallRe.MatchString(calleeName(call)) {
		return nil
	}
	return call
}

func allBlank(lhs []ast.Expr) bool {
	for _, e := range lhs {
		if id, ok := e.(*ast.Ident); !ok || id.Name != "_" {
			return false
		}
	}
	return len(lhs) > 0
}

// osErrorFuncs are the os functions in ioCallRe; all return error.
var osErrorFuncs = map[string]bool{"WriteFile": true, "Rename": true, "Mkdir": true, "MkdirAll": true, "Truncate": true}

// errorCall reports a call whose last result is taken to be an error: an
// os persistence function, or an Append/Save/Persist method unless a
// repository declaration of that name returns no error last. Other
// stdlib-named methods (bytes.Buffer.Truncate) and UI helpers such as a
// block's AppendOutput stay out of the check.
func (fc *fileCtx) errorCall(call *ast.CallExpr) bool {
	name := calleeName(call)
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "os" {
			return osErrorFuncs[name]
		}
	}
	if !persistMethodRe.MatchString(name) {
		return false
	}
	return !fc.ErrorFuncs.returnsNoError(name)
}

var persistMethodRe = regexp.MustCompile(`^(?:Append|Save|Persist)[A-Z]`)

// trailingBlank reports an assignment whose last target, the error, is `_`,
// whether or not earlier results are kept (`id, _ := in.AppendMessage(m)`).
func trailingBlank(lhs []ast.Expr) bool {
	if len(lhs) == 0 {
		return false
	}
	id, ok := lhs[len(lhs)-1].(*ast.Ident)
	return ok && id.Name == "_"
}

// comparesErrNil reports `err <op> nil`.
func comparesErrNil(cond ast.Expr, op token.Token) bool {
	b, ok := cond.(*ast.BinaryExpr)
	return ok && b.Op == op && isNil(b.Y) && exprName(b.X) == "err"
}
