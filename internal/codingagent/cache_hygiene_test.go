package codingagent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestNoTestConstructsSubprocessHostWithoutPinningPIGHome is a static guard
// against the class of bug where a test builds/loads a subprocess extension
// via subprocess.NewHost or subprocess.NewBuilder without pinning PIG_HOME
// first. Both fall back to resolveConfigRoot()
// (coding/extension/host/subprocess/builder.go), which reads $PIG_HOME, then
// $XDG_CONFIG_HOME, then the process's real $HOME - so an unguarded test
// writes the extension build cache and the pigsdklock SDK transaction lock
// under the operator's real ~/.pig/cache instead of an ephemeral test dir.
//
// This does not run the tests with $HOME redirected: overriding $HOME for a
// real `go test` invocation makes the Go toolchain treat that directory as
// GOPATH/GOCACHE and re-download the entire module cache into it, which is
// far too expensive and slow for a guard test (confirmed by hand while
// investigating this bug). A static scan is the robust, cheap alternative:
// it looks at every *_test.go source file in the module for a call to
// subprocess.NewHost/NewBuilder and requires either that test's own function
// body, or a TestMain in the same package, to reference PIG_HOME.
//
// Before the fix, this test failed on
// internal/codingagent/reload_sdk_stage_test.go's
// TestReloadListsAFailingExtensionOnce and TestReloadListsExtensionToolConflicts,
// which is exactly how the leak into the real ~/.pig/cache was found.
func TestNoTestConstructsSubprocessHostWithoutPinningPIGHome(t *testing.T) {
	root := moduleRoot(t)

	packagePinsHome := map[string]bool{} // package dir -> a TestMain in it sets PIG_HOME
	type unsafeCall struct {
		file string
		line int
		expr string
	}
	var byPackage = map[string][]unsafeCall{}

	skipDirs := map[string]bool{
		".git": true, ".upstream": true, "node_modules": true,
		".devcache": true, "bin": true, "dist": true, "vendor": true,
	}

	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			// Skip symlinked dirs (e.g. .upstream, node_modules links created
			// by the worktree helper) so this scan stays fast and in-tree.
			if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			// Non-Go-parseable test fixtures (rare) are not this check's concern.
			return nil //nolint:nilerr // parse failures are out of scope for this static scan
		}
		pkgDir := filepath.Dir(path)

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if fn.Name.Name == "TestMain" && strings.Contains(string(src), "PIG_HOME") {
				packagePinsHome[pkgDir] = true
			}
			var bodyBuf strings.Builder
			var calls []unsafeCall
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok || ident.Name != "subprocess" {
					return true
				}
				if sel.Sel.Name != "NewHost" && sel.Sel.Name != "NewBuilder" {
					return true
				}
				pos := fset.Position(call.Pos())
				calls = append(calls, unsafeCall{
					file: pos.Filename,
					line: pos.Line,
					expr: "subprocess." + sel.Sel.Name + "(...)",
				})
				return true
			})
			if len(calls) == 0 {
				continue
			}
			// A body-local reference to PIG_HOME (t.Setenv("PIG_HOME", ...) or
			// similar) is enough: this is a lint-style heuristic, not an
			// ordering proof.
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && strings.Contains(lit.Value, "PIG_HOME") {
					bodyBuf.WriteString("PIG_HOME")
				}
				return true
			})
			if strings.Contains(bodyBuf.String(), "PIG_HOME") {
				continue
			}
			byPackage[pkgDir] = append(byPackage[pkgDir], calls...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	var violations []string
	for pkgDir, calls := range byPackage {
		if packagePinsHome[pkgDir] {
			continue // a TestMain in this package isolates PIG_HOME for every test
		}
		for _, c := range calls {
			rel, relErr := filepath.Rel(root, c.file)
			if relErr != nil {
				rel = c.file
			}
			violations = append(violations, fmt.Sprintf("%s:%d: %s without pinning PIG_HOME "+
				"(neither this test nor a package TestMain sets it) - it will fall back to the "+
				"operator's real $HOME/.pig/cache", rel, c.line, c.expr))
		}
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("tests that construct a subprocess.Host/Builder must pin PIG_HOME to a t.TempDir() "+
			"(either in the test itself or via a package TestMain), or they write extension build "+
			"caches and the pigsdklock SDK transaction lock under the real ~/.pig/cache:\n%s",
			strings.Join(violations, "\n"))
	}
}

// moduleRoot walks up from the current package directory to find the module
// root (the directory containing this repo's go.mod).
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
			if strings.Contains(string(data), "module github.com/MichaelKinsy/PiG\n") ||
				strings.HasPrefix(string(data), "module github.com/MichaelKinsy/PiG\n") {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (go.mod for github.com/MichaelKinsy/PiG)")
		}
		dir = parent
	}
}
