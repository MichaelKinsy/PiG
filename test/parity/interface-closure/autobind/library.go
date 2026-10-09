package main

import (
	"encoding/json"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/test/parity/interface-closure/autobind/rules"
)

// Rule P1(b), the library route (LEAD-DECISION-P1): a Go field or method that no production path from cmd/pig reaches still closes
// its row when all of these hold.
//
//	L1 the row's upstream package is a library: its package.json publishes an index (exports or main). The package that ships the
//	   `pi` command (coding-agent) qualifies too: its index exports the SDK classes (RpcClient, EventBus, ResourceLoader, ...) and the
//	   inventory records only declarations that index publishes (LEAD-RULINGS-1520, lg-imla-2 census 1); and
//	   the member's Go package (or its owner type's) holds a file docs/parity/PORT_MAP.md records for an upstream source file that
//	   the package publishes (the `export ... from` closure of every package.json export), or such a package declares an exported
//	   alias of the owner type
//	L2 the member is exported and so is its owner type
//	L3 it duplicates no other member: no other exported field of its owner folds to the same name, a method is not a one-line
//	   forward to another exported member of its owner nor a repeat of a wired package function of its name and signature, and no type of the same name elsewhere in the module declares a wired or
//	   exported member of the same name and type, unless PORT_MAP records that declaration for another upstream package (upstream
//	   declares both)
//	L4 a method is not a stand-in: its body is not one return of constants, nil and empty composite literals, unless the upstream
//	   property type is itself a literal
//	L5 a field is read or set by non-test code in a Go directory that ports the upstream package; a JSON-tagged field counts when
//	   such code uses its owner type and the field's Go package encodes JSON
//	L6 an asserting Go test that references it cites, in its doc comment or body comments, an upstream file:line whose line lies
//	   in the file and whose file names the member (a file:line of the current mirror; a pinned mirror's never counts), or runs the Pi oracle (the test, its file or a test helper it calls within
//	   three calls is named for the oracle), or the test's file declares `Ports packages/<pkg>/test/<file>.test.ts`, that file names
//	   the member, and the test runs a t.Run subtest titled exactly as one of the file's it or test cases, or the test function is
//	   named after one (Test + the title, optionally prefixed by the last words of a describe title, compared on letters and
//	   digits), or `autobind -mutate` recorded that a zero-body mutant of the member fails an asserting
//	   test whose file cites such a file:line, or the member is used by a ported Pi suite: non-test Go code in a file
//	   PORT_MAP records for a file the package publishes (such as the `./testing` conformance suites), and that Pi file names the
//	   member, is reached within three calls inside its Go package from a function a Go test calls with its *testing.T (the
//	   suite reports its Pi assertions through it). Tests that skip unless an environment variable is set are not evidence
//	L7 a member of an interface whose every implementation is a test double (a type declared in a test file or in a test-support
//	   package, or whose name has a test-double prefix that testDouble lists) closes only on a conformance test: the double proves nothing about the production
//	   implementation (extension.API is implemented only by extensiontest.Fake; real extensions run through the SDKs)
//
// A row that fails any of them stays the P1 gap, and the reason names the first condition that failed.

// library holds what L1 and L6 read from the upstream mirror and PORT_MAP, built once per detector.
type library struct {
	root      string
	libraries map[string]bool                // upstream package key -> L1 package condition
	goDirs    map[string]map[string]bool     // upstream package key -> Go directories holding a port of a file its index publishes
	published map[string]map[string][]string // upstream package key -> Go file -> the published upstream files it ports
	pkgGo     map[string]map[string]bool     // upstream package key -> Go files that port any of its files
	pkgDirs   map[string]map[string]bool     // upstream package key -> directories of pkgGo
	mapped    map[string]bool                // every Go file PORT_MAP records
	files     map[string][]string            // upstream package key -> files under packages/<pkg>/, relative to it, sorted
	lines     map[string]int                 // pkg/file -> line count
	texts     map[string]string              // pkg/file -> content
	words     map[string]*regexp.Regexp
	fset      *token.FileSet
	tests     map[string]*ast.File // repo-relative test file -> parsed with comments
	funcs     map[string]map[string]*ast.FuncDecl
	// mutation maps a member target to the test refs whose run fails a zero-body mutant of it (mutation-checked.json, written by
	// `autobind -mutate`); candidates are the methods P1(b) closes, with their Pi-matched tests, for that run to check.
	mutation   map[string]map[string]bool
	candidates map[string]mutationCandidate
}

// mutationCheckedFile records the kills of `autobind -mutate`: an audit of the P1(b) closures of methods.
const mutationCheckedFile = "test/parity/interface-closure/autobind/mutation-checked.json"

// mutationCandidate is a method with the Pi-matched asserting tests to run against its mutant.
type mutationCandidate struct {
	sym   *sym
	tests []string
}

var (
	citeRe        = regexp.MustCompile(`([A-Za-z0-9_@.\-/]+\.(?:ts|tsx|mts|cts|js|mjs)):(\d+)`)
	citableExt    = map[string]bool{".ts": true, ".tsx": true, ".mts": true, ".cts": true, ".js": true, ".mjs": true}
	portsTestRe   = regexp.MustCompile(`(?m)^(?:Ports|Ported from) (packages/[a-z-]+/test/[A-Za-z0-9_./-]+\.test\.ts)\b`)
	describeRe    = regexp.MustCompile(`\bdescribe\(\s*(?:"((?:[^"\\\n]|\\.)*)"|'((?:[^'\\\n]|\\.)*)')\s*,`)
	nonAlnumRe    = regexp.MustCompile(`[^a-z0-9]+`)
	piTitleRe     = regexp.MustCompile(`\b(?:it|test)\(\s*(?:"((?:[^"\\\n]|\\.)*)"|'((?:[^'\\\n]|\\.)*)')\s*,`)
	literalPropRe = regexp.MustCompile(`^(?:"[^"]*"|'[^']*'|-?\d+(?:\.\d+)?|true|false|null)$`)
)

