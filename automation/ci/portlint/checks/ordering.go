// SPDX-License-Identifier: MIT

package checks

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/tools/go/analysis"
)

// The ordering checks guard the extension boundary: the goroutines, fan-in and writers through which Pi's single event loop order is re-created by hand. Their markers must say why the order is not observable or who owns it.

// classedChecks name the checks whose `//portlint:allow` reason must start with a class, so the intent is machine-checkable.
var classedChecks = map[string]bool{}

// markerClassRe matches the reasons an ordering check accepts:
//
//	executor: <name>                    the statement implements jsloop or a writer
//	joined: <what joins it>             owned and joined; order unobservable
//	lifecycle: <what>                   cancel, close, wait, heartbeat; no callback order observable
//	upstream: <file>:<symbol>           Pi itself is unordered here
//	pig divergence (D<N>): <reason>     a numbered divergence owns the order
var markerClassRe = regexp.MustCompile(`^(?:(?:executor|joined|lifecycle):\s*\S|upstream:\s*[^\s:]+:\S|pig divergence \(D\d+\))`)

// newOrderingCheck registers an ordering analyzer, whose markers need a class.
func newOrderingCheck(name, doc string, sev Severity, incident string, fn func(*Pass)) *analysis.Analyzer {
	classedChecks[name] = true
	return newCheck(name, doc, sev, incident, fn)
}

// underPath reports whether rel is dir or below it.
func underPath(rel, dir string) bool { return rel == dir || strings.HasPrefix(rel, dir+"/") }

// orderingScope reports whether a production file touches the extension boundary: agent, the coding root package, coding/cli/rpc*.go, coding/extension, coding/mcpext, internal/codingagent ext_*.go and extension_*.go, chord/host, and the Go SDK module. internal/jsloop implements the primitives and is exempt. A package outside the module (analysistest fixtures) is in scope.
func orderingScope(pkgPath, file string) bool {
	if strings.HasSuffix(file, "_test.go") {
		return false
	}
	rel := strings.TrimSuffix(strings.TrimPrefix(pkgPath, modulePath), "_test")
	if rel == pkgPath {
		return true
	}
	base := filepath.Base(file)
	switch {
	case underPath(rel, "/internal/jsloop"), underPath(rel, "/extensions/sdk/internal/jsloop"):
		return false
	case underPath(rel, "/agent"), rel == "/coding", underPath(rel, "/coding/extension"), underPath(rel, "/coding/mcpext"), underPath(rel, "/chord/host"), underPath(rel, "/extensions/sdk"):
		return true
	case rel == "/coding/cli":
		return strings.HasPrefix(base, "rpc")
	case rel == "/internal/codingagent":
		return strings.HasPrefix(base, "ext_") || strings.HasPrefix(base, "extension_")
	}
	return false
}

// inOrderingScope reports whether the file holding pos is a production file in orderingScope.
func (p *Pass) inOrderingScope(pos token.Pos) bool {
	return orderingScope(p.Pkg.Path(), p.Fset.Position(pos).Filename)
}

// boundaryInterface reports whether t is an interface declared in agent or coding/extension: an extension or host interface.
func boundaryInterface(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return false
	}
	if _, ok := n.Underlying().(*types.Interface); !ok {
		return false
	}
	rel := strings.TrimPrefix(n.Obj().Pkg().Path(), modulePath)
	return underPath(rel, "/agent") || underPath(rel, "/coding/extension")
}

// callbackReason says why call hands control to code the caller does not own, or "". It flags an invoked func-typed variable, field or parameter that takes at least one argument and is not a context cancel function, and a method call on an interface declared in agent or coding/extension. A variable declared in [from, to) is a local helper, not a callback.
func (p *Pass) callbackReason(call *ast.CallExpr, from, to token.Pos) string {
	fun := ast.Unparen(call.Fun)
	if tv, ok := p.TypesInfo.Types[fun]; ok && (tv.IsType() || tv.IsBuiltin()) {
		return ""
	}
	if sel, ok := fun.(*ast.SelectorExpr); ok {
		if s := p.TypesInfo.Selections[sel]; s != nil && s.Kind() == types.MethodVal {
			if m, ok := s.Obj().(*types.Func); ok {
				if sig, _ := m.Type().(*types.Signature); sig != nil && sig.Recv() != nil && boundaryInterface(sig.Recv().Type()) {
					return "calls " + types.TypeString(sig.Recv().Type(), packageName) + "." + m.Name() + ", a method of an extension or host interface"
				}
			}
			return ""
		}
	}
	var id *ast.Ident
	switch f := fun.(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	default:
		return ""
	}
	v, ok := p.TypesInfo.Uses[id].(*types.Var)
	if !ok || (v.Pos() >= from && v.Pos() < to) {
		return ""
	}
	sig, ok := v.Type().Underlying().(*types.Signature)
	if !ok || sig.Params().Len() == 0 || NamedIs(v.Type(), "context", "CancelFunc") || NamedIs(v.Type(), "context", "CancelCauseFunc") {
		return ""
	}
	return "invokes the callback " + exprText(fun)
}

