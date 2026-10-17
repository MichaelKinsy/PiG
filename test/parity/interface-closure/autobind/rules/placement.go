package rules

import (
	"go/types"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Placement rule family: which Go package and which Go form (type, function, method) stands for an upstream symbol.
//
//	P1 a name exported by a barrel (index.ts) is defined where the barrel's re-export chain ends: `export { A as B } from`,
//	   `export * from`, `export * as ns from` and `export { A }` of an imported name are followed to a declaration
//	P2 the Go package of a symbol is a directory that holds the Go files PORT_MAP records for the defining upstream file; the
//	   package seeds of the upstream package follow, in order; the longest recorded prefix of a path in the table decides a file
//	   PORT_MAP does not list
//	P3 an upstream free function whose first parameter is a type T is a Go method on T, and an upstream method of a class C is a
//	   Go function whose first parameter (after a context) is C; the other parameters follow the function rules
//	P4 a generic upstream declaration is a Go declaration with the same number of type parameters, or a Go concrete type that a
//	   documented instantiation table names for it

// Export is one name a module makes available: declared in it, or re-exported from another module.
type Export struct {
	Name     string // the exported name
	Orig     string // the name in the source module (Name when it is not renamed); empty for a namespace re-export
	From     string // the module specifier the name comes from; empty for a declaration
	TypeOnly bool
}

// Module is the parsed export surface of one TypeScript module.
type Module struct {
	Exports []Export
	Stars   []string // `export * from "x"` specifiers
}

var (
	commentRe   = regexp.MustCompile(`(?s)/\*.*?\*/|//[^\n]*`)
	exportFrom  = regexp.MustCompile(`(?m)^export\s+(type\s+)?\{([^}]*)\}\s*from\s*["']([^"']+)["']`)
	exportLocal = regexp.MustCompile(`(?m)^export\s+(type\s+)?\{([^}]*)\}\s*;`)
	exportStar  = regexp.MustCompile(`(?m)^export\s+\*\s+(?:as\s+(\w+)\s+)?from\s*["']([^"']+)["']`)
	importNamed = regexp.MustCompile(`(?m)^import\s+(?:type\s+)?\{([^}]*)\}\s*from\s*["']([^"']+)["']`)
	declaration = regexp.MustCompile(`(?m)^export\s+(?:declare\s+)?(?:default\s+)?(?:abstract\s+)?(?:async\s+)?(?:const\s+enum|class|interface|type|function\*?|const|let|var|enum|namespace)\s+(\w+)`)
)

// ParseModule extracts the export surface of TypeScript source. Only statements that start a line count, which is how every
// formatted module writes them and which keeps a commented-out export, a string and a template literal from being read as one;
// comments inside a specifier list are ignored.
func ParseModule(src string) Module {
	var m Module
	imports := map[string]Export{} // local name -> where it comes from
	for _, im := range importNamed.FindAllStringSubmatch(src, -1) {
		for _, spec := range parseSpecifiers(im[1]) {
			imports[spec.Name] = Export{Name: spec.Name, Orig: spec.Orig, From: im[2], TypeOnly: spec.TypeOnly}
		}
	}
	for _, e := range exportFrom.FindAllStringSubmatch(src, -1) {
		for _, spec := range parseSpecifiers(e[2]) {
			m.Exports = append(m.Exports, Export{Name: spec.Name, Orig: spec.Orig, From: e[3], TypeOnly: spec.TypeOnly || e[1] != ""})
		}
	}
	for _, e := range exportLocal.FindAllStringSubmatch(src, -1) {
		for _, spec := range parseSpecifiers(e[2]) {
			if from, ok := imports[spec.Orig]; ok {
				m.Exports = append(m.Exports, Export{Name: spec.Name, Orig: from.Orig, From: from.From, TypeOnly: spec.TypeOnly || e[1] != ""})
				continue
			}
			m.Exports = append(m.Exports, Export{Name: spec.Name, Orig: spec.Orig, TypeOnly: spec.TypeOnly || e[1] != ""})
		}
	}
	for _, e := range exportStar.FindAllStringSubmatch(src, -1) {
		if e[1] != "" {
			m.Exports = append(m.Exports, Export{Name: e[1], From: e[2]})
			continue
		}
		m.Stars = append(m.Stars, e[2])
	}
	for _, d := range declaration.FindAllStringSubmatch(src, -1) {
		m.Exports = append(m.Exports, Export{Name: d[1], Orig: d[1]})
	}
	return m
}

// parseSpecifiers reads `a, type B, c as d` into exports.
func parseSpecifiers(list string) []Export {
	var out []Export
	list = commentRe.ReplaceAllString(list, "")
	for part := range strings.SplitSeq(list, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var e Export
		if rest, ok := strings.CutPrefix(part, "type "); ok {
			e.TypeOnly, part = true, strings.TrimSpace(rest)
		}
		orig, name, renamed := strings.Cut(part, " as ")
		e.Orig, e.Name = strings.TrimSpace(orig), strings.TrimSpace(orig)
		if renamed {
			e.Name = strings.TrimSpace(name)
		}
		out = append(out, e)
	}
	return out
}

// Source reads upstream module source by slash-separated path under the repository's upstream mirror.
type Source func(file string) (string, bool)

// Definition is where P1 ends: the module that declares the name, or the external package the chain leaves the repository for.
type Definition struct {
	File     string // module path of the declaration; the specifier of an external package
	Name     string // the declared name (differs from the exported name after a rename)
	External bool
	Alias    bool // the chain ended at a namespace re-export (`export * as ns`): Name is the namespace and File its module
}

// Resolver applies P1 over a Source and caches every module it reads and parses.
type Resolver struct {
	src  Source
	mods map[string]*Module // nil for an unreadable module
}

// NewResolver returns a resolver that reads modules from src.
func NewResolver(src Source) *Resolver { return &Resolver{src: src, mods: map[string]*Module{}} }

// ResolveExport is Resolver.Resolve over a fresh resolver.
func ResolveExport(src Source, file, name string) (Definition, bool) {
	return NewResolver(src).Resolve(file, name)
}

func (r *Resolver) module(file string) *Module {
	if m, ok := r.mods[file]; ok {
		return m
	}
	var m *Module
	if text, ok := r.src(file); ok {
		parsed := ParseModule(text)
		m = &parsed
	}
	r.mods[file] = m
	return m
}

// Resolve applies P1: it follows the re-export chain of name from module file to the module that declares it. File paths are
// slash-separated and relative to the mirror root; a relative specifier resolves against the importing module and its .ts, .js
// or index.ts form. A cycle or a name no chain declares is not defined (ok is false).
func (r *Resolver) Resolve(file, name string) (Definition, bool) {
	return r.resolve(file, name, map[string]bool{})
}

func (r *Resolver) resolve(file, name string, seen map[string]bool) (Definition, bool) {
	key := file + "#" + name
	if seen[key] {
		return Definition{}, false
	}
	seen[key] = true
	mod := r.module(file)
	if mod == nil {
		return Definition{}, false
	}
	for _, e := range mod.Exports {
		if e.Name != name {
			continue
		}
		if e.From == "" {
			return Definition{File: file, Name: e.Orig}, true
		}
		target, external := r.specifier(file, e.From)
		if external {
			return Definition{File: e.From, Name: e.Orig, External: true}, true
		}
		if e.Orig == "" { // export * as ns
			return Definition{File: target, Name: e.Name, Alias: true}, true
		}
		if d, ok := r.resolve(target, e.Orig, seen); ok {
			return d, true
		}
	}
	for _, star := range mod.Stars {
		target, external := r.specifier(file, star)
		if external {
			continue
		}
		if d, ok := r.resolve(target, name, seen); ok {
			return d, true
		}
	}
	return Definition{}, false
}

// specifier resolves an import specifier from module file. A specifier that does not start with a dot is external.
func (r *Resolver) specifier(file, spec string) (target string, external bool) {
	if !strings.HasPrefix(spec, ".") {
		return spec, true
	}
	base := path.Join(path.Dir(file), spec)
	stem := strings.TrimSuffix(strings.TrimSuffix(base, ".js"), ".ts")
	for _, cand := range []string{base, stem + ".ts", stem + "/index.ts"} {
		if r.module(cand) != nil {
			return cand, false
		}
	}
	return stem + ".ts", false
}

// PortMap records, for each upstream file, the Go files that port it, as docs/parity/PORT_MAP.md does.
type PortMap map[string][]string

var (
	portRow = regexp.MustCompile("^\\|\\s*`(packages/[^`]+)`\\s*\\|([^|]*)\\|")
	goFile  = regexp.MustCompile(`[A-Za-z0-9_./-]+\.go\b`)
)

// ParsePortMap reads the table rows of PORT_MAP.md. A row's Go files are every token ending in .go in its second column.
func ParsePortMap(markdown string) PortMap {
	pm := PortMap{}
	for line := range strings.SplitSeq(markdown, "\n") {
		m := portRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if files := goFile.FindAllString(m[2], -1); len(files) > 0 {
			pm[m[1]] = append(pm[m[1]], files...) // a row with no Go file maps nowhere
		}
	}
	return pm
}

// PlacementTable applies P2. Seeds lists, for each upstream package, the Go directories that may hold its counterparts, best first.
type PlacementTable struct {
	PortMap PortMap
	Seeds   map[string][]string
}

// Dirs returns the Go directories that may hold the counterpart of a symbol declared in upstream file (a path such as
// packages/tui/src/components/loader.ts) of upstream package pkg: the directories of the Go files PORT_MAP records for the file,
// in file order, then the package seeds, without repeats. A file PORT_MAP does not list falls back to the files of the longest
// recorded sibling directory prefix.
func (p PlacementTable) Dirs(pkg, file string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(dir string) {
		if dir != "" && !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	files := p.PortMap[file]
	for dir := path.Dir(file); len(files) == 0 && strings.Count(dir, "/") >= 2; dir = path.Dir(dir) {
		var names []string
		for k := range p.PortMap {
			if path.Dir(k) == dir {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		for _, n := range names {
			files = append(files, p.PortMap[n]...)
		}
	}
	for _, f := range files {
		add(path.Dir(f))
	}
	for _, s := range p.Seeds[pkg] {
		add(strings.TrimSuffix(s, "/..."))
	}
	return out
}

// MethodOfFirstParam applies P3 in one direction: an upstream free function whose first parameter has type up.Parameters[0].Type is
// a Go method on recv. The receiver must agree with the first parameter; the remaining parameters and the result follow the
// function rules.
func MethodOfFirstParam(env FuncEnv, up Call, recv types.Type, sig *types.Signature) Verdict {
	if len(up.Parameters) == 0 {
		return Refute("P3: upstream function has no first parameter to be the receiver")
	}
	first := up.Parameters[0]
	if first.Optional || first.Rest {
		return Refute("P3: the first parameter %s is optional or rest, a receiver is always present", first.Name)
	}
	if v := env.Agree(first.Type, recv); v.OK != Yes {
		v.Why = "P3: receiver: " + v.Why
		return v
	}
	rest := up
	rest.Parameters = up.Parameters[1:]
	return Signature(env, rest, sig)
}

// FirstParamOfMethod applies P3 in the other direction: an upstream method of class owner is a Go function whose first parameter,
// after a leading context, is the owner.
func FirstParamOfMethod(env FuncEnv, owner string, up Call, sig *types.Signature) Verdict {
	params := sig.Params()
	at := 0
	if params.Len() > 0 && IsContext(params.At(0).Type()) {
		at = 1
	}
	if params.Len() <= at {
		return Refute("P3: Go function has no parameter for the receiver %s", owner)
	}
	if v := env.Agree(owner, params.At(at).Type()); v.OK != Yes {
		v.Why = "P3: receiver: " + v.Why
		return v
	}
	var rest []*types.Var
	for i := range params.Len() {
		if i != at {
			rest = append(rest, params.At(i))
		}
	}
	flat := types.NewSignatureType(nil, nil, nil, types.NewTuple(rest...), sig.Results(), sig.Variadic())
	return Signature(env, up, flat)
}

// Generics applies P4. up are the upstream type parameters of a declaration named name; goType is the Go declaration's type
// (a named type or function signature); instantiations documents upstream names that Go realises as one concrete type, keyed by
// name and holding the Go type as the Go compiler prints it.
func Generics(up []TypeParam, name string, goType types.Type, instantiations map[string]string) Verdict {
	n := goTypeParams(goType)
	switch {
	case len(up) == n && n > 0:
		return Accept("")
	case len(up) == 0 && n == 0:
		return Accept("")
	case len(up) == 0:
		return Refute("P4: upstream %s is not generic, Go type has %d type parameters", name, n)
	}
	if want, ok := instantiations[name]; ok && n == 0 {
		if typeLabel(goType) == want {
			return Accept("") // the documented instantiation
		}
		return Refute("P4: upstream %s is documented as %s, Go type is %s", name, want, typeLabel(goType))
	}
	if n == 0 {
		return Undecided("P4: upstream %s takes %d type parameters, Go type %s is concrete and no instantiation is documented", name, len(up), typeLabel(goType))
	}
	return Refute("P4: upstream %s takes %d type parameters, Go type takes %d", name, len(up), n)
}

func goTypeParams(t types.Type) int {
	switch t := types.Unalias(t).(type) {
	case *types.Named:
		return t.TypeParams().Len()
	case *types.Signature:
		return t.TypeParams().Len()
	}
	return 0
}

// TypeParamNames returns the names of an upstream declaration's type parameters, for the engine to treat as in scope.
func TypeParamNames(c Call) map[string]bool {
	out := map[string]bool{}
	for _, tp := range c.TypeParameters {
		out[tp.Name] = true
	}
	return out
}

func init() {
	RegisterPlacement(Placement, portMapPlacement{})
}

// portMapPlacement is rule P2: the Go directories that may hold the counterpart of an upstream package are the directories of the
// Go files that docs/parity/PORT_MAP.md records for the package's source files. The table is the repository's own mapping of
// upstream files to Go files, so a package whose Go home the seed list does not name (durable, chord, protocol, telemetry, env,
// client, server) is still searched where its files were ported. A row that records no Go file contributes nothing.
type portMapPlacement struct{}

func (portMapPlacement) Name() string { return "P2" }

func (portMapPlacement) Dirs(upstreamPackage string) []string {
	return PortMapDirs(portMapMarkdown(), upstreamPackage)
}

// ShapeDirs keeps the directories that hold the port of this package only. cmd/pig and internal/codingagent also hold coding-agent
// ports, so for ai they are searched by name only: a unique shape match there is an unrelated declaration, not a counterpart.
func (portMapPlacement) ShapeDirs(upstreamPackage string) []string {
	return PortMapExclusiveDirs(portMapMarkdown(), upstreamPackage)
}

var (
	portMapMu    sync.Mutex
	portMapByDir = map[string]string{}
	portRowRe    = regexp.MustCompile("^\\|\\s*`packages/([^/`]+)/[^`]*`\\s*\\|([^|]*)\\|")
	goFileRe     = regexp.MustCompile(`[A-Za-z0-9_./-]+\.go\b`)
)

// portMapMarkdown reads docs/parity/PORT_MAP.md from the module root enclosing the working directory; an unreadable table gives no
// directories. The text is read once per working directory, so a process that derives several trees (each from its own directory) reads each
// tree's table.
func portMapMarkdown() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	portMapMu.Lock()
	defer portMapMu.Unlock()
	if text, ok := portMapByDir[wd]; ok {
		return text
	}
	text := ""
	for dir := wd; ; {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			if data, readErr := os.ReadFile(filepath.Join(dir, "docs", "parity", "PORT_MAP.md")); readErr == nil {
				text = string(data)
			}
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	portMapByDir[wd] = text
	return text
}

// PortMapExclusiveDirs returns the PortMapDirs of pkg that PORT_MAP lists for no other upstream package, in the same order.
func PortMapExclusiveDirs(markdown, pkg string) []string {
	owners := map[string]map[string]bool{}
	for line := range strings.SplitSeq(markdown, "\n") {
		m := portRowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		for _, f := range goFileRe.FindAllString(m[2], -1) {
			if strings.HasSuffix(f, "_test.go") || strings.HasPrefix(f, "../") || strings.HasPrefix(f, ".") {
				continue
			}
			if dir := path.Dir(f); dir != "." {
				if owners[dir] == nil {
					owners[dir] = map[string]bool{}
				}
				owners[dir][m[1]] = true
			}
		}
	}
	var out []string
	for _, d := range PortMapDirs(markdown, pkg) {
		if len(owners[d]) == 1 {
			out = append(out, d)
		}
	}
	return out
}

// PortMapDirs returns the directories of the Go files that the PORT_MAP table lists for upstream package pkg, most frequently listed
// first and ties by path. Test files and files outside the repository's Go tree are ignored.
func PortMapDirs(markdown, pkg string) []string {
	counts := map[string]int{}
	for line := range strings.SplitSeq(markdown, "\n") {
		m := portRowRe.FindStringSubmatch(line)
		if m == nil || m[1] != pkg {
			continue
		}
		for _, f := range goFileRe.FindAllString(m[2], -1) {
			if strings.HasSuffix(f, "_test.go") || strings.HasPrefix(f, "../") || strings.HasPrefix(f, ".") {
				continue
			}
			if dir := path.Dir(f); dir != "." {
				counts[dir]++
			}
		}
	}
	out := make([]string, 0, len(counts))
	for d := range counts {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if counts[out[i]] != counts[out[j]] {
			return counts[out[i]] > counts[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}
