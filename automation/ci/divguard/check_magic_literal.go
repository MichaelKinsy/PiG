// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"math/big"
	"path"
	"regexp"
	"slices"
	"strings"
)

// magicLiteral flags numeric time.Duration literals and numeric cap literals
// (limits, sizes, retries, buffer and queue lengths) in the hot packages.
// Pi has very few of either, so each one is an invented behavior until a
// `// upstream: <file>:<symbol>` comment shows where Pi uses the same value or
// a `// pig divergence (D<N>)` marker records it. It is how the silent
// `opts.MaxTurns = 100` default would have been caught.
const magicLiteralName = "magic-literal"

var magicLiteral = check{
	Name:    magicLiteralName,
	Doc:     "time.Duration or cap literal in a hot package with no upstream reference and no DN marker",
	Applies: func(rel string) bool { return inHot(rel) && !additiveOnly(rel) && !testSupport(rel) },
	Run:     runMagicLiteral,
}

// capNameRe matches identifiers that name a limit, size, count or timing.
// Max and Min count only as a word prefix (MaxTurns, maxBytes), so an enum
// member such as ThinkingMax is not a limit.
var capNameRe = regexp.MustCompile(`^(?:[Mm]ax|[Mm]in)$|(?:^|[a-z0-9_])(?:[Mm]ax|[Mm]in)[A-Z0-9_]|(?i:limit|cap(?:acity)?$|timeout|deadline|interval|delay|backoff|retr(?:y|ies)|attempts|budget|threshold|depth|buf(?:fer)?(?:size|len)?$|size$|turns|lines$|bytes$|debounce|throttle|heartbeat|grace|ttl|quota|window)`)

var durationUnits = map[string]int64{
	"Nanosecond": 1, "Microsecond": 1e3, "Millisecond": 1e6, "Second": 1e9, "Minute": 60e9, "Hour": 3600e9,
}

// additiveOnly reports packages that implement a PiG-only feature with no Pi
// counterpart at all (D18 Piglets), so no literal there diverges from Pi.
func additiveOnly(rel string) bool {
	return strings.HasPrefix(rel, "coding/piglet/") || strings.HasPrefix(rel, "coding/pigletbuild/")
}

// testSupport reports non-_test.go files that only tests and the parity
// harness use.
func testSupport(rel string) bool {
	base := path.Base(rel)
	return strings.Contains(rel, "/testing/") || strings.HasPrefix(base, "test_") || base == "parity_harness.go"
}

// isDrain reports `io.Copy(io.Discard, io.LimitReader(body, n))`, which
// drains a response body for connection reuse and discards the bytes.
func isDrain(stack []ast.Node) bool {
	if len(stack) == 0 {
		return false
	}
	call, ok := stack[len(stack)-1].(*ast.CallExpr)
	if !ok || !isPkgCall(call, "io", "Copy") || len(call.Args) != 2 {
		return false
	}
	sel, ok := call.Args[0].(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Discard"
}

// readerGrowsPastBuffer reports a NewReaderSize result whose enclosing
// function consumes ReadSlice chunks and handles ErrBufferFull. Its size is a
// chunk allocation, not an input cap, because the caller accumulates every
// full chunk before continuing.
func readerGrowsPastBuffer(call *ast.CallExpr, stack []ast.Node) bool {
	var reader string
	if len(stack) > 0 {
		if as, ok := stack[len(stack)-1].(*ast.AssignStmt); ok {
			for i, rhs := range as.Rhs {
				if rhs == call && i < len(as.Lhs) {
					reader = exprName(as.Lhs[i])
				}
			}
		}
	}
	if reader == "" {
		return false
	}
	var scope ast.Node
	for _, s := range slices.Backward(stack) {
		switch s.(type) {
		case *ast.FuncDecl, *ast.FuncLit:
			scope = s
		}
		if scope != nil {
			break
		}
	}
	if scope == nil {
		return false
	}
	hasReadSlice, handlesFull := false, false
	ast.Inspect(scope, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok {
			hasReadSlice = hasReadSlice || id.Name == reader && sel.Sel.Name == "ReadSlice"
			handlesFull = handlesFull || id.Name == "bufio" && sel.Sel.Name == "ErrBufferFull"
		}
		return true
	})
	return hasReadSlice && handlesFull
}

// sizeCalls take a size or limit as their argument at the given index.
var sizeCalls = map[string]int{"Buffer": 1, "LimitReader": 1, "NewReaderSize": 1, "NewWriterSize": 1}