func newLibrary(root string) *library {
	lr := &library{root: root, libraries: map[string]bool{}, goDirs: map[string]map[string]bool{}, published: map[string]map[string][]string{}, pkgGo: map[string]map[string]bool{},
		pkgDirs: map[string]map[string]bool{}, mapped: map[string]bool{}, files: map[string][]string{},
		lines: map[string]int{}, texts: map[string]string{}, words: map[string]*regexp.Regexp{}, fset: token.NewFileSet(), tests: map[string]*ast.File{}, funcs: map[string]map[string]*ast.FuncDecl{},
		mutation: map[string]map[string]bool{}, candidates: map[string]mutationCandidate{}}
	var kills map[string][]string
	_ = readJSON(filepath.Join(root, mutationCheckedFile), &kills)
	for member, refs := range kills {
		lr.mutation[member] = map[string]bool{}
		for _, r := range refs {
			lr.mutation[member][r] = true
		}
	}
	base := filepath.Join(root, ".upstream", "current", "packages")
	entries, err := os.ReadDir(base)
	if err != nil {
		return lr
	}
	pmText, _ := os.ReadFile(filepath.Join(root, "docs", "parity", "PORT_MAP.md"))
	pm := rules.ParsePortMap(string(pmText))
	for up, gos := range pm {
		rest, ok := strings.CutPrefix(up, "packages/")
		if !ok {
			continue
		}
		pkg, _, _ := strings.Cut(rest, "/")
		if lr.pkgGo[pkg] == nil {
			lr.pkgGo[pkg], lr.pkgDirs[pkg] = map[string]bool{}, map[string]bool{}
		}
		for _, g := range gos {
			lr.mapped[g] = true
			lr.pkgGo[pkg][g] = true
			lr.pkgDirs[pkg][path.Dir(g)] = true
		}
	}
	for _, ent := range entries {
		pkg := ent.Name()
		if !ent.IsDir() {
			continue
		}
		dir := filepath.Join(base, pkg)
		_ = filepath.WalkDir(dir, func(p string, de fs.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // an unreadable file is simply not citable
			}
			if de.IsDir() && (de.Name() == "node_modules" || de.Name() == "dist") {
				return filepath.SkipDir
			}
			if !de.IsDir() && citableExt[path.Ext(de.Name())] {
				rel, _ := filepath.Rel(dir, p)
				lr.files[pkg] = append(lr.files[pkg], filepath.ToSlash(rel))
			}
			return nil
		})
		sort.Strings(lr.files[pkg])
		roots, ok := packageRoots(dir)
		if !ok {
			continue
		}
		lr.libraries[pkg] = true
		lr.goDirs[pkg], lr.published[pkg] = map[string]bool{}, map[string][]string{}
		for f := range exportClosure(dir, roots) {
			for _, g := range pm["packages/"+pkg+"/"+f] {
				lr.goDirs[pkg][path.Dir(g)] = true
				lr.published[pkg][g] = append(lr.published[pkg][g], f)
			}
		}
	}
	return lr
}

// packageRoots returns the source files (relative to the package directory) of every entry point the package.json publishes, and
// false when the package publishes no index.
func packageRoots(dir string) ([]string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil, false
	}
	var pj struct {
		Main    string          `json:"main"`
		Exports json.RawMessage `json:"exports"`
	}
	if json.Unmarshal(data, &pj) != nil {
		return nil, false
	}
	var targets []string
	var exports map[string]json.RawMessage
	if json.Unmarshal(pj.Exports, &exports) == nil {
		for _, v := range exports {
			var s string
			if json.Unmarshal(v, &s) == nil {
				targets = append(targets, s)
				continue
			}
			var cond map[string]string
			if json.Unmarshal(v, &cond) == nil {
				for _, k := range []string{"source", "types", "import", "default"} {
					if cond[k] != "" {
						targets = append(targets, cond[k])
						break
					}
				}
			}
		}
	}
	if len(targets) == 0 && pj.Main == "" {
		return nil, false
	}
	if pj.Main != "" {
		targets = append(targets, pj.Main)
	}
	seen := map[string]bool{}
	var roots []string
	for _, t := range targets {
		for _, f := range sourceFiles(dir, t) {
			if !seen[f] {
				seen[f] = true
				roots = append(roots, f)
			}
		}
	}
	sort.Strings(roots)
	return roots, len(roots) > 0
}

// sourceFiles maps a published path (./dist/x.js, ./dist/x.d.ts, ./src/x.ts, with an optional * wildcard) to the source files.
func sourceFiles(dir, target string) []string {
	t := strings.TrimPrefix(path.Clean(target), "./")
	if rest, ok := strings.CutPrefix(t, "dist/"); ok {
		t = "src/" + rest
	}
	for _, ext := range []string{".d.ts", ".js", ".mjs"} {
		if s, ok := strings.CutSuffix(t, ext); ok {
			t = s + ".ts"
			break
		}
	}
	matches, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(t)))
	var out []string
	for _, m := range matches {
		rel, err := filepath.Rel(dir, m)
		if err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
	}
	return out
}

// exportClosure follows `export ... from` and `export * from` relative specifiers from the roots.
func exportClosure(dir string, roots []string) map[string]bool {
	seen := map[string]bool{}
	queue := append([]string{}, roots...)
	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		if seen[f] {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			continue
		}
		seen[f] = true
		mod := rules.ParseModule(string(src))
		specs := append([]string{}, mod.Stars...)
		for _, e := range mod.Exports {
			if e.From != "" {
				specs = append(specs, e.From)
			}
		}
		for _, spec := range specs {
			if next := resolveRelative(dir, f, spec); next != "" && !seen[next] {
				queue = append(queue, next)
			}
		}
	}
	return seen
}

func resolveRelative(dir, from, spec string) string {
	if !strings.HasPrefix(spec, ".") {
		return ""
	}
	p := path.Join(path.Dir(from), spec)
	stem := p
	for _, ext := range []string{".js", ".ts", ".mjs", ".tsx"} {
		if s, ok := strings.CutSuffix(p, ext); ok {
			stem = s
			break
		}
	}
	for _, cand := range []string{stem + ".ts", stem + ".tsx", stem + "/index.ts", p} {
		if st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(cand))); err == nil && !st.IsDir() {
			return cand
		}
	}
	return ""
}

