// SPDX-License-Identifier: MIT

package checks

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"math"
	"regexp"
	"strings"

	"golang.org/x/tools/go/ssa"
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

// FusedMulAdd flags a float product that reaches an add or subtract unrounded. arm64 (the macOS runners), ppc64le, s390x
// and riscv64 fuse x*y+z into one FMA rounding and amd64 does not; JavaScript never fuses. The Go spec permits the fusion
// "possibly across statements", and Go 1.27 does fuse through a local variable and through an inlined return value, so
// the check follows the operand's SSA definition: a product written in the expression, a local variable whose reaching
// assignment is a product, or a call to a same-package function that returns one. Only an explicit float64()/float32()
// conversion forces the rounding. A product with a power-of-two constant factor is exact and exempt; integer-valued
// exactness is not, so such a site carries a //portlint:allow with the argument.
var FusedMulAdd = newCheck("fusedmuladd",
	"float x*y+z, x*y-z or z+=x*y, also through a local variable or a same-package function's return value: FMA architectures fuse it into one rounding and JavaScript never does; wrap the product in float64()",
	High, "fuzzy.ts score += i * 0.1: macOS arm64 scored -1.9999999999999993 where Pi scored -1.9999999999999996; review-ca-120c: cache-warmer expectedSavings, OAuth expiry and easter-egg smooth() fused across statements and inlined returns",
	func(p *Pass) {
		// A test variant recompiles the production files the package's own pass already checks; skip it rather than
		// build their SSA form twice.
		for _, f := range p.Files {
			if p.IsTest(f.Pos()) {
				return
			}
		}
		rounded := map[token.Pos]bool{}  // OpPos of every product an explicit float conversion rounds
		adds := map[token.Pos]ast.Node{} // SSA position of a float add or subtract: a BinaryExpr's OpPos, an op-assign's Pos
		unrounded := false               // the package multiplies floats without a conversion
		p.Inspect.WithStack([]ast.Node{(*ast.BinaryExpr)(nil), (*ast.AssignStmt)(nil)}, func(n ast.Node, push bool, stack []ast.Node) bool {
			if !push {
				return true
			}
			switch n := n.(type) {
			case *ast.BinaryExpr:
				switch n.Op {
				case token.MUL:
					if call, ok := convertingParent(stack); ok && p.isFloatConversion(call) {
						rounded[n.OpPos] = true
					} else if !unrounded && p.isFloatNonConst(n) {
						unrounded = true
					}
				case token.ADD, token.SUB:
					adds[n.OpPos] = n
				}
			case *ast.AssignStmt:
				if len(n.Lhs) != 1 || len(n.Rhs) != 1 {
					break
				}
				switch n.Tok {
				case token.ADD_ASSIGN, token.SUB_ASSIGN:
					adds[n.Pos()] = n
				case token.MUL_ASSIGN:
					unrounded = unrounded || p.isFloatNonConst(n.Lhs[0])
				}
			}
			return true
		})
		// Every finding needs an unrounded product in this package, so the SSA form, the costly part, is built only then.
		if !unrounded {
			return
		}
		for _, fn := range p.ssaFuncs() {
			for _, b := range fn.Blocks {
				for _, instr := range b.Instrs {
					bin, ok := instr.(*ssa.BinOp)
					if !ok || (bin.Op != token.ADD && bin.Op != token.SUB) || !isFloatType(bin.Type()) {
						continue
					}
					if node := adds[bin.Pos()]; node != nil {
						p.reportFusedOperand(node, bin, rounded)
					}
				}
			}
		}
	})

// ssaFuncs builds the package's SSA form and returns its source functions, function literals and package initializer.
// It builds the form itself rather than requiring buildssa, whose ctrlflow facts and per-package programs the driver
// would compute and retain for every package it loads.
func (p *Pass) ssaFuncs() []*ssa.Function {
	prog := ssa.NewProgram(p.Fset, 0)
	for _, imp := range p.Pkg.Imports() {
		prog.CreatePackage(imp, nil, nil, true)
	}
	pkg := prog.CreatePackage(p.Pkg, p.Files, p.TypesInfo, false)
	pkg.Build()
	var funcs []*ssa.Function
	var add func(*ssa.Function)
	add = func(fn *ssa.Function) {
		funcs = append(funcs, fn)
		for _, anon := range fn.AnonFuncs {
			add(anon)
		}
	}
	add(pkg.Func("init"))
	for _, f := range p.Files {
		for _, decl := range f.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok {
				if obj, ok := p.TypesInfo.Defs[fd.Name].(*types.Func); ok {
					if fn := prog.FuncValue(obj); fn != nil {
						add(fn)
					}
				}
			}
		}
	}
	return funcs
}

