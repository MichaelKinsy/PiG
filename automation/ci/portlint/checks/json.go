// SPDX-License-Identifier: MIT

package checks

import (
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"slices"
	"strings"
)

// jsonPackages are encoding/json and the shared host/Go-SDK codec forked from it (extensions/sdk/json). Both sort map keys, encode a nil slice as null and escape <, > and & by default.
var jsonPackages = [...]string{"encoding/json", modulePath + "/extensions/sdk/json"}

// isJSONFunc reports whether call invokes the package-level function name of a JSON package.
func (p *Pass) isJSONFunc(call *ast.CallExpr, name string) bool {
	for _, pkg := range jsonPackages {
		if p.IsFunc(call, pkg, name) {
			return true
		}
	}
	return false
}

// isJSONMethod reports whether call invokes the method name on a JSON package's named type typeName.
func (p *Pass) isJSONMethod(call *ast.CallExpr, typeName, name string) bool {
	for _, pkg := range jsonPackages {
		if p.IsMethod(call, pkg, typeName, name) {
			return true
		}
	}
	return false
}

// marshalArg returns the value encoded by a JSON package's Marshal, MarshalIndent or Encoder.Encode call.
func (p *Pass) marshalArg(call *ast.CallExpr) (ast.Expr, bool) {
	switch {
	case p.isJSONFunc(call, "Marshal"), p.isJSONFunc(call, "MarshalIndent"), p.isJSONMethod(call, "Encoder", "Encode"):
		if len(call.Args) > 0 {
			return call.Args[0], true
		}
	}
	return nil, false
}

func underlyingOf(t types.Type) types.Type {
	t = types.Unalias(t)
	if ptr, ok := t.Underlying().(*types.Pointer); ok {
		t = types.Unalias(ptr.Elem())
	}
	return t.Underlying()
}

// MapKeyOrder flags JSON encoding of a Go map. Go sorts map keys; JSON.stringify keeps insertion order.
var MapKeyOrder = newCheck("mapkeyorder",
	"json.Marshal or Encoder.Encode of a map type: Go sorts keys where Pi preserves insertion order",
	High, "#165 edited tool input arrived with sorted keys",
	func(p *Pass) {
		p.Inspect.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
			call := n.(*ast.CallExpr)
			arg, ok := p.marshalArg(call)
			if !ok || p.IsTest(call.Pos()) {
				return
			}
			tv, ok := p.TypesInfo.Types[arg]
			if !ok {
				return
			}
			if m, ok := underlyingOf(tv.Type).(*types.Map); ok {
				if _, isString := m.Key().Underlying().(*types.Basic); isString {
					p.Reportf(call.Pos(), "encoding %s sorts keys; Pi's JSON.stringify keeps insertion order (use orderedjson)", types.TypeString(tv.Type, types.RelativeTo(p.Pkg)))
				}
			}
		})
	})

// EmptyDrop flags omitempty on slice and map fields, which drops an explicit empty array or object.
var EmptyDrop = newCheck("emptydrop",
	"omitempty on a slice or map field drops an explicit [] or {} that Pi sends",
	Med, "MCP empty titles; ClientCapabilities empty objects; Piglet empty active-tool list",
	func(p *Pass) {
		p.Inspect.Preorder([]ast.Node{(*ast.StructType)(nil)}, func(n ast.Node) {
			st := n.(*ast.StructType)
			for _, f := range st.Fields.List {
				if f.Tag == nil || len(f.Names) == 0 {
					continue
				}
				tag, err := unquote(f.Tag.Value)
				if err != nil {
					continue
				}
				name, opts, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")
				if name == "-" || !hasOpt(opts, "omitempty") {
					continue
				}
				if rawJSON(p.TypesInfo.TypeOf(f.Type)) {
					// A RawMessage holds an encoded value: an explicit [] or {} is two bytes and survives omitempty; only an absent value is dropped.
					continue
				}
				switch types.Unalias(p.TypesInfo.TypeOf(f.Type)).Underlying().(type) {
				case *types.Slice, *types.Map:
					p.Reportf(f.Tag.Pos(), "field %s: omitempty drops an explicit empty %s; Pi sends it", f.Names[0].Name, types.TypeString(p.TypesInfo.TypeOf(f.Type), types.RelativeTo(p.Pkg)))
				}
			}
		})
	})

// rawJSON reports whether t is encoding/json.RawMessage, which the standard library may declare as an alias of encoding/json/jsontext.Value.
func rawJSON(t types.Type) bool {
	return NamedIs(t, "encoding/json", "RawMessage") || NamedIs(t, "encoding/json/jsontext", "Value")
}

func hasOpt(opts, want string) bool {
	for o := range strings.SplitSeq(opts, ",") {
		if o == want {
			return true
		}
	}
	return false
}

// NilSliceNull flags JSON encoding of a bare slice, which becomes null when nil where Pi sends [].
// A function that tests the value against nil has already decided what a nil slice means.
var NilSliceNull = newCheck("nilslicenull",
	"json.Marshal of a slice-typed value: a nil slice encodes as null where Pi sends []",
	High, "Piglet empty active-tool list",
	func(p *Pass) {
		p.Inspect.WithStack([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node, push bool, stack []ast.Node) bool {
			call := n.(*ast.CallExpr)
			if !push || p.IsTest(call.Pos()) {
				return true
			}
			arg, ok := p.marshalArg(call)
			if !ok {
				return true
			}
			if _, isLit := ast.Unparen(arg).(*ast.CompositeLit); isLit {
				return true
			}
			tv, ok := p.TypesInfo.Types[arg]
			if !ok {
				return true
			}
			s, ok := underlyingOf(tv.Type).(*types.Slice)
			if !ok {
				return true
			}
			if b, isByte := s.Elem().Underlying().(*types.Basic); isByte && b.Kind() == types.Byte {
				return true
			}
			if id, ok := ast.Unparen(arg).(*ast.Ident); ok && nilChecked(p, stack, p.TypesInfo.ObjectOf(id)) {
				return true
			}
			p.Reportf(call.Pos(), "encoding %s: a nil slice becomes null, Pi sends []", types.TypeString(tv.Type, types.RelativeTo(p.Pkg)))
			return true
		})
	})

