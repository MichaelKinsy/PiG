package node

// Ports packages/durable/src/env/node-watch.ts
//
// Go mapping: upstream runs on one event loop; here one goroutine at a time owns the snapshot (the opener, then the
// running flush) and the mutex guards the state that event and timer callbacks share. fsnotify has no recursive
// watcher, so a recursive target gets one watch per directory below it, as upstream does on Linux. Its kqueue and
// inotify registrations are synchronous, so the rescan upstream schedules on macOS after installing watchers
// (FSEvents misses changes made right after fs.watch returns) has no counterpart.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// nodeTimerDelay is the wait of setTimeout(callback, ms): Node runs a delay below 1 or above 2^31-1 after 1 ms.
func nodeTimerDelay(ms int) time.Duration {
	if ms < 1 || ms > math.MaxInt32 {
		ms = 1
	}
	return time.Duration(ms) * time.Millisecond
}

// NodeWatchOptions is how NodeExecutionEnv watches.
type NodeWatchOptions struct {
	// Mode forces a mode. By default polling is chosen on Windows, where native watchers keep directories open and so
	// block renaming their parents, and for file systems that do not report remote changes.
	Mode durableenv.WatchMode
	// PollIntervalMs is the interval between snapshots in polling mode; nil is the default 2000 ms. Like a Node timer delay,
	// a value below 1 or above 2^31-1 waits 1 ms.
	PollIntervalMs *int
	// MaxDirectories is the most directories one watcher covers; nil is the default 10,000. Any set value is the limit, so
	// 0 refuses every directory.
	MaxDirectories *int
}

const (
	debounce         = 50 * time.Millisecond
	defaultPoll      = 2000 * time.Millisecond
	defaultMaxDirs   = 10_000
	syncRounds       = 10
	hashMaxBytes     = 256 * 1024
	hashRecentWindow = 5000 * time.Millisecond
)

// unreliableFilesystems are the Linux statfs magic numbers of file systems that accept watches but do not report
// changes made elsewhere.
var unreliableFilesystems = map[uint32]bool{
	0x6969:     true, // NFS
	0x517b:     true, // SMB
	0xff534d42: true, // CIFS
	0xfe534d42: true, // SMB2
	0x65735546: true, // FUSE (sshfs, Android shared storage)
	0x01021997: true, // 9P (WSL2 Windows drives)
	0x0bd00bd0: true, // Lustre
	0x47504653: true, // GPFS
	0x00c36400: true, // Ceph
	0x5346414f: true, // OpenAFS
	0x6b414653: true, // kAFS
	0x5dca2df5: true, // sdcardfs
}

type resolvedTarget struct {
	path      string
	recursive bool
	hidden    bool
	names     map[string]bool
}

// entryKind is what a snapshot remembers of a path's type.
type entryKind int

const (
	kindOther entryKind = iota
	kindFile
	kindDirectory
	kindSymlink
)

// snapshotEntry is what a snapshot remembers of one path. Directories and ancestors are compared by identity only.
type snapshotEntry struct {
	kind    entryKind
	dev     uint64
	ino     uint64
	size    int64
	mtimeMs float64
	hash    string
}

type snapshot map[string]snapshotEntry

// installedWatch is the identity of what a native watcher was installed on. alias is set for a target that is a
// symbolic link to a file: the path with every link resolved, which names that watcher's events where fsnotify follows
// the link itself (kqueue on macOS and the BSDs).
type installedWatch struct {
	dev, ino uint64
	alias    string
}

// scanResult is a scan: the snapshot, and targets that are symbolic links to files, whose files need their own watchers.
type scanResult struct {
	snapshot    snapshot
	linkedFiles map[string]bool
}

// isDenied reports a failure for lack of permission.
func isDenied(err error) bool {
	code := errnoCode(err)
	return code == "EACCES" || code == "EPERM"
}

type budgetExceeded struct{ limit int }

func (err budgetExceeded) Error() string {
	return fmt.Sprintf("Watched paths exceed %d directories", err.limit)
}

func entryOf(path string, info fs.FileInfo, follow bool, hash string) snapshotEntry {
	var kind entryKind
	switch mode := info.Mode(); {
	case mode.IsRegular():
		kind = kindFile
	case mode.IsDir():
		kind = kindDirectory
	case mode&fs.ModeSymlink != 0:
		kind = kindSymlink
	}
	dev, ino := fileIdentity(path, info, follow)
	entry := snapshotEntry{kind: kind, dev: dev, ino: ino, hash: hash}
	if kind != kindDirectory {
		entry.size = info.Size()
		entry.mtimeMs = float64(info.ModTime().UnixNano()) / 1e6
	}
	return entry
}

