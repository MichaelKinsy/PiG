package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/test/next/bench/plugins.ts: the benchmark of the plugins component (slice 7c section 7, slice 12): a
// cold build of a 20-plugin set (one worker facet each, its own files, a copied contract, the app's API and kinds checked), a
// rebuild of one facet after an edit, the evaluation of a facet, and the heap after 100 reloads of one plugin on a host (old
// facets collected). Upstream reports medians over 7 runs by hand; Go's `testing.B` repeats and reports ns/op, and the other
// measures as metrics.
// Run by hand (not in CI): go test -run '^$' -bench BenchmarkPlugins ./chord/nextnode/plugins/
//
// The plugins are Pi's own TypeScript (`benchWorker` is bench/plugins.ts's `worker`: the shared contract is imported by relative
// path, `../contracts/contract.ts`, and Chord through the app's API `@bench/app/v1`, whose v1 is Chord's surface here). The
// builds are the real esbuild build, every one checked and required to be a script; the evaluation and the reloads are the
// real Node subprocess's (Pi's evaluate.ts and runtime.ts): the evaluation is measured
// inside it, by a probe (plugins_probe_test.go's shape) running Pi's `evaluateFacet`, and `--expose-gc` is the Node host's
// flag (the heap is read by the probe after `gc()`). The probe's helpers take a *testing.T, which a benchmark has not: the
// probe module and client are written again here for a *testing.B.

// benchApp is the app's API of the benchmarks (bench/plugins.ts's `API`, `HOST_PACKAGES`' `@bench/app/v1`).
var benchApp = AppApi{Name: "@bench/app", Versions: []string{"v1"}}

// benchSpecifier is the one host package of the benchmarks' plugins: the app's v1, which is Chord's surface.
const benchSpecifier = "@bench/app/v1"

// benchKinds are the kinds of the benchmarks' app (bench/plugins.ts's `KINDS`).
var benchKinds = []string{"server", "worker"}

const (
	benchPlugins = 20
	benchReloads = 100
)

// benchWorker is bench/plugins.ts's `worker(index, label)`: a plugin's worker facet.
func benchWorker(index int, label string) string {
	return strings.Join([]string{
		`import { constant, defineFacet, defineState, int, local, method, obj, str } from "@bench/app/v1";`,
		`import { Shared } from "../contracts/contract.ts";`,
		`import { format } from "./format.ts";`,
		fmt.Sprintf(`const Store = local("bench.store%d", { version: 1, params: {}, members: { label: str(), n: int(), bump: method(undefined, int()) } });`, index),
		`const Count = defineState("count", { schema: obj({ n: int() }), version: 1, initial: () => ({ n: 0 }) });`,
		`export default defineFacet((host) => host.serve(Store, (_, self) => {`,
		`  const doc = self.state(Count);`,
		fmt.Sprintf(`  return { label: constant(format(%s, Shared.name)), n: self.derive([doc], ([d]) => d.n),`, benchJSON(label)),
		`    bump: async (ctx) => { await doc.change((d) => { d.n += 1; }, ctx); return doc.value.n; } };`,
		`}));`,
	}, "\n")
}

func benchJSON(value any) string {
	text, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(text)
}