// packageName qualifies a type by its package name, as the source writes it.
func packageName(pkg *types.Package) string { return pkg.Name() }

// funcDecls maps each function and method of the package to its declaration.
func (p *Pass) funcDecls() map[*types.Func]*ast.FuncDecl {
	decls := map[*types.Func]*ast.FuncDecl{}
	for _, f := range p.Files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil {
				if fn, ok := p.TypesInfo.Defs[fd.Name].(*types.Func); ok {
					decls[fn] = fd
				}
			}
		}
	}
	return decls
}

// reachableDepth bounds how many static calls boundarygoroutine follows from a go statement.
const reachableDepth = 3

// calledCallback scans the calls in root and, through checkCall, in the bodies of the package's functions they call.
func (p *Pass) calledCallback(root ast.Node, from, to token.Pos, decls map[*types.Func]*ast.FuncDecl, seen map[*types.Func]bool, depth int) string {
	reason := ""
	ast.Inspect(root, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && reason == "" {
			reason = p.checkCall(call, from, to, decls, seen, depth)
		}
		return reason == ""
	})
	return reason
}

// checkCall returns the callbackReason of call, or of a call in the body of the package's function it invokes, up to reachableDepth levels below depth.
func (p *Pass) checkCall(call *ast.CallExpr, from, to token.Pos, decls map[*types.Func]*ast.FuncDecl, seen map[*types.Func]bool, depth int) string {
	if reason := p.callbackReason(call, from, to); reason != "" {
		return reason
	}
	if fn := p.Callee(call); fn != nil && depth < reachableDepth && !seen[fn] {
		if d := decls[fn]; d != nil {
			seen[fn] = true
			return p.calledCallback(d.Body, d.Body.Pos(), d.Body.End(), decls, seen, depth+1)
		}
	}
	return ""
}

// isConstructorName reports whether name is newFoo, NewFoo, new or New.
func isConstructorName(name string) bool {
	for _, prefix := range []string{"new", "New"} {
		if rest, ok := strings.CutPrefix(name, prefix); ok {
			return rest == "" || unicode.IsUpper([]rune(rest)[0])
		}
	}
	return false
}

// BoundaryGoroutine flags goroutines at the extension boundary whose order of effects is observable.
var BoundaryGoroutine = newOrderingCheck("boundarygoroutine",
	"go statement in an extension-boundary package whose body (up to three static calls deep) invokes a callback or a method of an extension or host interface, or that starts in a new*/New* function: Go cannot reproduce Pi's single-thread order from a bare goroutine; use jsloop.Loop, Lane or SettleGate, or mark a lifecycle or executor site",
	High, "#249: newNodeChannel started its IPC reader before the consumer registered its listener, so an early message was dropped",
	func(p *Pass) {
		var decls map[*types.Func]*ast.FuncDecl
		p.Inspect.WithStack([]ast.Node{(*ast.GoStmt)(nil)}, func(n ast.Node, push bool, stack []ast.Node) bool {
			g := n.(*ast.GoStmt)
			if !push || !p.inOrderingScope(g.Pos()) {
				return true
			}
			if name := constructorOf(stack); name != "" {
				p.Reportf(g.Pos(), "go statement in constructor %s starts work before the caller can register its consumer; start it after registration", name)
				return true
			}
			if decls == nil {
				decls = p.funcDecls()
			}
			seen := map[*types.Func]bool{}
			var reason string
			if lit, ok := ast.Unparen(g.Call.Fun).(*ast.FuncLit); ok {
				reason = p.calledCallback(lit.Body, lit.Body.Pos(), lit.Body.End(), decls, seen, 0)
			} else {
				reason = p.checkCall(g.Call, token.NoPos, token.NoPos, decls, seen, 0)
			}
			if reason != "" {
				p.Reportf(g.Pos(), "goroutine %s; its effects have no order Pi's single thread would give them", reason)
			}
			return true
		})
	})

// constructorOf names the enclosing new*/New* function declaration of a go statement, or "" when a function literal intervenes.
func constructorOf(stack []ast.Node) string {
	for i := len(stack) - 2; i >= 0; i-- {
		switch n := stack[i].(type) {
		case *ast.FuncLit:
			return ""
		case *ast.FuncDecl:
			if isConstructorName(n.Name.Name) {
				return n.Name.Name
			}
			return ""
		}
	}
	return ""
}