// ancestorsOf lists the ancestors of path, nearest first, up to the root.
func ancestorsOf(path string) []string {
	var result []string
	for current := filepath.Dir(path); ; current = filepath.Dir(current) {
		result = append(result, current)
		if filepath.Dir(current) == current {
			return result
		}
	}
}

// isWithin reports whether path is ancestor or below it.
func isWithin(path, ancestor string) bool {
	if path == ancestor {
		return true
	}
	prefix := ancestor
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(path, prefix)
}

func (target resolvedTarget) excludes(name string) bool {
	return (target.hidden && strings.HasPrefix(name, ".")) || target.names[name]
}

// compareUTF16 orders strings by UTF-16 code unit, as JavaScript's default sort does.
func compareUTF16(a, b string) int {
	return slices.Compare(jsstring.ToUTF16(a), jsstring.ToUTF16(b))
}

// nodeFileWatcher watches by snapshots: native events only trigger a debounced rescan, and changes are the difference
// between snapshots plus the event paths. A replaced file, a renamed or recreated ancestor, or a directory created
// with its contents therefore never depends on which events an operating system sends. New directories get their
// watchers before they are scanned again, so nothing written into them before the watcher existed is missed.
type nodeFileWatcher struct {
	targets        []resolvedTarget
	onChange       func(durableenv.WatchChange)
	pollInterval   time.Duration
	maxDirectories int

	// snapshot is owned by the opener, then by the running flush.
	snapshot snapshot

	// deliverMu is held while a change is delivered, so a delivery never starts after Close has set closed.
	deliverMu sync.Mutex

	mu   sync.Mutex
	mode durableenv.WatchMode
	fsw  *fsnotify.Watcher
	// watched is the identity of what each native watcher was installed on: a path replaced by another file or directory needs a new one.
	watched map[string]installedWatch
	// linkAliases maps the resolved path of each watched symbolic-link target to that target, so an event named by the
	// file the link points to reports the target, as upstream's #onLinkedFileEvent does whatever the event names.
	linkAliases map[string]string
	events      map[string]bool
	timer       *time.Timer
	running     chan struct{}
	dirty       bool
	closed      bool
}

var _ durableenv.FileWatcher = (*nodeFileWatcher)(nil)

// Watch reports changes to the targets; see FileSystem.Watch. Coverage is established when it returns: the watchers
// come first, then the snapshot later changes are compared with.
func (env *NodeExecutionEnv) Watch(ctx context.Context, targets []durableenv.WatchTarget, onChange func(durableenv.WatchChange)) (durableenv.FileWatcher, error) {
	if err := abortedFileError(ctx, ""); err != nil {
		return nil, err
	}
	cwd := env.Cwd()
	watcher, err := openFileWatcher(targets, func(path string) string { return resolvePath(cwd, path) }, onChange, env.watch)
	if err != nil {
		return nil, toFileError(err, fsCall{})
	}
	if abortErr := abortedFileError(ctx, ""); abortErr != nil {
		_ = watcher.Close(ctx)
		return nil, abortErr
	}
	return watcher, nil
}

