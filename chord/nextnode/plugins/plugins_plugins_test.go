package plugins

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/test/next-node/plugins/plugins.test.ts. Tests of the plugins component (slice 7c §4, §5, §7): per
// plugin at startup and reload (a failing build, evaluation, setup; unchanged plugins untouched; added and removed plugins;
// the closure rule through builds), `rollout` (order, stopping at a rejecting step, marks), installs through the component,
// GC over 200 reloads, and a server and a worker. The plugins are written in the facet language of plugins_seam_test.go; the
// cases that are not about TypeScript sources keep upstream's layouts, edits and expectations.

// reported collects what the component reports.
type reported struct {
	mu    sync.Mutex
	items []PluginsError
}

func (r *reported) add(err PluginsError) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, err)
}

func (r *reported) all() []PluginsError {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]PluginsError{}, r.items...)
}

// kindsOfFixtures is the fixtures' app's kinds: Node servers and workers, a browser web.
func kindsOfFixtures() []KindDefinition {
	return []KindDefinition{
		{Name: "server", Platform: PlatformNode, HostPackages: hostPackages},
		{Name: "worker", Platform: PlatformNode, HostPackages: hostPackages},
		{Name: "web", Platform: PlatformBrowser, HostPackages: hostPackages},
	}
}

// componentOn is the component over root with the reference builder and dslEvaluator (reporting the probe's states), closed
// when the test ends.
func componentOn(t *testing.T, root string, withProbe *probe, edit ...func(*PluginsOptions)) (Plugins, *reported) {
	t.Helper()
	collected := &reported{}
	options := PluginsOptions{
		Roots: []string{root}, Kinds: kindsOfFixtures(), OnError: collected.add,
		Builder: &referenceBuilder{}, Evaluator: dslEvaluator{probe: withProbe},
	}
	for _, each := range edit {
		each(&options)
	}
	plugins, err := CreatePlugins(options, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(plugins.Close)
	return plugins, collected
}

func component(t *testing.T, root string) Plugins {
	t.Helper()
	plugins, _ := componentOn(t, root, &probe{})
	return plugins
}

// waitMarks waits for the marks to satisfy check, as `vi.waitFor(() => expect(plugins.state.value).toMatchObject(...))`.
func waitMarks(t *testing.T, plugins Plugins, what string, check func(PluginsState) bool) {
	t.Helper()
	eventually(t, 5*time.Second, what, func() bool { return check(plugins.Marks()) })
}

func dirtyIs(names ...string) func(PluginsState) bool {
	return func(marks PluginsState) bool { return slices.Equal(marks.Dirty, names) }
}

// build is `plugins.build(names)`, failing the test on an error.
func build(t *testing.T, plugins Plugins, names ...string) *Built {
	t.Helper()
	built, err := plugins.Build(names, background)
	if err != nil {
		t.Fatal(err)
	}
	return built
}

func reload(t *testing.T, plugins Plugins, h host.Host, built *Built) host.ReloadReport {
	t.Helper()
	return mustReload(t)(plugins.Reload(h, built, background))
}

// loadWorker loads h with plugins' worker set and no core.
func loadWorker(t *testing.T, plugins Plugins, h host.Host) host.LoadReport {
	t.Helper()
	return mustLoad(t)(plugins.Load(h, PluginsLoad{Kind: "worker", Modules: HostPackages{}}, background))
}

// facetOf is the plugin and kind of each facet of built.
func facetOf(built *Built) [][2]string {
	out := make([][2]string, len(built.Facets))
	for i, facet := range built.Facets {
		out[i] = [2]string{facet.Plugin, facet.Facet}
	}
	return out
}

// failureError is the error of the failure of plugin.
func failureError(t *testing.T, failed []host.PluginFailure, plugin string) error {
	t.Helper()
	for _, failure := range failed {
		if failure.Plugin == plugin {
			return failure.Error
		}
	}
	t.Fatalf("no failure for %s in %v", plugin, failedNames(failed))
	return nil
}

func TestPerPluginAtStartupLeavesOutAPluginWhoseBuildFailsWhoseEvaluationThrowsOrWhoseSetupThrowsLoadsTheOthersAndCore(t *testing.T) {
	root := tempRoot(t)
	itemPlugin(t, root, "good", "good")
	writeFacet(t, root, "syntax", "syntax error: export default {")
	writeFacet(t, root, "throws", "throws at import")
	writeFacet(t, root, "setup", "setup-throws in setup")
	plugins := component(t, root)
	h := newTestHost(t)
	core := host.DefineFacet(func(f host.FacetHost, _ host.FacetSelf) error {
		return f.Serve(itemContract("test.item", 1), func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
			return labelled("core"), nil
		}, host.ServeOptions{})
	})
	report := mustLoad(t)(plugins.Load(h, PluginsLoad{Kind: "worker", Core: []host.HostFacet{{Name: "@app/core", Facet: core}}, Modules: HostPackages{}}, background))
	var got [][2]string
	for _, failure := range report.Failed {
		got = append(got, [2]string{failure.Plugin, failure.Error.Error()})
	}
	if len(got) != 3 || got[0][0] != "@test/syntax" || got[1] != [2]string{"@test/throws", "at import"} || got[2] != [2]string{"@test/setup", `plugin "@test/setup": in setup`} {
		t.Fatalf("failed = %q", got)
	}
	if !regexp.MustCompile(`^plugin "@test/syntax": its worker facet failed to build:\nworker\.ts:1:`).MatchString(got[0][1]) {
		t.Fatalf("syntax failure = %q", got[0][1])
	}
	if label := labelOf(t, h, "good"); label != "good" {
		t.Fatalf("good = %q", label)
	}
	if label := labelOf(t, h, "item"); label != "core" {
		t.Fatalf("item = %q", label)
	}
	plugins.Close()
}