// benchPluginSet writes the 20-plugin set (and the contracts plugin) under a new root.
func benchPluginSet(b *testing.B) string {
	b.Helper()
	root := b.TempDir()
	write := func(dir string, files map[string]string) {
		for file, text := range files {
			full := filepath.Join(root, dir, file)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
	write("contracts", map[string]string{
		"package.json": benchJSON(map[string]any{"name": "@bench/contracts", "type": "module"}),
		"contract.ts":  "import { local, str } from \"@bench/app/v1\";\nexport const Shared = local(\"bench.shared\", { version: 1, params: {}, members: { name: str() } });",
	})
	for index := range benchPlugins {
		write(fmt.Sprintf("p%d", index), map[string]string{
			"package.json": benchJSON(map[string]any{"name": fmt.Sprintf("@bench/p%d", index), "type": "module"}),
			"worker.ts":    benchWorker(index, "v1"),
			"format.ts":    "export function format(label: string, name: string): string {\n  return label + \" (\" + name + \")\";\n}",
		})
	}
	return root
}

// benchSetup is the discovered set: the root, its worker plugins and the build options.
type benchSetup struct {
	root    string
	facets  []Plugin
	options BuildOptions
}

func benchDiscovered(b *testing.B) benchSetup {
	b.Helper()
	root := benchPluginSet(b)
	discovery, err := DiscoverPlugins([]PluginRoot{{Path: root}}, DiscoveryOptions{Kinds: benchKinds}, background)
	if err != nil {
		b.Fatal(err)
	}
	setup := benchSetup{root: root, options: BuildOptions{HostPackages: []string{benchSpecifier}, Platform: PlatformNode, Api: &benchApp, Kinds: benchKinds, Plugins: discovery.Plugins}}
	for _, plugin := range discovery.Plugins {
		if _, found := plugin.Facets["worker"]; found {
			setup.facets = append(setup.facets, plugin)
		}
	}
	if len(setup.facets) != benchPlugins {
		b.Fatalf("%d worker facets discovered, want %d", len(setup.facets), benchPlugins)
	}
	return setup
}

// buildAll builds every worker facet at once, each on its own goroutine as the component runs them (Promise.all).
func (s benchSetup) buildAll(b *testing.B) []BuiltFacet {
	b.Helper()
	built := make([]BuiltFacet, len(s.facets))
	errs := make([]error, len(s.facets))
	var wait sync.WaitGroup
	for index, plugin := range s.facets {
		wait.Go(func() { built[index], errs[index] = BuildFacet(plugin, "worker", s.options, background) })
	}
	wait.Wait()
	for index, err := range errs {
		if err != nil {
			b.Fatal(err)
		}
		if built[index].Kind != BuiltFacetScript {
			b.Fatalf("plugin %s: build %v", s.facets[index].Name, built[index].Errors)
		}
	}
	return built
}

// BenchmarkPluginsColdBuild is the cold build of every worker facet (each its own esbuild build, all at once).
func BenchmarkPluginsColdBuild(b *testing.B) {
	setup := benchDiscovered(b)
	var bytes int
	b.ResetTimer()
	for range b.N {
		bytes = 0
		for _, each := range setup.buildAll(b) {
			bytes += len(each.Source)
		}
	}
	b.ReportMetric(float64(bytes)/1024, "KiB-scripts")
}

// BenchmarkPluginsRebuild is the rebuild of one facet after an edit.
func BenchmarkPluginsRebuild(b *testing.B) {
	setup := benchDiscovered(b)
	entry := setup.facets[0].Facets["worker"]
	b.ResetTimer()
	for run := range b.N {
		b.StopTimer()
		if err := os.WriteFile(entry, []byte(benchWorker(0, fmt.Sprintf("edit %d", run))), 0o644); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if built, err := BuildFacet(setup.facets[0], "worker", setup.options, background); err != nil || built.Kind != BuiltFacetScript {
			b.Fatalf("rebuild = %+v, %v", built, err)
		}
	}
}

// benchOps are the probe's ops of the benchmarks: `stage` keeps the scripts, `evaluate` evaluates each with Pi's evaluateFacet
// (what bench/plugins.ts times) and answers the milliseconds per facet, `heap` is bench/plugins.ts's `settled`.
const benchOps = `
stage: async (scripts) => { globalThis.benchScripts = scripts; return scripts.length; },
evaluate: async () => {
	const hostPackages = { "@bench/app/v1": next };
	let total = 0;
	for (const { plugin, built } of globalThis.benchScripts) {
		const started = performance.now();
		evaluate.evaluateFacet(built, { hostPackages, plugin });
		total += performance.now() - started;
	}
	return total / globalThis.benchScripts.length;
},
heap: async () => {
	for (let round = 0; round < 5; round++) {
		await new Promise((resolve) => setImmediate(resolve));
		globalThis.gc();
	}
	return process.memoryUsage().heapUsed;
},
`

// benchNode is a Node host with the benchmarks' probe, linked to a Go host, closed when the benchmark ends.
type benchNode struct {
	b   *testing.B
	ref *host.Reference
}

// benchProbeCore is probeCore for a *testing.B.
func benchProbeCore(b *testing.B, cacheDir string) CoreFacet {
	b.Helper()
	tree, err := extractNode(cacheDir)
	if err != nil {
		b.Fatal(err)
	}
	src := "file://" + filepath.ToSlash(filepath.Join(tree, "pi", "chord", "src")) + "/"
	module := `import * as next from "` + src + `index.ts";
import * as evaluate from "` + src + `node/plugins/evaluate.ts";
const { defineFacet, remote, method, str, hostKind } = next;
const Worker = hostKind("test.worker");
const Probe = remote("test.probe", {
	version: 1,
	params: {},
	hostAddress: () => Worker(),
	variants: { main: { state: {}, methods: { call: method(str(), str()) } } },
});
const ops = { ` + benchOps + ` };
export default defineFacet((host) =>
	host.serve(Probe, () => ({
		variant: "main",
		members: {
			call: async (request) => {
				const { op, args } = JSON.parse(request);
				return JSON.stringify((await ops[op](args)) ?? null);
			},
		},
	})),
);
`
	file := filepath.Join(b.TempDir(), "probe.mjs")
	if err := os.WriteFile(file, []byte(module), 0o644); err != nil {
		b.Fatal(err)
	}
	return CoreFacet{Name: "@test/probe", Module: file}
}

// benchStart starts a worker Node host with the probe (and --expose-gc) as start does, and holds the probe.
func benchStart(b *testing.B, cache string, start func(core CoreFacet, address *contracts.Address) (*NodeHost, error)) *benchNode {
	b.Helper()
	address := workerKind.Address(nil)
	node, err := start(benchProbeCore(b, cache), &address)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = node.Close(background) })
	parent := serverFor(b, workerRoute(node))
	ref, err := parent.Get(background, probeContract, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(ref.Release)
	return &benchNode{b: b, ref: ref}
}

// call runs ops[op](args) in the subprocess and decodes its JSON answer.
func (n *benchNode) call(op string, args any) any {
	n.b.Helper()
	request, err := json.Marshal(map[string]any{"op": op, "args": args})
	if err != nil {
		n.b.Fatal(err)
	}
	answer, err := n.ref.Call("call", string(request), context.Background())
	if err != nil {
		n.b.Fatalf("probe %s: %v", op, err)
	}
	var value any
	if err := json.Unmarshal([]byte(answer.(string)), &value); err != nil {
		n.b.Fatal(err)
	}
	return value
}

// benchNodeOptions are the Node hosts' options: the shared cache and --expose-gc.
func benchNodeOptions(cache string) NodeOptions {
	return NodeOptions{CacheDir: cache, ExecArgv: []string{"--expose-gc"}}
}

// benchModules are the benchmarks' host packages: `@bench/app/v1` is a module re-exporting the Chord the Node host loaded
// (bench/plugins.ts's `HOST_PACKAGES`).
func benchModules(b *testing.B, cacheDir string) HostPackages {
	b.Helper()
	tree, err := extractNode(cacheDir)
	if err != nil {
		b.Fatal(err)
	}
	src := "file://" + filepath.ToSlash(filepath.Join(tree, "pi", "chord", "src")) + "/"
	file := filepath.Join(b.TempDir(), "app.mjs")
	if err := os.WriteFile(file, []byte(`export * from "`+src+`index.ts";`), 0o644); err != nil {
		b.Fatal(err)
	}
	return HostPackages{benchSpecifier: file}
}

// BenchmarkPluginsEvaluate is the evaluation of a facet by Pi's evaluateFacet in the Node subprocess: each op evaluates the 20
// facets; `evaluate-ns/facet` is the time measured inside Node per facet (the op's ns/op adds the call's round trip).
func BenchmarkPluginsEvaluate(b *testing.B) {
	setup := benchDiscovered(b)
	built := setup.buildAll(b)
	scripts := make([]PluginScript, len(built))
	for index := range built {
		scripts[index] = PluginScript{Plugin: setup.facets[index], Built: built[index]}
	}
	cache := b.TempDir()
	modules := benchModules(b, cache)
	node := benchStart(b, cache, func(core CoreFacet, address *contracts.Address) (*NodeHost, error) {
		h, _, err := NewPluginRuntime(benchNodeOptions(cache)).Load(RuntimeLoad{
			Kind: "worker", Core: []CoreFacet{core}, Modules: modules, Address: address, Principal: workerPrincipal, Peer: serverPrincipal,
		}, background)
		return h, err
	})
	node.call("stage", scriptsWire(scripts))
	var perFacet float64
	b.ResetTimer()
	for range b.N {
		perFacet = node.call("evaluate", nil).(float64)
	}
	b.ReportMetric(perFacet*1e6, "evaluate-ns/facet")
}

// BenchmarkPluginsReload is the reload of one plugin on a Node host (evaluation and host reload), alternating two builds, with
// the heap of the Node process after 100 reloads against after a warm-up of 10 (old facets collected).
func BenchmarkPluginsReload(b *testing.B) {
	root := benchPluginSet(b)
	cache := b.TempDir()
	modules := benchModules(b, cache)
	kind := func(name string) KindDefinition {
		return KindDefinition{Name: name, Platform: PlatformNode, HostPackages: []string{benchSpecifier}}
	}
	component := CreatePlugins(PluginsOptions{
		Roots: []PluginRoot{{Path: root}}, Api: &benchApp, Kinds: []KindDefinition{kind("server"), kind("worker")}, OnError: func(PluginsError) {},
		Node: benchNodeOptions(cache),
	}, background)
	defer component.Close()
	var node *NodeHost
	n := benchStart(b, cache, func(core CoreFacet, address *contracts.Address) (*NodeHost, error) {
		h, report, err := component.Load(PluginsLoad{
			Kind: "worker", Core: []CoreFacet{core}, Modules: modules, Address: address, Principal: workerPrincipal, Peer: serverPrincipal,
		}, background)
		if err == nil && len(report.Failed) != 0 {
			err = fmt.Errorf("load failed: %v", report.Failed)
		}
		node = h
		return h, err
	})
	entry := filepath.Join(root, "p0", "worker.ts")
	build := func(label string) *Built {
		if err := os.WriteFile(entry, []byte(benchWorker(0, label)), 0o644); err != nil {
			b.Fatal(err)
		}
		built, err := component.Build([]string{"@bench/p0"}, background)
		if err != nil {
			b.Fatal(err)
		}
		return built
	}
	first := build("a")
	second := build("b")
	reload := func(index int) {
		built := second
		if index%2 != 0 {
			built = first
		}
		if report, err := component.Reload(node, built, background); err != nil || len(report.Failed) != 0 {
			b.Fatalf("reload = %+v, %v", report, err)
		}
	}
	for index := range 10 {
		reload(index)
	}
	warm := n.call("heap", nil).(float64)
	for index := range benchReloads {
		reload(index)
	}
	after := n.call("heap", nil).(float64)
	b.ResetTimer()
	for index := range b.N {
		reload(index)
	}
	b.ReportMetric((after-warm)/1024, "KiB-heap-after-100")
}

// benchCSSModules adds a CSS module to every worker facet and builds for a browser (a Node build refuses CSS).
func benchCSSModules(b *testing.B) benchSetup {
	b.Helper()
	setup := benchDiscovered(b)
	for index, plugin := range setup.facets {
		entry := plugin.Facets["worker"]
		text, err := os.ReadFile(entry)
		if err != nil {
			b.Fatal(err)
		}
		text = append(text, "\nimport styles from \"./styles.module.css\";\nexport const classes = styles;\n"...)
		if err := os.WriteFile(entry, text, 0o644); err != nil {
			b.Fatal(err)
		}
		css := fmt.Sprintf(".card%d{color:red}.title{composes:card%d;color:blue}.\\31 {color:green}", index, index)
		if err := os.WriteFile(filepath.Join(plugin.Dir, "styles.module.css"), []byte(css), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	setup.options.Platform = PlatformBrowser
	return setup
}

// BenchmarkPluginsColdBuildWithCSSModules is the cold build of every worker facet when each has a CSS module: the component's
// builds share one Node subprocess for the mappings ("shared"), where a build on its own starts one ("alone").
func BenchmarkPluginsColdBuildWithCSSModules(b *testing.B) {
	for _, shared := range []bool{true, false} {
		name := "alone"
		if shared {
			name = "shared"
		}
		b.Run(name, func(b *testing.B) {
			setup := benchCSSModules(b)
			if shared {
				evaluator := newMappingEvaluator(setup.options.Node)
				defer evaluator.close()
				setup.options.mappings = evaluator
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				setup.buildAll(b)
			}
		})
	}
}
