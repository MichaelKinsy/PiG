// SPDX-License-Identifier: MIT

// Command scan parses tracked Go files without evaluating build constraints.
// Byte offsets let the caller change literals without reprinting unrelated code.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
)

type literal struct {
	Start    int
	End      int
	Value    string
	Import   bool
	PathRoot bool
}

type file struct {
	Path     string
	Literals []literal
	Comments []literal
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var paths []string
	if err := json.NewDecoder(os.Stdin).Decode(&paths); err != nil {
		return err
	}
	var files []file
	for _, path := range paths {
		set := token.NewFileSet()
		node, err := parser.ParseFile(set, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		imports := map[*ast.BasicLit]bool{}
		for _, spec := range node.Imports {
			imports[spec.Path] = true
		}
		pathRoots := map[*ast.BasicLit]bool{}
		ast.Inspect(node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Join" {
				return true
			}
			owner, ok := selector.X.(*ast.Ident)
			if !ok || owner.Name != "filepath" && owner.Name != "path" {
				return true
			}
			for _, arg := range call.Args {
				lit, ok := arg.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil || value == ".." || value == "." {
					continue
				}
				pathRoots[lit] = true
				break
			}
			return true
		})
		result := file{Path: path}
		for _, group := range node.Comments {
			for _, comment := range group.List {
				result.Comments = append(result.Comments, literal{
					Start: set.Position(comment.Pos()).Offset, End: set.Position(comment.End()).Offset,
					Value: comment.Text,
				})
			}
		}
		ast.Inspect(node, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true // ParseFile already rejects invalid Go literals.
			}
			result.Literals = append(result.Literals, literal{
				Start: set.Position(lit.Pos()).Offset, End: set.Position(lit.End()).Offset,
				Value: value, Import: imports[lit], PathRoot: pathRoots[lit],
			})
			return true
		})
		files = append(files, result)
	}
	return json.NewEncoder(os.Stdout).Encode(files)
}