func openFileWatcher(targets []durableenv.WatchTarget, resolve func(string) string, onChange func(durableenv.WatchChange), options NodeWatchOptions) (*nodeFileWatcher, error) {
	resolved := make([]resolvedTarget, len(targets))
	paths := make([]string, len(targets))
	for index, target := range targets {
		resolved[index] = resolvedTarget{path: resolve(target.Path), recursive: target.Recursive, names: map[string]bool{}}
		if target.Exclude != nil {
			resolved[index].hidden = target.Exclude.Hidden
			for _, name := range target.Exclude.Names {
				resolved[index].names[name] = true
			}
		}
		paths[index] = resolved[index].path
	}
	mode := options.Mode
	if mode == "" {
		// Windows refuses to rename a directory while another directory below it is open, and a native watcher keeps
		// every watched directory open: watching would break renames of their parents, so Windows polls.
		mode = durableenv.WatchNative
		if runtime.GOOS == "windows" || anyUnreliable(paths) {
			mode = durableenv.WatchPolling
		}
	}
	watcher := &nodeFileWatcher{
		targets:        resolved,
		onChange:       onChange,
		mode:           mode,
		pollInterval:   defaultPoll,
		maxDirectories: defaultMaxDirs,
		snapshot:       snapshot{},
		watched:        map[string]installedWatch{},
		linkAliases:    map[string]string{},
		events:         map[string]bool{},
	}
	if options.PollIntervalMs != nil {
		watcher.pollInterval = nodeTimerDelay(*options.PollIntervalMs)
	}
	if options.MaxDirectories != nil {
		watcher.maxDirectories = *options.MaxDirectories
	}
	if mode == durableenv.WatchNative {
		fsw, err := fsnotify.NewWatcher()
		if err != nil {
			// Out of watch instances: compare snapshots from now on.
			watcher.mode = durableenv.WatchPolling
		} else {
			watcher.fsw = fsw
			go watcher.pumpEvents(fsw)
		}
	}
	// A rescan an event triggers while the first snapshot is taken waits for it: one goroutine owns the snapshot.
	opening := make(chan struct{})
	watcher.running = opening
	_, err := watcher.sync(false)
	watcher.mu.Lock()
	watcher.running = nil
	rescan := watcher.dirty || len(watcher.events) > 0
	watcher.mu.Unlock()
	close(opening)
	if err != nil {
		watcher.stop()
		if _, ok := errors.AsType[budgetExceeded](err); ok {
			return nil, durableenv.NewFileError(durableenv.FileErrorInvalid, err.Error(), "", nil)
		}
		return nil, err
	}
	if rescan {
		watcher.flush()
	}
	watcher.schedulePoll()
	return watcher, nil
}

func (watcher *nodeFileWatcher) Mode() durableenv.WatchMode {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	return watcher.mode
}

// Close stops watching; no onChange call starts after it returns. It waits for a rescan in progress, except while a
// change is being delivered, so Close may be called from onChange.
func (watcher *nodeFileWatcher) Close(context.Context) error {
	watcher.mu.Lock()
	if watcher.closed {
		watcher.mu.Unlock()
		return nil
	}
	watcher.mu.Unlock()
	watcher.stop()
	if !watcher.deliverMu.TryLock() {
		return nil
	}
	watcher.deliverMu.Unlock()
	watcher.mu.Lock()
	running := watcher.running
	watcher.mu.Unlock()
	if running != nil {
		<-running
	}
	return nil
}

func (watcher *nodeFileWatcher) stop() {
	watcher.mu.Lock()
	watcher.closed = true
	if watcher.timer != nil {
		watcher.timer.Stop()
		watcher.timer = nil
	}
	fsw := watcher.fsw
	watcher.fsw = nil
	clear(watcher.watched)
	clear(watcher.linkAliases)
	watcher.mu.Unlock()
	if fsw != nil {
		_ = fsw.Close()
	}
}

// deliver calls onChange unless the watcher is closed; a panic in the callback must not stop watching.
func (watcher *nodeFileWatcher) deliver(change durableenv.WatchChange) {
	watcher.deliverMu.Lock()
	defer watcher.deliverMu.Unlock()
	watcher.mu.Lock()
	closed := watcher.closed
	watcher.mu.Unlock()
	if closed {
		return
	}
	defer func() { _ = recover() }()
	watcher.onChange(change)
}

func (watcher *nodeFileWatcher) schedulePoll() {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	if watcher.closed || watcher.mode != durableenv.WatchPolling || watcher.timer != nil {
		return
	}
	watcher.timer = time.AfterFunc(watcher.pollInterval, func() {
		watcher.mu.Lock()
		watcher.timer = nil
		watcher.mu.Unlock()
		<-watcher.flush()
		watcher.schedulePoll()
	})
}

// scheduleFlushLocked arms the debounce timer; the caller holds mu.
func (watcher *nodeFileWatcher) scheduleFlushLocked() {
	if watcher.closed || watcher.timer != nil {
		return
	}
	watcher.timer = time.AfterFunc(debounce, func() {
		watcher.mu.Lock()
		watcher.timer = nil
		watcher.mu.Unlock()
		watcher.flush()
	})
}

func (watcher *nodeFileWatcher) scheduleFlush() {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	watcher.scheduleFlushLocked()
}