// nilChecked reports whether the enclosing declaration compares obj with nil or assigns it a non-nil slice.
func nilChecked(p *Pass, stack []ast.Node, obj types.Object) bool {
	fn := enclosingDecl(stack)
	if fn == nil || obj == nil {
		return false
	}
	checked := false
	ast.Inspect(fn, func(n ast.Node) bool {
		if as, ok := n.(*ast.AssignStmt); ok && len(as.Lhs) == len(as.Rhs) {
			for i, l := range as.Lhs {
				if id, ok := l.(*ast.Ident); ok && p.TypesInfo.ObjectOf(id) == obj && nonNilSlice(as.Rhs[i]) {
					checked = true
				}
			}
		}
		b, ok := n.(*ast.BinaryExpr)
		if !ok || (b.Op != token.EQL && b.Op != token.NEQ) {
			return !checked
		}
		for _, pair := range [][2]ast.Expr{{b.X, b.Y}, {b.Y, b.X}} {
			if id, ok := pair[0].(*ast.Ident); ok && p.TypesInfo.ObjectOf(id) == obj {
				if nid, ok := pair[1].(*ast.Ident); ok && nid.Name == "nil" {
					checked = true
				}
			}
		}
		return !checked
	})
	return checked
}

// JSONEscape flags JSON text built with an encoder that escapes <, > and & and rejects NaN and Inf, unlike JSON.stringify.
var JSONEscape = newCheck("jsonescape",
	"JSON text from json.Marshal, json.MarshalIndent or an Encoder without SetEscapeHTML(false): Go escapes <, > and & where JSON.stringify does not",
	Med, "JSON.stringify never escapes <, > or &",
	func(p *Pass) {
		p.Funcs(func(_ string, _ *ast.FuncType, body *ast.BlockStmt) {
			escapeOff := false
			var encoders, indented []*ast.CallExpr
			marshalled := map[types.Object]*ast.CallExpr{}
			textual := map[*ast.CallExpr]bool{}
			isMarshal := func(e ast.Expr) *ast.CallExpr {
				call, ok := ast.Unparen(e).(*ast.CallExpr)
				if ok && (p.isJSONFunc(call, "Marshal") || p.isJSONFunc(call, "MarshalIndent")) {
					return call
				}
				return nil
			}
			ast.Inspect(body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.FuncLit:
					return n.Body == body
				case *ast.AssignStmt:
					if len(n.Rhs) == 1 && len(n.Lhs) >= 1 {
						if call := isMarshal(n.Rhs[0]); call != nil {
							if id, ok := n.Lhs[0].(*ast.Ident); ok {
								if obj := p.TypesInfo.ObjectOf(id); obj != nil {
									marshalled[obj] = call
								}
							}
						}
					}
				case *ast.CallExpr:
					switch {
					case p.isJSONMethod(n, "Encoder", "SetEscapeHTML"):
						escapeOff = true
					case p.isJSONFunc(n, "NewEncoder"):
						encoders = append(encoders, n)
					case p.isJSONFunc(n, "MarshalIndent"):
						// Indented JSON is file or display text, the counterpart of JSON.stringify(value, null, 2).
						indented = append(indented, n)
					}
					// string(json.Marshal(...)) cannot compile; string(b) of a marshalled b is JSON text.
					if tv, ok := p.TypesInfo.Types[n.Fun]; ok && tv.IsType() && len(n.Args) == 1 {
						if b, ok := types.Unalias(tv.Type).Underlying().(*types.Basic); ok && b.Kind() == types.String {
							if id, ok := ast.Unparen(n.Args[0]).(*ast.Ident); ok {
								if call := marshalled[p.TypesInfo.ObjectOf(id)]; call != nil {
									textual[call] = true
								}
							}
						}
					}
				}
				return true
			})
			for call := range textual {
				// An indented call is already reported below.
				if !p.IsTest(call.Pos()) && !slices.Contains(indented, call) {
					p.Reportf(call.Pos(), "string(json.Marshal(...)) escapes <, > and & as \\u003c; JSON.stringify does not")
				}
			}
			for _, c := range indented {
				if !p.IsTest(c.Pos()) {
					p.Reportf(c.Pos(), "json.MarshalIndent escapes <, > and & as \\u003c; JSON.stringify(value, null, 2) does not")
				}
			}
			if !escapeOff {
				for _, c := range encoders {
					if !p.IsTest(c.Pos()) {
						p.Reportf(c.Pos(), "json.NewEncoder without SetEscapeHTML(false) escapes <, > and &; JSON.stringify does not")
					}
				}
			}
		})
	})

// nonNilSlice reports whether e is make([]T, ...) or a slice literal, which are never nil.
func nonNilSlice(e ast.Expr) bool {
	switch e := ast.Unparen(e).(type) {
	case *ast.CompositeLit:
		return true
	case *ast.CallExpr:
		id, ok := e.Fun.(*ast.Ident)
		return ok && id.Name == "make"
	}
	return false
}
