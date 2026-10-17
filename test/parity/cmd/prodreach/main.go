// Command prodreach lists the functions that the PiG binary can reach, by rapid type analysis from the main function and
// the package initializers of the given main packages, and from the exported API of the given library packages.
//
// Usage: go run ./test/parity/cmd/prodreach [-out reach.json] [-tags a,b]... [-library pkg]... <main package pattern>...
//
// Each -tags flag adds one more build configuration whose reachable functions are unioned with the default build's. A binary that
// a build tag selects is only analysed this way.
//
// Each -library flag names a Go package that ports a library entry point Pi publishes (a package.json exports subpath): a program
// that imports the Go package can call any of its exported functions and the exported methods of its exported types, as a Pi user
// can call what the entry point exports. Those functions are roots too. A generic function or a method of a generic type has code
// only per type argument: each instantiation the loaded non-test code builds is a root, and a generic API nothing instantiates adds
// none.
//
// Each entry is written as "<repo-relative file>#<Recv.Name or Name>", the form of a production reference in the interface
// mapping. Test files are not loaded, so a function that only tests call is absent. Rapid type analysis keeps a method only
// when its receiver type is instantiated in reachable code, so a type that only tests construct does not make its methods
// reachable. The walk does not enter the parity-harness probes in parity_harness.go, which run only under PIG_PARITY_HARNESS=1. The
// result is otherwise an over-approximation of what runs, never an under-approximation of what can run.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/token"
	"go/types"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/test/parity/internal/ledgertarget"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/rta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// repeated collects a repeatable string flag.
type repeated []string

func (t *repeated) String() string     { return strings.Join(*t, ";") }
func (t *repeated) Set(v string) error { *t = append(*t, v); return nil }

func main() {
	out := flag.String("out", "", "write the JSON list here instead of stdout")
	var tags, libraries repeated
	flag.Var(&tags, "tags", "comma-separated build tags of one more build configuration to union in (repeatable)")
	flag.Var(&libraries, "library", "a library package whose exported functions and methods are roots (repeatable)")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: prodreach [-out file] [-tags a,b]... [-library pkg]... <main package pattern>...")
		os.Exit(2)
	}
	wd, _ := os.Getwd()
	list, err := reachableUnion(wd, flag.Args(), libraries, tags)
	if err != nil {
		fmt.Fprintln(os.Stderr, "prodreach:", err)
		os.Exit(1)
	}
	data, _ := json.MarshalIndent(list, "", " ")
	if *out == "" {
		fmt.Println(string(data))
		return
	}
	if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "prodreach:", err)
		os.Exit(1)
	}
}

// reachableUnion returns the sorted union of the entries reachable in the default build and in each build with one tag set. The builds are independent and run concurrently; the first failing build in tag order reports its error.
func reachableUnion(dir string, patterns, libraries, tags []string) ([]string, error) {
	sets := append([]string{""}, tags...)
	lists := make([][]string, len(sets))
	errs := make([]error, len(sets))
	var wg sync.WaitGroup
	for i, set := range sets {
		wg.Go(func() { lists[i], errs[i] = reachable(dir, patterns, libraries, set) })
	}
	wg.Wait()
	seen := map[string]bool{}
	for i := range sets {
		if errs[i] != nil {
			return nil, errs[i]
		}
		for _, entry := range lists[i] {
			seen[entry] = true
		}
	}
	return slices.Sorted(maps.Keys(seen)), nil
}

