// SPDX-License-Identifier: MIT

package checks

import (
	"go/ast"
	"go/token"
	"go/types"
	"regexp"
	"strings"
)

// isPortFile reports whether the file declares itself a port of an upstream TypeScript file.
func (p *Pass) isPortFile(pos token.Pos) bool {
	f := p.fileOf(pos)
	if f == nil {
		return false
	}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "// Ports packages/") || strings.HasPrefix(c.Text, "//Ports packages/") {
				return true
			}
		}
	}
	return false
}

func isString(t types.Type) bool {
	b, ok := types.Unalias(t).Underlying().(*types.Basic)
	return ok && b.Info()&types.IsString != 0
}

var unitNameRe = regexp.MustCompile(`(?i)(max|limit|width|cols|trunc|cap|size|budget|window|cursor|offset|col|pos|chars|columns|head|tail|preview|clip)`)

// isUnitBound reports whether e names a text limit or position: a positive constant or an identifier such as maxLen, width or cursor.
func (p *Pass) isUnitBound(e ast.Expr) bool {
	if e == nil {
		return false
	}
	if tv := p.TypesInfo.Types[e]; tv.Value != nil {
		return tv.Value.String() != "0" && tv.Value.String() != "1"
	}
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Ident:
			if unitNameRe.MatchString(n.Name) {
				found = true
			}
		case *ast.CallExpr:
			// An offset returned by a strings search counts bytes in both directions.
			if fn := p.Callee(n); fn != nil && fn.Pkg() != nil && (fn.Pkg().Path() == "strings" || fn.Pkg().Path() == "bytes") {
				found = false
				return false
			}
		}
		return !found
	})
	return found
}

// UTF16Units flags byte-unit string arithmetic in files ported from TypeScript, where length, slice and index count UTF-16 code units.
// It flags only a string measured or cut against a limit or position, the shape of truncation, cursor and width code.
var UTF16Units = newCheck("utf16units",
	"len(s) or s[a:b] against a limit or position in a file ported from TypeScript: JS counts UTF-16 code units, Go counts bytes",
	Med, "truncation, cursor and width mismatches on non-ASCII text",
	func(p *Pass) {
		if strings.HasSuffix(p.Pkg.Path(), "/internal/jsstring") {
			return
		}
		p.Inspect.WithStack([]ast.Node{(*ast.CallExpr)(nil), (*ast.SliceExpr)(nil), (*ast.IndexExpr)(nil)}, func(n ast.Node, push bool, stack []ast.Node) bool {
			if !push || p.IsTest(n.Pos()) || !p.isPortFile(n.Pos()) {
				return true
			}
			switch n := n.(type) {
			case *ast.CallExpr:
				id, ok := n.Fun.(*ast.Ident)
				if !ok || id.Name != "len" || len(n.Args) != 1 || !isString(p.TypesInfo.TypeOf(n.Args[0])) {
					return true
				}
				if _, builtin := p.TypesInfo.Uses[id].(*types.Builtin); !builtin || len(stack) < 2 {
					return true
				}
				if b, ok := stack[len(stack)-2].(*ast.BinaryExpr); ok {
					other := b.Y
					if b.Y == ast.Expr(n) {
						other = b.X
					}
					switch b.Op {
					case token.LSS, token.GTR, token.LEQ, token.GEQ, token.EQL, token.NEQ:
						if p.isUnitBound(other) {
							p.Reportf(n.Pos(), "len of a string counts bytes; JS .length counts UTF-16 code units")
						}
					}
				}
			case *ast.SliceExpr:
				if isString(p.TypesInfo.TypeOf(n.X)) && (p.isUnitBound(n.Low) || p.isUnitBound(n.High)) {
					p.Reportf(n.Pos(), "string slice by byte offsets; JS slice/substring uses UTF-16 code-unit offsets")
				}
			case *ast.IndexExpr:
				if isString(p.TypesInfo.TypeOf(n.X)) && p.isUnitBound(n.Index) {
					p.Reportf(n.Pos(), "string index yields a byte; JS charAt/[] yields a UTF-16 code unit")
				}
			}
			return true
		})
	})

var regexpCtors = map[string]map[string]bool{
	"regexp":                            {"Compile": true, "MustCompile": true},
	modulePath + "/internal/lazyregexp": {"New": true, "MustCompile": true},
}

