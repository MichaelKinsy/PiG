// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"go/token"
	"regexp"
	"slices"
	"strconv"
)

const stopReasonSuccessName = "stop-reason-success"

// stopReasonSuccess flags three ways a provider turns a stream it did not
// understand into a clean `stop`:
//   - a stop-reason or finish-reason mapper whose `default:` returns
//     success, so an unknown reason such as model_context_window_exceeded
//     ends the turn silently (AI-01, AI-15); Pi throws "Unhandled stop
//     reason";
//   - an empty stop reason replaced with success (AI-05); Pi starts from
//     "pending" and throws when the stream ends without one;
//   - `[DONE]` finishing the stream as success (AI-06); Pi throws when no
//     terminal event arrived.
var stopReasonSuccess = check{
	Name:    stopReasonSuccessName,
	Doc:     "unknown, empty or missing stop reason mapped to success",
	Applies: inHot,
	Run:     runStopReasonSuccess,
}

var (
	mapperNameRe  = regexp.MustCompile(`(?i)(stop|finish)_?reason`)
	successStopRe = regexp.MustCompile(`^(?:\w+\.)?StopReason(?:Stop|EndTurn)$|^"(?:stop|end_turn)"$`)
)

func runStopReasonSuccess(fc *fileCtx) []Hit {
	var hits []Hit
	report := func(pos token.Pos, stack []ast.Node, msg string) {
		if h, ok := fc.hit(stopReasonSuccessName, pos, stack, msg); ok {
			hits = append(hits, h)
		}
	}
	inspect(fc.File, func(n ast.Node, stack []ast.Node) bool {
		switch n := n.(type) {
		case *ast.CaseClause:
			if n.List == nil && mapperNameRe.MatchString(enclosingFuncName(stack)) && returnsSuccess(fc, n.Body) {
				report(n.Pos(), stack, "stop-reason mapper maps an unknown reason to success")
			}
		case *ast.IfStmt:
			switch {
			case comparesTo(n.Cond, `""`) && assignsSuccess(fc, n.Body):
				report(n.Pos(), stack, "an empty stop reason becomes success")
			case comparesTo(n.Cond, `"[DONE]"`) && finishesSuccess(fc, n.Body):
				report(n.Pos(), stack, "[DONE] before a terminal event finishes the stream as success")
			}
		}
		return true
	})
	return hits
}

func enclosingFuncName(stack []ast.Node) string {
	for _, s := range slices.Backward(stack) {
		if f, ok := s.(*ast.FuncDecl); ok {
			return f.Name.Name
		}
	}
	return ""
}

func isSuccessStop(fc *fileCtx, e ast.Expr) bool {
	return successStopRe.MatchString(exprText(fc, e))
}

func returnsSuccess(fc *fileCtx, body []ast.Stmt) bool {
	for _, st := range body {
		if ret, ok := st.(*ast.ReturnStmt); ok && len(ret.Results) > 0 && isSuccessStop(fc, ret.Results[0]) {
			return true
		}
	}
	return false
}

// comparesTo reports `x == lit` or `lit == x`.
func comparesTo(cond ast.Expr, lit string) bool {
	b, ok := cond.(*ast.BinaryExpr)
	if !ok || b.Op != token.EQL {
		return false
	}
	for _, side := range []ast.Expr{b.X, b.Y} {
		if bl, ok := side.(*ast.BasicLit); ok && bl.Kind == token.STRING {
			if v, err := strconv.Unquote(bl.Value); err == nil && strconv.Quote(v) == lit {
				return true
			}
		}
	}
	return false
}

func assignsSuccess(fc *fileCtx, body *ast.BlockStmt) bool {
	for _, st := range body.List {
		if as, ok := st.(*ast.AssignStmt); ok && len(as.Rhs) == 1 && isSuccessStop(fc, as.Rhs[0]) {
			return true
		}
	}
	return false
}

// finishesSuccess reports a call in body whose first argument is a success
// stop reason, such as `builder.done(StopReasonStop, ...)`.
func finishesSuccess(fc *fileCtx, body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && len(call.Args) > 0 && isSuccessStop(fc, call.Args[0]) {
			found = true
		}
		return !found
	})
	return found
}
