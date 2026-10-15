// SPDX-License-Identifier: MIT

package checks

import (
	"go/ast"
	"go/types"
	"slices"
)

// regexpCompilers are the constructors that compile a pattern when they are called.
var regexpCompilers = map[string]map[string]bool{
	"regexp":                            {"Compile": true, "MustCompile": true, "CompilePOSIX": true, "MustCompilePOSIX": true},
	modulePath + "/internal/lazyregexp": {"New": true, "MustCompile": true},
}

// RegexpInFunc flags a constant pattern compiled inside a function body: every call compiles it again.
var RegexpInFunc = newCheck("regexpinfunc",
	"a constant regular expression compiled inside a function body: every call recompiles it; hoist it to a package-level variable (lazyregexp for a cold path) or a sync.OnceValue",
	Low, "go-idioms: per-call regexp.MustCompile in ai/validate_formats.go and coding/cli/chalk_color.go",
	func(p *Pass) {
		p.Inspect.WithStack([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node, push bool, stack []ast.Node) bool {
			if !push || p.IsTest(n.Pos()) {
				return true
			}
			call := n.(*ast.CallExpr)
			fn := p.Callee(call)
			if fn == nil || fn.Pkg() == nil || len(call.Args) == 0 || !regexpCompilers[fn.Pkg().Path()][fn.Name()] {
				return true
			}
			if tv, ok := p.TypesInfo.Types[call.Args[0]]; !ok || tv.Value == nil {
				return true
			}
			if p.compiledOnce(stack) {
				return true
			}
			p.Reportf(call.Pos(), "%s.%s of a constant pattern inside a function compiles it on every call", fn.Pkg().Name(), fn.Name())
			return true
		})
	})

// compiledOnce reports whether the innermost function around the top of stack runs once: a package-level initializer, init, or a
// function literal handed to sync.OnceValue, sync.OnceValues, sync.OnceFunc or (*sync.Once).Do.
func (p *Pass) compiledOnce(stack []ast.Node) bool {
	for i, s := range slices.Backward(stack) {
		switch fn := s.(type) {
		case *ast.FuncDecl:
			return fn.Recv == nil && fn.Name.Name == "init"
		case *ast.FuncLit:
			if i == 0 {
				return false
			}
			call, ok := stack[i-1].(*ast.CallExpr)
			if !ok {
				return false
			}
			if p.IsFunc(call, "sync", "OnceValue") || p.IsFunc(call, "sync", "OnceValues") || p.IsFunc(call, "sync", "OnceFunc") {
				return true
			}
			callee := p.Callee(call)
			return callee != nil && callee.Name() == "Do" && isSyncOnceMethod(callee)
		}
	}
	return true
}

func isSyncOnceMethod(fn *types.Func) bool {
	sig, _ := fn.Type().(*types.Signature)
	if sig == nil || sig.Recv() == nil {
		return false
	}
	return NamedIs(sig.Recv().Type(), "sync", "Once")
}

// jsonEncoders and jsonDecoders are the byte-level JSON codecs a round trip pairs.
var (
	jsonEncoders = map[string]map[string]bool{
		"encoding/json":                      {"Marshal": true, "MarshalIndent": true},
		"encoding/json/v2":                   {"Marshal": true},
		sdkJSONPath:                          {"Marshal": true, "MarshalIndent": true},
		modulePath + "/internal/jsstring":    {"MarshalJSON": true},
		modulePath + "/internal/orderedjson": {"Marshal": true, "MarshalMap": true, "MarshalArray": true},
	}
	jsonDecoders = map[string]map[string]bool{
		"encoding/json":    {"Unmarshal": true, "NewDecoder": true},
		"encoding/json/v2": {"Unmarshal": true},
		sdkJSONPath:        {"Unmarshal": true, "NewDecoder": true},
	}
)

// sdkJSONPath is the extension SDK's copy of encoding/json, which the host and the session code share.
const sdkJSONPath = modulePath + "/extensions/sdk/json"