// RegexFold flags (?i) patterns. Go folds Unicode simple case; a JS /i pattern folds by toUpperCase.
var RegexFold = newCheck("regexfold",
	"(?i) in a Go regexp: JS /i folds case differently (Kelvin sign, long s, dotless i)",
	Low, "gap-untested O2",
	func(p *Pass) {
		p.Inspect.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
			call := n.(*ast.CallExpr)
			fn := p.Callee(call)
			if fn == nil || fn.Pkg() == nil || len(call.Args) == 0 || !regexpCtors[fn.Pkg().Path()][fn.Name()] {
				return
			}
			if pat, ok := StringLit(call.Args[0]); ok && strings.Contains(pat, "(?i") {
				p.Reportf(call.Pos(), "(?i) pattern %q: a JS /i regex folds with toUpperCase, Go with Unicode simple folding", pat)
			}
		})
	})

// Numbers flags integer arithmetic and parsing in files ported from TypeScript, where every number is a float64.
var Numbers = newCheck("numbers",
	"integer division, float-to-int conversion or strconv parsing in a file ported from TypeScript: JS numbers are float64 and parseInt/Number have their own edge cases",
	Med, "parseInt/Number/Math.floor edge cases",
	func(p *Pass) {
		p.Inspect.Preorder([]ast.Node{(*ast.BinaryExpr)(nil), (*ast.CallExpr)(nil)}, func(n ast.Node) {
			if p.IsTest(n.Pos()) || !p.isPortFile(n.Pos()) {
				return
			}
			switch n := n.(type) {
			case *ast.BinaryExpr:
				if n.Op != token.QUO {
					return
				}
				if tv, ok := p.TypesInfo.Types[n]; ok && tv.Value == nil {
					if b, ok := tv.Type.Underlying().(*types.Basic); ok && b.Info()&types.IsInteger != 0 {
						p.Reportf(n.Pos(), "integer division truncates; JS / is float64 division")
					}
				}
			case *ast.CallExpr:
				if tv, ok := p.TypesInfo.Types[n.Fun]; ok && tv.IsType() && len(n.Args) == 1 {
					to, _ := tv.Type.Underlying().(*types.Basic)
					from, _ := p.TypesInfo.TypeOf(n.Args[0]).Underlying().(*types.Basic)
					if to != nil && from != nil && to.Info()&types.IsInteger != 0 && from.Info()&types.IsFloat != 0 && p.TypesInfo.Types[n.Args[0]].Value == nil {
						p.Reportf(n.Pos(), "float-to-integer conversion: NaN, Infinity and out-of-range values differ from JS")
					}
					return
				}
				for _, name := range []string{"Atoi", "ParseInt", "ParseUint", "ParseFloat"} {
					if p.IsFunc(n, "strconv", name) {
						p.Reportf(n.Pos(), "strconv.%s is stricter than JS parseInt/parseFloat/Number", name)
					}
				}
			}
		})
	})

// Clock flags time.Now in a package that already has an injectable clock.
var Clock = newCheck("clock",
	"time.Now() in a package that declares a func() time.Time clock seam: use the seam so logic stays testable and ordered like Pi's injected clock",
	Low, "durable reservation order; fake-clock tests",
	func(p *Pass) {
		hasSeam := false
		isClockType := func(t types.Type) bool {
			sig, ok := types.Unalias(t).Underlying().(*types.Signature)
			if !ok || sig.Params().Len() != 0 || sig.Results().Len() != 1 {
				return false
			}
			return NamedIs(sig.Results().At(0).Type(), "time", "Time")
		}
		for _, obj := range p.TypesInfo.Defs {
			switch o := obj.(type) {
			case *types.Var:
				if (o.IsField() || o.Parent() == p.Pkg.Scope()) && isClockType(o.Type()) && !strings.HasSuffix(p.Fset.Position(o.Pos()).Filename, "_test.go") {
					hasSeam = true
				}
			}
		}
		if !hasSeam {
			return
		}
		p.Inspect.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
			call := n.(*ast.CallExpr)
			if p.IsFunc(call, "time", "Now") && !p.IsTest(call.Pos()) {
				p.Reportf(call.Pos(), "time.Now() bypasses the package's injected clock")
			}
		})
	})
