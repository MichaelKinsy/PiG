package wiring

import (
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"strings"
	"testing"
)

// envelopeFieldType is the type of the envelope field whose JSON key is key,
// for example the payload type of the "call" frame.
func (p *goPackage) envelopeFieldType(t *testing.T, key string) *types.Named {
	t.Helper()
	scope := p.Types.Scope()
	for _, name := range scope.Names() {
		obj, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		st, ok := obj.Type().Underlying().(*types.Struct)
		if !ok {
			continue
		}
		for i := range st.NumFields() {
			tag, _, _ := strings.Cut(reflect.StructTag(st.Tag(i)).Get("json"), ",")
			if tag != key {
				continue
			}
			typ := st.Field(i).Type()
			if ptr, ok := typ.(*types.Pointer); ok {
				typ = ptr.Elem()
			}
			if named, ok := typ.(*types.Named); ok && named.Obj().Pkg() == p.Types {
				return named
			}
		}
	}
	t.Fatalf("%s: no envelope field with JSON key %q", p.PkgPath, key)
	return nil
}

// sender is one parameter that reaches a wire method field.
type sender struct {
	fn    *types.Func
	index int
}

// sentMethods derives every method name the package can put in the Method
// field of payload. A function whose parameter reaches that field, directly or
// through another such function, is a sender; every constant a caller passes in
// that position is a sent method.
func (p *goPackage) sentMethods(payload *types.Named) methodSet {
	sent, _ := p.sentMethodsAndSenders(payload)
	return sent
}

func (p *goPackage) sentMethodsAndSenders(payload *types.Named) (methodSet, map[sender]bool) {
	local := p.selfDispatched(payload)
	return p.constantsReaching(func(_ *ast.FuncDecl, _ *types.Func, node ast.Node) []ast.Expr {
		switch node := node.(type) {
		case *ast.CompositeLit:
			if isType(p.TypesInfo.TypeOf(node), payload) && !local[node] {
				if value, ok := fieldValue(node, "Method"); ok {
					return []ast.Expr{value}
				}
			}
		case *ast.AssignStmt:
			// env.Request.Method = MethodProviderSync re-targets a payload built above.
			var values []ast.Expr
			for i, lhs := range node.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if ok && sel.Sel.Name == "Method" && isType(p.TypesInfo.TypeOf(sel.X), payload) && i < len(node.Rhs) {
					values = append(values, node.Rhs[i])
				}
			}
			return values
		}
		return nil
	})
}

// constantsReaching derives every constant that reaches a sink expression. A
// sink that is a parameter makes its function a sender, and the arguments every
// caller passes in that position are sinks in turn.
func (p *goPackage) constantsReaching(sinks func(fn *ast.FuncDecl, obj *types.Func, node ast.Node) []ast.Expr) (methodSet, map[sender]bool) {
	sent := methodSet{}
	senders := map[sender]bool{}
	record := func(fn *ast.FuncDecl, obj *types.Func, expr ast.Expr) *sender {
		if ident, ok := ast.Unparen(expr).(*ast.Ident); ok {
			if index := paramIndex(obj, p.TypesInfo.ObjectOf(ident)); index >= 0 {
				return &sender{obj, index}
			}
		}
		if sel, ok := ast.Unparen(expr).(*ast.SelectorExpr); ok {
			// A method carried in a struct field holds every constant stored in that field.
			if field, ok := p.TypesInfo.ObjectOf(sel.Sel).(*types.Var); ok && field.IsField() {
				for value, at := range p.fieldConstants(field) {
					sent.add(value, at)
				}
				return nil
			}
		}
		for _, value := range p.stringValues(fn.Body, expr) {
			sent.add(value, p.position(expr.Pos()))
		}
		return nil
	}
	p.inspect(func(fn *ast.FuncDecl, obj *types.Func, node ast.Node) {
		for _, expr := range sinks(fn, obj, node) {
			if s := record(fn, obj, expr); s != nil {
				senders[*s] = true
			}
		}
	})
	for changed := true; changed; {
		changed = false
		p.inspect(func(fn *ast.FuncDecl, obj *types.Func, node ast.Node) {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return
			}
			callee := p.callee(call)
			for index, arg := range call.Args {
				if callee == nil || !senders[sender{callee, index}] {
					continue
				}
				if s := record(fn, obj, arg); s != nil && !senders[*s] {
					senders[*s] = true
					changed = true
				}
			}
		})
	}
	return sent, senders
}

