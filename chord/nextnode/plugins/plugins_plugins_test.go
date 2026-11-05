package plugins

// pi: packages/chord/src/next-node/plugins/plugins.ts

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
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports packages/chord/test/next-node/plugins/plugins.test.ts (all but "end to end: a server and a worker", which is
// plugins_endtoend_test.go). Tests of the plugins component (slice 7c §4, §5, §7): per plugin at startup and reload (a failing
// build, evaluation, setup; unchanged plugins untouched; added and removed plugins; the closure rule through builds),
// `rollout` (order, stopping at a rejecting step, marks), installs through the component, GC over 200 reloads. The component
// logic that needs no Node (Build, Scripts, rollout order and marks, installs, fake steps) runs on real builds with no
// NodeHost; whatever loads or reloads facets runs on a real Node host (docs/plan/chord-plugins-node-evaluator.md), where a
// case that Pi writes against a LOCAL type (`host.get(Item)`, a held `item.label.value`) or a JavaScript object (WeakRefs,
// the heap, `batch.commit`) is read through the probe, which runs the same JavaScript in the subprocess. Each such case says so.

// CreatePlugins returns the concrete *Plugins (review item).
var _ func(PluginsOptions, context.Context) *Plugins = CreatePlugins

// pcReported collects what the component reports.
type pcReported struct {
	mu    sync.Mutex
	items []PluginsError
}

func (r *pcReported) add(err PluginsError) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, err)
}

func (r *pcReported) all() []PluginsError {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]PluginsError{}, r.items...)
}

// pcOptions is what a test changes of the component it makes: host packages (specifiers kept external), Node's flags, the
// install command.
type pcOptions struct {
	modules []string
	gc      bool
	install InstallOptions
	// hostError gets what the Node hosts' hosts report in the background (NodeOptions.OnError).
	hostError func(NodeHostError)
}

// pcComponent is the component over a root (plugins.test.ts's `component`), its reports collected, and the cache directory
// its Node hosts are started with.
type pcComponent struct {
	*Plugins
	cache    string
	reported *pcReported
	// quieted is whether quiet ran with the watcher started.
	quieted bool
}

// quiet is called after a use of the component: once its watcher started, it settles it and clears the marks. What the case
// wrote before its first use (the plugins' directories and files, a fixture's copy) is reported by the file system on its own
// time (Windows reports a directory's last-write time lazily), and would be marks the case did not make: the case starts from the
// marks of what it does.
func (c *pcComponent) quiet(t *testing.T) {
	t.Helper()
	if c.quieted {
		return
	}
	var watching bool
	c.section(func() { watching = c.watcher.fs != nil })
	if !watching {
		return
	}
	c.quieted = true
	settle(t, c.Plugins)
	c.section(func() { forgetMarks(c.watcher) })
}

// pcKinds is the fixtures' app's kinds (support.ts's `kindsOf`).
func pcKinds(modules []string) []KindDefinition {
	packages := append(slices.Clone(pcHostPackages), modules...)
	return []KindDefinition{
		{Name: "server", Platform: PlatformNode, HostPackages: packages},
		{Name: "worker", Platform: PlatformNode, HostPackages: packages},
		{Name: "web", Platform: PlatformBrowser, HostPackages: packages},
	}
}

func pcNew(t *testing.T, root string, opts ...pcOptions) *pcComponent {
	t.Helper()
	var chosen pcOptions
	if len(opts) > 0 {
		chosen = opts[0]
	}
	cache := t.TempDir()
	node := NodeOptions{CacheDir: cache, OnError: chosen.hostError}
	if chosen.gc {
		node.ExecArgv = []string{"--expose-gc"}
	}
	reported := &pcReported{}
	created := CreatePlugins(PluginsOptions{
		Roots: []string{root}, Kinds: pcKinds(chosen.modules), Install: chosen.install, OnError: reported.add, Node: node,
	}, background)
	t.Cleanup(created.Close)
	return &pcComponent{Plugins: created, cache: cache, reported: reported}
}

// pcTree is the extracted tree's Pi sources, as a file URL prefix: what a module of a test imports Pi's modules from.
func (c *pcComponent) src(t *testing.T) string {
	t.Helper()
	tree, err := extractNode(c.cache)
	if err != nil {
		t.Fatal(err)
	}
	return "file://" + filepath.ToSlash(filepath.Join(tree, "pi", "chord", "src")) + "/"
}

// pcLoaded is a Node host of the component, the Go host that routes the worker kind to it, and its probe.
type pcLoaded struct {
	node   *NodeHost
	parent host.Host
	report host.LoadReport
	probe  *probe
}

// loadWorker is `plugins.load(host, { kind: "worker", core, modules })`: a Node host with the probe (whose ops are pcLabelOps
// and ops) and core, closed when the test ends.
func (c *pcComponent) loadWorker(t *testing.T, ops string, modules HostPackages, core ...CoreFacet) pcLoaded {
	t.Helper()
	return c.loadWorkerWith(t, ops, modules, nil, core...)
}

// loadWorkerWith is loadWorker with load changed by edit (the host's options: timeouts, compression, ...).
func (c *pcComponent) loadWorkerWith(t *testing.T, ops string, modules HostPackages, edit func(load *PluginsLoad), core ...CoreFacet) pcLoaded {
	t.Helper()
	address := workerKind.Address(nil)
	load := PluginsLoad{
		Kind: "worker", Core: append([]CoreFacet{probeCore(t, c.cache, pcLabelOps+ops)}, core...), Modules: modules,
		Address: &address, Principal: workerPrincipal, Peer: serverPrincipal,
	}
	if edit != nil {
		edit(&load)
	}
	node, report, err := c.Load(load, background)
	if err != nil {
		t.Fatal(err)
	}
	c.quiet(t)
	t.Cleanup(func() { _ = node.Close(background) })
	parent := serverFor(t, workerRoute(node))
	return pcLoaded{node: node, parent: parent, report: report, probe: probeOn(t, parent)}
}

// label is the label `test.<dir>` shows on the Node host.
func (h pcLoaded) label(dir string) string {
	h.probe.t.Helper()
	text, ok := h.probe.must("label", map[string]any{"dir": dir}).(string)
	if !ok {
		h.probe.t.Fatalf("label %s is no text", dir)
	}
	return text
}

