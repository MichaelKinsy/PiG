// SPDX-License-Identifier: MIT

// Package gotests reads the tests a Go test file declares, for the parity ledgers that cite a Go test by name as evidence.
package gotests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// File is one parsed Go test file and the functions `go test` runs from it.
type File struct {
	Syntax *ast.File
	// Tests holds the top-level tests and fuzz targets by name. A fuzz target runs its seed corpus under go test, so evidence
	// may name one like a test.
	Tests map[string]*ast.FuncDecl
}

// Parse reads the Go test file at path. A function is a test when go test would run it: Test or Fuzz followed by no lower-case
// letter, no receiver, no result, a body, and one parameter of type *testing.T (*testing.F for a fuzz target).
func Parse(path string) (File, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return File{}, err
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
	tests := map[string]*ast.FuncDecl{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Type.Params.NumFields() != 1 || fn.Type.Results.NumFields() != 0 || fn.Body == nil {
			continue
		}
		prefix, paramType := "Test", "T"
		if strings.HasPrefix(fn.Name.Name, "Fuzz") {
			prefix, paramType = "Fuzz", "F"
		}
		if !strings.HasPrefix(fn.Name.Name, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(fn.Name.Name, prefix)
		r, _ := utf8.DecodeRuneInString(suffix)
		if suffix != "" && unicode.IsLower(r) {
			continue
		}
		param, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		typ, ok := param.X.(*ast.SelectorExpr)
		if !ok || typ.Sel.Name != paramType {
			continue
		}
		ident, ok := typ.X.(*ast.Ident)
		if !ok || !testingAliases[ident.Name] {
			continue
		}
		tests[fn.Name.Name] = fn
	}
	return File{Syntax: file, Tests: tests}, nil
}