// mapMethodValues returns the "method" entries of every map literal in node.
func (p *goPackage) mapMethodValues(node ast.Node) []ast.Expr {
	var values []ast.Expr
	ast.Inspect(node, func(inner ast.Node) bool {
		lit, ok := inner.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if _, isMap := p.TypesInfo.TypeOf(lit).Underlying().(*types.Map); !isMap {
			return true
		}
		for _, elt := range lit.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				if key, ok := p.constString(kv.Key); ok && key == "method" {
					values = append(values, kv.Value)
				}
			}
		}
		return true
	})
	return values
}

// handledMethods derives every method name the package dispatches on for a
// payload: the cases of a switch on its Method field and the constants compared
// with that field, in every function that receives the payload, and the same
// on a parameter the field is passed to (isRuntimeDrainNotify(n.Method)).
// prefixes lists each strings.HasPrefix branch on the field.
func (p *goPackage) handledMethods(payload *types.Named) (handled methodSet, prefixes []string) {
	handled = methodSet{}
	carriers := map[types.Object]bool{}
	isMethodField := func(expr ast.Expr) bool {
		if ident, ok := ast.Unparen(expr).(*ast.Ident); ok {
			return carriers[p.TypesInfo.ObjectOf(ident)]
		}
		sel, ok := ast.Unparen(expr).(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "Method" && isType(p.TypesInfo.TypeOf(sel.X), payload)
	}
	for changed := true; changed; {
		changed = false
		p.inspect(func(_ *ast.FuncDecl, _ *types.Func, node ast.Node) {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return
			}
			callee := p.callee(call)
			if callee == nil || callee.Pkg() != p.Types {
				return
			}
			params := callee.Signature().Params()
			for i, arg := range call.Args {
				if i < params.Len() && isMethodField(arg) && !carriers[params.At(i)] {
					carriers[params.At(i)] = true
					changed = true
				}
			}
		})
	}
	p.inspect(func(_ *ast.FuncDecl, _ *types.Func, node ast.Node) {
		switch node := node.(type) {
		case *ast.SwitchStmt:
			if node.Tag != nil && isMethodField(node.Tag) {
				for _, stmt := range node.Body.List {
					for _, expr := range stmt.(*ast.CaseClause).List {
						if value, ok := p.constString(expr); ok {
							handled.add(value, p.position(expr.Pos()))
						}
					}
				}
			}
		case *ast.BinaryExpr:
			if node.Op == token.EQL {
				for _, pair := range [][2]ast.Expr{{node.X, node.Y}, {node.Y, node.X}} {
					if isMethodField(pair[0]) {
						if value, ok := p.constString(pair[1]); ok {
							handled.add(value, p.position(node.Pos()))
						}
					}
				}
			}
		case *ast.CallExpr:
			if fn := p.callee(node); fn != nil && fn.Name() == "HasPrefix" && len(node.Args) == 2 && isMethodField(node.Args[0]) {
				if prefix, ok := p.constString(node.Args[1]); ok {
					prefixes = append(prefixes, prefix)
				}
			}
		}
	})
	return handled, prefixes
}

// fieldValue returns the value of a named field in a struct composite literal.
func fieldValue(lit *ast.CompositeLit, field string) (ast.Expr, bool) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if ident, ok := kv.Key.(*ast.Ident); ok && ident.Name == field {
			return kv.Value, true
		}
	}
	return nil, false
}

