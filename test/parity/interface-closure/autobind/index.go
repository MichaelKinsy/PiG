package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// use is one reference to a declaration from inside a top-level function.
type use struct {
	File       string // repo-relative file of the referencing function
	Caller     string // Name or Recv.Name of the referencing function
	Test       bool
	Call       bool // the reference is the callee of a call
	ResultUsed bool // for a call: the value is not discarded
	Dispatch   bool // the reference reaches the declaration through an interface
}

func (u use) key() string { return u.File + "#" + u.Caller }

// fnInfo describes a function declared in a test file.
type fnInfo struct {
	asserts bool
	callees map[string]bool
}

// pkgInfo is one repository package, taken from its non-test variant when there is one.
type pkgInfo struct {
	Dir   string
	Path  string
	Types *types.Package
	// Files and Info are the non-test syntax and type information of the package, for the rules that read code (union discriminators).
	Files []*ast.File
	Info  *types.Info
}

type namedType struct {
	obj *types.TypeName
	dir string
}

// index holds every repository declaration and the references to it, derived from go/types.
type index struct {
	root   string
	module string
	fset   *token.FileSet
	pkgs   map[string]*pkgInfo
	uses   map[string][]use
	useSet map[string]bool
	owners map[string]string
	fns    map[string]*fnInfo
	named  []namedType
	// methodSets memoizes the method set of a pointer to each named type; implementing asks for it once per interface method.
	methodSets map[*types.TypeName]*types.MethodSet
	// sigStrings memoizes sigString per method object.
	sigStrings map[*types.Func]string
	ifaces     map[string]*types.Func // interface method decl key -> method object
	// ctxCache memoizes ctxConsumers.
	ctxCache map[types.Type]bool
	reach    map[string]bool
	// literalMethods maps pkgpath.Type.method to the string a parameterless method returns as its only statement.
	literalMethods map[string]string
	// encodedPairs holds pkgpath.Type + "\x00" + key + "\x00" + value for each string pair a method of the type writes as a
	// composite literal element: {"key", "value"} or "key": "value".
	encodedPairs map[string]bool
	// reverse maps a test-file function to the test-file functions that call it, built on first use.
	reverse map[string][]string
}

func (ix *index) pos(p token.Pos) token.Position { return ix.fset.Position(p) }

func (ix *index) rel(filename string) string {
	r, err := filepath.Rel(ix.root, filename)
	if err != nil {
		return filename
	}
	return filepath.ToSlash(r)
}

// declKey names a declaration by its source position, which is stable across the package variants go/packages builds.
func (ix *index) declKey(obj types.Object) string {
	switch o := obj.(type) {
	case *types.Func:
		obj = o.Origin()
	case *types.Var:
		obj = o.Origin()
	}
	p := ix.pos(obj.Pos())
	return fmt.Sprintf("%s:%d", p.Filename, p.Offset)
}

func (ix *index) file(obj types.Object) string { return ix.rel(ix.pos(obj.Pos()).Filename) }

func isTestFile(name string) bool { return strings.HasSuffix(name, "_test.go") }