// reachable loads the main packages matched by patterns and the library packages in dir, built with the comma-separated tags (none
// when empty), and returns the sorted entries of every function the walk reaches.
func reachable(dir string, patterns, libraries []string, tags string) ([]string, error) {
	cfg := &packages.Config{Mode: packages.LoadAllSyntax, Tests: false, Dir: dir, Env: ledgertarget.Env()}
	if tags != "" {
		cfg.BuildFlags = []string{"-tags=" + tags}
	}
	initial, err := packages.Load(cfg, append(slices.Clone(patterns), libraries...)...)
	if err != nil {
		return nil, err
	}
	for _, pkg := range initial {
		if len(pkg.Errors) > 0 {
			return nil, pkg.Errors[0]
		}
	}
	libraryPaths := map[string]bool{}
	if len(libraries) > 0 {
		listed, err := packages.Load(&packages.Config{Mode: packages.NeedName, Dir: dir, BuildFlags: cfg.BuildFlags, Env: cfg.Env}, libraries...)
		if err != nil {
			return nil, err
		}
		for _, pkg := range listed {
			libraryPaths[pkg.PkgPath] = true
		}
	}
	program, ssaPackages := ssautil.AllPackages(initial, ssa.InstantiateGenerics)
	program.Build()
	var roots []*ssa.Function
	for _, pkg := range ssaPackages {
		if pkg == nil {
			continue
		}
		if libraryPaths[pkg.Pkg.Path()] {
			roots = append(roots, exportedAPI(program, pkg)...)
			continue
		}
		if pkg.Pkg.Name() != "main" {
			continue
		}
		if fn := pkg.Func("main"); fn != nil {
			roots = append(roots, fn)
		}
		if fn := pkg.Func("init"); fn != nil {
			roots = append(roots, fn)
		}
	}
	if len(libraryPaths) > 0 {
		roots = append(roots, libraryInstances(program, libraryPaths)...)
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("no main package in the patterns")
	}
	result := rta.Analyze(roots, true)
	// The walk starts at the roots and at every function rapid type analysis reached without a call edge (a method of a type that
	// reflection can call, for example).
	var seeds []*callgraph.Node
	for _, fn := range roots {
		if node := result.CallGraph.Nodes[fn]; node != nil {
			seeds = append(seeds, node)
		}
	}
	for _, node := range result.CallGraph.Nodes {
		if len(node.In) == 0 {
			seeds = append(seeds, node)
		}
	}
	isProbe := func(node *callgraph.Node) bool { return node.Func != nil && isHarness(program.Fset, node.Func) }
	production, all := walk(seeds, isProbe), walk(seeds, func(*callgraph.Node) bool { return false })
	seen := map[string]bool{}
	_ = callgraph.GraphVisitEdges(result.CallGraph, func(e *callgraph.Edge) error {
		for _, node := range []*callgraph.Node{e.Caller, e.Callee} {
			// A function the seeds reach only through a probe is dropped; one they do not reach at all (a cycle only rapid type
			// analysis enters) is kept, as before.
			if all[node] && !production[node] || isProbe(node) {
				continue
			}
			if entry := entryFor(program.Fset, node.Func, dir); entry != "" {
				seen[entry] = true
			}
		}
		return nil
	})
	// A root is reachable by definition, even one with no call edge (a library getter that calls nothing has no node in the graph).
	for _, fn := range roots {
		if !isHarness(program.Fset, fn) {
			if entry := entryFor(program.Fset, fn, dir); entry != "" {
				seen[entry] = true
			}
		}
	}
	list := make([]string, 0, len(seen))
	for entry := range seen {
		list = append(list, entry)
	}
	sort.Strings(list)
	return list, nil
}

// exportedAPI returns the roots a library package's exported API adds: its initializer, its exported non-generic functions and the
// exported methods of its exported non-generic named types. The method set of *T holds every method of T (a value method through a
// wrapper that calls it); an interface's methods have no body and add nothing.
func exportedAPI(program *ssa.Program, pkg *ssa.Package) []*ssa.Function {
	var roots []*ssa.Function
	if fn := pkg.Func("init"); fn != nil {
		roots = append(roots, fn)
	}
	for _, name := range slices.Sorted(maps.Keys(pkg.Members)) {
		switch member := pkg.Members[name].(type) {
		case *ssa.Function:
			if token.IsExported(name) && member.TypeParams().Len() == 0 {
				roots = append(roots, member)
			}
		case *ssa.Type:
			named, ok := member.Type().(*types.Named)
			if !ok || !token.IsExported(name) || named.TypeParams().Len() > 0 {
				continue
			}
			methods := program.MethodSets.MethodSet(types.NewPointer(named))
			for sel := range methods.Methods() {
				if sel.Obj().Exported() {
					if fn := program.MethodValue(sel); fn != nil {
						roots = append(roots, fn)
					}
				}
			}
		}
	}
	return roots
}

