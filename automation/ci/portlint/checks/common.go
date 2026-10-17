// SPDX-License-Identifier: MIT

// Package checks holds the porting anti-pattern analyzers behind `make port-lint`.
// Each analyzer flags one way a Go port can drift from the TypeScript behavior it
// maps. Every diagnostic carries the analyzer name as its category.
package checks

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// Severity ranks how likely a finding changes observable behavior against Pi.
type Severity string

// Severity values.
const (
	High Severity = "HIGH"
	Med  Severity = "MED"
	Low  Severity = "LOW"
)

// Check pairs an analyzer with its severity and the incident that motivated it.
type Check struct {
	Analyzer *analysis.Analyzer
	Severity Severity
	Incident string
}

// registry lists every check in report order.
var registry []Check

// All returns every check in report order.
func All() []Check { return registry }

// modulePath is the import path of the module the checks guard.
const modulePath = "github.com/MichaelKinsy/PiG"

// toolingDirs hold harnesses, build automation and examples. They never feed
// Pi-facing wire output, so the porting checks skip them. The test-hygiene
// checks still scan them.
var toolingDirs = []string{"/test/", "/automation/", "/examples/", "/internal/evals", "/durable/harness", "/internal/modelgen"}

// newCheck registers an analyzer over production and test code outside the tooling directories.
func newCheck(name, doc string, sev Severity, incident string, fn func(*Pass)) *analysis.Analyzer {
	return register(name, doc, sev, incident, false, fn)
}

// newToolingCheck registers an analyzer that also scans the tooling directories.
func newToolingCheck(name, doc string, sev Severity, incident string, fn func(*Pass)) *analysis.Analyzer {
	return register(name, doc, sev, incident, true, fn)
}

func isTooling(pkgPath string) bool {
	rel := strings.TrimPrefix(pkgPath, modulePath)
	if rel == pkgPath {
		return false
	}
	rel = strings.TrimSuffix(rel, "_test") + "/"
	for _, d := range toolingDirs {
		if strings.HasPrefix(rel, d) || strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}

func register(name, doc string, sev Severity, incident string, tooling bool, fn func(*Pass)) *analysis.Analyzer {
	a := &analysis.Analyzer{
		Name:     name,
		Doc:      doc,
		Requires: []*analysis.Analyzer{inspect.Analyzer},
		Run: func(p *analysis.Pass) (any, error) {
			if !tooling && isTooling(p.Pkg.Path()) {
				return nil, nil
			}
			fn(&Pass{Pass: p, Inspect: p.ResultOf[inspect.Analyzer].(*inspector.Inspector), check: name})
			return nil, nil
		},
	}
	registry = append(registry, Check{Analyzer: a, Severity: sev, Incident: incident})
	return a
}

// Pass is an analysis pass plus the helpers every check shares.
type Pass struct {
	*analysis.Pass
	Inspect *inspector.Inspector
	check   string
}

var allowRe = regexp.MustCompile(`^//\s*portlint:allow\s+(\S+)(?:\s+(.*\S))?\s*$`)

// Reportf reports a finding at pos unless the line, or the line above, carries
// `//portlint:allow <check> <reason>`. A marker with no reason is itself a finding.
func (p *Pass) Reportf(pos token.Pos, format string, args ...any) {
	file := p.fileOf(pos)
	if file == nil || isGenerated(file) {
		return
	}
	line := p.Fset.Position(pos).Line
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			cl := p.Fset.Position(c.Slash).Line
			if cl != line && cl != line-1 {
				continue
			}
			m := allowRe.FindStringSubmatch(c.Text)
			if m == nil || (m[1] != p.check && m[1] != "all") {
				continue
			}
			if m[2] == "" {
				p.Report(analysis.Diagnostic{Pos: c.Slash, Category: p.check, Message: "portlint:allow " + m[1] + " needs a reason"})
				return
			}
			return
		}
	}
	p.Report(analysis.Diagnostic{Pos: pos, Category: p.check, Message: sprintf(format, args...)})
}