// flush rescans and reports what changed; it returns a channel closed when the rescan, or the one already running,
// ends. A flush that finds one running asks it to go around once more.
func (watcher *nodeFileWatcher) flush() <-chan struct{} {
	watcher.mu.Lock()
	if watcher.running != nil {
		watcher.dirty = true
		running := watcher.running
		watcher.mu.Unlock()
		return running
	}
	done := make(chan struct{})
	watcher.running = done
	watcher.mu.Unlock()
	go func() {
		defer func() {
			watcher.mu.Lock()
			watcher.running = nil
			watcher.mu.Unlock()
			close(done)
		}()
		watcher.runFlush()
	}()
	return done
}

func (watcher *nodeFileWatcher) runFlush() {
	for {
		watcher.mu.Lock()
		watcher.dirty = false
		events := watcher.events
		watcher.events = map[string]bool{}
		watcher.mu.Unlock()
		changed, err := watcher.sync(true)
		if err != nil {
			fileError, ok := errors.AsType[*durableenv.FileError](err)
			if !ok {
				code := durableenv.FileErrorInvalid
				if isDenied(err) {
					code = durableenv.FileErrorPermissionDenied
				}
				fileError = durableenv.NewFileError(code, err.Error(), "", err)
			}
			if _, ok := errors.AsType[budgetExceeded](err); ok {
				fileError = durableenv.NewFileError(durableenv.FileErrorInvalid, err.Error(), "", nil)
			}
			watcher.deliver(durableenv.WatchChangeError{Error: fileError})
			watcher.stop()
			return
		}
		for path := range events {
			changed[path] = true
		}
		if len(changed) > 0 {
			paths := make([]string, 0, len(changed))
			for path := range changed {
				paths = append(paths, path)
			}
			slices.SortFunc(paths, compareUTF16)
			watcher.deliver(durableenv.WatchChangePaths{Paths: paths})
		}
		watcher.mu.Lock()
		again := watcher.dirty && !watcher.closed
		watcher.mu.Unlock()
		if !again {
			return
		}
	}
}

// sync rescans, reports differences, and installs watchers for new directories, rescanning until none are new.
func (watcher *nodeFileWatcher) sync(report bool) (map[string]bool, error) {
	changed := map[string]bool{}
	for range syncRounds {
		watcher.mu.Lock()
		closed, mode := watcher.closed, watcher.mode
		watcher.mu.Unlock()
		if closed {
			break
		}
		scan, err := watcher.scan(mode)
		if err != nil {
			return nil, err
		}
		// Closed during the scan: installing watchers now would leak them.
		watcher.mu.Lock()
		closed, mode = watcher.closed, watcher.mode
		watcher.mu.Unlock()
		if closed {
			break
		}
		next := scan.snapshot
		if report {
			for _, path := range watcher.diff(watcher.snapshot, next) {
				changed[path] = true
			}
		}
		watcher.snapshot = next
		if mode == durableenv.WatchPolling || !watcher.reconcileWatchers(scan) {
			break
		}
		// Something written into a new directory before its watcher existed shows up in the next round.
		report = true
	}
	return changed, nil
}

func (watcher *nodeFileWatcher) diff(previous, next snapshot) []string {
	var changed []string
	for path, entry := range next {
		if before, ok := previous[path]; !ok || before != entry {
			changed = append(changed, watcher.reported(path))
		}
	}
	for path := range previous {
		if _, ok := next[path]; !ok {
			changed = append(changed, watcher.reported(path))
		}
	}
	return changed
}

// reported names what to report for a path: an ancestor that changed identity moved every target below it, so those
// targets are reported.
func (watcher *nodeFileWatcher) reported(path string) string {
	for _, target := range watcher.targets {
		if isWithin(path, target.path) {
			return path
		}
	}
	for _, target := range watcher.targets {
		if isWithin(target.path, path) {
			return target.path
		}
	}
	return path
}

