package codingagent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// Pi's terminal writes are calls of terminal.write on one event loop, so a write is before the bytes of a frame or after them. Here a frame is written
// by the renderer under its writer lock and, with the owner loop running, on that loop; a write of this package that reaches the terminal by another way
// (a direct write to the process's stdout, a write through the renderer's terminal) meets a frame only when it is made off the loop. Every such site is
// named below with the reason it cannot, or the way it is ordered. A new site fails here until it is read and listed.
var terminalWriteSites = map[string]string{
	"OpenExternalEditor":                        "the TUI is stopped before the editor runs (handleOpenExternalEditor calls ui.stop() first), so no frame exists; Pi writes the notice with process.stdout.write at the same place",
	"clipboardCopier.emitOSC52":                 "runs through c.ordered, which hands the write to the owner loop while a mode runs (orderClipboardWrites)",
	"InteractiveMode.writeDirectOutsideStop":    "the renderer's WriteRaw takes the writer lock; this direct write runs once the TUI stopped or for a renderer that cannot write raw, through the terminal's stop transaction (writeOutsideStop), which orders it before or after a stop",
	"InteractiveMode.Init":                      "the model scope notice is written before the renderer exists",
	"presentationTheme.writeThemeNotifications": "the branch that writes to the output runs without a renderer; with one the renderer writes under its lock",
	"InteractiveMode.setTerminalTitle":          "runs through writeBetweenFrames; the terminal's SetTitle is ordered against a stop (WriteOutsideStop)",
	"InteractiveMode.setTerminalProgress":       "called on the owner loop (agent events and the teardown); the keepalive goes through the renderer's owner dispatcher",
}

// terminalWriteCall reports whether call writes to the terminal other than through the renderer frame writes.
func terminalWriteCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if pkg, ok := selector.X.(*ast.Ident); ok {
		switch {
		case pkg.Name == "tui" && selector.Sel.Name == "WriteToTerminal":
			return true
		case pkg.Name == "io" && selector.Sel.Name == "WriteString" && len(call.Args) > 0:
			if arg, ok := call.Args[0].(*ast.CallExpr); ok {
				if inner, ok := arg.Fun.(*ast.SelectorExpr); ok && inner.Sel.Name == "output" {
					return true
				}
			}
		}
	}
	switch selector.Sel.Name {
	case "SetTitle", "SetProgress", "SetProgramStatus", "HideCursor", "ShowCursor", "Write":
		if receiver, ok := selector.X.(*ast.CallExpr); ok {
			if inner, ok := receiver.Fun.(*ast.SelectorExpr); ok && inner.Sel.Name == "Terminal" {
				return true
			}
		}
	}
	return false
}

func TestEveryTerminalWriteOutsideTheFrameIsOrdered(t *testing.T) {
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
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			key := fn.Name.Name
			if fn.Recv != nil && len(fn.Recv.List) == 1 {
				recv := fn.Recv.List[0].Type
				if star, ok := recv.(*ast.StarExpr); ok {
					recv = star.X
				}
				if ident, ok := recv.(*ast.Ident); ok {
					key = ident.Name + "." + key
				}
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok && terminalWriteCall(call) {
					seen[key] = true
					if _, listed := terminalWriteSites[key]; !listed {
						findings = append(findings, name+": "+key)
					}
				}
				return true
			})
		}
	}
	sort.Strings(findings)
	for _, finding := range findings {
		t.Errorf("%s writes to the terminal outside the frame and is not listed in terminalWriteSites with the reason it is ordered against a frame", finding)
	}
	for key := range terminalWriteSites {
		if !seen[key] {
			t.Errorf("terminalWriteSites lists %s, which no longer writes to the terminal", key)
		}
	}
}
