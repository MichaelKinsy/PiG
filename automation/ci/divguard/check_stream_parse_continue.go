// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"go/token"
	"slices"
)

const streamParseContinueName = "stream-parse-continue"

// streamParseContinue flags a decode error that a loop skips: an `if` on a
// json.Unmarshal (or Decode) error whose branch ends in `continue`, or a
// decode whose error is discarded, inside a `for` loop. In a stream loop a
// corrupt chunk then vanishes, and the turn ends as a success with text,
// tool arguments or the stop reason missing (AI-08). Pi's SSE decoders
// rethrow parse errors.
var streamParseContinue = check{
	Name:    streamParseContinueName,
	Doc:     "decode error skipped with continue or discarded inside a loop",
	Applies: inHot,
	Run:     runStreamParseContinue,
}

// decodeNames are calls whose error means the input was not understood.
var decodeNames = map[string]bool{"Unmarshal": true, "Decode": true}

func runStreamParseContinue(fc *fileCtx) []Hit {
	var hits []Hit
	report := func(pos token.Pos, stack []ast.Node, msg string) {
		if h, ok := fc.hit(streamParseContinueName, pos, stack, msg); ok {
			hits = append(hits, h)
		}
	}
	inspect(fc.File, func(n ast.Node, stack []ast.Node) bool {
		if !inLoop(stack) {
			return true
		}
		switch n := n.(type) {
		case *ast.IfStmt:
			if call := decodeErrCond(n, stack); call != nil && endsInContinue(n.Body) && !surfaces(n.Body, errIdent(n)) {
				report(n.Pos(), stack, calleeName(call)+" error is skipped with continue")
			}
		case *ast.ExprStmt:
			if call, ok := n.X.(*ast.CallExpr); ok && decodeNames[calleeName(call)] {
				report(n.Pos(), stack, calleeName(call)+" error is ignored")
			}
		case *ast.AssignStmt:
			if call := discardedDecode(n); call != nil {
				report(n.Pos(), stack, calleeName(call)+" error is discarded")
			}
		}
		return true
	})
	return hits
}

// inLoop reports whether the innermost function on the stack encloses the
// node in a for or range loop.
func inLoop(stack []ast.Node) bool {
	for _, s := range slices.Backward(stack) {
		switch s.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			return true
		case *ast.FuncLit, *ast.FuncDecl:
			return false
		}
	}
	return false
}

// decodeErrCond returns the decode call whose error the if tests: `if
// json.Unmarshal(...) != nil`, `if err := json.Unmarshal(...); err != nil`,
// or `if err != nil` right after `err = json.Unmarshal(...)`.
func decodeErrCond(ifs *ast.IfStmt, stack []ast.Node) *ast.CallExpr {
	cond, ok := ifs.Cond.(*ast.BinaryExpr)
	if !ok || cond.Op != token.NEQ || !isNil(cond.Y) {
		return nil
	}
	if call, ok := cond.X.(*ast.CallExpr); ok && decodeNames[calleeName(call)] {
		return call
	}
	errName := exprName(cond.X)
	if as, ok := ifs.Init.(*ast.AssignStmt); ok {
		return decodeAssigning(as, errName)
	}
	if len(stack) == 0 {
		return nil
	}
	if block, ok := stack[len(stack)-1].(*ast.BlockStmt); ok {
		for i, st := range block.List {
			if st == ifs && i > 0 {
				if as, ok := block.List[i-1].(*ast.AssignStmt); ok {
					return decodeAssigning(as, errName)
				}
			}
		}
	}
	return nil
}

// decodeAssigning returns the decode call when as assigns its error to name.
func decodeAssigning(as *ast.AssignStmt, name string) *ast.CallExpr {
	if len(as.Rhs) != 1 || name == "" {
		return nil
	}
	call, ok := as.Rhs[0].(*ast.CallExpr)
	if !ok || !decodeNames[calleeName(call)] {
		return nil
	}
	if last := as.Lhs[len(as.Lhs)-1]; exprName(last) == name {
		return call
	}
	return nil
}

// discardedDecode returns the decode call of `_ = json.Unmarshal(...)`.
func discardedDecode(as *ast.AssignStmt) *ast.CallExpr {
	if len(as.Rhs) != 1 || len(as.Lhs) == 0 {
		return nil
	}
	call, ok := as.Rhs[0].(*ast.CallExpr)
	if !ok || !decodeNames[calleeName(call)] {
		return nil
	}
	if id, ok := as.Lhs[len(as.Lhs)-1].(*ast.Ident); ok && id.Name == "_" {
		return call
	}
	return nil
}

// errIdent is the error variable an `if err != nil` tests, or "".
func errIdent(ifs *ast.IfStmt) string {
	if cond, ok := ifs.Cond.(*ast.BinaryExpr); ok {
		if id, ok := cond.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

// logNames are calls that only record an error for a developer, plus the
// Error accessor they format it with.
var logNames = map[string]bool{"Error": true, "Printf": true, "Println": true, "Print": true, "Fprintf": true, "Fprintln": true, "Debug": true, "Debugf": true, "Info": true, "Infof": true, "Warn": true, "Warnf": true, "Log": true, "Logf": true, "logf": true}

// surfaces reports whether body passes the error named errName to a call
// other than a log call, such as an error reply to the client.
func surfaces(body *ast.BlockStmt, errName string) bool {
	if errName == "" {
		return false
	}
	found := false
	inspect(body, func(n ast.Node, stack []ast.Node) bool {
		if id, ok := n.(*ast.Ident); !ok || id.Name != errName || found {
			return !found
		}
		for _, anc := range stack {
			if call, ok := anc.(*ast.CallExpr); ok && !logNames[calleeName(call)] {
				found = true
			}
		}
		return false
	})
	return found
}

func endsInContinue(b *ast.BlockStmt) bool {
	if b == nil || len(b.List) == 0 {
		return false
	}
	br, ok := b.List[len(b.List)-1].(*ast.BranchStmt)
	return ok && br.Tok == token.CONTINUE
}

func isNil(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}
