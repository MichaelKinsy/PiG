package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/build"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports packages/chord/test/next/examples/35-plugins, 36-plugin-failures, 37-plugin-imports, 38-watch-and-rollout,
// 39-browser-build, 49-plugin-api, 50-plugin-imports and 51-single-file-plugin. Pi's examples are scripts that print what they observe; each Go test runs the same scenario, with the
// same edits, on the REAL esbuild build and REAL Node hosts (the example's own plugin directories, byte for byte, under
// testdata/examples/<example>), and asserts what the script prints as structured values. What the script reads with
// `host.get` of a LOCAL type (Index, Clock, Greeting...) lives on the Node host, so it is read through the probe
// (plugins_probe_test.go): the contract is declared in the probe's op, nothing is imported from the plugin files.

const exChord = "@earendil-works/chord"

// exOps are the probe ops of the examples. `read` reads one member of a one-member local type on the Node host (every local
// type the examples read has that shape and no params); `state` reports whether `get` of such a type succeeds; `armStatus`,
// `armed` and `leftLive` are 36's watch of the status reference's state: `leftLive` answers the kind the state left `live` to,
// and `armStatus` stays open until the state is `live` again, then answers every kind the watch delivered (36 prints each:
// `the status reference: <kind>`).
const exOps = `
read: async ({ name, member, kind }, { ctx }) => {
	const Type = next.local(name, { version: 1, params: {}, members: { [member]: next[kind]() } });
	const ref = await ctx.get(Type, {});
	return ref[member].value;
},
state: async ({ name, member, kind }, { ctx }) => {
	const Type = next.local(name, { version: 1, params: {}, members: { [member]: next[kind]() } });
	try {
		await ctx.get(Type, {}, { readyTimeout: 1 });
		return { live: true };
	} catch (error) {
		return { live: false, code: error.code, reason: error.reason, message: error.message };
	}
},
armStatus: async (_args, { ctx }) => {
	const Status = next.local("example.status", { version: 1, params: {}, members: { text: next.str() } });
	const ref = await ctx.get(Status, {});
	globalThis.exArmed = false;
	const kinds = [];
	let left;
	globalThis.exLeft = new Promise((resolve) => (left = resolve));
	return await new Promise((resolve, reject) => {
		ref.state.watch(ctx).then((watch) => {
			watch.start(async (call) => {
				if (call.kind !== "value") return;
				kinds.push(call.value.kind);
				if (call.value.kind !== "live") left(call.value.kind);
				else if (kinds.length > 1) resolve(kinds);
			});
			globalThis.exArmed = true;
		}, reject);
	});
},
leftLive: async () => await globalThis.exLeft,
armed: async () => globalThis.exArmed === true,
`

var (
	exWorkerKind = mustKind(contracts.NewHostKind("example.worker"))
	// exNotes is 35's and Pi's `Notes` of plugins/contracts/contract.ts.
	exNotes = mustContract(contracts.Remote("example.notes", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return exWorkerKind.Address(nil), nil },
		Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
			State:   []contracts.Property{contracts.Prop("label", contracts.Str()), contracts.Prop("count", contracts.Int())},
			Methods: []contracts.MethodEntry{contracts.Meth("add", contracts.NewMethod(contracts.Schema{}, contracts.Int()))},
		})},
	}))
)

