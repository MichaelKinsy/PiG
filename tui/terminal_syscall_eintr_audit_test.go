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
//   - x/sys/unix's Poll, Select, Read and Ioctl* calls, and calls through those seams, must sit in a loop that goes round again on EINTR; a
//     function that only names EINTR, or returns on it, does not count.
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
		var retryLoops []*ast.ForStmt
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				if loop, ok := node.(*ast.ForStmt); ok && retriesEINTR(loop) {
					retryLoops = append(retryLoops, loop)
				}
				return true
			})
		}
		ast.Inspect(decl, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok {
				if fn, ok := call.Fun.(*ast.Ident); ok && (fn.Name == "makeRawTerminal" || fn.Name == "restoreTerminal") && !insideAny(retryLoops, call.Pos()) {
					report(call.Pos(), fn.Name+" outside a loop that retries on EINTR")
				}
			}
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
				if sleeps && !insideAny(retryLoops, selector.Pos()) {
					report(selector.Pos(), "unix."+name+" outside a loop that retries on EINTR")
				}
			}
			return true
		})
	}
	return findings
}

// The audit must demand the retry, not the mention: a function that names isEINTR and then returns on it, or a loop that returns on EINTR,
// still hands EINTR to its caller.
func TestEINTRAuditRequiresTheRetryNotTheMention(t *testing.T) {
	const header = "package p\nimport \"golang.org/x/sys/unix\"\nfunc isEINTR(error) bool { return false }\nvar _ = unix.EINTR\nvar makeRawTerminal = func(int) error { return nil }\n"
	for _, tc := range []struct {
		name, body string
		flagged    bool
	}{
		{"retry loop that continues", "for {\n n, err := unix.Poll(nil, 0)\n if isEINTR(err) { continue }\n return n, err\n}", false},
		{"retry loop that leaves only on a different error", "for {\n n, err := unix.Poll(nil, 0)\n if !isEINTR(err) { return n, err }\n}", false},
		{"mutant: returns on EINTR", "n, err := unix.Poll(nil, 0)\n if isEINTR(err) { return 0, err }\n return n, err", true},
		{"mutant: names EINTR and loops but returns on it", "for {\n n, err := unix.Poll(nil, 0)\n if isEINTR(err) { return 0, err }\n if err == nil { return n, nil }\n}", true},
		{"mutant: no check at all", "return unix.Poll(nil, 0)", true},
		{"seam call in a retry loop", "for {\n err := makeRawTerminal(0)\n if !isEINTR(err) { return 0, err }\n}", false},
		{"mutant: seam call that returns on EINTR", "err := makeRawTerminal(0)\n if isEINTR(err) { return 0, err }\n return 0, err", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "audited.go", header+"func f() (int, error) {\n"+tc.body+"\n}\n", 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := auditEINTR(fset, "audited.go", file); (len(got) > 0) != tc.flagged {
				t.Fatalf("audit findings %v, flagged want %t", got, tc.flagged)
			}
		})
	}
}

// retriesEINTR reports whether loop goes round again on EINTR: it holds an if on isEINTR or EINTR that continues (the interrupted case), or an
// if on the negation that leaves the loop (the interrupted case falls through to the next iteration). An if that returns on EINTR is no retry.
func retriesEINTR(loop *ast.ForStmt) bool {
	retries := false
	ast.Inspect(loop.Body, func(node ast.Node) bool {
		stmt, ok := node.(*ast.IfStmt)
		if !ok || !namesEINTR(stmt.Cond) {
			return true
		}
		if not, ok := stmt.Cond.(*ast.UnaryExpr); ok && not.Op == token.NOT {
			retries = true
			return false
		}
		ast.Inspect(stmt.Body, func(inner ast.Node) bool {
			if branch, ok := inner.(*ast.BranchStmt); ok && branch.Tok == token.CONTINUE {
				retries = true
			}
			return true
		})
		return true
	})
	return retries
}

func namesEINTR(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok && (ident.Name == "EINTR" || ident.Name == "isEINTR") {
			found = true
		}
		return true
	})
	return found
}

func insideAny(loops []*ast.ForStmt, pos token.Pos) bool {
	for _, loop := range loops {
		if loop.Pos() <= pos && pos < loop.End() {
			return true
		}
	}
	return false
}
