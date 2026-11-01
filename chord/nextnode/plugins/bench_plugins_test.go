package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/test/next/bench/plugins.ts: the benchmark of the plugins component (slice 7c section 7): a cold
// build of a 20-plugin set (one worker facet each, its own files, a copied contract), a rebuild of one facet after an edit,
// the evaluation of a facet, and the heap after 100 reloads of one plugin on a host (old facets collected). Upstream
// reports medians over 7 runs by hand; Go's `testing.B` repeats and reports ns/op, and the heap as a metric.
//
// Go forced differences. Upstream builds with esbuild and evaluates with `vm.Script`; this uses the reference Builder and
// dslEvaluator of plugins_seam_test.go (AGENTS.md: no embedded JavaScript runtime), so the numbers measure the component
// (discovery, build fan-out, digesting, rollout, host reload) and not a JavaScript toolchain. A facet is a file of the
// seam's small language: `item <label>` with an `import` of a helper file and of the shared contract plugin. The
// plugin kinds are the fixtures' (`server`, `worker`, `web`). `--expose-gc` is `runtime.GC`.

const (
	benchPlugins = 20
	benchReloads = 100
)

// benchWorker is a plugin's worker facet: it serves `test.p<index>` with a label, its helper module and the shared contract.
func benchWorker(label string) string {
	return "import ./format.ts\nimport @bench/contracts/contract.ts\nitem " + label + "\n"
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
	manifest := func(name string) string {
		text, err := json.Marshal(map[string]any{"name": name, "type": "module"})
		if err != nil {
			b.Fatal(err)
		}
		return string(text)
	}
	write("contracts", map[string]string{
		"package.json": manifest("@bench/contracts"),
		"contract.ts":  "shared contract bench.shared\n",
	})
	for index := range benchPlugins {
		write(fmt.Sprintf("p%d", index), map[string]string{
			"package.json": manifest(fmt.Sprintf("@bench/p%d", index)),
			"worker.ts":    benchWorker("v1"),
			"format.ts":    "format helper\n",
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
	discovery, err := DiscoverPlugins([]string{root}, DiscoveryOptions{Kinds: []string{"server", "worker"}}, background)
	if err != nil {
		b.Fatal(err)
	}
	setup := benchSetup{root: root, options: BuildOptions{HostPackages: hostPackages, Platform: PlatformNode, Plugins: discovery.Plugins}}
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
func (s benchSetup) buildAll(b *testing.B, builder Builder) []BuiltFacet {
	built := make([]BuiltFacet, len(s.facets))
	errs := make([]error, len(s.facets))
	var wait sync.WaitGroup
	for index, plugin := range s.facets {
		wait.Go(func() { built[index], errs[index] = BuildFacet(builder, plugin, "worker", s.options, background) })
	}
	wait.Wait()
	for index, err := range errs {
		if err != nil {
			b.Fatal(err)
		}
		if built[index].Kind != BuiltFacetScript {
			b.Fatalf("plugin %s: build %+v", s.facets[index].Name, built[index])
		}
	}
	return built
}

// BenchmarkPluginsColdBuild is the cold build of every worker facet (each its own build, all at once).
func BenchmarkPluginsColdBuild(b *testing.B) {
	setup := benchDiscovered(b)
	var bytes int
	b.ResetTimer()
	for range b.N {
		bytes = 0
		for _, each := range setup.buildAll(b, &referenceBuilder{}) {
			bytes += len(each.Source)
		}
	}
	b.ReportMetric(float64(bytes)/1024, "KiB-scripts")
}

// BenchmarkPluginsRebuild is the rebuild of one facet after an edit.
func BenchmarkPluginsRebuild(b *testing.B) {
	setup := benchDiscovered(b)
	builder := &referenceBuilder{}
	entry := setup.facets[0].Facets["worker"]
	b.ResetTimer()
	for run := range b.N {
		b.StopTimer()
		if err := os.WriteFile(entry, []byte(benchWorker(fmt.Sprintf("edit %d", run))), 0o644); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if built, err := BuildFacet(builder, setup.facets[0], "worker", setup.options, background); err != nil || built.Kind != BuiltFacetScript {
			b.Fatalf("rebuild = %+v, %v", built, err)
		}
	}
}

// BenchmarkPluginsEvaluate is the evaluation of a facet (per facet: the op is one evaluation of each of the 20).
func BenchmarkPluginsEvaluate(b *testing.B) {
	setup := benchDiscovered(b)
	scripts := setup.buildAll(b, &referenceBuilder{})
	evaluator := dslEvaluator{probe: &probe{}}
	b.ResetTimer()
	for range b.N {
		for index, script := range scripts {
			if _, err := evaluator.EvaluateFacet(script, EvaluateOptions{HostPackages: HostPackages{}, Plugin: setup.facets[index]}); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkPluginsReload is the reload of one plugin on a host (evaluation and host reload), alternating two builds, with
// the heap after 100 reloads against after a warm-up of 10 (old facets collected).
func BenchmarkPluginsReload(b *testing.B) {
	root := benchPluginSet(b)
	component, err := CreatePlugins(PluginsOptions{
		Roots: []string{root}, Kinds: kindsOfFixtures(), OnError: func(PluginsError) {},
		Builder: &referenceBuilder{}, Evaluator: dslEvaluator{probe: &probe{}},
	}, background)
	if err != nil {
		b.Fatal(err)
	}
	defer component.Close()
	h, err := host.CreateHost(host.HostOptions{OnError: func(host.HostError) {}})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = h.Close(background) }()
	if report, err := component.Load(h, PluginsLoad{Kind: "worker", Modules: HostPackages{}}, background); err != nil || len(report.Failed) != 0 {
		b.Fatalf("load = %+v, %v", report, err)
	}
	entry := filepath.Join(root, "p0", "worker.ts")
	build := func(label string) *Built {
		if err := os.WriteFile(entry, []byte(benchWorker(label)), 0o644); err != nil {
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
		if report, err := component.Reload(h, built, background); err != nil || len(report.Failed) != 0 {
			b.Fatalf("reload = %+v, %v", report, err)
		}
	}
	settled := func() uint64 {
		runtime.GC()
		runtime.GC()
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		return stats.HeapAlloc
	}
	for index := range 10 {
		reload(index)
	}
	warm := settled()
	for index := range benchReloads {
		reload(index)
	}
	after := settled()
	b.ResetTimer()
	for index := range b.N {
		reload(index)
	}
	b.ReportMetric((float64(after)-float64(warm))/1024, "KiB-heap-after-100")
}