func (watcher *nodeFileWatcher) scan(mode durableenv.WatchMode) (scanResult, error) {
	result := snapshot{}
	linkedFiles := map[string]bool{}
	// Directories are counted once, but traversed once per target: overlapping targets differ in recursion and exclusions,
	// and an entry recorded for one target must still be descended into for another.
	counted := map[string]bool{}
	type traversal struct {
		target    int
		directory string
	}
	traversed := map[traversal]bool{}
	// listed is the kind of each listed entry, not following links: a target that links to a directory is recorded by stat,
	// but symbolic links below a recursive target are not followed.
	listed := map[string]entryKind{}
	countDirectory := func(path string) error {
		counted[path] = true
		if len(counted) > watcher.maxDirectories {
			return budgetExceeded{limit: watcher.maxDirectories}
		}
		return nil
	}
	record := func(path string, info fs.FileInfo, follow bool) {
		hash := ""
		if mode == durableenv.WatchPolling && info.Mode().IsRegular() && info.Size() <= hashMaxBytes &&
			time.Since(info.ModTime()) < hashRecentWindow {
			if content, err := os.ReadFile(path); err == nil {
				hash = fmt.Sprintf("%x", sha256.Sum256(content))
			}
		}
		result[path] = entryOf(path, info, follow, hash)
	}
	var scanDirectory func(index int, target resolvedTarget, directory string) error
	scanDirectory = func(index int, target resolvedTarget, directory string) error {
		key := traversal{index, directory}
		if traversed[key] {
			return nil
		}
		traversed[key] = true
		names, err := readDirNames(directory)
		if err != nil {
			// The watched directory itself must be readable; below it, unreadable directories are skipped.
			if directory == target.path && isDenied(err) {
				return err
			}
			switch errnoCode(err) {
			case "ENOENT", "EACCES", "EPERM", "ENOTDIR":
				return nil
			}
			return err
		}
		for _, name := range names {
			if target.excludes(name) {
				continue
			}
			path := filepath.Join(directory, name)
			kind, known := listed[path]
			if !known {
				info, err := os.Lstat(path)
				if err != nil {
					continue
				}
				kind = entryOf(path, info, false, "").kind
				listed[path] = kind
				// A target's own entry (following links) wins over its listing by another target.
				if _, seen := result[path]; !seen {
					record(path, info, false)
				}
			}
			if target.recursive && kind == kindDirectory {
				if err := countDirectory(path); err != nil {
					return err
				}
				if err := scanDirectory(index, target, path); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for index, target := range watcher.targets {
		for _, ancestor := range ancestorsOf(target.path) {
			if _, seen := result[ancestor]; seen {
				continue
			}
			// Identity only: an ancestor's own timestamps change with every unrelated sibling.
			if info, err := os.Lstat(ancestor); err == nil {
				entry := entryOf(ancestor, info, false, "")
				entry.size, entry.mtimeMs = 0, 0
				result[ancestor] = entry
			}
		}
		// The target itself may be a symbolic link to what is watched; follow it. A missing target is watched for its
		// creation; one that cannot be reached for lack of permission fails.
		info, err := os.Stat(target.path)
		if err != nil {
			if isDenied(err) {
				return scanResult{}, err
			}
			continue
		}
		record(target.path, info, true)
		if info.Mode().IsRegular() {
			if link, err := os.Lstat(target.path); err == nil && link.Mode()&fs.ModeSymlink != 0 {
				linkedFiles[target.path] = true
			}
		}
		if info.IsDir() {
			if err := countDirectory(target.path); err != nil {
				return scanResult{}, err
			}
			if err := scanDirectory(index, target, target.path); err != nil {
				return scanResult{}, err
			}
		}
	}
	return scanResult{snapshot: result, linkedFiles: linkedFiles}, nil
}

// reconcileWatchers watches every existing ancestor of each target, each target directory, each target that is a
// symbolic link to a file (changes to that file are not events of the link's directory), and each directory below a
// recursive target. It returns whether a watcher was added.
func (watcher *nodeFileWatcher) reconcileWatchers(scan scanResult) bool {
	snap := scan.snapshot
	wanted := map[string]bool{}
	for path := range scan.linkedFiles {
		wanted[path] = true
	}
	for _, target := range watcher.targets {
		for _, ancestor := range ancestorsOf(target.path) {
			if snap[ancestor].kind == kindDirectory {
				wanted[ancestor] = true
			}
		}
		entry, ok := snap[target.path]
		if !ok || entry.kind != kindDirectory {
			continue
		}
		wanted[target.path] = true
		if target.recursive {
			for path, below := range snap {
				if below.kind == kindDirectory && path != target.path && isWithin(path, target.path) {
					wanted[path] = true
				}
			}
		}
	}
	watcher.mu.Lock()
	fsw := watcher.fsw
	var remove, add []string
	removeAliases := map[string]string{}
	for path, installed := range watcher.watched {
		entry, present := snap[path]
		// Gone, or replaced: a watcher follows the inode it was installed on, not the path.
		if !wanted[path] || !present || entry.dev != installed.dev || entry.ino != installed.ino {
			remove = append(remove, path)
			delete(watcher.watched, path)
			if installed.alias != "" {
				delete(watcher.linkAliases, installed.alias)
				// A backend that registered the link under its resolved path removes it only by that path, unless
				// that path is also the entry of a watched directory, whose watcher the backend shares.
				if _, shared := watcher.watched[filepath.Dir(installed.alias)]; !shared {
					removeAliases[path] = installed.alias
				}
			}
		}
	}
	for path := range wanted {
		if _, present := snap[path]; !present {
			continue
		}
		if _, installed := watcher.watched[path]; !installed {
			add = append(add, path)
		}
	}
	watcher.mu.Unlock()
	if fsw == nil {
		return false
	}
	for _, path := range remove {
		if err := fsw.Remove(path); err != nil {
			if alias, ok := removeAliases[path]; ok {
				_ = fsw.Remove(alias)
			}
		}
	}
	slices.Sort(add)
	added := false
	for _, path := range add {
		if err := fsw.Add(path); err != nil {
			// Out of watches or unsupported: compare snapshots from now on, and say coverage was uncertain.
			switch errnoCode(err) {
			case "ENOENT", "EACCES", "EPERM":
				continue
			}
			watcher.switchToPolling()
			return false
		}
		var alias string
		if scan.linkedFiles[path] {
			if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != path {
				alias = resolved
			}
		}
		watcher.mu.Lock()
		if !watcher.closed {
			entry := snap[path]
			watcher.watched[path] = installedWatch{dev: entry.dev, ino: entry.ino, alias: alias}
			if alias != "" {
				watcher.linkAliases[alias] = path
			}
		}
		watcher.mu.Unlock()
		added = true
	}
	return added
}

func (watcher *nodeFileWatcher) switchToPolling() {
	watcher.mu.Lock()
	if watcher.mode == durableenv.WatchPolling {
		watcher.mu.Unlock()
		return
	}
	watcher.mode = durableenv.WatchPolling
	fsw := watcher.fsw
	watcher.fsw = nil
	clear(watcher.watched)
	clear(watcher.linkAliases)
	if watcher.timer != nil {
		watcher.timer.Stop()
		watcher.timer = nil
	}
	watcher.mu.Unlock()
	if fsw != nil {
		_ = fsw.Close()
	}
	watcher.deliver(durableenv.WatchChangeOverflow{})
	watcher.schedulePoll()
}

// pumpEvents turns the events of one fsnotify watcher into rescans until the watcher closes.
func (watcher *nodeFileWatcher) pumpEvents(fsw *fsnotify.Watcher) {
	for {
		select {
		case event, ok := <-fsw.Events:
			if !ok {
				return
			}
			watcher.onEvent(event.Name)
		case _, ok := <-fsw.Errors:
			if !ok {
				return
			}
			// A lost event or a failed watch: rescan, which also replaces the watcher.
			watcher.scheduleFlush()
		}
	}
}

func (watcher *nodeFileWatcher) onEvent(path string) {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	if watcher.closed {
		return
	}
	// The file a linked target points to changed: report the target.
	if link, ok := watcher.linkAliases[path]; ok {
		watcher.events[link] = true
		watcher.scheduleFlushLocked()
	}
	// Events about unrelated siblings of an ancestor, or about excluded entries, are ignored.
	relevant := watcher.inScope(path)
	if relevant {
		watcher.events[watcher.reported(path)] = true
	}
	// An event on a watched directory itself names no entry: the directory may have gone, so rescan.
	if _, watched := watcher.watched[path]; relevant || watched {
		watcher.scheduleFlushLocked()
	}
}

func (watcher *nodeFileWatcher) inScope(path string) bool {
	for _, target := range watcher.targets {
		if isWithin(target.path, path) {
			return true
		}
		if !isWithin(path, target.path) || path == target.path {
			continue
		}
		relative, err := filepath.Rel(target.path, path)
		if err != nil {
			continue
		}
		components := strings.Split(relative, string(filepath.Separator))
		if !target.recursive && len(components) > 1 {
			continue
		}
		if slices.ContainsFunc(components, target.excludes) {
			continue
		}
		return true
	}
	return false
}

// anyUnreliable reports whether any path, or its nearest existing ancestor, is on a file system that does not report
// remote changes.
func anyUnreliable(paths []string) bool {
	if runtime.GOOS != "linux" && runtime.GOOS != "android" {
		return false
	}
	for _, path := range paths {
		for _, candidate := range append([]string{path}, ancestorsOf(path)...) {
			kind, ok := filesystemType(candidate)
			if !ok {
				continue
			}
			if unreliableFilesystems[kind] {
				return true
			}
			break
		}
	}
	return false
}
