package surfaces

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// A host ticks its heartbeat from a timer for as long as it has a link, whether or not a test still looks at it: one a test leaves
// open keeps reading the clock inside the tests that run after it (and a test that sets time.Local races with that). So every
// function of this package's tests that makes a host (host.CreateHost, StartClient) must also arrange its close: a t.Cleanup or
// a Close(ctx)/Stop(ctx) in the function itself (a setup that returns its own teardown has the Close in its closure).
func TestEveryTestHostIsClosedByTheFunctionThatMadeIt(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("test files: %v, %v", files, err)
	}
	fset := token.NewFileSet()
	for _, file := range files {
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			var makes, closes bool
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CallExpr:
					switch f := n.Fun.(type) {
					case *ast.SelectorExpr:
						switch {
						case f.Sel.Name == "CreateHost" && isIdent(f.X, "host"):
							makes = true
						case f.Sel.Name == "Cleanup" && isIdent(f.X, "t"):
							closes = true
						case (f.Sel.Name == "Close" || f.Sel.Name == "Stop") && len(n.Args) == 1:
							closes = true // host.Close(ctx), client.Stop(ctx); a timer's Stop() takes none
						}
					case *ast.Ident:
						if f.Name == "StartClient" {
							makes = true
						}
					}
				}
				return true
			})
			if makes && !closes {
				t.Errorf("%s: %s makes a host and never closes it (t.Cleanup, defer, or Close(ctx)/Stop(ctx) in the function)",
					strings.TrimSuffix(fset.Position(fn.Pos()).String(), ":1"), fn.Name.Name)
			}
		}
	}
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}