// libraryMember applies P1(b) to a field or method that no reachable production code uses. It returns the evidence when the row
// closes, and otherwise the condition that failed.
func (d *detector) libraryMember(s *sym, keys []string, tests []testEv) (*exerciseInfo, string) {
	if d.lib == nil {
		d.lib = newLibrary(d.ix.root)
	}
	lr := d.lib
	if d.row == nil {
		return nil, "L1: the member is not decided for a property row"
	}
	pkg := parseID(d.row.ID).Pkg
	if !lr.libraries[pkg] {
		return nil, "L1: upstream package " + pkg + " is not a library (no published index)"
	}
	ownerFile := ownerFileOf(d.ix, s)
	if !lr.goDirs[pkg][s.Dir] && !lr.goDirs[pkg][path.Dir(ownerFile)] && !aliasedInto(d.ix, s, lr.goDirs[pkg]) {
		return nil, "L1: Go package " + s.Dir + " holds no PORT_MAP port of a file the " + pkg + " package index publishes"
	}
	if !s.Exported() && !promotedOnto(s, parseID(d.row.ID).Name) {
		return nil, "L2: " + s.Target() + " is not exported"
	}
	if why := d.duplicateMember(s); why != "" {
		return nil, "L3: " + why
	}
	if s.Kind == "method" && !literalPropRe.MatchString(strings.TrimSpace(d.row.Shape.Type)) && d.constantStub(s) && !d.fixedUpstream(s) {
		return nil, "L4: " + s.Target() + " returns only constants (a stand-in)"
	}
	if s.Kind == "field" && !d.usedInOwnPackage(s) {
		return nil, "L5: no non-test code of " + s.Dir + " reads or sets " + s.Target()
	}
	double := d.testDoubleOnly(s)
	for _, t := range tests {
		if !t.Asserts {
			continue
		}
		key := strings.TrimPrefix(t.Ref, "test:")
		if double && !conformanceTest(key) {
			continue // L7: a test of a test double proves nothing about the production implementation
		}
		if lr.envGated(key) {
			continue // L6: a test that skips unless an environment variable is set does not run by default
		}
		if cite := lr.citation(key, pkg, d.row.Shape.Name); cite != "" || lr.nameCitation(key, d.row.Shape.Name) != "" || d.oracleTest(key) {
			if s.Kind == "method" {
				lr.candidates[s.Target()] = mutationCandidate{s, []string{t.Ref}}
			}
			return &exerciseInfo{Tests: []string{t.Ref}}, ""
		}
		if lr.mutation[s.Target()][t.Ref] && lr.fileCitation(key, pkg, d.row.Shape.Name) != "" {
			// L6, recorded kill: `autobind -mutate` saw a zero-body mutant fail this test, and its file cites the Pi source.
			return &exerciseInfo{Tests: []string{t.Ref}}, ""
		}
	}
	if ref := d.librarySuite(keys, pkg, d.row.Shape.Name); ref != "" && !double {
		return &exerciseInfo{Tests: []string{ref}}, ""
	}
	if double {
		if ref := d.conformanceEvidence(pkg, s); ref != "" {
			return &exerciseInfo{Tests: []string{ref}}, ""
		}
		return nil, "L7: " + s.Target() + " is a member of an interface whose only implementations are test doubles; only a cross-SDK conformance test (test/extension-conformance or TestConformance*) closes it"
	}
	return nil, "L6: no asserting test of " + s.Target() + " cites a Pi file:line of a file that names " + d.row.Shape.Name + ", runs the Pi oracle or runs a ported Pi suite that uses it"
}

// librarySuite returns the Go test that runs a ported Pi suite using the member (L6, suite): a non-test use of the member in a Go
// file that ports a published upstream file naming it, a function of that Go package within three calls above the use, and a test
// that calls the function with its *testing.T. It returns "" when there is none.
func (d *detector) librarySuite(keys []string, pkg, member string) string {
	lr := d.lib
	for _, k := range keys {
		for _, u := range d.ix.uses[k] {
			if u.Test || !slices.ContainsFunc(lr.published[pkg][u.File], func(f string) bool { return lr.word(member).MatchString(lr.text(pkg, f)) }) {
				continue
			}
			for _, entry := range d.ix.suiteEntries(u.File, u.Caller) {
				for _, tu := range d.ix.uses[entry.key] {
					if !tu.Test || !tu.Call || !isTestFunc(tu.key()) || lr.envGated(tu.key()) {
						continue
					}
					if fd := lr.testFunc(tu.key()); fd != nil && passesTestingT(fd, entry.name) {
						return "test:" + tu.key()
					}
				}
			}
		}
	}
	return ""
}

// suiteFn is a function of a suite's Go package: its declaration key and its name.
type suiteFn struct{ key, name string }

// suiteEntries returns the function caller of file and the functions of the same Go package that call it within three calls, with
// their declaration keys.
func (ix *index) suiteEntries(file, caller string) []suiteFn {
	dir := path.Dir(file)
	info := ix.pkgs[dir]
	if info == nil || info.Types == nil {
		return nil
	}
	declOf := func(name string) types.Object {
		recv, method, isMethod := strings.Cut(name, ".")
		if !isMethod {
			return info.Types.Scope().Lookup(name)
		}
		tn, _ := info.Types.Scope().Lookup(recv).(*types.TypeName)
		if tn == nil {
			return nil
		}
		obj, _, _ := types.LookupFieldOrMethod(tn.Type(), true, info.Types, method)
		return obj
	}
	var out []suiteFn
	seen := map[string]bool{}
	frontier := []string{caller}
	for depth := 0; depth <= 3 && len(frontier) > 0; depth++ {
		var next []string
		for _, name := range frontier {
			obj := declOf(name)
			if obj == nil || seen[name] {
				continue
			}
			seen[name] = true
			key := ix.declKey(obj)
			out = append(out, suiteFn{key, obj.Name()})
			for _, u := range ix.uses[key] {
				if !u.Test && path.Dir(u.File) == dir {
					next = append(next, u.Caller)
				}
			}
		}
		frontier = next
	}
	return out
}

// passesTestingT reports whether the test function calls a function or method named name with the test's *testing.T parameter as an
// argument, so failures the callee reports reach the test.
func passesTestingT(fd *ast.FuncDecl, name string) bool {
	if fd.Type.Params == nil || len(fd.Type.Params.List) == 0 || len(fd.Type.Params.List[0].Names) == 0 {
		return false
	}
	t := fd.Type.Params.List[0].Names[0].Name
	found := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || found {
			return !found
		}
		callee := ""
		switch f := call.Fun.(type) {
		case *ast.Ident:
			callee = f.Name
		case *ast.SelectorExpr:
			callee = f.Sel.Name
		}
		found = callee == name && slices.ContainsFunc(call.Args, func(a ast.Expr) bool {
			id, ok := a.(*ast.Ident)
			return ok && id.Name == t
		})
		return !found
	})
	return found
}

