package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// A renderer's writes to its terminal writer hold outMu, so one goroutine at a time is in the writer (terminal_writer_ownership_test.go runs the rows).
// A write added later that takes no lock is caught here before a test has to meet a second goroutine: every function of this package that writes to
// the renderer's writer (WriteToTerminal, writeColorSchemeNotifications, io.WriteString or Write on out, t.out, t.direct, t.cursorOut()) takes outMu before
// its first write, or is named in writerLockHeldElsewhere with the reason its caller or its writer holds the lock.
func TestEveryRendererWriteHoldsTheWriterLock(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var findings []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		findings = append(findings, unlockedRendererWrites(fset, name, file, writerLockHeldElsewhere, seen)...)
	}
	for key := range writerLockHeldElsewhere {
		if !seen[key] {
			findings = append(findings, key+": listed in writerLockHeldElsewhere but writes nothing to a writer any more; delete the entry")
		}
	}
	sort.Strings(findings)
	if len(findings) > 0 {
		t.Fatalf("writes to the renderer's terminal writer without outMu (take it with writeOwned, or hold it before the write):\n%s", strings.Join(findings, "\n"))
	}
}

// writerLockHeldElsewhere names, as "file.go:function", the functions that write to a writer and take no outMu themselves, with the reason the lock is held.
var writerLockHeldElsewhere = map[string]string{
	"terminal.go:WriteToTerminal":                         "the primitive every write reaches; each caller is audited for the lock",
	"terminal.go:writeTerminalText":                       "the primitive under WriteToTerminal; each caller is audited for the lock",
	"terminal.go:writeString":                             "the primitive under writeTerminalText and utf8Writer; each caller is audited for the lock",
	"terminal.go:write":                                   "a ProcessTerminal's own write: Write, writeUnlogged and the keyboard-protocol writes hold the ProcessTerminal's outMu around it",
	"terminal_screen_writer.go:buildTerminalScreenWriter": "the screen writer gets the ProcessTerminal's outMu.Lock and Unlock as its Lock and Unlock and takes them around each write",
	"terminal_write_log.go:Write":                         "loggingWriter, the PI_TUI_WRITE_LOG wrapper of a terminal writer: the write it wraps holds the lock when it calls it",
	"tui.go:Write":                                        "utf8Writer, the writer of NewWithOutput: WriteToTerminal calls it under the renderer outMu",
	"tui.go:WriteString":                                  "utf8Writer, the writer of NewWithOutput: its callers (WriteToTerminal under the renderer's outMu) hold the lock",
	"tui.go:fullRender":                                   "a main-screen frame in its writes: doRender took outMu when the components had rendered and keeps it to its last write",
	"tui.go:positionHardwareCursor":                       "a main-screen frame in its writes: doRender holds outMu to its last write",
	"tui.go:stopWithOptionsLocked":                        "StopWithOptions takes mu and outMu before it; the overflow path is a frame in its writes, which holds both",
	"tui_input.go:writeColorSchemeNotifications":          "the primitive of the mode 2031 writes; each caller is audited for the lock",
	"tui_surface.go:SetTerminalColorSchemeNotifications":  "TuiSurface writes through its Terminal, whose Write is the lock of the terminal (ProcessTerminal.outMu)",
	"tui_surface.go:Start":                                "TuiSurface writes through its Terminal, whose Write is the lock of the terminal (ProcessTerminal.outMu)",
	"tui_surface.go:StopWithOptions":                      "TuiSurface writes through its Terminal, whose Write is the lock of the terminal (ProcessTerminal.outMu)",
	"tui_surface.go:Suspend":                              "TuiSurface writes through its Terminal, whose Write is the lock of the terminal (ProcessTerminal.outMu)",
	"tui_surface.go:Resume":                               "TuiSurface writes through its Terminal, whose Write is the lock of the terminal (ProcessTerminal.outMu)",
}

// unlockedRendererWrites reports the writes of file's functions that come before an outMu lock of the function, in functions that are not in held. It records in seen
// each function that writes.
func unlockedRendererWrites(fset *token.FileSet, name string, file *ast.File, held map[string]string, seen map[string]bool) []string {
	var findings []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		key := name + ":" + fn.Name.Name
		outParam := hasWriterParam(fn)
		var writes []token.Pos
		lock := token.NoPos
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if writesToRendererWriter(call, outParam) {
				writes = append(writes, call.Pos())
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Lock" && isOutMu(sel.X) && (lock == token.NoPos || call.Pos() < lock) {
				lock = call.Pos()
			}
			return true
		})
		if len(writes) == 0 {
			continue
		}
		seen[key] = true
		if held[key] != "" {
			continue
		}
		for _, write := range writes {
			if lock == token.NoPos || write < lock {
				findings = append(findings, fset.Position(write).String()+": "+key+" writes before it holds outMu")
			}
		}
	}
	return findings
}