// fieldConstants is every constant stored in field, by composite literal or assignment.
func (p *goPackage) fieldConstants(field *types.Var) methodSet {
	values := methodSet{}
	p.inspect(func(_ *ast.FuncDecl, _ *types.Func, node ast.Node) {
		switch node := node.(type) {
		case *ast.CompositeLit:
			// A positional struct literal stores its i-th element in the i-th field.
			st, ok := p.TypesInfo.TypeOf(node).Underlying().(*types.Struct)
			if !ok || len(node.Elts) != st.NumFields() {
				return
			}
			for i, elt := range node.Elts {
				if _, keyed := elt.(*ast.KeyValueExpr); !keyed && st.Field(i) == field {
					if value, ok := p.constString(elt); ok {
						values.add(value, p.position(elt.Pos()))
					}
				}
			}
		case *ast.KeyValueExpr:
			if key, ok := node.Key.(*ast.Ident); ok && p.TypesInfo.ObjectOf(key) == field {
				if value, ok := p.constString(node.Value); ok {
					values.add(value, p.position(node.Pos()))
				}
			}
		case *ast.AssignStmt:
			for i, lhs := range node.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && p.TypesInfo.ObjectOf(sel.Sel) == field && i < len(node.Rhs) {
					if value, ok := p.constString(node.Rhs[i]); ok {
						values.add(value, p.position(node.Pos()))
					}
				}
			}
		}
	})
	return values
}

// selfDispatched lists payload literals built inside an envelope literal that
// sets no frame type. Such an envelope is handed to the package's own
// dispatcher, as the Go SDK replays its ready state as a state_update, and
// never reaches the wire.
func (p *goPackage) selfDispatched(payload *types.Named) map[*ast.CompositeLit]bool {
	local := map[*ast.CompositeLit]bool{}
	p.inspect(func(_ *ast.FuncDecl, _ *types.Func, node ast.Node) {
		env, ok := node.(*ast.CompositeLit)
		if !ok {
			return
		}
		st, ok := p.TypesInfo.TypeOf(env).Underlying().(*types.Struct)
		if !ok || !hasField(st, "Type") {
			return
		}
		if _, typed := fieldValue(env, "Type"); typed {
			return
		}
		ast.Inspect(env, func(inner ast.Node) bool {
			if lit, ok := inner.(*ast.CompositeLit); ok && lit != env && isType(p.TypesInfo.TypeOf(lit), payload) {
				local[lit] = true
			}
			return true
		})
	})
	return local
}

func hasField(st *types.Struct, name string) bool {
	for i := range st.NumFields() {
		if st.Field(i).Name() == name {
			return true
		}
	}
	return false
}

// jsonField finds the struct field of the package whose JSON key is key and
// whose type is a slice of strings, such as the provider declaration's methods.
func (p *goPackage) jsonStringSliceField(t *testing.T, key string) *types.Var {
	t.Helper()
	scope := p.Types.Scope()
	for _, name := range scope.Names() {
		obj, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		st, ok := obj.Type().Underlying().(*types.Struct)
		if !ok {
			continue
		}
		for i := range st.NumFields() {
			tag, _, _ := strings.Cut(reflect.StructTag(st.Tag(i)).Get("json"), ",")
			slice, isSlice := st.Field(i).Type().(*types.Slice)
			if tag == key && isSlice && types.Identical(slice.Elem(), types.Typ[types.String]) {
				return st.Field(i)
			}
		}
	}
	t.Fatalf("%s: no []string field with JSON key %q", p.PkgPath, key)
	return nil
}

// sliceFieldConstants is every constant stored in a []string field: by a
// composite literal or by append, including a table entry's field that is
// appended.
func (p *goPackage) sliceFieldConstants(field *types.Var) methodSet {
	values := methodSet{}
	add := func(expr ast.Expr) {
		if value, ok := p.constString(expr); ok {
			values.add(value, p.position(expr.Pos()))
			return
		}
		if sel, ok := ast.Unparen(expr).(*ast.SelectorExpr); ok {
			if inner, ok := p.TypesInfo.ObjectOf(sel.Sel).(*types.Var); ok && inner.IsField() {
				for value, at := range p.fieldConstants(inner) {
					values.add(value, at)
				}
			}
		}
	}
	isField := func(expr ast.Expr) bool {
		sel, ok := ast.Unparen(expr).(*ast.SelectorExpr)
		return ok && p.TypesInfo.ObjectOf(sel.Sel) == field
	}
	p.inspect(func(_ *ast.FuncDecl, _ *types.Func, node ast.Node) {
		switch node := node.(type) {
		case *ast.KeyValueExpr:
			if key, ok := node.Key.(*ast.Ident); ok && p.TypesInfo.ObjectOf(key) == field {
				if lit, ok := node.Value.(*ast.CompositeLit); ok {
					for _, elt := range lit.Elts {
						add(elt)
					}
				}
			}
		case *ast.CallExpr:
			if ident, ok := node.Fun.(*ast.Ident); ok && ident.Name == "append" && len(node.Args) > 0 && isField(node.Args[0]) {
				for _, arg := range node.Args[1:] {
					add(arg)
				}
			}
		}
	})
	return values
}

