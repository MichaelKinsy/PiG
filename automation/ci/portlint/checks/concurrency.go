// SPDX-License-Identifier: MIT

package checks

import (
	"go/ast"
	"go/token"
	"go/types"
	"regexp"
	"strings"
)

// inLoop reports whether the innermost enclosing function-bounded ancestor chain of stack contains a for or range statement.
func inLoop(stack []ast.Node) bool {
	for i := len(stack) - 2; i >= 0; i-- {
		switch stack[i].(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			return true
		case *ast.FuncLit, *ast.FuncDecl:
			return false
		}
	}
	return false
}

var stopNameRe = regexp.MustCompile(`(?i)(done|quit|stop|closed|cancel|shutdown|exit|ctx|abort|kill|term)`)

// isStopRecv reports whether a select case receives from a channel that signals cancellation.
func isStopRecv(cc *ast.CommClause) bool {
	var expr ast.Expr
	switch c := cc.Comm.(type) {
	case *ast.ExprStmt:
		expr = c.X
	case *ast.AssignStmt:
		if len(c.Rhs) == 1 {
			expr = c.Rhs[0]
		}
	}
	u, ok := ast.Unparen(expr).(*ast.UnaryExpr)
	if !ok || u.Op != token.ARROW {
		return false
	}
	return stopNameRe.MatchString(exprText(u.X))
}

func exprText(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return exprText(e.X) + "." + e.Sel.Name
	case *ast.CallExpr:
		return exprText(e.Fun) + "()"
	case *ast.ParenExpr:
		return exprText(e.X)
	case *ast.IndexExpr:
		return exprText(e.X) + "[]"
	}
	return ""
}

// GoLifetime flags goroutines and loops that outlive their owner.
var GoLifetime = newCheck("golifetime",
	"time.After in a loop, a looping select with no cancellation case, or a goroutine with no context, WaitGroup, errgroup or channel owner",
	Med, "catalog refreshes after Close",
	func(p *Pass) {
		p.Inspect.WithStack([]ast.Node{(*ast.CallExpr)(nil), (*ast.SelectStmt)(nil), (*ast.GoStmt)(nil)}, func(n ast.Node, push bool, stack []ast.Node) bool {
			if !push || p.IsTest(n.Pos()) {
				return true
			}
			switch n := n.(type) {
			case *ast.CallExpr:
				if p.IsFunc(n, "time", "After") && inLoop(stack) {
					p.Reportf(n.Pos(), "time.After in a loop leaks a timer per iteration; use a reused time.Timer")
				}
			case *ast.SelectStmt:
				if !inLoop(stack) {
					return true
				}
				for _, c := range n.Body.List {
					cc := c.(*ast.CommClause)
					if cc.Comm == nil || isStopRecv(cc) {
						return true
					}
				}
				p.Reportf(n.Pos(), "select in a loop with no ctx.Done()/stop case and no default")
			case *ast.GoStmt:
				if !p.goOwned(n) {
					p.Reportf(n.Pos(), "goroutine has no context, WaitGroup, errgroup or channel to own its lifetime")
				}
			}
			return true
		})
	})

// goOwned reports whether a go statement mentions something that bounds or joins the goroutine.
func (p *Pass) goOwned(g *ast.GoStmt) bool {
	owned := false
	check := func(n ast.Node) bool {
		if owned {
			return false
		}
		switch n := n.(type) {
		case *ast.SendStmt:
			owned = true
		case *ast.UnaryExpr:
			if n.Op == token.ARROW {
				owned = true
			}
		case *ast.RangeStmt:
			if _, ok := types.Unalias(p.TypesInfo.TypeOf(n.X)).Underlying().(*types.Chan); ok {
				owned = true
			}
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "close" {
				owned = true
			}
		case ast.Expr:
			if t := p.TypesInfo.TypeOf(n); t != nil && ownerType(t) {
				owned = true
			}
		}
		return !owned
	}
	ast.Inspect(g.Call, check)
	return owned
}

func ownerType(t types.Type) bool {
	return NamedIs(t, "context", "Context") || NamedIs(t, "sync", "WaitGroup") ||
		NamedIs(t, "golang.org/x/sync/errgroup", "Group") || NamedIs(t, "sync", "Once") || NamedIs(t, "testing", "T")
}

