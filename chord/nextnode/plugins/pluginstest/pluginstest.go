// Package pluginstest holds what the tests of the plugins component and the tests of an app that owns one share: the barrier
// of the component's file watcher. Import this package only from _test.go files: TestNoProductionCodeImportsTheTestHelper
// enforces it.
package pluginstest

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Watcher is what the barrier asks of a file watcher. *plugins.Plugins answers for its own (WatchTree, Watches,
// ComparingBase); the tests of package plugins answer for a watcher they made without a component.
type Watcher interface {
	// WatchTree has the watcher watch the tree at dir too, until it closes, and returns the directories it watches then; false
	// when it watches nothing (a watcher that failed to watch, or a closed one). No discovery of the watcher's drops the tree:
	// one that did, between WatchTree and the event of the barrier's directory, would leave that directory unwatched.
	WatchTree(dir string) (watched []string, watching bool)
	// Watches reports whether the watcher has a watch on the directory dir.
	Watches(dir string) bool
	// ComparingBase reports whether a comparison of a base file is queued or in flight.
	ComparingBase() bool
}

// settleBound is how long Settle waits for the watcher before it fails the test.
const settleBound = 15 * time.Second

// barrier is the barrier of one watcher: a directory outside its roots that the watcher watches too, where a new directory is
// made to learn that the events of everything written before it were handled; count numbers them.
type barrier struct {
	dir   string
	count atomic.Int64
}

// barriers is each watcher's barrier, from its first Settle until the test that made it ends.
var barriers sync.Map

// Settle returns once watcher handled the events of every file operation made before the call and finished every base-file
// comparison those events started: a watch event of an edit arrives on its own time, and one that arrives after a build that
// followed the first event of the edit would leave the plugin dirty; a base file's hash is read off the watcher's section, so
// its mark reflects the latest write only once ComparingBase is false. A write in place is two events, a removed directory
// many. The events of one file system arrive in order, and a directory made in a watched one is watched when its event was
// handled: the watcher's own answer (Watches) says the barrier's directory was reached, so everything before it was, and
// ComparingBase then waits for any queued or in-flight base comparison to end. Nothing is waited for when the watcher does not
// watch. It fails the test when the watcher has not settled after 15 s.
func Settle(t testing.TB, watcher Watcher) {
	t.Helper()
	settle(t, watcher, settleBound)
}

// settle is Settle with the bound a test of it gives.
func settle(t testing.TB, watcher Watcher, bound time.Duration) {
	t.Helper()
	found, ok := barriers.Load(watcher)
	if !ok {
		found, ok = barriers.LoadOrStore(watcher, &barrier{dir: t.TempDir()})
		if !ok {
			// The directory goes with the test that made it: a later test's Settle of the same watcher makes its own.
			t.Cleanup(func() { barriers.Delete(watcher) })
		}
	}
	own := found.(*barrier)
	// The watcher keeps the barrier's tree through its discoveries (a build beside the test discovers at any moment). Asking each
	// time says whether it watches at all and, for Windows, which directories.
	directories, watching := watcher.WatchTree(own.dir)
	if !watching {
		return
	}
	if runtime.GOOS == "windows" {
		settleDirectoryStamps(directories)
	}
	sentinel := filepath.Join(own.dir, "sentinel-"+strconv.FormatInt(own.count.Add(1), 10))
	if err := os.Mkdir(sentinel, 0o755); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(bound)
	for !watcher.Watches(sentinel) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for the watcher to handle a directory made after the edits", bound)
		}
		time.Sleep(5 * time.Millisecond)
	}
	for watcher.ComparingBase() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for the watcher to finish base file comparisons", bound)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// settleDirectoryStamps has Windows report, before the barrier's directory is made, the last-write time of the watched
// directories whose entries changed. Windows reports it as a write of the directory to the watch of its parent, lazily, at a
// moment after the entry's own event that no barrier orders; opening and closing each watched directory makes the report
// come then, so a case reads marks that already hold it.
func settleDirectoryStamps(directories []string) {
	for _, directory := range directories {
		if file, err := os.Open(directory); err == nil {
			_ = file.Close()
		}
	}
}