// duplicateMember applies L3.
func (d *detector) duplicateMember(s *sym) string {
	info := d.ix.pkgs[s.Dir]
	if info == nil {
		return ""
	}
	owner, _ := info.Types.Scope().Lookup(s.Owner).(*types.TypeName)
	if owner == nil {
		return ""
	}
	siblings := d.ix.members(owner, s.Dir, 0)
	for _, m := range siblings {
		if m.Key != s.Key && m.Kind == "field" && s.Kind == "field" && m.Exported() && norm(m.Name) == norm(s.Name) {
			return s.Target() + " and " + m.Target() + " are the same field"
		}
	}
	if s.Kind == "method" {
		if fd := d.lib.funcDecl(d.ix, s); fd != nil {
			if target := forwardTarget(fd); target != "" && target != s.Name {
				for _, m := range siblings {
					if m.Name == target && m.Exported() {
						return s.Target() + " only forwards to " + m.Target()
					}
				}
			}
		}
	}
	want := memberTypeString(s.Obj)
	if fn, ok := info.Types.Scope().Lookup(s.Name).(*types.Func); ok && s.Kind == "method" && fn.Exported() {
		if file := d.ix.file(fn); memberTypeString(fn) == want && d.ix.reach[file+"#"+fn.Name()] {
			return s.Target() + " repeats the wired function " + file + "#" + fn.Name()
		}
	}
	for _, nt := range d.ix.named {
		if nt.obj.Name() != s.Owner || nt.dir == s.Dir {
			continue
		}
		for _, m := range d.ix.members(nt.obj, nt.dir, 0) {
			if m.Name != s.Name || m.Kind != s.Kind || memberTypeString(m.Obj) != want || m.Dir == s.Dir {
				continue
			}
			if !d.lib.samePort(d.ix, m, d.pkg()) {
				continue // another upstream package's port of a type of the same name: upstream declares both
			}
			wired := d.ix.reach[m.File+"#"+m.Qualified()]
			if !wired {
				reachable, _ := d.ix.prodFor([]string{m.Key}, m.Dir, selfExclude(m.Qualified()))
				wired = len(reachable) > 0
			}
			if wired {
				return s.Target() + " duplicates the wired " + m.Target()
			}
			if m.Exported() {
				return s.Target() + " duplicates the exported " + m.Target()
			}
		}
	}
	return ""
}

func (d *detector) pkg() string { return parseID(d.row.ID).Pkg }

// samePort reports whether a Go member lives in a file (or its owner's file) that PORT_MAP records for a file of upstream package
// pkg, or in a file PORT_MAP records for no upstream file at all: a declaration that ports another package is a separate port.
func (lr *library) samePort(ix *index, m *sym, pkg string) bool {
	ownerFile := ownerFileOf(ix, m)
	if lr.pkgGo[pkg][m.File] || lr.pkgGo[pkg][ownerFile] {
		return true
	}
	return !lr.mapped[m.File] && !lr.mapped[ownerFile]
}

func ownerFileOf(ix *index, m *sym) string {
	if info := ix.pkgs[m.Dir]; info != nil && m.Owner != "" {
		if o := info.Types.Scope().Lookup(m.Owner); o != nil {
			return ix.file(o)
		}
	}
	return ""
}

// memberTypeString prints a member's type with package names only, so the same declaration in two packages compares equal.
func memberTypeString(obj types.Object) string {
	t := obj.Type()
	if sig, ok := t.(*types.Signature); ok {
		t = types.NewSignatureType(nil, nil, nil, sig.Params(), sig.Results(), sig.Variadic())
	}
	return types.TypeString(t, func(p *types.Package) string { return p.Name() })
}

// forwardTarget returns X when the method body is one statement `return recv.X` or `return recv.X(...)` (or the call as a statement)
// over the method's own receiver.
func forwardTarget(fd *ast.FuncDecl) string {
	if fd.Body == nil || len(fd.Body.List) != 1 || fd.Recv == nil || len(fd.Recv.List) != 1 || len(fd.Recv.List[0].Names) != 1 {
		return ""
	}
	recv := fd.Recv.List[0].Names[0].Name
	var e ast.Expr
	switch st := fd.Body.List[0].(type) {
	case *ast.ReturnStmt:
		if len(st.Results) != 1 {
			return ""
		}
		e = st.Results[0]
	case *ast.ExprStmt:
		e = st.X
	default:
		return ""
	}
	if call, ok := e.(*ast.CallExpr); ok {
		e = call.Fun
	}
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if id, ok := sel.X.(*ast.Ident); !ok || id.Name != recv {
		return ""
	}
	return sel.Sel.Name
}

// constantStub applies L4: one return statement whose every result is a constant, nil, or an empty composite literal.
func (d *detector) constantStub(s *sym) bool {
	if d.lib == nil {
		d.lib = newLibrary(d.ix.root)
	}
	fd := d.lib.funcDecl(d.ix, s)
	if fd == nil || fd.Body == nil || len(fd.Body.List) != 1 {
		return false
	}
	ret, ok := fd.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) == 0 {
		return false
	}
	info := d.ix.pkgs[s.Dir].Info
	for _, r := range ret.Results {
		if !constantExpr(info, r) {
			return false
		}
	}
	return true
}

// fixedUpstream reports whether the upstream class fixes the member to the one constant the Go method returns, so the constant is
// the faithful port and not a stand-in: an Error subclass whose constructor passes a string literal to super is that `message`; a
// constructor whose super call has no options argument and that never assigns this.cause leaves `cause` undefined (Go nil); and a
// `readonly id = "node:local"` initializer is that value.
func (d *detector) fixedUpstream(s *sym) bool {
	fd := d.lib.funcDecl(d.ix, s)
	if fd == nil || fd.Body == nil || len(fd.Body.List) != 1 {
		return false
	}
	ret, ok := fd.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return false
	}
	tv := d.ix.pkgs[s.Dir].Info.Types[ret.Results[0]]
	id := parseID(d.row.ID)
	body := d.lib.classBody(id.Pkg, id.Name)
	if body == "" {
		return false
	}
	member := d.row.Shape.Name
	if member == "cause" {
		m := superCallRe.FindStringSubmatch(body)
		return tv.IsNil() && m != nil && len(splitTop(m[1], ",")) == 1 && !strings.Contains(body, "this.cause")
	}
	if tv.IsNil() && neverRejects(body, member) {
		return true
	}
	if tv.Value == nil || tv.Value.Kind() != constant.String {
		return false
	}
	want := constant.StringVal(tv.Value)
	if member == "message" {
		m := superCallRe.FindStringSubmatch(body)
		if m == nil {
			return false
		}
		lit, err := strconv.Unquote(strings.TrimSpace(splitTop(m[1], ",")[0]))
		return err == nil && lit == want && !strings.Contains(body, "this.message")
	}
	m := regexp.MustCompile(`(?m)^\s*(?:readonly\s+)?` + regexp.QuoteMeta(member) + `\s*(?::[^=;]+)?=\s*("(?:[^"\\]|\\.)*")\s*;`).FindStringSubmatch(body)
	if m == nil {
		return false
	}
	lit, err := strconv.Unquote(m[1])
	return err == nil && lit == want
}