// loadedABC is a host loaded with plugins `a`, `b` and `c` (worker facets), and the component.
func loadedABC(t *testing.T) (root string, plugins Plugins, h host.Host) {
	t.Helper()
	root = tempRoot(t)
	for _, dir := range []string{"a", "b", "c"} {
		itemPlugin(t, root, dir, dir+"1")
	}
	plugins = component(t, root)
	h = newTestHost(t)
	loadWorker(t, plugins, h)
	return root, plugins, h
}

func TestPerPluginAtReloadRebuildsOnePluginOfThreeOnlyItChangesTheOthersArePassedAsTheSameFacetsUntouched(t *testing.T) {
	root, plugins, h := loadedABC(t)
	itemPlugin(t, root, "b", "b2")
	built := build(t, plugins, "@test/b")
	if got := facetOf(built); !reflect.DeepEqual(got, [][2]string{{"@test/b", "worker"}}) {
		t.Fatalf("facets = %v", got)
	}
	report := reload(t, plugins, h, built)
	expectChanges(t, report.Plugins, nil, []string{"@test/b"}, nil)
	if !reflect.DeepEqual(report.Objects, []host.ReloadedObject{{Object: "test.b", Kind: host.ReloadSwapped}}) {
		t.Fatalf("objects = %+v", report.Objects)
	}
	if got := []string{labelOf(t, h, "a"), labelOf(t, h, "b"), labelOf(t, h, "c")}; !slices.Equal(got, []string{"a1", "b2", "c1"}) {
		t.Fatalf("labels = %q", got)
	}
	// Built again unchanged: the same digest, nothing reloaded.
	again := reload(t, plugins, h, build(t, plugins, "@test/b"))
	expectChanges(t, again.Plugins, nil, nil, nil)
}

func TestPerPluginAtReloadKeepsTheLoadedFacetOfAPluginWhoseBuildFailsOrWhoseEvaluationThrowsTheOthersApply(t *testing.T) {
	root, plugins, h := loadedABC(t)
	writeFiles(t, filepath.Join(root, "a"), map[string]string{"worker.ts": "syntax error: export default {"})
	writeFiles(t, filepath.Join(root, "b"), map[string]string{"worker.ts": "throws at import"})
	itemPlugin(t, root, "c", "c2")
	report := reload(t, plugins, h, build(t, plugins, "@test/a", "@test/b", "@test/c"))
	expectChanges(t, report.Plugins, nil, []string{"@test/c"}, nil)
	expectNames(t, "failed", failedNames(report.Failed), "@test/a", "@test/b")
	if got := []string{labelOf(t, h, "a"), labelOf(t, h, "b"), labelOf(t, h, "c")}; !slices.Equal(got, []string{"a1", "b1", "c2"}) {
		t.Fatalf("labels = %q", got)
	}
}

