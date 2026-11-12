package plugins

// Not a port: cases of fsnotify's kqueue backend (v1.5.4 kqueue.go), the system watcher on macOS and the BSDs, which Pi's
// watcher never meets (libuv watches a path with FSEvents on macOS, so the watch of a directory moved away and back goes on).
// kqueueNotifier is that backend as far as the plugin watcher meets it, driven by the test where the system's reader would
// run, so the cases run on any system.
//
// What it keeps of kqueue.go:
//   - `Add` of a directory (addWatch) watches it for what is made in it and, as watchDirectoryFiles, every entry of it: a file
//     entirely, a directory (internalWatch) for its own removal and rename alone, when it is held no other way;
//   - the reader (readEvents), on the rename of a watched directory, removes its watch and forgets it in `fileExists`;
//   - the reader, on a write of a watched directory (sendDirectoryChangeEvents), reports each entry it does not know as
//     created, and holds every entry as above, a directory that is back included;
//   - a watch that was not added for what is made in its directory reports nothing of it.

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

type kqueueNotifier struct {
	mu sync.Mutex
	// held is every path with a watch: true when it was added for the changes of its directory, false when it is held for its
	// own removal and rename alone.
	held map[string]bool
	// exists is `fileExists`: the entries the reader reported or listed.
	exists map[string]bool
	adds   int
	events chan fsnotify.Event
	errors chan error
	closed bool
}

var _ notifier = (*kqueueNotifier)(nil)

func newKqueueNotifier() *kqueueNotifier {
	// Buffered: the cases deliver the reader's events in a section of the host, which the watcher's goroutine cannot enter
	// before they are all delivered.
	return &kqueueNotifier{held: map[string]bool{}, exists: map[string]bool{}, events: make(chan fsnotify.Event, 1<<16), errors: make(chan error)}
}

// notifier is the watcherOptions' way to the one notifier.
func (k *kqueueNotifier) notifier() (notifier, error) { return k, nil }

func (k *kqueueNotifier) Events() <-chan fsnotify.Event { return k.events }
func (k *kqueueNotifier) Errors() <-chan error          { return k.errors }
func (k *kqueueNotifier) PartialWatches() bool          { return true }

func (k *kqueueNotifier) Add(name string) error {
	name = filepath.Clean(name)
	info, err := os.Lstat(name)
	if err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.adds++
	k.held[name] = true
	if info.IsDir() {
		k.hold(name)
	}
	return nil
}

func (k *kqueueNotifier) Remove(name string) error {
	name = filepath.Clean(name)
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.held[name]; !ok {
		return errors.New("can't remove non-existent kevent watch for: " + name)
	}
	delete(k.held, name)
	return nil
}

func (k *kqueueNotifier) WatchList() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	list := make([]string, 0, len(k.held))
	for path := range k.held {
		list = append(list, path)
	}
	return list
}

func (k *kqueueNotifier) Close() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.closed {
		k.closed = true
		close(k.events)
		close(k.errors)
	}
	return nil
}

// hold is watchDirectoryFiles and the holding of sendDirectoryChangeEvents: every entry of dir is known to exist and held,
// unless it is held already (the flags of a watch added stay).
func (k *kqueueNotifier) hold(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if _, ok := k.held[path]; !ok {
			k.held[path] = !entry.IsDir()
		}
		k.exists[path] = true
	}
}

// renamed is the reader meeting the rename of the watched directory dir (its own watch's event): the watch ends, the entry is
// forgotten, and the event names it, as removed too when nothing is at its path.
func (k *kqueueNotifier) renamed(dir string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.held[dir]; !ok {
		return
	}
	delete(k.held, dir)
	delete(k.exists, dir)
	op := fsnotify.Rename
	if _, err := os.Lstat(dir); err != nil {
		op |= fsnotify.Remove
	}
	k.events <- fsnotify.Event{Name: dir, Op: op}
}

// listed is the reader meeting the write of the watched directory dir: each entry it does not know is reported as created, and
// held.
func (k *kqueueNotifier) listed(dir string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if !k.exists[path] {
			k.events <- fsnotify.Event{Name: path, Op: fsnotify.Create}
		}
	}
	k.hold(dir)
}

// reports is the kernel telling the watch of dir that event happened to an entry of it: it reaches the watcher only through a
// watch that was added for the changes of dir.
func (k *kqueueNotifier) reports(dir string, event fsnotify.Event) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.held[filepath.Clean(dir)] {
		return false
	}
	k.events <- event
	return true
}

// holds reports whether the notifier holds a watch of dir, whichever way, and added whether it was added for the changes of dir.
func (k *kqueueNotifier) holds(dir string) (holds, added bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	added, holds = k.held[dir]
	return holds, added
}

func (k *kqueueNotifier) addCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.adds
}

