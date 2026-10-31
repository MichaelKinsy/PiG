package plugins

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/test/next/examples/35-plugins and 38-watch-and-rollout (the plugins component's examples). Pi's
// examples are scripts that print what they observe; each Go test runs the same scenario with the same edits and asserts what
// the script prints, as structured values. The plugin directories are the examples' in the facet language of
// plugins_seam_test.go (`text <contract> <text>` is `host.serve(labelOf("notes"), () => ({ text: constant(`${label} on the
// server`) }))`), under the contract names the examples use (`example.*`). 36 (failures: its failures print mapped
// JavaScript stacks), 37 (imports: esbuild's refusals) and 39 (browser build) are in the rows file.

// edit replaces the text of file (under dir) with text, as an editor saves: through a temporary file and a rename (a write
// in place is two events of one name, a save through a rename is a few of two), and returns once the watcher's events for it
// are in: upstream's examples wait for the mark they print (`until`), which the first event sets, and then print marks the
// later events of the same save may still change.
func edit(t *testing.T, plugins Plugins, dir, file, text string) {
	t.Helper()
	path := filepath.Join(dir, file)
	temporary := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".saving")
	if err := os.WriteFile(temporary, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatal(err)
	}
	quiet(t, plugins)
}

// quiet waits until the watcher made no mark for a while: the file events of what the test did are in.
func quiet(t *testing.T, plugins Plugins) {
	t.Helper()
	c := plugins.(*pluginsComponent)
	last, since := -1, time.Now()
	eventually(t, 10*time.Second, "the watcher's events in", func() bool {
		var generation int
		c.section(func() { generation = c.watcher.generation })
		if generation != last {
			last, since = generation, time.Now()
			return false
		}
		return time.Since(since) > 150*time.Millisecond
	})
}

// marksIs fails the test unless the marks are want.
func marksIs(t *testing.T, plugins Plugins, want PluginsState) {
	t.Helper()
	got := plugins.Marks()
	if got.Dirty == nil {
		got.Dirty = []string{}
	}
	if got.Install == nil {
		got.Install = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("marks = %+v, want %+v", got, want)
	}
}

// reportLine is what the examples print of a report: `{ ...report.plugins, failed: [...] }`.
type reportLine struct {
	Added, Changed, Removed, Failed []string
}

func lineOf(report host.ReloadReport) reportLine {
	norm := func(names []string) []string {
		if names == nil {
			return []string{}
		}
		return names
	}
	return reportLine{norm(report.Plugins.Added), norm(report.Plugins.Changed), norm(report.Plugins.Removed), norm(failedNames(report.Failed))}
}