func TestPerPluginAtReloadAddsAPluginDiscoveredSinceRemovesOneNoLongerThereAnAddedPluginThatFailsIsLeftOut(t *testing.T) {
	root, plugins, h := loadedABC(t)
	itemPlugin(t, root, "d", "d1")
	writeFacet(t, root, "e", "syntax error: export default {")
	if err := os.RemoveAll(filepath.Join(root, "c")); err != nil {
		t.Fatal(err)
	}
	waitMarks(t, plugins, "set", func(marks PluginsState) bool { return marks.Set })
	built := build(t, plugins)
	expectNames(t, "removed", built.Removed, "@test/c")
	report := reload(t, plugins, h, built)
	expectChanges(t, report.Plugins, []string{"@test/d"}, nil, []string{"@test/c"})
	expectNames(t, "failed", failedNames(report.Failed), "@test/e")
	if got := labelOf(t, h, "d"); got != "d1" {
		t.Fatalf("d = %q", got)
	}
}

func TestPerPluginAtReloadKeepsAConsumerEditedTogetherWithAProviderWhoseNewBuildFailsTheClosureRuleThroughBuilds(t *testing.T) {
	root := tempRoot(t)
	writeFacet(t, root, "provider", "provider 1")
	writeFacet(t, root, "consumer", "consumer 1")
	plugins := component(t, root)
	h := newTestHost(t)
	loadWorker(t, plugins, h)
	shown, err := h.Get(background, itemContract("test.consumer", 1), delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, filepath.Join(root, "provider"), map[string]string{"worker.ts": "provider 2\nsyntax error: export const broken = ;"})
	writeFiles(t, filepath.Join(root, "consumer"), map[string]string{"worker.ts": "consumer 2"})
	report := reload(t, plugins, h, build(t, plugins, "@test/provider", "@test/consumer"))
	expectNames(t, "failed", failedNames(report.Failed), "@test/provider", "@test/consumer")
	if got, want := failureError(t, report.Failed, "@test/consumer").Error(), `plugin "@test/consumer": needs test.provider v2, which plugin "@test/provider" serves on its old code`; got != want {
		t.Fatalf("consumer failure = %q, want %q", got, want)
	}
	if value, err := shown.Member("label").Value(); err != nil || value != "v1 1" {
		t.Fatalf("label = %v, %v, want v1 1", value, err)
	}
}

// --- rollout -----------------------------------------------------------------------------------------------------------

// orderLog records the names of the steps that ran.
type orderLog struct {
	mu    sync.Mutex
	items []string
}

func (o *orderLog) add(name string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.items = append(o.items, name)
}

func (o *orderLog) all() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string{}, o.items...)
}

// stepOutcome is what a recording step answers: a report for one host named after it, or hosts, or an error.
type stepOutcome struct {
	report *host.ReloadReport
	hosts  []RolloutHost
	err    error
}

// step is a step recording its name into order, answering outcome, with kinds (the worker by default).
func step(name string, order *orderLog, outcome stepOutcome, kinds ...string) RolloutStep {
	if kinds == nil {
		kinds = []string{"worker"}
	}
	return RolloutStep{Name: name, Kinds: kinds, Reload: func(*Built, map[string][]PluginScript, context.Context) (StepResult, error) {
		order.add(name)
		if outcome.err != nil {
			return StepResult{}, outcome.err
		}
		if outcome.hosts != nil {
			return StepResult{Hosts: outcome.hosts}, nil
		}
		return StepResult{Hosts: []RolloutHost{{Kind: RolloutReported, Host: name, Report: *outcome.report}}}, nil
	}}
}

// reportOfFailed is a report whose plugins failed.
func reportOfFailed(failed ...string) *host.ReloadReport {
	report := &host.ReloadReport{}
	for _, plugin := range failed {
		report.Failed = append(report.Failed, host.PluginFailure{Plugin: plugin, Error: errors.New("x")})
	}
	return report
}