// neverRejects reports whether the upstream method `async member(): Promise<void> { await this.q; }` only awaits a queue that
// cannot reject: q starts as `Promise.resolve()` and every `this.q = ...` assignment ends its chain in a `.catch(...)` whose
// handler throws nothing (settings-manager.ts:684-694, flush at :777). Such a flush always resolves, so a Go Flush returning nil
// is its result, not a stand-in (Go completes the queued work before the write call returns).
func neverRejects(body, member string) bool {
	m := regexp.MustCompile(`\basync\s+` + regexp.QuoteMeta(member) + `\s*\(\s*\)\s*:\s*Promise<void>\s*\{\s*await\s+this\.(\w+)\s*;\s*\}`).FindStringSubmatch(body)
	if m == nil {
		return false
	}
	q := regexp.QuoteMeta(m[1])
	if !regexp.MustCompile(`\b` + q + `\s*(?::[^=;]+)?=\s*Promise\.resolve\(\)\s*;`).MatchString(body) {
		return false
	}
	assign := regexp.MustCompile(`this\.` + q + `\s*=[^=]`)
	locs := assign.FindAllStringIndex(body, -1)
	if len(locs) == 0 {
		return false
	}
	for _, loc := range locs {
		stmt := topLevelStatement(body[loc[1]-1:])
		i := strings.LastIndex(stmt, ".catch(")
		if i < 0 || depthAt(stmt, i) != 0 || strings.Contains(stmt[i:], "throw") {
			return false
		}
		if end := closingParen(stmt[i+len(".catch("):]); end < 0 || strings.TrimSpace(stmt[i+len(".catch(")+end+1:]) != "" {
			return false // a call after the catch can reject again
		}
	}
	return true
}

// topLevelStatement returns text up to its first `;` outside brackets, or all of it.
func topLevelStatement(text string) string {
	depth := 0
	for i, r := range text {
		switch r {
		case '(', '{', '[':
			depth++
		case ')', '}', ']':
			depth--
		case ';':
			if depth == 0 {
				return text[:i]
			}
		}
	}
	return text
}

// closingParen returns the offset of the `)` that closes a call whose arguments start text, or -1.
func closingParen(text string) int {
	depth := 0
	for i, r := range text {
		switch r {
		case '(', '{', '[':
			depth++
		case ')', '}', ']':
			if depth--; depth < 0 {
				return i
			}
		}
	}
	return -1
}

// depthAt returns the bracket depth of text before offset i.
func depthAt(text string, i int) int {
	depth := 0
	for _, r := range text[:i] {
		switch r {
		case '(', '{', '[':
			depth++
		case ')', '}', ']':
			depth--
		}
	}
	return depth
}

// superCallRe captures the arguments of the first super(...) call of a class body (no nested parentheses in the arguments).
var superCallRe = regexp.MustCompile(`\bsuper\(([^()]*)\)`)

// classBody returns the text between the braces of `class name` in the upstream package's sources, or "" when it is not found.
func (lr *library) classBody(pkg, name string) string {
	head := regexp.MustCompile(`\bclass\s+` + regexp.QuoteMeta(name) + `\b[^{]*\{`)
	for _, f := range lr.files[pkg] {
		if !strings.HasSuffix(f, ".ts") || strings.HasSuffix(f, ".test.ts") {
			continue
		}
		text := lr.text(pkg, f)
		loc := head.FindStringIndex(text)
		if loc == nil {
			continue
		}
		depth := 1
		for i := loc[1]; i < len(text); i++ {
			switch text[i] {
			case '{':
				depth++
			case '}':
				if depth--; depth == 0 {
					return text[loc[1]:i]
				}
			}
		}
	}
	return ""
}

func constantExpr(info *types.Info, e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return constantExpr(info, x.X)
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			return constantExpr(info, x.X)
		}
	case *ast.CompositeLit:
		return len(x.Elts) == 0
	}
	if tv, ok := info.Types[e]; ok && (tv.Value != nil || tv.IsNil()) {
		return true
	}
	return false
}

// funcDecl returns the declaration of a method symbol from its package's non-test syntax.
func (lr *library) funcDecl(ix *index, s *sym) *ast.FuncDecl {
	byKey, ok := lr.funcs[s.Dir]
	if !ok {
		byKey = map[string]*ast.FuncDecl{}
		if info := ix.pkgs[s.Dir]; info != nil {
			for _, f := range info.Files {
				for _, decl := range f.Decls {
					if fd, ok := decl.(*ast.FuncDecl); ok {
						if obj := info.Info.Defs[fd.Name]; obj != nil {
							byKey[ix.declKey(obj)] = fd
						}
					}
				}
			}
		}
		lr.funcs[s.Dir] = byKey
	}
	if fn, ok := s.Obj.(*types.Func); ok {
		return byKey[ix.declKey(fn)]
	}
	return nil
}

// usedInOwnPackage applies L5. The upstream package is the unit of ownership, so a use counts in any Go directory that ports one of
// its files. A JSON-tagged field of an owner that one of those directories uses is read and set by the encoder when the field's Go
// package encodes JSON (it imports encoding/json or the owner has its own MarshalJSON or UnmarshalJSON).
func (d *detector) usedInOwnPackage(s *sym) bool {
	dirs := d.lib.pkgDirs[d.pkg()]
	inPkg := func(u use) bool { return !u.Test && (path.Dir(u.File) == s.Dir || dirs[path.Dir(u.File)]) }
	if slices.ContainsFunc(d.ix.uses[s.Key], inPkg) {
		return true
	}
	if s.JSON == "" {
		return false
	}
	info := d.ix.pkgs[s.Dir]
	owner, _ := info.Types.Scope().Lookup(s.Owner).(*types.TypeName)
	if owner == nil {
		return false
	}
	usedBy := func(tn *types.TypeName) bool {
		for _, u := range d.ix.uses[d.ix.declKey(tn)] {
			if inPkg(u) && !strings.HasPrefix(u.Caller, tn.Name()+".") {
				return true
			}
		}
		return false
	}
	if d.encodesJSON(s) && usedBy(owner) {
		return true
	}
	// L5t: a data-only schema struct (no methods) is read through the typed builder that takes the type holding it: the field is
	// read when non-test code of the package consumes a type that contains the owner by fields, elements or embedding, and the
	// encoder serializes the whole value (LEAD-RULINGS-1520, lg-imla-2 census 3).
	if types.NewMethodSet(types.NewPointer(owner.Type())).Len() != 0 {
		return false
	}
	for _, name := range info.Types.Scope().Names() {
		root, _ := info.Types.Scope().Lookup(name).(*types.TypeName)
		if root == nil || !containsType(root.Type(), owner.Type(), map[types.Type]bool{}) || root == owner && !jsonTaggedFields(owner.Type()) {
			continue // a builder that takes the schema struct itself counts when every field of it is serializable data (L5t)
		}
		if usedBy(root) {
			return true
		}
	}
	return false
}

