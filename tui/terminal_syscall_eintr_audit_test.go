package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A stopped and continued process (Ctrl+Z, bg, fg) resumes inside the syscall that stopped it, and where the kernel reports EINTR to that call
// (macOS) the Go wrapper returns it: a termios ioctl that is not retried ended the session with "interrupted system call" (#397). Node's libuv
// retries every such call, so Pi never sees it. This audit keeps the whole class out of the terminal code: the calls that can sleep in the
// kernel, which are the ones job control and signals interrupt, go through the retrying helpers or retry in place.
//
//   - x/term's MakeRaw, Restore and SetState issue the termios setting ioctls (TCSETS, TIOCSETA), which wait for output to drain and stop a
//     background group with SIGTTOU. Only the seam variables in terminal.go may name them; everything else calls MakeRaw and RestoreCooked.
//   - x/sys/unix's Poll, Select, Read and Ioctl* calls, and calls through those seams, must sit in a loop that goes round again on EINTR; a
//     function that only names EINTR, or returns on it, does not count.
//
// GetSize, GetState and IsTerminal are queries that never sleep and so never see EINTR; they are not audited.
func TestTerminalSyscallsThatCanSleepRetryEINTR(t *testing.T) {
	findings, audited, err := auditEINTRTree("..", eintrAuditExcluded)
	if err != nil {
		t.Fatal(err)
	}
	for rel, reason := range eintrAuditExcluded {
		if reason == "" {
			t.Errorf("%s is excluded from the EINTR audit without a stated reason", rel)
		}
	}
	if audited == 0 {
		t.Fatal("the audit found no file that touches the user's terminal; its file selection is broken")
	}
	if len(findings) > 0 {
		t.Fatalf("terminal syscalls that can be interrupted by a stop and continue without a retry:\n%s", strings.Join(findings, "\n"))
	}
}

// auditEINTRTree audits every non-test Go file under root that touches the user's terminal, except the files in excluded (slash paths
// relative to root). It returns the findings and how many files it audited.
func auditEINTRTree(root string, excluded map[string]string) (findings []string, audited int, err error) {
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", ".upstream", "vendor", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, skip := excluded[filepath.ToSlash(rel)]; skip {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		if !touchesUserTerminal(file) {
			return nil
		}
		audited++
		findings = append(findings, auditEINTR(fset, path, file)...)
		return nil
	})
	return findings, audited, err
}

// A syscall outside the directories the audit first covered must fail it too: a planted file under an unrelated path, and the same file with the
// retry (clean), and a file that never names a stream (not audited).
func TestEINTRAuditCoversTheWholeRepository(t *testing.T) {
	const unguarded = "package p\nimport (\n\t\"os\"\n\t\"golang.org/x/sys/unix\"\n)\nfunc f() (int, error) {\n\tfds := []unix.PollFd{{Fd: int32(os.Stdin.Fd()), Events: unix.POLLIN}}\n\treturn unix.Poll(fds, -1)\n}\n"
	const guarded = "package p\nimport (\n\t\"os\"\n\t\"golang.org/x/sys/unix\"\n)\nfunc isEINTR(error) bool { return false }\nfunc f() (int, error) {\n\tfds := []unix.PollFd{{Fd: int32(os.Stdin.Fd()), Events: unix.POLLIN}}\n\tfor {\n\t\tn, err := unix.Poll(fds, -1)\n\t\tif isEINTR(err) {\n\t\t\tcontinue\n\t\t}\n\t\treturn n, err\n\t}\n}\n"
	const unrelated = "package p\nimport \"golang.org/x/sys/unix\"\nfunc f() (int, error) { return unix.Poll(nil, 0) }\n"
	for _, tc := range []struct {
		name, path, source string
		excluded           map[string]string
		audited, findings  int
	}{
		{"mutant: unguarded Poll on stdin far from the old directories", "extensions/host/deep/poll.go", unguarded, nil, 1, 1},
		{"the same call with a retry", "extensions/host/deep/poll.go", guarded, nil, 1, 0},
		{"a file that names no stream or descriptor", "extensions/host/deep/poll.go", unrelated, nil, 0, 0},
		{"mutant excluded by a stated path", "env/daemon/pty_unix.go", unguarded, map[string]string{"env/daemon/pty_unix.go": "a pty PiG owns"}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			full := filepath.Join(root, filepath.FromSlash(tc.path))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			findings, audited, err := auditEINTRTree(root, tc.excluded)
			if err != nil {
				t.Fatal(err)
			}
			if audited != tc.audited || len(findings) != tc.findings {
				t.Fatalf("audited %d files with findings %v, want %d files and %d findings", audited, findings, tc.audited, tc.findings)
			}
		})
	}
}

