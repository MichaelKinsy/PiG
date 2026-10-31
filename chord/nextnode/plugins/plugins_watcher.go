package plugins

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next-node/plugins/watcher.ts. The watcher and its marks (slice 7c §5): a watch on every
// directory of the plugin roots and the app's base code, `node_modules` and `.git` never watched (a directory appearing
// later gets its watch), marks what changed and never applies it. A changed file dirties every plugin whose facets used it
// (a copied contract every user) or whose directory contains it; a changed `package.json` asks for an install (and for its
// metadata to be read again); a plugin directory or facet file added or removed asks for discovery again; a file of base
// code asks the app to restart its hosts. The marks are a JSON watchable; each mark records the generation of its latest
// change (generations only increase), so a change during a build keeps its mark, however late an older build's rollout
// clears.
//
// Upstream's `fs.watch` is fsnotify here: one fsnotify.Watcher, one watch per directory (neither recurses, as `fs.watch`
// does not on Linux), its events read by one goroutine the watcher owns. Every method runs in a section of the host, as
// the code of the host's own sources does; a file event enters one through the batch's section (state.Batch.UseSection),
// as a timer does. close does not wait for the goroutine (it may run in a section the goroutine is waiting to enter): join
// does, after the section.

// PluginsState is the marks: plugins to rebuild, plugins to install, whether to discover again, whether base code changed.
// Its value as a JSON watchable is the object `{ dirty, install, set, base }`.
type PluginsState struct {
	Dirty   []string
	Install []string
	Set     bool
	Base    bool
}

// json is the state as the watchable's value.
func (s PluginsState) json() *delta.JsonObject {
	list := func(names []string) []any {
		items := make([]any, len(names))
		for i, name := range names {
			items[i] = name
		}
		return items
	}
	return delta.JsonObjectOf("dirty", list(s.Dirty), "install", list(s.Install), "set", s.Set, "base", s.Base)
}

// MarkSnapshot is the generation of each dirty plugin's latest change when a build started: what its rollout may clear.
// Plugins is by name (a plugin with no mark is 0), Names its keys in the order they were asked, as the Map they are.
type MarkSnapshot struct {
	Plugins map[string]int
	Names   []string
}

// DiscoverySnapshot is the generations of the latest change of the plugin set and of a `package.json` when a discovery
// started.
type DiscoverySnapshot struct {
	Set      int
	Manifest int
}

// watcherOptions is what the watcher watches and how it reports: the roots, the base code, the facet kinds, a failure to
// watch.
type watcherOptions struct {
	Roots []string
	Base  []string
	Kinds []string
	// Failed is called when the watcher cannot watch (the system's limit): reported once, after which the app builds on
	// demand. It runs in a section of the host.
	Failed func(error)
}

// ignored are the directories never watched, and changes under them never marked.
var ignored = map[string]struct{}{"node_modules": {}, ".git": {}}

func isIgnored(name string) bool {
	_, ok := ignored[name]
	return ok
}

// usedFiles are the files one built facet read, with its plugin.
type usedFiles struct {
	plugin string
	files  map[string]struct{}
}

// pluginWatcher is the watcher and the marks (its value), a JSON watchable published at most once per microtask turn with
// changes. It marks for the plugins of the last discovery (know) and the files their last builds read (used).
type pluginWatcher struct {
	state.Source
	options  watcherOptions
	roots    []string
	baseDirs []string
	// trees are the trees it watches: the roots, the base code, and plugin directories linked from outside the roots.
	trees map[string]struct{}
	// watched is its watches, one per directory of its trees.
	watched map[string]struct{}
	// fs is the one fsnotify watcher the watches are on, made with the first (nil before it and after close); running joins
	// the goroutine that reads its events.
	fs      *fsnotify.Watcher
	running sync.WaitGroup
	// reported is set once a failure to watch was reported; closed once the watcher was closed.
	reported bool
	closed   bool
	// generation is the one the next change gets: every mark records a newer one than any before it.
	generation int
	// dirty is each dirty plugin's latest change, by generation.
	dirty   map[string]int
	install map[string]struct{}
	// set is the latest change of the plugin set since the last discovery (0: none).
	set int
	// manifest is the latest change of a known plugin's `package.json` since the last discovery (0: none).
	manifest    int
	baseChanged bool
	discovery   Discovery
	// usedBy is the files each built facet read (its inputs and copies), by plugin and kind.
	usedBy    map[string]usedFiles
	value     delta.JsonValue
	scheduled bool
}

