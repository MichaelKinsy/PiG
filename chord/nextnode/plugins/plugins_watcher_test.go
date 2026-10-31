package plugins

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/test/next-node/plugins/watcher.test.ts. Tests of the watcher (slice 7c §5): dirty marks from the
// files facets used and from plugin directories, `install`, `set`, `base`, a change during a build keeping its mark,
// `node_modules` and `.git` ignored, real file events, and the marks as a JSON watchable.

// watcherFixture is what `watched()` returns: a root with plugins and a base directory, their watcher, and the host loop
// its batch runs on (the sections of the watcher's methods and of its file events).
type watcherFixture struct {
	t         *testing.T
	root      string
	base      string
	watcher   *pluginWatcher
	loop      *host.Loop
	scheduler *chord.DeliveryScheduler
	mu        sync.Mutex
	failures  []error
}

// run runs work as a section of the loop: what the host's callers of the watcher do.
func (f *watcherFixture) run(work func()) { f.loop.Run(work) }

// failed is what the watcher reported as a failure to watch.
func (f *watcherFixture) failed() []error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]error{}, f.failures...)
}

// marks is the watcher's value, as `PluginsState` (the JSON object it is).
func (f *watcherFixture) marks() PluginsState {
	f.t.Helper()
	var value delta.JsonValue
	f.run(func() {
		var err error
		if value, err = f.watcher.Value(); err != nil {
			f.t.Error(err)
		}
	})
	object, ok := value.(*delta.JsonObject)
	if !ok {
		f.t.Fatalf("value = %#v, want a JSON object", value)
	}
	return stateOf(f.t, object)
}

// stateOf reads the marks out of their JSON object.
func stateOf(t testing.TB, object *delta.JsonObject) PluginsState {
	t.Helper()
	names := func(key string) []string {
		list, ok := object.Value(key).([]any)
		if !ok {
			t.Fatalf("%s = %#v, want a list", key, object.Value(key))
		}
		out := make([]string, len(list))
		for i, each := range list {
			if out[i], ok = each.(string); !ok {
				t.Fatalf("%s[%d] = %#v, want a string", key, i, each)
			}
		}
		return out
	}
	set, setOK := object.Value("set").(bool)
	base, baseOK := object.Value("base").(bool)
	if !setOK || !baseOK {
		t.Fatalf("set and base = %#v, %#v, want booleans", object.Value("set"), object.Value("base"))
	}
	return PluginsState{Dirty: names("dirty"), Install: names("install"), Set: set, Base: base}
}

// watched is a root with plugins `a` (worker), `b` (server) and the contracts fixture, a base directory, and a watcher of
// both.
func watched(t *testing.T) *watcherFixture {
	t.Helper()
	root := realOf(t, tempRoot(t, "contracts"))
	base := realOf(t, tempRoot(t))
	writePlugin(t, root, "a", "@test/a", map[string]string{"worker.ts": "", "own.ts": ""}, nil)
	writePlugin(t, root, "b", "@test/b", map[string]string{"server.ts": ""}, nil)
	f := &watcherFixture{t: t, root: root, base: base, loop: host.NewLoop(), scheduler: chord.NewDeliveryScheduler()}
	batch := state.NewBatch(func(state.BatchError) {}, nil).UseScheduler(f.scheduler).UseMicrotasks(f.loop.Queue).UseSection(f.loop.Run)
	f.watcher = newPluginWatcher(watcherOptions{
		Roots: []string{root}, Base: []string{base}, Kinds: kinds,
		Failed: func(err error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.failures = append(f.failures, err)
		},
	}, batch)
	t.Cleanup(func() {
		f.run(f.watcher.close)
		f.watcher.join()
	})
	discovery, err := DiscoverPlugins([]string{root}, DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	f.run(func() { f.watcher.know(discovery, f.watcher.discoverySnapshot()) })
	contract := filepath.Join(root, "contracts", "contract.ts")
	used := func(plugin, facet string, files ...string) BuiltFacet {
		inputs := make([]BuildInput, len(files))
		for i, file := range files {
			inputs[i] = BuildInput{File: file}
		}
		return BuiltFacet{Kind: BuiltFacetScript, Plugin: plugin, Facet: facet, Inputs: inputs, Copied: []string{contract}, Assets: []Asset{}}
	}
	f.run(func() {
		f.watcher.used([]BuiltFacet{
			used("@test/a", "worker", filepath.Join(root, "a", "worker.ts"), contract),
			used("@test/b", "server", filepath.Join(root, "b", "server.ts"), contract),
		})
	})
	return f
}

func (f *watcherFixture) mark(path string) { f.run(func() { f.watcher.mark(path) }) }

// know has the watcher learn a new discovery of the root, as the component does after discovering again.
func (f *watcherFixture) know(seen DiscoverySnapshot) {
	f.t.Helper()
	discovery, err := DiscoverPlugins([]string{f.root}, DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		f.t.Fatal(err)
	}
	f.run(func() { f.watcher.know(discovery, seen) })
}

func (f *watcherFixture) discoverySnapshot() (seen DiscoverySnapshot) {
	f.run(func() { seen = f.watcher.discoverySnapshot() })
	return seen
}

func (f *watcherFixture) snapshot(names ...string) (snapshot MarkSnapshot) {
	f.run(func() { snapshot = f.watcher.snapshot(names) })
	return snapshot
}

func (f *watcherFixture) applied(snapshot MarkSnapshot, names ...string) {
	f.run(func() { f.watcher.applied(names, snapshot) })
}

func (f *watcherFixture) close() { f.run(f.watcher.close) }

func expectMarks(t *testing.T, got, want PluginsState) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("marks = %+v, want %+v", got, want)
	}
}