// mustRollout fails the test when a rollout failed.
func mustRollout(t *testing.T) func(RolloutResult, error) RolloutResult {
	return func(result RolloutResult, err error) RolloutResult {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
}

func rollout(t *testing.T, plugins Plugins, steps []RolloutStep, built *Built) RolloutResult {
	t.Helper()
	return mustRollout(t)(plugins.Rollout(steps, built, background))
}

func writeText(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRolloutRunsTheStepsInOrderClearsTheMarksOfPluginsAppliedOnEveryStepKeepsAPluginFailedOnAny(t *testing.T) {
	root := tempRoot(t)
	itemPlugin(t, root, "a", "a1")
	itemPlugin(t, root, "b", "b1")
	plugins := component(t, root)
	build(t, plugins)
	writeText(t, filepath.Join(root, "a", "worker.ts"), "// a\n")
	writeText(t, filepath.Join(root, "b", "worker.ts"), "// b\n")
	waitMarks(t, plugins, `dirty [a b]`, dirtyIs("@test/a", "@test/b"))
	built := build(t, plugins, "@test/a", "@test/b")
	order := &orderLog{}
	result := rollout(t, plugins, []RolloutStep{
		step("server", order, stepOutcome{report: reportOfFailed()}),
		step("worker", order, stepOutcome{report: reportOfFailed("@test/b")}),
	}, built)
	var names []string
	for _, each := range result.Steps {
		names = append(names, each.Name)
	}
	if got := order.all(); !slices.Equal(got, []string{"server", "worker"}) || result.Stopped != nil || !slices.Equal(names, []string{"server", "worker"}) {
		t.Fatalf("order = %v, stopped = %v, steps = %v", got, result.Stopped, names)
	}
	if marks := plugins.Marks(); !slices.Equal(marks.Dirty, []string{"@test/b"}) {
		t.Fatalf("marks = %+v, want @test/b dirty", marks)
	}
}

func TestRolloutCountsAHostsErrorAsTheStepsBuiltPluginsFailingThereAHostStoppedCleanlyDirtiesNothingD12(t *testing.T) {
	root := tempRoot(t)
	itemPlugin(t, root, "a", "a1")
	itemPlugin(t, root, "b", "b1")
	plugins := component(t, root)
	build(t, plugins)
	writeText(t, filepath.Join(root, "a", "worker.ts"), "// a\n")
	waitMarks(t, plugins, `dirty [a]`, dirtyIs("@test/a"))
	stopped := []RolloutHost{{Kind: RolloutStopped, Host: "session|S1"}}
	rollout(t, plugins, []RolloutStep{step("workers", &orderLog{}, stepOutcome{hosts: stopped})}, build(t, plugins, "@test/a"))
	if marks := plugins.Marks(); len(marks.Dirty) != 0 {
		t.Fatalf("marks = %+v, want nothing dirty", marks)
	}
	writeText(t, filepath.Join(root, "a", "worker.ts"), "// a again\n")
	waitMarks(t, plugins, `dirty [a]`, dirtyIs("@test/a"))
	crashed := []RolloutHost{
		{Kind: RolloutReported, Host: "session|S1", Report: *reportOfFailed()},
		{Kind: RolloutErrored, Host: "session|S2", Error: errors.New("crashed")},
	}
	result := rollout(t, plugins, []RolloutStep{step("workers", &orderLog{}, stepOutcome{hosts: crashed})}, build(t, plugins, "@test/a"))
	if len(result.Steps) != 1 || result.Steps[0].Name != "workers" || !reflect.DeepEqual(result.Steps[0].Hosts, crashed) {
		t.Fatalf("steps = %+v", result.Steps)
	}
	if marks := plugins.Marks(); !slices.Equal(marks.Dirty, []string{"@test/a"}) {
		t.Fatalf("marks = %+v, want @test/a dirty", marks)
	}
}

func TestRolloutKeepsDirtyAPluginWhoseBuiltKindNoStepShipped(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "a", "@test/a", map[string]string{"worker.ts": "empty", "server.ts": ""}, nil)
	plugins := component(t, root)
	build(t, plugins)
	writeText(t, filepath.Join(root, "a", "worker.ts"), "empty\n// edited\n")
	waitMarks(t, plugins, `dirty [a]`, dirtyIs("@test/a"))
	built := build(t, plugins, "@test/a")
	rollout(t, plugins, []RolloutStep{step("worker", &orderLog{}, stepOutcome{report: reportOfFailed()})}, built)
	if marks := plugins.Marks(); !slices.Equal(marks.Dirty, []string{"@test/a"}) {
		t.Fatalf("marks = %+v, want @test/a dirty", marks)
	}
	rollout(t, plugins, []RolloutStep{
		step("worker", &orderLog{}, stepOutcome{report: reportOfFailed()}),
		step("server", &orderLog{}, stepOutcome{report: reportOfFailed()}, "server"),
	}, built)
	if marks := plugins.Marks(); len(marks.Dirty) != 0 {
		t.Fatalf("marks = %+v, want nothing dirty", marks)
	}
}

func TestRolloutStopsAtAStepThatRejectsClearingNoMarkAChangeDuringTheBuildKeepsItsMark(t *testing.T) {
	root := tempRoot(t)
	itemPlugin(t, root, "a", "a1")
	plugins := component(t, root)
	build(t, plugins)
	writeText(t, filepath.Join(root, "a", "worker.ts"), "// 1\n")
	waitMarks(t, plugins, `dirty [a]`, dirtyIs("@test/a"))
	built := build(t, plugins, "@test/a")
	order := &orderLog{}
	stopped := rollout(t, plugins, []RolloutStep{
		step("server", order, stepOutcome{err: errors.New("closed")}),
		step("worker", order, stepOutcome{report: reportOfFailed()}),
	}, built)
	if got := order.all(); !slices.Equal(got, []string{"server"}) || stopped.Stopped == nil || stopped.Stopped.Step != "server" || stopped.Stopped.Error.Error() != "closed" {
		t.Fatalf("order = %v, stopped = %+v", got, stopped.Stopped)
	}
	if marks := plugins.Marks(); !slices.Equal(marks.Dirty, []string{"@test/a"}) {
		t.Fatalf("marks = %+v, want @test/a dirty", marks)
	}
	// Saved again after the build: applied, the mark stays (the build did not include the new content).
	later := build(t, plugins, "@test/a")
	writeText(t, filepath.Join(root, "a", "worker.ts"), "// 2\n")
	eventually(t, 5*time.Second, "the mark of the second save kept by an applied older build", func() bool {
		rollout(t, plugins, []RolloutStep{step("server", &orderLog{}, stepOutcome{report: reportOfFailed()})}, later)
		return slices.Equal(plugins.Marks().Dirty, []string{"@test/a"})
	})
}

// --- current sets (slice 10 D11) ---------------------------------------------------------------------------------------

func TestCurrentSetsScriptsKindBuiltOnFirstUseABuildChangesNoSetARolloutAppliesItRightBeforeAStepOfItsKind(t *testing.T) {
	root := tempRoot(t)
	itemPlugin(t, root, "a", "a1")
	writePlugin(t, root, "b", "@test/b", map[string]string{"server.ts": "empty"}, nil)
	plugins := component(t, root)
	digests := func() [][2]string {
		scripts, err := plugins.Scripts("worker", background)
		if err != nil {
			t.Fatal(err)
		}
		out := make([][2]string, len(scripts))
		for i, script := range scripts {
			out[i] = [2]string{script.Plugin.Name, string(script.Built.Kind)}
			if script.Built.Kind == BuiltFacetScript {
				out[i][1] = script.Built.Digest[:8]
			}
		}
		return out
	}
	first := digests()
	if len(first) != 1 || first[0][0] != "@test/a" {
		t.Fatalf("first = %v", first)
	}
	itemPlugin(t, root, "a", "a2")
	built := build(t, plugins, "@test/a")
	if got := digests(); !reflect.DeepEqual(got, first) {
		t.Fatalf("after a build = %v, want %v", got, first)
	}
	rejecting := RolloutStep{Name: "server", Kinds: []string{"server"}, Reload: func(*Built, map[string][]PluginScript, context.Context) (StepResult, error) {
		return StepResult{}, errors.New("closed")
	}}
	shippedStep := RolloutStep{Name: "workers", Kinds: []string{"worker"}, Reload: func(*Built, map[string][]PluginScript, context.Context) (StepResult, error) {
		return StepResult{Hosts: []RolloutHost{}}, nil
	}}
	rollout(t, plugins, []RolloutStep{rejecting, shippedStep}, built)
	if got := digests(); !reflect.DeepEqual(got, first) {
		t.Fatalf("after a rejected rollout = %v, want %v", got, first)
	}
	rollout(t, plugins, []RolloutStep{shippedStep}, built)
	if got := digests(); reflect.DeepEqual(got, first) {
		t.Fatalf("after a rollout = %v, want it changed", got)
	}
}

// --- installs through the component ------------------------------------------------------------------------------------

func TestInstallsThroughTheComponentClearsThePluginsInstallMarkAndDirtiesIt(t *testing.T) {
	root := tempRoot(t)
	itemPlugin(t, root, "a", "a1")
	tools := tempRoot(t)
	plugins, collected := componentOn(t, root, &probe{}, func(options *PluginsOptions) {
		options.Install = InstallOptions{NpmCommand: fakeNpm(t, tools, 0, false)}
	})
	build(t, plugins)
	writeText(t, filepath.Join(root, "a", "package.json"), `{"name":"@test/a","type":"module","dependencies":{}}`)
	waitMarks(t, plugins, `install [a]`, func(marks PluginsState) bool { return slices.Equal(marks.Install, []string{"@test/a"}) })
	if err := plugins.InstallDependencies("@test/a", background); err != nil {
		t.Fatal(err)
	}
	marks := plugins.Marks()
	if len(marks.Install) != 0 || !slices.Equal(marks.Dirty, []string{"@test/a"}) {
		t.Fatalf("marks = %+v, want no install, @test/a dirty", marks)
	}
	if got := collected.all(); len(got) != 0 {
		t.Fatalf("reported = %v", got)
	}
}

// --- GC ----------------------------------------------------------------------------------------------------------------

// settledHeap is the live heap after collections have settled.
func settledHeap() uint64 {
	var stats runtime.MemStats
	for range 3 {
		runtime.GC()
	}
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// expectCollected fails the test unless every state is collected, collecting as the test waits.
func expectCollected(t *testing.T, alive ...func() bool) {
	t.Helper()
	eventually(t, 5*time.Second, "the states collected", func() bool {
		runtime.GC()
		return !slices.ContainsFunc(alive, func(each func() bool) bool { return each() })
	})
}

func TestGCCollectsOldFacetsOver200ReloadsTheHeapStaysFlatAfterWarmUp(t *testing.T) {
	root := tempRoot(t)
	writeFacet(t, root, "gc", "gc a")
	states := &probe{}
	plugins, _ := componentOn(t, root, states)
	h := newTestHost(t)
	loadWorker(t, plugins, h)
	first := build(t, plugins, "@test/gc")
	writeFiles(t, filepath.Join(root, "gc"), map[string]string{"worker.ts": "gc b"})
	second := build(t, plugins, "@test/gc")
	reloadTimes := func(count int) {
		for i := range count {
			built := first
			if i%2 == 0 {
				built = second
			}
			reload(t, plugins, h, built)
		}
	}
	reloadTimes(20)
	warm := settledHeap()
	reloadTimes(200)
	after := settledHeap()
	if after > warm && after-warm >= 2*1024*1024 {
		t.Fatalf("the heap grew by %d bytes over 200 reloads, want under 2 MiB", after-warm)
	}
	made := states.alive()
	expectCollected(t, made[:len(made)-2]...)
}

func TestTheReloadQueuesAndGarbageCollectionLetsARejectedNewFacetGoOnceItsReportIsDroppedTheKeptOldOneLiveSol(t *testing.T) {
	root := tempRoot(t)
	writeFacet(t, root, "failed", "failed-gc false")
	states := &probe{}
	plugins, _ := componentOn(t, root, states)
	h := newTestHost(t)
	loadWorker(t, plugins, h)
	writeFiles(t, filepath.Join(root, "failed"), map[string]string{"worker.ts": "failed-gc true"})
	built := build(t, plugins, "@test/failed")
	// The report (its failure's error holds the new facet's closure) is dropped here; the queues must not keep it.
	names := failedNames(reload(t, plugins, h, built).Failed)
	expectNames(t, "failed", names, "@test/failed")
	made := states.alive()
	if len(made) < 2 {
		t.Fatalf("states made = %d, want at least 2", len(made))
	}
	expectCollected(t, made[1])
	if !made[0]() {
		t.Fatal("the kept old facet was collected")
	}
}

// --- fix round 1 -------------------------------------------------------------------------------------------------------

func TestFixRound1SerializesReloadsOfOneHostTwoStartedTogetherKeepBothChangesAstraSolOpusS1(t *testing.T) {
	root := tempRoot(t)
	itemPlugin(t, root, "a", "a1")
	itemPlugin(t, root, "b", "b1")
	plugins := component(t, root)
	h := newTestHost(t)
	loadWorker(t, plugins, h)
	itemPlugin(t, root, "a", "a2")
	builtA := build(t, plugins, "@test/a")
	itemPlugin(t, root, "b", "b2")
	builtB := build(t, plugins, "@test/b")
	reports := make([]host.ReloadReport, 2)
	errs := make([]error, 2)
	var group sync.WaitGroup
	for i, built := range []*Built{builtA, builtB} {
		group.Go(func() { reports[i], errs[i] = plugins.Reload(h, built, background) })
	}
	group.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	// Each reload keeps both changes: they ran one after another, whichever came first.
	expectNames(t, "first changed", reports[0].Plugins.Changed, "@test/a")
	expectNames(t, "second changed", reports[1].Plugins.Changed, "@test/b")
	if got := []string{labelOf(t, h, "a"), labelOf(t, h, "b")}; !slices.Equal(got, []string{"a2", "b2"}) {
		t.Fatalf("labels = %q", got)
	}
}

func TestFixRound1AppliesTheRemovalsOfTheBuildItWasGivenNeverALaterDiscoverysAstra(t *testing.T) {
	root := tempRoot(t)
	itemPlugin(t, root, "a", "a1")
	itemPlugin(t, root, "b", "b1")
	plugins := component(t, root)
	h := newTestHost(t)
	loadWorker(t, plugins, h)
	itemPlugin(t, root, "a", "a2")
	first := build(t, plugins, "@test/a")
	if err := os.RemoveAll(filepath.Join(root, "b")); err != nil {
		t.Fatal(err)
	}
	waitMarks(t, plugins, "set", func(marks PluginsState) bool { return marks.Set })
	later := build(t, plugins)
	expectNames(t, "first.Removed", first.Removed)
	expectNames(t, "later.Removed", later.Removed, "@test/b")
	report := reload(t, plugins, h, first)
	expectChanges(t, report.Plugins, nil, []string{"@test/a"}, nil)
	expectNames(t, "removed", reload(t, plugins, h, later).Plugins.Removed, "@test/b")
}

func TestFixRound1LeavesAPluginOutOfAHostWhoseKindItsRebuiltFacetsNoLongerHaveOpusRecheckR1(t *testing.T) {
	root := tempRoot(t)
	itemPlugin(t, root, "a", "a1")
	plugins := component(t, root)
	h := newTestHost(t)
	loadWorker(t, plugins, h)
	// The plugin keeps a server facet only.
	if err := os.Remove(filepath.Join(root, "a", "worker.ts")); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, filepath.Join(root, "a"), map[string]string{"server.ts": "empty"})
	waitMarks(t, plugins, "set", func(marks PluginsState) bool { return marks.Set })
	built := build(t, plugins, "@test/a")
	expectNames(t, "removed", built.Removed)
	expectChanges(t, reload(t, plugins, h, built).Plugins, nil, nil, []string{"@test/a"})
}

func TestFixRound1ReloadsACopyOfABuildAsTheBuildItselfItIsPlainDataAstra(t *testing.T) {
	root := tempRoot(t)
	itemPlugin(t, root, "a", "a1")
	plugins := component(t, root)
	h := newTestHost(t)
	loadWorker(t, plugins, h)
	item, err := h.Get(background, itemContract("test.a", 1), delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	itemPlugin(t, root, "a", "a2")
	waitMarks(t, plugins, `dirty [a]`, dirtyIs("@test/a"))
	built := build(t, plugins, "@test/a")
	clone := *built
	expectNames(t, "changed", reload(t, plugins, h, &clone).Plugins.Changed, "@test/a")
	if value, err := item.Member("label").Value(); err != nil || value != "a2" {
		t.Fatalf("label = %v, %v, want a2", value, err)
	}
	// The marks clear only through the build object itself (a copy carries no snapshot).
	step, err := plugins.Step("worker", h)
	if err != nil {
		t.Fatal(err)
	}
	cloned := *built
	result := rollout(t, plugins, []RolloutStep{step}, &cloned)
	if result.Stopped != nil {
		t.Fatalf("stopped = %+v", result.Stopped)
	}
	if marks := plugins.Marks(); !slices.Equal(marks.Dirty, []string{"@test/a"}) {
		t.Fatalf("marks = %+v, want @test/a dirty", marks)
	}
}

func TestFixRound1ReadsAChangedPackageJSONAgainAtTheNextBuildANewDependencyIsAcceptedAstraSol(t *testing.T) {
	root := tempRoot(t)
	tools := tempRoot(t)
	dir := writePlugin(t, root, "a", "@test/a", map[string]string{
		"worker.ts": "import ./helper.ts\nempty",
		"helper.ts": "old",
	}, nil)
	plugins, _ := componentOn(t, root, &probe{}, func(options *PluginsOptions) {
		options.Install = InstallOptions{NpmCommand: fakeNpm(t, tools, 0, false)}
	})
	first := build(t, plugins)
	if len(first.Facets) != 1 || first.Facets[0].Kind != BuiltFacetScript {
		t.Fatalf("facets = %+v", first.Facets)
	}
	writeFiles(t, dir, map[string]string{"package.json": `{"name":"@test/a","type":"module","dependencies":{"new-dep":"1.0.0"}}`})
	waitMarks(t, plugins, `install [a]`, func(marks PluginsState) bool { return slices.Equal(marks.Install, []string{"@test/a"}) })
	writeFiles(t, dir, map[string]string{"helper.ts": "new"})
	if err := plugins.InstallDependencies("@test/a", background); err != nil {
		t.Fatal(err)
	}
	if marks := plugins.Marks(); marks.Set {
		t.Fatalf("marks = %+v, want set false", marks)
	}
	built := build(t, plugins, "@test/a")
	if len(built.Facets) != 1 || built.Facets[0].Kind != BuiltFacetScript || built.Facets[0].Plugin != "@test/a" {
		t.Fatalf("facets = %+v", built.Facets)
	}
	if got := plugins.Discovery().Plugins[0].Dependencies; !slices.Equal(got, []string{"new-dep"}) {
		t.Fatalf("dependencies = %v", got)
	}
}

// plugins.test.ts / workers.test.ts 'two rollouts started together each ship their own build': a step reads the set ITS rollout
// applied (Pi: no await lies between the `ship` and the step's synchronous part, so `plugins.current(kind)` is the rollout's
// own there; the Go step is handed the applied sets).
func TestRolloutHandsEachStepTheSetItsOwnRolloutApplied(t *testing.T) {
	root := tempRoot(t)
	itemPlugin(t, root, "a", "a1")
	itemPlugin(t, root, "b", "b1")
	plugins := component(t, root)
	h := newTestHost(t)
	loadWorker(t, plugins, h)
	itemPlugin(t, root, "a", "a2")
	builtA := build(t, plugins, "@test/a")
	itemPlugin(t, root, "b", "b2")
	builtB := build(t, plugins, "@test/b")
	digestOf := func(scripts []PluginScript, name string) string {
		for _, script := range scripts {
			if script.Plugin.Name == name {
				return script.Built.Digest
			}
		}
		return ""
	}
	firstShipped := make(chan struct{})
	secondShipped := make(chan struct{})
	var seenByA, seenByB map[string][]PluginScript
	stepA := RolloutStep{Name: "a", Kinds: []string{"worker"}, Reload: func(_ *Built, applied map[string][]PluginScript, _ context.Context) (StepResult, error) {
		seenByA = applied
		close(firstShipped)
		// The other rollout ships its build while this step still runs.
		<-secondShipped
		return StepResult{}, nil
	}}
	stepB := RolloutStep{Name: "b", Kinds: []string{"worker"}, Reload: func(_ *Built, applied map[string][]PluginScript, _ context.Context) (StepResult, error) {
		seenByB = applied
		close(secondShipped)
		return StepResult{}, nil
	}}
	var group sync.WaitGroup
	group.Go(func() {
		if _, err := plugins.Rollout([]RolloutStep{stepA}, builtA, background); err != nil {
			t.Error(err)
		}
	})
	<-firstShipped
	if _, err := plugins.Rollout([]RolloutStep{stepB}, builtB, background); err != nil {
		t.Fatal(err)
	}
	group.Wait()
	facetDigest := func(built *Built, plugin string) string {
		for _, facet := range built.Facets {
			if facet.Plugin == plugin && facet.Facet == "worker" {
				return facet.Digest
			}
		}
		return ""
	}
	wantA, wantB := facetDigest(builtA, "@test/a"), facetDigest(builtB, "@test/b")
	if got := digestOf(seenByA["worker"], "@test/a"); got != wantA {
		t.Fatalf("A's applied set has a = %q, want its own build %q", got, wantA)
	}
	if got := digestOf(seenByA["worker"], "@test/b"); got == wantB {
		t.Fatalf("A's applied set already holds B's build of b")
	}
	if got := digestOf(seenByB["worker"], "@test/b"); got != wantB {
		t.Fatalf("B's applied set has b = %q, want its own build %q", got, wantB)
	}
}
