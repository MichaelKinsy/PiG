// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// errorFuncs records, for each function or method name declared in the
// repository, how many declarations end their results with error and how
// many do not. It stands in for type information where a check must know
// whether a call's discarded result was an error.
type errorFuncs map[string]*[2]int

// returnsError reports a name whose every known declaration returns error
// last.
func (m errorFuncs) returnsError(name string) bool {
	c := m[name]
	return c != nil && c[0] > 0 && c[1] == 0
}

// returnsNoError reports a name with a known declaration that does not
// return error last.
func (m errorFuncs) returnsNoError(name string) bool {
	c := m[name]
	return c != nil && c[1] > 0
}

// add records f's function and method declarations and its func-typed
// struct fields and interface methods.
func (m errorFuncs) add(f *ast.File) {
	record := func(name string, ft *ast.FuncType) {
		c := m[name]
		if c == nil {
			c = &[2]int{}
			m[name] = c
		}
		if lastResultIsError(ft) {
			c[0]++
		} else {
			c[1]++
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			record(n.Name.Name, n.Type)
		case *ast.Field:
			if ft, ok := n.Type.(*ast.FuncType); ok {
				for _, name := range n.Names {
					record(name.Name, ft)
				}
			}
		}
		return true
	})
}

func lastResultIsError(ft *ast.FuncType) bool {
	if ft.Results == nil || len(ft.Results.List) == 0 {
		return false
	}
	id, ok := ft.Results.List[len(ft.Results.List)-1].Type.(*ast.Ident)
	return ok && id.Name == "error"
}

// indexErrorFuncs parses every non-test Go file under root once, keeping
// only declarations.
func indexErrorFuncs(root string) (errorFuncs, error) {
	m := errorFuncs{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.SkipObjectResolution)
		if err != nil {
			return nil
		}
		m.add(f)
		return nil
	})
	return m, err
}
