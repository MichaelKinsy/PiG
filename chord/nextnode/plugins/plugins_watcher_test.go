package plugins

// pi: packages/chord/src/node/plugins/watcher.ts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/pluginstest"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Ports packages/chord/test/node/plugins/watcher.test.ts. Tests of the watcher (slice 7c §5, slice 12 §4): dirty marks
// from the files facets used and from plugin directories, `install`, `set`, `base`, a change during a build keeping its mark,
// `node_modules` and `.git` ignored, real file events, the marks as a JSON watchable, and single-file plugins' files.

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

// watcherOn is a watcher of root and base on a fresh loop, closed and joined when the test ends.
func watcherOn(t *testing.T, root, base string) *watcherFixture {
	t.Helper()
	return watcherOver(t, []string{root}, base)
}

// watcherOver is a watcher of roots (root is the first, "" for none) and base on a fresh loop, closed and joined when the
// test ends.
func watcherOver(t *testing.T, roots []string, base string) *watcherFixture {
	t.Helper()
	return watcherOverNotifier(t, roots, base, nil)
}

// watcherOverNotifier is watcherOver on the system watcher makeNotifier makes (nil: fsnotify's).
func watcherOverNotifier(t *testing.T, roots []string, base string, makeNotifier func() (notifier, error)) *watcherFixture {
	t.Helper()
	root := ""
	if len(roots) > 0 {
		root = roots[0]
	}
	f := &watcherFixture{t: t, root: root, base: base, loop: host.NewLoop(), scheduler: chord.NewDeliveryScheduler()}
	batch := state.NewBatch(func(state.BatchError) {}, nil).UseScheduler(f.scheduler).UseMicrotasks(f.loop.Queue).UseSection(f.loop.Run, f.loop.InSection)
	f.watcher = newPluginWatcher(watcherOptions{
		Roots: roots, Base: []string{base}, Kinds: kinds, Notifier: makeNotifier,
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
	return f
}

// watched is a root with plugins `a` (worker), `b` (server) and the contracts fixture, a base directory, and a watcher of
// both.
func watched(t *testing.T) *watcherFixture {
	t.Helper()
	root := realOf(t, tempRoot(t, "contracts"))
	base := realOf(t, tempRoot(t))
	writePlugin(t, root, "a", "@test/a", map[string]string{"worker.ts": "", "own.ts": ""}, nil)
	writePlugin(t, root, "b", "@test/b", map[string]string{"server.ts": ""}, nil)
	f := watcherOn(t, root, base)
	discovery, err := model.DiscoverPlugins([]model.PluginRoot{{Path: root}}, model.DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	f.run(func() { f.watcher.know(discovery, f.watcher.discoverySnapshot()) })
	contract := filepath.Join(root, "contracts", "contract.ts")
	used := func(plugin, facet string, files ...string) model.BuiltFacet {
		inputs := make([]model.BuildInput, len(files))
		for i, file := range files {
			inputs[i] = model.BuildInput{File: file}
		}
		return model.BuiltFacet{Kind: model.BuiltFacetScript, Plugin: plugin, Facet: facet, Inputs: inputs, Copied: []string{contract}, Assets: []model.Asset{}}
	}
	f.run(func() {
		f.watcher.used([]model.BuiltFacet{
			used("@test/a", "worker", filepath.Join(root, "a", "worker.ts"), contract),
			used("@test/b", "server", filepath.Join(root, "b", "server.ts"), contract),
		})
	})
	// The fixture's own files were written, and discovery read them, as the watcher started: what the file system reports
	// of that arrives on its own time (Windows reports a directory's last-write time, and a file's, lazily), and must not
	// be a mark of the case. The case starts from marks of nothing but what it does.
	f.settle()
	f.forget()
	return f
}

// settle returns once the watcher handled the events of every file operation made before the call (pluginstest.Settle, the
// barrier a component's watcher has too).
func (f *watcherFixture) settle() {
	f.t.Helper()
	pluginstest.Settle(f.t, f)
}

// WatchTree, Watches and ComparingBase are the barrier's questions (pluginstest.Watcher), asked in a section of the loop.
func (f *watcherFixture) WatchTree(dir string) ([]string, bool) {
	var watched []string
	var watching bool
	f.run(func() { watched, watching = f.watcher.alsoWatch(dir) })
	return watched, watching
}

func (f *watcherFixture) Watches(dir string) bool {
	var watches bool
	f.run(func() { watches = f.watcher.watches(dir) })
	return watches
}

func (f *watcherFixture) ComparingBase() bool {
	var comparing bool
	f.run(func() { comparing = f.watcher.comparingBase() })
	return comparing
}

// forget clears every mark the watcher holds: what a case sees is what it does from here.
func (f *watcherFixture) forget() { f.run(func() { forgetMarks(f.watcher) }) }

// forgetMarks clears the marks of w; it runs in a section of its host.
func forgetMarks(w *pluginWatcher) {
	clear(w.dirty)
	clear(w.install)
	w.set, w.manifest = 0, 0
	clear(w.differing)
	w.marksChanged()
}

// marksOf is the marks as a case that changed files for real reads them: on Windows without what it adds to the directories
// the case changed an entry of (exTouch) that want does not have (see withoutDirectoryStamps).
func (f *watcherFixture) marksOf(want PluginsState) PluginsState {
	f.t.Helper()
	return f.withoutStamps(f.marks(), want)
}

func (f *watcherFixture) withoutStamps(marks, want PluginsState) PluginsState {
	if runtime.GOOS == "windows" {
		return withoutDirectoryStamps(f.t, marks, want)
	}
	return marks
}

func (f *watcherFixture) mark(path string) { f.run(func() { f.watcher.mark(path) }) }

// know has the watcher learn a new discovery of the root, as the component does after discovering again.
func (f *watcherFixture) know(seen DiscoverySnapshot) {
	f.t.Helper()
	discovery, err := model.DiscoverPlugins([]model.PluginRoot{{Path: f.root}}, model.DiscoveryOptions{Kinds: kinds}, background)
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
	writeFiles(t, f.base, map[string]string{"core.ts": "export {};"})
	f.mark(filepath.Join(f.base, "core.ts"))
	eventually(t, 5*time.Second, "base", func() bool { return f.marks().Base })
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
	want := PluginsState{Dirty: list("@test/a")}
	writeFiles(t, filepath.Join(f.root, "a"), map[string]string{"node_modules-free.ts": "export {};"})
	writeFiles(t, filepath.Join(f.root, "b", "node_modules", "pkg"), map[string]string{"index.js": ""})
	// b got an entry (node_modules, which no change under it marks): Windows reports b's own last-write time as a change of b.
	exTouch(t, filepath.Join(f.root, "b"))
	eventually(t, 5*time.Second, `dirty ["@test/a"]`, func() bool {
		return reflect.DeepEqual(f.marksOf(want).Dirty, want.Dirty)
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
	if got := f.withoutStamps(stateOf(t, object), want).Dirty; !reflect.DeepEqual(got, want.Dirty) {
		t.Fatalf("last frame's dirty = %v, want [@test/a]", got)
	}
	f.close()
}

// watcher.test.ts › the watcher and single-file plugins (slice 12 §4) › dirties a single-file plugin for a change of its file; a
// file appearing or disappearing at a root's top sets set.
func TestTheWatcherAndSingleFilePluginsDirtiesASingleFilePluginForAChangeOfItsFileAFileAppearingOrDisappearingAtARootsTopSetsSet(t *testing.T) {
	f := watched(t)
	writeFiles(t, f.root, map[string]string{"hello.server.ts": ""})
	f.settle()
	f.mark(filepath.Join(f.root, "hello.server.ts"))
	expectMarks(t, f.marks(), PluginsState{Dirty: list(), Install: list(), Set: true})
	seen := f.discoverySnapshot()
	f.know(seen)
	expectMarks(t, f.marks(), PluginsState{Dirty: list(), Install: list()})
	// Its own file: only it is dirty (not every plugin of the root its directory is).
	f.mark(filepath.Join(f.root, "hello.server.ts"))
	expectMarks(t, f.marks(), PluginsState{Dirty: list("hello"), Install: list()})
	writeFiles(t, f.root, map[string]string{"hello.web.tsx": ""})
	f.settle()
	f.mark(filepath.Join(f.root, "hello.web.tsx"))
	if marks := f.marks(); !marks.Set {
		t.Fatalf("marks = %+v, want set", marks)
	}
	f.know(f.discoverySnapshot())
	if err := os.Remove(filepath.Join(f.root, "hello.server.ts")); err != nil {
		t.Fatal(err)
	}
	f.settle()
	f.mark(filepath.Join(f.root, "hello.server.ts"))
	expectMarks(t, f.marks(), PluginsState{Dirty: list("hello"), Install: list(), Set: true})
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
	// What the file system still reports of the first file (lib's last-write time, a's) must come before the mark clears, or
	// it would stand for the second file's event.
	f.settle()
	f.applied(f.snapshot("@test/a"), "@test/a")
	writeFiles(t, filepath.Join(f.root, "a", "lib"), map[string]string{"second.ts": "export {};"})
	eventually(t, 5*time.Second, `dirty ["@test/a"]`, func() bool { return reflect.DeepEqual(f.marks().Dirty, list("@test/a")) })
	f.close()
}

func TestTheWatcherAndLinksSurvivesALinkLoopCreatedUnderARootItsStatNeverEscapesTheCallbackAndStillMarksItSol(t *testing.T) {
	f := watched(t)
	testenv.Symlink(t, "loop", filepath.Join(f.root, "a", "loop"))
	eventually(t, 5*time.Second, `dirty ["@test/a"]`, func() bool { return reflect.DeepEqual(f.marks().Dirty, list("@test/a")) })
	f.close()
}

// Not upstream's: the cases of watcher.ts's own rules that upstream's file events do not reach.

func TestTheWatcherRemovingAWatchedRootOrBaseDirectoryMarksSetAndBaseAsFsWatchReportsIt(t *testing.T) {
	root := realOf(t, tempRoot(t))
	base := realOf(t, tempRoot(t))
	// The base directory's source file is what the watcher recorded at its start: the directory's removal is its loss.
	writeFiles(t, base, map[string]string{"core.ts": "export {};"})
	f := watcherOn(t, root, base)
	// Nothing in the root: only its own removal is an event, which `fs.watch` names by the directory's base name under it.
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "set", func() bool { return f.marks().Set })
	if f.marks().Base {
		t.Fatal("base set by a root's removal")
	}
	if err := os.RemoveAll(base); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "base", func() bool { return f.marks().Base })
}

func TestTheWatcherAfterCloseOnlyAPluginLinkedFromOutsideGetsItsWatchAgain(t *testing.T) {
	// watcher.ts has no closed flag: `close` clears the trees, and the next discovery puts the watches of linked plugin
	// directories back (roots and base code are put back by the constructor only).
	elsewhere := realOf(t, tempRoot(t))
	dir := writePlugin(t, elsewhere, "linked", "@test/linked", map[string]string{"worker.ts": ""}, nil)
	root := realOf(t, tempRoot(t))
	testenv.RequireDirectoryLink(t, dir, filepath.Join(root, "link"))
	f := watcherOn(t, root, realOf(t, tempRoot(t)))
	f.know(f.discoverySnapshot())
	f.run(func() {
		if _, ok := f.watcher.trees[realOf(t, dir)]; !ok {
			t.Errorf("trees = %v, want the linked plugin's", f.watcher.trees)
		}
	})
	f.close()
	f.know(f.discoverySnapshot())
	f.run(func() {
		if got := len(f.watcher.trees); got != 1 {
			t.Errorf("trees = %v, want only the linked plugin's", f.watcher.trees)
		}
	})
	writeFiles(t, dir, map[string]string{"own.ts": "export {};"})
	eventually(t, 5*time.Second, `dirty ["@test/linked"]`, func() bool { return reflect.DeepEqual(f.marks().Dirty, list("@test/linked")) })
}

// Not a port: closing the fsnotify watcher is slow where it removes a watch per entry (kqueue, on a large directory), and a
// failure to watch ends the watching in a section of the host, which holds the host's lock. The close of the watcher must run
// outside the section: here it is held back, and the host stays free (its sections run, the failure is reported) until it
// is let go; join waits for it.
func TestTheWatcherClosesTheSystemWatcherOutsideTheHostsLockAndJoinWaitsForIt(t *testing.T) {
	base := realOf(t, tempRoot(t))
	f := watcherOver(t, nil, base)
	closing, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	f.watcher.closeFS = func(watcher notifier) {
		close(closing)
		<-release
		_ = watcher.Close()
		close(closed)
	}
	// Once the test ends the fixture's cleanup joins: the hook must have been let go by then.
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	free := make(chan struct{})
	go func() {
		f.run(func() { f.watcher.failed(errors.New("a watch failed")) })
		f.run(func() {}) // a later section of the host: it enters while the watcher is still closing
		close(free)
	}()
	select {
	case <-free:
	case <-time.After(10 * time.Second):
		t.Fatal("the host's lock was held while the system watcher closed")
	}
	select {
	case <-closing:
	case <-time.After(10 * time.Second):
		t.Fatal("the system watcher was not closed")
	}
	if len(f.failed()) != 1 {
		t.Fatalf("failures = %v, want the one watch failure reported", f.failed())
	}
	joined := make(chan struct{})
	go func() { f.watcher.join(); close(joined) }()
	select {
	case <-joined:
		t.Fatal("join returned before the system watcher was closed")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	select {
	case <-joined:
	case <-time.After(10 * time.Second):
		t.Fatal("join did not return after the system watcher was closed")
	}
	<-closed
}

// discoveryAfterWatchTree is a fixture's watcher as the barrier asks it, with a discovery of the root landing right after each
// WatchTree: the order of a build that discovers again beside a test's barrier.
type discoveryAfterWatchTree struct{ *watcherFixture }

func (w discoveryAfterWatchTree) WatchTree(dir string) ([]string, bool) {
	watched, watching := w.watcherFixture.WatchTree(dir)
	w.know(w.discoverySnapshot())
	return watched, watching
}

// No Pi counterpart: Pi's watcher watches no tree on request (watcher.ts:177-181 drops every tree that is no root's, base's or
// linked plugin's, and nothing asks it for another). A tree the watcher was asked to watch (alsoWatch: the tests' barrier's) stays
// watched across discoveries, in both orders of the request and a discovery, with the directories under it; a directory made in
// it afterwards gets its watch when its event is handled.
func TestATreeTheWatcherWasAskedToWatchStaysWatchedAcrossDiscoveries(t *testing.T) {
	orders := []struct {
		name  string
		steps func(f *watcherFixture, tree string) bool
	}{
		{"asked, then a discovery", func(f *watcherFixture, tree string) bool {
			_, watching := f.WatchTree(tree)
			f.know(f.discoverySnapshot())
			return watching
		}},
		{"a discovery, then asked", func(f *watcherFixture, tree string) bool {
			f.know(f.discoverySnapshot())
			_, watching := f.WatchTree(tree)
			return watching
		}},
	}
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			f := watched(t)
			tree := realOf(t, t.TempDir())
			inside := filepath.Join(tree, "inside")
			if err := os.Mkdir(inside, 0o755); err != nil {
				t.Fatal(err)
			}
			if !order.steps(f, tree) {
				t.Fatal("the watcher watches nothing")
			}
			for _, dir := range []string{tree, inside} {
				if !f.Watches(dir) {
					t.Fatalf("%s is not watched", dir)
				}
			}
			made := filepath.Join(tree, "made")
			if err := os.Mkdir(made, 0o755); err != nil {
				t.Fatal(err)
			}
			eventually(t, 5*time.Second, "a watch on the directory made in the tree", func() bool { return f.Watches(made) })
		})
	}
}

// watcher.ts:177-181: a discovery drops the tree of a plugin directory linked from outside the roots once its link is gone
// from the root. A tree the watcher was asked to watch changes nothing of that, and stays.
func TestADiscoveryDropsTheTreeOfAPluginWhoseLinkIsGoneAndKeepsATreeTheWatcherWasAskedToWatch(t *testing.T) {
	elsewhere := realOf(t, tempRoot(t))
	dir := realOf(t, writePlugin(t, elsewhere, "linked", "@test/linked", map[string]string{"worker.ts": ""}, nil))
	root := realOf(t, tempRoot(t))
	link := filepath.Join(root, "link")
	testenv.RequireDirectoryLink(t, dir, link)
	f := watcherOn(t, root, realOf(t, tempRoot(t)))
	asked := realOf(t, t.TempDir())
	if _, watching := f.WatchTree(asked); !watching {
		t.Fatal("the watcher watches nothing")
	}
	f.know(f.discoverySnapshot())
	if !f.Watches(dir) {
		t.Fatal("the linked plugin's directory is not watched")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	f.know(f.discoverySnapshot())
	if f.Watches(dir) {
		t.Fatal("the discovery kept the tree of a plugin whose link is gone")
	}
	if !f.Watches(asked) {
		t.Fatal("the discovery dropped the tree the watcher was asked to watch")
	}
}

// The barrier (pluginstest.Settle) returns when a discovery lands between its WatchTree and the directory it makes. The build of
// an apply that runs beside a test's edit discovers at any moment (reload_rollout_test.go, two rollouts back to back): such a
// discovery dropped the barrier's tree, the directory made next got no watch, and Settle timed out.
func TestTheBarrierReturnsWhenADiscoveryLandsBetweenItsWatchTreeAndItsDirectory(t *testing.T) {
	f := watched(t)
	pluginstest.Settle(t, discoveryAfterWatchTree{f})
}
