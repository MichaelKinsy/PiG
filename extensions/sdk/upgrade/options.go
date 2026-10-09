// SPDX-License-Identifier: MIT

package upgrade

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"
)

// planOptionalBools wraps a bool the compiler rejects for an optional *bool
// field of an SDK struct literal in sdk.Bool.
func (p *filePlanner) planOptionalBools() {
	var rejected []types.Error
	for _, typeErr := range p.errors {
		before, _, found := strings.Cut(typeErr.Msg, " as *bool value")
		if found && strings.HasPrefix(before, "cannot use ") && strings.Contains(before, "bool") {
			rejected = append(rejected, typeErr)
		}
	}
	if len(rejected) == 0 {
		return
	}
	ast.Inspect(p.file.Syntax, func(node ast.Node) bool {
		pair, ok := node.(*ast.KeyValueExpr)
		if !ok || !p.rejectedAt(rejected, pair.Value.Pos()) {
			return true
		}
		key, ok := pair.Key.(*ast.Ident)
		if !ok {
			return true
		}
		field, ok := p.pkg.Info.Uses[key].(*types.Var)
		if !ok || !field.IsField() || field.Pkg() == nil || !IsSDKPath(field.Pkg().Path()) {
			return true
		}
		symbol := p.structName(pair, key.Name)
		if _, known := changeFor(symbol); !known {
			return true
		}
		open, closing := p.sdk+".Bool(", ")"
		switch p.sdk {
		case "":
			p.skip(RuleOptionalBool, symbol, pair.Value.Pos(), "the file does not import the SDK by name")
			return true
		case ".":
			open = "Bool("
		}
		p.sites = append(p.sites, site{
			rewrites: []Rewrite{{Rule: RuleOptionalBool, Symbol: symbol, Line: p.line(pair.Value.Pos())}},
			edits: []edit{
				{start: p.offset(pair.Value.Pos()), end: p.offset(pair.Value.Pos()), text: open},
				{start: p.offset(pair.Value.End()), end: p.offset(pair.Value.End()), text: closing},
			},
		})
		return true
	})
}

func (p *filePlanner) rejectedAt(rejected []types.Error, pos token.Pos) bool {
	for _, typeErr := range rejected {
		if typeErr.Pos == pos {
			return true
		}
	}
	return false
}

// structName returns "Type.Field" for the struct literal that holds pair.
func (p *filePlanner) structName(pair *ast.KeyValueExpr, field string) string {
	name := ""
	ast.Inspect(p.file.Syntax, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok || name != "" || literal.Pos() > pair.Pos() || pair.End() > literal.End() {
			return name == ""
		}
		for _, element := range literal.Elts {
			if element == ast.Expr(pair) {
				if named, ok := types.Unalias(p.pkg.Info.Types[literal].Type).(*types.Named); ok {
					name = named.Obj().Name() + "." + field
				}
			}
		}
		return name == ""
	})
	return name
}

// planContextUsage reads ContextUsage.Tokens and Percent through TokensOr(0)
// and PercentOr(0), which is what the old int and float64 fields returned for
// unknown usage. It runs on a file the compiler rejects for those fields, or
// that needed the getter rewrite: that file was written before the fields were
// nullable.
func (p *filePlanner) planContextUsage(legacy bool) {
	if !legacy {
		for _, typeErr := range p.errors {
			if usagePointerTypes.MatchString(typeErr.Msg) {
				legacy = true
			}
		}
	}
	if !legacy {
		return
	}
	var stack []ast.Node
	ast.Inspect(p.file.Syntax, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, node)
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || (selector.Sel.Name != "Tokens" && selector.Sel.Name != "Percent") || !p.isUsageField(selector) || p.isNullableUse(stack) {
			return true
		}
		symbol := "ContextUsage." + selector.Sel.Name
		p.sites = append(p.sites, site{
			rewrites: []Rewrite{{Rule: RuleContextUsage, Symbol: symbol, Line: p.line(selector.Pos())}},
			edits:    []edit{{start: p.offset(selector.Sel.Pos()), end: p.offset(selector.Sel.End()), text: selector.Sel.Name + "Or(0)"}},
		})
		return true
	})
}

// isUsageField reports whether selector reads Tokens or Percent of the SDK's ContextUsage.
func (p *filePlanner) isUsageField(selector *ast.SelectorExpr) bool {
	selection := p.pkg.Info.Selections[selector]
	if selection == nil || selection.Kind() != types.FieldVal {
		return false
	}
	t := selection.Recv()
	if pointer, ok := t.(*types.Pointer); ok {
		t = pointer.Elem()
	}
	named, ok := types.Unalias(t).(*types.Named)
	return ok && named.Obj().Name() == "ContextUsage" && named.Obj().Pkg() != nil && IsSDKPath(named.Obj().Pkg().Path())
}

// isNullableUse reports whether the selector at the end of stack already
// treats the field as a pointer: a nil comparison, a dereference, an address,
// or an assignment target.
func (p *filePlanner) isNullableUse(stack []ast.Node) bool {
	if len(stack) < 2 {
		return false
	}
	self, parent := stack[len(stack)-1], stack[len(stack)-2]
	switch node := parent.(type) {
	case *ast.StarExpr:
		return true
	case *ast.UnaryExpr:
		return node.Op == token.AND
	case *ast.BinaryExpr:
		if node.Op != token.EQL && node.Op != token.NEQ {
			return false
		}
		other := node.Y
		if self == ast.Node(node.Y) {
			other = node.X
		}
		ident, ok := other.(*ast.Ident)
		return ok && ident.Name == "nil"
	case *ast.AssignStmt:
		for _, target := range node.Lhs {
			if target == self {
				return true
			}
		}
	case *ast.IncDecStmt:
		return true
	}
	return false
}