func buildIndex(root string, reach map[string]bool, patterns []string) (*index, error) {
	reach = withoutHarnessProbes(reach)
	cfg := &packages.Config{
		Mode:  packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
		Tests: true,
		Dir:   root,
	}
	loaded, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}
	ix := &index{root: root, pkgs: map[string]*pkgInfo{}, uses: map[string][]use{}, useSet: map[string]bool{}, owners: map[string]string{},
		fns: map[string]*fnInfo{}, ifaces: map[string]*types.Func{}, reach: reach}
	for _, p := range loaded {
		if ix.module == "" && p.Module != nil {
			ix.module = p.Module.Path
		}
		if ix.fset == nil {
			ix.fset = p.Fset
		}
	}
	if ix.fset == nil {
		return nil, fmt.Errorf("no packages loaded for %v", patterns)
	}
	sort.SliceStable(loaded, func(i, j int) bool {
		return !strings.Contains(loaded[i].ID, " [") && strings.Contains(loaded[j].ID, " [")
	})
	seenFile := map[string]bool{}
	var methodUses []methodUse
	for _, p := range loaded {
		if p.Types == nil || p.TypesInfo == nil || !ix.inModule(p.PkgPath) {
			continue
		}
		if !strings.HasSuffix(p.PkgPath, "_test") && !strings.HasSuffix(p.ID, ".test") {
			if dir := ix.pkgDir(p); dir != "" && ix.pkgs[dir] == nil {
				info := &pkgInfo{Dir: dir, Path: p.PkgPath, Types: p.Types, Info: p.TypesInfo}
				for _, f := range p.Syntax {
					if !isTestFile(ix.pos(f.Pos()).Filename) {
						info.Files = append(info.Files, f)
					}
				}
				ix.pkgs[dir] = info
			}
		}
		for _, f := range p.Syntax {
			name := ix.pos(f.Pos()).Filename
			if seenFile[name] {
				continue
			}
			seenFile[name] = true
			ix.walkFile(p, f, name, &methodUses)
		}
	}
	ix.collectNamed()
	ix.expandDispatch(methodUses)
	return ix, nil
}

func (ix *index) inModule(path string) bool {
	return ix.module == "" || path == ix.module || strings.HasPrefix(path, ix.module+"/")
}

func (ix *index) pkgDir(p *packages.Package) string {
	for _, f := range p.GoFiles {
		if !isTestFile(f) {
			return filepath.ToSlash(filepath.Dir(ix.rel(f)))
		}
	}
	for _, f := range p.GoFiles {
		return filepath.ToSlash(filepath.Dir(ix.rel(f)))
	}
	return ""
}

// methodUse is a call through an interface method, kept until the implementers are known.
type methodUse struct {
	method *types.Func
	use    use
}

func (ix *index) walkFile(p *packages.Package, f *ast.File, filename string, methodUses *[]methodUse) {
	rel := ix.rel(filename)
	test := isTestFile(filename)
	info := p.TypesInfo
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			ix.collectOwners(info, d)
		case *ast.FuncDecl:
			if d.Body == nil {
				continue
			}
			ix.collectLiteralMethod(p.PkgPath, d)
			ix.collectEncodedPairs(p.PkgPath, d)
			caller := d.Name.Name
			if d.Recv != nil && len(d.Recv.List) == 1 {
				caller = receiverName(d.Recv.List[0].Type) + "." + caller
			}
			ix.walkFunc(info, d, rel, caller, test, methodUses)
		}
	}
}

func receiverName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return receiverName(t.X)
	case *ast.IndexExpr:
		return receiverName(t.X)
	case *ast.IndexListExpr:
		return receiverName(t.X)
	case *ast.ParenExpr:
		return receiverName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}

func (ix *index) collectOwners(info *types.Info, d *ast.GenDecl) {
	for _, spec := range d.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		var fields *ast.FieldList
		switch t := ts.Type.(type) {
		case *ast.StructType:
			fields = t.Fields
		case *ast.InterfaceType:
			fields = t.Methods
		}
		if fields == nil {
			continue
		}
		for _, f := range fields.List {
			for _, n := range f.Names {
				if obj := info.Defs[n]; obj != nil {
					ix.owners[ix.declKey(obj)] = ts.Name.Name
				}
			}
		}
	}
}

var testingFailures = map[string]bool{"Error": true, "Errorf": true, "Fatal": true, "Fatalf": true, "Fail": true, "FailNow": true}

func isAssertion(fn *types.Func) bool {
	if fn.Pkg() == nil {
		return false
	}
	path := fn.Pkg().Path()
	switch {
	case path == "testing":
		return testingFailures[fn.Name()]
	case strings.HasSuffix(path, "/testify/assert"), strings.HasSuffix(path, "/testify/require"):
		return true
	}
	return false
}