// coreFile writes a core facet's module: body after the import of Pi's `next` (the names of next/index.ts).
func (c *pcComponent) coreFile(t *testing.T, name, imports, body string) CoreFacet {
	t.Helper()
	file := filepath.Join(t.TempDir(), "core.mjs")
	text := `import { ` + imports + ` } from "` + c.src(t) + `next/index.ts";` + "\n" + body
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return CoreFacet{Name: name, Module: file}
}

// build is `plugins.build(names)`.
func (c *pcComponent) build(t *testing.T, names ...string) *Built {
	t.Helper()
	built, err := c.Build(names, background)
	if err != nil {
		t.Fatal(err)
	}
	c.quiet(t)
	return built
}

// reload is `plugins.reload(host, built)`.
func (c *pcComponent) reload(t *testing.T, h pcLoaded, built *Built) host.ReloadReport {
	t.Helper()
	report, err := c.Reload(h.node, built, background)
	if err != nil {
		t.Fatal(err)
	}
	c.quiet(t)
	return report
}

// waitMarks is `vi.waitFor(() => expect(plugins.state.value).toMatchObject(...), { timeout: 5_000 })`, with 30 s: the event of
// a file took 17 s on a Windows runner running this package with the workers' (an idle one takes under a second).
func (c *pcComponent) waitMarks(t *testing.T, what string, check func(PluginsState) bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		marks := c.Marks()
		// Windows marks the directories tempRoot filled that the test did not write to (withoutCopyStamps).
		if check(marks) || runtime.GOOS == "windows" && check(withoutCopyStamps(t, marks)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after 30s waiting for %s; the marks are %+v", what, marks)
		}
		time.Sleep(5 * time.Millisecond)
	}
	settle(t, c.Plugins)
}

func pcDirty(names ...string) func(PluginsState) bool {
	return func(marks PluginsState) bool { return slices.Equal(marks.Dirty, names) }
}

// pcFailedList is the failures of a report as plugin names, in report order (plugins.test.ts's `failedNames`).
func pcFailedList(failed []host.PluginFailure) []string {
	names := []string{}
	for _, failure := range failed {
		names = append(names, failure.Plugin)
	}
	return names
}

// pcWrite is `writeFile(join(root, dir, file), text)`.
func pcWrite(t *testing.T, root, dir, file, text string) {
	t.Helper()
	writeFiles(t, filepath.Join(root, dir), map[string]string{file: text})
}

func pcEmpty() host.PluginChanges {
	return host.PluginChanges{Added: []string{}, Changed: []string{}, Removed: []string{}}
}

// plugins.test.ts › per plugin at startup › leaves out a plugin whose build fails, whose evaluation throws or whose setup
// throws; loads the others and core. (`host.get(Good)` and `host.get(Item)` are local types of the subprocess: read with
// the probe.)
func TestPluginsPerPluginAtStartupLeavesOutAPluginWhoseBuildFailsWhoseEvaluationThrowsOrWhoseSetupThrowsLoadsTheOthersAndCore(t *testing.T) {
	root := tempRoot(t)
	pcItemPlugin(t, root, "good", "good")
	pcPluginSource(t, root, "syntax", "export default {")
	pcPluginSource(t, root, "throws", "throw new Error(\"at import\");\nexport default {};")
	pcPluginSource(t, root, "setup", "import { defineFacet } from \"@earendil-works/chord/next\";\nexport default defineFacet(() => { throw new Error(\"in setup\"); });")
	c := pcNew(t, root)
	core := c.coreFile(t, "@app/core", "constant, defineFacet, local, str", `const Item = local("test.item", { version: 1, params: {}, members: { label: str() } });
export default defineFacet((h) => h.serve(Item, () => ({ label: constant("core") })));
`)
	h := c.loadWorker(t, "", nil, core)
	if len(h.report.Failed) != 3 {
		t.Fatalf("failed = %+v", h.report.Failed)
	}
	for i, want := range []string{"@test/syntax", "@test/throws", "@test/setup"} {
		if h.report.Failed[i].Plugin != want {
			t.Fatalf("failed[%d] = %s, want %s", i, h.report.Failed[i].Plugin, want)
		}
	}
	syntax := h.report.Failed[0].Error.Error()
	if !regexpMatch(`^plugin "@test/syntax": its worker facet failed to build:\nworker\.ts:1:`, syntax) {
		t.Fatalf("syntax error = %q", syntax)
	}
	if got := h.report.Failed[1].Error.Error(); got != "at import" {
		t.Fatalf("throws error = %q", got)
	}
	if got := h.report.Failed[2].Error.Error(); got != `plugin "@test/setup": in setup` {
		t.Fatalf("setup error = %q", got)
	}
	if got := []string{h.label("good"), h.label("item")}; !slices.Equal(got, []string{"good", "core"}) {
		t.Fatalf("labels = %v, want [good core]", got)
	}
}

// pcLoadedABC is plugins.test.ts's `loaded()` of "per plugin at reload": a host loaded with plugins a, b and c.
func pcLoadedABC(t *testing.T) (string, *pcComponent, pcLoaded) {
	t.Helper()
	root := tempRoot(t)
	for _, dir := range []string{"a", "b", "c"} {
		pcItemPlugin(t, root, dir, dir+"1")
	}
	c := pcNew(t, root)
	return root, c, c.loadWorker(t, "", nil)
}

// plugins.test.ts › per plugin at reload › rebuilds one plugin of three: only it changes; the others are passed as the same
// facets, untouched.
func TestPluginsPerPluginAtReloadRebuildsOnePluginOfThreeOnlyItChangesTheOthersArePassedAsTheSameFacetsUntouched(t *testing.T) {
	root, c, h := pcLoadedABC(t)
	pcItemPlugin(t, root, "b", "b2")
	built := c.build(t, "@test/b")
	if got := pcBuiltFacets(built); !reflect.DeepEqual(got, [][2]string{{"@test/b", "worker"}}) {
		t.Fatalf("facets = %v", got)
	}
	report := c.reload(t, h, built)
	if want := (host.PluginChanges{Added: []string{}, Changed: []string{"@test/b"}, Removed: []string{}}); !reflect.DeepEqual(report.Plugins, want) {
		t.Fatalf("plugins = %+v", report.Plugins)
	}
	if want := []host.ReloadedObject{{Object: "test.b", Kind: host.ReloadSwapped}}; !reflect.DeepEqual(report.Objects, want) {
		t.Fatalf("objects = %+v", report.Objects)
	}
	if got := []string{h.label("a"), h.label("b"), h.label("c")}; !slices.Equal(got, []string{"a1", "b2", "c1"}) {
		t.Fatalf("labels = %v", got)
	}
	// Built again unchanged: the same digest, nothing reloaded.
	again := c.reload(t, h, c.build(t, "@test/b"))
	if !reflect.DeepEqual(again.Plugins, pcEmpty()) {
		t.Fatalf("again = %+v", again.Plugins)
	}
}