// subMethods derives, per call method, the "method" entries a package sends
// inside that call's arguments, such as provider.callback's prompt or
// sessionRead's getCwd.
func (p *goPackage) subMethods(payload *types.Named) map[string]methodSet {
	_, senders := p.sentMethodsAndSenders(payload)
	namespaces := map[string]bool{}
	sinksIn := func(namespace string) func(*ast.FuncDecl, *types.Func, ast.Node) []ast.Expr {
		return func(_ *ast.FuncDecl, _ *types.Func, node ast.Node) []ast.Expr {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return nil
			}
			callee := p.callee(call)
			var values []ast.Expr
			for index, arg := range call.Args {
				method, ok := p.constString(arg)
				if !ok || callee == nil || !senders[sender{callee, index}] {
					continue
				}
				for _, other := range call.Args {
					if found := p.mapMethodValues(other); len(found) > 0 {
						namespaces[method] = true
						if method == namespace {
							values = append(values, found...)
						}
					}
				}
			}
			return values
		}
	}
	p.constantsReaching(sinksIn(""))
	out := map[string]methodSet{}
	for namespace := range namespaces {
		out[namespace], _ = p.constantsReaching(sinksIn(namespace))
	}
	return out
}

// methodFieldConstants is every constant the package compares or switches on
// through a struct field decoded from a JSON "method" key, or through a string
// parameter named method, as a forwarded sub-method dispatcher takes it.
func (p *goPackage) methodFieldConstants() methodSet {
	values := methodSet{}
	isMethodKey := func(expr ast.Expr) bool {
		if ident, ok := ast.Unparen(expr).(*ast.Ident); ok {
			v, ok := p.TypesInfo.ObjectOf(ident).(*types.Var)
			return ok && !v.IsField() && v.Name() == "method" && types.Identical(v.Type(), types.Typ[types.String])
		}
		sel, ok := ast.Unparen(expr).(*ast.SelectorExpr)
		if !ok {
			return false
		}
		field, ok := p.TypesInfo.ObjectOf(sel.Sel).(*types.Var)
		if !ok || !field.IsField() {
			return false
		}
		st, ok := p.TypesInfo.TypeOf(sel.X).Underlying().(*types.Struct)
		if ptr, isPtr := p.TypesInfo.TypeOf(sel.X).(*types.Pointer); isPtr {
			st, ok = ptr.Elem().Underlying().(*types.Struct)
		}
		if !ok {
			return false
		}
		for i := range st.NumFields() {
			if st.Field(i) == field {
				tag, _, _ := strings.Cut(reflect.StructTag(st.Tag(i)).Get("json"), ",")
				return tag == "method"
			}
		}
		return false
	}
	p.inspect(func(_ *ast.FuncDecl, _ *types.Func, node ast.Node) {
		switch node := node.(type) {
		case *ast.SwitchStmt:
			if node.Tag != nil && isMethodKey(node.Tag) {
				for _, stmt := range node.Body.List {
					for _, expr := range stmt.(*ast.CaseClause).List {
						if value, ok := p.constString(expr); ok {
							values.add(value, p.position(expr.Pos()))
						}
					}
				}
			}
		case *ast.BinaryExpr:
			if node.Op == token.EQL || node.Op == token.NEQ {
				for _, pair := range [][2]ast.Expr{{node.X, node.Y}, {node.Y, node.X}} {
					if isMethodKey(pair[0]) {
						if value, ok := p.constString(pair[1]); ok {
							values.add(value, p.position(node.Pos()))
						}
					}
				}
			}
		}
	})
	return values
}