func (ix *index) walkFunc(info *types.Info, d *ast.FuncDecl, rel, caller string, test bool, methodUses *[]methodUse) {
	fnKey := rel + "#" + caller
	var fi *fnInfo
	if test {
		fi = &fnInfo{callees: map[string]bool{}}
		ix.fns[fnKey] = fi
	}
	var stack []ast.Node
	// The whole declaration: a type named only in a parameter or result list is used by that function too.
	ast.Inspect(d, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		obj := info.Uses[id]
		if obj == nil || obj.Pkg() == nil {
			return true
		}
		if fn, ok := obj.(*types.Func); ok && fi != nil {
			if isAssertion(fn) {
				fi.asserts = true
			}
			if fn.Pkg() != nil && ix.inModule(fn.Pkg().Path()) && isTestFile(ix.pos(fn.Pos()).Filename) {
				fi.callees[ix.rel(ix.pos(fn.Pos()).Filename)+"#"+funcName(fn)] = true
			}
		}
		if !ix.inModule(obj.Pkg().Path()) {
			return true
		}
		u := use{File: rel, Caller: caller, Test: test}
		u.Call, u.ResultUsed = callContext(stack)
		key := ix.declKey(obj)
		dedupe := fmt.Sprintf("%s|%s|%t|%t", key, fnKey, u.Call, u.ResultUsed)
		if !ix.useSet[dedupe] {
			ix.useSet[dedupe] = true
			ix.uses[key] = append(ix.uses[key], u)
		}
		if fn, ok := obj.(*types.Func); ok {
			if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil && types.IsInterface(sig.Recv().Type()) {
				u.Dispatch = true
				*methodUses = append(*methodUses, methodUse{method: fn.Origin(), use: u})
			}
		}
		return true
	})
}

func funcName(fn *types.Func) string {
	if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
		t := sig.Recv().Type()
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
		}
		if n, ok := t.(*types.Named); ok {
			return n.Obj().Name() + "." + fn.Name()
		}
	}
	return fn.Name()
}

// callContext reports whether the identifier on top of stack is the callee of a call and whether the call's value is used.
func callContext(stack []ast.Node) (call, resultUsed bool) {
	i := len(stack) - 1
	callee := stack[i]
	j := i - 1
	if j >= 0 {
		if sel, ok := stack[j].(*ast.SelectorExpr); ok && sel.Sel == callee {
			callee = sel
			j--
		}
	}
	for j >= 0 {
		switch p := stack[j].(type) {
		case *ast.ParenExpr:
			callee = p
			j--
			continue
		case *ast.IndexExpr:
			callee = p
			j--
			continue
		case *ast.IndexListExpr:
			callee = p
			j--
			continue
		}
		break
	}
	if j < 0 {
		return false, false
	}
	c, ok := stack[j].(*ast.CallExpr)
	if !ok || c.Fun != callee {
		return false, false
	}
	if j == 0 {
		return true, true
	}
	switch stack[j-1].(type) {
	case *ast.ExprStmt, *ast.GoStmt, *ast.DeferStmt:
		return true, false
	}
	return true, true
}

func (ix *index) collectNamed() {
	dirs := make([]string, 0, len(ix.pkgs))
	for d := range ix.pkgs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		scope := ix.pkgs[d].Types.Scope()
		for _, name := range scope.Names() {
			if tn, ok := scope.Lookup(name).(*types.TypeName); ok && !tn.IsAlias() && !types.IsInterface(tn.Type()) {
				if _, ok := tn.Type().(*types.Named); ok {
					ix.named = append(ix.named, namedType{obj: tn, dir: d})
				}
			}
		}
	}
}

func sigString(fn *types.Func) string {
	return types.TypeString(fn.Type().(*types.Signature), func(p *types.Package) string { return p.Path() })
}