// jsonTaggedFields reports whether t is a struct whose every field carries a json tag: serializable data with no behaviour.
func jsonTaggedFields(t types.Type) bool {
	st, ok := t.Underlying().(*types.Struct)
	if !ok || st.NumFields() == 0 {
		return false
	}
	for i := range st.NumFields() {
		if reflect.StructTag(st.Tag(i)).Get("json") == "" {
			return false
		}
	}
	return true
}

// containsType reports whether a value of type from holds a value of type target through struct fields, embedded fields, pointers,
// slices, arrays or maps.
func containsType(from, target types.Type, seen map[types.Type]bool) bool {
	if seen[from] {
		return false
	}
	seen[from] = true
	if types.Identical(from, target) {
		return true
	}
	switch t := from.(type) {
	case *types.Named:
		return containsType(t.Underlying(), target, seen)
	case *types.Pointer:
		return containsType(t.Elem(), target, seen)
	case *types.Slice:
		return containsType(t.Elem(), target, seen)
	case *types.Array:
		return containsType(t.Elem(), target, seen)
	case *types.Map:
		return containsType(t.Elem(), target, seen)
	case *types.Struct:
		for f := range t.Fields() {
			if containsType(f.Type(), target, seen) {
				return true
			}
		}
	}
	return false
}

func (d *detector) encodesJSON(s *sym) bool {
	info := d.ix.pkgs[s.Dir]
	if info == nil {
		return false
	}
	for _, imp := range info.Types.Imports() {
		if imp.Path() == "encoding/json" {
			return true
		}
	}
	owner, _ := info.Types.Scope().Lookup(s.Owner).(*types.TypeName)
	if owner == nil {
		return false
	}
	ms := types.NewMethodSet(types.NewPointer(owner.Type()))
	return ms.Lookup(owner.Pkg(), "MarshalJSON") != nil || ms.Lookup(owner.Pkg(), "UnmarshalJSON") != nil
}

// oracleTest reports whether the test, its file, or a test-file helper it calls within three calls is named for the Pi oracle.
func (d *detector) oracleTest(key string) bool {
	file, name, _ := strings.Cut(key, "#")
	if strings.Contains(strings.ToLower(name), "oracle") || strings.Contains(strings.ToLower(path.Base(file)), "oracle") {
		return true
	}
	seen := map[string]bool{key: true}
	frontier := []string{key}
	for depth := 0; depth < 3 && len(frontier) > 0; depth++ {
		var next []string
		for _, k := range frontier {
			fi := d.ix.fns[k]
			if fi == nil {
				continue
			}
			callees := make([]string, 0, len(fi.callees))
			for c := range fi.callees {
				callees = append(callees, c)
			}
			sort.Strings(callees)
			for _, c := range callees {
				if seen[c] {
					continue
				}
				seen[c] = true
				_, cname, _ := strings.Cut(c, "#")
				if strings.Contains(strings.ToLower(cname), "oracle") {
					return true
				}
				next = append(next, c)
			}
		}
		frontier = next
	}
	return false
}

// envGated reports whether the test function key (file#Name) skips when an environment variable is unset or set: an if statement whose
// condition reads os.Getenv or os.LookupEnv and whose body calls t.Skip, t.Skipf or t.SkipNow. Such a test proves nothing in a
// default run (env TestSSHToThisMachinesServer needs PI_ENV_SSH_HOST).
func (lr *library) envGated(key string) bool {
	fd := lr.testFunc(key)
	if fd == nil || fd.Body == nil {
		return false
	}
	gated := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || gated {
			return !gated
		}
		reads := callsSelector(ifs.Cond, "os", "Getenv", "LookupEnv") || ifs.Init != nil && callsSelector(ifs.Init, "os", "Getenv", "LookupEnv")
		if reads && callsSelector(ifs.Body, "t", "Skip", "Skipf", "SkipNow") {
			gated = true
		}
		return !gated
	})
	return gated
}

// callsSelector reports whether n contains a call x.name(...) with x the identifier pkg and name one of names.
func callsSelector(n ast.Node, pkg string, names ...string) bool {
	found := false
	ast.Inspect(n, func(c ast.Node) bool {
		call, ok := c.(*ast.CallExpr)
		if !ok || found {
			return !found
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == pkg && slices.Contains(names, sel.Sel.Name) {
				found = true
			}
		}
		return !found
	})
	return found
}

// testFunc returns the declaration of the top-level test function key (file#Name), parsing and caching its file.
func (lr *library) testFunc(key string) *ast.FuncDecl {
	file, name, _ := strings.Cut(key, "#")
	f, ok := lr.tests[file]
	if !ok {
		f, _ = parser.ParseFile(lr.fset, filepath.Join(lr.root, filepath.FromSlash(file)), nil, parser.ParseComments|parser.SkipObjectResolution)
		lr.tests[file] = f
	}
	if f == nil {
		return nil
	}
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == name {
			return fd
		}
	}
	return nil
}

// citation returns the first upstream file:line cited in the doc comment or body comments of the test function key (file#Name)
// whose line lies inside the cited file and whose file names the upstream member: the citation then points at Pi source about it. A
// bare or partial path resolves in the member's package first, then in every package.
func (lr *library) citation(key, pkg, member string) string {
	file, name, _ := strings.Cut(key, "#")
	f := lr.testFile(file)
	if f == nil {
		return ""
	}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || fd.Name.Name != name {
			continue
		}
		start := fd.Pos()
		if fd.Doc != nil {
			start = fd.Doc.Pos()
		}
		for _, cg := range f.Comments {
			if cg.Pos() < start || cg.End() > fd.End() {
				continue
			}
			for _, m := range citeRe.FindAllStringSubmatch(cg.Text(), -1) {
				line, _ := strconv.Atoi(m[2])
				if lr.cites(pkg, m[1], line, member) {
					return m[0]
				}
			}
		}
	}
	return ""
}

