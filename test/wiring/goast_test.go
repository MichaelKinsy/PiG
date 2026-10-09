package wiring

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/types/typeutil"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The checks in this package read each side of the extension wire contract from
// the code that implements it, so a method or variant added on one side and not
// the other fails here before it reaches a user. Public issue #201 is the class:
// every piece had Pi's shape, the interface ledger passed, and the production
// wiring between them was broken.

const module = "github.com/MichaelKinsy/PiG/"

var (
	loadOnce   sync.Once
	loaded     map[string]*packages.Package
	loadErr    error
	moduleRoot string
)

// loadPackages type-checks the production packages the checks read. Test files
// are excluded: a test-only call site proves nothing about production wiring.
func loadPackages(t *testing.T) map[string]*packages.Package {
	t.Helper()
	moduleRoot = testenv.ModuleRoot(t)
	loadOnce.Do(func() {
		cfg := &packages.Config{Dir: moduleRoot, Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo}
		var pkgs []*packages.Package
		pkgs, loadErr = packages.Load(cfg, "./ai", "./coding/extension", "./coding/extension/host/subprocess", "./extensions/sdk", "./internal/codingagent")
		loaded = map[string]*packages.Package{}
		for _, pkg := range pkgs {
			for _, err := range pkg.Errors {
				loadErr = err
			}
			loaded[strings.TrimPrefix(pkg.PkgPath, module)] = pkg
		}
	})
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	return loaded
}

func goPkg(t *testing.T, rel string) *goPackage {
	t.Helper()
	pkg := loadPackages(t)[rel]
	if pkg == nil {
		t.Fatalf("package %s not loaded", rel)
	}
	return &goPackage{Package: pkg}
}

type goPackage struct{ *packages.Package }

// position renders a node as a repository-relative file:line.
func (p *goPackage) position(pos token.Pos) string {
	at := p.Fset.Position(pos)
	rel, err := filepath.Rel(moduleRoot, at.Filename)
	if err != nil {
		rel = at.Filename
	}
	return filepath.ToSlash(rel) + ":" + strconv.Itoa(at.Line)
}

// named looks up a package-level type by name.
func (p *goPackage) named(t *testing.T, name string) *types.Named {
	t.Helper()
	obj, ok := p.Types.Scope().Lookup(name).(*types.TypeName)
	if !ok {
		t.Fatalf("%s has no type %s", p.PkgPath, name)
	}
	return obj.Type().(*types.Named)
}

// constString is the compile-time string value of expr, if it has one.
func (p *goPackage) constString(expr ast.Expr) (string, bool) {
	tv, ok := p.TypesInfo.Types[expr]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}

// stringValues resolves expr to every string it can hold in body: a constant,
// or a local variable assigned only constants there.
func (p *goPackage) stringValues(body ast.Node, expr ast.Expr) []string {
	if value, ok := p.constString(expr); ok {
		return []string{value}
	}
	ident, ok := ast.Unparen(expr).(*ast.Ident)
	if !ok || body == nil {
		return nil
	}
	target := p.TypesInfo.ObjectOf(ident)
	var values []string
	ast.Inspect(body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != len(assign.Rhs) {
			return true
		}
		for i, lhs := range assign.Lhs {
			if name, ok := lhs.(*ast.Ident); ok && p.TypesInfo.ObjectOf(name) == target {
				if value, ok := p.constString(assign.Rhs[i]); ok {
					values = append(values, value)
				}
			}
		}
		return true
	})
	return values
}

// funcs visits every function declaration body.
func (p *goPackage) funcs(visit func(fn *ast.FuncDecl, obj *types.Func)) {
	for _, file := range p.Syntax {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				obj, _ := p.TypesInfo.Defs[fn.Name].(*types.Func)
				visit(fn, obj)
			}
		}
	}
}

// inspect visits every node of every function body with its enclosing declaration.
func (p *goPackage) inspect(visit func(fn *ast.FuncDecl, obj *types.Func, node ast.Node)) {
	p.funcs(func(fn *ast.FuncDecl, obj *types.Func) {
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			if node != nil {
				visit(fn, obj, node)
			}
			return true
		})
	})
}

// callee is the declared function a call statically invokes, through generic instantiation.
func (p *goPackage) callee(call *ast.CallExpr) *types.Func {
	fn, ok := typeutil.Callee(p.TypesInfo, call).(*types.Func)
	if !ok {
		return nil
	}
	return fn.Origin()
}

// paramIndex is the index of the parameter v of fn, or -1.
func paramIndex(fn *types.Func, v types.Object) int {
	if fn == nil || v == nil {
		return -1
	}
	params := fn.Signature().Params()
	for i := range params.Len() {
		if params.At(i) == v {
			return i
		}
	}
	return -1
}

// isType reports whether typ is named, or a pointer to it.
func isType(typ types.Type, named *types.Named) bool {
	if ptr, ok := typ.(*types.Pointer); ok {
		typ = ptr.Elem()
	}
	n, ok := typ.(*types.Named)
	return ok && n.Origin() == named.Origin()
}

// methodSet records each name with the first source position that produced it.
type methodSet map[string]string

func (s methodSet) add(name, at string) {
	if _, ok := s[name]; !ok {
		s[name] = at
	}
}

func (s methodSet) sorted() []string { return slices.Sorted(maps.Keys(s)) }

func (s methodSet) has(name string) bool {
	_, ok := s[name]
	return ok
}