func (p *Pass) fileOf(pos token.Pos) *ast.File {
	for _, f := range p.Files {
		if f.FileStart <= pos && pos <= f.FileEnd {
			return f
		}
	}
	return nil
}

func isGenerated(f *ast.File) bool {
	for _, cg := range f.Comments {
		if cg.Pos() > f.Package {
			break
		}
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "// Code generated") && strings.HasSuffix(c.Text, "DO NOT EDIT.") {
				return true
			}
		}
	}
	return false
}

// IsTest reports whether pos is in a _test.go file.
func (p *Pass) IsTest(pos token.Pos) bool {
	return strings.HasSuffix(p.Fset.Position(pos).Filename, "_test.go")
}

// FileName is the base name of the file containing pos.
func (p *Pass) FileName(pos token.Pos) string { return filepath.Base(p.Fset.Position(pos).Filename) }

// Callee resolves a call to the package-level function or method it invokes.
func (p *Pass) Callee(call *ast.CallExpr) *types.Func {
	var id *ast.Ident
	switch f := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	case *ast.IndexExpr:
		return p.calleeOf(f.X)
	case *ast.IndexListExpr:
		return p.calleeOf(f.X)
	default:
		return nil
	}
	fn, _ := p.TypesInfo.Uses[id].(*types.Func)
	return fn
}

func (p *Pass) calleeOf(x ast.Expr) *types.Func {
	switch f := ast.Unparen(x).(type) {
	case *ast.Ident:
		fn, _ := p.TypesInfo.Uses[f].(*types.Func)
		return fn
	case *ast.SelectorExpr:
		fn, _ := p.TypesInfo.Uses[f.Sel].(*types.Func)
		return fn
	}
	return nil
}

// IsFunc reports whether call invokes pkgPath.name (a package-level function).
func (p *Pass) IsFunc(call *ast.CallExpr, pkgPath, name string) bool {
	fn := p.Callee(call)
	if fn == nil || fn.Pkg() == nil || fn.Name() != name || fn.Pkg().Path() != pkgPath {
		return false
	}
	sig, _ := fn.Type().(*types.Signature)
	return sig != nil && sig.Recv() == nil
}

// IsMethod reports whether call invokes the method name on the named type pkgPath.typeName.
func (p *Pass) IsMethod(call *ast.CallExpr, pkgPath, typeName, name string) bool {
	fn := p.Callee(call)
	if fn == nil || fn.Name() != name {
		return false
	}
	sig, _ := fn.Type().(*types.Signature)
	if sig == nil || sig.Recv() == nil {
		return false
	}
	return NamedIs(sig.Recv().Type(), pkgPath, typeName)
}

// NamedIs reports whether t, after pointer indirection, is the named type pkgPath.name.
func NamedIs(t types.Type, pkgPath, name string) bool {
	if ptr, ok := types.Unalias(t).(*types.Pointer); ok {
		t = ptr.Elem()
	}
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return false
	}
	return n.Obj().Name() == name && n.Obj().Pkg().Path() == pkgPath
}

// StringLit returns the value of a string literal expression.
func StringLit(e ast.Expr) (string, bool) {
	lit, ok := ast.Unparen(e).(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := unquote(lit.Value)
	return s, err == nil
}

// Funcs calls fn for each function declaration and literal body with its ancestors' shared name.
func (p *Pass) Funcs(fn func(name string, typ *ast.FuncType, body *ast.BlockStmt)) {
	p.Inspect.Preorder([]ast.Node{(*ast.FuncDecl)(nil), (*ast.FuncLit)(nil)}, func(n ast.Node) {
		switch n := n.(type) {
		case *ast.FuncDecl:
			if n.Body != nil {
				fn(n.Name.Name, n.Type, n.Body)
			}
		case *ast.FuncLit:
			fn("", n.Type, n.Body)
		}
	})
}