func runMagicLiteral(fc *fileCtx) []Hit {
	var hits []Hit
	done := map[ast.Node]bool{}
	var stack []ast.Node
	report := func(lit ast.Expr, kind string, values []*big.Int) {
		if done[lit] {
			return
		}
		done[lit] = true
		expr := ""
		if isConstLit(lit) {
			expr = exprText(fc, lit)
		}
		implicit := spellings(values, expr)
		explicit := implicit
		if f := compoundFactor(lit); f != nil {
			explicit = append(slices.Clone(implicit), exprSpellingRe(exprText(fc, f)))
		}
		if fc.upstreamAllowed(fc.markerLines(lit.Pos(), stack), implicit, explicit) {
			return
		}
		if h, ok := fc.hit(magicLiteralName, lit.Pos(), stack, kind+" "+exprText(fc, lit)+" has no `// upstream:` reference and no DN marker"); ok {
			hits = append(hits, h)
		}
	}
	capAssign := func(lhs, rhs ast.Expr) {
		if !capNameRe.MatchString(exprName(lhs)) || !isConstLit(rhs) {
			return
		}
		if v := constValue(rhs); bigAtLeast(v, 2) {
			report(rhs, "cap "+exprName(lhs)+" =", intValues(v))
		}
	}
	inspect(fc.File, func(n ast.Node, s []ast.Node) bool {
		stack = s
		switch n := n.(type) {
		case *ast.BinaryExpr:
			if n.Op == token.MUL && !isOffset(stack) {
				if u, ok := durationUnit(n.Y); ok && isConstLit(n.X) {
					report(n, "duration", durationValues(constValue(n.X), u))
					return false
				}
				if u, ok := durationUnit(n.X); ok && isConstLit(n.Y) {
					report(n, "duration", durationValues(constValue(n.Y), u))
					return false
				}
			}
			if isRelational(n.Op) {
				if lit, other := constSide(n); lit != nil && capNameRe.MatchString(exprName(other)) && bigAtLeast(constValue(lit), 2) {
					report(lit, "cap", intValues(constValue(lit)))
				}
			}
		case *ast.CallExpr:
			if isPkgCall(n, "time", "Duration") && len(n.Args) == 1 && isConstLit(n.Args[0]) {
				if v := constValue(n.Args[0]); bigAtLeast(v, 1) {
					report(n, "duration", durationValues(v, 1))
				}
				return false
			}
			if calleeName(n) == "NewReaderSize" && readerGrowsPastBuffer(n, stack) {
				return true
			}
			if i, ok := sizeCalls[calleeName(n)]; ok && i < len(n.Args) && isConstLit(n.Args[i]) && bigAtLeast(constValue(n.Args[i]), 2) && !isDrain(stack) {
				report(n.Args[i], "size", intValues(constValue(n.Args[i])))
			}
		case *ast.SelectorExpr:
			if u, ok := durationUnit(n); ok && !unitIsScale(stack) {
				report(n, "duration", durationValues(big.NewInt(1), u))
			}
		case *ast.AssignStmt:
			if len(n.Lhs) == len(n.Rhs) {
				for i, lhs := range n.Lhs {
					capAssign(lhs, n.Rhs[i])
				}
			}
		case *ast.ValueSpec:
			for i, name := range n.Names {
				if i < len(n.Values) {
					capAssign(name, n.Values[i])
				}
			}
		case *ast.KeyValueExpr:
			capAssign(n.Key, n.Value)
		}
		return true
	})
	return hits
}

// upstreamAllowed reports whether an `// upstream:` comment on one of lines
// names a mirror file that contains one of values, or whether one of
// implicit appears in an upstream file PORT_MAP.md maps to this Go file. A
// reference to a missing file or symbol is recorded as a problem.
func (fc *fileCtx) upstreamAllowed(lines []int, implicit, values []*regexp.Regexp) bool {
	if fc.Env.Upstream.mappedValue(fc.Env.PortMap[fc.Rel], implicit) {
		return true
	}
	var problems []string
	for _, ln := range lines {
		for _, c := range fc.commentsNear(ln) {
			for _, m := range upstreamRefRe.FindAllStringSubmatch(c, -1) {
				ok, err := fc.Env.Upstream.verifyRef(m[1], m[2], values)
				if ok {
					return true
				}
				if err != nil {
					problems = append(problems, fmt.Sprintf("%s:%d: `upstream: %s`: %v", fc.Rel, ln, strings.TrimSuffix(m[1]+":"+m[2], ":"), err))
				}
			}
		}
	}
	for _, p := range problems {
		fc.problem(p)
	}
	return false
}

// compoundFactor returns the literal factor of a duration such as
// `7 * 24 * time.Hour` when it is itself an expression, so an explicit
// reference may match upstream's `7 * 24 * 60 * 60 * 1000` by its prefix.
func compoundFactor(e ast.Expr) ast.Expr {
	b, ok := e.(*ast.BinaryExpr)
	if !ok || b.Op != token.MUL {
		return nil
	}
	f := b.X
	if _, unit := durationUnit(b.X); unit {
		f = b.Y
	}
	if inner, ok := f.(*ast.BinaryExpr); ok && isConstLit(inner) {
		return f
	}
	return nil
}

func durationUnit(e ast.Expr) (int64, bool) {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return 0, false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != "time" {
		return 0, false
	}
	u, ok := durationUnits[sel.Sel.Name]
	return u, ok
}