// movedAwayAndBack is the two renames of dir away and back and what the reader does with their kernel events when it wakes
// after both: it ends the watch of dir, then lists its parent, finds dir there and holds it for its rename alone. It runs in a
// section of the host, as the case's own file operations do, so the watcher handles nothing before the reader is done.
func (k *kqueueNotifier) movedAwayAndBack(t *testing.T, f *watcherFixture, dir string) {
	t.Helper()
	away := dir + "-away"
	f.run(func() {
		if err := os.Rename(dir, away); err != nil {
			t.Error(err)
		}
		if err := os.Rename(away, dir); err != nil {
			t.Error(err)
		}
		k.renamed(dir)
		k.listed(filepath.Dir(dir))
	})
	if holds, added := k.holds(dir); !holds || added {
		t.Fatalf("the notifier holds %s = %v, added for its changes = %v: the case needs it held for its rename alone", dir, holds, added)
	}
}

// A directory of base code watched as a tree, moved away and back in one wake of kqueue's reader: its watch ended with the
// rename, and the listing of its parent holds it again for its own removal and rename alone, so what is made in it is reported
// to no one. It is in the notifier's list, so `keepWatches` finds nothing lost, and no event is left that names it: the watcher
// adds it again.
func TestTheBaseMarkAddsTheTreeDirectoryAgainThatKqueueHoldsForItsRenameAloneWhenItIsBack(t *testing.T) {
	parent := realOf(t, tempRoot(t))
	base := filepath.Join(parent, "app")
	writeFiles(t, base, map[string]string{"core.ts": "v1", "sub/inner.ts": "inner"})
	k := newKqueueNotifier()
	f := watcherOverNotifier(t, nil, base, k.notifier)
	f.joinComparisons()
	if f.marks().Base {
		t.Fatal("base set at the start")
	}
	k.movedAwayAndBack(t, f, base)
	eventually(t, 5*time.Second, "the watcher to add the watch of the directory that was back", func() bool {
		_, added := k.holds(base)
		return added
	})
	write(t, filepath.Join(base, "new.ts"), "x")
	if !k.reports(base, fsnotify.Event{Name: filepath.Join(base, "new.ts"), Op: fsnotify.Create}) {
		t.Fatal("the notifier reports nothing that is made in the directory")
	}
	f.baseIs(true)
}

// The same for a directory watched alone, an ancestor of a recorded file that is not there yet: the one a case of the watcher's
// identity tests moves, here in kqueue's way.
func TestTheBaseMarkAddsTheDirectoryAgainThatKqueueHoldsForItsRenameAloneWhenItIsBack(t *testing.T) {
	top := realOf(t, tempRoot(t))
	dir := filepath.Join(top, "d")
	file := filepath.Join(dir, "sub", "entry.ts")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	k := newKqueueNotifier()
	base := realOf(t, tempRoot(t))
	f := watcherOverNotifier(t, nil, base, k.notifier)
	f.recordBase(model.BaseInputs{file: hashOf("as built")})
	f.baseIs(true)
	k.movedAwayAndBack(t, f, dir)
	eventually(t, 5*time.Second, "the watcher to add the watch of the directory that was back", func() bool {
		_, added := k.holds(dir)
		return added
	})
	writeFiles(t, filepath.Join(dir, "sub"), map[string]string{"entry.ts": "as built"})
	if !k.reports(dir, fsnotify.Event{Name: filepath.Join(dir, "sub"), Op: fsnotify.Create}) {
		t.Fatal("the notifier reports nothing that is made in the directory")
	}
	f.baseIs(false)
	write(t, file, "edited")
	// The directory made is watched alone now: its watch was added by the watcher.
	if !k.reports(filepath.Join(dir, "sub"), fsnotify.Event{Name: file, Op: fsnotify.Write}) {
		t.Fatal("the notifier reports nothing that is made in the directory made")
	}
	f.baseIs(true)
}

// A tree of thousands of directories moved away and back is one `Add` more (two with the two events that name it), not one for
// each directory: the watches under it were not ended by its rename.
func TestTheBaseMarkAddsAgainOnlyTheDirectoryKqueueHoldsForItsRenameAloneNotTheTreeUnderIt(t *testing.T) {
	parent := realOf(t, tempRoot(t))
	base := filepath.Join(parent, "app")
	files := map[string]string{"core.ts": "v1"}
	for group := range 50 {
		for each := range 100 {
			files[filepath.Join("g"+strconv.Itoa(group), "d"+strconv.Itoa(each), "f.ts")] = "x"
		}
	}
	writeFiles(t, base, files)
	k := newKqueueNotifier()
	f := watcherOverNotifier(t, nil, base, k.notifier)
	f.joinComparisons()
	before := k.addCount()
	if before < 5000 {
		t.Fatalf("the watcher added %d watches of a tree of 5,000 directories", before)
	}
	k.movedAwayAndBack(t, f, base)
	eventually(t, 5*time.Second, "the watcher to add the watch of the directory that was back", func() bool {
		_, added := k.holds(base)
		return added
	})
	f.run(func() {})
	if added := k.addCount() - before; added > 2 {
		t.Fatalf("the watcher added %d watches for one directory moved away and back, want at most one for each event that names it", added)
	}
}
