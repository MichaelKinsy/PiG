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

// A stopped and continued process (Ctrl+Z, bg, fg) resumes inside the syscall that stopped it, and where the kernel reports EINTR to that call
// (macOS) the Go wrapper returns it: a termios ioctl that is not retried ended the session with "interrupted system call" (#397). Node's libuv
// retries every such call, so Pi never sees it. This audit keeps the whole class out of the terminal code: the calls that can sleep in the
// kernel, which are the ones job control and signals interrupt, go through the retrying helpers or retry in place.
//
//   - x/term's MakeRaw, Restore and SetState issue the termios setting ioctls (TCSETS, TIOCSETA), which wait for output to drain and stop a
//     background group with SIGTTOU. Only the seam variables in terminal.go may name them; everything else calls makeRaw and restoreCooked.
//   - x/sys/unix's Poll, Select, Read and Ioctl* calls must sit in a function that handles EINTR.
//
// GetSize, GetState and IsTerminal are queries that never sleep and so never see EINTR; they are not audited.
func TestTerminalSyscallsThatCanSleepRetryEINTR(t *testing.T) {
	dirs := []string{".", "../internal/codingagent", "../coding/cli"}
	fset := token.NewFileSet()
	var findings []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			findings = append(findings, auditEINTR(fset, path, file)...)
		}
	}
	if len(findings) > 0 {
		t.Fatalf("terminal syscalls that can be interrupted by a stop and continue without a retry:\n%s", strings.Join(findings, "\n"))
	}
}

func auditEINTR(fset *token.FileSet, path string, file *ast.File) []string {
	aliases := map[string]string{} // local import name -> import path
	for _, spec := range file.Imports {
		importPath := strings.Trim(spec.Path.Value, `"`)
		name := filepath.Base(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		aliases[name] = importPath
	}
	var findings []string
	report := func(pos token.Pos, what string) {
		findings = append(findings, fset.Position(pos).String()+": "+what)
	}
	// seamVars are the declarations in terminal.go that hold the raw ioctls for tests to interrupt.
	seamVar := func(decl ast.Decl) bool {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR || filepath.Base(path) != "terminal.go" {
			return false
		}
		for _, spec := range gen.Specs {
			for _, name := range spec.(*ast.ValueSpec).Names {
				if name.Name != "makeRawTerminal" && name.Name != "restoreTerminal" {
					return false
				}
			}
		}
		return true
	}
	for _, decl := range file.Decls {
		allowedSeam := seamVar(decl)
		handlesEINTR := false
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				if ident, ok := node.(*ast.Ident); ok && (ident.Name == "EINTR" || ident.Name == "isEINTR") {
					handlesEINTR = true
				}
				return true
			})
		}
		ast.Inspect(decl, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}
			switch aliases[pkg.Name] {
			case "golang.org/x/term":
				switch selector.Sel.Name {
				case "MakeRaw", "Restore", "SetState":
					if !allowedSeam {
						report(selector.Pos(), "term."+selector.Sel.Name+" without the EINTR retry (use makeRaw and restoreCooked)")
					}
				}
			case "golang.org/x/sys/unix":
				name := selector.Sel.Name
				sleeps := name == "Poll" || name == "Select" || name == "Read" || (strings.HasPrefix(name, "Ioctl") && name != "IoctlFileClone")
				if sleeps && !handlesEINTR {
					report(selector.Pos(), "unix."+name+" in a function that never handles EINTR")
				}
			}
			return true
		})
	}
	return findings
}
