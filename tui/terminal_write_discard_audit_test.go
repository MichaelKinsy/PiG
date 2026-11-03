package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pi sends every write to the user's terminal through process.stdout, whose "error" listener turns a dead terminal into exit 129 (#397). A
// `_, _ = fmt.Fprint(os.Stdout, ...)` or `_, _ = io.WriteString(t.out, ...)` drops that error, so every such write in the interactive code
// goes through WriteToTerminal. Writes to stderr, to a caller's io.Writer and to files are not terminal writes and are not audited.
func TestTerminalWritesAreNotDiscarded(t *testing.T) {
	var findings []string
	for _, dir := range []string{".", "../internal/codingagent"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			path := filepath.Join(dir, name)
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			findings = append(findings, discardedTerminalWrites(fset, path, file)...)
		}
	}
	if len(findings) > 0 {
		t.Fatalf("terminal writes that discard their error (use WriteToTerminal):\n%s", strings.Join(findings, "\n"))
	}
}

func discardedTerminalWrites(fset *token.FileSet, path string, file *ast.File) []string {
	var findings []string
	ast.Inspect(file, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 || len(assign.Lhs) == 0 {
			return true
		}
		for _, lhs := range assign.Lhs {
			if ident, ok := lhs.(*ast.Ident); !ok || ident.Name != "_" {
				return true
			}
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "WriteString" && isTerminalTarget(sel.X) {
			findings = append(findings, fset.Position(assign.Pos()).String()+": "+path)
			return true
		}
		if len(call.Args) > 0 && isTerminalTarget(call.Args[0]) {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && (strings.HasPrefix(sel.Sel.Name, "Fprint") || sel.Sel.Name == "WriteString") {
				findings = append(findings, fset.Position(assign.Pos()).String()+": "+path)
			}
		}
		return true
	})
	return findings
}

// isTerminalTarget is os.Stdout, a tuiBase's t.out, or t.cursorOut().
func isTerminalTarget(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			return (x.Name == "os" && e.Sel.Name == "Stdout") || (x.Name == "t" && e.Sel.Name == "out")
		}
	case *ast.CallExpr:
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
			return sel.Sel.Name == "cursorOut"
		}
	}
	return false
}
