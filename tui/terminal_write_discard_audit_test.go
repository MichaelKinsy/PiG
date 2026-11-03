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

// discardAllowed names the functions whose terminal write may drop its error, as "file.go:function", with the reason. Each entry must say why.
var discardAllowed = map[string]string{
	"interactive.go:printResumeHint":         "Pi's stop() unregisters the stdout error listener before it prints the resume hint (interactive-mode.ts :7209, :4422), so that write never reaches the exit-129 handler",
	"migrations.go:migrateCommandsToPrompts": "a startup migration notice (console.log in Pi's migrations.ts) that runs before the interactive mode installs its error handler",
}

func discardedTerminalWrites(fset *token.FileSet, path string, file *ast.File) []string {
	var findings []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && discardAllowed[filepath.Base(path)+":"+fn.Name.Name] != "" {
			continue
		}
		findings = append(findings, discardedIn(fset, path, decl)...)
	}
	return findings
}

func discardedIn(fset *token.FileSet, path string, file ast.Node) []string {
	var findings []string
	report := func(pos token.Pos) {
		findings = append(findings, fset.Position(pos).String()+": "+path)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch stmt := node.(type) {
		case *ast.ExprStmt:
			if call, ok := stmt.X.(*ast.CallExpr); ok && writesToTerminal(call) {
				report(stmt.Pos())
			}
		case *ast.AssignStmt:
			if len(stmt.Rhs) != 1 {
				return true
			}
			for _, lhs := range stmt.Lhs {
				if ident, ok := lhs.(*ast.Ident); !ok || ident.Name != "_" {
					return true
				}
			}
			if call, ok := stmt.Rhs[0].(*ast.CallExpr); ok && writesToTerminal(call) {
				report(stmt.Pos())
			}
		}
		return true
	})
	return findings
}

// writesToTerminal reports whether call writes to the user's terminal: Write or WriteString on os.Stdout, a tuiBase's t.out or a stdout field;
// fmt.Fprint*, io.WriteString and the like with such a first argument; or fmt.Print*, which writes to os.Stdout.
func writesToTerminal(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "fmt" && strings.HasPrefix(sel.Sel.Name, "Print") {
		return true
	}
	if sel.Sel.Name == "Write" || sel.Sel.Name == "WriteString" {
		if isTerminalTarget(sel.X) {
			return true
		}
	}
	return len(call.Args) > 0 && isTerminalTarget(call.Args[0]) && (strings.HasPrefix(sel.Sel.Name, "Fprint") || sel.Sel.Name == "WriteString")
}

// isTerminalTarget is os.Stdout, a tuiBase's t.out, a stdout field or t.cursorOut().
func isTerminalTarget(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			return (x.Name == "os" && e.Sel.Name == "Stdout") || (x.Name == "t" && e.Sel.Name == "out") || e.Sel.Name == "stdout"
		}
	case *ast.CallExpr:
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
			return sel.Sel.Name == "cursorOut"
		}
	}
	return false
}