// expandDispatch attributes each call through a module interface method to the same-named method of every
// repository type that implements the whole interface. Implementation is structural (method names and signature
// strings), so it holds across go/packages' package variants.
func (ix *index) expandDispatch(methodUses []methodUse) {
	type ifaceKey = string
	implementers := map[ifaceKey][]types.Object{}
	byMethod := map[string][]methodUse{}
	for _, mu := range methodUses {
		k := ix.declKey(mu.method)
		byMethod[k] = append(byMethod[k], mu)
	}
	for _, mus := range byMethod {
		fn := mus[0].method
		sig := fn.Type().(*types.Signature)
		iface, ok := sig.Recv().Type().Underlying().(*types.Interface)
		if !ok || fn.Pkg() == nil || !ix.inModule(fn.Pkg().Path()) {
			continue
		}
		ik := ix.declKey(fn)
		if _, done := implementers[ik]; !done {
			var impl []types.Object
			for _, nt := range ix.named {
				if m := ix.implementing(nt, iface, fn.Name()); m != nil {
					impl = append(impl, m)
				}
			}
			implementers[ik] = impl
		}
		for _, m := range implementers[ik] {
			mk := ix.declKey(m)
			for _, mu := range mus {
				dedupe := fmt.Sprintf("%s|%s|dispatch", mk, mu.use.key())
				if !ix.useSet[dedupe] {
					ix.useSet[dedupe] = true
					ix.uses[mk] = append(ix.uses[mk], mu.use)
				}
			}
		}
	}
}

// implementing returns the method called name of nt (or *nt) when nt implements every method of iface.
func (ix *index) implementing(nt namedType, iface *types.Interface, name string) types.Object {
	ms := ix.methodSets[nt.obj]
	if ms == nil {
		if ix.methodSets == nil {
			ix.methodSets = map[*types.TypeName]*types.MethodSet{}
		}
		ms = types.NewMethodSet(types.NewPointer(nt.obj.Type()))
		ix.methodSets[nt.obj] = ms
	}
	var found types.Object
	for want := range iface.Methods() {
		sel := ms.Lookup(want.Pkg(), want.Name())
		if sel == nil {
			return nil
		}
		got, ok := sel.Obj().(*types.Func)
		if !ok || ix.cachedSigString(got) != ix.cachedSigString(want) {
			return nil
		}
		if want.Name() == name {
			found = got
		}
	}
	return found
}

// collectLiteralMethod records a method of the form func (T) name() string { return "literal" }.
func (ix *index) collectLiteralMethod(pkgPath string, d *ast.FuncDecl) {
	if d.Recv == nil || len(d.Recv.List) != 1 || d.Type.Params.NumFields() != 0 || len(d.Body.List) != 1 {
		return
	}
	ret, ok := d.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return
	}
	lit, ok := ret.Results[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return
	}
	if ix.literalMethods == nil {
		ix.literalMethods = map[string]string{}
	}
	ix.literalMethods[pkgPath+"."+receiverName(d.Recv.List[0].Type)+"."+d.Name.Name] = v
}

// collectEncodedPairs records the string key/value pairs a method writes as composite literal elements, the form a hand-written
// encoder uses for a constant discriminator: Object{{"type", "hello"}, ...} or map[string]any{"type": "hello"}.
func (ix *index) collectEncodedPairs(pkgPath string, d *ast.FuncDecl) {
	if d.Recv == nil || len(d.Recv.List) != 1 || d.Body == nil {
		return
	}
	prefix := pkgPath + "." + receiverName(d.Recv.List[0].Type) + "\x00"
	str := func(e ast.Expr) (string, bool) {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(lit.Value)
		return v, err == nil
	}
	ast.Inspect(d.Body, func(n ast.Node) bool {
		var k, v ast.Expr
		switch e := n.(type) {
		case *ast.KeyValueExpr:
			k, v = e.Key, e.Value
		case *ast.CompositeLit:
			if len(e.Elts) != 2 {
				return true
			}
			k, v = e.Elts[0], e.Elts[1]
		default:
			return true
		}
		key, ok1 := str(k)
		val, ok2 := str(v)
		if ok1 && ok2 {
			if ix.encodedPairs == nil {
				ix.encodedPairs = map[string]bool{}
			}
			ix.encodedPairs[prefix+key+"\x00"+val] = true
		}
		return true
	})
}