// reportFusedOperand reports the add or subtract node when either SSA operand of bin is an unrounded float product.
func (p *Pass) reportFusedOperand(node ast.Node, bin *ssa.BinOp, rounded map[token.Pos]bool) {
	feeds := "an add or subtract"
	var x, y ast.Expr
	switch n := node.(type) {
	case *ast.BinaryExpr:
		x, y = n.X, n.Y
	case *ast.AssignStmt:
		feeds, x, y = "+= or -=", n.Lhs[0], n.Rhs[0]
	}
	for _, side := range []struct {
		v ssa.Value
		e ast.Expr
	}{{bin.X, x}, {bin.Y, y}} {
		// go/ssa drops a conversion between identical types, so the source operand decides whether one rounds it.
		if p.isFloatConversion(side.e) {
			continue
		}
		mul, via := p.unroundedProduct(side.v, rounded, map[*ssa.Function]bool{})
		if mul == nil {
			continue
		}
		switch {
		case via != nil:
			p.Reportf(node.Pos(), "a float product feeds %s through the return value of %s: an inlined return fuses (FMA) on arm64; wrap the returned product in float64()", feeds, via.Name())
		case isOperand(side.e, mul.Pos()):
			p.Reportf(node.Pos(), "a float product feeds %s: arm64 fuses it (FMA); wrap the product in float64()", feeds)
		default:
			p.Reportf(node.Pos(), "a float product feeds %s through a variable assigned at line %d: Go fuses (FMA) across statements; wrap the product in float64() where it is assigned", feeds, p.Fset.Position(mul.Pos()).Line)
		}
		return
	}
}

// unroundedProduct returns the float multiplication v holds, looking through a negation and through the single result
// of a same-package function, which the compiler may inline; via names that function. A product an explicit conversion
// rounds, or with a power-of-two constant factor (an exact product), does not count.
func (p *Pass) unroundedProduct(v ssa.Value, rounded map[token.Pos]bool, seen map[*ssa.Function]bool) (mul *ssa.BinOp, via *ssa.Function) {
	if neg, ok := v.(*ssa.UnOp); ok && neg.Op == token.SUB {
		v = neg.X
	}
	switch v := v.(type) {
	case *ssa.BinOp:
		if v.Op == token.MUL && isFloatType(v.Type()) && !rounded[v.Pos()] && !isPowerOfTwo(v.X) && !isPowerOfTwo(v.Y) {
			return v, nil
		}
	case *ssa.Call:
		callee := v.Call.StaticCallee()
		if callee == nil || seen[callee] || callee.Pkg == nil || callee.Pkg.Pkg != p.Pkg {
			return nil, nil
		}
		seen[callee] = true
		for _, b := range callee.Blocks {
			ret, ok := b.Instrs[len(b.Instrs)-1].(*ssa.Return)
			if !ok || len(ret.Results) != 1 {
				continue
			}
			if mul, _ := p.unroundedProduct(ret.Results[0], rounded, seen); mul != nil {
				return mul, callee
			}
		}
	}
	return nil, nil
}

// convertingParent returns the call enclosing the top of stack through parentheses and unary signs.
func convertingParent(stack []ast.Node) (*ast.CallExpr, bool) {
	for i := len(stack) - 2; i >= 0; i-- {
		switch n := stack[i].(type) {
		case *ast.ParenExpr:
		case *ast.UnaryExpr:
			if n.Op != token.SUB && n.Op != token.ADD {
				return nil, false
			}
		case *ast.CallExpr:
			return n, true
		default:
			return nil, false
		}
	}
	return nil, false
}

// isFloatConversion reports whether e is an explicit conversion to a float type, which rounds its operand.
func (p *Pass) isFloatConversion(e ast.Expr) bool {
	call, ok := ast.Unparen(e).(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	tv, ok := p.TypesInfo.Types[call.Fun]
	return ok && tv.IsType() && isFloatType(tv.Type)
}

// isOperand reports whether e is, through parentheses and a sign, the product whose operator is at pos.
func isOperand(e ast.Expr, pos token.Pos) bool {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.UnaryExpr:
			e = x.X
		case *ast.BinaryExpr:
			return x.OpPos == pos
		default:
			return false
		}
	}
}

// isFloatNonConst reports whether e is a non-constant expression of a float type.
func (p *Pass) isFloatNonConst(e ast.Expr) bool {
	tv, ok := p.TypesInfo.Types[e]
	return ok && tv.Value == nil && isFloatType(tv.Type)
}

func isFloatType(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsFloat != 0
}

// isPowerOfTwo reports whether v is a float constant ±2^k: multiplying by it only shifts the exponent, so it is exact.
func isPowerOfTwo(v ssa.Value) bool {
	c, ok := v.(*ssa.Const)
	if !ok || c.Value == nil {
		return false
	}
	f, _ := constant.Float64Val(constant.ToFloat(c.Value))
	frac, _ := math.Frexp(f)
	return frac == 0.5 || frac == -0.5
}
