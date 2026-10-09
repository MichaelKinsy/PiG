package wiring

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strings"
	"testing"
)

// taggedUnion is a closed Go union whose variants carry a wire tag: an
// interface with an unexported marker method and a Type() string method, as
// ai.AuthPrompt and ai.AuthEvent port Pi's discriminated unions.
type taggedUnion struct {
	iface    *types.Named
	variants map[*types.Named]string // variant -> tag
}

func (u taggedUnion) name() string { return u.iface.Obj().Pkg().Name() + "." + u.iface.Obj().Name() }

func (u taggedUnion) tags() []string {
	var tags []string
	for _, tag := range u.variants {
		tags = append(tags, tag)
	}
	slices.Sort(tags)
	return tags
}

// taggedUnions discovers every tagged union declared in the package.
func (p *goPackage) taggedUnions() []taggedUnion {
	scope := p.Types.Scope()
	var unions []taggedUnion
	for _, name := range scope.Names() {
		obj, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || !obj.Exported() {
			continue
		}
		named, ok := obj.Type().(*types.Named)
		if !ok {
			continue
		}
		iface, ok := named.Underlying().(*types.Interface)
		if !ok || !sealed(iface) {
			continue
		}
		union := taggedUnion{iface: named, variants: map[*types.Named]string{}}
		for _, other := range scope.Names() {
			candidate, ok := scope.Lookup(other).(*types.TypeName)
			if !ok || candidate.IsAlias() {
				continue
			}
			variant, ok := candidate.Type().(*types.Named)
			if !ok || types.IsInterface(variant) {
				continue
			}
			if types.Implements(variant, iface) || types.Implements(types.NewPointer(variant), iface) {
				union.variants[variant] = p.typeTag(variant)
			}
		}
		if len(union.variants) > 1 && !slices.Contains(union.tags(), "") {
			unions = append(unions, union)
		}
	}
	return unions
}

// sealed reports whether an interface has an unexported marker method and a Type() string method.
func sealed(iface *types.Interface) bool {
	marker, tagged := false, false
	for i := range iface.NumMethods() {
		method := iface.Method(i)
		sig := method.Signature()
		if !method.Exported() && sig.Params().Len() == 0 && sig.Results().Len() == 0 {
			marker = true
		}
		if method.Name() == "Type" && sig.Params().Len() == 0 && sig.Results().Len() == 1 && types.Identical(sig.Results().At(0).Type(), types.Typ[types.String]) {
			tagged = true
		}
	}
	return marker && tagged
}

// typeTag is the constant a variant's Type method returns.
func (p *goPackage) typeTag(variant *types.Named) string {
	tag := ""
	p.funcs(func(fn *ast.FuncDecl, obj *types.Func) {
		if obj == nil || obj.Name() != "Type" || obj.Signature().Recv() == nil {
			return
		}
		recv := obj.Signature().Recv().Type()
		if ptr, ok := recv.(*types.Pointer); ok {
			recv = ptr.Elem()
		}
		if named, ok := recv.(*types.Named); !ok || named.Obj() != variant.Obj() || len(fn.Body.List) != 1 {
			return
		}
		if ret, ok := fn.Body.List[0].(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
			tag, _ = p.constString(ret.Results[0])
		}
	})
	return tag
}

// unionsCrossingTheBoundary are the tagged unions of the packages an extension's wire values decode into.
func unionsCrossingTheBoundary(t *testing.T) []taggedUnion {
	t.Helper()
	var unions []taggedUnion
	for _, rel := range []string{"ai", "coding/extension"} {
		unions = append(unions, goPkg(t, rel).taggedUnions()...)
	}
	names := make([]string, len(unions))
	for i, union := range unions {
		names[i] = union.name()
	}
	for _, required := range []string{"ai.AuthPrompt", "ai.AuthEvent"} {
		if !slices.Contains(names, required) {
			t.Fatalf("tagged union discovery lost %s (found %v); update this check", required, names)
		}
	}
	return unions
}

// handlerPackages are the packages that answer an extension's prompts and events.
var handlerPackages = []string{"ai", "coding/extension/host/subprocess", "internal/codingagent"}

// TestTypeSwitchesOnTaggedUnionsAreExhaustive: every type switch over a tagged
// union crossing the extension boundary handles every variant. A missing case
// silently drops the variant: no prompt opens, or an event never shows.
func TestTypeSwitchesOnTaggedUnionsAreExhaustive(t *testing.T) {
	unions := unionsCrossingTheBoundary(t)
	l := newLedger(t, "union-switch", nil)
	for _, rel := range handlerPackages {
		p := goPkg(t, rel)
		p.inspect(func(fn *ast.FuncDecl, _ *types.Func, node ast.Node) {
			sw, ok := node.(*ast.TypeSwitchStmt)
			if !ok {
				return
			}
			subject := typeSwitchSubject(sw)
			if subject == nil {
				return
			}
			subjectType := p.TypesInfo.TypeOf(subject)
			for _, union := range unions {
				if !types.Identical(subjectType, union.iface) {
					continue
				}
				covered := map[string]bool{}
				for _, stmt := range sw.Body.List {
					for _, expr := range stmt.(*ast.CaseClause).List {
						if named, ok := p.TypesInfo.TypeOf(expr).(*types.Named); ok {
							covered[union.variants[named]] = true
						}
					}
				}
				var missing []string
				for _, tag := range union.tags() {
					if !covered[tag] {
						missing = append(missing, tag)
					}
				}
				if len(missing) > 0 {
					at := p.position(sw.Pos())
					l.add(fmt.Sprintf("%s in %s", union.name(), fn.Name.Name), fmt.Sprintf("%s: the type switch over %s in %s drops variants %v", at, union.name(), fn.Name.Name, missing))
				}
			}
		})
	}
	l.finish()
}