// fileCitation is citation over every comment of the test's file.
func (lr *library) fileCitation(key, pkg, member string) string {
	file, _, _ := strings.Cut(key, "#")
	f := lr.testFile(file)
	if f == nil {
		return ""
	}
	for _, cg := range f.Comments {
		for _, m := range citeRe.FindAllStringSubmatch(cg.Text(), -1) {
			line, _ := strconv.Atoi(m[2])
			if lr.cites(pkg, m[1], line, member) {
				return m[0]
			}
		}
	}
	return ""
}

// nameCitation returns the upstream test case a test names: its file declares `Ports packages/<pkg>/test/<file>.test.ts`, that file
// names the member, and the test function is named after one of its it or test cases or calls t.Run with that case's exact title. The title identifies one upstream case as a file:line would.
func (lr *library) nameCitation(key, member string) string {
	file, name, _ := strings.Cut(key, "#")
	f := lr.testFile(file)
	if f == nil || member == "" {
		return ""
	}
	titles := map[string]string{}
	names := map[string]string{} // normalized describe+title and title -> Pi file, for a test function named after the case
	for _, cg := range f.Comments {
		for _, m := range portsTestRe.FindAllStringSubmatch(cg.Text(), -1) {
			pkg, rel, _ := strings.Cut(strings.TrimPrefix(m[1], "packages/"), "/")
			text := lr.text(pkg, rel)
			if !lr.word(member).MatchString(text) {
				continue
			}
			var describes []string
			for _, d := range describeRe.FindAllStringSubmatch(text, -1) {
				words := strings.Fields(d[1] + d[2])
				for i := range words {
					describes = append(describes, normTitle(strings.Join(words[i:], " "))) // a Go name may drop the leading words
				}
			}
			for _, t := range piTitleRe.FindAllStringSubmatch(text, -1) {
				if title := t[1] + t[2]; title != "" && !strings.Contains(title, `\`) {
					titles[title] = m[1]
					names[normTitle(title)] = m[1]
					for _, d := range describes {
						names[d+normTitle(title)] = m[1]
					}
				}
			}
		}
	}
	if len(titles) == 0 {
		return ""
	}
	if goName, ok := strings.CutPrefix(name, "Test"); ok && names[normTitle(goName)] != "" {
		return names[normTitle(goName)] + ": " + name
	}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || fd.Name.Name != name || fd.Body == nil {
			continue
		}
		found := ""
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if found != "" || !ok || len(call.Args) < 2 {
				return found == ""
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			lit, isLit := call.Args[0].(*ast.BasicLit)
			if !ok || sel.Sel.Name != "Run" || !isLit || lit.Kind != token.STRING {
				return true
			}
			if v, err := strconv.Unquote(lit.Value); err == nil && titles[v] != "" {
				found = titles[v] + ": " + v
			}
			return true
		})
		return found
	}
	return ""
}

// normTitle keeps the lower-case letters and digits of a case title or a Go test name.
func normTitle(s string) string { return nonAlnumRe.ReplaceAllString(strings.ToLower(s), "") }

// word matches member as a whole word. A word boundary applies only at an end that is a word character: a symbol-keyed member
// such as [Symbol.asyncIterator] starts and ends with a bracket, next to which `\b` would require a word character.
func (lr *library) word(member string) *regexp.Regexp {
	word, ok := lr.words[member]
	if !ok {
		// \b needs a word character beside it, so a name that starts or ends with a symbol ([Symbol.asyncIterator]) takes none there.
		pattern := regexp.QuoteMeta(member)
		if member != "" && isWordByte(member[0]) {
			pattern = `\b` + pattern
		}
		if member != "" && isWordByte(member[len(member)-1]) {
			pattern += `\b`
		}
		word = regexp.MustCompile(pattern)
		lr.words[member] = word
	}
	return word
}

// promotedOnto reports whether s, an exported member of an unexported type, is promoted through embedding onto the exported Go
// type named after the row's upstream owner: TS inheritance is Go embedding (`class NodeSqliteDatabase extends
// NodeSqliteExecutor`), so a caller reaches the member as a member of the exported type.
func promotedOnto(s *sym, owner string) bool {
	if s.Obj == nil || s.Obj.Pkg() == nil || !token.IsExported(s.Name) || !token.IsExported(owner) {
		return false
	}
	tn, ok := s.Obj.Pkg().Scope().Lookup(owner).(*types.TypeName)
	if !ok {
		return false
	}
	obj, _, _ := types.LookupFieldOrMethod(types.NewPointer(tn.Type()), true, s.Obj.Pkg(), s.Name)
	return obj == s.Obj
}

// aliasedInto reports whether a Go directory that ports the package declares an exported alias of the member's owner type
// (`type RoutedServerPresentation = services.RoutedServerPresentation` in the server port): the member is reached through the
// alias as public API of that package, as Pi's `export interface` in the published file, although the type is declared elsewhere.
func aliasedInto(ix *index, s *sym, dirs map[string]bool) bool {
	if s.Owner == "" || s.Obj == nil || s.Obj.Pkg() == nil || !token.IsExported(s.Owner) {
		return false
	}
	owner, ok := s.Obj.Pkg().Scope().Lookup(s.Owner).(*types.TypeName)
	if !ok {
		return false
	}
	for _, dir := range slices.Sorted(maps.Keys(dirs)) {
		pi := ix.pkgs[dir]
		if pi == nil || pi.Types == nil {
			continue
		}
		scope := pi.Types.Scope()
		for _, name := range scope.Names() {
			if tn, ok := scope.Lookup(name).(*types.TypeName); ok && tn.Exported() && types.Identical(tn.Type(), owner.Type()) { // only an alias is identical to the owner
				return true
			}
		}
	}
	return false
}

// isWordByte reports whether b is an ASCII word character, as `\b` defines one.
func isWordByte(b byte) bool {
	return b == '_' || '0' <= b && b <= '9' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z'
}

func (lr *library) testFile(file string) *ast.File {
	f, ok := lr.tests[file]
	if !ok {
		f, _ = parser.ParseFile(lr.fset, filepath.Join(lr.root, filepath.FromSlash(file)), nil, parser.ParseComments|parser.SkipObjectResolution)
		lr.tests[file] = f
	}
	return f
}

// cites reports whether cited (a path or a path suffix) names an upstream file that has the line and mentions member as a word.
// A path into the mirror, .upstream/current/packages/<pkg>/<file>, is an ordinary citation. A path into a pinned mirror,
// .upstream/<version>/..., never counts: CI checks out only the current mirror and the pinned version, so the evidence must
// read the current Pi source.
func (lr *library) cites(pkg, cited string, line int, member string) bool {
	if line < 1 || member == "" {
		return false
	}
	if i := strings.Index(cited, ".upstream/"); i >= 0 {
		rel, ok := strings.CutPrefix(cited[i+len(".upstream/"):], "current/")
		if !ok || !strings.HasPrefix(rel, "packages/") {
			return false
		}
		cited = rel
	}
	pkgs := []string{pkg}
	if rest, ok := strings.CutPrefix(cited, "packages/"); ok {
		p, r, _ := strings.Cut(rest, "/")
		pkgs, cited = []string{p}, r
	} else {
		for _, p := range sortedKeys(lr.pkgSet()) {
			if p != pkg {
				pkgs = append(pkgs, p)
			}
		}
	}
	word := lr.word(member)
	for _, p := range pkgs {
		for _, f := range lr.files[p] {
			if f != cited && !strings.HasSuffix(f, "/"+cited) {
				continue
			}
			if text := lr.text(p, f); lr.lineCount(p, f) >= line && word.MatchString(text) {
				return true
			}
		}
	}
	return false
}

func (lr *library) pkgSet() map[string]bool {
	out := map[string]bool{}
	for p := range lr.files {
		out[p] = true
	}
	return out
}

func (lr *library) text(pkg, f string) string {
	key := pkg + "/" + f
	if t, ok := lr.texts[key]; ok {
		return t
	}
	data, _ := os.ReadFile(filepath.Join(lr.root, ".upstream", "current", "packages", pkg, filepath.FromSlash(f)))
	lr.texts[key] = string(data)
	return lr.texts[key]
}

func (lr *library) lineCount(pkg, f string) int {
	key := pkg + "/" + f
	if n, ok := lr.lines[key]; ok {
		return n
	}
	data := lr.text(pkg, f)
	n := strings.Count(data, "\n")
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	lr.lines[key] = n
	return n
}

// methodDecl returns the declaration of a method symbol, for the mutant `autobind -mutate` writes.
func (d *detector) methodDecl(s *sym) *ast.FuncDecl {
	info := d.ix.pkgs[s.Dir]
	if info == nil {
		return nil
	}
	for _, f := range info.Files {
		for _, decl := range f.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok && fd.Recv != nil && len(fd.Recv.List) == 1 && fd.Name.Name == s.Name && recvTypeName(fd.Recv.List[0].Type) == s.Owner {
				return fd
			}
		}
	}
	return nil
}

func recvTypeName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.StarExpr:
		return recvTypeName(v.X)
	case *ast.IndexExpr:
		return recvTypeName(v.X)
	case *ast.IndexListExpr:
		return recvTypeName(v.X)
	case *ast.ParenExpr:
		return recvTypeName(v.X)
	case *ast.Ident:
		return v.Name
	}
	return ""
}

// conformanceTest reports whether a test ref is a cross-SDK conformance test.
func conformanceTest(key string) bool {
	file, name, _ := strings.Cut(key, "#")
	return strings.HasPrefix(file, "test/extension-conformance/") || strings.HasPrefix(name, "TestConformance")
}

// testDoubleOnly reports whether s is a method of a Go interface that has implementations and every one is a test double.
func (d *detector) testDoubleOnly(s *sym) bool {
	if s.Kind != "method" || s.Owner == "" {
		return false
	}
	info := d.ix.pkgs[s.Dir]
	if info == nil {
		return false
	}
	owner, _ := info.Types.Scope().Lookup(s.Owner).(*types.TypeName)
	if owner == nil {
		return false
	}
	iface, ok := owner.Type().Underlying().(*types.Interface)
	if !ok {
		return false
	}
	doubles := 0
	for _, nt := range d.ix.named {
		if nt.obj == owner {
			continue
		}
		if _, isIface := nt.obj.Type().Underlying().(*types.Interface); isIface {
			continue
		}
		if !types.Implements(nt.obj.Type(), iface) && !types.Implements(types.NewPointer(nt.obj.Type()), iface) {
			continue
		}
		if !d.testDouble(nt) {
			return false
		}
		doubles++
	}
	return doubles > 0
}

// testDouble reports whether a named type exists only to stand in for a production type in tests.
func (d *detector) testDouble(nt namedType) bool {
	name := nt.obj.Name()
	file := d.ix.file(nt.obj)
	return strings.HasSuffix(file, "_test.go") || testSupportDir(nt.dir) || strings.HasPrefix(name, "Fake") || strings.HasPrefix(name, "Mock") || strings.HasPrefix(name, "Stub")
}

// conformanceEvidence finds the cross-SDK conformance test that exercises a member of a double-only interface (L7): a test function
// of test/extension-conformance (or named TestConformance*) whose body uses the upstream member name as an identifier or string literal (and, for an overload row, the
// overload's literal such as the event name) as a word and whose comments cite a Pi file:line of a file that names the member. The
// conformance suites drive the same capability through every SDK and the host wire, which is where Pig implements the interface.
func (d *detector) conformanceEvidence(pkg string, s *sym) string {
	lr := d.lib
	member := d.row.Shape.Name
	if member == "" {
		member = parseID(d.row.ID).Member
	}
	if member == "" {
		return ""
	}
	overload := parseID(d.row.ID).Call
	if _, err := strconv.Atoi(overload); err == nil {
		overload = ""
	}
	keys := make([]string, 0, len(d.ix.fns))
	for k := range d.ix.fns {
		if conformanceTest(k) && isTestFunc(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if lr.envGated(key) {
			continue
		}
		text := lr.testCode(key)
		if text == "" || !regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(member)+`\b`).MatchString(text) || overload != "" && !lr.word(overload).MatchString(text) {
			continue
		}
		if lr.citation(key, pkg, member) != "" {
			return "test:" + key
		}
	}
	return ""
}

// testCode returns the identifiers and string literals of the body of test function key (file#Name), joined by newlines: the names the
// test uses, with no comment text (a comment that names a member is no use of it).
func (lr *library) testCode(key string) string {
	fd := lr.testFunc(key)
	if fd == nil || fd.Body == nil {
		return ""
	}
	var b strings.Builder
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			b.WriteString(x.Name)
			b.WriteByte('\n')
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				b.WriteString(x.Value)
				b.WriteByte('\n')
			}
		}
		return true
	})
	return b.String()
}