var _ state.SourceImpl = (*pluginWatcher)(nil)

// newPluginWatcher starts watching the roots and the base code of options, on batch.
func newPluginWatcher(options watcherOptions, batch *state.Batch) *pluginWatcher {
	w := &pluginWatcher{
		options:    options,
		trees:      map[string]struct{}{},
		watched:    map[string]struct{}{},
		generation: 1,
		dirty:      map[string]int{},
		install:    map[string]struct{}{},
		discovery:  Discovery{Plugins: []Plugin{}, Problems: []DiscoveryProblem{}},
		usedBy:     map[string]usedFiles{},
	}
	w.Init(batch, w)
	// Events name paths under the directories as watched: by their real paths, as discovery names plugins.
	w.roots = realPaths(options.Roots)
	w.baseDirs = realPaths(options.Base)
	batch.Section(func() {
		for _, dir := range slices.Concat(w.roots, w.baseDirs) {
			w.watchTree(dir)
		}
	})
	return w
}

// realPaths are dirs absolute and by their real paths; as given (absolute) when one does not exist.
func realPaths(dirs []string) []string {
	out := make([]string, len(dirs))
	for i, dir := range dirs {
		if resolved, err := realpath(dir); err == nil {
			out[i] = resolved
		} else if absolute, err := filepath.Abs(dir); err == nil {
			out[i] = absolute
		} else {
			out[i] = dir
		}
	}
	return out
}

// marks is the state now.
func (w *pluginWatcher) marks() PluginsState {
	dirty := make([]string, 0, len(w.dirty))
	for name := range w.dirty {
		dirty = append(dirty, name)
	}
	install := make([]string, 0, len(w.install))
	for name := range w.install {
		install = append(install, name)
	}
	slices.SortFunc(dirty, compareUTF16)
	slices.SortFunc(install, compareUTF16)
	return PluginsState{Dirty: dirty, Install: install, Set: w.set > 0, Base: w.baseChanged}
}

// Value is the marks as a JSON object, the same one until the next change.
func (w *pluginWatcher) Value() (delta.JsonValue, error) {
	if w.value == nil {
		w.value = w.marks().json()
	}
	return w.value, nil
}

// Deliver gives the watches the marks as they are now, as one frame from whatever they had.
func (w *pluginWatcher) Deliver(ctx context.Context) error {
	value, _ := w.Value()
	w.Publish(value, []delta.Op{{"r", value}}, false, ctx)
	return nil
}

// needsDiscovery reports whether the next build discovers again: the plugin set or a plugin's `package.json` changed.
func (w *pluginWatcher) needsDiscovery() bool { return w.set > 0 || w.manifest > 0 }

// discoverySnapshot is what a discovery that starts now has seen.
func (w *pluginWatcher) discoverySnapshot() DiscoverySnapshot {
	return DiscoverySnapshot{Set: w.set, Manifest: w.manifest}
}

// know is the plugins discovery found: marks are for them (a removed one's install clears); the changes it had seen clear.
func (w *pluginWatcher) know(discovery Discovery, seen DiscoverySnapshot) {
	w.discovery = discovery
	if w.set == seen.Set {
		w.set = 0
	}
	if w.manifest == seen.Manifest {
		w.manifest = 0
	}
	// A removed plugin has nothing to install (its directory went, its package.json with it).
	for name := range w.install {
		if !slices.ContainsFunc(discovery.Plugins, func(plugin Plugin) bool { return plugin.Name == name }) {
			delete(w.install, name)
		}
	}
	// A plugin directory linked from outside the roots: a tree of its own (a symlink is not followed).
	var linked []string
	for _, plugin := range discovery.Plugins {
		if !slices.ContainsFunc(w.roots, func(root string) bool { return inside(root, plugin.Dir) }) {
			linked = append(linked, plugin.Dir)
		}
	}
	for _, dir := range linked {
		w.watchTree(dir)
	}
	for tree := range w.trees {
		if !slices.Contains(w.roots, tree) && !slices.Contains(w.baseDirs, tree) && !slices.Contains(linked, tree) {
			w.unwatchTree(tree)
		}
	}
	w.marksChanged()
}

