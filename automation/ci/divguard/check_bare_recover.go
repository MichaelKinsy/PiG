// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"go/token"
	"slices"
)

const bareRecoverName = "bare-recover"

// bareRecover flags a recover() whose panic value is thrown away: a bare
// `recover()` or `_ = recover()`, a `recover() != nil` test, or `r :=
// recover()` where r is only compared with nil. The panic then vanishes
// with no event, error or log. A panicking listener silently ended the
// session event forwarder, and the next run hung (AGENT-16). Pi surfaces a
// listener's exception as a failed run. The check scans every non-test Go
// file in the repository: a swallowed panic hides a failure in any package.
var bareRecover = check{
	Name:    bareRecoverName,
	Doc:     "recover() whose panic value is discarded",
	Applies: func(string) bool { return true },
	Run:     runBareRecover,
}

func runBareRecover(fc *fileCtx) []Hit {
	var hits []Hit
	inspect(fc.File, func(n ast.Node, stack []ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isRecover(call) || recoverValueUsed(call, stack) {
			return true
		}
		if h, ok := fc.hit(bareRecoverName, call.Pos(), stack, "recover() discards the panic value; report it as an error or event"); ok {
			hits = append(hits, h)
		}
		return true
	})
	return hits
}

func isRecover(call *ast.CallExpr) bool {
	id, ok := call.Fun.(*ast.Ident)
	return ok && id.Name == "recover" && len(call.Args) == 0
}

// recoverValueUsed reports whether the recovered value reaches anything
// other than a nil comparison.
func recoverValueUsed(call *ast.CallExpr, stack []ast.Node) bool {
	if len(stack) == 0 {
		return false
	}
	switch p := stack[len(stack)-1].(type) {
	case *ast.ExprStmt, *ast.DeferStmt:
		return false
	case *ast.BinaryExpr:
		return !isNil(p.X) && !isNil(p.Y)
	case *ast.AssignStmt:
		if len(p.Lhs) != 1 {
			return true
		}
		id, ok := p.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		if id.Name == "_" {
			return false
		}
		fn := enclosingFunc(stack)
		if p.Tok == token.ASSIGN && !declaredWithin(fn, id.Name) {
			owner := declaringFunc(stack, fn, id.Name)
			if owner == nil || isNamedResult(owner, id.Name) {
				return true // a package variable or a result the caller receives
			}
			return usedBeyondNilCheck(id.Name, owner, p)
		}
		return usedBeyondNilCheck(id.Name, fn, p)
	}
	return true
}

func enclosingFunc(stack []ast.Node) ast.Node {
	for _, s := range slices.Backward(stack) {
		switch f := s.(type) {
		case *ast.FuncLit, *ast.FuncDecl:
			return f
		}
	}
	return nil
}

// declaringFunc returns the nearest function enclosing inner that declares
// name, or nil for a package-level variable.
func declaringFunc(stack []ast.Node, inner ast.Node, name string) ast.Node {
	passed := false
	for _, s := range slices.Backward(stack) {
		switch s.(type) {
		case *ast.FuncLit, *ast.FuncDecl:
			if s == inner {
				passed = true
				continue
			}
			if passed && declaredWithin(s, name) {
				return s
			}
		}
	}
	return nil
}

// isNamedResult reports whether name is a named result of fn.
func isNamedResult(fn ast.Node, name string) bool {
	var ftype *ast.FuncType
	switch f := fn.(type) {
	case *ast.FuncLit:
		ftype = f.Type
	case *ast.FuncDecl:
		ftype = f.Type
	}
	if ftype == nil || ftype.Results == nil {
		return false
	}
	for _, field := range ftype.Results.List {
		for _, n := range field.Names {
			if n.Name == name {
				return true
			}
		}
	}
	return false
}

// declaredWithin reports whether name is declared by fn itself: a parameter,
// a named result, a `:=` or a var declaration. An identifier that is not is
// captured from an outer scope.
func declaredWithin(fn ast.Node, name string) bool {
	if fn == nil {
		return false
	}
	var ftype *ast.FuncType
	switch f := fn.(type) {
	case *ast.FuncLit:
		ftype = f.Type
	case *ast.FuncDecl:
		ftype = f.Type
	}
	for _, list := range []*ast.FieldList{ftype.Params, ftype.Results} {
		if list == nil {
			continue
		}
		for _, field := range list.List {
			for _, n := range field.Names {
				if n.Name == name {
					return true
				}
			}
		}
	}
	declared := false
	ast.Inspect(fn, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.AssignStmt:
			if d.Tok == token.DEFINE {
				for _, lhs := range d.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Name == name {
						declared = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, id := range d.Names {
				if id.Name == name {
					declared = true
				}
			}
		}
		return !declared
	})
	return declared
}

// usedBeyondNilCheck reports a use of name in fn, other than its defining
// assignment or var declaration, that is not one side of `== nil` or
// `!= nil`.
func usedBeyondNilCheck(name string, fn ast.Node, def *ast.AssignStmt) bool {
	if fn == nil {
		return true
	}
	used := false
	inspect(fn, func(n ast.Node, stack []ast.Node) bool {
		if used || n == def.Lhs[0] {
			return !used
		}
		id, ok := n.(*ast.Ident)
		if !ok || id.Name != name {
			return true
		}
		if len(stack) > 0 {
			if b, ok := stack[len(stack)-1].(*ast.BinaryExpr); ok && (b.Op == token.EQL || b.Op == token.NEQ) && (isNil(b.X) || isNil(b.Y)) {
				return true
			}
			if spec, ok := stack[len(stack)-1].(*ast.ValueSpec); ok && slices.Contains(spec.Names, id) {
				return true // the declaration of an outer variable
			}
			if field, ok := stack[len(stack)-1].(*ast.Field); ok && slices.Contains(field.Names, id) {
				return true // a parameter or result declaration
			}
		}
		used = true
		return false
	})
	return used
}