// enumValues returns the string values listed by a package-level variable whose value is a slice or array literal of tn
// (`var CacheWarmingModes = []CacheWarmingMode{"off", "streaming", "idle"}`), the Go form of Pi's `as const` value arrays.
func (ix *index) enumValues(tn *types.TypeName) map[string]bool {
	out := map[string]bool{}
	for _, info := range ix.pkgs {
		if info.Types != tn.Pkg() {
			continue
		}
		for _, f := range info.Files {
			for _, decl := range f.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.VAR {
					continue
				}
				for _, spec := range gd.Specs {
					for _, v := range spec.(*ast.ValueSpec).Values {
						lit, ok := v.(*ast.CompositeLit)
						if !ok || !listOf(info.Info.TypeOf(lit), tn.Type()) {
							continue
						}
						for _, el := range lit.Elts {
							if tv, ok := info.Info.Types[el]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
								out[constant.StringVal(tv.Value)] = true
							}
						}
					}
				}
			}
		}
	}
	return out
}

// listOf reports whether t is a slice or array of elem.
func listOf(t, elem types.Type) bool {
	if t == nil {
		return false
	}
	switch u := t.Underlying().(type) {
	case *types.Slice:
		return types.Identical(u.Elem(), elem)
	case *types.Array:
		return types.Identical(u.Elem(), elem)
	}
	return false
}

// ctxConsumers reports whether every Go function, method or function-typed field of the repository that takes a value of t (or *t)
// also takes a context.Context, and at least one does: the context then carries the cancellation that an upstream AbortSignal
// member of t's type carries (T12s).
func (ix *index) ctxConsumers(t types.Type) bool {
	if ix.ctxCache == nil {
		ix.ctxCache = map[types.Type]bool{}
	}
	if v, ok := ix.ctxCache[t]; ok {
		return v
	}
	takes := func(sig *types.Signature) (bool, bool) {
		has, ctx := false, false
		for v := range sig.Params().Variables() {
			pt := v.Type()
			if p, ok := pt.(*types.Pointer); ok {
				pt = p.Elem()
			}
			has = has || types.Identical(pt, t)
			ctx = ctx || isContext(v.Type())
		}
		return has, ctx
	}
	n, all := 0, true
	for _, info := range ix.pkgs {
		for _, obj := range info.Info.Defs {
			if obj == nil {
				continue
			}
			switch o := obj.(type) {
			case *types.Func:
			case *types.Var:
				if !o.IsField() {
					continue
				}
			default:
				continue
			}
			sig, ok := obj.Type().Underlying().(*types.Signature)
			if !ok {
				continue
			}
			if has, ctx := takes(sig); has {
				n++
				all = all && ctx
			}
		}
	}
	ix.ctxCache[t] = n > 0 && all
	return ix.ctxCache[t]
}

// withoutHarnessProbes drops the parity-harness probes from a reach set: they run only under PIG_PARITY_HARNESS=1, so a member they
// use has no production caller (the validator rejects them as production references). A nil set stays nil.
func withoutHarnessProbes(reach map[string]bool) map[string]bool {
	if reach == nil {
		return nil
	}
	out := make(map[string]bool, len(reach))
	for fn, ok := range reach {
		if file, _, _ := strings.Cut(fn, "#"); filepath.Base(file) == "parity_harness.go" {
			continue
		}
		out[fn] = ok
	}
	return out
}

func (ix *index) cachedSigString(fn *types.Func) string {
	if s, ok := ix.sigStrings[fn]; ok {
		return s
	}
	if ix.sigStrings == nil {
		ix.sigStrings = map[*types.Func]string{}
	}
	s := sigString(fn)
	ix.sigStrings[fn] = s
	return s
}
