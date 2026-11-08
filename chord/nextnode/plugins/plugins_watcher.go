package plugins

import (
	"context"
	"errors"
	"io/fs"
	"maps"
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

// Ports packages/chord/src/next-node/plugins/watcher.ts. The watcher and its marks (slice 7c §5, slice 12 §4): a watch on
// every directory of the plugin roots and the app's base code, `node_modules` and `.git` never watched (a directory
// appearing later gets its watch), marks what changed and never applies it. A changed file dirties every plugin whose
// facets used it (a copied contract every user), whose directory contains it or which is a single-file plugin of it; a
// changed `package.json` asks for an install (and for its metadata to be read again); a plugin directory, a facet file or a
// single-file plugin's file added or removed asks for discovery again. Base code is compared, not latched: `base` (the app
// restarts its hosts) is true exactly while a file differs from what a running base artifact read (its bytes' hash: the
// server's own code hashed at start, each base bundle's record), is gone, or is a source file new in a base directory;
// checked once when its watch starts and again at each event, the reads of one path in order. The marks are a JSON watchable;
// each plugin mark records the generation of its latest change (generations only increase), so a change during a build keeps
// its mark, however late an older build's rollout clears.
//
// Upstream's base comparisons are promise chains on the event loop; here each path's read is one step of a small pool of
// goroutines (reads are bounded, never one per recorded file), a step's result entering the host as a section. A path
// asked to be read again while it is being read is read once more after it: the latest read decides.
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

// sourceExtensions are the files of a base directory that are code: one added there marks `base` (a swap file or a note does
// not).
var sourceExtensions = map[string]struct{}{
	".ts": {}, ".tsx": {}, ".mts": {}, ".cts": {}, ".js": {}, ".jsx": {}, ".mjs": {}, ".cjs": {}, ".json": {}, ".css": {},
}

// maxComparers bounds the goroutines reading base files at once.
const maxComparers = 8

// maxEventBurst bounds the file events one section of the host handles.
const maxEventBurst = 256

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
	options watcherOptions
	roots   []string
	// baseDirs are the base directories (watched as trees): a source file new in one marks `base`.
	baseDirs []string
	// records is what each running base artifact read: each file's bytes' hash, by real path (the server's own first).
	records []BaseInputs
	// recorded is every file a record names.
	recorded map[string]struct{}
	// differing is the base files that differ now (changed, gone, or new in a base directory): `base` while there is one.
	differing map[string]struct{}
	// comparing is the base files being compared (queued or read), and queue the ones not yet taken by a goroutine.
	comparing map[string]*comparison
	queue     []string
	// comparers is the goroutines reading base files now.
	comparers int
	// locations is where base code lives: the base directories and the directories of recorded files outside them.
	locations map[string]struct{}
	// anchored is the locations and every ancestor of theirs: an entry there created, removed or moved concerns base code.
	anchored map[string]struct{}
	// baseWatched is the directories watched alone for base code: the recorded files' directories outside the base
	// directories, and every existing ancestor of a location (its replacement or move is an event of its parent's watch).
	baseWatched map[string]struct{}
	// renamed is the entries on the way to base code replaced since the last renewal: one renewal per burst of events.
	renamed map[string]struct{}
	closed  bool
	// trees are the trees it watches: the roots, the base code, and plugin directories linked from outside the roots.
	trees map[string]struct{}
	// watched is its watches, one per directory of its trees.
	watched map[string]struct{}
	// fs is the one fsnotify watcher the watches are on, made with the first (nil before it and after close); running joins
	// the goroutine that reads its events.
	fs      *fsnotify.Watcher
	running sync.WaitGroup
	// reported is set once a failure to watch was reported.
	reported bool
	// generation is the one the next change gets: every mark records a newer one than any before it.
	generation int
	// dirty is each dirty plugin's latest change, by generation.
	dirty   map[string]int
	install map[string]struct{}
	// set is the latest change of the plugin set since the last discovery (0: none).
	set int
	// manifest is the latest change of a known plugin's `package.json` since the last discovery (0: none).
	manifest  int
	discovery Discovery
	// usedBy is the files each built facet read (its inputs and copies), by plugin and kind.
	usedBy    map[string]usedFiles
	value     delta.JsonValue
	scheduled bool
}

var _ state.SourceImpl = (*pluginWatcher)(nil)