// hasWriterParam reports whether fn takes a parameter `out io.Writer`: only there is an identifier `out` a terminal writer (elsewhere it is a strings.Builder).
func hasWriterParam(fn *ast.FuncDecl) bool {
	for _, field := range fn.Type.Params.List {
		sel, ok := field.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Writer" {
			continue
		}
		for _, name := range field.Names {
			if name.Name == "out" {
				return true
			}
		}
	}
	return false
}

func isOutMu(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "outMu"
}

// writesToRendererWriter reports whether call writes to a renderer's writer, not through writeOwned (which takes outMu).
func writesToRendererWriter(call *ast.CallExpr, outParam bool) bool {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name == "WriteToTerminal" || fun.Name == "writeTerminalText" || fun.Name == "writeString" || fun.Name == "writeColorSchemeNotifications"
	case *ast.SelectorExpr:
		if (fun.Sel.Name == "Write" || fun.Sel.Name == "WriteString") && isWriterTarget(fun.X, outParam) {
			return true
		}
		if len(call.Args) > 0 && isWriterTarget(call.Args[0], outParam) {
			pkg, ok := fun.X.(*ast.Ident)
			return ok && (pkg.Name == "io" && fun.Sel.Name == "WriteString" || pkg.Name == "fmt" && strings.HasPrefix(fun.Sel.Name, "Fprint"))
		}
	}
	return false
}

// isWriterTarget is a renderer's writer: an io.Writer parameter named out, x.out, x.direct or x.cursorOut().
func isWriterTarget(expr ast.Expr, outParam bool) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return outParam && e.Name == "out"
	case *ast.SelectorExpr:
		return e.Sel.Name == "out" || e.Sel.Name == "direct"
	case *ast.CallExpr:
		sel, ok := e.Fun.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "cursorOut"
	}
	return false
}

// The audit must see every spelling of an unlocked write: a planted mutant for each.
func TestTheWriterLockAuditSeesEveryFormOfAnUnlockedWrite(t *testing.T) {
	const header = "package p\nimport (\n\t\"fmt\"\n\t\"io\"\n\t\"os\"\n\t\"sync\"\n)\nvar _ = fmt.Sprint\nvar _ io.Writer\nvar _ = os.Stdout\ntype T struct {\n\tout, direct io.Writer\n\toutMu sync.Mutex\n}\nfunc (t *T) writeOwned(out io.Writer, data string) {}\nfunc (t *T) cursorOut() io.Writer { return t.out }\nfunc WriteToTerminal(io.Writer, string) {}\nfunc writeColorSchemeNotifications(io.Writer, bool) {}\n"
	for _, tc := range []struct {
		name, body string
		flagged    bool
	}{
		{"locked before the write", "t.outMu.Lock()\nWriteToTerminal(t.out, \"x\")\nt.outMu.Unlock()", false},
		{"through writeOwned", "t.writeOwned(t.out, \"x\")", false},
		{"stderr is not the renderer's writer", "_, _ = fmt.Fprint(os.Stderr, \"x\")", false},
		{"mutant: WriteToTerminal with no lock", "WriteToTerminal(t.out, \"x\")", true},
		{"mutant: lock after the write", "WriteToTerminal(t.out, \"x\")\nt.outMu.Lock()\nt.outMu.Unlock()", true},
		{"mutant: the cursor writer", "WriteToTerminal(t.cursorOut(), \"x\")", true},
		{"mutant: the direct writer", "WriteToTerminal(t.direct, \"x\")", true},
		{"mutant: writeColorSchemeNotifications", "writeColorSchemeNotifications(t.out, true)", true},
		{"mutant: io.WriteString(out)", "_, _ = io.WriteString(out, \"x\")", true},
		{"mutant: t.out.Write", "_, _ = t.out.Write([]byte(\"x\"))", true},
		{"mutant: fmt.Fprint(t.out)", "_, _ = fmt.Fprint(t.out, \"x\")", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := header + "func (t *T) f(out io.Writer) {\n" + tc.body + "\n}\n"
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "audited.go", src, 0)
			if err != nil {
				t.Fatal(err)
			}
			got := unlockedRendererWrites(fset, "audited.go", file, nil, map[string]bool{})
			// The planted header declares WriteToTerminal and the like with empty bodies: they write nothing.
			if (len(got) > 0) != tc.flagged {
				t.Fatalf("findings %v, flagged want %t", got, tc.flagged)
			}
		})
	}
	t.Run("a table entry that no longer writes is stale", func(t *testing.T) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "audited.go", header+"func (t *T) f() {}\n", 0)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		unlockedRendererWrites(fset, "audited.go", file, map[string]string{"audited.go:f": "held"}, seen)
		if seen["audited.go:f"] {
			t.Fatal("a function with no write was recorded as writing")
		}
	})
}
