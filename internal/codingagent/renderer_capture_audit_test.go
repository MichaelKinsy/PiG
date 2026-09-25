package codingagent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestNoRetainedRendererBoundMethodValues is the AST inventory that replaces the
// grep-only stable-reference audit. A renderer swap (switchTuiMode) only works if
// nothing retains a bound method value of the renderer (e.g.
// newSpecialLinesComponent(m.tuiInst.Render) or newCustomOverlay(m.tuiInst.RequestRender)):
// such a value freezes the renderer captured at construction and, after a swap,
// invalidates the stopped renderer off the owner loop. All invalidations must go
// through the race-safe indirections (m.renderNow/requestRender/invalidate,
// TUIUIContext.withRenderer). This test fails if a new `<recv>.tuiInst.<Render|
// RequestRender|Invalidate>` bound method value (not an immediate call) appears,
// which is exactly the class of miss the manual sweep made with newCustomOverlay.
func TestNoRetainedRendererBoundMethodValues(t *testing.T) {
	fset := token.NewFileSet()
	dir := "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	rendererMethods := []string{"Render", "RequestRender", "Invalidate"}
	var findings []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		// Collect selector nodes that are the Fun of a CallExpr (immediate calls),
		// so we can exclude them.
		called := map[*ast.SelectorExpr]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					called[sel] = true
				}
			}
			return true
		})
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || !slices.Contains(rendererMethods, sel.Sel.Name) {
				return true
			}
			// The receiver must be `<x>.tuiInst`.
			inner, ok := sel.X.(*ast.SelectorExpr)
			if !ok || inner.Sel.Name != "tuiInst" {
				return true
			}
			if called[sel] {
				return true // immediate call, e.g. m.tuiInst.Render(): not retained
			}
			findings = append(findings, e.Name()+": "+exprString(sel))
			return true
		})
	}
	if len(findings) != 0 {
		t.Fatalf("retained renderer bound method values (route through renderNow/requestRender/invalidate/withRenderer instead):\n%s", strings.Join(findings, "\n"))
	}
}

func exprString(sel *ast.SelectorExpr) string {
	if inner, ok := sel.X.(*ast.SelectorExpr); ok {
		if x, ok := inner.X.(*ast.Ident); ok {
			return x.Name + "." + inner.Sel.Name + "." + sel.Sel.Name
		}
	}
	return sel.Sel.Name
}
