// SPDX-License-Identifier: MIT

package upgrade

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// helperFileName is the file that holds the getter helpers of a package. The
// plan picks the first name in the series that the package does not use.
const helperFileName = "pig_upgrade_getters"

// helperDef is one generated function that calls a Context getter the way the
// old SDK did: a failed host call gives the zero value, which the old getters
// returned and a plain call in an expression cannot take as two results.
type helperDef struct {
	name, method string
	params       []string
	result       string
	pointer      bool
}

// helperRegistry is the helper functions one package needs.
type helperRegistry struct {
	dir, pkg string
	defs     map[string]*helperDef
	// used holds the getters whose helper a rewrite calls.
	used    map[string]bool
	imports map[string]string
	sdkPath string
	// path is the helper file; it is chosen when the file is first written.
	path string
	// written is the number of helpers in the file as last written.
	written int
}

func newHelperRegistry(dir, pkg string) *helperRegistry {
	return &helperRegistry{dir: dir, pkg: pkg, defs: map[string]*helperDef{}, used: map[string]bool{}, imports: map[string]string{}}
}

func (r *helperRegistry) qualify(pkg *types.Package) string {
	if IsSDKPath(pkg.Path()) {
		r.sdkPath = pkg.Path()
		return "sdk"
	}
	r.imports[pkg.Path()] = pkg.Name()
	return pkg.Name()
}

// define returns the helper for the getter, reading its signature from the
// method the call selects.
func (r *helperRegistry) define(p *filePlanner, g getter, fn *types.Func) *helperDef {
	if def, ok := r.defs[g.Name]; ok {
		return def
	}
	signature := fn.Signature()
	r.sdkPath = fn.Pkg().Path()
	def := &helperDef{name: p.fresh("pigUpgrade" + g.Name), method: g.Name, pointer: g.Pointer}
	for i := range signature.Params().Len() {
		def.params = append(def.params, types.TypeString(signature.Params().At(i).Type(), r.qualify))
	}
	result := signature.Results().At(0).Type()
	if g.Pointer {
		result = result.(*types.Pointer).Elem()
	}
	def.result = types.TypeString(result, r.qualify)
	r.defs[g.Name] = def
	// A later file asking for a fresh name must not reuse this one.
	p.used[def.name] = true
	return def
}

// source renders the helper file.
func (r *helperRegistry) source() ([]byte, error) {
	var out bytes.Buffer
	out.WriteString("// Added by pig extension upgrade. These Context getters returned one value before the SDK gave them an error result; a failed host call gives the zero value, as it did then.\n")
	out.WriteString("// Call the getter itself where the error matters, then delete the helper.\n\n")
	out.WriteString("package " + r.pkg + "\n\nimport (\n")
	paths := make([]string, 0, len(r.imports))
	for path := range r.imports {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		out.WriteString("\t" + strconv.Quote(path) + "\n")
	}
	if len(paths) > 0 {
		out.WriteString("\n")
	}
	out.WriteString("\tsdk " + strconv.Quote(r.sdkPath) + "\n)\n")
	methods := make([]string, 0, len(r.used))
	for method := range r.used {
		methods = append(methods, method)
	}
	slices.Sort(methods)
	for _, method := range methods {
		def := r.defs[method]
		params := []string{"ctx sdk.Context"}
		args := make([]string, len(def.params))
		for i, param := range def.params {
			params = append(params, fmt.Sprintf("a%d %s", i, param))
			args[i] = fmt.Sprintf("a%d", i)
		}
		fmt.Fprintf(&out, "\n// %s calls ctx.%s as the SDK did before it returned an error.\nfunc %s(%s) %s {\n\tvalue, _ := ctx.%s(%s)\n", def.name, def.method, def.name, strings.Join(params, ", "), def.result, def.method, strings.Join(args, ", "))
		if def.pointer {
			out.WriteString("\tif value == nil {\n\t\treturn \"\"\n\t}\n\treturn *value\n}\n")
		} else {
			out.WriteString("\treturn value\n}\n")
		}
	}
	return format.Source(out.Bytes())
}

// choosePath names the helper file in the package directory, avoiding a file
// that is there already.
func (r *helperRegistry) choosePath() {
	if r.path != "" {
		return
	}
	for n := 1; ; n++ {
		name := helperFileName + ".go"
		if n > 1 {
			name = fmt.Sprintf("%s_%d.go", helperFileName, n)
		}
		path := filepath.Join(r.dir, name)
		if _, err := os.Stat(path); err != nil {
			r.path = path
			return
		}
	}
}

// registry returns the helpers of the file's package.
func (p *filePlanner) registry() *helperRegistry {
	key := p.pkg.ImportPath
	if r, ok := p.registries[key]; ok {
		return r
	}
	r := newHelperRegistry(filepath.Dir(p.file.Path), p.pkg.Types.Name())
	p.registries[key] = r
	return r
}

// fallback handles a getter that cannot move before its statement. One that
// returned its zero value on failure takes a helper that does the same; one
// that must handle its error, or that cannot take a helper, is listed.
func (p *filePlanner) fallback(s *getterSite, reason string) {
	if s.rule != RuleContextGetters || s.getter.MustHandle || s.spread || !p.planHelper(s) {
		p.skipSite(s, reason)
	}
}

// planHelper replaces the getter call with a call of its helper and reports
// whether it could.
func (p *filePlanner) planHelper(s *getterSite) bool {
	call, ok := s.call.(*ast.CallExpr)
	if !ok || p.registries == nil {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	selection := p.pkg.Info.Selections[selector]
	if selection == nil {
		return false
	}
	fn, ok := selection.Obj().(*types.Func)
	if !ok || fn.Signature().Results().Len() != 2 {
		return false
	}
	def := p.registry().define(p, s.getter, fn)
	receiver := p.text(selector.X.Pos(), selector.X.End())
	if _, isPointer := p.pkg.Info.TypeOf(selector.X).(*types.Pointer); isPointer {
		receiver = "*" + receiver
	}
	replacement := def.name + "(" + receiver
	if len(call.Args) > 0 {
		replacement += ", " + p.text(call.Args[0].Pos(), call.Args[len(call.Args)-1].End())
	}
	replacement += ")"
	p.sites = append(p.sites, site{
		rewrites: []Rewrite{{Rule: s.rule, Symbol: s.symbol, Line: p.line(call.Pos())}},
		edits:    []edit{{start: p.offset(call.Pos()), end: p.offset(call.End()), text: replacement}},
		helper:   s.getter.Name,
	})
	return true
}

// skipSite lists a getter use for the author, with the form to write.
func (p *filePlanner) skipSite(s *getterSite, reason string) {
	p.skipped = append(p.skipped, Skipped{Rule: s.rule, Symbol: s.symbol, File: p.file.Path, Line: p.line(s.call.Pos()), Reason: reason, Suggest: p.suggestion(s)})
}

// suggestion is the form the author writes in place of a getter use the plan
// cannot rewrite.
func (p *filePlanner) suggestion(s *getterSite) string {
	if s.rule != RuleContextGetters {
		return ""
	}
	return s.value + ", err := " + s.text + "; handle err (return it, or report it and stop); use " + s.value + " where the call was"
}