// used is what built facets read: a later change of one of these files dirties their plugins.
func (w *pluginWatcher) used(built []BuiltFacet) {
	for _, facet := range built {
		if facet.Kind != BuiltFacetScript {
			continue
		}
		files := make(map[string]struct{}, len(facet.Inputs)+len(facet.Copied))
		for _, input := range facet.Inputs {
			files[input.File] = struct{}{}
		}
		for _, file := range facet.Copied {
			files[file] = struct{}{}
		}
		w.usedBy[facet.Plugin+"\x00"+facet.Facet] = usedFiles{plugin: facet.Plugin, files: files}
	}
}

// snapshot is the marks a build of names starts from: each one's latest change.
func (w *pluginWatcher) snapshot(names []string) MarkSnapshot {
	snapshot := MarkSnapshot{Plugins: make(map[string]int, len(names)), Names: make([]string, 0, len(names))}
	for _, name := range names {
		if _, ok := snapshot.Plugins[name]; !ok {
			snapshot.Names = append(snapshot.Names, name)
		}
		snapshot.Plugins[name] = w.dirty[name]
	}
	return snapshot
}

// applied is names were applied everywhere: their marks clear unless a change came after snapshot.
func (w *pluginWatcher) applied(names []string, snapshot MarkSnapshot) {
	for _, name := range names {
		if latest, ok := w.dirty[name]; ok && latest <= snapshot.Plugins[name] {
			delete(w.dirty, name)
		}
	}
	w.marksChanged()
}

// installed is plugin name's dependencies were installed: its install mark clears and it is dirty (its packages changed).
func (w *pluginWatcher) installed(name string) {
	delete(w.install, name)
	w.dirty[name] = w.nextGeneration()
	w.marksChanged()
}

// nextGeneration is `this.#generation++`.
func (w *pluginWatcher) nextGeneration() int {
	generation := w.generation
	w.generation++
	return generation
}

// mark marks what a change of the file or directory at path (absolute) asks for.
func (w *pluginWatcher) mark(path string) {
	if slices.ContainsFunc(strings.Split(path, string(filepath.Separator)), isIgnored) {
		return
	}
	if slices.ContainsFunc(w.baseDirs, func(dir string) bool { return inside(dir, path) }) {
		w.baseChanged = true
	} else {
		w.markPlugin(path, w.nextGeneration())
	}
	w.marksChanged()
}

// close stops watching. It does not wait for the goroutine that reads the events (join does).
func (w *pluginWatcher) close() {
	w.closed = true
	if w.fs != nil {
		watcher := w.fs
		w.fs = nil
		// It ends the goroutine reading its events; nothing it sends is waited for.
		_ = watcher.Close()
	}
	clear(w.watched)
	clear(w.trees)
}

// join waits for the goroutine that read the file events to end after close: it ends when the events do. It must not be
// called in a section of the host, which the goroutine may be waiting to enter.
func (w *pluginWatcher) join() { w.running.Wait() }

// markPlugin handles a change under a root, at generation: in a plugin's directory, it dirties that plugin (and asks for
// an install and a new reading of its metadata for its `package.json`, for discovery for a facet file at its top);
// elsewhere under a root (a directory added or removed, a directory that is no plugin yet), discovery; every plugin whose
// facets read the file is dirty too.
func (w *pluginWatcher) markPlugin(path string, generation int) {
	index := slices.IndexFunc(w.discovery.Plugins, func(each Plugin) bool { return each.Dir == path || inside(each.Dir, path) })
	if index < 0 {
		if slices.ContainsFunc(w.roots, func(root string) bool { return inside(root, path) }) {
			w.set = generation
		}
	} else {
		plugin := w.discovery.Plugins[index]
		if path == filepath.Join(plugin.Dir, "package.json") {
			w.install[plugin.Name] = struct{}{}
			w.manifest = generation
		} else {
			if path == plugin.Dir || (filepath.Dir(path) == plugin.Dir && w.isFacetFile(filepath.Base(path))) {
				w.set = generation
			}
			w.dirty[plugin.Name] = generation
		}
	}
	for _, user := range w.usedBy {
		if _, ok := user.files[path]; ok {
			w.dirty[user.plugin] = generation
		}
	}
}

