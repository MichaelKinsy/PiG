package host

// pi: packages/chord/src/loader.ts
// pi: packages/chord/src/node.ts
// Ports packages/chord/test/loader.test.ts ("node loader") and its fixture test/fixtures/loader.ts, through the
// isolated-Node loader (loader.go): the same facet modules, contract and assertions, run against a Go host.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// fileURL is url.pathToFileURL(path).href on the host's path flavour: "file://" + a Windows path is not a URL.
func fileURL(path string) string { return nodeurl.PathToFileURL(path, runtime.GOOS == "windows") }

// nodeLoaderEnv is what the Pi fixture reads from its own package: the Chord source it imports (src/index.ts) and
// the typebox it resolves from the package's node_modules. The mirror is read-only, so the plugin files under a
// temporary directory name both by file URL (the fixture writes its plugin files under the package instead).
type nodeLoaderEnv struct {
	chord   string // the Chord package directory
	chordJS string // src/index.ts as a file URL
	typebox string // typebox's entry as a file URL
}

func requireNodeLoaderEnv(t *testing.T) nodeLoaderEnv {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH")
	}
	chord, err := filepath.Abs(filepath.Join("..", "..", ".upstream", "current", "packages", "chord"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(chord, "src", "index.ts")); err != nil {
		t.Skip("the Pi mirror has no packages/chord/src/index.ts")
	}
	var typebox string
	for directory := chord; ; directory = filepath.Dir(directory) {
		manifest, err := os.ReadFile(filepath.Join(directory, "node_modules", "typebox", "package.json"))
		if err == nil {
			var parsed struct {
				Exports map[string]any `json:"exports"`
			}
			if err := json.Unmarshal(manifest, &parsed); err != nil {
				t.Fatal(err)
			}
			entry, _ := parsed.Exports["."].(map[string]any)
			target, _ := entry["import"].(string)
			if target == "" {
				t.Fatalf("typebox exports no import entry: %s", manifest)
			}
			typebox = fileURL(filepath.Join(directory, "node_modules", "typebox", target))
			break
		}
		if filepath.Dir(directory) == directory {
			t.Skip("typebox is not installed above the Pi mirror's chord package")
		}
	}
	return nodeLoaderEnv{chord: chord, chordJS: fileURL(filepath.Join(chord, "src", "index.ts")), typebox: typebox}
}

func (env nodeLoaderEnv) loader() *NodeLoaderImpl {
	return NewNodeLoader(NodeLoaderOptions{Chord: filepath.Join(env.chord, "src", "index.ts")})
}