func typeSwitchSubject(sw *ast.TypeSwitchStmt) ast.Expr {
	var assert *ast.TypeAssertExpr
	switch stmt := sw.Assign.(type) {
	case *ast.AssignStmt:
		assert, _ = stmt.Rhs[0].(*ast.TypeAssertExpr)
	case *ast.ExprStmt:
		assert, _ = stmt.X.(*ast.TypeAssertExpr)
	}
	if assert == nil {
		return nil
	}
	return assert.X
}

// TestWireTagDecodersAreExhaustive: code that decodes a tagged union from the
// wire by comparing its "type" string handles every tag of the union it
// compares, and a switch on the tag rejects an unknown tag with an error. A
// decoder that compares some tags and lets the rest fall through turns an
// unknown or unlisted variant into another one, as #201's native login turned
// every unlisted prompt into a text prompt and every unlisted event into
// progress.
func TestWireTagDecodersAreExhaustive(t *testing.T) {
	unions := unionsCrossingTheBoundary(t)
	l := newLedger(t, "union-decoder", nil)
	for _, rel := range handlerPackages {
		p := goPkg(t, rel)
		p.funcs(func(fn *ast.FuncDecl, _ *types.Func) {
			// Group tag comparisons by the string they read.
			compared := map[types.Object]map[string]token.Pos{}
			var switches []*ast.SwitchStmt
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.BinaryExpr:
					if node.Op != token.EQL && node.Op != token.NEQ {
						return true
					}
					for _, pair := range [][2]ast.Expr{{node.X, node.Y}, {node.Y, node.X}} {
						if obj := tagReader(p, pair[0]); obj != nil {
							if value, ok := p.constString(pair[1]); ok {
								if compared[obj] == nil {
									compared[obj] = map[string]token.Pos{}
								}
								compared[obj][value] = node.Pos()
							}
						}
					}
				case *ast.SwitchStmt:
					if node.Tag != nil && tagReader(p, node.Tag) != nil {
						switches = append(switches, node)
						obj := tagReader(p, node.Tag)
						for _, stmt := range node.Body.List {
							for _, expr := range stmt.(*ast.CaseClause).List {
								if value, ok := p.constString(expr); ok {
									if compared[obj] == nil {
										compared[obj] = map[string]token.Pos{}
									}
									compared[obj][value] = expr.Pos()
								}
							}
						}
					}
				}
				return true
			})
			for _, union := range unions {
				for obj, values := range compared {
					var hit []string
					var at token.Pos
					for value, pos := range values {
						if slices.Contains(union.tags(), value) {
							hit = append(hit, value)
							if at == token.NoPos || pos < at {
								at = pos
							}
						}
					}
					// One shared tag (Pi's "select" names a dialog and a view event) is not a decoder of the union.
					if len(hit) < 2 {
						continue
					}
					var missing []string
					for _, tag := range union.tags() {
						if !slices.Contains(hit, tag) {
							missing = append(missing, tag)
						}
					}
					if len(missing) > 0 {
						l.add(fmt.Sprintf("%s in %s", union.name(), fn.Name.Name), fmt.Sprintf("%s: %s decodes %s by its %s tag but never compares %v, so those variants fall through as another", p.position(at), fn.Name.Name, union.name(), obj.Name(), missing))
					}
				}
			}
			for _, sw := range switches {
				obj := tagReader(p, sw.Tag)
				var hit int
				for value := range compared[obj] {
					for _, union := range unions {
						if slices.Contains(union.tags(), value) {
							hit++
						}
					}
				}
				if hit >= 2 && !rejectsDefault(sw) {
					l.add(fmt.Sprintf("default in %s", fn.Name.Name), fmt.Sprintf("%s: the tag switch in %s accepts an unknown tag; its default must return an error", p.position(sw.Pos()), fn.Name.Name))
				}
			}
		})
	}
	l.finish()
}

// tagReader returns the object a wire tag is read from: a string field or
// variable named type, kind or tag.
func tagReader(p *goPackage, expr ast.Expr) types.Object {
	var ident *ast.Ident
	switch expr := ast.Unparen(expr).(type) {
	case *ast.Ident:
		ident = expr
	case *ast.SelectorExpr:
		ident = expr.Sel
	default:
		return nil
	}
	obj := p.TypesInfo.ObjectOf(ident)
	if obj == nil || !types.Identical(obj.Type(), types.Typ[types.String]) {
		return nil
	}
	switch strings.ToLower(obj.Name()) {
	case "type", "kind", "tag":
		return obj
	}
	return nil
}

// rejectsDefault reports whether a switch's default clause returns an error.
func rejectsDefault(sw *ast.SwitchStmt) bool {
	for _, stmt := range sw.Body.List {
		clause := stmt.(*ast.CaseClause)
		if clause.List != nil {
			continue
		}
		found := false
		ast.Inspect(clause, func(node ast.Node) bool {
			if ret, ok := node.(*ast.ReturnStmt); ok && len(ret.Results) > 0 {
				last := ret.Results[len(ret.Results)-1]
				if ident, ok := last.(*ast.Ident); !ok || ident.Name != "nil" {
					found = true
				}
			}
			return !found
		})
		return found
	}
	return false
}