// unitIsScale reports whether a bare time unit converts or scales a
// non-literal value (`time.Duration(ms) * time.Millisecond`, `d /
// time.Second`, `float64(time.Second)`, `d.Round(time.Millisecond)`) rather
// than being a duration value itself.
func unitIsScale(stack []ast.Node) bool {
	if len(stack) == 0 {
		return false
	}
	switch p := stack[len(stack)-1].(type) {
	case *ast.BinaryExpr:
		return true
	case *ast.CallExpr:
		switch calleeName(p) {
		case "Round", "Truncate", "Duration", "float64", "int64", "int", "uint64":
			return true
		}
	}
	return false
}

// isOffset reports a duration added to or subtracted from a non-literal
// value, such as the `999*time.Millisecond` of a round-up, which is
// arithmetic rather than a limit.
func isOffset(stack []ast.Node) bool {
	if len(stack) == 0 {
		return false
	}
	p, ok := stack[len(stack)-1].(*ast.BinaryExpr)
	return ok && (p.Op == token.ADD || p.Op == token.SUB) && (!isConstLit(p.X) || !isConstLit(p.Y))
}

func isRelational(op token.Token) bool {
	return op == token.LSS || op == token.LEQ || op == token.GTR || op == token.GEQ || op == token.EQL
}

func constSide(b *ast.BinaryExpr) (ast.Expr, ast.Expr) {
	if isConstLit(b.Y) && !isConstLit(b.X) {
		return b.Y, b.X
	}
	if isConstLit(b.X) && !isConstLit(b.Y) {
		return b.X, b.Y
	}
	return nil, nil
}

// isConstLit reports a numeric constant built only from literals.
func isConstLit(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.BasicLit:
		return t.Kind == token.INT || t.Kind == token.FLOAT
	case *ast.ParenExpr:
		return isConstLit(t.X)
	case *ast.UnaryExpr:
		return t.Op == token.SUB && isConstLit(t.X)
	case *ast.BinaryExpr:
		switch t.Op {
		case token.ADD, token.SUB, token.MUL, token.QUO, token.SHL:
			return isConstLit(t.X) && isConstLit(t.Y)
		}
	}
	return false
}

// constValue evaluates a literal expression to an integer, or nil when it
// is not integral.
func constValue(e ast.Expr) *big.Int {
	v := evalConst(e)
	if v == nil {
		return nil
	}
	if v.Kind() == constant.Float {
		v = constant.ToInt(v)
	}
	if v.Kind() != constant.Int {
		return nil
	}
	n, ok := new(big.Int).SetString(v.ExactString(), 10)
	if !ok {
		return nil
	}
	return n
}

func evalConst(e ast.Expr) constant.Value {
	switch t := e.(type) {
	case *ast.BasicLit:
		return constant.MakeFromLiteral(t.Value, t.Kind, 0)
	case *ast.ParenExpr:
		return evalConst(t.X)
	case *ast.UnaryExpr:
		x := evalConst(t.X)
		if x == nil {
			return nil
		}
		return constant.UnaryOp(t.Op, x, 0)
	case *ast.BinaryExpr:
		x, y := evalConst(t.X), evalConst(t.Y)
		if x == nil || y == nil {
			return nil
		}
		if t.Op == token.SHL {
			s, ok := constant.Uint64Val(y)
			if !ok {
				return nil
			}
			return constant.Shift(x, token.SHL, uint(s))
		}
		if t.Op == token.QUO && x.Kind() == constant.Int && y.Kind() == constant.Int {
			return constant.BinaryOp(x, token.QUO_ASSIGN, y)
		}
		return constant.BinaryOp(x, t.Op, y)
	}
	return nil
}

// bigAtLeast reports v >= n. JavaScript and wire-format constants of the
// form 2^k-1 with k >= 31 (Number.MAX_SAFE_INTEGER, the setTimeout ceiling,
// UUID field masks) are language facts, not invented limits, and never count.
func bigAtLeast(v *big.Int, n int64) bool {
	if v == nil || v.Cmp(big.NewInt(n)) < 0 {
		return false
	}
	next := new(big.Int).Add(v, big.NewInt(1))
	return next.BitLen() < 32 || next.Cmp(new(big.Int).Lsh(big.NewInt(1), uint(next.BitLen()-1))) != 0
}

func intValues(v *big.Int) []*big.Int {
	if v == nil {
		return nil
	}
	return []*big.Int{v}
}

// durationValues lists the spellings upstream may use for n units of unit
// nanoseconds: milliseconds (setTimeout), seconds, minutes and hours.
func durationValues(n *big.Int, unit int64) []*big.Int {
	if n == nil {
		return nil
	}
	ns := new(big.Int).Mul(n, big.NewInt(unit))
	var out []*big.Int
	for _, div := range []int64{1e6, 1e9, 60e9, 3600e9} {
		q, r := new(big.Int).QuoRem(ns, big.NewInt(div), new(big.Int))
		if r.Sign() == 0 && q.Sign() > 0 {
			out = append(out, q)
		}
	}
	return out
}

func exprText(fc *fileCtx, e ast.Expr) string {
	start, end := fc.Fset.Position(e.Pos()).Offset, fc.Fset.Position(e.End()).Offset
	if start < 0 || end > len(fc.Src) || start >= end {
		return "literal"
	}
	return string(fc.Src[start:end])
}