// isFacetFile reports whether file (at a plugin's top) is named as a facet file of one of the app's kinds.
func (w *pluginWatcher) isFacetFile(file string) bool {
	return slices.ContainsFunc(w.options.Kinds, func(kind string) bool {
		return slices.ContainsFunc(facetExtensions, func(extension string) bool { return file == kind+extension })
	})
}

// watchTree watches the tree at dir (once): it and every directory under it but ignored ones; a missing one is skipped.
func (w *pluginWatcher) watchTree(dir string) {
	if _, ok := w.trees[dir]; ok {
		return
	}
	w.trees[dir] = struct{}{}
	w.watchDirs(dir)
}

// unwatchTree stops watching the tree at dir.
func (w *pluginWatcher) unwatchTree(dir string) {
	delete(w.trees, dir)
	w.unwatchDirs(dir)
}

// watchDirs watches dir and, walking down, every directory under it but ignored ones (each not watched yet).
func (w *pluginWatcher) watchDirs(dir string) {
	if _, ok := w.watched[dir]; ok || w.reported || w.closed {
		return
	}
	if err := w.addWatch(dir); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			w.failed(err)
		}
		return
	}
	w.watched[dir] = struct{}{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Removed meanwhile: its parent's event says so.
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && !isIgnored(entry.Name()) {
			w.watchDirs(filepath.Join(dir, entry.Name()))
		}
	}
}

// addWatch puts a watch on dir, starting the watcher and the goroutine that reads its events with the first.
func (w *pluginWatcher) addWatch(dir string) error {
	if w.fs == nil {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			return err
		}
		w.fs = watcher
		//portlint:allow golifetime the goroutine ends when close closes the watcher's channels, and join waits for it
		w.running.Go(func() { w.readEvents(watcher) })
	}
	return w.fs.Add(dir)
}

// readEvents is the goroutine of a watcher: each event or error enters the host as a section.
func (w *pluginWatcher) readEvents(watcher *fsnotify.Watcher) {
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			w.Batch().Section(func() { w.event(watcher, event) })
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			w.Batch().Section(func() {
				if w.fs == watcher {
					w.failed(err)
				}
			})
		}
	}
}

// event handles an event of the watch on watcher's directories, unless the watcher was closed since.
func (w *pluginWatcher) event(watcher *fsnotify.Watcher, event fsnotify.Event) {
	if w.fs != watcher {
		return
	}
	w.changedIn(event.Name)
}

// changedIn handles an event for path in a watched directory: a new directory gets its watches, a removed one loses them;
// marked.
func (w *pluginWatcher) changedIn(path string) {
	if !isIgnored(filepath.Base(path)) {
		if isDirectory(path) {
			w.watchDirs(path)
		} else if _, ok := w.watched[path]; ok {
			w.unwatchDirs(path)
		}
	}
	w.mark(path)
}

// unwatchDirs stops watching dir and the directories under it (it was removed, or its tree is not watched any more).
func (w *pluginWatcher) unwatchDirs(dir string) {
	for watched := range w.watched {
		if watched != dir && !inside(dir, watched) {
			continue
		}
		if w.fs != nil {
			// A removed directory's watch went with it.
			_ = w.fs.Remove(watched)
		}
		delete(w.watched, watched)
	}
}

// failed is watching failed: reported once; every watch stops (the app builds on demand).
func (w *pluginWatcher) failed(err error) {
	w.close()
	if w.reported {
		return
	}
	w.reported = true
	w.options.Failed(err)
}

// marksChanged is the marks changed: one publication per microtask turn while someone watches or derives from them.
func (w *pluginWatcher) marksChanged() {
	w.value = nil
	if w.scheduled || (!w.Watched() && w.DependentSet().Len() == 0) {
		return
	}
	w.scheduled = true
	w.Batch().Soon(func() {
		w.scheduled = false
		w.Changed()
	}, chordctx.Background())
}

// isDirectory reports whether path is a directory, never following a link (as the scan of a tree does not); one that cannot
// be read (gone, a link loop, no access) is none: an event handler must not fail.
func isDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}