// GoRecover flags a fan-out goroutine that has no recover boundary.
var GoRecover = newCheck("gorecover",
	"goroutine started in a loop (a fan-out over untrusted input) whose body has no deferred recover",
	Med, "websearch fan-outs",
	func(p *Pass) {
		p.Inspect.WithStack([]ast.Node{(*ast.GoStmt)(nil)}, func(n ast.Node, push bool, stack []ast.Node) bool {
			if !push || p.IsTest(n.Pos()) || !inLoop(stack) {
				return true
			}
			lit, ok := n.(*ast.GoStmt).Call.Fun.(*ast.FuncLit)
			if !ok {
				return true
			}
			recovers := false
			ast.Inspect(lit.Body, func(m ast.Node) bool {
				if call, ok := m.(*ast.CallExpr); ok {
					if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "recover" {
						recovers = true
					}
					if fn := p.Callee(call); fn != nil && strings.Contains(strings.ToLower(fn.Name()), "recover") {
						recovers = true
					}
				}
				return !recovers
			})
			if !recovers {
				p.Reportf(n.Pos(), "fan-out goroutine has no deferred recover; one panic ends the process")
			}
			return true
		})
	})

// DoubleClose flags close of a shared channel in a function with no once or lock guard.
var DoubleClose = newCheck("doubleclose",
	"close of a field or captured channel in a function with no sync.Once, lock or compare-and-swap guard",
	Med, "double close flake",
	func(p *Pass) {
		p.Inspect.Preorder([]ast.Node{(*ast.FuncDecl)(nil)}, func(n ast.Node) {
			body := n.(*ast.FuncDecl).Body
			if body == nil {
				return
			}
			var closes []*ast.CallExpr
			guarded := false
			ast.Inspect(body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "close" && len(call.Args) == 1 {
					if _, builtin := p.TypesInfo.Uses[id].(*types.Builtin); builtin {
						if sel, ok := ast.Unparen(call.Args[0]).(*ast.SelectorExpr); ok && p.TypesInfo.Selections[sel] != nil {
							closes = append(closes, call)
						}
					}
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					switch sel.Sel.Name {
					case "Do", "Lock", "RLock", "CompareAndSwap", "Swap", "TryLock":
						guarded = true
					}
				}
				return true
			})
			if guarded {
				return
			}
			for _, c := range closes {
				if !p.IsTest(c.Pos()) {
					p.Reportf(c.Pos(), "close of a shared channel with no sync.Once or lock guard; a second call panics")
				}
			}
		})
	})

// CtxMapping flags async-mapped code that drops the caller's cancellation.
var CtxMapping = newCheck("ctxmapping",
	"context.Background()/TODO() or a context-free request or command inside a function that already has a context.Context or *http.Request",
	Med, "AbortSignal not mapped to context",
	func(p *Pass) {
		p.Funcs(func(_ string, typ *ast.FuncType, body *ast.BlockStmt) {
			hasCtx := false
			for _, f := range typ.Params.List {
				if t := p.TypesInfo.TypeOf(f.Type); t != nil && (NamedIs(t, "context", "Context") || NamedIs(t, "net/http", "Request")) {
					hasCtx = true
				}
			}
			if !hasCtx {
				return
			}
			ast.Inspect(body, func(n ast.Node) bool {
				if lit, ok := n.(*ast.FuncLit); ok && lit.Body != body {
					return false
				}
				call, ok := n.(*ast.CallExpr)
				if !ok || p.IsTest(call.Pos()) {
					return true
				}
				switch {
				case p.IsFunc(call, "context", "Background"), p.IsFunc(call, "context", "TODO"):
					p.Reportf(call.Pos(), "context.%s() inside a function that has a caller context; Pi threads the AbortSignal", p.Callee(call).Name())
				case p.IsFunc(call, "net/http", "NewRequest"):
					p.Reportf(call.Pos(), "http.NewRequest ignores the caller's context; use NewRequestWithContext")
				case p.IsFunc(call, "os/exec", "Command"):
					p.Reportf(call.Pos(), "exec.Command ignores the caller's context; use CommandContext")
				}
				return true
			})
		})
	})

// GoDropsError flags a go statement whose call returns an error that nothing receives.
var GoDropsError = newCheck("godropserr",
	"go statement whose call returns an error: the error is dropped where a rejected Promise would surface",
	Med, "Promise rejection must surface",
	func(p *Pass) {
		p.Inspect.Preorder([]ast.Node{(*ast.GoStmt)(nil)}, func(n ast.Node) {
			g := n.(*ast.GoStmt)
			if p.IsTest(g.Pos()) {
				return
			}
			sig, ok := p.TypesInfo.TypeOf(g.Call.Fun).(*types.Signature)
			if !ok {
				return
			}
			for v := range sig.Results().Variables() {
				if types.Identical(v.Type(), types.Universe.Lookup("error").Type()) {
					p.Reportf(g.Pos(), "go statement discards the call's error result")
					return
				}
			}
		})
	})
