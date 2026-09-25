// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"go/token"
	"strings"
)

const hookFailOpenName = "hook-fail-open"

// hookFailOpen flags a hook that answers an error with its zero result:
// `if err != nil || result == nil { return agent.ToolCallHookResult{} }`.
// A zero hook result means "proceed", so a failing tool_call handler let
// bash and write run (AGENT-04, EXT-05). Pi rethrows the handler error and
// blocks the tool. Only results whose type name ends in HookResult are
// checked, where the zero value always means "no objection".
var hookFailOpen = check{
	Name:    hookFailOpenName,
	Doc:     "hook returns its zero (proceed) result when its handler fails",
	Applies: inHot,
	Run:     runHookFailOpen,
}

func runHookFailOpen(fc *fileCtx) []Hit {
	var hits []Hit
	inspect(fc.File, func(n ast.Node, stack []ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || !testsErrNotNil(ifs.Cond) || len(ifs.Body.List) != 1 {
			return true
		}
		ret, ok := ifs.Body.List[0].(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 || !isZeroHookResult(fc, ret.Results[0]) {
			return true
		}
		if h, ok := fc.hit(hookFailOpenName, ifs.Pos(), stack, "hook error returns the zero "+exprText(fc, ret.Results[0])+", which lets the action proceed"); ok {
			hits = append(hits, h)
		}
		return true
	})
	return hits
}

// testsErrNotNil reports a condition containing `err != nil`.
func testsErrNotNil(cond ast.Expr) bool {
	found := false
	ast.Inspect(cond, func(n ast.Node) bool {
		if b, ok := n.(*ast.BinaryExpr); ok && b.Op == token.NEQ && isNil(b.Y) && exprName(b.X) == "err" {
			found = true
		}
		return !found
	})
	return found
}

func isZeroHookResult(fc *fileCtx, e ast.Expr) bool {
	lit, ok := e.(*ast.CompositeLit)
	return ok && len(lit.Elts) == 0 && lit.Type != nil && strings.HasSuffix(exprText(fc, lit.Type), "HookResult")
}