// libraryInstances returns the instantiations the program builds of the library packages' exported generic API. An exported generic
// function, or an exported method of an exported generic type, has code only per type argument; each instance that the loaded
// non-test code creates is one a caller of the library can call, so it is a root. A generic API that nothing instantiates adds none.
func libraryInstances(program *ssa.Program, libraries map[string]bool) []*ssa.Function {
	var roots []*ssa.Function
	for fn := range ssautil.AllFunctions(program) {
		origin := fn.Origin()
		if origin == nil || origin.Pkg == nil || origin.Parent() != nil || !libraries[origin.Pkg.Pkg.Path()] || !token.IsExported(origin.Name()) ||
			slices.ContainsFunc(fn.TypeArgs(), parameterized) {
			continue
		}
		if recv := origin.Signature.Recv(); recv != nil {
			t := recv.Type()
			if p, ok := t.(*types.Pointer); ok {
				t = p.Elem()
			}
			if named, ok := t.(*types.Named); !ok || !named.Obj().Exported() {
				continue
			}
		}
		roots = append(roots, fn)
	}
	slices.SortFunc(roots, func(a, b *ssa.Function) int { return strings.Compare(a.String(), b.String()) })
	return roots
}

// parameterized reports whether t mentions a type parameter: an instance built inside another generic body (Box[T] in Wrap[T]) is
// not code of its own until that body is instantiated, and rapid type analysis cannot take it as a root.
func parameterized(t types.Type) bool {
	switch t := t.(type) {
	case *types.TypeParam:
		return true
	case *types.Pointer:
		return parameterized(t.Elem())
	case *types.Slice:
		return parameterized(t.Elem())
	case *types.Array:
		return parameterized(t.Elem())
	case *types.Chan:
		return parameterized(t.Elem())
	case *types.Map:
		return parameterized(t.Key()) || parameterized(t.Elem())
	case *types.Named:
		return slices.ContainsFunc(slices.Collect(t.TypeArgs().Types()), parameterized)
	case *types.Alias:
		return parameterized(types.Unalias(t))
	case *types.Signature:
		return parameterized(t.Params()) || parameterized(t.Results())
	case *types.Tuple:
		for v := range t.Variables() {
			if parameterized(v.Type()) {
				return true
			}
		}
	case *types.Struct:
		for f := range t.Fields() {
			if parameterized(f.Type()) {
				return true
			}
		}
	case *types.Interface:
		for m := range t.Methods() {
			if parameterized(m.Type()) {
				return true
			}
		}
		for e := range t.EmbeddedTypes() {
			if parameterized(e) {
				return true
			}
		}
	}
	return false
}

// walk returns the nodes reachable from seeds without entering a node that skip reports.
func walk(seeds []*callgraph.Node, skip func(*callgraph.Node) bool) map[*callgraph.Node]bool {
	visited := map[*callgraph.Node]bool{}
	queue := slices.Clone(seeds)
	for _, node := range seeds {
		visited[node] = true
	}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		if skip(node) {
			continue
		}
		for _, edge := range node.Out {
			if !visited[edge.Callee] {
				visited[edge.Callee] = true
				queue = append(queue, edge.Callee)
			}
		}
	}
	return visited
}

// isHarness reports a parity-harness probe. The probes run only under PIG_PARITY_HARNESS=1, so a function that only they call is test
// scaffolding: the walk does not enter them.
func isHarness(fset *token.FileSet, fn *ssa.Function) bool {
	if origin := fn.Origin(); origin != nil {
		fn = origin
	}
	return fn.Pos().IsValid() && filepath.Base(fset.Position(fn.Pos()).Filename) == "parity_harness.go"
}

func entryFor(fset *token.FileSet, fn *ssa.Function, wd string) string {
	origin := fn.Origin()
	if origin != nil {
		fn = origin
	}
	if fn.Synthetic != "" || !fn.Pos().IsValid() {
		return ""
	}
	name := fn.Name()
	if recv := fn.Signature.Recv(); recv != nil {
		t := recv.Type()
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
		}
		if n, ok := t.(*types.Named); ok {
			name = n.Obj().Name() + "." + name
		}
	}
	rel, err := filepath.Rel(wd, fset.Position(fn.Pos()).Filename)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return rel + "#" + name
}