// exCopy copies the example's directories (dirs of testdata/examples/<example>) into a new temporary directory, which it
// returns: the examples edit their files, so they work on a copy.
func exCopy(t *testing.T, example string, dirs ...string) string {
	t.Helper()
	base := realOf(t, t.TempDir())
	for _, dir := range dirs {
		copied := filepath.Join(base, dir)
		if err := os.CopyFS(copied, os.DirFS(filepath.Join("testdata", "examples", example, dir))); err != nil {
			t.Fatal(err)
		}
		// Windows reports the last-write time of each directory the copy filled as a change of it, at a moment no test can bound (see
		// withoutDirectoryStamps): the watcher may mark the set or a plugin for the copy itself.
		if err := filepath.WalkDir(copied, func(path string, entry fs.DirEntry, err error) error {
			if err == nil && entry.IsDir() {
				exTouch(t, path)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

// exErrors collects what the component reports (the examples print `plugins: <kind>` for each: none is expected).
type exErrors struct {
	mu   sync.Mutex
	kind []PluginsErrorKind
}

func (e *exErrors) add(err PluginsError) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.kind = append(e.kind, err.Kind)
}

func (e *exErrors) none(t *testing.T) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.kind) != 0 {
		t.Fatalf("plugins reported %v", e.kind)
	}
}

// exComponent is the example's `createPlugins`: Node kinds (or a browser one) keeping Chord and the extra packages external.
func exComponent(t *testing.T, roots []string, cache string, kinds []KindDefinition, edit func(*PluginsOptions)) (*Plugins, *exErrors) {
	t.Helper()
	collected := &exErrors{}
	options := PluginsOptions{Build: build.BuildFacet, Roots: exRoots(roots), Kinds: kinds, OnError: collected.add, Node: NodeOptions{CacheDir: cache}}
	if edit != nil {
		edit(&options)
	}
	plugins := CreatePlugins(options, background)
	t.Cleanup(plugins.Close)
	return plugins, collected
}

// exRoots are the roots as Pi's examples give them as plain paths: not required.
func exRoots(paths []string) []model.PluginRoot {
	roots := make([]model.PluginRoot, len(paths))
	for i, path := range paths {
		roots[i] = model.PluginRoot{Path: path}
	}
	return roots
}

func exNodeKind(name string, packages ...string) KindDefinition {
	return KindDefinition{Name: name, Platform: model.PlatformNode, HostPackages: append([]string{exChord}, packages...)}
}

// exNode is a Node host loaded with a kind's plugins, attached to its Go host, with the probe when the host has one.
type exNode struct {
	node   *NodeHost
	report host.LoadReport
	h      host.Host
	probe  *probe
}

// exStart loads kind's plugins on a new Node host (address, principal, peer as the example's host has them) and attaches it to
// a new Go host: a route of the probe's kind when withProbe adds the probe core facet (the host then has the probe's address),
// else an accepted child link.
func exStart(t *testing.T, plugins *Plugins, cache, kind string, modules HostPackages, address *contracts.Address, principal, peer data.Principal, withProbe bool) *exNode {
	t.Helper()
	started := exLoad(t, plugins, cache, kind, modules, address, principal, peer, withProbe)
	if withProbe {
		started.h = serverFor(t, nodeRoute{kind: workerKind, node: started.node, principal: principal})
		started.probe = probeOn(t, started.h)
	} else {
		started.h = acceptorOf(t, started.node, principal)
	}
	return started
}

// exLoad is exStart's load: the Node host, its link not attached yet (NodeHost.Link attaches at its first use).
func exLoad(t *testing.T, plugins *Plugins, cache, kind string, modules HostPackages, address *contracts.Address, principal, peer data.Principal, withProbe bool) *exNode {
	t.Helper()
	load := PluginsLoad{Kind: kind, Modules: modules, Address: address, Principal: principal, Peer: peer}
	if withProbe {
		tree, err := extractNode(cache)
		if err != nil {
			t.Fatal(err)
		}
		src := "file://" + filepath.ToSlash(filepath.Join(tree, "pi", "chord", "src")) + "/"
		load.Core = []CoreFacet{probeCore(t, cache, exOps+pcHoldOps(src))}
	}
	node, report, err := plugins.Load(load, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	return &exNode{node: node, report: report}
}

// read is `(await host.get(Type, {}, context))[member].value` for a one-member local type, through the probe.
func (n *exNode) read(name, member, kind string) any {
	n.probe.t.Helper()
	return n.probe.must("read", map[string]any{"name": name, "member": member, "kind": kind})
}

// exEdit replaces the first `from` with `to` in file (as the examples' `edit`: read, replace, write in place).
func exEdit(t *testing.T, dir, file, from, to string) {
	t.Helper()
	path := filepath.Join(dir, file)
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), from) {
		t.Fatalf("%s has no %q", file, from)
	}
	exTouch(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(strings.Replace(string(text), from, to, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// exWrite writes a whole file (`writeFile`).
func exWrite(t *testing.T, path, text string) {
	t.Helper()
	exTouch(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// exUntil is the examples' `until(ready)`: the watcher's events arrive on their own time.
func exUntil(t *testing.T, plugins *Plugins, what string, ready func(PluginsState) bool) {
	t.Helper()
	eventually(t, 15*time.Second, what, func() bool { return ready(plugins.Marks()) })
	// What the edit also raised has arrived too: the next build starts from the marks the edit left.
	settle(t, plugins)
}

// exDirty is `dirty(...names)`.
func exDirty(t *testing.T, plugins *Plugins, names ...string) {
	t.Helper()
	exUntil(t, plugins, "dirty "+strings.Join(names, ", "), func(marks PluginsState) bool {
		return !slices.ContainsFunc(names, func(name string) bool { return !slices.Contains(marks.Dirty, name) })
	})
}

// exMarks fails the test unless the marks are want (JSON.stringify of `plugins.state.value`).
func exMarks(t *testing.T, plugins *Plugins, want PluginsState) {
	t.Helper()
	got := plugins.Marks()
	if got.Dirty == nil {
		got.Dirty = []string{}
	}
	if got.Install == nil {
		got.Install = []string{}
	}
	if want.Dirty == nil {
		want.Dirty = []string{}
	}
	if want.Install == nil {
		want.Install = []string{}
	}
	if runtime.GOOS == "windows" {
		got = withoutDirectoryStamps(t, got, want)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("marks = %+v, want %+v", got, want)
	}
}

// exTouched is the directories each test changed an entry of, by name: only they can report their own last-write time.
var exTouched = struct {
	sync.Mutex
	dirs map[*testing.T][]string
}{dirs: map[*testing.T][]string{}}

// exTouch records that the test changes an entry of dir.
func exTouch(t *testing.T, dir string) {
	t.Helper()
	exTouched.Lock()
	defer exTouched.Unlock()
	if _, ok := exTouched.dirs[t]; !ok {
		t.Cleanup(func() {
			exTouched.Lock()
			defer exTouched.Unlock()
			delete(exTouched.dirs, t)
		})
	}
	exTouched.dirs[t] = append(exTouched.dirs[t], filepath.Base(dir))
}

func exTouchedBy(t *testing.T) []string {
	exTouched.Lock()
	defer exTouched.Unlock()
	return slices.Clone(exTouched.dirs[t])
}

// exStamp is whether the mark of a plugin is what Windows adds when its directory's entries changed: a directory the test
// changed an entry of (the plugin's directory is named as the last segment of its name).
func exStamp(touched []string, plugin string) bool {
	_, dir, _ := strings.Cut(plugin, "/")
	if dir == "" {
		dir = plugin
	}
	return slices.Contains(touched, dir)
}

// withoutDirectoryStamps is got without what Windows adds to the marks of a plugin directory whose entries changed: the OS
// reports the directory's own last-write time as a change of it (Node's `fs.watch` reports it as `change`, which Pi's watcher
// marks too), at a moment no test can bound: the set is marked and the plugin dirty, though only the entry's own change was
// made. Only the marks of a directory the test changed an entry of, and that want does not have, are dropped (the set's only
// when the test changed some entry); an unrelated plugin that is dirty, or a mark want has and got lacks, still fails.
func withoutDirectoryStamps(t *testing.T, got, want PluginsState) PluginsState {
	touched := exTouchedBy(t)
	if !want.Set && len(touched) > 0 {
		got.Set = false
	}
	got.Dirty = slices.DeleteFunc(slices.Clone(got.Dirty), func(name string) bool {
		return !slices.Contains(want.Dirty, name) && exStamp(touched, name)
	})
	return got
}

// exDirtyOf is the plugins the marks say to build, as the case expects them (want): on Windows the plugins whose directory
// saw an entry come or go are dirty too (see withoutDirectoryStamps), and are not built here.
func exDirtyOf(t *testing.T, plugins *Plugins, want ...string) []string {
	dirty := plugins.Marks().Dirty
	if runtime.GOOS == "windows" {
		touched := exTouchedBy(t)
		dirty = slices.DeleteFunc(slices.Clone(dirty), func(name string) bool { return !slices.Contains(want, name) && exStamp(touched, name) })
	}
	return dirty
}

func exBuild(t *testing.T, plugins *Plugins, names []string) *Built {
	t.Helper()
	built, err := plugins.Build(names, background)
	if err != nil {
		t.Fatal(err)
	}
	return built
}

func exNames(failed []host.PluginFailure) []string {
	names := make([]string, len(failed))
	for i, failure := range failed {
		names[i] = failure.Plugin
	}
	return names
}

func exList(names []string) []string {
	if names == nil {
		return []string{}
	}
	return names
}

// exLine is what 36 and 38 print of a report: `{ ...report.plugins, failed: [...] }`.
type exLine struct{ Added, Changed, Removed, Failed []string }

func exLineOf(report host.ReloadReport) exLine {
	return exLine{exList(report.Plugins.Added), exList(report.Plugins.Changed), exList(report.Plugins.Removed), exList(exNames(report.Failed))}
}

// exReports are the reports of a rollout, one per reporting host, in order.
func exReports(result RolloutResult) []exLine {
	var lines []exLine
	for _, step := range result.Steps {
		for _, entry := range step.Hosts {
			if entry.Kind == RolloutReported {
				lines = append(lines, exLineOf(entry.Report))
			}
		}
	}
	return lines
}

// exFrames collects the value frames of a watch, as 35's `watch.start`.
type exFrames struct {
	mu     sync.Mutex
	values []delta.JsonValue
}

func (f *exFrames) all() []delta.JsonValue {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.values)
}

func exMember(t *testing.T, ref *host.Reference, member string) delta.JsonValue {
	t.Helper()
	value, err := ref.Member(member).Value()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func exPlainGet(t *testing.T, h host.Host, contract *contracts.Contract) *host.Reference {
	t.Helper()
	ref, err := h.Get(background, contract, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ref.Release)
	return ref
}

// exFailure is 36's `print` of one failed plugin: the first line of its mapped stack, and the second where it names the
// plugin's code (a build error, or a mapped stack frame of server.ts).
func exFailure(t *testing.T, err error) string {
	t.Helper()
	var nodeErr *NodeError
	if !errors.As(err, &nodeErr) {
		t.Fatalf("%v is no *NodeError", err)
	}
	message, next, _ := strings.Cut(nodeErr.Stack, "\n")
	next, _, _ = strings.Cut(next, "\n")
	detail := ""
	if !strings.HasPrefix(strings.TrimLeft(next, " \t"), "at ") || strings.Contains(next, "/server.ts:") {
		detail = " " + strings.TrimSpace(next)
	}
	return message + detail
}

// exFailures are the failures of a report by plugin, each printed as 36's `print`.
func exFailures(t *testing.T, failed []host.PluginFailure) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, failure := range failed {
		out[failure.Plugin] = exFailure(t, failure.Error)
	}
	return out
}

// Ports examples/35-plugins/main.ts. The server and the worker are two Node hosts (kind "server", kind "worker"), each the
// child of a Go host: Pi's memory link between its two hosts is one link per Node host here. The server's local types (Index,
// Clock) are read through the probe of the server's Node host.
func TestExample35PluginsLoadEditMarkBuildRollOut(t *testing.T) {
	cache := t.TempDir()
	root := filepath.Join(exCopy(t, "35-plugins", "plugins"), "plugins")
	plugins, collected := exComponent(t, []string{root}, cache, []KindDefinition{exNodeKind("server"), exNodeKind("worker")}, nil)

	// 2. A worker and a server, each loading the plugins' facets of its kind (no core facets here).
	workerAddress, probeAddress := exWorkerKind.Address(nil), workerKind.Address(nil)
	worker := exLoad(t, plugins, cache, "worker", nil, &workerAddress, workerPrincipal, serverPrincipal, false)
	server := exLoad(t, plugins, cache, "server", nil, &probeAddress, serverPrincipal, workerPrincipal, true)
	// The app's server routes both kinds to the Nodes' links, as Pi's server routes its worker's: one Go host observes both.
	app := serverFor(t,
		nodeRoute{kind: exWorkerKind, node: worker.node, principal: workerPrincipal},
		nodeRoute{kind: workerKind, node: server.node, principal: serverPrincipal})
	worker.h, server.h = app, app
	server.probe = probeOn(t, app)
	var discovered []string
	for _, plugin := range plugins.Discovery().Plugins {
		var facets []string
		for _, kind := range []string{"server", "worker"} {
			if _, ok := plugin.Facets[kind]; ok {
				facets = append(facets, kind)
			}
		}
		discovered = append(discovered, plugin.Name+" ["+strings.Join(facets, ",")+"]")
	}
	if got, want := strings.Join(discovered, ", "), "@example/clock [server], @example/contracts [], @example/notes [server,worker]"; got != want {
		t.Fatalf("1. discovered: %q, want %q", got, want)
	}
	if len(worker.report.Failed) != 0 || len(server.report.Failed) != 0 {
		t.Fatalf("load failures: worker %v | server %v", exNames(worker.report.Failed), exNames(server.report.Failed))
	}
	notes := exPlainGet(t, worker.h, exNotes)
	if _, err := notes.Call("add", nil, background); err != nil {
		t.Fatal(err)
	}
	evaluatedAt := server.read("example.clock", "evaluatedAt", "str")
	if got := []any{exMember(t, notes, "label"), exMember(t, notes, "count"), server.read("example.index", "label", "str")}; !reflect.DeepEqual(got, []any{"v1", float64(1), "v1"}) {
		t.Fatalf("notes: %v, want [v1 1 v1]", got)
	}
	watch, err := notes.Member("label").Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	frames := &exFrames{}
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue {
			frames.mu.Lock()
			frames.values = append(frames.values, call.Value)
			frames.mu.Unlock()
		}
		return nil
	})
	t.Cleanup(func() { _, _ = watch.Stop() })

	// 3. An edit on disk: the watcher marks the plugin dirty; nothing is applied yet.
	exWrite(t, filepath.Join(root, "notes", "label.ts"), "export const label = \"v2\";\n")
	exDirty(t, plugins, "@example/notes")
	exMarks(t, plugins, PluginsState{Dirty: []string{"@example/notes"}})

	// 4. The app builds the dirty plugins and rolls the build out: the server first, then the worker.
	serverStep, err := plugins.Step("server", server.node)
	if err != nil {
		t.Fatal(err)
	}
	workerStep, err := plugins.Step("worker", worker.node)
	if err != nil {
		t.Fatal(err)
	}
	built := exBuild(t, plugins, exDirtyOf(t, plugins, "@example/notes"))
	var builtLines []string
	for _, facet := range built.Facets {
		builtLines = append(builtLines, facet.Plugin+"/"+facet.Facet+": "+string(facet.Kind))
	}
	if want := []string{"@example/notes/server: script", "@example/notes/worker: script"}; !slices.Equal(builtLines, want) {
		t.Fatalf("3. built: %q, want %q", builtLines, want)
	}
	rolled, err := plugins.Rollout([]RolloutStep{serverStep, workerStep}, built, background)
	if err != nil {
		t.Fatal(err)
	}
	type stepLine struct {
		Name    string
		Line    exLine
		Objects []host.ReloadedObject
	}
	var got []stepLine
	for _, each := range rolled.Steps {
		for _, entry := range each.Hosts {
			if entry.Kind == RolloutReported {
				got = append(got, stepLine{each.Name, exLineOf(entry.Report), entry.Report.Objects})
			}
		}
	}
	changed := exLine{[]string{}, []string{"@example/notes"}, []string{}, []string{}}
	want := []stepLine{
		{"server", changed, []host.ReloadedObject{{Object: "example.index", Kind: host.ReloadRestarted}}},
		{"worker", changed, []host.ReloadedObject{{Object: "example.notes", Kind: host.ReloadRestarted}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rolled = %+v, want %+v", got, want)
	}
	eventually(t, 15*time.Second, `notes label "v2"`, func() bool { return exMember(t, notes, "label") == "v2" })
	eventually(t, 15*time.Second, `index label "v2"`, func() bool { return server.read("example.index", "label", "str") == "v2" })
	if got := []any{exMember(t, notes, "label"), exMember(t, notes, "count"), server.read("example.index", "label", "str")}; !reflect.DeepEqual(got, []any{"v2", float64(1), "v2"}) {
		t.Fatalf("4. notes: %v, want [v2 1 v2]", got)
	}
	if got := server.read("example.clock", "evaluatedAt", "str"); got != evaluatedAt {
		t.Fatalf("clock untouched: %v, was %v", got, evaluatedAt)
	}
	eventually(t, 15*time.Second, `one frame "v2"`, func() bool { return reflect.DeepEqual(frames.all(), []delta.JsonValue{"v2"}) })
	exMarks(t, plugins, PluginsState{})
	collected.none(t)
}

// Ports examples/36-plugin-failures/main.ts: one Node host (kind "server"); the app's local types are read through the probe.
func TestExample36PluginFailures(t *testing.T) {
	cache := t.TempDir()
	base := exCopy(t, "36-plugin-failures", "plugins")
	root := filepath.Join(base, "plugins")
	plugins, collected := exComponent(t, []string{root}, cache, []KindDefinition{exNodeKind("server")}, nil)
	buildDirty := func(names ...string) *Built {
		t.Helper()
		exDirty(t, plugins, names...)
		return exBuild(t, plugins, plugins.Marks().Dirty)
	}
	probeAddress := workerKind.Address(nil)
	server := exStart(t, plugins, cache, "server", nil, &probeAddress, serverPrincipal, workerPrincipal, true)
	p := server.probe

	// 1. Startup: the weather plugin's setup throws; it is left out and reported, the other plugins run. A reference to what it
	// would serve waits (`failed`, cause `waiting`: not served here), and so does its consumer's object.
	if len(server.report.Failed) != 1 || server.report.Failed[0].Plugin != "@example/weather" ||
		server.report.Failed[0].Error.Error() != `plugin "@example/weather": weather: no API key configured` {
		t.Fatalf("1. left out: %+v", server.report.Failed)
	}
	notLive := func(name, member, kind string) map[string]any {
		var state map[string]any
		eventually(t, 15*time.Second, name+" failed", func() bool {
			state, _ = p.must("state", map[string]any{"name": name, "member": member, "kind": kind}).(map[string]any)
			return state["live"] == false
		})
		return state
	}
	forecast := notLive("example.forecast", "text", "str")
	weather := notLive("example.weather", "celsius", "int")
	for name, state := range map[string]map[string]any{"example.weather": weather, "example.forecast": forecast} {
		want := "example.weather is not served here"
		if name == "example.forecast" {
			want = "waiting for example.weather: example.weather is not served here"
		}
		if state["code"] != "unavailable" || state["message"] != want {
			t.Fatalf("1. %s: %v, want failed waiting (unavailable: %s)", name, state, want)
		}
	}
	values := func() []any {
		return []any{
			server.read("example.greeting", "text", "str"), server.read("example.boot", "mode", "str"),
			server.read("example.counter", "start", "int"), server.read("example.summary", "text", "str"),
		}
	}
	if got, want := values(), []any{"hello", "v1", float64(1), "price v1: 250 cents"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("running: %v, want %v", got, want)
	}

	// 2. Four bad edits at once, and the prices plugin moving its contract to version 2 while breaking its facet.
	exEdit(t, root, "greeter/label.ts", `"hello"`, `"hello" +`)
	exEdit(t, root, "boot/server.ts", "const Boot", "throw new Error(\"boot: config missing\");\n\nconst Boot")
	exEdit(t, root, "counter/server.ts", "() => ({ start: constant(1) })", "() => {\n\tthrow new Error(\"counter: no seed\");\n}")
	exEdit(t, root, "prices/contract.ts", "version: 1", "version: 2")
	exEdit(t, root, "prices/server.ts", "constant(250)", "constant(250")
	bad := buildDirty("@example/greeter", "@example/boot", "@example/counter", "@example/summary")
	var builtLines []string
	for _, facet := range bad.Facets {
		builtLines = append(builtLines, facet.Plugin+": "+string(facet.Kind))
	}
	if want := []string{"@example/boot: script", "@example/counter: script", "@example/greeter: failed", "@example/prices: failed", "@example/summary: script"}; !slices.Equal(builtLines, want) {
		t.Fatalf("2. built: %q, want %q", builtLines, want)
	}
	// Each failed plugin keeps its old code; the summary's new code would ask for `Price` v2, which only the prices plugin's
	// new (failed) code serves: the closure rule keeps the summary on its old code too.
	report, err := plugins.Reload(server.node, bad, background)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := exLineOf(report), (exLine{[]string{}, []string{}, []string{}, []string{"@example/boot", "@example/greeter", "@example/prices", "@example/weather", "@example/counter", "@example/summary"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("2. reload = %+v, want %+v", got, want)
	}
	if len(report.Objects) != 0 {
		t.Fatalf("2. objects = %+v", report.Objects)
	}
	wantFailures := map[string]string{
		"@example/greeter": `Error: plugin "@example/greeter": its server facet failed to build: label.ts:3:34: Unexpected ";"`,
		"@example/prices":  `Error: plugin "@example/prices": its server facet failed to build: server.ts:6:85: Expected ")" but found "}"`,
		"@example/summary": `Error: plugin "@example/summary": needs example.price v2, which plugin "@example/prices" serves on its old code`,
		"@example/boot":    `Error: boot: config missing at boot/server.ts:5:7`,
		"@example/counter": `Error: plugin "@example/counter", example.counter: counter: no seed`,
		"@example/weather": `Error: plugin "@example/weather": weather: no API key configured`,
	}
	if got := exFailures(t, report.Failed); !reflect.DeepEqual(got, wantFailures) {
		t.Fatalf("2. failures = %q, want %q", got, wantFailures)
	}
	if got, want := values(), []any{"hello", "v1", float64(1), "price v1: 250 cents"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("running: %v, want %v", got, want)
	}
	if got, want := plugins.Marks().Dirty, []string{"@example/boot", "@example/counter", "@example/greeter", "@example/prices", "@example/summary"}; !slices.Equal(got, want) {
		t.Fatalf("still dirty: %v, want %v", got, want)
	}

	// 3. The fixes: the next build and reload apply every plugin, the prices provider and its consumer together.
	exEdit(t, root, "greeter/label.ts", `"hello" +`, `"hi"`)
	exEdit(t, root, "boot/server.ts", "throw new Error(\"boot: config missing\");\n\n", "")
	exEdit(t, root, "boot/server.ts", `constant("v1")`, `constant("v2")`)
	exEdit(t, root, "counter/server.ts", "() => {\n\tthrow new Error(\"counter: no seed\");\n}", "() => ({ start: constant(2) })")
	exEdit(t, root, "prices/server.ts", "constant(250", "constant(300)")
	good := buildDirty("@example/greeter", "@example/boot", "@example/counter", "@example/prices")
	report, err = plugins.Reload(server.node, good, background)
	if err != nil {
		t.Fatal(err)
	}
	// The weather plugin, left out at startup, is added again by every reload and fails again.
	if got, want := exLineOf(report), (exLine{[]string{}, []string{"@example/boot", "@example/counter", "@example/greeter", "@example/prices", "@example/summary"}, []string{}, []string{"@example/weather"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("3. reload = %+v, want %+v", got, want)
	}
	objects := map[string]host.ReloadedObjectKind{}
	for _, object := range report.Objects {
		objects[object.Object] = object.Kind
	}
	wantObjects := map[string]host.ReloadedObjectKind{
		"example.boot": host.ReloadRestarted, "example.counter": host.ReloadRestarted, "example.greeting": host.ReloadRestarted,
		"example.price": host.ReloadRestarted, "example.summary": host.ReloadRestarted,
	}
	if !reflect.DeepEqual(objects, wantObjects) {
		t.Fatalf("3. objects: %v, want %v", objects, wantObjects)
	}
	// The summary's new code waited for the new `Price` object (parked at the report): it runs once that one is live.
	eventually(t, 15*time.Second, "the summary on price v2", func() bool {
		text, _ := server.read("example.summary", "text", "str").(string)
		return strings.HasPrefix(text, "price v2")
	})
	if got, want := values(), []any{"hi", "v2", float64(2), "price v2: 300 cents"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("then: %v, want %v", got, want)
	}

	// 4. A reload whose context aborts after its commit, while it waits for its report: the app shuts down the moment it sees
	// the status object leave `live`. Pi's window for the abort is a few microtasks between the commit and the report, inside
	// one process. Here the probe holds the report's wait (pcHoldOps: the same stand-in as plugins.test.ts's abort after the
	// commit) so the window is open until the app has seen the status leave `live` and cancelled; the reload then rejects
	// `aborted`, naming what its commit applied (`details.committed`), and the commit stands.
	if got := server.read("example.status", "text", "str"); got != "cache v1" {
		t.Fatalf("4. status: %v", got)
	}
	exEdit(t, root, "cache/server.ts", `const generation = "v1"`, `const generation = "v2"`)
	cacheBuilt := buildDirty("@example/cache")
	aborting, cancel := context.WithCancelCause(background)
	p.must("holdChecks", nil)
	kindsOf := make(chan any, 1)
	go func() {
		answer, err := p.call("armStatus", nil)
		if err != nil {
			kindsOf <- err
			return
		}
		kindsOf <- answer
	}()
	eventually(t, 15*time.Second, "the status watch armed", func() bool {
		select {
		case answer := <-kindsOf:
			t.Fatalf("the status watch ended before it was armed: %v", answer)
		default:
		}
		return p.must("armed", nil) == true
	})
	// The app aborts the moment it sees the status leave `live`.
	leftTo := make(chan any, 1)
	go func() {
		answer, err := p.call("leftLive", nil)
		if err != nil {
			leftTo <- err
		} else {
			leftTo <- answer
		}
		cancel(errors.New("the app shuts down"))
	}()
	_, err = plugins.Reload(server.node, cacheBuilt, aborting)
	p.must("release", nil)
	var nodeErr *NodeError
	if !errors.As(err, &nodeErr) || nodeErr.Chord == nil || nodeErr.Chord.Code != data.CodeAborted {
		t.Fatalf("4. the reload returned %v, want aborted", err)
	}
	// Pi prints `rejected: aborted reload: aborted after its commit {"committed":{...}}`.
	details, jsonErr := chordjson.Marshal(nodeErr.Chord.Details)
	if jsonErr != nil {
		t.Fatal(jsonErr)
	}
	if got, want := nodeErr.Chord.Message+" "+string(details), `reload: aborted after its commit {"committed":{"added":[],"changed":["@example/cache"],"removed":[],"failed":["@example/weather"]}}`; got != want {
		t.Fatalf("4. rejected: aborted %s, want %s", got, want)
	}
	// Pi prints `the status reference: loading` (the kind it left `live` to, where the app aborts), then, once the new cache is
	// open, `the status reference: live`.
	if answer := <-leftTo; answer != "loading" {
		t.Fatalf("4. the status reference left live to %v, want loading", answer)
	}
	eventually(t, 15*time.Second, "the status on cache v2", func() bool { return server.read("example.status", "text", "str") == "cache v2" })
	if kinds := <-kindsOf; !reflect.DeepEqual(kinds, []any{"loading", "live"}) {
		t.Fatalf("4. the status reference: %v, want [loading live]", kinds)
	}
	exEdit(t, root, "greeter/label.ts", `"hi"`, `"hey"`)
	last, err := plugins.Reload(server.node, buildDirty("@example/greeter"), background)
	if err != nil {
		t.Fatal(err)
	}
	// Pi prints the report: `plugins: {"added":[],"changed":["@example/greeter"],"removed":[]}`, `objects: example.greeting
	// restarted` and `failed @example/weather: Error: plugin "@example/weather": weather: no API key configured`.
	if got, want := exLineOf(last), (exLine{[]string{}, []string{"@example/greeter"}, []string{}, []string{"@example/weather"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("4. plugins: %+v, want %+v", got, want)
	}
	if want := []host.ReloadedObject{{Object: "example.greeting", Kind: host.ReloadRestarted}}; !reflect.DeepEqual(last.Objects, want) {
		t.Fatalf("4. objects: %+v, want %+v", last.Objects, want)
	}
	if got, want := exFailures(t, last.Failed), map[string]string{"@example/weather": `Error: plugin "@example/weather": weather: no API key configured`}; !reflect.DeepEqual(got, want) {
		t.Fatalf("4. failures: %q, want %q", got, want)
	}
	if got := []any{server.read("example.status", "text", "str"), server.read("example.greeting", "text", "str")}; !reflect.DeepEqual(got, []any{"cache v2", "hey"}) {
		t.Fatalf("after another reload: %v, want [cache v2 hey]", got)
	}
	collected.none(t)
}

// Ports examples/37-plugin-imports/main.ts: the build inlines a plugin's own files, copies of other plugins' files and its npm
// dependencies, leaves host packages, `chord.external` packages and Node builtins external; every refusal is a build error.
func TestExample37PluginImports(t *testing.T) {
	cache := t.TempDir()
	base := exCopy(t, "37-plugin-imports", "plugins", "app", "deps")
	root := filepath.Join(base, "plugins")
	fakeNpm, err := filepath.Abs(filepath.Join("testdata", "examples", "37-plugin-imports", "fake-npm.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	// 1. The component. Its host packages: Chord, the app's ES module and a CommonJS module of the app.
	modules := HostPackages{"@example/app": filepath.Join(base, "app", "index.ts"), "@example/legacy": filepath.Join(base, "app", "legacy.cjs")}
	plugins, collected := exComponent(t, []string{root}, cache, []KindDefinition{exNodeKind("server", "@example/app", "@example/legacy")}, func(options *PluginsOptions) {
		options.Install = InstallOptions{NpmCommand: []string{"node", fakeNpm}}
	})

	// 2. Startup: every plugin whose build fails is left out with its errors (one per line: file, line, column, why). The
	// report plugin's dependency is not installed yet.
	probeAddress := workerKind.Address(nil)
	server := exStart(t, plugins, cache, "server", modules, &probeAddress, serverPrincipal, workerPrincipal, true)
	left := map[string][]string{}
	var order []string
	for _, failure := range server.report.Failed {
		left[failure.Plugin] = strings.Split(failure.Error.Error(), "\n")
		order = append(order, failure.Plugin)
	}
	if want := []string{"@example/paths", "@example/refused", "@example/report", "@example/ruler"}; !slices.Equal(order, want) {
		t.Fatalf("1. left out: %v, want %v", order, want)
	}
	const hint = ` (a file of the plugin is read through pluginFile(path) from "@earendil-works/chord/plugins")`
	const identity = "import.meta.url, __dirname and __filename are not available in a plugin script" + hint
	wantLeft := map[string][]string{
		"@example/paths": {
			`plugin "@example/paths": its server facet failed to build:`,
			"server.ts:8:19: " + identity,
			"server.ts:9:14: " + identity,
		},
		"@example/refused": {
			`plugin "@example/refused": its server facet failed to build:`,
			`server.ts:6:17: "../../app/index.ts": outside the plugin's root (base code is reached through a host package, other repositories' plugins through dependencies)`,
			"server.ts:9:14: " + identity,
			`server.ts:10:14: import("./other.ts"): a plugin script has no dynamic import` + hint,
		},
		"@example/report": {
			`plugin "@example/report": its server facet failed to build:`,
			`server.ts:10:23: Could not resolve "example-greeting"`,
		},
		"@example/ruler": {
			`plugin "@example/ruler": its server facet failed to build:`,
			`../shapes/units.ts:4:21: "@earendil-works/pi-schema": a file copied from plugin "@example/shapes" may import only host packages and files of its own plugin`,
		},
	}
	if !reflect.DeepEqual(left, wantLeft) {
		t.Fatalf("1. left out = %q, want %q", left, wantLeft)
	}

	// 3. The report plugin's dependencies, installed into its own node_modules; then it builds, and a reload adds it.
	if err := plugins.InstallDependencies("@example/report", background); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "report", "node_modules"))
	if err != nil {
		t.Fatal(err)
	}
	var installed []string
	for _, entry := range entries {
		installed = append(installed, entry.Name())
	}
	if want := []string{"example-greeting", "example-native"}; !slices.Equal(installed, want) {
		t.Fatalf("2. installed: %v, want %v", installed, want)
	}
	built := exBuild(t, plugins, []string{"@example/report"})
	if len(built.Facets) != 1 || built.Facets[0].Kind != model.BuiltFacetScript {
		t.Fatalf("built %+v", built.Facets)
	}
	facet := built.Facets[0]
	var read []string
	for _, input := range facet.Inputs {
		read = append(read, filepath.ToSlash(strings.TrimPrefix(input.File, root+string(filepath.Separator))))
	}
	if want := []string{"report/server.ts", "shapes/contract.ts"}; !slices.Equal(read, want) || len(facet.Copied) != 1 {
		t.Fatalf("built %s/%s: read %v; copied %d file, want %v and 1", facet.Plugin, facet.Facet, read, len(facet.Copied), want)
	}
	report, err := plugins.Reload(server.node, built, background)
	if err != nil {
		t.Fatal(err)
	}
	// The reload passes the complete set: the refused plugins fail again and stay out.
	if got, want := exLineOf(report), (exLine{[]string{"@example/report"}, []string{}, []string{}, []string{"@example/paths", "@example/refused", "@example/ruler"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("reload = %+v, want %+v", got, want)
	}
	// The fixture reports the last three segments of `__filename` split at "/": a Windows path has none, so the report holds
	// the whole path there, as Pi's does on Windows.
	native := "node_modules/example-native/index.js"
	if runtime.GOOS == "windows" {
		native = filepath.Join(realOf(t, root), "report", "node_modules", "example-native", "index.js")
	}
	want := "example app v3 | EUR 12.50 | ...vat | 20% | hello, plugins | " + native
	if got := server.read("example.report", "text", "str"); got != want {
		t.Fatalf("3. the report: %q, want %q", got, want)
	}
	collected.none(t)
}

// Ports examples/38-watch-and-rollout/main.ts: two Node hosts (kind "server", kind "worker"), each with a Go host as its child.
func TestExample38WatchAndRollout(t *testing.T) {
	cache := t.TempDir()
	base := exCopy(t, "38-watch-and-rollout", "plugins", "app")
	root, app := filepath.Join(base, "plugins"), filepath.Join(base, "app")
	incoming := filepath.Join("testdata", "examples", "38-watch-and-rollout", "incoming", "extra")
	plugins, collected := exComponent(t, []string{root}, cache, []KindDefinition{exNodeKind("server"), exNodeKind("worker")}, func(options *PluginsOptions) {
		// The app's own code: a change marks `base`. A stand-in for npm (no network; these plugins have no dependencies).
		options.Base = []string{app}
		options.Install = InstallOptions{NpmCommand: []string{"node", "-e", ""}}
	})
	server := exStart(t, plugins, cache, "server", nil, nil, serverPrincipal, workerPrincipal, false)
	worker := exStart(t, plugins, cache, "worker", nil, nil, workerPrincipal, serverPrincipal, false)
	serverStep, err := plugins.Step("server", server.node)
	if err != nil {
		t.Fatal(err)
	}
	workerStep, err := plugins.Step("worker", worker.node)
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
	exMarks(t, plugins, PluginsState{})
	rollout := func(steps []RolloutStep, built *Built) RolloutResult {
		t.Helper()
		result, err := plugins.Rollout(steps, built, background)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	none := []string{}

	// 2. Each kind of change and its mark.
	all := []string{"@example/notes", "@example/old", "@example/shared", "@example/tags"}
	exEdit(t, root, "notes/label.ts", "notes v1", "notes v2")
	exDirty(t, plugins, "@example/notes")
	exMarks(t, plugins, PluginsState{Dirty: []string{"@example/notes"}})
	exEdit(t, root, "contracts/contract.ts", "one type per plugin", "one type per plugin (edited)")
	exDirty(t, plugins, "@example/shared", "@example/tags")
	exMarks(t, plugins, PluginsState{Dirty: all})
	exEdit(t, root, "tags/package.json", `"private": true`, "\"private\": true,\n\t\"description\": \"tags\"")
	exUntil(t, plugins, "tags to install", func(marks PluginsState) bool { return slices.Contains(marks.Install, "@example/tags") })
	exMarks(t, plugins, PluginsState{Dirty: all, Install: []string{"@example/tags"}})
	if err := plugins.InstallDependencies("@example/tags", background); err != nil {
		t.Fatal(err)
	}
	exMarks(t, plugins, PluginsState{Dirty: all})
	exTouch(t, root)
	exTouch(t, filepath.Join(root, "tags"))
	if err := os.CopyFS(filepath.Join(root, "extra"), os.DirFS(incoming)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "tags", "worker.ts")); err != nil {
		t.Fatal(err)
	}
	// Deleting old's directory deleted its package.json too: an install mark, which goes with the next discovery.
	exUntil(t, plugins, "set and old to install", func(marks PluginsState) bool {
		return marks.Set && slices.Equal(marks.Install, []string{"@example/old"})
	})
	exMarks(t, plugins, PluginsState{Dirty: all, Install: []string{"@example/old"}, Set: true})
	exEdit(t, app, "config.ts", `"example"`, `"example 2"`)
	exUntil(t, plugins, "base", func(marks PluginsState) bool { return marks.Base })
	exMarks(t, plugins, PluginsState{Dirty: all, Install: []string{"@example/old"}, Set: true, Base: true})

	// 3. One build of the dirty plugins (discovery runs again first: `set`), rolled out server then worker. The marks clear,
	// but `base`: that one is the app's to act on.
	built := exBuild(t, plugins, plugins.Marks().Dirty)
	var facets []string
	for _, facet := range built.Facets {
		facets = append(facets, facet.Plugin+"/"+facet.Facet)
	}
	if want := []string{"@example/extra/server", "@example/notes/server", "@example/notes/worker", "@example/tags/server"}; !slices.Equal(facets, want) {
		t.Fatalf("3. built: %q, want %q", facets, want)
	}
	if !slices.Equal(built.Removed, []string{"@example/old"}) {
		t.Fatalf("removed: %v", built.Removed)
	}
	result := rollout(steps, built)
	want := []exLine{
		{Added: []string{"@example/extra"}, Changed: []string{"@example/notes"}, Removed: []string{"@example/old"}, Failed: none},
		{Added: none, Changed: []string{"@example/notes"}, Removed: []string{"@example/tags"}, Failed: none},
	}
	if got := exReports(result); !reflect.DeepEqual(got, want) {
		t.Fatalf("3. rollout = %+v, want %+v", got, want)
	}
	exMarks(t, plugins, PluginsState{Base: true})

	// 4. A rollout stops at a step that rejects (a host the app cannot reach): the steps after it do not run, and the marks
	// stay (the plugin was not applied everywhere). The next rollout clears them.
	unreachable := RolloutStep{Name: "edge", Kinds: []string{"server"}, Reload: func(*Built, map[string][]PluginScript, context.Context) (StepResult, error) {
		return StepResult{}, errors.New("the edge host is unreachable")
	}}
	exEdit(t, root, "notes/label.ts", "notes v2", "notes v3")
	exDirty(t, plugins, "@example/notes")
	notes := exBuild(t, plugins, plugins.Marks().Dirty)
	result = rollout([]RolloutStep{steps[0], unreachable, steps[1]}, notes)
	if got, want := exReports(result), []exLine{{Added: none, Changed: []string{"@example/notes"}, Removed: none, Failed: none}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("4. rollout over server, edge, worker = %+v, want %+v", got, want)
	}
	if result.Stopped == nil || result.Stopped.Step != "edge" || result.Stopped.Error.Error() != "the edge host is unreachable" {
		t.Fatalf("stopped = %+v", result.Stopped)
	}
	exMarks(t, plugins, PluginsState{Dirty: []string{"@example/notes"}, Base: true})
	result = rollout(steps, notes)
	want = []exLine{
		{Added: none, Changed: none, Removed: none, Failed: none},
		{Added: none, Changed: []string{"@example/notes"}, Removed: none, Failed: none},
	}
	if got := exReports(result); !reflect.DeepEqual(got, want) {
		t.Fatalf("4. the next rollout = %+v, want %+v", got, want)
	}
	exMarks(t, plugins, PluginsState{Base: true})

	// 5. A failed plugin stays dirty, the others clear: notes gets a syntax error, tags a good edit.
	exEdit(t, root, "notes/label.ts", `"notes v3"`, `"notes v4" +`)
	exEdit(t, root, "tags/server.ts", "tags on the server", "tags v2 on the server")
	exDirty(t, plugins, "@example/notes", "@example/tags")
	result = rollout(steps, exBuild(t, plugins, plugins.Marks().Dirty))
	want = []exLine{
		{Added: none, Changed: []string{"@example/tags"}, Removed: none, Failed: []string{"@example/notes"}},
		{Added: none, Changed: none, Removed: none, Failed: []string{"@example/notes"}},
	}
	if got := exReports(result); !reflect.DeepEqual(got, want) {
		t.Fatalf("5. rollout = %+v, want %+v", got, want)
	}
	exMarks(t, plugins, PluginsState{Dirty: []string{"@example/notes"}, Base: true})

	// 6. An edit after the build started keeps its mark: the rollout applies the build, but the newer edit is not in it.
	exEdit(t, root, "notes/label.ts", `"notes v4" +`, `"notes v4"`)
	exDirty(t, plugins, "@example/notes")
	early := exBuild(t, plugins, plugins.Marks().Dirty)
	generation := func() int {
		var generation int
		plugins.section(func() { generation = plugins.watcher.generation })
		return generation
	}
	before := generation()
	exEdit(t, root, "notes/label.ts", `"notes v4"`, `"notes v5"`)
	// The edit's event arrives on its own time (the mark is set already, so no change shows it): its generation does.
	eventually(t, 15*time.Second, "the second edit's event", func() bool { return generation() != before })
	result = rollout(steps, early)
	want = []exLine{
		{Added: none, Changed: []string{"@example/notes"}, Removed: none, Failed: none},
		{Added: none, Changed: []string{"@example/notes"}, Removed: none, Failed: none},
	}
	if got := exReports(result); !reflect.DeepEqual(got, want) {
		t.Fatalf("6. built before the second edit = %+v, want %+v", got, want)
	}
	exMarks(t, plugins, PluginsState{Dirty: []string{"@example/notes"}, Base: true})
	result = rollout(steps, exBuild(t, plugins, plugins.Marks().Dirty))
	if got := exReports(result); !reflect.DeepEqual(got, want) {
		t.Fatalf("6. building again = %+v, want %+v", got, want)
	}
	exMarks(t, plugins, PluginsState{Base: true})
	collected.none(t)
}

// exBrowser is 39's component: `web` is a browser kind; its assets are served under `/plugin-assets/`.
func exBrowser(t *testing.T) (*Plugins, string) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "examples", "39-browser-build", "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	plugins, collected := exComponent(t, []string{root}, t.TempDir(), []KindDefinition{{Name: "web", Platform: model.PlatformBrowser, HostPackages: []string{exChord}}}, func(options *PluginsOptions) {
		options.PublicPath = new("/plugin-assets/")
	})
	t.Cleanup(func() { collected.none(t) })
	return plugins, root
}

// Ports examples/39-browser-build/main.ts steps 1 to 3: the build of a browser kind is plain data (script, stylesheet,
// assets), a build that a browser refuses is a failed result, and a copy of the build (a structured clone: here JSON) is the
// same build. The example reads `plugins/` in place.
func TestExample39BrowserBuild(t *testing.T) {
	plugins, root := exBrowser(t)
	built := exBuild(t, plugins, nil)
	var gallery, bad *model.BuiltFacet
	for i := range built.Facets {
		switch built.Facets[i].Plugin {
		case "@example/gallery":
			gallery = &built.Facets[i]
		case "@example/bad-web":
			bad = &built.Facets[i]
		}
	}
	if gallery == nil || bad == nil || gallery.Kind != model.BuiltFacetScript || bad.Kind != model.BuiltFacetFailed {
		t.Fatalf("unexpected build results: %+v", built.Facets)
	}
	// Pi's example prints `script of 1392 bytes, digest 0e9e13b5e4c3`: the Go build is the same esbuild (Pi's pin), so the script
	// is the same bytes.
	if gallery.Facet != "web" || len(gallery.Source) != 1392 || gallery.Digest != sha256Hex(gallery.Source) || gallery.Digest[:12] != "0e9e13b5e4c3" {
		t.Fatalf("1. @example/gallery/web: %d bytes, digest %q", len(gallery.Source), gallery.Digest)
	}
	if gallery.CSS == nil {
		t.Fatal("no stylesheet")
	}
	if first, _, _ := strings.Cut(gallery.CSS.Text, "\n"); gallery.CSS.Name != "web-PDJFG4AT.css" || first != ".gallery{display:grid;gap:8px}.gallery img{width:64px}" {
		t.Fatalf("stylesheet: %s %q", gallery.CSS.Name, first)
	}
	svg, err := os.ReadFile(filepath.Join(root, "gallery", "assets", "logo.svg"))
	if err != nil {
		t.Fatal(err)
	}
	if len(gallery.Assets) != 1 || gallery.Assets[0].Name != "logo-3IRJDQYU.svg" || !bytes.Equal(gallery.Assets[0].Bytes, svg) {
		t.Fatalf("assets: %+v", gallery.Assets)
	}
	if url := regexp.MustCompile(`/plugin-assets/[^"]+`).FindString(gallery.Source); url != "/plugin-assets/"+gallery.Assets[0].Name {
		t.Fatalf("the asset's URL in the script: %q", url)
	}
	var read []string
	for _, input := range gallery.Inputs {
		read = append(read, filepath.ToSlash(strings.TrimPrefix(input.File, root)))
	}
	if want := []string{"/gallery/assets/logo.svg", "/gallery/gallery.css", "/gallery/web.ts"}; !slices.Equal(read, want) {
		t.Fatalf("read: %v, want %v", read, want)
	}
	if got, want := strings.Join(bad.Errors, "; "), `web.ts:3:22: "node:path": a Node builtin in a browser build`; got != want {
		t.Fatalf("2. @example/bad-web/web: %q, want %q", got, want)
	}

	// 3. Plain data: a copy of the build is the same build.
	text, err := json.Marshal(built)
	if err != nil {
		t.Fatal(err)
	}
	var shipped Built
	if err := json.Unmarshal(text, &shipped); err != nil {
		t.Fatal(err)
	}
	var copied *model.BuiltFacet
	for i := range shipped.Facets {
		if shipped.Facets[i].Plugin == "@example/gallery" {
			copied = &shipped.Facets[i]
		}
	}
	keys := make([]string, 0, len(shipped.Plugins))
	for key := range shipped.Plugins {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if copied == nil || copied.Kind != model.BuiltFacetScript || copied.Digest != gallery.Digest || !reflect.DeepEqual(*copied, *gallery) {
		t.Fatalf("3. cloned: %+v", copied)
	}
	if !slices.Equal(keys, []string{"@example/bad-web", "@example/gallery"}) {
		t.Fatalf("its plugins: %v", keys)
	}
}

// Ports examples/39-browser-build/main.ts step 4: where the build runs (a page; a Node host stands in, as Node does in Pi), the
// copy of the script is evaluated with the host packages and loaded, and its logo is read (through the probe).
func TestExample39BrowserBuildEvaluatedOnAPage(t *testing.T) {
	plugins, _ := exBrowser(t)
	built := exBuild(t, plugins, nil)
	text, err := json.Marshal(built)
	if err != nil {
		t.Fatal(err)
	}
	var shipped Built
	if err := json.Unmarshal(text, &shipped); err != nil {
		t.Fatal(err)
	}
	var script PluginScript
	for _, facet := range shipped.Facets {
		if facet.Plugin == "@example/gallery" {
			script = PluginScript{Plugin: shipped.Plugins[facet.Plugin], Built: facet}
		}
	}
	cache := t.TempDir()
	address := workerKind.Address(nil)
	node, report, err := NewPluginRuntime(NodeOptions{CacheDir: cache}).Load(RuntimeLoad{
		Kind: "web", Core: []CoreFacet{probeCore(t, cache, exOps)}, Scripts: []PluginScript{script},
		Address: &address, Principal: workerPrincipal, Peer: serverPrincipal,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	if len(report.Failed) != 0 {
		t.Fatalf("failed: %+v", report.Failed)
	}
	parent := serverFor(t, workerRoute(node))
	page := &exNode{node: node, h: parent, probe: probeOn(t, parent)}
	want := "/plugin-assets/" + script.Built.Assets[0].Name
	if got := page.read("example.gallery", "logo", "str"); got != want {
		t.Fatalf("4. the gallery's logo on the page: %v, want %q", got, want)
	}
}

// The slice-12 examples (49 to 51) build against example 49's `app/`: the app's versioned API, whose modules are the host
// packages `@acme/app/v3`, `@acme/app/v3/server`, `@acme/app/v3/web` and `@acme/app/v2`. The app imports Chord by a relative
// path from where Pi keeps it (`app/v3.ts`: `../../../../src/index.ts`), so the app is staged where that path reaches
// the Chord the Node host runs: under the extracted tree's `pi/chord/test/examples/49-plugin-api/app`, next to its `src`
// (the modules then share the host's own instances of Chord, as Pi's example imports the app's modules in the host's
// process).

// exAcmeApi is the app's API of 49 to 51: its name and, per example, the versions it lists.
const exAcmeApi = "@acme/app"

// exAcmeKinds are the kinds of the app of 49 to 51: `server` (Node) and `web` (browser), each with the API's shared module
// and its own, plus the versions' modules the example lists. Chord is no host package of either.
func exAcmeKinds(versions ...string) []KindDefinition {
	var server, web []string
	for _, version := range versions {
		server = append(server, exAcmeApi+"/"+version)
		web = append(web, exAcmeApi+"/"+version)
		if version == "v3" {
			server = append(server, exAcmeApi+"/v3/server")
			web = append(web, exAcmeApi+"/v3/web")
		}
	}
	return []KindDefinition{
		{Name: "server", Platform: model.PlatformNode, HostPackages: server},
		{Name: "web", Platform: model.PlatformBrowser, HostPackages: web},
	}
}

// exAcmeApp stages example 49's `app/` for the Node host started with cache and returns its directory.
func exAcmeApp(t *testing.T, cache string) string {
	t.Helper()
	tree, err := extractNode(cache)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tree, "pi", "chord", "test", "examples", "49-plugin-api", "app")
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("testdata", "examples", "49-plugin-api", "app"))); err != nil {
		t.Fatal(err)
	}
	return dir
}

// exAcmeModules are the app's modules as the server (`modules`) or the web page gives them, by specifier: the shared v3
// module, the kind's own, and v2's when the example lists v2.
func exAcmeModules(app, kind string, v2 bool) HostPackages {
	modules := HostPackages{
		exAcmeApi + "/v3":         filepath.Join(app, "v3.ts"),
		exAcmeApi + "/v3/" + kind: filepath.Join(app, "v3", kind+".ts"),
	}
	if v2 {
		modules[exAcmeApi+"/v2"] = filepath.Join(app, "v2.ts")
	}
	return modules
}

// exAcmeComponent is the example's `createPlugins` with the app's API.
func exAcmeComponent(t *testing.T, roots []model.PluginRoot, cache string, versions []string, edit func(*PluginsOptions)) (*Plugins, *exErrors) {
	t.Helper()
	collected := &exErrors{}
	options := PluginsOptions{
		Build: build.BuildFacet, Roots: roots, Api: &model.AppApi{Name: exAcmeApi, Versions: versions}, Kinds: exAcmeKinds(versions...),
		OnError: collected.add, Node: NodeOptions{CacheDir: cache},
	}
	if edit != nil {
		edit(&options)
	}
	plugins := CreatePlugins(options, background)
	t.Cleanup(plugins.Close)
	return plugins, collected
}

// exFileOps are the probe ops of 51: `hold` is the example's `await host.get(Hello)` and its `hello.release()`: the reference is
// held for as long as the call lasts (a reference lives as long as the context it was got with, here the call's), so the call
// stays open until `release`; `held` answers its text and whether it is held yet, `release` ends the call.
const exFileOps = `
hold: async (_args, { ctx }) => {
	const Hello = next.local("example.hello", { version: 1, params: {}, members: { text: next.str() } });
	const hello = await ctx.get(Hello, {});
	globalThis.exHello = hello;
	await new Promise((resolve) => (globalThis.exHelloEnd = resolve));
	hello.release();
},
held: async () => (globalThis.exHello === undefined ? null : globalThis.exHello.text.value),
release: async () => globalThis.exHelloEnd(),
`

// exAcmeStart loads kind's plugins on a new Node host of the app (the example's `plugins.load(host, { kind, core, modules })`)
// with the probe, and returns the host with the load's error: nil, or the *PluginError a required root's failure makes, with the
// host loaded as the load settled it.
func exAcmeStart(t *testing.T, plugins *Plugins, cache, kind string, modules HostPackages, core []CoreFacet, imports, setup, ops string) (*exNode, error) {
	t.Helper()
	probed := probeCoreSetup(t, cache, imports, setup, exOps+exFileOps+ops)
	address := workerKind.Address(nil)
	node, report, err := plugins.Load(PluginsLoad{
		Kind: kind, Core: append(slices.Clone(core), probed), Modules: modules,
		Address: &address, Principal: serverPrincipal, Peer: workerPrincipal,
	}, background)
	if node == nil {
		t.Fatalf("load: %v", err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	if _, ok := errors.AsType[*PluginError](err); err != nil && !ok {
		t.Fatalf("load: %v", err)
	}
	started := &exNode{node: node, report: report}
	started.h = serverFor(t, nodeRoute{kind: workerKind, node: node, principal: serverPrincipal})
	started.probe = probeOn(t, started.h)
	return started, err
}

// exRefused is what an example prints of a failed plugin: its build errors, one per line after the first, joined by `; `.
func exRefused(failure host.PluginFailure) string {
	_, rest, _ := strings.Cut(failure.Error.Error(), "\n")
	return strings.Join(strings.Split(rest, "\n"), "; ")
}

// Ports examples/49-plugin-api/main.ts: the app's versioned API. A v3 plugin and a v2 plugin (a thin layer over v3) load; a v1
// plugin, a facet mixing v2 and v3, a direct import of Chord and a server facet importing the web's module are refused at
// build, each with its error; the panel plugin's web facet builds, and a page evaluates and loads it. What the script reads
// of the hosts it created itself (the server's `Greetings` registry; a page host with the web's modules) is read through the
// probe: a registry read declared in the probe's own object, and an op that makes the page in the Node process.
func TestExample49PluginAPI(t *testing.T) {
	cache := t.TempDir()
	app := exAcmeApp(t, cache)
	root, err := filepath.Abs(filepath.Join("testdata", "examples", "49-plugin-api", "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	root = realOf(t, root)
	// 1. The component with the app's API.
	plugins, collected := exAcmeComponent(t, []model.PluginRoot{{Path: root}}, cache, []string{"v3", "v2"}, nil)

	// 2. The server's modules and its own start-data facet, base code: it serves the `Greetings` registry, composing the lines
	// in plugin-name order.
	v3 := fileURL(filepath.Join(app, "v3.ts"))
	tree, err := extractNode(cache)
	if err != nil {
		t.Fatal(err)
	}
	src := "file://" + filepath.ToSlash(filepath.Join(tree, "pi", "chord", "src")) + "/"
	coreModule := filepath.Join(t.TempDir(), "core.mjs")
	if err := os.WriteFile(coreModule, []byte(`import { defineFacet } from "`+src+`host/index.ts";
import * as v3 from "`+v3+`";
export default defineFacet((host) => host.serve(v3.Greetings, { compose: (table) => table.map(({ value }) => value) }));
`), 0o644); err != nil {
		t.Fatal(err)
	}
	web := exAcmeModules(app, "web", true)
	ops := `
greetings: async (_args, { held }) => held.greetings.value,
panels: async (a, { ctx }) => {
	const { evaluateFacet } = evaluate;
	const v2 = await import(a.v2), v3 = await import(a.v3), v3web = await import(a.v3web);
	const panels = next.defineFacet((h) => h.serve(v3web.Panels, { compose: (table) => table.map(({ value }) => value.title) }));
	const page = next.createHost({ onError: () => {} });
	const facet = evaluateFacet(a.script.built, { hostPackages: { "@acme/app/v3": v3, "@acme/app/v3/web": v3web, "@acme/app/v2": v2 }, plugin: a.script.plugin });
	await page.load([{ name: "@acme/page", facet: panels }, { name: "@acme/panel", facet }], ctx);
	const titles = (await page.registry(v3web.Panels, ctx)).value.join(", ");
	await page.close(ctx);
	return titles;
},
`
	server, err := exAcmeStart(t, plugins, cache, "server", exAcmeModules(app, "server", true), []CoreFacet{{Name: "@acme/core", Module: coreModule}},
		`import { Greetings } from "`+v3+`";`, "held.greetings = self.registry(Greetings);", ops)
	if err != nil {
		t.Fatal(err)
	}

	// 3. Who loaded, and who was refused (each build error names the file, line and rule).
	var printed []string
	printed = append(printed, "1. greetings:")
	for _, line := range server.probe.must("greetings", nil).([]any) {
		printed = append(printed, "   "+line.(string))
	}
	printed = append(printed, "2. refused at build:")
	for _, failure := range server.report.Failed {
		printed = append(printed, "   "+failure.Plugin+": "+exRefused(failure))
	}

	// 4. The web kind provides `/v3/web`: the panel plugin's web facet builds, and a page (a host in the Node process, given
	// the web's modules) evaluates and loads it.
	built := exBuild(t, plugins, []string{"@acme/panel"})
	var facets []string
	for _, facet := range built.Facets {
		facets = append(facets, facet.Facet+" "+string(facet.Kind))
	}
	printed = append(printed, "3. the panel plugin: "+strings.Join(facets, ", "))
	for _, facet := range built.Facets {
		if facet.Facet != "web" || facet.Kind != model.BuiltFacetScript {
			continue
		}
		args := map[string]any{
			"script": scriptsWire([]PluginScript{{Plugin: built.Plugins["@acme/panel"], Built: facet}})[0],
			"v2":     fileURL(web[exAcmeApi+"/v2"]), "v3": fileURL(web[exAcmeApi+"/v3"]), "v3web": fileURL(web[exAcmeApi+"/v3/web"]),
		}
		printed = append(printed, "   panels on the page: "+server.probe.must("panels", args).(string))
	}

	want := []string{
		"1. greetings:",
		"   hello from a v3 plugin on the acme server",
		"   hello from a v2 plugin",
		"2. refused at build:",
		`   @acme/ancient: server.ts:3:40: "@acme/app/v1": @acme/app/v1 is not available: this app provides v3, v2`,
		`   @acme/direct: server.ts:3:29: "@earendil-works/chord": plugins import Chord through the app's API ("@acme/app/v3"), never directly`,
		"   @acme/mixed: one facet imports one version of @acme/app: this one imports @acme/app/v2 (server.ts) and @acme/app/v3 (server.ts)",
		`   @acme/panel: server.ts:5:24: "@acme/app/v3/web": not available to server facets`,
		"3. the panel plugin: server failed, web script",
		"   panels on the page: word count",
	}
	if !slices.Equal(printed, want) {
		t.Fatalf("printed:\n%s\nwant:\n%s", strings.Join(printed, "\n"), strings.Join(want, "\n"))
	}
	collected.none(t)
}

// Ports examples/50-plugin-imports/main.ts: imports between plugins. Plugins of one root import each other's contract files by
// relative path (copied into the importer's script); a per-kind contract file (`contract.web.ts`) is for its kind only, also
// through a helper; a relative path out of the plugin's root is refused; a plugin of another repository takes its dependency
// pinned to a git commit (`words/` committed twice in a local repository, no network), inlined at the installed commit. The
// app's root is required: the load rejects with a PluginError once it settled.
func TestExample50PluginImports(t *testing.T) {
	// npm 12 refuses git dependencies unless allowed (`EALLOWGIT`); the example's dependency is one.
	t.Setenv("npm_config_allow_git", "all")
	for _, tool := range []string{"git", "npm"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("example 50 runs %s: %v", tool, err)
		}
	}
	cache := t.TempDir()
	app := exAcmeApp(t, cache)
	// The example installs into a plugin: it works on a dir of its directories.
	dir := exCopy(t, "50-plugin-imports", "plugins", "user", "words")

	// 1. `@bob/words` in its own git repository: version 1 committed, then version 2. Bob pinned the first commit.
	git := func(dir string, args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-c", "user.email=bob@example.com", "-c", "user.name=bob"}, args...)...)
		command.Dir = dir
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	words := filepath.Join(dir, "words")
	git(words, "init", "--quiet")
	git(words, "add", "--all")
	git(words, "commit", "--quiet", "--message", "words 1")
	pinned := strings.TrimSpace(git(words, "rev-parse", "HEAD"))
	source, err := os.ReadFile(filepath.Join(words, "words.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(words, "words.ts"), []byte(strings.Replace(string(source), `VERSION = "1"`, `VERSION = "2"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	git(words, "commit", "--quiet", "--all", "--message", "words 2")
	git(dir, "clone", "--quiet", "--bare", words, filepath.Join(dir, "words.git"))
	stats := filepath.Join(dir, "user", "stats", "package.json")
	text, err := os.ReadFile(stats)
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Name         string            `json:"name"`
		Private      bool              `json:"private"`
		Type         string            `json:"type"`
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(text, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Dependencies["@bob/words"] = "git+file://" + filepath.Join(dir, "words.git") + "#" + pinned
	text, err = json.MarshalIndent(manifest, "", "\t")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stats, text, 0o644); err != nil {
		t.Fatal(err)
	}
	printed := []string{"1. @bob/stats depends on " + strings.Replace(manifest.Dependencies["@bob/words"], dir, "…", 1)}

	// 2. The component: the app's root required, the user's not; the app's API (example 49's) for both kinds.
	plugins, collected := exAcmeComponent(t, []model.PluginRoot{{Path: filepath.Join(dir, "plugins"), Required: true}, {Path: filepath.Join(dir, "user")}}, cache, []string{"v3"}, nil)

	// 3. Bob's dependencies, installed by npm into his plugin's node_modules (a git dependency at a ref is npm's own).
	if err := plugins.InstallDependencies("@bob/stats", background); err != nil {
		t.Fatal(err)
	}

	// 4. The server loads both roots. The peek plugin of the app's root fails: so the required root's load rejects (a
	// PluginError, after the load settled: the others are loaded), and the app decides (here: it reports and goes on).
	server, err := exAcmeStart(t, plugins, cache, "server", exAcmeModules(app, "server", false), nil, "", "", "")
	pluginError, ok := errors.AsType[*PluginError](err)
	if !ok {
		t.Fatalf("load error = %v, want a *PluginError", err)
	}
	var required []string
	for _, failure := range pluginError.Required {
		required = append(required, failure.Plugin)
	}
	printed = append(printed, "2. the load rejects: PluginError for "+strings.Join(required, ", "))
	for _, failure := range pluginError.Failed {
		printed = append(printed, "   "+failure.Plugin+": "+exRefused(failure))
	}
	printed = append(printed,
		fmt.Sprintf("3. search hits: %v", server.read("acme.search", "hits", "int")),
		fmt.Sprintf("   stats: %v", server.read("bob.stats", "text", "str")))

	// 5. The search plugin's builds: what each copied from the notes plugin (its server facet the shared file, its web facet
	// the web file).
	built := exBuild(t, plugins, []string{"@acme/search"})
	for _, facet := range built.Facets {
		if facet.Kind != model.BuiltFacetScript {
			continue
		}
		var copied []string
		for _, file := range facet.Copied {
			copied = append(copied, filepath.ToSlash(strings.TrimPrefix(file, dir+string(filepath.Separator))))
		}
		printed = append(printed, "4. @acme/search "+facet.Facet+" copied: "+strings.Join(copied, ", "))
	}

	want := []string{
		"1. @bob/stats depends on git+file://…/words.git#" + pinned,
		"2. the load rejects: PluginError for @acme/peek",
		`   @acme/peek: view.ts:3:28: "../notes/contract.web.ts": a web file, imported into a server facet (server.ts → view.ts → ../notes/contract.web.ts)`,
		`   @bob/reach: server.ts:5:23: "../../plugins/notes/contract.ts": outside the plugin's root (base code is reached through the app's API ("@acme/app/v3"), other repositories' plugins through dependencies)`,
		"3. search hits: 3",
		"   stats: 4 words (@bob/words 1)",
		"4. @acme/search server copied: plugins/notes/contract.ts",
		"4. @acme/search web copied: plugins/notes/contract.web.ts",
	}
	if !slices.Equal(printed, want) {
		t.Fatalf("printed:\n%s\nwant:\n%s", strings.Join(printed, "\n"), strings.Join(want, "\n"))
	}
	collected.none(t)
}

// Ports examples/51-single-file-plugin/main.ts: a file named `<name>.<kind>.ts` at a root's top is a facet of the plugin
// `<name>`; `bad` imports another file and is refused; then `hello` becomes a directory with a helper file: the watcher marks
// the root, a build and a reload change the plugin `hello` in place (what the example holds of `hello` is read through the
// probe: the reference is held on the Node host).
func TestExample51SingleFilePlugin(t *testing.T) {
	cache := t.TempDir()
	app := exAcmeApp(t, cache)
	// The example turns a file into a directory: it works on a copy of `plugins/`.
	root := filepath.Join(exCopy(t, "51-single-file-plugin", "plugins"), "plugins")

	// 1. The component, the server loaded: discovery found three single-file plugins; `bad` fails its build.
	plugins, collected := exAcmeComponent(t, []model.PluginRoot{{Path: root}}, cache, []string{"v3"}, nil)
	server, err := exAcmeStart(t, plugins, cache, "server", exAcmeModules(app, "server", false), nil, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	var printed []string
	for _, plugin := range plugins.Discovery().Plugins {
		var files []string
		for _, kind := range []string{"server", "web"} {
			if file, ok := plugin.Facets[kind]; ok {
				files = append(files, strings.TrimPrefix(file, root+string(filepath.Separator)))
			}
		}
		printed = append(printed, "1. "+plugin.Name+" ("+string(plugin.Layout)+"): "+strings.Join(files, ", "))
	}
	for _, failure := range server.report.Failed {
		printed = append(printed, "   "+failure.Plugin+" refused: "+exRefused(failure))
	}
	holding := make(chan error, 1)
	go func() {
		_, err := server.probe.call("hold", nil)
		holding <- err
	}()
	var hello any
	eventually(t, 15*time.Second, "hello held", func() bool {
		select {
		case err := <-holding:
			t.Fatalf("the hold ended before it was held: %v", err)
		default:
		}
		hello = server.probe.must("held", nil)
		return hello != nil
	})
	printed = append(printed, fmt.Sprintf("2. hello: %v | clock: %v", hello, server.read("example.clock", "text", "str")))

	// 2. `hello` needs a second file: it becomes the directory `hello/` (a package.json named `hello`), its files removed.
	if err := os.Mkdir(filepath.Join(root, "hello"), 0o755); err != nil {
		t.Fatal(err)
	}
	exTouch(t, root)
	exWrite(t, filepath.Join(root, "hello", "package.json"), `{"name":"hello","type":"module"}`)
	exWrite(t, filepath.Join(root, "hello", "greeting.ts"), "export const greeting = \"hello from a directory\";\n")
	exWrite(t, filepath.Join(root, "hello", "server.ts"), strings.Join([]string{
		`import { constant, defineFacet, local, str } from "@acme/app/v3";`,
		`import { greeting } from "./greeting.ts";`,
		`const Hello = local("example.hello", { version: 1, params: {}, members: { text: str() } });`,
		"export default defineFacet((host) => host.serve(Hello, () => ({ text: constant(greeting) })));",
	}, "\n"))
	exTouch(t, root)
	for _, file := range []string{"hello.server.ts", "hello.web.ts"} {
		if err := os.Remove(filepath.Join(root, file)); err != nil {
			t.Fatal(err)
		}
	}
	exUntil(t, plugins, "the root marked and hello dirty", func(marks PluginsState) bool {
		return marks.Set && slices.Contains(marks.Dirty, "hello")
	})
	marks := plugins.Marks()
	// Pi's run on macOS prints `bad` and `clock` dirty too: FSEvents delivers, late, the events of the example's own copy of
	// `plugins/` made just before the watch began. Only the files the example changes are marked here (inotify, kqueue).
	exMarks(t, plugins, PluginsState{Dirty: []string{"hello"}, Set: true})
	printed = append(printed, "3. marks: "+exMarksJSON(marks))

	// 3. A build discovers again; the reload changes `hello` (the same plugin, its object restarted), a holder sees the
	// new text.
	built := exBuild(t, plugins, marks.Dirty)
	report, err := plugins.Reload(server.node, built, background)
	if err != nil {
		t.Fatal(err)
	}
	printed = append(printed, "4. reload: "+exMarksPlugins(report)+" | hello: "+server.probe.must("held", nil).(string))
	var layout model.PluginLayout
	for _, plugin := range plugins.Discovery().Plugins {
		if plugin.Name == "hello" {
			layout = plugin.Layout
		}
	}
	printed = append(printed, "   hello is now: "+string(layout))
	server.probe.must("release", nil)
	if err := <-holding; err != nil {
		t.Fatalf("hold: %v", err)
	}

	want := []string{
		"1. bad (file): bad.server.ts",
		"1. clock (file): clock.server.ts",
		"1. hello (file): hello.server.ts, hello.web.ts",
		`   bad refused: bad.server.ts:4:24: "./helper.ts": a single-file plugin imports only host packages (the app's API); make it a directory "bad/" with a package.json`,
		"2. hello: hello from one file | clock: ticking on the acme server",
		`3. marks: {"dirty":["hello"],"install":[],"set":true,"base":false}`,
		`4. reload: {"added":[],"changed":["hello"],"removed":[]} | hello: hello from a directory`,
		"   hello is now: directory",
	}
	if !slices.Equal(printed, want) {
		t.Fatalf("printed:\n%s\nwant:\n%s", strings.Join(printed, "\n"), strings.Join(want, "\n"))
	}
	collected.none(t)
}

// exMarksJSON is `JSON.stringify(plugins.state.value)`: the marks' fields in the order the component gives them.
func exMarksJSON(marks PluginsState) string {
	text, err := json.Marshal(struct {
		Dirty   []string `json:"dirty"`
		Install []string `json:"install"`
		Set     bool     `json:"set"`
		Base    bool     `json:"base"`
	}{exList(marks.Dirty), exList(marks.Install), marks.Set, marks.Base})
	if err != nil {
		panic(err)
	}
	return string(text)
}

// exMarksPlugins is `JSON.stringify(report.plugins)`: `{"added":[],"changed":[],"removed":[]}`.
func exMarksPlugins(report host.ReloadReport) string {
	text, err := json.Marshal(struct {
		Added   []string `json:"added"`
		Changed []string `json:"changed"`
		Removed []string `json:"removed"`
	}{exList(report.Plugins.Added), exList(report.Plugins.Changed), exList(report.Plugins.Removed)})
	if err != nil {
		panic(err)
	}
	return string(text)
}