func (env nodeLoaderEnv) write(t *testing.T, path, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	source = strings.ReplaceAll(source, `"typebox"`, `"`+env.typebox+`"`)
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func jsLiteral(value string) string {
	text, _ := json.Marshal(value)
	return string(text)
}

// loader.ts fixture: the test's own copy of the plugin's contract (binding is by description, not object identity).
var nodeLoaderHello = Local("t.hello", LocalDef{
	Version: 1,
	Input:   tObject(),
	Members: Fields[any]{{"greet", Method(tString(), tString())}},
})

// facetSource is the fixture's facetSource.
func (env nodeLoaderEnv) facetSource() string {
	return `
import { Type } from "typebox";
import { defineFacet, local, method } from ` + jsLiteral(env.chordJS) + `;
import { greeting } from "./greeting.ts";

const Hello = local("t.hello", {
	version: 1,
	input: Type.Object({}),
	members: { greet: method(Type.String(), Type.String()) },
});

export default defineFacet((host) => {
	host.serve(Hello, () => ({ greet: (name) => greeting + " " + name }));
});
`
}

func greetOnce(t *testing.T, host *HostImpl, contract *Contract, name string) any {
	t.Helper()
	ref, err := host.Hold(contract, map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ref.Release)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	return mustCall(t, ref, "greet", name, nil)
}

// loader.test.ts:8-15 "loads facet modules and reloads them with fresh copies of their relative imports"; the body is
// test/fixtures/loader.ts.
func TestNodeLoaderLoadsFacetModulesAndReloadsThemWithFreshCopiesOfTheirRelativeImports(t *testing.T) {
	env := requireNodeLoaderEnv(t)
	dir := t.TempDir() + "/"
	plugins := t.TempDir() + "/"
	loader := env.loader()
	t.Cleanup(func() { _ = loader.Close() })

	writeGreeting := func(text string) { env.write(t, dir+"greeting.ts", "export const greeting = "+jsLiteral(text)+";\n") }
	env.write(t, dir+"facet.ts", env.facetSource())
	writeGreeting("v1")
	host := CreateHost(HostOptions{Self: kindHost(), Loader: loader.Load, OnError: func(error) {}})
	if err := host.Load([]PluginRef{ModuleRef(dir + "facet.ts")}, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	ref, err := host.Hold(nodeLoaderHello, map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustCall(t, ref, "greet", "x", nil); got != "v1 x" {
		t.Fatalf("greet = %v", got)
	}
	writeGreeting("v2")
	report, err := host.Reload([]PluginRef{ModuleRef(dir + "facet.ts")}, ReloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Changed, []string{"t.hello"}) {
		t.Fatalf("changed = %v", report.Changed)
	}
	if got := mustCall(t, ref, "greet", "x", nil); got != "v2 x" {
		t.Fatalf("greet after reload = %v", got)
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}

	// A facet listed first that imports a later facet's directory shares that facet's copy of the module.
	shared := Local("t.shared", LocalDef{
		Version: 1,
		Input:   tObject(),
		Members: Fields[any]{{"same", Method(nil, tBoolean())}},
	})
	env.write(t, plugins+"b/state.ts", "export const state = {};\n")
	env.write(t, plugins+"b/facet.ts",
		"import { defineFacet } from "+jsLiteral(env.chordJS)+";\nimport { state } from \"./state.ts\";\nexport const own = state;\nexport default defineFacet(() => {});\n")
	env.write(t, plugins+"a/facet.ts",
		`import { Type } from "typebox";`+"\nimport { defineFacet, local, method } from "+jsLiteral(env.chordJS)+";\nimport { state } from \"../b/state.ts\";\n"+
			`const Shared = local("t.shared", { version: 1, input: Type.Object({}), members: { same: method(undefined, Type.Boolean()) } });`+"\n"+
			`export default defineFacet((host) => host.serve(Shared, () => ({ same: async () => (await import(new URL("../b/facet.ts", import.meta.url).href)).own === state })));`+"\n")
	ordered := CreateHost(HostOptions{Self: kindHost(), Loader: loader.Load, OnError: func(error) {}})
	t.Cleanup(func() { _ = ordered.Close() })
	if err := ordered.Load([]PluginRef{ModuleRef(plugins + "a/facet.ts"), ModuleRef(plugins + "b/facet.ts")}, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	ref, err = ordered.Get(shared, map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustCall(t, ref, "same", nil, nil); got != true {
		t.Fatalf("same = %v", got)
	}
}

// loader.ts:57-59: a module whose default export is not a facet fails the load with a TypeError naming the module URL.
func TestNodeLoaderRejectsAModuleWhoseDefaultExportIsNotAFacet(t *testing.T) {
	env := requireNodeLoaderEnv(t)
	dir := t.TempDir() + "/"
	env.write(t, dir+"plain.ts", "export default { setup: 1 };\n")
	loader := env.loader()
	t.Cleanup(func() { _ = loader.Close() })
	host := CreateHost(HostOptions{Self: kindHost(), Loader: loader.Load, OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	err := host.Load([]PluginRef{ModuleRef(dir + "plain.ts")}, LoadOptions{})
	var typeErr *TypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("err = %T %v", err, err)
	}
	if want := fileURL(dir+"plain.ts") + ": default export must be a facet (defineFacet)"; typeErr.Message != want {
		t.Fatalf("message = %q, want %q", typeErr.Message, want)
	}
}

// loader.ts:63: a facet is named by its module URL; Facets come back in module order.
func TestNodeLoaderNamesFacetsByTheirModuleURLInModuleOrder(t *testing.T) {
	env := requireNodeLoaderEnv(t)
	dir := t.TempDir() + "/"
	env.write(t, dir+"greeting.ts", "export const greeting = \"g\";\n")
	env.write(t, dir+"one.ts", env.facetSource())
	env.write(t, dir+"two.ts", "import { defineFacet } from "+jsLiteral(env.chordJS)+";\nexport default defineFacet(() => {});\n")
	loader := env.loader()
	t.Cleanup(func() { _ = loader.Close() })
	facets, err := loader.Load([]string{dir + "two.ts", fileURL(dir+"one.ts") + "?stale=1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(facets) != 2 || facets[0].Name != fileURL(dir+"two.ts") || facets[1].Name != fileURL(dir+"one.ts") {
		t.Fatalf("facets = %+v", facets)
	}
}

// A throw in a method reaches the caller with its Chord code and message; a root that is aborted aborts the child's
// call and returns at once, and the child's method sees the abort.
func TestNodeLoaderCarriesErrorsAndAbortsAcrossTheProcessBoundary(t *testing.T) {
	env := requireNodeLoaderEnv(t)
	dir := t.TempDir() + "/"
	marker, started := dir+"aborted", dir+"started"
	env.write(t, dir+"facet.ts", `
import { Type } from "typebox";
import { writeFileSync } from "node:fs";
import { defineFacet, forbidden, local, method } from `+jsLiteral(env.chordJS)+`;

const Probe = local("t.probe", {
	version: 1,
	input: Type.Object({}),
	members: { refuse: method(), wait: method() },
});

export default defineFacet((host) => {
	host.serve(Probe, () => ({
		refuse: () => { throw forbidden("not for you"); },
		wait: (ctx) => new Promise((resolve) => {
			writeFileSync(`+jsLiteral(started)+`, "x");
			ctx.signal.addEventListener("abort", () => { writeFileSync(`+jsLiteral(marker)+`, "x"); resolve(null); });
		}),
	}));
});
`)
	probe := Local("t.probe", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{
		{"refuse", Method(nil, nil)}, {"wait", Method(nil, nil)},
	}})
	loader := env.loader()
	t.Cleanup(func() { _ = loader.Close() })
	host := CreateHost(HostOptions{Self: kindHost(), Loader: loader.Load, OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	if err := host.Load([]PluginRef{ModuleRef(dir + "facet.ts")}, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	ref, err := host.Hold(probe, map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := callMember(ref, "refuse", nil, nil); !IsChordError(err, CodeForbidden) || !regexp.MustCompile(`not for you`).MatchString(err.Error()) {
		t.Fatalf("refuse: %v", err)
	}
	signal, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := host.Root(func(ctx Ctx) (any, error) {
			close(entered)
			return callMember(ref, "wait", nil, ctx)
		}, RootOptions{Signal: signal})
		done <- err
	}()
	<-entered
	// The abort must reach a call the child's method is already running, or it has no listener yet.
	waitForFile(t, started, "the child's method never started")
	cancel()
	select {
	case err := <-done:
		if !IsChordError(err, CodeAborted) {
			t.Fatalf("wait: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the aborted call did not return")
	}
	waitForFile(t, marker, "the child's method never saw the abort")
}

func waitForFile(t *testing.T, path, failure string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(failure)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A facet that serves what the parent cannot mirror is refused at Load (loader_child.mjs checkServed).
func TestNodeLoaderRefusesFacetsThatServeNonMethodMembers(t *testing.T) {
	env := requireNodeLoaderEnv(t)
	dir := t.TempDir() + "/"
	env.write(t, dir+"facet.ts", `
import { Type } from "typebox";
import { defineFacet, local } from `+jsLiteral(env.chordJS)+`;
const Counter = local("t.counter", { version: 1, input: Type.Object({}), members: { count: Type.Number() } });
export default defineFacet((host) => host.serve(Counter, () => ({})));
`)
	loader := env.loader()
	t.Cleanup(func() { _ = loader.Close() })
	_, err := loader.Load([]string{dir + "facet.ts"})
	var typeErr *TypeError
	if !errors.As(err, &typeErr) || !strings.Contains(typeErr.Message, "t.counter.count is a state member") {
		t.Fatalf("err = %v", err)
	}
}

// Close stops the child; the children of facets nothing refers to any longer stop on their own (the eviction Pi's
// loader lacks: its old module copies leak until the process exits).
func TestNodeLoaderStopsItsChildrenAtCloseAndWhenTheirFacetsAreUnreferenced(t *testing.T) {
	env := requireNodeLoaderEnv(t)
	dir := t.TempDir() + "/"
	env.write(t, dir+"greeting.ts", "export const greeting = \"g\";\n")
	env.write(t, dir+"facet.ts", env.facetSource())
	loader := env.loader()
	t.Cleanup(func() { _ = loader.Close() })

	closed, err := loader.Load([]string{dir + "facet.ts"})
	if err != nil {
		t.Fatal(err)
	}
	closedHost := hostOfFacets(t, loader)
	_ = closed
	if err := loader.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closedHost.instance.Exited():
	case <-time.After(10 * time.Second):
		t.Fatal("Close left the child running")
	}

	if _, err := loader.Load([]string{dir + "facet.ts"}); err != nil {
		t.Fatal(err)
	}
	unreferenced := hostOfFacets(t, loader)
	deadline := time.Now().Add(20 * time.Second)
	for {
		runtime.GC()
		select {
		case <-unreferenced.instance.Exited():
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the child of unreferenced facets kept running")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func hostOfFacets(t *testing.T, loader *NodeLoaderImpl) *nodeFacetHost {
	t.Helper()
	loader.mu.Lock()
	defer loader.mu.Unlock()
	for host := range loader.hosts {
		return host
	}
	t.Fatal("the loader runs no child")
	return nil
}

// loader.test.ts:17-20 "needs a configured loader for modules".
func TestNodeLoaderNeedsAConfiguredLoaderForModules(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	err := host.Load([]PluginRef{ModuleRef("./facet.ts")}, LoadOptions{})
	if err == nil || !regexp.MustCompile(`createHost\(\{ loader \}\)`).MatchString(err.Error()) {
		t.Fatalf("err = %v", err)
	}
}
