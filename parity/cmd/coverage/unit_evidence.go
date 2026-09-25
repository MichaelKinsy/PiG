package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Unit evidence names reviewed observable assertions and their compiling
// mutations. It is separate from paired Pi/PiG scenarios and is not a run result.
type unitEvidence struct {
	Upstream          string   `json:"upstream"`
	Package           string   `json:"package"`
	Tests             []string `json:"tests"`
	UpstreamReference string   `json:"upstream_reference"`
	Mutation          string   `json:"mutation"`
}

func loadUnitEvidence(root string, entries []portMapEntry) (map[string][]string, error) {
	paths, err := filepath.Glob(filepath.Join(root, "parity", "unit-evidence", "*.json"))
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, entry := range entries {
		known[entry.UpstreamPath] = true
	}
	result := map[string][]string{}
	packages := map[string]map[string]bool{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var records []unitEvidence
		if err := json.Unmarshal(data, &records); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for _, record := range records {
			if !known[record.Upstream] || len(record.Tests) == 0 || strings.TrimSpace(record.UpstreamReference) == "" || strings.TrimSpace(record.Mutation) == "" {
				return nil, fmt.Errorf("%s: incomplete unit evidence or unknown upstream %q", path, record.Upstream)
			}
			pkg := strings.TrimPrefix(record.Package, "./")
			if !strings.HasPrefix(record.Package, "./") || !filepath.IsLocal(pkg) || filepath.ToSlash(filepath.Clean(pkg)) != pkg {
				return nil, fmt.Errorf("%s: invalid package %q", path, record.Package)
			}
			tests, ok := packages[pkg]
			if !ok {
				tests, err = declaredTests(filepath.Join(root, pkg))
				if err != nil {
					return nil, err
				}
				packages[pkg] = tests
			}
			for _, test := range record.Tests {
				if !tests[test] {
					return nil, fmt.Errorf("%s: test %s does not exist in %s", path, test, record.Package)
				}
				result[record.Upstream] = append(result[record.Upstream], "unit:"+record.Package+":"+test)
			}
		}
	}
	return result, nil
}

func declaredTests(dir string) (map[string]bool, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil, err
	}
	tests := map[string]bool{}
	for _, path := range paths {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return nil, err
		}
		testingAliases := map[string]bool{}
		for _, imp := range file.Imports {
			name, err := strconv.Unquote(imp.Path.Value)
			if err != nil || name != "testing" {
				continue
			}
			alias := "testing"
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			testingAliases[alias] = true
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Type.Params.NumFields() != 1 || fn.Type.Results.NumFields() != 0 || fn.Body == nil {
				continue
			}
			suffix := strings.TrimPrefix(fn.Name.Name, "Test")
			r, _ := utf8.DecodeRuneInString(suffix)
			if suffix != "" && unicode.IsLower(r) {
				continue
			}
			param, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			typ, ok := param.X.(*ast.SelectorExpr)
			if !ok || typ.Sel.Name != "T" {
				continue
			}
			ident, ok := typ.X.(*ast.Ident)
			if !ok || !testingAliases[ident.Name] {
				continue
			}
			tests[fn.Name.Name] = true
		}
	}
	return tests, nil
}

func addUnitEvidence(coverage, behavioral map[string][]string, units map[string][]string) {
	for path, tests := range units {
		coverage[path] = append(coverage[path], tests...)
		behavioral[path] = append(behavioral[path], tests...)
	}
}

func splitEvidence(names []string) (scenarios, units []string) {
	for _, name := range names {
		if unit, ok := strings.CutPrefix(name, "unit:"); ok {
			units = append(units, unit)
		} else {
			scenarios = append(scenarios, name)
		}
	}
	return scenarios, units
}
