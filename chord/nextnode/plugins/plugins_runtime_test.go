package plugins

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/test/next-node/plugins/runtime.test.ts. Tests of the plugin runtime (slice 10 §3, D11): loading and
// reloading hosts with complete sets of shipped scripts (unchanged digests kept as the same facet, removed = loaded minus
// shipped, failed builds and evaluations kept old). The plugins are written in the facet language of plugins_seam_test.go
// (`item <label>` is upstream's `defineFacet((host) => host.serve(Item, () => ({ label: constant(label) })))`, `syntax
// error` an `export const = ;`, `throws at import` a `throw new Error("at import")`); "loads no esbuild" (an import hook
// run in a Node process) has no Go form: a PluginRuntime takes an Evaluator and has no Builder.

// hostPackages are the host packages the fixtures' kinds keep external.
var hostPackages = []string{"@earendil-works/chord/next"}

// newTestHost is `createHost({ onError: () => {} })`, closed when the test ends.
func newTestHost(t *testing.T) host.Host {
	t.Helper()
	h, err := host.CreateHost(host.HostOptions{OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(background) })
	return h
}

// mustLoad fails the test when a load failed.
func mustLoad(t *testing.T) func(host.LoadReport, error) host.LoadReport {
	return func(report host.LoadReport, err error) host.LoadReport {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return report
	}
}

// mustReload fails the test when a reload failed.
func mustReload(t *testing.T) func(host.ReloadReport, error) host.ReloadReport {
	return func(report host.ReloadReport, err error) host.ReloadReport {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return report
	}
}

// writeFacet writes plugin `@test/<dir>` with its worker facet `source` (see itemPlugin).
func writeFacet(t *testing.T, root, dir, source string) {
	t.Helper()
	writePlugin(t, root, dir, "@test/"+dir, map[string]string{"worker.ts": source}, nil)
}

// itemPlugin writes plugin `@test/<dir>` serving `test.<dir>` labelled label from its worker facet.
func itemPlugin(t *testing.T, root, dir, label string) {
	t.Helper()
	writeFacet(t, root, dir, "item "+label)
}

// shipped is the worker scripts of every plugin under root, built now: what the server would ship.
func shipped(t *testing.T, root string) []PluginScript {
	t.Helper()
	discovery, err := DiscoverPlugins([]string{root}, DiscoveryOptions{Kinds: []string{"worker"}}, background)
	if err != nil {
		t.Fatal(err)
	}
	options := BuildOptions{HostPackages: hostPackages, Platform: PlatformNode, Plugins: discovery.Plugins}
	builder := &referenceBuilder{}
	scripts := make([]PluginScript, len(discovery.Plugins))
	for i, plugin := range discovery.Plugins {
		built, err := BuildFacet(builder, plugin, "worker", options, background)
		if err != nil {
			t.Fatal(err)
		}
		scripts[i] = PluginScript{Plugin: plugin, Built: built}
	}
	return scripts
}

// labelOf is the label `test.<dir>` shows on h.
func labelOf(t *testing.T, h host.Host, dir string) string {
	t.Helper()
	ref, err := h.Get(background, itemContract("test."+dir, 1), delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	value, err := ref.Member("label").Value()
	if err != nil {
		t.Fatal(err)
	}
	text, ok := value.(string)
	if !ok {
		t.Fatalf("label of test.%s = %#v, want a string", dir, value)
	}
	return text
}

// failedNames are the plugins of failures, in order.
func failedNames(failed []host.PluginFailure) []string {
	names := make([]string, len(failed))
	for i, failure := range failed {
		names[i] = failure.Plugin
	}
	return names
}

// sorted is names sorted: upstream's `.sort()` of a report's failures.
func sorted(names []string) []string {
	slices.Sort(names)
	return names
}

// expectNames fails the test unless got is want (nil is empty).
func expectNames(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s = %q, want %q", what, got, want)
	}
}

// expectChanges fails the test unless changes are the added, changed and removed plugins.
func expectChanges(t *testing.T, changes host.PluginChanges, added, changed, removed []string) {
	t.Helper()
	expectNames(t, "added", changes.Added, added...)
	expectNames(t, "changed", changes.Changed, changed...)
	expectNames(t, "removed", changes.Removed, removed...)
}

func TestLoadingAndReloadingWithCompleteSetsLoadsASetAndLeavesOutAPluginWhoseBuildOrEvaluationFailedReportedWithTheHostsReport(t *testing.T) {
	root := tempRoot(t)
	itemPlugin(t, root, "a", "a1")
	writeFacet(t, root, "broken", "syntax error: export const = ;")
	writeFacet(t, root, "throws", "throws at import")
	runtime := NewPluginRuntime(dslEvaluator{})
	h := newTestHost(t)
	report := mustLoad(t)(runtime.Load(h, RuntimeLoad{Kind: "worker", Scripts: shipped(t, root), Modules: HostPackages{}}, background))
	expectNames(t, "failed", sorted(failedNames(report.Failed)), "@test/broken", "@test/throws")
	if got := labelOf(t, h, "a"); got != "a1" {
		t.Fatalf("label = %q, want a1", got)
	}
}