// pcBuiltFacets is the plugin and kind of each facet of built.
func pcBuiltFacets(built *Built) [][2]string {
	out := make([][2]string, len(built.Facets))
	for i, facet := range built.Facets {
		out[i] = [2]string{facet.Plugin, facet.Facet}
	}
	return out
}

// plugins.test.ts › per plugin at reload › keeps the loaded facet of a plugin whose build fails or whose evaluation throws;
// the others apply.
func TestPluginsPerPluginAtReloadKeepsTheLoadedFacetOfAPluginWhoseBuildFailsOrWhoseEvaluationThrowsTheOthersApply(t *testing.T) {
	root, c, h := pcLoadedABC(t)
	pcWrite(t, root, "a", "worker.ts", "export default {")
	pcWrite(t, root, "b", "worker.ts", "throw new Error(\"at import\");\nexport default {};")
	pcItemPlugin(t, root, "c", "c2")
	report := c.reload(t, h, c.build(t, "@test/a", "@test/b", "@test/c"))
	if want := (host.PluginChanges{Added: []string{}, Changed: []string{"@test/c"}, Removed: []string{}}); !reflect.DeepEqual(report.Plugins, want) {
		t.Fatalf("plugins = %+v", report.Plugins)
	}
	if got := pcFailedList(report.Failed); !slices.Equal(got, []string{"@test/a", "@test/b"}) {
		t.Fatalf("failed = %v", got)
	}
	if got := []string{h.label("a"), h.label("b"), h.label("c")}; !slices.Equal(got, []string{"a1", "b1", "c2"}) {
		t.Fatalf("labels = %v", got)
	}
}

// plugins.test.ts › per plugin at reload › adds a plugin discovered since, removes one no longer there; an added plugin that
// fails is left out.
func TestPluginsPerPluginAtReloadAddsAPluginDiscoveredSinceRemovesOneNoLongerThereAnAddedPluginThatFailsIsLeftOut(t *testing.T) {
	root, c, h := pcLoadedABC(t)
	pcItemPlugin(t, root, "d", "d1")
	pcPluginSource(t, root, "e", "export default {")
	if err := os.RemoveAll(filepath.Join(root, "c")); err != nil {
		t.Fatal(err)
	}
	c.waitMarks(t, "set", func(marks PluginsState) bool { return marks.Set })
	built := c.build(t)
	if !slices.Equal(built.Removed, []string{"@test/c"}) {
		t.Fatalf("removed = %v", built.Removed)
	}
	report := c.reload(t, h, built)
	if want := (host.PluginChanges{Added: []string{"@test/d"}, Changed: []string{}, Removed: []string{"@test/c"}}); !reflect.DeepEqual(report.Plugins, want) {
		t.Fatalf("plugins = %+v", report.Plugins)
	}
	if got := pcFailedList(report.Failed); !slices.Equal(got, []string{"@test/e"}) {
		t.Fatalf("failed = %v", got)
	}
	if got := h.label("d"); got != "d1" {
		t.Fatalf("label d = %q", got)
	}
}

// plugins.test.ts › per plugin at reload › keeps a consumer edited together with a provider whose new build fails (the
// closure rule through builds). Pi's `shown` is a held reference to the local `test.consumer`; the probe reads it with a
// `ctx.get` of its own: the object lives on its old code, so the label is read from it the same.
func TestPluginsPerPluginAtReloadKeepsAConsumerEditedTogetherWithAProviderWhoseNewBuildFailsTheClosureRuleThroughBuilds(t *testing.T) {
	root := tempRoot(t)
	provider := func(version string) string {
		return strings.Join([]string{
			`import { constant, defineFacet, local, int } from "@earendil-works/chord/next";`,
			`export const P = local("test.provider", { version: ` + version + `, params: {}, members: { n: int() } });`,
			`export default defineFacet((host) => host.serve(P, () => ({ n: constant(` + version + `) })));`,
		}, "\n")
	}
	consumer := func(version string) string {
		return strings.Join([]string{
			`import { defineFacet, local, int, str } from "@earendil-works/chord/next";`,
			`const P = local("test.provider", { version: ` + version + `, params: {}, members: { n: int() } });`,
			`const C = local("test.consumer", { version: 1, params: {}, members: { label: str() } });`,
			`export default defineFacet((host) => host.serve(C, (_, self) => ({ label: self.derive([self.get(P, {}).n], ([n]) => "v` + version + ` " + n) })));`,
		}, "\n")
	}
	writePlugin(t, root, "provider", "@test/provider", map[string]string{"worker.ts": provider("1")}, nil)
	writePlugin(t, root, "consumer", "@test/consumer", map[string]string{"worker.ts": consumer("1")}, nil)
	c := pcNew(t, root)
	h := c.loadWorker(t, "", nil)
	if got := h.label("consumer"); got != "v1 1" {
		t.Fatalf("label before = %q", got)
	}
	pcWrite(t, root, "provider", "worker.ts", provider("2")+"\nexport const broken = ;")
	pcWrite(t, root, "consumer", "worker.ts", consumer("2"))
	report := c.reload(t, h, c.build(t, "@test/provider", "@test/consumer"))
	if got := pcFailedList(report.Failed); !slices.Equal(got, []string{"@test/provider", "@test/consumer"}) {
		t.Fatalf("failed = %v", got)
	}
	if got := report.Failed[1].Error.Error(); got != `plugin "@test/consumer": needs test.provider v2, which plugin "@test/provider" serves on its old code` {
		t.Fatalf("consumer error = %q", got)
	}
	if got := h.label("consumer"); got != "v1 1" {
		t.Fatalf("label after = %q, want v1 1", got)
	}
}

// pcOutcome is what a fake step does (plugins.test.ts's `step` outcome): one host's report, or hosts, or an error.
type pcOutcome struct {
	report *host.ReloadReport
	hosts  []RolloutHost
	err    error
}