// changes collects what a watch's listener, which runs on a delivery goroutine, was called with: support.ts `changesOf`
// (chord/next/internal/nexttest is internal to chord/next).
type changes struct {
	mu    sync.Mutex
	calls []chord.WatchCall[delta.JsonValue]
}

func (c *changes) items() []chord.WatchCall[delta.JsonValue] {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]chord.WatchCall[delta.JsonValue]{}, c.calls...)
}

// changesOf starts watch and collects every call it gets.
func changesOf(watch chord.WatchHandle[delta.JsonValue]) *changes {
	collected := &changes{}
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		collected.mu.Lock()
		defer collected.mu.Unlock()
		collected.calls = append(collected.calls, call)
		return nil
	})
	return collected
}

func list(names ...string) []string { return append([]string{}, names...) }

func TestTheWatchersMarksDirtiesEveryPluginWhoseFacetsUsedAChangedFileAndThePluginWhoseDirectoryContainsIt(t *testing.T) {
	f := watched(t)
	f.mark(filepath.Join(f.root, "contracts", "contract.ts"))
	expectMarks(t, f.marks(), PluginsState{Dirty: list("@test/a", "@test/b", "@test/contracts"), Install: list()})
	f.close()
}

func TestTheWatchersMarksDirtiesThePluginOfAFileNoFacetReadYetAsksForAnInstallForAPackageJSON(t *testing.T) {
	f := watched(t)
	f.mark(filepath.Join(f.root, "a", "new.ts"))
	f.mark(filepath.Join(f.root, "b", "package.json"))
	expectMarks(t, f.marks(), PluginsState{Dirty: list("@test/a"), Install: list("@test/b")})
	// Installed: the mark clears, and the plugin is dirty (its packages changed).
	f.run(func() { f.watcher.installed("@test/b") })
	expectMarks(t, f.marks(), PluginsState{Dirty: list("@test/a", "@test/b"), Install: list()})
	f.close()
}

func TestTheWatchersMarksAsksForDiscoveryForADirectoryUnderARootAndAFacetFileAtAPluginsTopBaseForBaseCode(t *testing.T) {
	f := watched(t)
	f.mark(filepath.Join(f.root, "c"))
	if marks := f.marks(); !marks.Set || len(marks.Dirty) != 0 {
		t.Fatalf("marks = %+v, want set and nothing dirty", marks)
	}
	seen := f.discoverySnapshot()
	f.know(seen)
	if marks := f.marks(); marks.Set {
		t.Fatalf("marks = %+v, want set cleared", marks)
	}
	f.mark(filepath.Join(f.root, "b", "web.tsx"))
	if marks := f.marks(); !marks.Set || !reflect.DeepEqual(marks.Dirty, list("@test/b")) {
		t.Fatalf("marks = %+v, want set and @test/b dirty", marks)
	}
	f.mark(filepath.Join(f.base, "core.ts"))
	if marks := f.marks(); !marks.Base {
		t.Fatalf("marks = %+v, want base", marks)
	}
	f.close()
}

func TestTheWatchersMarksIgnoresNodeModulesAndGit(t *testing.T) {
	f := watched(t)
	f.mark(filepath.Join(f.root, "a", "node_modules", "x", "index.js"))
	f.mark(filepath.Join(f.root, "a", ".git", "HEAD"))
	expectMarks(t, f.marks(), PluginsState{Dirty: list(), Install: list()})
	f.close()
}

