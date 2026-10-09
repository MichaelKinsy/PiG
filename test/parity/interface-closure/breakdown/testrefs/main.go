// testrefs prints, per directory with Go tests, which identifiers each Test function reaches (itself plus same-package helper functions, depth 3)
// and whether it asserts (calls t.Error*/Fatal*/Fail*/assert/require). Output: {dir:{ident:[ "TestName|a" or "TestName|s" ]}}.
package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// oracleName matches tests, helpers and literals that run the pinned Pi (Node) oracle.
var oracleName = regexp.MustCompile(`(?i)oracle|runnode|nodescript|pinnedcomparison|matchespi|matchpi|runpi|againstpi|piscript`)

type fn struct {
	idents  map[string]bool
	calls   map[string]bool
	asserts bool
}

func main() {
	root := os.Args[1]
	pkgs := map[string]map[string]*fn{} // dir -> func name -> info (tests and helpers of *_test.go files)
	fset := token.NewFileSet()
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			n := d.Name()
			if n == ".git" || n == ".upstream" || n == "node_modules" || n == "vendor" || n == "target" || n == "build" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			return nil
		}
		dir, _ := filepath.Rel(root, filepath.Dir(p))
		if pkgs[dir] == nil {
			pkgs[dir] = map[string]*fn{}
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			name := fd.Name.Name
			if fd.Recv != nil {
				name = "." + name
			}
			info := &fn{idents: map[string]bool{}, calls: map[string]bool{}}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.Ident:
					info.idents[x.Name] = true
				case *ast.BasicLit:
					if x.Kind == token.STRING && len(x.Value) <= 64 {
						info.idents[strings.Trim(x.Value, "`\"")] = true
					}
				case *ast.CallExpr:
					switch c := x.Fun.(type) {
					case *ast.Ident:
						info.calls[c.Name] = true
					case *ast.SelectorExpr:
						s := c.Sel.Name
						if strings.HasPrefix(s, "Error") || strings.HasPrefix(s, "Fatal") || strings.HasPrefix(s, "Fail") {
							info.asserts = true
						}
						if id, ok := c.X.(*ast.Ident); ok && (id.Name == "assert" || id.Name == "require") {
							info.asserts = true
						}
						info.calls[s] = true
					}
				}
				return true
			})
			pkgs[dir][name] = info
		}
		return nil
	})
	out := map[string]map[string][]string{}
	for dir, fns := range pkgs {
		for name, info := range fns {
			if !strings.HasPrefix(name, "Test") {
				continue
			}
			ids := map[string]bool{}
			asserts := info.asserts
			seen := map[string]bool{name: true}
			frontier := []*fn{info}
			for depth := 0; depth < 4 && len(frontier) > 0; depth++ {
				var next []*fn
				for _, f := range frontier {
					for i := range f.idents {
						ids[i] = true
					}
					for c := range f.calls {
						if h, ok := fns[c]; ok && !seen[c] && !strings.HasPrefix(c, "Test") {
							seen[c] = true
							asserts = asserts || h.asserts
							next = append(next, h)
						}
					}
				}
				frontier = next
			}
			if oracleName.MatchString(name) {
				ids["@oracle"] = true
			}
			for i := range ids {
				if oracleName.MatchString(i) {
					ids["@oracle"] = true
					break
				}
			}
			tag := "|s"
			if asserts {
				tag = "|a"
			}
			if out[dir] == nil {
				out[dir] = map[string][]string{}
			}
			for i := range ids {
				out[dir][i] = append(out[dir][i], name+tag)
			}
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}
