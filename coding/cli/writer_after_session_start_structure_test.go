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

// Pi's JSON-mode stdout writer subscribes after `await session.bindExtensions()` (print-mode.ts:76-108), so the events a session_start handler emits are never
// written. The JSON mode's entry_appended writer (print_mode.go) must test the "writing" flag its bind sets after session_start, and the JSON mode must
// subscribe its event writer after the session_start emit. A sink that writes a Session-origin event without the flag, or a JSON subscription moved before
// session_start, fails here. RPC mode is the reverse since Pi beffcde92dea: TestRPCWritersSubscribeBeforeSessionStart.
func TestSessionOriginWritersObeyTheWriterSubscribedGate(t *testing.T) {
	fset := token.NewFileSet()

	// The JSON mode's entry_appended writer, and its event subscription's place after session_start.
	file, err := parser.ParseFile(fset, "print_mode.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var emit, subscribe []token.Pos

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
	// Pi 6aab551a79ba: the JSON mode has no writer-subscribed flag, and its event writer subscribes before the session_start emit (print-mode.ts rebindSession).
	ast.Inspect(file, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok && ident.Name == "writing" {
			t.Errorf("print_mode.go: %s names a writer-subscribed flag; the JSON writer subscribes before session_start and nothing waits for it", fset.Position(ident.Pos()))
		}
		return true
	})
	if len(emit) != 1 || len(subscribe) != 1 {
		t.Fatalf("print_mode.go: %d session_start emits and %d subscribe calls in bind, want one each", len(emit), len(subscribe))
	}
	if !slices.IsSorted([]token.Pos{subscribe[0], emit[0]}) {
		t.Errorf("print_mode.go: subscribe(session) at %s comes after the session_start emit at %s; Pi subscribes the JSON writer before bindExtensions", fset.Position(subscribe[0]), fset.Position(emit[0]))
	}
}

// Pi's RPC rebindSession subscribes its event writer before `await session.bindExtensions()` (rpc-mode.ts, Pi beffcde92dea), so the events a session_start
// handler emits are written. In rpcHost.bind the Session's writer (forward) and name listener subscribe before the session_start emit (announce), and no RPC
// sink waits on a "writing" flag: the flag is how RPC mode held the events back before.
func TestRPCWritersSubscribeBeforeSessionStart(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("rpc_*.go")
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
		ast.Inspect(file, func(node ast.Node) bool {
			if ident, ok := node.(*ast.Ident); ok && ident.Name == "writing" {
				t.Errorf("%s: %s names a writing flag; RPC mode's writers are subscribed before session_start and write from the first event", name, fset.Position(ident.Pos()))
			}
			return true
		})
	}

	file, err := parser.ParseFile(fset, "rpc_mode_runtime.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var bind *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "bind" && fn.Recv != nil {
			bind = fn
		}
	}
	if bind == nil {
		t.Fatal("rpc_mode_runtime.go: rpcHost.bind not found")
	}
	// The reload path announces before the Session's writer exists to reuse it; the last announce is the first bind's.
	var forward, lastAnnounce token.Pos
	var subscribes []token.Pos
	ast.Inspect(bind.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
			switch selector.Sel.Name {
			case "forward":
				forward = call.Pos()
			case "announce":
				lastAnnounce = call.Pos()
			case "Subscribe":
				subscribes = append(subscribes, call.Pos())
			}
		}
		return true
	})
	if forward == token.NoPos || lastAnnounce == token.NoPos || len(subscribes) == 0 {
		t.Fatalf("rpc_mode_runtime.go: bind has forward at %v, announce at %v and %d Subscribe calls; update this test with what replaced them", forward, lastAnnounce, len(subscribes))
	}
	if forward > lastAnnounce {
		t.Errorf("rpc_mode_runtime.go: the event writer (forward) at %s comes after the session_start emit (announce) at %s; Pi subscribes before bindExtensions", fset.Position(forward), fset.Position(lastAnnounce))
	}
	for _, position := range subscribes {
		if position > lastAnnounce {
			t.Errorf("rpc_mode_runtime.go: the Session subscription at %s comes after the session_start emit at %s", fset.Position(position), fset.Position(lastAnnounce))
		}
	}
}