// pcStep is a step recording its name into order (plugins.test.ts's `step`).
func pcStep(name string, order *[]string, outcome pcOutcome, kinds ...string) RolloutStep {
	if kinds == nil {
		kinds = []string{"worker"}
	}
	return RolloutStep{
		Name: name, Kinds: kinds,
		Reload: func(*Built, map[string][]PluginScript, context.Context) (StepResult, error) {
			*order = append(*order, name)
			if outcome.err != nil {
				return StepResult{}, outcome.err
			}
			if outcome.hosts != nil {
				return StepResult{Hosts: outcome.hosts}, nil
			}
			return StepResult{Hosts: []RolloutHost{{Kind: RolloutReported, Host: name, Report: *outcome.report}}}, nil
		},
	}
}

// pcReport is plugins.test.ts's `report(failed)`.
func pcReport(failed ...string) *host.ReloadReport {
	report := &host.ReloadReport{Plugins: pcEmpty(), Failed: []host.PluginFailure{}, Objects: []host.ReloadedObject{}}
	for _, plugin := range failed {
		report.Failed = append(report.Failed, host.PluginFailure{Plugin: plugin, Error: errors.New("x")})
	}
	return report
}

func (c *pcComponent) rollout(t *testing.T, steps []RolloutStep, built *Built) RolloutResult {
	t.Helper()
	result, err := c.Rollout(steps, built, background)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// plugins.test.ts › rollout › runs the steps in order, clears the marks of plugins applied on every step, keeps a plugin
// failed on any. (No Node: fake steps over real builds.)
func TestPluginsRolloutRunsTheStepsInOrderClearsTheMarksOfPluginsAppliedOnEveryStepKeepsAPluginFailedOnAny(t *testing.T) {
	root := tempRoot(t)
	pcItemPlugin(t, root, "a", "a1")
	pcItemPlugin(t, root, "b", "b1")
	c := pcNew(t, root)
	c.build(t)
	pcWrite(t, root, "a", "worker.ts", "// a\n")
	pcWrite(t, root, "b", "worker.ts", "// b\n")
	c.waitMarks(t, "dirty a and b", pcDirty("@test/a", "@test/b"))
	built := c.build(t, "@test/a", "@test/b")
	var order []string
	result := c.rollout(t, []RolloutStep{
		pcStep("server", &order, pcOutcome{report: pcReport()}), pcStep("worker", &order, pcOutcome{report: pcReport("@test/b")}),
	}, built)
	var names []string
	for _, step := range result.Steps {
		names = append(names, step.Name)
	}
	if !slices.Equal(order, []string{"server", "worker"}) || result.Stopped != nil || !slices.Equal(names, []string{"server", "worker"}) {
		t.Fatalf("order = %v, stopped = %v, steps = %v", order, result.Stopped, names)
	}
	if marks := c.Marks(); !slices.Equal(marks.Dirty, []string{"@test/b"}) {
		t.Fatalf("dirty = %v, want [@test/b]", marks.Dirty)
	}
}

// plugins.test.ts › rollout › counts a host's error as the step's built plugins failing there; a host stopped cleanly
// dirties nothing (D12).
func TestPluginsRolloutCountsAHostsErrorAsTheStepsBuiltPluginsFailingThereAHostStoppedCleanlyDirtiesNothing(t *testing.T) {
	root := tempRoot(t)
	pcItemPlugin(t, root, "a", "a1")
	pcItemPlugin(t, root, "b", "b1")
	c := pcNew(t, root)
	c.build(t)
	pcWrite(t, root, "a", "worker.ts", "// a\n")
	c.waitMarks(t, "dirty a", pcDirty("@test/a"))
	var none []string
	stopped := []RolloutHost{{Kind: RolloutStopped, Host: "session|S1"}}
	c.rollout(t, []RolloutStep{pcStep("workers", &none, pcOutcome{hosts: stopped})}, c.build(t, "@test/a"))
	if marks := c.Marks(); len(marks.Dirty) != 0 {
		t.Fatalf("dirty = %v, want none", marks.Dirty)
	}
	pcWrite(t, root, "a", "worker.ts", "// a again\n")
	c.waitMarks(t, "dirty a again", pcDirty("@test/a"))
	crashed := []RolloutHost{
		{Kind: RolloutReported, Host: "session|S1", Report: *pcReport()},
		{Kind: RolloutErrored, Host: "session|S2", Error: errors.New("crashed")},
	}
	result := c.rollout(t, []RolloutStep{pcStep("workers", &none, pcOutcome{hosts: crashed})}, c.build(t, "@test/a"))
	if want := []RolloutStepResult{{Name: "workers", Hosts: crashed}}; !reflect.DeepEqual(result.Steps, want) {
		t.Fatalf("steps = %+v", result.Steps)
	}
	if marks := c.Marks(); !slices.Equal(marks.Dirty, []string{"@test/a"}) {
		t.Fatalf("dirty = %v, want [@test/a]", marks.Dirty)
	}
}

// plugins.test.ts › rollout › keeps dirty a plugin whose built kind no step shipped.
func TestPluginsRolloutKeepsDirtyAPluginWhoseBuiltKindNoStepShipped(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "a", "@test/a", map[string]string{"worker.ts": "export default { setup: () => {} };", "server.ts": ""}, nil)
	c := pcNew(t, root)
	c.build(t)
	pcWrite(t, root, "a", "worker.ts", "export default { setup: () => {} }; // edited\n")
	c.waitMarks(t, "dirty a", pcDirty("@test/a"))
	built := c.build(t, "@test/a")
	var none []string
	c.rollout(t, []RolloutStep{pcStep("worker", &none, pcOutcome{report: pcReport()})}, built)
	if marks := c.Marks(); !slices.Equal(marks.Dirty, []string{"@test/a"}) {
		t.Fatalf("dirty = %v, want [@test/a]", marks.Dirty)
	}
	c.rollout(t, []RolloutStep{
		pcStep("worker", &none, pcOutcome{report: pcReport()}), pcStep("server", &none, pcOutcome{report: pcReport()}, "server"),
	}, built)
	if marks := c.Marks(); len(marks.Dirty) != 0 {
		t.Fatalf("dirty = %v, want none", marks.Dirty)
	}
}

