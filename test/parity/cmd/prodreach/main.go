// Command prodreach lists the functions that the PiG binary can reach, by rapid type analysis from the main function and
// the package initializers of the given main packages.
//
// Usage: go run ./test/parity/cmd/prodreach [-out reach.json] [-tags a,b]... <main package pattern>...
//
// Each -tags flag adds one more build configuration whose reachable functions are unioned with the default build's. A binary that
// a build tag selects (cmd/pig with pig_experimental is pig-experimental) is only analysed this way.
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

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/rta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// tagSets collects the repeated -tags flag.
type tagSets []string

func (t *tagSets) String() string     { return strings.Join(*t, ";") }
func (t *tagSets) Set(v string) error { *t = append(*t, v); return nil }

func main() {
	out := flag.String("out", "", "write the JSON list here instead of stdout")
	var tags tagSets
	flag.Var(&tags, "tags", "comma-separated build tags of one more build configuration to union in (repeatable)")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: prodreach [-out file] [-tags a,b]... <main package pattern>...")
		os.Exit(2)
	}
	wd, _ := os.Getwd()
	list, err := reachableUnion(wd, flag.Args(), tags)
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
func reachableUnion(dir string, patterns, tags []string) ([]string, error) {
	sets := append([]string{""}, tags...)
	lists := make([][]string, len(sets))
	errs := make([]error, len(sets))
	var wg sync.WaitGroup
	for i, set := range sets {
		wg.Go(func() { lists[i], errs[i] = reachable(dir, patterns, set) })
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

// reachable loads the main packages matched by patterns in dir, built with the comma-separated tags (none when empty), and returns the sorted entries of every function the walk reaches.
func reachable(dir string, patterns []string, tags string) ([]string, error) {
	cfg := &packages.Config{Mode: packages.LoadAllSyntax, Tests: false, Dir: dir}
	if tags != "" {
		cfg.BuildFlags = []string{"-tags=" + tags}
	}
	initial, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}
	for _, pkg := range initial {
		if len(pkg.Errors) > 0 {
			return nil, pkg.Errors[0]
		}
	}
	program, ssaPackages := ssautil.AllPackages(initial, ssa.InstantiateGenerics)
	program.Build()
	var roots []*ssa.Function
	for _, pkg := range ssaPackages {
		if pkg == nil || pkg.Pkg.Name() != "main" {
			continue
		}
		if fn := pkg.Func("main"); fn != nil {
			roots = append(roots, fn)
		}
		if fn := pkg.Func("init"); fn != nil {
			roots = append(roots, fn)
		}
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
	list := make([]string, 0, len(seen))
	for entry := range seen {
		list = append(list, entry)
	}
	sort.Strings(list)
	return list, nil
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