func TestExample35PluginsLoadEditMarkBuildRollOut(t *testing.T) {
	root := tempRoot(t)
	writeFiles(t, filepath.Join(root, "contracts"), map[string]string{
		"package.json": `{"name":"@example/contracts","private":true,"type":"module"}`,
		"contract.ts":  "contracts",
	})
	writePlugin(t, root, "notes", "@example/notes", map[string]string{
		"label.ts":  "label v1",
		"server.ts": "import ./label.ts\nimport @example/contracts/contract.ts\nindex-server",
		"worker.ts": "import ./label.ts\nimport @example/contracts/contract.ts\nnotes-worker",
	}, nil)
	writePlugin(t, root, "clock", "@example/clock", map[string]string{
		"server.ts": "import @example/contracts/contract.ts\nclock",
	}, nil)
	evaluations := &atomic.Int64{}
	plugins, collected := componentOn(t, root, &probe{}, func(options *PluginsOptions) {
		options.Kinds = kindsOfFixtures()[:2]
		options.Evaluator = dslEvaluator{prefix: "example", evaluations: evaluations}
	})
	server, worker := linkedHosts(t, "example")
	workerLoad := mustLoad(t)(plugins.Load(worker, PluginsLoad{Kind: "worker", Modules: HostPackages{}}, background))
	serverLoad := mustLoad(t)(plugins.Load(server, PluginsLoad{Kind: "server", Modules: HostPackages{}}, background))
	var discovered []string
	for _, plugin := range plugins.Discovery().Plugins {
		var kinds []string
		for _, kind := range []string{"server", "worker"} {
			if _, ok := plugin.Facets[kind]; ok {
				kinds = append(kinds, kind)
			}
		}
		discovered = append(discovered, plugin.Name+" ["+strings.Join(kinds, ",")+"]")
	}
	if got, want := strings.Join(discovered, ", "), "@example/clock [server], @example/contracts [], @example/notes [server,worker]"; got != want {
		t.Fatalf("1. discovered: %q, want %q", got, want)
	}
	if len(workerLoad.Failed) != 0 || len(serverLoad.Failed) != 0 {
		t.Fatalf("load failures: worker %d, server %d", len(workerLoad.Failed), len(serverLoad.Failed))
	}
	get := func(contract string, version int) *host.Reference {
		var ref *host.Reference
		var err error
		switch contract {
		case "example.notes":
			ref, err = server.Get(notesContractOf("example"), delta.NewJsonObject(0), background, host.GetOptions{})
		case "example.clock":
			ref, err = server.Get(clockContract(contract), delta.NewJsonObject(0), background, host.GetOptions{})
		default:
			ref, err = server.Get(itemContract(contract, version), delta.NewJsonObject(0), background, host.GetOptions{})
		}
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}
	notes, index, clock := get("example.notes", 1), get("example.index", 1), get("example.clock", 1)
	member := func(ref *host.Reference, name string) delta.JsonValue {
		t.Helper()
		value, err := ref.Member(name).Value()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if _, err := notes.Call("add", nil, background); err != nil {
		t.Fatal(err)
	}
	evaluatedAt := member(clock, "evaluatedAt")
	if got := []delta.JsonValue{member(notes, "label"), member(notes, "count"), member(index, "label")}; !reflect.DeepEqual(got, []delta.JsonValue{"v1", float64(1), "v1"}) {
		t.Fatalf("notes: %v, want [v1 1 v1]", got)
	}
	watch, err := notes.Member("label").Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	frames := startFrames(watch)

	// 3. An edit on disk: the watcher marks the plugin dirty; nothing is applied yet.
	edit(t, plugins, filepath.Join(root, "notes"), "label.ts", "label v2")
	waitMarks(t, plugins, `dirty ["@example/notes"]`, dirtyIs("@example/notes"))
	marksIs(t, plugins, PluginsState{Dirty: []string{"@example/notes"}, Install: []string{}})

	// 4. The app builds the dirty plugins and rolls the build out, the server first, then the worker; the clock plugin was
	// not built, so it keeps its facet and its object.
	serverStep, err := plugins.Step("server", server)
	if err != nil {
		t.Fatal(err)
	}
	workerStep, err := plugins.Step("worker", worker)
	if err != nil {
		t.Fatal(err)
	}
	built := build(t, plugins, plugins.Marks().Dirty...)
	var builtLines []string
	for _, facet := range built.Facets {
		builtLines = append(builtLines, facet.Plugin+"/"+facet.Facet+": "+string(facet.Kind))
	}
	if want := []string{"@example/notes/server: script", "@example/notes/worker: script"}; !slices.Equal(builtLines, want) {
		t.Fatalf("3. built: %q, want %q", builtLines, want)
	}
	rolled := rollout(t, plugins, []RolloutStep{serverStep, workerStep}, built)
	type stepLine struct {
		name    string
		line    reportLine
		objects []host.ReloadedObject
	}
	var got []stepLine
	for _, each := range rolled.Steps {
		for _, entry := range each.Hosts {
			if entry.Kind == RolloutReported {
				got = append(got, stepLine{each.Name, lineOf(entry.Report), entry.Report.Objects})
			}
		}
	}
	want := []stepLine{
		{"server", reportLine{[]string{}, []string{"@example/notes"}, []string{}, []string{}}, []host.ReloadedObject{{Object: "example.index", Kind: host.ReloadSwapped}}},
		{"worker", reportLine{[]string{}, []string{"@example/notes"}, []string{}, []string{}}, []host.ReloadedObject{{Object: "example.notes", Kind: host.ReloadSwapped}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rolled = %+v, want %+v", got, want)
	}
	eventually(t, 5*time.Second, `notes label "v2"`, func() bool { return member(notes, "label") == "v2" })
	if got := []delta.JsonValue{member(notes, "label"), member(notes, "count"), member(index, "label")}; !reflect.DeepEqual(got, []delta.JsonValue{"v2", float64(1), "v2"}) {
		t.Fatalf("4. notes: %v, want [v2 1 v2]", got)
	}
	if member(clock, "evaluatedAt") != evaluatedAt || evaluations.Load() != 1 {
		t.Fatalf("the clock plugin was touched: %v, %d evaluations", member(clock, "evaluatedAt"), evaluations.Load())
	}
	eventually(t, 5*time.Second, `one frame "v2"`, func() bool { return reflect.DeepEqual(frames.all(), []delta.JsonValue{"v2"}) })
	marksIs(t, plugins, PluginsState{Dirty: []string{}, Install: []string{}})
	if got := collected.all(); len(got) != 0 {
		t.Fatalf("plugins reported %v", got)
	}
}

func TestExample38WatchAndRollout(t *testing.T) {
	directory := realOf(t, t.TempDir())
	root, app := filepath.Join(directory, "plugins"), filepath.Join(directory, "app")
	shared := "import @example/shared/contract.ts\n"
	writeFiles(t, root, map[string]string{
		"contracts/package.json": `{"name":"@example/shared","private":true,"type":"module"}`,
		"contracts/contract.ts":  "one type per plugin",
		"notes/package.json":     `{"name":"@example/notes","private":true,"type":"module"}`,
		"notes/label.ts":         "label notes v1",
		"notes/server.ts":        "import ./label.ts\n" + shared + "text example.label.notes {label} on the server",
		"notes/worker.ts":        "import ./label.ts\n" + shared + "text example.label.notes {label} on the worker",
		"old/package.json":       `{"name":"@example/old","private":true,"type":"module"}`,
		"old/server.ts":          shared + "text example.label.old old on the server",
		"tags/package.json":      `{"name":"@example/tags","private":true,"type":"module"}`,
		"tags/server.ts":         shared + "text example.label.tags tags on the server",
		"tags/worker.ts":         shared + "text example.label.tags tags on the worker",
	})
	writeFiles(t, directory, map[string]string{
		"app/config.ts":               "appName example",
		"incoming/extra/package.json": `{"name":"@example/extra","private":true,"type":"module"}`,
		"incoming/extra/server.ts":    "text example.label.extra extra on the server",
	})
	tools := tempRoot(t)
	plugins, collected := componentOn(t, root, &probe{}, func(options *PluginsOptions) {
		options.Kinds = kindsOfFixtures()[:2]
		options.Base = []string{app}
		options.Evaluator = dslEvaluator{prefix: "example"}
		options.Install = InstallOptions{NpmCommand: fakeNpm(t, tools, 0, false)}
	})
	server, worker := newTestHost(t), newTestHost(t)
	loadKind := func(h host.Host, kind string) {
		mustLoad(t)(plugins.Load(h, PluginsLoad{Kind: kind, Modules: HostPackages{}}, background))
	}
	loadKind(server, "server")
	loadKind(worker, "worker")
	serverStep, err := plugins.Step("server", server)
	if err != nil {
		t.Fatal(err)
	}
	workerStep, err := plugins.Step("worker", worker)
	if err != nil {
		t.Fatal(err)
	}
	steps := []RolloutStep{serverStep, workerStep}
	var names []string
	for _, plugin := range plugins.Discovery().Plugins {
		names = append(names, plugin.Name)
	}
	if !slices.Equal(names, []string{"@example/notes", "@example/old", "@example/shared", "@example/tags"}) {
		t.Fatalf("1. loaded: %v", names)
	}
	marksIs(t, plugins, PluginsState{Dirty: []string{}, Install: []string{}})

	// 2. Each kind of change and its mark.
	all := []string{"@example/notes", "@example/old", "@example/shared", "@example/tags"}
	edit(t, plugins, filepath.Join(root, "notes"), "label.ts", "label notes v2")
	waitMarks(t, plugins, "notes dirty", dirtyIs("@example/notes"))
	marksIs(t, plugins, PluginsState{Dirty: []string{"@example/notes"}, Install: []string{}})
	edit(t, plugins, filepath.Join(root, "contracts"), "contract.ts", "one type per plugin (edited)")
	waitMarks(t, plugins, "shared and tags dirty", dirtyIs(all...))
	marksIs(t, plugins, PluginsState{Dirty: all, Install: []string{}})
	edit(t, plugins, filepath.Join(root, "tags"), "package.json", `{"name":"@example/tags","private":true,"type":"module","description":"tags"}`)
	waitMarks(t, plugins, "tags to install", func(marks PluginsState) bool { return slices.Equal(marks.Install, []string{"@example/tags"}) })
	marksIs(t, plugins, PluginsState{Dirty: all, Install: []string{"@example/tags"}})
	if err := plugins.InstallDependencies("@example/tags", background); err != nil {
		t.Fatal(err)
	}
	marksIs(t, plugins, PluginsState{Dirty: all, Install: []string{}})
	if err := os.CopyFS(filepath.Join(root, "extra"), os.DirFS(filepath.Join(directory, "incoming", "extra"))); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "tags", "worker.ts")); err != nil {
		t.Fatal(err)
	}
	waitMarks(t, plugins, "set and old to install", func(marks PluginsState) bool {
		return marks.Set && slices.Equal(marks.Install, []string{"@example/old"})
	})
	quiet(t, plugins)
	// Deleting old's directory deleted its package.json too: an install mark, which goes with the next discovery.
	marksIs(t, plugins, PluginsState{Dirty: all, Install: []string{"@example/old"}, Set: true})
	edit(t, plugins, app, "config.ts", "appName example 2")
	waitMarks(t, plugins, "base", func(marks PluginsState) bool { return marks.Base })
	marksIs(t, plugins, PluginsState{Dirty: all, Install: []string{"@example/old"}, Set: true, Base: true})

	// 3. One build of the dirty plugins (discovery runs again first: `set`), rolled out server then worker. The server adds
	// extra and removes old; the worker leaves tags out (it has no worker facet any more). The marks clear, but `base`: that
	// one is the app's to act on (it restarts its hosts).
	built := build(t, plugins, plugins.Marks().Dirty...)
	var facets []string
	for _, facet := range built.Facets {
		facets = append(facets, facet.Plugin+"/"+facet.Facet)
	}
	if want := []string{"@example/extra/server", "@example/notes/server", "@example/notes/worker", "@example/tags/server"}; !slices.Equal(facets, want) {
		t.Fatalf("3. built: %q, want %q", facets, want)
	}
	expectNames(t, "removed", built.Removed, "@example/old")
	reports := func(result RolloutResult) []reportLine {
		var lines []reportLine
		for _, each := range result.Steps {
			for _, entry := range each.Hosts {
				if entry.Kind == RolloutReported {
					lines = append(lines, lineOf(entry.Report))
				}
			}
		}
		return lines
	}
	none := []string{}
	result := rollout(t, plugins, steps, built)
	want := []reportLine{
		{Added: []string{"@example/extra"}, Changed: []string{"@example/notes", "@example/tags"}, Removed: []string{"@example/old"}, Failed: none},
		{Added: none, Changed: []string{"@example/notes"}, Removed: []string{"@example/tags"}, Failed: none},
	}
	if got := reports(result); !reflect.DeepEqual(got, want) {
		t.Fatalf("3. rollout = %+v, want %+v", got, want)
	}
	marksIs(t, plugins, PluginsState{Dirty: []string{}, Install: []string{}, Base: true})

	// 4. A rollout stops at a step that rejects (a host the app cannot reach): the steps after it do not run, and the marks
	// stay (the plugin was not applied everywhere). The next rollout clears them.
	unreachable := RolloutStep{Name: "edge", Kinds: []string{"server"}, Reload: func(*Built, context.Context) (StepResult, error) {
		return StepResult{}, errors.New("the edge host is unreachable")
	}}
	edit(t, plugins, filepath.Join(root, "notes"), "label.ts", "label notes v3")
	waitMarks(t, plugins, "notes dirty", dirtyIs("@example/notes"))
	notes := build(t, plugins, plugins.Marks().Dirty...)
	result = rollout(t, plugins, []RolloutStep{steps[0], unreachable, steps[1]}, notes)
	if got, want := reports(result), []reportLine{{Added: none, Changed: []string{"@example/notes"}, Removed: none, Failed: none}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("4. rollout over server, edge, worker = %+v, want %+v", got, want)
	}
	if result.Stopped == nil || result.Stopped.Step != "edge" || result.Stopped.Error.Error() != "the edge host is unreachable" {
		t.Fatalf("stopped = %+v", result.Stopped)
	}
	marksIs(t, plugins, PluginsState{Dirty: []string{"@example/notes"}, Install: []string{}, Base: true})
	result = rollout(t, plugins, steps, notes)
	want = []reportLine{
		{Added: none, Changed: none, Removed: none, Failed: none},
		{Added: none, Changed: []string{"@example/notes"}, Removed: none, Failed: none},
	}
	if got := reports(result); !reflect.DeepEqual(got, want) {
		t.Fatalf("4. the next rollout = %+v, want %+v", got, want)
	}
	marksIs(t, plugins, PluginsState{Dirty: []string{}, Install: []string{}, Base: true})

	// 5. A failed plugin stays dirty, the others clear: notes gets a syntax error, tags a good edit.
	edit(t, plugins, filepath.Join(root, "notes"), "label.ts", `syntax error: label notes v4 +`)
	edit(t, plugins, filepath.Join(root, "tags"), "server.ts", shared+"text example.label.tags tags v2 on the server")
	waitMarks(t, plugins, "notes and tags dirty", dirtyIs("@example/notes", "@example/tags"))
	result = rollout(t, plugins, steps, build(t, plugins, plugins.Marks().Dirty...))
	want = []reportLine{
		{Added: none, Changed: []string{"@example/tags"}, Removed: none, Failed: []string{"@example/notes"}},
		{Added: none, Changed: none, Removed: none, Failed: []string{"@example/notes"}},
	}
	if got := reports(result); !reflect.DeepEqual(got, want) {
		t.Fatalf("5. rollout = %+v, want %+v", got, want)
	}
	marksIs(t, plugins, PluginsState{Dirty: []string{"@example/notes"}, Install: []string{}, Base: true})

	// 6. An edit after the build started keeps its mark: the rollout applies the build, but the newer edit is not in it.
	edit(t, plugins, filepath.Join(root, "notes"), "label.ts", "label notes v4")
	early := build(t, plugins, plugins.Marks().Dirty...)
	edit(t, plugins, filepath.Join(root, "notes"), "label.ts", "label notes v5")
	result = rollout(t, plugins, steps, early)
	want = []reportLine{
		{Added: none, Changed: []string{"@example/notes"}, Removed: none, Failed: none},
		{Added: none, Changed: []string{"@example/notes"}, Removed: none, Failed: none},
	}
	if got := reports(result); !reflect.DeepEqual(got, want) {
		t.Fatalf("6. built before the second edit = %+v, want %+v", got, want)
	}
	marksIs(t, plugins, PluginsState{Dirty: []string{"@example/notes"}, Install: []string{}, Base: true})
	result = rollout(t, plugins, steps, build(t, plugins, plugins.Marks().Dirty...))
	if got := reports(result); !reflect.DeepEqual(got, want) {
		t.Fatalf("6. building again = %+v, want %+v", got, want)
	}
	marksIs(t, plugins, PluginsState{Dirty: []string{}, Install: []string{}, Base: true})
	if got := collected.all(); len(got) != 0 {
		t.Fatalf("plugins reported %v", got)
	}
}

// refOf is a reference to the local contract on h.
func refOf(t *testing.T, h host.Host, contract *contracts.Contract) *host.Reference {
	t.Helper()
	ref, err := h.Get(contract, delta.NewJsonObject(0), background, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

// valueOf is the value of a reference's member.
func valueOf(t *testing.T, ref *host.Reference, member string) delta.JsonValue {
	t.Helper()
	value, err := ref.Member(member).Value()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// Ports 36-plugin-failures: every failure is per plugin. At startup a plugin whose setup throws is left out and the others run
// (a consumer of what it would serve waits). At a reload a plugin whose new code fails keeps its old code and is listed in
// the report's `failed`: a build that failed, a script that throws while it is evaluated, a factory that throws while the host
// stages it. The closure rule: a provider and its consumer edited together, the provider broken, the consumer kept on its old
// code too and still working; the next good edit applies both. Last, a reload whose context aborts after its commit: the
// commit stands, and the component keeps what was committed. The mapped JavaScript stacks the example prints (`mapStack`) have
// no Go form: the errors are asserted by message.
func TestExample36PluginFailures(t *testing.T) {
	root := realOf(t, t.TempDir())
	writeFiles(t, root, map[string]string{
		"weather/package.json":  `{"name":"@example/weather","private":true,"type":"module"}`,
		"weather/contract.ts":   "contract weather",
		"weather/server.ts":     "import ./contract.ts\nsetup-throws weather: no API key configured",
		"forecast/package.json": `{"name":"@example/forecast","private":true,"type":"module"}`,
		"forecast/server.ts":    "import @example/weather/contract.ts\nforecast",
		"greeter/package.json":  `{"name":"@example/greeter","private":true,"type":"module"}`,
		"greeter/label.ts":      "label hello",
		"greeter/server.ts":     "import ./label.ts\ntext example.greeting {label}",
		"boot/package.json":     `{"name":"@example/boot","private":true,"type":"module"}`,
		"boot/server.ts":        "local example.boot mode str v1",
		"counter/package.json":  `{"name":"@example/counter","private":true,"type":"module"}`,
		"counter/server.ts":     "local example.counter start int 1",
		"prices/package.json":   `{"name":"@example/prices","private":true,"type":"module"}`,
		"prices/contract.ts":    "version 1",
		"prices/server.ts":      "import ./contract.ts\nprice-server 250",
		"summary/package.json":  `{"name":"@example/summary","private":true,"type":"module"}`,
		"summary/server.ts":     "import @example/prices/contract.ts\nsummary",
		"cache/package.json":    `{"name":"@example/cache","private":true,"type":"module"}`,
		"cache/server.ts":       "cache v1",
	})
	plugins, _ := componentOn(t, root, &probe{}, func(options *PluginsOptions) {
		options.Kinds = kindsOfFixtures()[:1]
		options.Evaluator = dslEvaluator{prefix: "example"}
	})
	h := newTestHost(t)
	buildDirty := func(names ...string) *Built {
		t.Helper()
		waitMarks(t, plugins, "the plugins dirty", func(marks PluginsState) bool {
			return !slices.ContainsFunc(names, func(name string) bool { return !slices.Contains(marks.Dirty, name) })
		})
		return build(t, plugins, plugins.Marks().Dirty...)
	}
	greetingContract, bootContract := textContract("example.greeting"), oneMemberContract("example.boot", 1, "mode", "str")
	counterContract, summaryContract := oneMemberContract("example.counter", 1, "start", "int"), textContract("example.summary")

	// 1. Startup: the weather plugin's setup throws; it is left out and reported, the other plugins run. A reference to what it
	// would serve waits, and so does its consumer's object.
	loaded := mustLoad(t)(plugins.Load(h, PluginsLoad{Kind: "server", Modules: HostPackages{}}, background))
	if len(loaded.Failed) != 1 || loaded.Failed[0].Plugin != "@example/weather" || loaded.Failed[0].Error.Error() != `plugin "@example/weather": weather: no API key configured` {
		t.Fatalf("1. left out: %+v", loaded.Failed)
	}
	weather, err := h.Hold(oneMemberContract("example.weather", 1, "celsius", "int"), delta.NewJsonObject(0), background, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	forecast, err := h.Hold(textContract("example.forecast"), delta.NewJsonObject(0), background, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "forecast failed", func() bool { return forecast.State().Value().Kind == host.RefStateFailed })
	if weather.State().Value().Kind != host.RefStateFailed {
		t.Fatalf("weather: %+v, want failed", weather.State().Value())
	}
	greeting, boot, counter, summary := refOf(t, h, greetingContract), refOf(t, h, bootContract), refOf(t, h, counterContract), refOf(t, h, summaryContract)
	status := refOf(t, h, textContract("example.status"))
	values := func() []delta.JsonValue {
		return []delta.JsonValue{valueOf(t, greeting, "text"), valueOf(t, boot, "mode"), valueOf(t, counter, "start"), valueOf(t, summary, "text")}
	}
	if got, want := values(), []delta.JsonValue{"hello", "v1", float64(1), "price v1: 250 cents"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("running: %v, want %v", got, want)
	}

	// 2. Four bad edits at once, and the prices plugin moving its contract to version 2 while breaking its facet.
	edit(t, plugins, filepath.Join(root, "greeter"), "label.ts", "syntax error: label hello +")
	edit(t, plugins, filepath.Join(root, "boot"), "server.ts", "throws boot: config missing\nlocal example.boot mode str v1")
	edit(t, plugins, filepath.Join(root, "counter"), "server.ts", "local-throws example.counter start int counter: no seed")
	edit(t, plugins, filepath.Join(root, "prices"), "contract.ts", "version 2")
	edit(t, plugins, filepath.Join(root, "prices"), "server.ts", "import ./contract.ts\nsyntax error: price-server 250")
	bad := buildDirty("@example/greeter", "@example/boot", "@example/counter", "@example/summary")
	kinds := map[string]BuiltKind{}
	for _, facet := range bad.Facets {
		kinds[facet.Plugin] = facet.Kind
	}
	wantKinds := map[string]BuiltKind{
		"@example/boot": BuiltFacetScript, "@example/counter": BuiltFacetScript, "@example/greeter": BuiltFacetFailed,
		"@example/prices": BuiltFacetFailed, "@example/summary": BuiltFacetScript,
	}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("2. built: %v, want %v", kinds, wantKinds)
	}
	// Each failed plugin keeps its old code; the summary's new code would ask for `Price` v2, which only the prices plugin's new
	// (failed) code serves: the closure rule keeps the summary on its old code too.
	report := reload(t, plugins, h, bad)
	expectChanges(t, report.Plugins, nil, nil, nil)
	expectNames(t, "failed", sorted(failedNames(report.Failed)), "@example/boot", "@example/counter", "@example/greeter", "@example/prices", "@example/summary", "@example/weather")
	if got, want := failureError(t, report.Failed, "@example/summary").Error(), `plugin "@example/summary": needs example.price v2, which plugin "@example/prices" serves on its old code`; got != want {
		t.Fatalf("summary failure = %q, want %q", got, want)
	}
	if got, want := failureError(t, report.Failed, "@example/boot").Error(), "boot: config missing"; got != want {
		t.Fatalf("boot failure = %q, want %q", got, want)
	}
	if got := failureError(t, report.Failed, "@example/counter").Error(); !strings.Contains(got, "counter: no seed") {
		t.Fatalf("counter failure = %q", got)
	}
	if got, want := values(), []delta.JsonValue{"hello", "v1", float64(1), "price v1: 250 cents"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("running: %v, want %v", got, want)
	}
	expectNames(t, "still dirty", plugins.Marks().Dirty, "@example/boot", "@example/counter", "@example/greeter", "@example/prices", "@example/summary")

	// 3. The fixes: the next build and reload apply every plugin, the prices provider and its consumer together.
	edit(t, plugins, filepath.Join(root, "greeter"), "label.ts", "label hi")
	edit(t, plugins, filepath.Join(root, "boot"), "server.ts", "local example.boot mode str v2")
	edit(t, plugins, filepath.Join(root, "counter"), "server.ts", "local example.counter start int 2")
	edit(t, plugins, filepath.Join(root, "prices"), "server.ts", "import ./contract.ts\nprice-server 300")
	good := buildDirty("@example/greeter", "@example/boot", "@example/counter", "@example/prices")
	report = reload(t, plugins, h, good)
	// The weather plugin, left out at startup, is added again by every reload and fails again.
	expectNames(t, "failed", failedNames(report.Failed), "@example/weather")
	expectNames(t, "changed", sorted(report.Plugins.Changed), "@example/boot", "@example/counter", "@example/greeter", "@example/prices", "@example/summary")
	// The summary's new code waited for the new `Price` object (parked at the report): it runs once that one is live.
	eventually(t, 5*time.Second, "the summary on price v2", func() bool {
		value, err := summary.Member("text").Value()
		text, _ := value.(string)
		return err == nil && strings.HasPrefix(text, "price v2")
	})
	if got, want := values(), []delta.JsonValue{"hi", "v2", float64(2), "price v2: 300 cents"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("then: %v, want %v", got, want)
	}

	// 4. A reload whose context aborts after its commit, while it waits for its report: here the app shuts down the moment it
	// sees the status object leave `live` (its new code waits for a new `Cache`, which takes 200 ms to open). The reload may
	// return `aborted`, but its commit stands: `details.committed` says what it applied, and the component's loaded set is what was
	// committed (a later reload does not roll the cache plugin back).
	if got := valueOf(t, status, "text"); got != "cache v1" {
		t.Fatalf("4. status: %v", got)
	}
	edit(t, plugins, filepath.Join(root, "cache"), "server.ts", "cache v2")
	cache := buildDirty("@example/cache")
	aborting, cancel := chordctx.WithCancel(background)
	watchStatus, err := status.State().Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	watchStatus.Start(func(_ context.Context, call chord.NativeWatchCall[*host.RefState]) error {
		if call.Kind == chord.WatchCallValue && call.Value.Kind != host.RefStateLive {
			cancel(errors.New("the app shuts down"))
		}
		return nil
	})
	// Pi's window for the abort is microtask-level: the host's report awaits the swap's first dependency check, which settles
	// at the first parked check (attempt.ts), not when the new Cache is open, so the reload resolves a few microtasks after
	// its commit, and the status's `loading` frame reaches the app's watch inside that window. A Go watch delivers on its own
	// goroutine, so whether the abort lands before or after the report is a race the test cannot order. What the example
	// shows holds either way and is asserted: the commit stands (the status runs on cache v2 below), a reload that does
	// return `aborted` names what it committed, and a later reload does not roll the cache plugin back. The abort branch
	// itself is tested deterministically with a fake host (TestRuntimeAdoptsWhatAReloadCommitted...).
	_, err = plugins.Reload(h, cache, aborting)
	if err != nil {
		chordErr, aborted := data.IsChordErrorCode(err, data.CodeAborted)
		if !aborted {
			t.Fatalf("the reload returned %v, want a report or aborted", err)
		}
		committed, ok := committedOf(chordErr)
		if !ok || !slices.Equal(committed.Changed, []string{"@example/cache"}) || len(committed.Added) != 0 || len(committed.Removed) != 0 || !slices.Equal(committed.Failed, []string{"@example/weather"}) {
			t.Fatalf("rejected: %v, committed %+v", chordErr, committed)
		}
	}
	eventually(t, 5*time.Second, "the status on cache v2", func() bool {
		value, err := status.Member("text").Value()
		return err == nil && value == "cache v2"
	})
	edit(t, plugins, filepath.Join(root, "greeter"), "label.ts", "label hey")
	reload(t, plugins, h, buildDirty("@example/greeter"))
	if got := []delta.JsonValue{valueOf(t, status, "text"), valueOf(t, greeting, "text")}; !reflect.DeepEqual(got, []delta.JsonValue{"cache v2", "hey"}) {
		t.Fatalf("after another reload: %v, want [cache v2 hey]", got)
	}
	_, _ = watchStatus.Stop()
}
