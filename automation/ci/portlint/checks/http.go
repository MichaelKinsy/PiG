// SPDX-License-Identifier: MIT

package checks

import (
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strings"
)

// HTTPRequestReuse flags a request body rewritten in place or a request re-sent from a loop without a clone.
var HTTPRequestReuse = newCheck("httprequestreuse",
	"assignment to a *http.Request's Body in RoundTrip, or a request re-sent in a loop with no Clone or GetBody: the transport may still read the previous body",
	High, "ai/provider_retry.go retry rewrote req.Body (VictoriaMetrics http race)",
	func(p *Pass) {
		p.Inspect.WithStack([]ast.Node{(*ast.AssignStmt)(nil), (*ast.CallExpr)(nil)}, func(n ast.Node, push bool, stack []ast.Node) bool {
			if !push || p.IsTest(n.Pos()) {
				return true
			}
			switch n := n.(type) {
			case *ast.AssignStmt:
				for _, l := range n.Lhs {
					sel, ok := l.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "Body" {
						continue
					}
					if t := p.TypesInfo.TypeOf(sel.X); t == nil || !NamedIs(t, "net/http", "Request") {
						continue
					}
					fn := enclosingDecl(stack)
					id, isIdent := sel.X.(*ast.Ident)
					if fn != nil && fn.Name.Name == "RoundTrip" && isIdent && declaredIn(p, id, fn.Type.Params) {
						p.Reportf(n.Pos(), "RoundTrip assigns req.Body on the caller's request; send a Clone with a fresh body")
					}
				}
			case *ast.CallExpr:
				if !inLoop(stack) || len(n.Args) == 0 {
					return true
				}
				isSend := p.IsMethod(n, "net/http", "Client", "Do") || p.IsMethod(n, "net/http", "RoundTripper", "RoundTrip") ||
					(p.Callee(n) != nil && p.Callee(n).Name() == "RoundTrip")
				if !isSend {
					return true
				}
				id, ok := ast.Unparen(n.Args[0]).(*ast.Ident)
				if !ok {
					return true
				}
				obj := p.TypesInfo.ObjectOf(id)
				loop := enclosingLoop(stack)
				if obj == nil || loop == nil || (obj.Pos() >= loop.Pos() && obj.Pos() <= loop.End()) {
					return true
				}
				if loopRewinds(p, loop, obj) {
					return true
				}
				p.Reportf(n.Pos(), "request %s is re-sent from a loop with no Clone, NewRequest or GetBody; its body is spent after the first send", id.Name)
			}
			return true
		})
	})

func enclosingDecl(stack []ast.Node) *ast.FuncDecl {
	for _, s := range slices.Backward(stack) {
		if fd, ok := s.(*ast.FuncDecl); ok {
			return fd
		}
	}
	return nil
}

func enclosingLoop(stack []ast.Node) ast.Node {
	for i := len(stack) - 2; i >= 0; i-- {
		switch stack[i].(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			return stack[i]
		case *ast.FuncLit, *ast.FuncDecl:
			return nil
		}
	}
	return nil
}

// loopRewinds reports whether the loop body reassigns the request or reads GetBody or Clone.
func loopRewinds(p *Pass, loop ast.Node, req types.Object) bool {
	rewinds := false
	ast.Inspect(loop, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for _, l := range n.Lhs {
				if id, ok := l.(*ast.Ident); ok && p.TypesInfo.ObjectOf(id) == req {
					rewinds = true
				}
			}
		case *ast.SelectorExpr:
			if n.Sel.Name == "GetBody" || n.Sel.Name == "Clone" {
				rewinds = true
			}
		}
		return !rewinds
	})
	return rewinds
}

// TransportPerRequest reports a new http.Transport built outside a constructor (report only).
var TransportPerRequest = newCheck("transportperrequest",
	"http.Transport literal or Transport.Clone() inside a function that is not a constructor: a transport per request defeats connection reuse (report only)",
	Low, "audit H1",
	func(p *Pass) {
		p.Inspect.WithStack([]ast.Node{(*ast.CompositeLit)(nil), (*ast.CallExpr)(nil)}, func(n ast.Node, push bool, stack []ast.Node) bool {
			if !push || p.IsTest(n.Pos()) {
				return true
			}
			switch n := n.(type) {
			case *ast.CompositeLit:
				if t := p.TypesInfo.TypeOf(n); t == nil || !NamedIs(t, "net/http", "Transport") {
					return true
				}
			case *ast.CallExpr:
				if !p.IsMethod(n, "net/http", "Transport", "Clone") {
					return true
				}
			}
			fn := enclosingDecl(stack)
			if fn == nil {
				return true
			}
			name := fn.Name.Name
			if name == "init" || strings.HasPrefix(name, "New") || strings.HasPrefix(name, "new") || strings.HasPrefix(name, "Default") || strings.HasPrefix(name, "default") {
				return true
			}
			p.Reportf(n.Pos(), "%s builds an http.Transport; share one per host so connections are reused", name)
			return true
		})
	})

var errCompareFuncs = map[string]bool{"Contains": true, "HasPrefix": true, "HasSuffix": true, "EqualFold": true, "Index": true}

// isErrorString reports whether e is a call of the Error() string method.
func (p *Pass) isErrorString(e ast.Expr) bool {
	call, ok := ast.Unparen(e).(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Error" {
		return false
	}
	t := p.TypesInfo.TypeOf(sel.X)
	return t != nil && types.Implements(t, errorIface)
}

var errorIface = types.Universe.Lookup("error").Type().Underlying().(*types.Interface)

// ErrorIdentity flags errors classified by their message.
var ErrorIdentity = newCheck("erroridentity",
	"err.Error() compared or searched as text: use errors.Is or errors.As on a typed or sentinel error",
	Med, "error classes lost when a message is reworded",
	func(p *Pass) {
		p.Inspect.Preorder([]ast.Node{(*ast.BinaryExpr)(nil), (*ast.CallExpr)(nil)}, func(n ast.Node) {
			if p.IsTest(n.Pos()) {
				return
			}
			switch n := n.(type) {
			case *ast.BinaryExpr:
				if (n.Op == token.EQL || n.Op == token.NEQ) && (p.isErrorString(n.X) || p.isErrorString(n.Y)) {
					p.Reportf(n.Pos(), "comparing err.Error() text; use errors.Is or errors.As")
				}
			case *ast.CallExpr:
				fn := p.Callee(n)
				if fn == nil || fn.Pkg() == nil || (fn.Pkg().Path() != "strings" && fn.Pkg().Path() != "bytes") || !errCompareFuncs[fn.Name()] || len(n.Args) == 0 {
					return
				}
				if p.isErrorString(n.Args[0]) {
					p.Reportf(n.Pos(), "strings.%s on err.Error(); use errors.Is or errors.As", fn.Name())
				}
			}
		})
	})

// declaredIn reports whether id names one of the parameters in fields.
func declaredIn(p *Pass, id *ast.Ident, fields *ast.FieldList) bool {
	obj := p.TypesInfo.ObjectOf(id)
	if obj == nil {
		return false
	}
	for _, f := range fields.List {
		for _, name := range f.Names {
			if p.TypesInfo.ObjectOf(name) == obj {
				return true
			}
		}
	}
	return false
}
