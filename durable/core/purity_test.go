// SPDX-License-Identifier: MIT

package core

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// layers lists the core packages bottom-up; a package may import only packages before it (ADR D15, LAYOUT.md).
var layers = []string{"abi", "jv", "delta", "rec", "history", "store", "session", "turn", "."}

// tooling directories are not core: they may use the standard library freely.
var tooling = []string{
	"cmd", "host", "sqlhost", "driver", "probe", "budget", "contracttest", "abi/spec", "abi/abitest", "abi/payload",
	"history/internal", "history/oracle", "history/testdata",
}

var forbiddenImports = []string{
	"encoding/json", "reflect", "fmt", "os", "time", "sync", "sync/atomic", "context", "unsafe", "syscall", "runtime",
	"log", "log/slog", "io/ioutil", "net", "math/rand", "math/rand/v2", "crypto/rand", "encoding/gob", "text/template",
}

const modulePrefix = "github.com/MichaelKinsy/PiG/durable/core"

// TestCorePurity enforces the core rules of package doc: no reflection, encoding/json, fmt, OS, clock, sync or
// network packages; no goroutines or channels; no package-level mutable state; imports only down the layer list.
func TestCorePurity(t *testing.T) {
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if slices.Contains(tooling, path) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		pkg := filepath.ToSlash(filepath.Dir(path))
		layer := slices.Index(layers, pkg)
		if layer < 0 {
			t.Errorf("%s: package %s is not in the layer list of purity_test.go (add it, and to LAYOUT.md)", path, pkg)
		}
		for _, imp := range file.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if slices.Contains(forbiddenImports, p) || strings.HasPrefix(p, "net/") || strings.HasPrefix(p, "os/") {
				t.Errorf("%s imports %s, which the core forbids", path, p)
			}
			if rest, ok := strings.CutPrefix(p, modulePrefix); ok {
				dep := strings.TrimPrefix(rest, "/")
				if dep == "" {
					dep = "."
				}
				if at := slices.Index(layers, dep); at < 0 || at >= layer {
					t.Errorf("%s imports %s, which is not below %s in the layer list", path, p, pkg)
				}
			} else if strings.Contains(p, ".") {
				t.Errorf("%s imports %s; the core depends only on the standard library", path, p)
			}
		}
		for _, decl := range file.Decls {
			g, ok := decl.(*ast.GenDecl)
			if !ok || g.Tok != token.VAR {
				continue
			}
			doc := strings.ToLower(g.Doc.Text())
			for _, spec := range g.Specs {
				vs := spec.(*ast.ValueSpec)
				if !strings.Contains(doc, "read-only") && !strings.Contains(strings.ToLower(vs.Doc.Text()), "read-only") {
					t.Errorf("%s: package-level var %s must be read-only and documented as such", path, vs.Names[0].Name)
				}
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.GoStmt:
				t.Errorf("%s: go statement in the core", fset.Position(n.Pos()))
			case *ast.ChanType, *ast.SelectStmt:
				t.Errorf("%s: channels in the core", fset.Position(n.Pos()))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