// plugins.test.ts › rollout › stops at a step that rejects, clearing no mark; a change during the build keeps its mark.
func TestPluginsRolloutStopsAtAStepThatRejectsClearingNoMarkAChangeDuringTheBuildKeepsItsMark(t *testing.T) {
	root := tempRoot(t)
	pcItemPlugin(t, root, "a", "a1")
	c := pcNew(t, root)
	c.build(t)
	pcWrite(t, root, "a", "worker.ts", "// 1\n")
	c.waitMarks(t, "dirty a", pcDirty("@test/a"))
	built := c.build(t, "@test/a")
	var order []string
	stopped := c.rollout(t, []RolloutStep{
		pcStep("server", &order, pcOutcome{err: errors.New("closed")}), pcStep("worker", &order, pcOutcome{report: pcReport()}),
	}, built)
	if !slices.Equal(order, []string{"server"}) || stopped.Stopped == nil || stopped.Stopped.Step != "server" || stopped.Stopped.Error.Error() != "closed" {
		t.Fatalf("order = %v, stopped = %+v", order, stopped.Stopped)
	}
	if marks := c.Marks(); !slices.Equal(marks.Dirty, []string{"@test/a"}) {
		t.Fatalf("dirty = %v, want [@test/a]", marks.Dirty)
	}
	// Saved again after the build: applied, the mark stays (the build did not include the new content).
	later := c.build(t, "@test/a")
	pcWrite(t, root, "a", "worker.ts", "// 2\n")
	var none []string
	eventually(t, 5*time.Second, "the mark of a stays after a rollout of a build older than the save", func() bool {
		c.rollout(t, []RolloutStep{pcStep("server", &none, pcOutcome{report: pcReport()})}, later)
		return slices.Equal(c.Marks().Dirty, []string{"@test/a"})
	})
}

// plugins.test.ts › current sets (slice 10 D11) › scripts(kind): built on first use; a build changes no set; a rollout
// applies it right before a step of its kind.
func TestPluginsCurrentSetsScriptsKindBuiltOnFirstUseABuildChangesNoSetARolloutAppliesItRightBeforeAStepOfItsKind(t *testing.T) {
	root := tempRoot(t)
	pcItemPlugin(t, root, "a", "a1")
	writePlugin(t, root, "b", "@test/b", map[string]string{"server.ts": "export default { setup: () => {} };"}, nil)
	c := pcNew(t, root)
	digests := func() [][2]string {
		scripts, err := c.Scripts("worker", background)
		if err != nil {
			t.Fatal(err)
		}
		out := [][2]string{}
		for _, script := range scripts {
			what := string(script.Built.Kind)
			if script.Built.Kind == BuiltFacetScript {
				what = script.Built.Digest[:8]
			}
			out = append(out, [2]string{script.Plugin.Name, what})
		}
		return out
	}
	first := digests()
	if len(first) != 1 || first[0][0] != "@test/a" {
		t.Fatalf("first = %v, want only @test/a", first)
	}
	pcItemPlugin(t, root, "a", "a2")
	built := c.build(t, "@test/a")
	if got := digests(); !reflect.DeepEqual(got, first) {
		t.Fatalf("a build changed the set: %v, was %v", got, first)
	}
	rejecting := RolloutStep{Name: "server", Kinds: []string{"server"}, Reload: func(*Built, map[string][]PluginScript, context.Context) (StepResult, error) {
		return StepResult{}, errors.New("closed")
	}}
	shipped := RolloutStep{Name: "workers", Kinds: []string{"worker"}, Reload: func(*Built, map[string][]PluginScript, context.Context) (StepResult, error) {
		return StepResult{Hosts: []RolloutHost{}}, nil
	}}
	c.rollout(t, []RolloutStep{rejecting, shipped}, built)
	if got := digests(); !reflect.DeepEqual(got, first) {
		t.Fatalf("a stopped rollout changed the set: %v, was %v", got, first)
	}
	c.rollout(t, []RolloutStep{shipped}, built)
	if got := digests(); reflect.DeepEqual(got, first) {
		t.Fatalf("the rollout did not apply the build: %v", got)
	}
}