func TestTheWatchersMarksKeepsTheMarkOfAChangeDuringABuildOnlyAnUnchangedMarkClearsWhenApplied(t *testing.T) {
	f := watched(t)
	f.mark(filepath.Join(f.root, "a", "own.ts"))
	snapshot := f.snapshot("@test/a")
	// Saved again while the build runs.
	f.mark(filepath.Join(f.root, "a", "own.ts"))
	f.applied(snapshot, "@test/a")
	if marks := f.marks(); !reflect.DeepEqual(marks.Dirty, list("@test/a")) {
		t.Fatalf("marks = %+v, want @test/a still dirty", marks)
	}
	f.applied(f.snapshot("@test/a"), "@test/a")
	if marks := f.marks(); len(marks.Dirty) != 0 {
		t.Fatalf("marks = %+v, want nothing dirty", marks)
	}
	// A discovery that started before a change of the set keeps `set`.
	seen := f.discoverySnapshot()
	f.mark(filepath.Join(f.root, "d"))
	f.know(seen)
	if marks := f.marks(); !marks.Set {
		t.Fatalf("marks = %+v, want set kept", marks)
	}
	f.close()
}

func TestTheWatchersMarksFromRealFileEventsPublishingTheMarksToAWatch(t *testing.T) {
	f := watched(t)
	var watch chord.WatchHandle[delta.JsonValue]
	f.run(func() {
		var err error
		if watch, err = f.watcher.Watch(background); err != nil {
			t.Error(err)
		}
	})
	changes := changesOf(watch)
	writeFiles(t, filepath.Join(f.root, "a"), map[string]string{"node_modules-free.ts": "export {};"})
	writeFiles(t, filepath.Join(f.root, "b", "node_modules", "pkg"), map[string]string{"index.js": ""})
	eventually(t, 5*time.Second, `dirty ["@test/a"]`, func() bool {
		return reflect.DeepEqual(f.marks().Dirty, list("@test/a"))
	})
	f.scheduler.Wait()
	calls := changes.items()
	if len(calls) == 0 {
		t.Fatal("the watch got no frame")
	}
	last := calls[len(calls)-1]
	if last.Kind != chord.WatchCallValue {
		t.Fatalf("last call = %+v, want a value", last)
	}
	object, ok := last.Value.(*delta.JsonObject)
	if !ok {
		t.Fatalf("last value = %#v, want a JSON object", last.Value)
	}
	if got := stateOf(t, object).Dirty; !reflect.DeepEqual(got, list("@test/a")) {
		t.Fatalf("last frame's dirty = %v, want [@test/a]", got)
	}
	f.close()
}

func TestTheWatcherFixRound1NeverLetsAnOlderBuildsAppliedSnapshotClearANewerEditsMarkAstra(t *testing.T) {
	f := watched(t)
	file := filepath.Join(f.root, "a", "own.ts")
	f.mark(file)
	first := f.snapshot("@test/a")
	f.mark(file)
	second := f.snapshot("@test/a")
	f.applied(second, "@test/a")
	if marks := f.marks(); len(marks.Dirty) != 0 {
		t.Fatalf("marks = %+v, want nothing dirty", marks)
	}
	f.mark(file)
	f.applied(first, "@test/a")
	if marks := f.marks(); !reflect.DeepEqual(marks.Dirty, list("@test/a")) {
		t.Fatalf("marks = %+v, want @test/a dirty", marks)
	}
	f.close()
}

func TestTheWatcherFixRound1DropsTheInstallMarkOfAPluginTheNextDiscoveryNoLongerHasExample38(t *testing.T) {
	f := watched(t)
	f.mark(filepath.Join(f.root, "b", "package.json"))
	if marks := f.marks(); !reflect.DeepEqual(marks.Install, list("@test/b")) {
		t.Fatalf("marks = %+v, want @test/b to install", marks)
	}
	if err := os.RemoveAll(filepath.Join(f.root, "b")); err != nil {
		t.Fatal(err)
	}
	seen := f.discoverySnapshot()
	f.know(seen)
	if marks := f.marks(); len(marks.Install) != 0 {
		t.Fatalf("marks = %+v, want no install", marks)
	}
	f.close()
}

func TestTheWatcherFixRound1WatchesADirectoryThatAppearsLaterAndMarksTheFilesInIt(t *testing.T) {
	f := watched(t)
	writeFiles(t, filepath.Join(f.root, "a", "lib"), map[string]string{"first.ts": ""})
	eventually(t, 5*time.Second, `dirty ["@test/a"]`, func() bool { return reflect.DeepEqual(f.marks().Dirty, list("@test/a")) })
	f.applied(f.snapshot("@test/a"), "@test/a")
	writeFiles(t, filepath.Join(f.root, "a", "lib"), map[string]string{"second.ts": "export {};"})
	eventually(t, 5*time.Second, `dirty ["@test/a"]`, func() bool { return reflect.DeepEqual(f.marks().Dirty, list("@test/a")) })
	f.close()
}

func TestTheWatcherAndLinksSurvivesALinkLoopCreatedUnderARootItsStatNeverEscapesTheCallbackAndStillMarksItSol(t *testing.T) {
	f := watched(t)
	if err := os.Symlink("loop", filepath.Join(f.root, "a", "loop")); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, `dirty ["@test/a"]`, func() bool { return reflect.DeepEqual(f.marks().Dirty, list("@test/a")) })
	f.close()
}
