// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"
)

// hotFunc reports whether fn's doc comment marks it hot: `//perf:hot` (an optional reason may follow), or portlint's `//pig:hotpath`.
// A hot function runs once per streamed event, frame, keystroke or chunk; chan-hop, open-per-call and timer-per-call count only there.
func hotFunc(fn *ast.FuncDecl) bool {
	if fn.Doc == nil {
		return false
	}
	for _, c := range fn.Doc.List {
		text := strings.TrimSpace(c.Text)
		if text == "//pig:hotpath" || text == "//perf:hot" || strings.HasPrefix(text, "//perf:hot ") {
			return true
		}
	}
	return false
}

// timerCall is a timer or deadline a call allocates (timer-per-call); time.AfterFunc and context.AfterFunc are chan-hop's, as each schedules a callback on another goroutine.
func timerCall(path, name string) bool {
	switch path {
	case "time":
		return name == "NewTimer" || name == "After" || name == "NewTicker" || name == "Tick"
	case "context":
		return name == "WithTimeout" || name == "WithDeadline" || name == "WithTimeoutCause" || name == "WithDeadlineCause"
	}
	return false
}

// openCall is a call that opens a file descriptor for one use (open-per-call).
func openCall(path, name string) bool {
	return path == "os" && (name == "Open" || name == "OpenFile" || name == "Create" || name == "ReadFile" || name == "WriteFile" || name == "OpenInRoot")
}

// scanHot reports the per-call hand-offs, opens and timers of a hot function's body, closures included (a closure built on the hot path runs on it); the body of a `go` statement runs on another goroutine and is not walked.
func (s *scanner) scanHot(owner string, body *ast.BlockStmt) {
	info := s.pkg.TypesInfo
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.GoStmt:
			s.add(owner, "chan-hop", "go", n.Pos())
			// The arguments are evaluated on the hot goroutine; a function literal's body is not.
			if _, ok := n.Call.Fun.(*ast.FuncLit); !ok {
				ast.Inspect(n.Call.Fun, visit)
			}
			for _, arg := range n.Call.Args {
				ast.Inspect(arg, visit)
			}
			return false
		case *ast.SendStmt:
			s.add(owner, "chan-hop", "send", n.Pos())
		case *ast.UnaryExpr:
			if n.Op == token.ARROW {
				s.add(owner, "chan-hop", "<-", n.Pos())
			}
		case *ast.RangeStmt:
			if t := info.TypeOf(n.X); t != nil {
				if _, ok := t.Underlying().(*types.Chan); ok {
					s.add(owner, "chan-hop", "range chan", n.Pos())
				}
			}
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "make" && len(n.Args) > 0 {
				if t := info.TypeOf(n.Args[0]); t != nil {
					if _, ok := t.Underlying().(*types.Chan); ok {
						s.add(owner, "chan-hop", "make(chan)", n.Pos())
					}
				}
				return true
			}
			p, name := s.callee(n)
			switch {
			case (p == "time" || p == "context") && name == "AfterFunc":
				s.add(owner, "chan-hop", p+".AfterFunc", n.Pos())
			case timerCall(p, name):
				s.add(owner, "timer-per-call", p+"."+name, n.Pos())
			case openCall(p, name):
				s.add(owner, "open-per-call", "os."+name, n.Pos())
			}
		}
		return true
	}
	ast.Inspect(body, visit)
}

// isEncodeName reports a function or method that returns its argument's JSON text: Marshal and its kin, MarshalJSON methods and Stringify helpers.
func isEncodeName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "marshal") || strings.HasPrefix(lower, "mustmarshal") || strings.Contains(lower, "stringify")
}

// isDecodeName reports a function or method that decodes JSON text into a value.
func isDecodeName(path, name string) bool {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "decoderune") || strings.HasPrefix(lower, "decodelastrune") {
		return false
	}
	if name == "NewDecoder" {
		return isJSONPkg(path) || strings.HasSuffix(path, "/jsontext")
	}
	return strings.HasPrefix(lower, "unmarshal") || strings.HasPrefix(lower, "decode")
}

// isCapCheck reports a call that reads a buffer's capacity: the builtin cap or a Cap method.
func isCapCheck(call *ast.CallExpr) bool {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name == "cap"
	case *ast.SelectorExpr:
		return fun.Sel.Name == "Cap"
	}
	return false
}

// isPoolPut reports a Put on a sync.Pool.
func (s *scanner) isPoolPut(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Put" || len(call.Args) != 1 {
		return false
	}
	fn, ok := s.pkg.TypesInfo.Uses[sel.Sel].(*types.Func)
	if !ok {
		return false
	}
	recv := fn.Signature().Recv()
	if recv == nil {
		return false
	}
	t := recv.Type()
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "sync" && n.Obj().Name() == "Pool"
}

// growable reports a pooled value whose memory grows with use: a slice, a pointer to one, a value with a Cap method (bytes.Buffer), or a struct (or pointer to one) holding such a field.
func growable(t types.Type, depth int) bool {
	if t == nil || depth > 2 {
		return false
	}
	if ms := types.NewMethodSet(t); ms.Lookup(nil, "Cap") != nil {
		return true
	}
	if p, ok := t.Underlying().(*types.Pointer); ok {
		if ms := types.NewMethodSet(p); ms.Lookup(nil, "Cap") != nil {
			return true
		}
		return growable(p.Elem(), depth+1)
	}
	if ms := types.NewMethodSet(types.NewPointer(t)); ms.Lookup(nil, "Cap") != nil {
		return true
	}
	switch u := t.Underlying().(type) {
	case *types.Slice:
		return true
	case *types.Struct:
		for field := range u.Fields() {
			if growable(field.Type(), depth+1) {
				return true
			}
		}
	}
	return false
}