func TestLoadingAndReloadingWithCompleteSetsReloadsACompleteSetSameDigestsUnchangedAChangedOneAppliedALoadedOneNotShippedRemoved(t *testing.T) {
	root := tempRoot(t)
	itemPlugin(t, root, "a", "a1")
	itemPlugin(t, root, "b", "b1")
	itemPlugin(t, root, "c", "c1")
	runtime := NewPluginRuntime(dslEvaluator{})
	h := newTestHost(t)
	mustLoad(t)(runtime.Load(h, RuntimeLoad{Kind: "worker", Scripts: shipped(t, root), Modules: HostPackages{}}, background))
	itemPlugin(t, root, "b", "b2")
	var next []PluginScript
	for _, script := range shipped(t, root) {
		if script.Plugin.Name != "@test/c" {
			next = append(next, script)
		}
	}
	report := mustReload(t)(runtime.Reload(h, next, background))
	expectChanges(t, report.Plugins, nil, []string{"@test/b"}, []string{"@test/c"})
	if got := []string{labelOf(t, h, "a"), labelOf(t, h, "b")}; !slices.Equal(got, []string{"a1", "b2"}) {
		t.Fatalf("labels = %q, want [a1 b2]", got)
	}
}

func TestLoadingAndReloadingWithCompleteSetsKeepsTheLoadedFacetOfAPluginWhoseNewBuildOrEvaluationFailedAnAddedFailingOneIsLeftOut(t *testing.T) {
	root := tempRoot(t)
	itemPlugin(t, root, "a", "a1")
	runtime := NewPluginRuntime(dslEvaluator{})
	h := newTestHost(t)
	mustLoad(t)(runtime.Load(h, RuntimeLoad{Kind: "worker", Scripts: shipped(t, root), Modules: HostPackages{}}, background))
	writeFacet(t, root, "a", "syntax error: export const = ;")
	writeFacet(t, root, "new", "throws at import")
	report := mustReload(t)(runtime.Reload(h, shipped(t, root), background))
	expectNames(t, "failed", sorted(failedNames(report.Failed)), "@test/a", "@test/new")
	expectChanges(t, report.Plugins, nil, nil, nil)
	if got := labelOf(t, h, "a"); got != "a1" {
		t.Fatalf("label = %q, want a1", got)
	}
}

// Not upstream's: `reload` throws for a host it did not load.
func TestPluginRuntimeReloadOfAHostItDidNotLoadFails(t *testing.T) {
	runtime := NewPluginRuntime(dslEvaluator{})
	_, err := runtime.Reload(newTestHost(t), nil, background)
	if err == nil || err.Error() != "plugins.reload: the host was not loaded by this runtime" {
		t.Fatalf("error = %v", err)
	}
}

// fakeHost is a host that only loads and reloads, as the test says: the host's own commit hook (`batch.commit`, which
// upstream spies to abort right after a commit) is internal to it, so the `aborted` error with its `details.committed` is
// what the fake returns.
type fakeHost struct {
	host.Host
	mu      sync.Mutex
	reloads [][]host.HostFacet
	reload  func(facets []host.HostFacet) (host.ReloadReport, error)
}

func (f *fakeHost) Load([]host.HostFacet, context.Context) (host.LoadReport, error) {
	return host.LoadReport{}, nil
}

func (f *fakeHost) Reload(facets []host.HostFacet, _ context.Context, _ host.ReloadOptions) (host.ReloadReport, error) {
	f.mu.Lock()
	f.reloads = append(f.reloads, facets)
	f.mu.Unlock()
	return f.reload(facets)
}

// countingEvaluator counts the evaluations of each plugin.
type countingEvaluator struct {
	Evaluator
	mu    sync.Mutex
	count map[string]int
}

func (e *countingEvaluator) EvaluateFacet(built BuiltFacet, options EvaluateOptions) (host.Facet, error) {
	e.mu.Lock()
	e.count[options.Plugin.Name]++
	e.mu.Unlock()
	return e.Evaluator.EvaluateFacet(built, options)
}

// Ports the runtime's half of plugins.test.ts › fix round 1 › "adopts what a reload committed when its wait aborts after
// the commit: a later reload keeps it (Astra)", with the abort made by a fake host (see fakeHost).
func TestRuntimeAdoptsWhatAReloadCommittedWhenItsWaitAbortsAfterTheCommitALaterReloadKeepsIt(t *testing.T) {
	root := tempRoot(t)
	itemPlugin(t, root, "a", "a1")
	itemPlugin(t, root, "b", "b1")
	evaluator := &countingEvaluator{Evaluator: dslEvaluator{}, count: map[string]int{}}
	runtime := NewPluginRuntime(evaluator)
	committed := delta.JsonObjectOf("committed", delta.JsonObjectOf("added", []any{}, "changed", []any{"@test/a"}, "removed", []any{}, "failed", []any{}))
	aborting := true
	fake := &fakeHost{reload: func([]host.HostFacet) (host.ReloadReport, error) {
		if aborting {
			return host.ReloadReport{}, data.NewChordError(data.ChordErrorInit{Code: data.CodeAborted, Message: "reload aborted", Details: committed, HasDetails: true}, nil)
		}
		return host.ReloadReport{}, nil
	}}
	mustLoad(t)(runtime.Load(fake, RuntimeLoad{Kind: "worker", Scripts: shipped(t, root), Modules: HostPackages{}}, background))
	itemPlugin(t, root, "a", "a2")
	_, err := runtime.Reload(fake, shipped(t, root), background)
	if chordErr, ok := data.IsChordErrorCode(err, data.CodeAborted); !ok || chordErr.Details != committed {
		t.Fatalf("error = %v, want the host's aborted error", err)
	}
	aborting = false
	itemPlugin(t, root, "b", "b2")
	mustReload(t)(runtime.Reload(fake, shipped(t, root), background))
	// a was evaluated for load, and for the aborted reload (committed: now loaded, not evaluated again); b for load and
	// its own reload.
	if evaluator.count["@test/a"] != 2 || evaluator.count["@test/b"] != 2 {
		t.Fatalf("evaluations = %v, want a: 2 (load, the committed reload), b: 2 (load, its reload)", evaluator.count)
	}
}
