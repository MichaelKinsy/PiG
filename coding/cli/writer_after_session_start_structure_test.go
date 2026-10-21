package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Pi's stdout writer subscribes after `await session.bindExtensions()` (rpc-mode.ts:316-361, print-mode.ts:76-108), so the events a session_start handler
// emits are never written. The sinks that write a Session-origin event on their own, apart from the Session's event subscription, are the RPC
// name listener and entry_appended writer (rpc_mode_runtime.go) and the JSON mode's entry_appended writer (print_mode.go). Each must test the
// "writing" flag its bind sets after session_start, and the JSON mode must subscribe its event writer after the session_start emit. A new sink that
// writes a Session-origin event without the flag, or a JSON subscription moved before session_start, fails here.
func TestSessionOriginWritersObeyTheWriterSubscribedGate(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		// These files may build an event or call the constructor without the flag: rpc_types.go defines rpcSessionInfoChanged; rpc_events.go converts the Session's own events for the event subscription, which the writer subscribes late; session_extension_actions.go builds the entry_appended event and hands it to the mode's gated emit.
		literalOK := slices.Contains([]string{"rpc_types.go", "rpc_events.go", "session_extension_actions.go"}, name)
		var stack []ast.Node
		ast.Inspect(file, func(node ast.Node) bool {
			if node == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, node)
			literal := false
			switch node := node.(type) {
			case *ast.CompositeLit:
				if ident, ok := node.Type.(*ast.Ident); ok && ident.Name == "RPCEntryAppendedEvent" {
					literal = true
				}
			case *ast.CallExpr:
				if ident, ok := node.Fun.(*ast.Ident); ok && ident.Name == "rpcSessionInfoChanged" {
					literal = true
				}
			}
			if !literal || literalOK {
				return true
			}
			// The innermost function literal around the writer must test the flag.
			for _, s := range slices.Backward(stack) {
				if fn, ok := s.(*ast.FuncLit); ok {
					if !callsLoadOnWriting(fn.Body) {
						t.Errorf("%s: %s writes a Session-origin event without testing the writer-subscribed flag (writing.Load()) in its function", name, fset.Position(node.Pos()))
					}
					return true
				}
			}
			t.Errorf("%s: %s writes a Session-origin event outside a gated closure", name, fset.Position(node.Pos()))
			return true
		})
	}

	// The JSON mode's entry_appended writer, and its event subscription's place after session_start.
	file, err := parser.ParseFile(fset, "print_mode.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var emit, subscribe []token.Pos
	gated := 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			if fun.Sel.Name == "EmitSessionStartTransition" {
				emit = append(emit, call.Pos())
			}
		case *ast.Ident:
			if fun.Name == "subscribe" {
				subscribe = append(subscribe, call.Pos())
			}
		}
		return true
	})
	ast.Inspect(file, func(node ast.Node) bool {
		fn, ok := node.(*ast.FuncLit)
		if !ok {
			return true
		}
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			if call, ok := inner.(*ast.CallExpr); ok {
				if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "writeJSONLine" && len(call.Args) == 2 {
					if arg, ok := call.Args[1].(*ast.Ident); ok && arg.Name == "event" {
						if !callsLoadOnWriting(fn.Body) {
							t.Errorf("print_mode.go: %s writes a Session-origin event without testing writing.Load()", fset.Position(call.Pos()))
						}
						gated++
					}
				}
			}
			return true
		})
		return true
	})
	if gated == 0 {
		t.Error("print_mode.go: the JSON entry_appended writer (writeJSONLine(jsonOut, event)) was not found; update this test with the sink that replaced it")
	}
	if len(emit) != 1 || len(subscribe) != 1 {
		t.Fatalf("print_mode.go: %d session_start emits and %d subscribe calls in bind, want one each", len(emit), len(subscribe))
	}
	if !slices.IsSorted([]token.Pos{emit[0], subscribe[0]}) {
		t.Errorf("print_mode.go: subscribe(session) at %s comes before the session_start emit at %s; Pi subscribes the JSON writer after bindExtensions", fset.Position(subscribe[0]), fset.Position(emit[0]))
	}
}

// callsLoadOnWriting reports whether body calls Load on a value named writing (st.writing.Load() or writing.Load()).
func callsLoadOnWriting(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Load" {
			return true
		}
		switch recv := selector.X.(type) {
		case *ast.Ident:
			found = found || recv.Name == "writing"
		case *ast.SelectorExpr:
			found = found || recv.Sel.Name == "writing"
		}
		return true
	})
	return found
}
