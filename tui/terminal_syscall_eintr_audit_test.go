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
		var stack []ast.Node
		ast.Inspect(decl, func(node ast.Node) bool {
			if node == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, node)
			if call, ok := node.(*ast.CallExpr); ok {
				if fn, ok := call.Fun.(*ast.Ident); ok && (fn.Name == "makeRawTerminal" || fn.Name == "restoreTerminal") && !retriedAtCallSite(stack) {
					report(call.Pos(), fn.Name+" outside a loop that retries on EINTR for this call's error")
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
				if sleeps && !retriedAtCallSite(stack) {
					report(selector.Pos(), "unix."+name+" outside a loop that retries on EINTR for this call's error")
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
		{"mutant: a second unguarded call beside a guarded one in the function", "for {\n n, err := unix.Poll(nil, 0)\n if isEINTR(err) { continue }\n if err != nil { return n, err }\n break\n}\n return unix.Poll(nil, 0)", true},
		{"mutant: a second unguarded call inside the same retry loop", "for {\n n, err := unix.Poll(nil, 0)\n if isEINTR(err) { continue }\n m, err2 := unix.Poll(nil, 0)\n return n + m, err2\n}", true},
		{"two guarded calls", "for {\n n, err := unix.Poll(nil, 0)\n if isEINTR(err) { continue }\n return n, err\n}\n for {\n m, err := unix.Poll(nil, 0)\n if !isEINTR(err) { return m, err }\n}", false},
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

// retriedAtCallSite reports whether the call at the top of stack is retried on EINTR: the innermost loop around it goes round again on an
// EINTR test of the error this very call assigned. A loop that retries on some other call's error, or a call whose error is never tested in
// the loop (a second unguarded call beside a guarded one), is not a retry.
func retriedAtCallSite(stack []ast.Node) bool {
	loopIndex := -1
	for i := len(stack) - 1; i >= 0; i-- {
		if _, ok := stack[i].(*ast.ForStmt); ok {
			loopIndex = i
			break
		}
	}
	if loopIndex < 0 {
		return false
	}
	var assigned map[string]bool
	for i := len(stack) - 1; i > loopIndex; i-- {
		if assign, ok := stack[i].(*ast.AssignStmt); ok {
			assigned = map[string]bool{}
			for _, lhs := range assign.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok && ident.Name != "_" {
					assigned[ident.Name] = true
				}
			}
			break
		}
	}
	if len(assigned) == 0 {
		return false
	}
	for name := range retriedErrors(stack[loopIndex].(*ast.ForStmt)) {
		if assigned[name] {
			return true
		}
	}
	return false
}

// retriedErrors returns the identifiers tested for EINTR by an if in loop that makes it go round again: an if on isEINTR or EINTR that
// continues (the interrupted case), or an if on the negation that leaves the loop (the interrupted case falls through to the next iteration).
// An if that returns on EINTR is no retry.
func retriedErrors(loop *ast.ForStmt) map[string]bool {
	names := map[string]bool{}
	ast.Inspect(loop.Body, func(node ast.Node) bool {
		stmt, ok := node.(*ast.IfStmt)
		if !ok || !namesEINTR(stmt.Cond) {
			return true
		}
		retries := false
		if not, ok := stmt.Cond.(*ast.UnaryExpr); ok && not.Op == token.NOT {
			retries = true
		} else {
			ast.Inspect(stmt.Body, func(inner ast.Node) bool {
				if branch, ok := inner.(*ast.BranchStmt); ok && branch.Tok == token.CONTINUE {
					retries = true
				}
				return true
			})
		}
		if retries {
			ast.Inspect(stmt.Cond, func(inner ast.Node) bool {
				if ident, ok := inner.(*ast.Ident); ok {
					names[ident.Name] = true
				}
				return true
			})
		}
		return true
	})
	return names
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