// newPluginWatcher starts watching the roots and the base code of options, on batch.
func newPluginWatcher(options watcherOptions, batch *state.Batch) *pluginWatcher {
	w := &pluginWatcher{
		options:     options,
		trees:       map[string]struct{}{},
		watched:     map[string]struct{}{},
		recorded:    map[string]struct{}{},
		differing:   map[string]struct{}{},
		comparing:   map[string]*comparison{},
		locations:   map[string]struct{}{},
		anchored:    map[string]struct{}{},
		baseWatched: map[string]struct{}{},
		renamed:     map[string]struct{}{},
		generation:  1,
		dirty:       map[string]int{},
		install:     map[string]struct{}{},
		discovery:   Discovery{Plugins: []Plugin{}, Problems: []DiscoveryProblem{}},
		usedBy:      map[string]usedFiles{},
	}
	w.Init(batch, w)
	// Events name paths under the directories as watched: by their real paths, as discovery names plugins.
	w.roots = realPaths(options.Roots)
	base := realPaths(options.Base)
	for _, path := range base {
		if isDirectory(path) {
			w.baseDirs = append(w.baseDirs, path)
		}
	}
	batch.Section(func() {
		for _, dir := range slices.Concat(w.roots, w.baseDirs) {
			w.watchTree(dir)
		}
		w.recordOwn(base)
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
	return PluginsState{Dirty: dirty, Install: install, Set: w.set > 0, Base: len(w.differing) > 0}
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
	// A plugin whose files moved (a single file became a directory, a facet file came or went) is dirty.
	generation := w.nextGeneration()
	for _, plugin := range discovery.Plugins {
		index := slices.IndexFunc(w.discovery.Plugins, func(each Plugin) bool { return each.Name == plugin.Name })
		if index >= 0 && !SameFiles(w.discovery.Plugins[index], plugin) {
			w.dirty[plugin.Name] = generation
		}
	}
	// A plugin no longer discovered: what its builds read no longer dirties anything.
	for key, user := range w.usedBy {
		if !slices.ContainsFunc(discovery.Plugins, func(plugin Plugin) bool { return plugin.Name == user.plugin }) {
			delete(w.usedBy, key)
		}
	}
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
		if plugin.Layout == LayoutDirectory && !slices.ContainsFunc(w.roots, func(root string) bool { return inside(root, plugin.Dir) }) {
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

// added is plugins were installed into a root: discovery runs at the next build (without waiting for the watch's events).
func (w *pluginWatcher) added() {
	w.set = w.nextGeneration()
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
	if w.isBase(path) {
		w.compareAt(path)
		return
	}
	w.markPlugin(path, w.nextGeneration())
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
	clear(w.baseWatched)
	clear(w.renamed)
	clear(w.trees)
}

// join waits for the goroutine that read the file events to end after close: it ends when the events do. It must not be
// called in a section of the host, which the goroutine may be waiting to enter.
func (w *pluginWatcher) join() { w.running.Wait() }

// markPlugin handles a change under a root, at generation: in a plugin's directory, it dirties that plugin (and asks for
// an install and a new reading of its metadata for its `package.json`, for discovery for a facet file at its top); a
// single-file plugin's file dirties it (and asks for discovery when it is gone); elsewhere under a root (a directory or a
// single-file plugin's file added or removed, a directory that is no plugin yet), discovery; every plugin whose facets read
// the file is dirty too.
func (w *pluginWatcher) markPlugin(path string, generation int) {
	index := slices.IndexFunc(w.discovery.Plugins, func(each Plugin) bool { return owns(each, path) })
	if index < 0 {
		if slices.ContainsFunc(w.roots, func(root string) bool { return inside(root, path) }) {
			w.set = generation
		}
	} else if plugin := w.discovery.Plugins[index]; plugin.Layout == LayoutFile {
		if _, err := os.Stat(path); err != nil {
			w.set = generation
		}
		w.dirty[plugin.Name] = generation
	} else {
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

// owns reports whether path is plugin's: in its directory, or one of a single-file plugin's files.
func owns(plugin Plugin, path string) bool {
	if plugin.Layout == LayoutFile {
		for _, file := range plugin.Facets {
			if file == path {
				return true
			}
		}
		return false
	}
	return plugin.Dir == path || inside(plugin.Dir, path)
}

// isFacetFile reports whether file (at a plugin's top) is named as a facet file of one of the app's kinds.
func (w *pluginWatcher) isFacetFile(file string) bool {
	return slices.ContainsFunc(w.options.Kinds, func(kind string) bool {
		return slices.ContainsFunc(FacetExtensions, func(extension string) bool { return file == kind+extension })
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
	if _, ok := w.watched[dir]; ok || w.reported {
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

// readEvents is the goroutine of a watcher: the events that are queued together enter the host as one section, which then
// renews what the burst replaced.
func (w *pluginWatcher) readEvents(watcher *fsnotify.Watcher) {
	var burst []fsnotify.Event
	for {
		burst = burst[:0]
		//portlint:allow golifetime the loop ends when close closes the fsnotify watcher, which closes both channels
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			burst = append(burst, event)
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			w.Batch().Section(func() {
				if w.fs == watcher {
					w.failed(err)
				}
			})
			continue
		}
	queued:
		for len(burst) < maxEventBurst {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					break queued
				}
				burst = append(burst, event)
			default:
				break queued
			}
		}
		w.Batch().Section(func() {
			for _, event := range burst {
				w.event(watcher, event)
			}
			w.renewRenamed(watcher)
		})
	}
}

// event handles an event of the watch on watcher's directories, unless the watcher was closed since (close sets fs nil).
func (w *pluginWatcher) event(watcher *fsnotify.Watcher, event fsnotify.Event) {
	if w.fs != watcher {
		return
	}
	path := event.Name
	if path == "" {
		// `fs.watch` names no file: nothing to mark.
		return
	}
	_, inTree := w.watched[filepath.Dir(path)]
	_, inBase := w.baseWatched[filepath.Dir(path)]
	// An event of a tree's own directory (removed, moved, its attributes changed) names it by its own base name under it,
	// as `fs.watch` reports an event of a watch with no file name; unless a watch of its parent tells it by its name.
	if _, tree := w.trees[path]; tree && !inTree {
		w.changedIn(filepath.Join(path, filepath.Base(path)), event.Op)
	} else if inTree || !inBase {
		w.changedIn(path, event.Op)
	}
	if inBase {
		w.baseEvent(path, event.Op)
	}
}

// changedIn handles an event for path in a watched directory: a new directory gets its watches (renewed when it was removed
// or moved: its old watches are dead, its inode possibly reused), a removed one loses them; marked.
func (w *pluginWatcher) changedIn(path string, op fsnotify.Op) {
	if !isIgnored(filepath.Base(path)) {
		_, watched := w.watched[path]
		if watched && op&(fsnotify.Remove|fsnotify.Rename) != 0 {
			w.unwatchDirs(path)
			watched = false
		}
		if isDirectory(path) {
			w.watchDirs(path)
		} else if watched {
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
		delete(w.watched, watched)
		w.releaseWatch(watched)
	}
}

// releaseWatch removes the watch on dir unless the tree watches or the base watches still want it (a removed directory's
// watch went with it).
func (w *pluginWatcher) releaseWatch(dir string) {
	if w.fs == nil {
		return
	}
	if _, ok := w.watched[dir]; ok {
		return
	}
	if _, ok := w.baseWatched[dir]; ok {
		return
	}
	_ = w.fs.Remove(dir)
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

// comparison is a base file being compared: queued until a goroutine takes it, then read.
type comparison struct {
	// again is set when it was asked for once more since it was queued: it is read once more after the read under way, as
	// upstream's reads of one path run in order, each one's result applied.
	again bool
}

// recordOwn records the server's own base code (files and the source files of directories), after their watches: the bytes
// Node loaded, as far as a read now can tell (an edit between the load and this read is not seen).
func (w *pluginWatcher) recordOwn(base []string) {
	var files []string
	for _, path := range base {
		if slices.Contains(w.baseDirs, path) {
			files = append(files, sourceFiles(path)...)
		} else {
			files = append(files, path)
		}
	}
	for _, dir := range w.baseDirs {
		w.locate(dir)
	}
	for _, file := range files {
		w.locate(filepath.Dir(file))
	}
	record := BaseInputs{}
	for _, file := range files {
		if hash, ok := hashFile(file); ok {
			record[file] = hash
		}
	}
	w.records = append(w.records, record)
	for path := range record {
		w.recorded[path] = struct{}{}
	}
}

// recordBase adds the record of a running base artifact (a base bundle's inputs): `base` is set while one of its files differs
// from it. Each file's directory is watched alone (not its tree), then every file is compared once (an edit since the build
// has no later event); files under ignored directories are skipped.
func (w *pluginWatcher) recordBase(inputs BaseInputs) {
	record := make(BaseInputs, len(inputs))
	for file, hash := range inputs {
		path := realPaths([]string{file})[0]
		if !hasIgnoredPart(path) {
			record[path] = hash
		}
	}
	for path := range record {
		w.locate(filepath.Dir(path))
	}
	w.records = append(w.records, record)
	for path := range record {
		w.recorded[path] = struct{}{}
		w.compare(path)
	}
}

// hasIgnoredPart reports whether path lies under a directory never watched (`node_modules`, `.git`).
func hasIgnoredPart(path string) bool {
	return slices.ContainsFunc(strings.Split(path, string(filepath.Separator)), isIgnored)
}

// under reports whether path lies strictly under dir; both are absolute and clean (real paths).
func under(dir, path string) bool {
	if dir == "" || len(path) <= len(dir) || !strings.HasPrefix(path, dir) {
		return false
	}
	return os.IsPathSeparator(dir[len(dir)-1]) || os.IsPathSeparator(path[len(dir)])
}

// isBase reports whether a change at path concerns base code: a recorded or differing file, or anything in a base directory.
func (w *pluginWatcher) isBase(path string) bool {
	if _, ok := w.recorded[path]; ok {
		return true
	}
	if _, ok := w.differing[path]; ok || w.inBaseDir(path) {
		return true
	}
	for file := range w.recorded {
		if under(path, file) {
			return true
		}
	}
	return false
}

// inBaseDir reports whether path is a base directory or in one.
func (w *pluginWatcher) inBaseDir(path string) bool {
	return slices.ContainsFunc(w.baseDirs, func(dir string) bool { return dir == path || under(dir, path) })
}

// compareAt compares the base files at or under path again: the recorded and differing ones, and the source files now in the
// base directories there (a directory added, removed or moved names only itself).
func (w *pluginWatcher) compareAt(path string) {
	paths := map[string]struct{}{}
	_, recorded := w.recorded[path]
	_, differing := w.differing[path]
	if recorded || differing {
		paths[path] = struct{}{}
	} else {
		for file := range w.recorded {
			if under(path, file) {
				paths[file] = struct{}{}
			}
		}
		for file := range w.differing {
			if under(path, file) {
				paths[file] = struct{}{}
			}
		}
		for _, dir := range w.baseDirs {
			scanned := ""
			switch {
			case dir == path || under(path, dir):
				scanned = dir
			case under(dir, path):
				scanned = path
			}
			if scanned != "" {
				for _, file := range sourceFiles(scanned) {
					paths[file] = struct{}{}
				}
			}
		}
	}
	for file := range paths {
		w.compare(file)
	}
}

// compare compares the base file at path once the comparison under way of it is done: the reads of one path run in order,
// and the latest decides.
func (w *pluginWatcher) compare(path string) {
	if running, ok := w.comparing[path]; ok {
		running.again = true
		return
	}
	w.comparing[path] = &comparison{}
	w.queue = append(w.queue, path)
	if w.comparers < maxComparers {
		w.comparers++
		//portlint:allow golifetime the goroutine ends when the queue is empty, and join waits for it
		w.running.Go(w.compareQueued)
	}
}

// nextComparison takes the next queued path for a goroutine, or ends the goroutine (nil) when none is. It runs in a section.
func (w *pluginWatcher) nextComparison() (string, *comparison) {
	if len(w.queue) == 0 {
		w.comparers--
		return "", nil
	}
	path := w.queue[0]
	w.queue = w.queue[1:]
	return path, w.comparing[path]
}

// compareQueued is a goroutine of the comparers: it reads the queued base files, one path at a time, and gives each hash to the
// host as a section.
func (w *pluginWatcher) compareQueued() {
	var path string
	var current *comparison
	w.Batch().Section(func() { path, current = w.nextComparison() })
	for current != nil {
		hash, ok := hashFile(path)
		w.Batch().Section(func() {
			w.applyComparison(path, hash, ok)
			if current.again {
				current.again = false
				return
			}
			delete(w.comparing, path)
			path, current = w.nextComparison()
		})
	}
}

// applyComparison is what the bytes of the file at path (readable: it could be read) decide: it differs while any record has
// other bytes for it (or it is gone), or, recorded by none, while it is a source file in a base directory.
func (w *pluginWatcher) applyComparison(path, hash string, readable bool) {
	recorded, differs := false, false
	for _, record := range w.records {
		if want, ok := record[path]; ok {
			recorded = true
			differs = differs || !readable || want != hash
		}
	}
	if !recorded {
		_, source := sourceExtensions[extname(path)]
		differs = readable && source && w.inBaseDir(path)
	}
	if _, was := w.differing[path]; was == differs {
		return
	}
	if differs {
		w.differing[path] = struct{}{}
	} else {
		delete(w.differing, path)
	}
	w.marksChanged()
}

// locate adds dir to where base code lives (a base directory, or one holding recorded files outside them): it and its
// ancestors are watched. A directory inside a base directory is its tree's.
func (w *pluginWatcher) locate(dir string) {
	if _, ok := w.locations[dir]; ok {
		return
	}
	if w.inBaseDir(dir) && !slices.Contains(w.baseDirs, dir) {
		return
	}
	w.locations[dir] = struct{}{}
	for each := dir; ; each = filepath.Dir(each) {
		w.anchored[each] = struct{}{}
		if each == filepath.Dir(each) {
			break
		}
	}
	w.watchLocation(dir)
}

// watchLocation watches location dir's existing ancestors alone, then dir itself: as a tree for a base directory, alone for
// one holding recorded files. A part that does not exist is skipped: its parent's watch reports its arrival.
func (w *pluginWatcher) watchLocation(dir string) {
	var ancestors []string
	for each := filepath.Dir(dir); ; each = filepath.Dir(each) {
		ancestors = append(ancestors, each)
		if each == filepath.Dir(each) {
			break
		}
	}
	for _, ancestor := range slices.Backward(ancestors) {
		w.watchAlone(ancestor)
	}
	if slices.Contains(w.baseDirs, dir) {
		w.watchDirs(dir)
	} else {
		w.watchAlone(dir)
	}
}

// watchAlone watches directory dir alone (once) for base code: a recorded (or differing) file there is compared; an entry on
// the way to a location created, removed or moved renews the watches under it. A directory that does not exist or cannot be
// watched (an ancestor without access) is skipped.
func (w *pluginWatcher) watchAlone(dir string) {
	if _, ok := w.baseWatched[dir]; ok || w.reported {
		return
	}
	if err := w.addWatch(dir); err != nil {
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrPermission) {
			w.failed(err)
		}
		return
	}
	w.baseWatched[dir] = struct{}{}
}

// baseEvent handles an event for path in a directory watched alone: a recorded (or differing) file there is compared; an
// entry on the way to base code created, removed or moved is renewed after this burst of events (an attribute change is no
// replacement).
func (w *pluginWatcher) baseEvent(path string, op fsnotify.Op) {
	if op == fsnotify.Chmod {
		return
	}
	_, recorded := w.recorded[path]
	_, differing := w.differing[path]
	if recorded || differing {
		w.mark(path)
		return
	}
	if _, anchored := w.anchored[path]; anchored && op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
		w.renamed[path] = struct{}{}
	}
}

// renewRenamed renews the watches under the entries on the way to base code replaced in this burst of events (the outermost
// renamed path covers those under it).
func (w *pluginWatcher) renewRenamed(watcher *fsnotify.Watcher) {
	if len(w.renamed) == 0 {
		return
	}
	renamed := slices.Collect(maps.Keys(w.renamed))
	clear(w.renamed)
	if w.closed || w.reported || w.fs != watcher {
		return
	}
	for _, path := range renamed {
		if !slices.ContainsFunc(renamed, func(other string) bool { return under(other, path) }) {
			w.reconcile(path)
		}
	}
}

// reconcile renews the watches at and under path, an entry on the way to base code that was created, removed, moved or
// replaced (its watches may be dead, its inode reused): every base watch there starts again where the paths now are, and the
// base files there are compared.
func (w *pluginWatcher) reconcile(path string) {
	for dir := range w.baseWatched {
		if dir == path || under(path, dir) {
			delete(w.baseWatched, dir)
			w.releaseWatch(dir)
		}
	}
	for _, dir := range w.baseDirs {
		if dir == path || under(path, dir) {
			w.unwatchDirs(dir)
		}
	}
	for dir := range w.locations {
		if dir == path || under(path, dir) {
			w.watchLocation(dir)
		}
	}
	w.compareAt(path)
}

// sourceFiles are the source files at path: itself when it is one, every one under it (ignored directories skipped) when a
// directory.
func sourceFiles(path string) []string {
	if !isDirectory(path) {
		if _, ok := sourceExtensions[extname(path)]; ok {
			return []string{path}
		}
		return nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		if !isIgnored(entry.Name()) {
			files = append(files, sourceFiles(filepath.Join(path, entry.Name()))...)
		}
	}
	return files
}