// JSONClone flags bytes a function encodes and then decodes again: a deep copy or type conversion by JSON round trip.
var JSONClone = newCheck("jsonclone",
	"JSON bytes encoded and decoded again in one function: a deep copy or a conversion through JSON; copy the value with a typed Clone or convert it directly",
	Med, "go-idioms: ai history cloned through JSON per provider request (bolt-bench FINDING-2/3)",
	func(p *Pass) {
		p.Funcs(func(_ string, _ *ast.FuncType, body *ast.BlockStmt) {
			if p.IsTest(body.Pos()) {
				return
			}
			encoded := map[types.Object]bool{}
			ast.Inspect(body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.FuncLit:
					return false
				case *ast.AssignStmt:
					if len(n.Rhs) == 1 && len(n.Lhs) >= 1 {
						if call, ok := ast.Unparen(n.Rhs[0]).(*ast.CallExpr); ok && p.callsCodec(call, jsonEncoders) {
							if id, ok := n.Lhs[0].(*ast.Ident); ok {
								if obj := p.TypesInfo.ObjectOf(id); obj != nil {
									encoded[obj] = true
								}
							}
						}
					}
				case *ast.CallExpr:
					if !p.callsCodec(n, jsonDecoders) || len(n.Args) == 0 {
						return true
					}
					if p.mentionsEncoded(n.Args[0], encoded) {
						p.Reportf(n.Pos(), "%s decodes JSON this function just encoded; copy or convert the value without a JSON round trip", p.Callee(n).Name())
					}
				}
				return true
			})
		})
	})

func (p *Pass) callsCodec(call *ast.CallExpr, codecs map[string]map[string]bool) bool {
	fn := p.Callee(call)
	if fn == nil || fn.Pkg() == nil || !codecs[fn.Pkg().Path()][fn.Name()] {
		return false
	}
	sig, _ := fn.Type().(*types.Signature)
	return sig != nil && sig.Recv() == nil
}

// mentionsEncoded reports whether x is an encoded variable, possibly wrapped in a conversion or a reader over it (bytes.NewReader(b)).
func (p *Pass) mentionsEncoded(x ast.Expr, encoded map[types.Object]bool) bool {
	switch x := ast.Unparen(x).(type) {
	case *ast.Ident:
		return encoded[p.TypesInfo.ObjectOf(x)]
	case *ast.CallExpr:
		if len(x.Args) != 1 {
			return false
		}
		if tv, ok := p.TypesInfo.Types[x.Fun]; ok && tv.IsType() {
			return p.mentionsEncoded(x.Args[0], encoded)
		}
		if p.IsFunc(x, "bytes", "NewReader") || p.IsFunc(x, "bytes", "NewBuffer") || p.IsFunc(x, "strings", "NewReader") {
			return p.mentionsEncoded(x.Args[0], encoded)
		}
	}
	return false
}

// CtxBackground flags a root context made in library code. ctxmapping covers functions that already receive a context; this check covers the
// rest: a library function that starts work with context.Background() cannot be cancelled by its caller.
var CtxBackground = newCheck("ctxbackground",
	"context.Background()/TODO() in library code outside main and init: the work it starts has no caller to cancel it; take a context.Context parameter",
	Low, "go-idioms: 284 root contexts in library code",
	func(p *Pass) {
		if p.Pkg.Name() == "main" {
			return
		}
		p.Inspect.WithStack([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node, push bool, stack []ast.Node) bool {
			if !push || p.IsTest(n.Pos()) {
				return true
			}
			call := n.(*ast.CallExpr)
			if !p.IsFunc(call, "context", "Background") && !p.IsFunc(call, "context", "TODO") {
				return true
			}
			if p.innermostHasContext(stack) {
				return true
			}
			p.Reportf(call.Pos(), "context.%s() in library code: the caller cannot cancel the work it starts", p.Callee(call).Name())
			return true
		})
	})

// innermostHasContext reports whether the innermost function around the call is init, a package-level initializer, or declares a
// context.Context or *http.Request parameter (ctxmapping reports those).
func (p *Pass) innermostHasContext(stack []ast.Node) bool {
	for _, s := range slices.Backward(stack) {
		var typ *ast.FuncType
		switch fn := s.(type) {
		case *ast.FuncDecl:
			if fn.Recv == nil && fn.Name.Name == "init" {
				return true
			}
			typ = fn.Type
		case *ast.FuncLit:
			typ = fn.Type
		default:
			continue
		}
		for _, f := range typ.Params.List {
			if t := p.TypesInfo.TypeOf(f.Type); t != nil && (NamedIs(t, "context", "Context") || NamedIs(t, "net/http", "Request")) {
				return true
			}
		}
		return false
	}
	return true
}