// pcNode is the path of node.
func pcNode(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// plugins.test.ts › installs through the component › clears the plugin's install mark and dirties it.
func TestPluginsInstallsThroughTheComponentClearsThePluginsInstallMarkAndDirtiesIt(t *testing.T) {
	root := tempRoot(t)
	pcItemPlugin(t, root, "a", "a1")
	tools := tempRoot(t)
	writeFiles(t, tools, map[string]string{"npm.mjs": "process.exit(0);"})
	c := pcNew(t, root, pcOptions{install: InstallOptions{NpmCommand: []string{pcNode(t), filepath.Join(tools, "npm.mjs")}}})
	c.build(t)
	writeFiles(t, filepath.Join(root, "a"), map[string]string{"package.json": `{"name":"@test/a","type":"module","dependencies":{}}`})
	c.waitMarks(t, "install a", func(marks PluginsState) bool { return slices.Equal(marks.Install, []string{"@test/a"}) })
	if err := c.InstallDependencies("@test/a", background); err != nil {
		t.Fatal(err)
	}
	if marks := c.Marks(); len(marks.Install) != 0 || !slices.Equal(marks.Dirty, []string{"@test/a"}) {
		t.Fatalf("marks = %+v, want install [] dirty [@test/a]", marks)
	}
	if got := c.reported.all(); len(got) != 0 {
		t.Fatalf("reported = %+v", got)
	}
}

// pcProbeModule is the host package `@test/probe` of the GC cases: `made(value)` keeps a WeakRef to value in the subprocess
// (Pi's `probe.made`, whose `made` array the probe's ops read as `globalThis.__made`).
func pcProbeModule(t *testing.T) HostPackages {
	t.Helper()
	file := filepath.Join(t.TempDir(), "made.mjs")
	if err := os.WriteFile(file, []byte("export const made = (value) => { (globalThis.__made ??= []).push(new WeakRef(value)); };\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return HostPackages{"@test/probe": file}
}

// pcGCOps are the probe ops of support.ts's `settledHeap` and `expectCollected` (the subprocess runs with --expose-gc),
// over the WeakRefs `made` recorded.
const pcGCOps = `heap: async () => {
	for (let round = 0; round < 5; round++) {
		await new Promise((resolve) => setImmediate(resolve));
		globalThis.gc();
	}
	return process.memoryUsage().heapUsed;
},
count: async () => globalThis.__made.length,
collected: async ({ from, to }) => {
	const refs = globalThis.__made.slice(from, to);
	for (let round = 0; round < 10 && refs.some((ref) => ref.deref() !== undefined); round++) {
		await new Promise((resolve) => setImmediate(resolve));
		globalThis.gc();
	}
	return refs.map((ref) => ref.deref() === undefined);
},
alive: async ({ index }) => globalThis.__made[index].deref() !== undefined,
`

// pcCollected is expectCollected over the WeakRefs from..to-1 of `made`: whether each was collected.
func pcCollected(t *testing.T, p *probe, from, to int) []bool {
	t.Helper()
	answer, ok := p.must("collected", map[string]any{"from": from, "to": to}).([]any)
	if !ok {
		t.Fatal("collected is no list")
	}
	// `made.slice(from, to)` is shorter when fewer values were made: Pi's `expectCollected(made[1]!)` would throw on a value
	// that was never made, so a short list fails here.
	if len(answer) != to-from {
		t.Fatalf("the probe made %d of the %d values %d..%d the case reads", len(answer), to-from, from, to-1)
	}
	out := make([]bool, len(answer))
	for i, each := range answer {
		out[i], _ = each.(bool)
	}
	return out
}

func pcAllTrue(values []bool) bool {
	for _, value := range values {
		if !value {
			return false
		}
	}
	return true
}

// plugins.test.ts › GC › collects old facets over 200 reloads; the heap stays flat after warm-up. The heap and the WeakRefs
// are the subprocess's: read through the probe's ops (`settledHeap`, `expectCollected`).
func TestPluginsGCCollectsOldFacetsOver200ReloadsTheHeapStaysFlatAfterWarmUp(t *testing.T) {
	root := tempRoot(t)
	source := func(label string) string {
		return strings.Join([]string{
			`import { constant, defineFacet, local, str } from "@earendil-works/chord/next";`,
			`import { made } from "@test/probe";`,
			`const Item = local("test.gc", { version: 1, params: {}, members: { label: str() } });`,
			// A closure per evaluation, which only this evaluation's code holds.
			`const state = { label: ` + pcJSON(t, label) + `, big: new Array(10_000).fill(` + pcJSON(t, label) + `) };`,
			"made(state);",
			"export default defineFacet((host) => host.serve(Item, () => ({ label: constant(state.label) })));",
		}, "\n")
	}
	writePlugin(t, root, "gc", "@test/gc", map[string]string{"worker.ts": source("a")}, nil)
	modules := pcProbeModule(t)
	c := pcNew(t, root, pcOptions{modules: []string{"@test/probe"}, gc: true})
	h := c.loadWorker(t, pcGCOps, modules)
	first := c.build(t, "@test/gc")
	pcWrite(t, root, "gc", "worker.ts", source("b"))
	second := c.build(t, "@test/gc")
	reload := func(count int) {
		for i := range count {
			built := first
			if i%2 == 0 {
				built = second
			}
			c.reload(t, h, built)
		}
	}
	reload(20)
	warm := h.probe.must("heap", nil).(float64)
	reload(200)
	after := h.probe.must("heap", nil).(float64)
	if after-warm >= 2*1024*1024 {
		t.Fatalf("the heap grew by %v bytes over 200 reloads, want < 2 MiB", after-warm)
	}
	made := int(h.probe.must("count", nil).(float64))
	if collected := pcCollected(t, h.probe, 0, made-2); !pcAllTrue(collected) {
		t.Fatalf("collected = %v, want all true", collected)
	}
}

// plugins.test.ts › fix round 1 › serializes reloads of one host: two started together keep both changes (Astra, Sol, Opus
// S1). Goroutines start the two reloads together; each reports its own build whichever the component decided first.
func TestPluginsFixRound1SerializesReloadsOfOneHostTwoStartedTogetherKeepBothChanges(t *testing.T) {
	root := tempRoot(t)
	pcItemPlugin(t, root, "a", "a1")
	pcItemPlugin(t, root, "b", "b1")
	c := pcNew(t, root)
	h := c.loadWorker(t, "", nil)
	pcItemPlugin(t, root, "a", "a2")
	builtA := c.build(t, "@test/a")
	pcItemPlugin(t, root, "b", "b2")
	builtB := c.build(t, "@test/b")
	reports := make([]host.ReloadReport, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, built := range []*Built{builtA, builtB} {
		wg.Go(func() {
			reports[i], errs[i] = c.Reload(h.node, built, background)
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := [][]string{reports[0].Plugins.Changed, reports[1].Plugins.Changed}; !reflect.DeepEqual(got, [][]string{{"@test/a"}, {"@test/b"}}) {
		t.Fatalf("changed = %v", got)
	}
	if got := []string{h.label("a"), h.label("b")}; !slices.Equal(got, []string{"a2", "b2"}) {
		t.Fatalf("labels = %v, want [a2 b2]", got)
	}
}

// plugins.test.ts › fix round 1 › applies the removals of the build it was given, never a later discovery's (Astra).
func TestPluginsFixRound1AppliesTheRemovalsOfTheBuildItWasGivenNeverALaterDiscoverys(t *testing.T) {
	root := tempRoot(t)
	pcItemPlugin(t, root, "a", "a1")
	pcItemPlugin(t, root, "b", "b1")
	c := pcNew(t, root)
	h := c.loadWorker(t, "", nil)
	pcItemPlugin(t, root, "a", "a2")
	first := c.build(t, "@test/a")
	if err := os.RemoveAll(filepath.Join(root, "b")); err != nil {
		t.Fatal(err)
	}
	c.waitMarks(t, "set", func(marks PluginsState) bool { return marks.Set })
	later := c.build(t)
	if len(first.Removed) != 0 || !slices.Equal(later.Removed, []string{"@test/b"}) {
		t.Fatalf("removed = %v, %v", first.Removed, later.Removed)
	}
	report := c.reload(t, h, first)
	if want := (host.PluginChanges{Added: []string{}, Changed: []string{"@test/a"}, Removed: []string{}}); !reflect.DeepEqual(report.Plugins, want) {
		t.Fatalf("plugins = %+v", report.Plugins)
	}
	if got := c.reload(t, h, later).Plugins.Removed; !slices.Equal(got, []string{"@test/b"}) {
		t.Fatalf("removed = %v", got)
	}
}

// pcHoldOps are the probe ops that stand in for Pi's `vi.spyOn(batch, "commit")` that aborts right after the reload's commit.
// The reload's report waits for each started fate's `attempt.checked` (host.ts `#report`); the ops hold that promise with a
// gate, so the wait is open, and `reached` resolves when the report began to wait (the commit is done). The test aborts the
// Go context then, and `release` opens the gate afterwards (the swap's handover waits on the same promise).
func pcHoldOps(src string) string {
	return `holdChecks: async () => {
	const { Attempt } = await import("` + src + `next/host/attempt.ts");
	const proto = Attempt.prototype;
	const descriptor = Object.getOwnPropertyDescriptor(proto, "checked");
	const gate = Promise.withResolvers();
	const reached = Promise.withResolvers();
	globalThis.__hold = { proto, descriptor, gate, reached };
	Object.defineProperty(proto, "checked", {
		configurable: true,
		get() {
			const real = descriptor.get.call(this);
			reached.resolve();
			return Promise.all([real, gate.promise]).then(([cause]) => cause);
		},
	});
},
reached: async () => { await globalThis.__hold.reached.promise; },
release: async () => {
	const { proto, descriptor, gate } = globalThis.__hold;
	Object.defineProperty(proto, "checked", descriptor);
	gate.resolve();
},
`
}

// plugins.test.ts › fix round 1 › adopts what a reload committed when its wait aborts after the commit: a later reload keeps
// it (Astra). Adapted: Pi hooks `batch.commit` of its in-process host and aborts there; the host is in the subprocess, so
// the probe holds the report's wait (pcHoldOps) and Go cancels the reload's context once it is reached, after the commit. The
// held `item` is read with fresh gets of the probe (the object is the plugin's served type either way).
func TestPluginsFixRound1AdoptsWhatAReloadCommittedWhenItsWaitAbortsAfterTheCommitALaterReloadKeepsIt(t *testing.T) {
	root := tempRoot(t)
	pcItemPlugin(t, root, "a", "a1")
	pcItemPlugin(t, root, "b", "b1")
	c := pcNew(t, root)
	h := c.loadWorker(t, pcHoldOps(c.src(t)), nil)
	pcItemPlugin(t, root, "a", "a2")
	first := c.build(t, "@test/a")
	h.probe.must("holdChecks", nil)
	ctx, cancel := context.WithCancel(background)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := c.Reload(h.node, first, ctx)
		done <- err
	}()
	h.probe.must("reached", nil)
	cancel()
	err := <-done
	h.probe.must("release", nil)
	var nodeErr *NodeError
	if !errors.As(err, &nodeErr) || nodeErr.Chord == nil || nodeErr.Chord.Code != data.CodeAborted {
		t.Fatalf("error = %v, want an aborted ChordError", err)
	}
	details, jsonErr := chordjson.Marshal(nodeErr.Chord.Details)
	if jsonErr != nil {
		t.Fatal(jsonErr)
	}
	if got, want := string(details), `{"committed":{"added":[],"changed":["@test/a"],"removed":[],"failed":[]}}`; got != want {
		t.Fatalf("details = %s, want %s", got, want)
	}
	if got := h.label("a"); got != "a2" {
		t.Fatalf("label a = %q, want a2", got)
	}
	pcItemPlugin(t, root, "b", "b2")
	c.reload(t, h, c.build(t, "@test/b"))
	if got := h.label("a"); got != "a2" {
		t.Fatalf("label a after the later reload = %q, want a2", got)
	}
}

// plugins.test.ts › fix round 1 › leaves a plugin out of a host whose kind its rebuilt facets no longer have (Opus recheck
// R1).
func TestPluginsFixRound1LeavesAPluginOutOfAHostWhoseKindItsRebuiltFacetsNoLongerHave(t *testing.T) {
	root := tempRoot(t)
	pcItemPlugin(t, root, "a", "a1")
	c := pcNew(t, root)
	h := c.loadWorker(t, "", nil)
	// The plugin keeps a server facet only.
	if err := os.Remove(filepath.Join(root, "a", "worker.ts")); err != nil {
		t.Fatal(err)
	}
	pcWrite(t, root, "a", "server.ts", "import { defineFacet } from \"@earendil-works/chord/next\";\nexport default defineFacet(() => {});")
	c.waitMarks(t, "set", func(marks PluginsState) bool { return marks.Set })
	built := c.build(t, "@test/a")
	if len(built.Removed) != 0 {
		t.Fatalf("removed = %v", built.Removed)
	}
	if got, want := c.reload(t, h, built).Plugins, (host.PluginChanges{Added: []string{}, Changed: []string{}, Removed: []string{"@test/a"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("plugins = %+v", got)
	}
}

// pcClone is `structuredClone(built)`: a deep copy of the plain data (a JSON round trip: the build's exported fields, nothing of
// its identity or snapshot).
func pcClone(t *testing.T, built *Built) *Built {
	t.Helper()
	text, err := json.Marshal(built)
	if err != nil {
		t.Fatal(err)
	}
	var clone Built
	if err := json.Unmarshal(text, &clone); err != nil {
		t.Fatal(err)
	}
	if clone.self != nil || clone.snapshot.Plugins != nil || clone.snapshot.Names != nil {
		t.Fatal("a clone of the plain data carries the build's identity")
	}
	return &clone
}

// plugins.test.ts › fix round 1 › reloads a structured clone of a build as the build itself: it is plain data (Astra). The
// clone is a deep copy (another address, no shared slices or maps).
func TestPluginsFixRound1ReloadsAStructuredCloneOfABuildAsTheBuildItselfItIsPlainData(t *testing.T) {
	root := tempRoot(t)
	pcItemPlugin(t, root, "a", "a1")
	c := pcNew(t, root)
	h := c.loadWorker(t, "", nil)
	pcItemPlugin(t, root, "a", "a2")
	c.waitMarks(t, "dirty a", pcDirty("@test/a"))
	built := c.build(t, "@test/a")
	clone := pcClone(t, built)
	if len(built.Facets) == 0 || &clone.Facets[0] == &built.Facets[0] {
		t.Fatal("the clone shares the build's facets")
	}
	if got := c.reload(t, h, clone).Plugins.Changed; !slices.Equal(got, []string{"@test/a"}) {
		t.Fatalf("changed = %v", got)
	}
	if got := h.label("a"); got != "a2" {
		t.Fatalf("label a = %q, want a2", got)
	}
	// The marks clear only through the build object itself (a copy carries no snapshot).
	step, err := c.Step("worker", h.node)
	if err != nil {
		t.Fatal(err)
	}
	result := c.rollout(t, []RolloutStep{step}, pcClone(t, built))
	if result.Stopped != nil {
		t.Fatalf("stopped = %+v", result.Stopped)
	}
	if marks := c.Marks(); !slices.Equal(marks.Dirty, []string{"@test/a"}) {
		t.Fatalf("dirty = %v, want [@test/a]", marks.Dirty)
	}
}

// plugins.test.ts › fix round 1 › reads a changed package.json again at the next build: a new dependency is accepted (Astra,
// Sol). No Node.
func TestPluginsFixRound1ReadsAChangedPackageJSONAgainAtTheNextBuildANewDependencyIsAccepted(t *testing.T) {
	root := tempRoot(t)
	tools := tempRoot(t)
	writeFiles(t, tools, map[string]string{"npm.mjs": "process.exit(0);"})
	dir := writePlugin(t, root, "a", "@test/a", map[string]string{
		"worker.ts": "import { label } from \"./helper.ts\";\nexport default { setup: () => {}, label };",
		"helper.ts": `export const label = "old";`,
	}, nil)
	c := pcNew(t, root, pcOptions{install: InstallOptions{NpmCommand: []string{pcNode(t), filepath.Join(tools, "npm.mjs")}}})
	first := c.build(t)
	if len(first.Facets) != 1 || first.Facets[0].Kind != BuiltFacetScript {
		t.Fatalf("facets = %+v", first.Facets)
	}
	writeFiles(t, dir, map[string]string{"package.json": `{"name":"@test/a","type":"module","dependencies":{"new-dep":"1.0.0"}}`})
	c.waitMarks(t, "install a", func(marks PluginsState) bool { return slices.Equal(marks.Install, []string{"@test/a"}) })
	// writePackage(dir, "new-dep", { "index.js": 'exports.label = "new";' }).
	writeFiles(t, filepath.Join(dir, "node_modules", "new-dep"), map[string]string{
		"package.json": `{"name":"new-dep","version":"1.0.0","main":"index.js"}`,
		"index.js":     `exports.label = "new";`,
	})
	writeFiles(t, dir, map[string]string{"helper.ts": `export { label } from "new-dep";`})
	if err := c.InstallDependencies("@test/a", background); err != nil {
		t.Fatal(err)
	}
	// Windows marks the set for the plugin directory's own last-write time, which the new node_modules changed (see
	// withoutDirectoryStamps).
	if marks := c.Marks(); marks.Set && runtime.GOOS != "windows" {
		t.Fatalf("marks = %+v, want set false", marks)
	}
	built := c.build(t, "@test/a")
	if len(built.Facets) != 1 || built.Facets[0].Kind != BuiltFacetScript || built.Facets[0].Plugin != "@test/a" {
		t.Fatalf("facets = %+v", built.Facets)
	}
	if got := c.Discovery().Plugins[0].Dependencies; !slices.Equal(got, []string{"new-dep"}) {
		t.Fatalf("dependencies = %v", got)
	}
}

// plugins.test.ts › the reload queues and garbage collection › lets a rejected new facet go once its report is dropped, the
// kept old one live (Sol). The WeakRefs are the subprocess's: read through the probe.
func TestPluginsTheReloadQueuesAndGarbageCollectionLetsARejectedNewFacetGoOnceItsReportIsDroppedTheKeptOldOneLive(t *testing.T) {
	root := tempRoot(t)
	source := func(bad string) string {
		return strings.Join([]string{
			`import { constant, defineFacet, int, local } from "@earendil-works/chord/next";`,
			`import { made } from "@test/probe";`,
			`const state = { bad: ` + bad + `, big: new Array(10000).fill("payload") };`,
			"made(state);",
			`const Type = local("test.failed-gc", { version: 1, params: {}, members: { n: int() } });`,
			"export default defineFacet((h) => {",
			"\tif (state.bad) throw new Error(\"bad setup\");",
			"\th.serve(Type, () => ({ n: constant(state.big.length) }));",
			"});",
		}, "\n")
	}
	writePlugin(t, root, "failed", "@test/failed", map[string]string{"worker.ts": source("false")}, nil)
	c := pcNew(t, root, pcOptions{modules: []string{"@test/probe"}, gc: true})
	h := c.loadWorker(t, pcGCOps, pcProbeModule(t))
	pcWrite(t, root, "failed", "worker.ts", source("true"))
	built := c.build(t, "@test/failed")
	// The report (its failure's error holds the new facet's closure) is dropped here; the queues must not keep it.
	if got := pcFailedList(c.reload(t, h, built).Failed); !slices.Equal(got, []string{"@test/failed"}) {
		t.Fatalf("failed = %v", got)
	}
	if got := pcCollected(t, h.probe, 1, 2); !pcAllTrue(got) {
		t.Fatalf("the rejected facet's closure was not collected: %v", got)
	}
	if alive, _ := h.probe.must("alive", map[string]any{"index": 0}).(bool); !alive {
		t.Fatal("the kept old facet's closure was collected")
	}
}

// Review item: a Go panic anywhere propagates; it is never turned into a plugin failure or a rollout's host. A step's
// Reload is the app's callback: its panic leaves Rollout (no RolloutHost, no StoppedStep, no mark cleared), and the
// component stays usable.
func TestPluginsAPanicInAnAppCallbackPropagatesAndIsNeverAPluginFailure(t *testing.T) {
	root := tempRoot(t)
	pcItemPlugin(t, root, "a", "a1")
	c := pcNew(t, root)
	c.build(t)
	pcWrite(t, root, "a", "worker.ts", "// a\n")
	c.waitMarks(t, "dirty a", pcDirty("@test/a"))
	built := c.build(t, "@test/a")
	panicking := RolloutStep{Name: "worker", Kinds: []string{"worker"}, Reload: func(*Built, map[string][]PluginScript, context.Context) (StepResult, error) {
		panic("boom in a step")
	}}
	var result RolloutResult
	var returned error
	recovered := func() (value any) {
		defer func() { value = recover() }()
		result, returned = c.Rollout([]RolloutStep{panicking}, built, background)
		return nil
	}()
	if recovered != "boom in a step" {
		t.Fatalf("recovered = %v (result %+v, error %v), want the panic to propagate", recovered, result, returned)
	}
	if marks := c.Marks(); !slices.Equal(marks.Dirty, []string{"@test/a"}) {
		t.Fatalf("dirty = %v, want [@test/a]", marks.Dirty)
	}
	var none []string
	after := c.rollout(t, []RolloutStep{pcStep("worker", &none, pcOutcome{report: pcReport()})}, built)
	if after.Stopped != nil || len(c.Marks().Dirty) != 0 {
		t.Fatalf("the component is not usable after the panic: %+v, %+v", after, c.Marks())
	}
}

// regexpMatch reports whether text matches the expression.
func regexpMatch(expression, text string) bool {
	return regexp.MustCompile(expression).MatchString(text)
}
