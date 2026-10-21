package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// Pi installs a process 'exit' handler while a TUI runs (packages/tui/src/tui.ts exitRestoreHandler, 9df819de, #11170), so
// every process.exit() restores the terminal modes. Go has no exit hook: each function that calls os.Exit must call
// tui.RestoreTerminalOnExit first, unless it can only run where no TUI owns the terminal. A new os.Exit path fails here until
// it restores or is listed with its reason.
func TestEveryExitPathReachableWhileATUIRunsRestoresTheTerminal(t *testing.T) {
	// exempt: file:function -> why no TUI owns the terminal there.
	exempt := map[string]string{
		"coding/cli/guard.go:guardBinaryIdentity":        "runs before any mode starts",
		"coding/cli/rpc_mode.go:serveRPCMode":            "RPC mode has no TUI: stdin and stdout are the JSONL protocol",
		"coding/cli/rpc_shutdown_unix.go:dieBySignal":    "RPC shutdown; the process dies by the signal",
		"coding/cli/rpc_shutdown_windows.go:dieBySignal": "RPC shutdown; the process exits with the signal's status",
	}
	fset := token.NewFileSet()
	var seen int
	for _, pattern := range []string{"*.go", filepath.Join("..", "..", "internal", "codingagent", "*.go")} {
		files, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			parsed, err := parser.ParseFile(fset, file, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			rel := filepath.ToSlash(strings.TrimPrefix(filepath.ToSlash(file), "../../"))
			if !strings.Contains(rel, "/") {
				rel = "coding/cli/" + rel
			}
			for _, decl := range parsed.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				exits, restores := false, false
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					pkg, ok := sel.X.(*ast.Ident)
					if !ok {
						return true
					}
					switch {
					case pkg.Name == "os" && sel.Sel.Name == "Exit", pkg.Name == "log" && strings.HasPrefix(sel.Sel.Name, "Fatal"):
						exits = true
					case pkg.Name == "tui" && sel.Sel.Name == "RestoreTerminalOnExit":
						restores = true
					}
					return true
				})
				if !exits {
					continue
				}
				seen++
				key := rel + ":" + fn.Name.Name
				if _, ok := exempt[key]; ok {
					continue
				}
				if !restores {
					t.Errorf("%s calls os.Exit without tui.RestoreTerminalOnExit: a TUI that exits this way leaves mouse tracking, bracketed paste, the Kitty keyboard protocol and the alternate screen enabled (#11170)", key)
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("found no os.Exit call: the scan is broken")
	}
}

// Pi's 'exit' event also fires when the event loop drains and on an uncaught exception, so Main defers the handler: it runs on a
// return and on a panic unwinding through Main, and last, so it must be the first deferred call.
func TestMainDefersTheTerminalExitRestoreFirst(t *testing.T) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Main" || fn.Body == nil {
			continue
		}
		for _, statement := range fn.Body.List {
			deferred, ok := statement.(*ast.DeferStmt)
			if !ok {
				continue
			}
			if sel, ok := deferred.Call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "RestoreTerminalOnExit" {
				return
			}
			t.Fatalf("Main's first deferred call is not tui.RestoreTerminalOnExit (at %s): it would run before that cleanup, not after it", fset.Position(deferred.Pos()))
		}
	}
	t.Fatal("Main defers no tui.RestoreTerminalOnExit")
}
