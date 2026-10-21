package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The native SDKs answer Pi's synchronous getters (ctx.model, ctx.sessionManager.getSessionName(), ctx.hasUI, ...) from values the host replicates in its
// ready state and every state_update. A value an SDK reads but assigns only at ready never changes again: Python ctx.session_name and Rust ctx.session_name()
// answered "" for the life of the process because the host never filled the ready payload's name and no state_update path read it
// (docs/extension-api-parity.md, "Replicated accessors"). These tests fail when a replicated field is assigned from nowhere but the ready message, and when
// a new replicated field is added without saying where it is written.

// replicatedReadyOnly names the replicated fields that are written outside a state snapshot, with the reason: they never change within a connection or
// have their own notify.
var replicatedReadyOnly = map[string]string{
	"cwd":    "fixed for the connection: the ready payload's working directory",
	"mode":   "fixed for the connection: the ready payload's run mode",
	"width":  "width_change notifies",
	"height": "height_change notifies",
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// funcBody returns the source of the Python, Rust or Go function that starts at the line matching start, up to the next line at the same or a shallower indent.
func funcBody(t *testing.T, src string, start *regexp.Regexp) string {
	t.Helper()
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		if !start.MatchString(line) {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		end := i + 1
		for ; end < len(lines); end++ {
			l := lines[end]
			if strings.TrimSpace(l) != "" && len(l)-len(strings.TrimLeft(l, " \t")) <= indent {
				break
			}
		}
		return strings.Join(lines[i:end], "\n")
	}
	t.Fatalf("no function matches %v", start)
	return ""
}

func TestGoSDKReplicatedFieldsAreAssignedFromStateUpdates(t *testing.T) {
	fset := token.NewFileSet()
	dir := filepath.Join(repoRoot, "extensions", "sdk")
	pkgs, err := parser.ParseDir(fset, dir, func(info os.FileInfo) bool { return !strings.HasSuffix(info.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg := pkgs["sdk"]
	if pkg == nil {
		t.Fatal("package sdk not found")
	}
	// The fields the Extension struct guards with mu, in declaration order, up to the next lock.
	var guarded []string
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != "Extension" {
					continue
				}
				in := false
				for _, field := range ts.Type.(*ast.StructType).Fields.List {
					for _, name := range field.Names {
						switch {
						case name.Name == "mu":
							in = true
						case name.Name == "requestsMu":
							in = false
						case in:
							guarded = append(guarded, name.Name)
						}
					}
				}
			}
		}
	}
	if len(guarded) < 8 {
		t.Fatalf("found only %v after Extension.mu: the struct layout changed, update this test", guarded)
	}
	// The functions that apply a state snapshot: handleNotifyAt (state_update and the ready state, which runs through it) and what it calls.
	stateFuncs := map[string]bool{"handleNotifyAt": true, "applyReplicatedState": true, "applyFooterData": true}
	assigned := map[string]bool{}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !stateFuncs[fn.Name.Name] {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.AssignStmt:
					for _, lhs := range n.Lhs {
						if sel, ok := lhs.(*ast.SelectorExpr); ok {
							assigned[sel.Sel.Name] = true
						}
					}
				case *ast.IncDecStmt:
					if sel, ok := n.X.(*ast.SelectorExpr); ok {
						assigned[sel.Sel.Name] = true
					}
				}
				return true
			})
		}
	}
	for _, field := range guarded {
		if _, exempt := replicatedReadyOnly[field]; exempt {
			continue
		}
		if !assigned[field] {
			t.Errorf("Extension.%s is replicated state (guarded by mu) but no state_update path assigns it: apply it in handleNotifyAt, or list it in replicatedReadyOnly with the reason", field)
		}
	}
	for field := range replicatedReadyOnly {
		if !slices.Contains(guarded, field) {
			t.Errorf("replicatedReadyOnly names %s, which is no longer a guarded Extension field", field)
		}
	}
}

func TestPythonSDKReplicatedFieldsAreAssignedFromStateUpdates(t *testing.T) {
	src := readRepoFile(t, "extensions/sdk-py/pig_sdk/__init__.py")
	// Context's replicated getters: `with self.extension._state_lock: return self.extension._x`.
	read := regexp.MustCompile(`with self\.extension\._state_lock:\s*\n\s*return ([^\n]+)`)
	attr := regexp.MustCompile(`self\.extension\._(\w+)`)
	var fields []string
	for _, m := range read.FindAllStringSubmatch(src, -1) {
		for _, a := range attr.FindAllStringSubmatch(m[1], -1) {
			if !slices.Contains(fields, a[1]) {
				fields = append(fields, a[1])
			}
		}
	}
	if len(fields) < 6 {
		t.Fatalf("found only %v: the getters changed shape, update this test", fields)
	}
	apply := funcBody(t, src, regexp.MustCompile(`^    def _apply_ui_state\(`)) + "\n" + funcBody(t, src, regexp.MustCompile(`^    def _handle_notify\(`))
	for _, field := range fields {
		if _, exempt := replicatedReadyOnly[field]; exempt {
			continue
		}
		if !regexp.MustCompile(`\bself\._` + field + `\b[^\n]*=[^=]|\bself\._` + field + `\b,|, self\._` + field + `\b[^\n]*=[^=]`).MatchString(apply) {
			t.Errorf("Extension._%s is replicated state but no state_update path (_apply_ui_state, _handle_notify) assigns it: apply it from the snapshot, or list it in replicatedReadyOnly with the reason", field)
		}
	}
}

func TestRustSDKReplicatedStateIsAppliedFromStateUpdates(t *testing.T) {
	for _, tc := range []struct{ file, structName, impl string }{
		{"extensions/sdk-rs/src/context.rs", "SessionIdentity", "SessionIdentity"},
		{"extensions/sdk-rs/src/extension_api.rs", "ApiState", "ApiState"},
		{"extensions/sdk-rs/src/theme.rs", "UiState", "UiState"},
	} {
		src := readRepoFile(t, tc.file)
		body := funcBody(t, src, regexp.MustCompile(`^(pub\(crate\) )?struct `+tc.structName+`\b`))
		var fields []string
		for _, m := range regexp.MustCompile(`(?m)^\s+(?:pub(?:\(crate\))? )?(\w+):`).FindAllStringSubmatch(body, -1) {
			fields = append(fields, m[1])
		}
		if len(fields) == 0 {
			t.Fatalf("%s has no fields", tc.structName)
		}
		apply := funcBody(t, src, regexp.MustCompile(`^\s+pub\(crate\) fn apply_state\(`))
		if !strings.Contains(src, "impl "+tc.impl+" {") {
			t.Fatalf("no impl %s", tc.impl)
		}
		for _, field := range fields {
			if !regexp.MustCompile(`self\.` + field + `\b`).MatchString(apply) {
				t.Errorf("%s.%s is replicated state but %s::apply_state does not assign it: the ready state and every state_update pass through it", tc.structName, field, tc.structName)
			}
		}
	}
}
