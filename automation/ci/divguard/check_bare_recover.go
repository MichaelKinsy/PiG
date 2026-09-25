// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"go/token"
	"slices"
)

const bareRecoverName = "bare-recover"

// bareRecover flags a recover() whose panic value is thrown away: a bare
// `recover()` or `_ = recover()`, a `recover() != nil` test, or `r :=
// recover()` where r is only compared with nil. The panic then vanishes
// with no event, error or log. A panicking listener silently ended the
// session event forwarder, and the next run hung (AGENT-16). Pi surfaces a
// listener's exception as a failed run.
var bareRecover = check{
	Name:    bareRecoverName,
	Doc:     "recover() whose panic value is discarded",
	Applies: inHot,
	Run:     runBareRecover,
}

func runBareRecover(fc *fileCtx) []Hit {
	var hits []Hit
	inspect(fc.File, func(n ast.Node, stack []ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isRecover(call) || recoverValueUsed(call, stack) {
			return true
		}
		if h, ok := fc.hit(bareRecoverName, call.Pos(), stack, "recover() discards the panic value; report it as an error or event"); ok {
			hits = append(hits, h)
		}
		return true
	})
	return hits
}

func isRecover(call *ast.CallExpr) bool {
	id, ok := call.Fun.(*ast.Ident)
	return ok && id.Name == "recover" && len(call.Args) == 0
}

// recoverValueUsed reports whether the recovered value reaches anything
// other than a nil comparison.
func recoverValueUsed(call *ast.CallExpr, stack []ast.Node) bool {
	if len(stack) == 0 {
		return false
	}
	switch p := stack[len(stack)-1].(type) {
	case *ast.ExprStmt, *ast.DeferStmt:
		return false
	case *ast.BinaryExpr:
		return !isNil(p.X) && !isNil(p.Y)
	case *ast.AssignStmt:
		if len(p.Lhs) != 1 {
			return true
		}
		id, ok := p.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		if id.Name == "_" {
			return false
		}
		return usedBeyondNilCheck(id.Name, enclosingFunc(stack), p)
	}
	return true
}

func enclosingFunc(stack []ast.Node) ast.Node {
	for _, s := range slices.Backward(stack) {
		switch f := s.(type) {
		case *ast.FuncLit, *ast.FuncDecl:
			return f
		}
	}
	return nil
}

// usedBeyondNilCheck reports a use of name in fn, other than its defining
// assignment, that is not one side of `== nil` or `!= nil`.
func usedBeyondNilCheck(name string, fn ast.Node, def *ast.AssignStmt) bool {
	if fn == nil {
		return true
	}
	used := false
	inspect(fn, func(n ast.Node, stack []ast.Node) bool {
		if used || n == def.Lhs[0] {
			return !used
		}
		id, ok := n.(*ast.Ident)
		if !ok || id.Name != name {
			return true
		}
		if len(stack) > 0 {
			if b, ok := stack[len(stack)-1].(*ast.BinaryExpr); ok && (b.Op == token.EQL || b.Op == token.NEQ) && (isNil(b.X) || isNil(b.Y)) {
				return true
			}
		}
		used = true
		return false
	})
	return used
}