// eintrAuditExcluded names the files that import x/term or x/sys/unix and name a standard stream or a tty descriptor, and still are not
// the user's terminal, with the reason. Each entry must say why; an empty reason fails the test.
var eintrAuditExcluded = map[string]string{
	"bench/sessionscale/cmd/sessionscale/run.go": "a benchmark driver's ioctls (TIOCSPTLCK, TIOCGPTN, TIOCSWINSZ) on a pty master it opened for the session it measures; not the user's terminal and never stopped by job control",
	"env/daemon/pty_unix.go":                     "ioctls and reads on a pty master the environment daemon allocated for its own child; a stop and continue of the user's terminal never interrupts them",
	"env/daemon/pty_linux.go":                    "ioctls (TIOCGPTN, TIOCSPTLCK) on a pty master the environment daemon allocated for its own child, not the user's terminal",
}

// touchesUserTerminal reports whether file imports x/term or x/sys/unix and names a standard stream, a tty path or an Fd call: the files whose
// syscalls can reach the user's terminal. A file that imports neither cannot issue the audited calls, and one that never names a stream or a
// descriptor is working on something else (sockets, pipes it opened itself).
func touchesUserTerminal(file *ast.File) bool {
	imports := false
	for _, spec := range file.Imports {
		switch strings.Trim(spec.Path.Value, `"`) {
		case "golang.org/x/term", "golang.org/x/sys/unix":
			imports = true
		}
	}
	if !imports {
		return false
	}
	touches := false
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.SelectorExpr:
			if x, ok := n.X.(*ast.Ident); ok && x.Name == "os" && (n.Sel.Name == "Stdin" || n.Sel.Name == "Stdout" || n.Sel.Name == "Stderr") {
				touches = true
			}
			if n.Sel.Name == "Fd" {
				touches = true
			}
		case *ast.BasicLit:
			if n.Kind == token.STRING && (strings.Contains(n.Value, "/dev/tty") || strings.Contains(n.Value, "/dev/ptmx")) {
				touches = true
			}
		}
		return !touches
	})
	return touches
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
	// sleepingCall is a call the audit demands a retry for: a seam call or a unix call that can sleep.
	sleepingCall := func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CallExpr:
			fn, ok := n.Fun.(*ast.Ident)
			return ok && (fn.Name == "makeRawTerminal" || fn.Name == "restoreTerminal")
		case *ast.SelectorExpr:
			pkg, ok := n.X.(*ast.Ident)
			if !ok || aliases[pkg.Name] != "golang.org/x/sys/unix" {
				return false
			}
			name := n.Sel.Name
			return name == "Poll" || name == "Select" || name == "Read" || (strings.HasPrefix(name, "Ioctl") && name != "IoctlFileClone")
		}
		return false
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
				if fn, ok := call.Fun.(*ast.Ident); ok && (fn.Name == "makeRawTerminal" || fn.Name == "restoreTerminal") && !retriedAtCallSite(stack, sleepingCall) {
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
						report(selector.Pos(), "term."+selector.Sel.Name+" without the EINTR retry (use tui.MakeRaw and tui.RestoreCooked)")
					}
				}
			case "golang.org/x/sys/unix":
				name := selector.Sel.Name
				sleeps := name == "Poll" || name == "Select" || name == "Read" || (strings.HasPrefix(name, "Ioctl") && name != "IoctlFileClone")
				if sleeps && !retriedAtCallSite(stack, sleepingCall) {
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
		{"mutant: a second unguarded call that reuses err in the same retry loop", "for {\n n, err := unix.Poll(nil, 0)\n if isEINTR(err) { continue }\n _, err = unix.Read(0, nil)\n return n, err\n}", true},
		{"mutant: an unguarded call in a closure inside a retry loop", "for {\n n, err := unix.Poll(nil, 0)\n if isEINTR(err) { continue }\n read := func() error { _, err := unix.Read(0, nil); return err }\n return n, read()\n}", true},
		{"mutant: a negated EINTR test whose body does not leave the loop", "for {\n n, err := unix.Poll(nil, 0)\n if !isEINTR(err) { println(err) }\n _ = n\n}", true},
		{"two calls each guarded in the same loop", "for {\n n, err := unix.Poll(nil, 0)\n if isEINTR(err) { continue }\n if err != nil { return n, err }\n _, err = unix.Read(0, nil)\n if isEINTR(err) { continue }\n return n, err\n}", false},
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

// retriedAtCallSite reports whether the call at the top of stack is retried on EINTR: the innermost loop around it, inside the same function
// body, goes round again on an EINTR test of the error this very call assigned, and that test is the first one after the call with no other
// sleeping call between the call and the test. A loop that retries on some other call's error, a call whose error is never tested before the next
// call in the loop (a second unguarded call that reuses err), or a call in a closure that the loop only creates, is not a retry. sleeps reports
// whether a node is a call to audit.
func retriedAtCallSite(stack []ast.Node, sleeps func(ast.Node) bool) bool {
	loopIndex := -1
	for i, node := range slices.Backward(stack) {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		if _, ok := node.(*ast.ForStmt); ok {
			loopIndex = i
			break
		}
	}
	if loopIndex < 0 {
		return false
	}
	loop := stack[loopIndex].(*ast.ForStmt)
	var assign *ast.AssignStmt
	for i := len(stack) - 1; i > loopIndex; i-- {
		if found, ok := stack[i].(*ast.AssignStmt); ok {
			assign = found
			break
		}
	}
	if assign == nil {
		return false
	}
	assigned := map[string]bool{}
	for _, lhs := range assign.Lhs {
		if ident, ok := lhs.(*ast.Ident); ok && ident.Name != "_" {
			assigned[ident.Name] = true
		}
	}
	if len(assigned) == 0 {
		return false
	}
	// The next sleeping call after this one ends the window in which a test counts as this call's.
	windowEnd := loop.End()
	ast.Inspect(loop.Body, func(node ast.Node) bool {
		if node != nil && node.Pos() >= assign.End() && node.Pos() < windowEnd && sleeps(node) {
			windowEnd = node.Pos()
		}
		return true
	})
	for _, guard := range retryGuards(loop) {
		if guard.pos < assign.End() || guard.pos >= windowEnd {
			continue
		}
		for name := range guard.names {
			if assigned[name] {
				return true
			}
		}
	}
	return false
}

type retryGuard struct {
	pos   token.Pos
	names map[string]bool
}

// retryGuards returns the ifs in loop that make it go round again on EINTR: an if on isEINTR or EINTR that continues (the interrupted case),
// or an if on the negation whose body leaves the loop (the interrupted case falls through to the next iteration). Each carries the identifiers
// its condition names. An if that returns on EINTR is no retry, and neither is a negated test whose body stays in the loop.
func retryGuards(loop *ast.ForStmt) []retryGuard {
	var guards []retryGuard
	ast.Inspect(loop.Body, func(node ast.Node) bool {
		stmt, ok := node.(*ast.IfStmt)
		if !ok || !namesEINTR(stmt.Cond) {
			return true
		}
		retries := false
		if not, ok := stmt.Cond.(*ast.UnaryExpr); ok && not.Op == token.NOT {
			ast.Inspect(stmt.Body, func(inner ast.Node) bool {
				switch branch := inner.(type) {
				case *ast.ReturnStmt:
					retries = true
				case *ast.BranchStmt:
					if branch.Tok == token.BREAK || branch.Tok == token.GOTO {
						retries = true
					}
				}
				return true
			})
		} else {
			ast.Inspect(stmt.Body, func(inner ast.Node) bool {
				if branch, ok := inner.(*ast.BranchStmt); ok && branch.Tok == token.CONTINUE {
					retries = true
				}
				return true
			})
		}
		if retries {
			guard := retryGuard{pos: stmt.Cond.Pos(), names: map[string]bool{}}
			ast.Inspect(stmt.Cond, func(inner ast.Node) bool {
				if ident, ok := inner.(*ast.Ident); ok {
					guard.names[ident.Name] = true
				}
				return true
			})
			guards = append(guards, guard)
		}
		return true
	})
	return guards
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
