//go:build linux

package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Ports the Linux-only case of packages/chord/test/node/plugins/watcher.test.ts (`it.skipIf(process.platform !==
// "linux")`): as a file named for its platform, it runs where upstream's does.

func TestTheWatcherFixRound1PutsNoWatchOnNodeModulesOrGitOpusN2200PackagesAddNoInotifyWatch(t *testing.T) {
	root := realOf(t, tempRoot(t))
	dir := writePlugin(t, root, "a", "@test/a", map[string]string{"worker.ts": ""}, nil)
	for i := range 200 {
		writeFiles(t, filepath.Join(dir, "node_modules", fmt.Sprintf("pkg%d", i), "lib"), map[string]string{"index.js": ""})
	}
	writeFiles(t, filepath.Join(dir, ".git", "objects"), map[string]string{"x": ""})
	before := inotifyWatches(t)
	watcher := newPluginWatcher(watcherOptions{Roots: []string{root}, Base: []string{}, Kinds: kinds, Failed: func(error) {}},
		state.NewBatch(func(state.BatchError) {}, nil))
	// The root and the plugin's directory.
	if got := inotifyWatches(t) - before; got != 2 {
		t.Fatalf("the watcher added %d inotify watches, want 2", got)
	}
	watcher.close()
	watcher.join()
}

// inotifyWatches is the inotify watches this process holds: one line per watch in its inotify descriptors' fdinfo.
func inotifyWatches(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fdinfo")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		text, err := os.ReadFile(filepath.Join("/proc/self/fdinfo", entry.Name()))
		if err != nil {
			// Closed meanwhile.
			continue
		}
		for line := range strings.SplitSeq(string(text), "\n") {
			if strings.HasPrefix(line, "inotify wd:") {
				count++
			}
		}
	}
	return count
}

// fifo makes a named pipe at path.
func fifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

// readerWaits reports whether a reader has the pipe at path open (a non-blocking open for writing succeeds; it then reads
// nothing). Because closing the write end sends EOF to a waiting reader, call this only to assert that no reader waits or
// to drain readers during cleanup.
func readerWaits(path string) bool {
	file, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

// pipeWatcherState summarizes the watcher's base-comparison state for diagnostics when waiting on a pipe times out.
func pipeWatcherState(f *watcherFixture, path string) string {
	var summary string
	f.run(func() {
		w := f.watcher
		comp := w.comparing[path]
		again := false
		if comp != nil {
			again = comp.again
		}
		summary = fmt.Sprintf("closed=%v comparers=%d comparing=%d queue=%d pathComparing=%v pathAgain=%v base=%v",
			w.closed, w.comparers, len(w.comparing), len(w.queue), comp != nil, again, len(w.differing) > 0)
	})
	return summary
}

// drainPipeOnCleanup unblocks any comparison still reading path if the test ends early (for example on a failed assertion),
// so watcherOverNotifier's later watcher.join() cleanup returns instead of hanging on a reader blocked in model.HashFile.
func drainPipeOnCleanup(t *testing.T, f *watcherFixture, path string) {
	t.Helper()
	t.Cleanup(func() {
		if !f.ComparingBase() {
			return
		}
		f.close()
		deadline := time.Now().Add(testbudget.Wait(t))
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for f.ComparingBase() {
			readerWaits(path)
			if !f.ComparingBase() || time.Now().After(deadline) {
				return
			}
			<-ticker.C
		}
	})
}

// writeWhenRead opens the named pipe at path once for writing (blocking until a reader opens it), writes text, and closes
// it: that reader reads text. Opening once in blocking mode pairs with the reader without an extra open-and-close probe
// that would deliver an early EOF. On timeout it opens a non-blocking reader to release the blocked writer goroutine before
// failing with the watcher's state.
func writeWhenRead(t *testing.T, f *watcherFixture, path, text string) {
	t.Helper()
	type openResult struct {
		file *os.File
		err  error
	}
	opened := make(chan openResult, 1)
	go func() {
		file, err := os.OpenFile(path, os.O_WRONLY, 0)
		opened <- openResult{file: file, err: err}
	}()
	bound := testbudget.Wait(t)
	timer := time.NewTimer(bound)
	defer timer.Stop()
	var res openResult
	select {
	case res = <-opened:
	case <-timer.C:
		// Open a non-blocking reader and hold it open until the blocked O_WRONLY open returns, then close both ends so the
		// helper goroutine never leaks.
		rfile, rerr := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if rerr == nil {
			res = <-opened
			if res.err == nil {
				_ = res.file.Close()
			}
			_ = rfile.Close()
		}
		t.Fatalf("timed out after %v waiting for a reader on %s (%s)", bound, path, pipeWatcherState(f, path))
	}
	if res.err != nil {
		t.Fatal(res.err)
	}
	if _, err := res.file.WriteString(text); err != nil {
		_ = res.file.Close()
		t.Fatal(err)
	}
	if err := res.file.Close(); err != nil {
		t.Fatal(err)
	}
}

// Ports watcher.test.ts "reads one path in order: a second comparison waits for the first, held on a pipe, and decides"
// (`it.skipIf(process.platform !== "linux")`).
func TestTheBaseMarkReadsOnePathInOrderASecondComparisonWaitsForTheFirstHeldOnAPipeAndDecides(t *testing.T) {
	_, f := based(t, nil)
	root := realOf(t, tempRoot(t))
	pipe := filepath.Join(root, "held.ts")
	fifo(t, pipe)
	drainPipeOnCleanup(t, f, pipe)
	// The first comparison opens the pipe and waits for a writer; a second one is queued behind it (a comparison stays
	// held on purpose until writeWhenRead below, so do not wait for ComparingBase to clear).
	f.recordBase(model.BaseInputs{pipe: hashOf("as built")})
	f.mark(pipe)
	// The first read gets other bytes: marked. (Two readers at once would split this: one of them gets nothing.)
	writeWhenRead(t, f, pipe, "other bytes")
	f.baseIs(true)
	// Only now does the second read open the pipe, and it decides: the recorded bytes.
	writeWhenRead(t, f, pipe, "as built")
	f.baseIs(false)
}

// Ports watcher.test.ts "renews nothing for a burst of attribute changes on an ancestor, and renews at its rename".
func TestTheBaseMarkRenewsNothingForABurstOfAttributeChangesOnAnAncestorAndRenewsAtItsRename(t *testing.T) {
	_, f := based(t, nil)
	root := realOf(t, tempRoot(t))
	if err := os.MkdirAll(filepath.Join(root, "ancestor", "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	pipe := filepath.Join(root, "ancestor", "dir", "held.ts")
	fifo(t, pipe)
	drainPipeOnCleanup(t, f, pipe)
	f.recordBase(model.BaseInputs{pipe: hashOf("as built")})
	writeWhenRead(t, f, pipe, "as built")
	f.settle()
	if f.marks().Base {
		t.Fatal("base set at the record")
	}
	for round := range 20 {
		mode := os.FileMode(0o755)
		if round%2 == 0 {
			mode = 0o700
		}
		for _, dir := range []string{filepath.Join(root, "ancestor"), filepath.Join(root, "ancestor", "dir")} {
			if err := os.Chmod(dir, mode); err != nil {
				t.Fatal(err)
			}
		}
	}
	f.settle()
	// A renewal would compare the file again: a reader would wait on the pipe.
	if readerWaits(pipe) {
		t.Fatal("a comparison waits on the pipe after attribute changes")
	}
	if err := os.Rename(filepath.Join(root, "ancestor"), filepath.Join(root, "moved")); err != nil {
		t.Fatal(err)
	}
	f.baseIs(true)
	if err := os.Rename(filepath.Join(root, "moved"), filepath.Join(root, "ancestor")); err != nil {
		t.Fatal(err)
	}
	writeWhenRead(t, f, pipe, "as built")
	f.baseIs(false)
}

// Not a port (Pi's watcher watches no tree on request). Closing the watcher releases the watches of a tree it was asked to
// watch: a discovery keeps them, and after the close the process holds as many inotify watches as before the watcher was made.
func TestClosingTheWatcherReleasesTheWatchesOfATreeItWasAskedToWatch(t *testing.T) {
	root := realOf(t, tempRoot(t))
	writePlugin(t, root, "a", "@test/a", map[string]string{"worker.ts": ""}, nil)
	tree := realOf(t, t.TempDir())
	if err := os.Mkdir(filepath.Join(tree, "inside"), 0o755); err != nil {
		t.Fatal(err)
	}
	discovery, err := model.DiscoverPlugins([]model.PluginRoot{{Path: root}}, model.DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	before := inotifyWatches(t)
	watcher := newPluginWatcher(watcherOptions{Roots: []string{root}, Base: []string{}, Kinds: kinds, Failed: func(error) {}},
		state.NewBatch(func(state.BatchError) {}, nil))
	own := inotifyWatches(t)
	if _, watching := watcher.alsoWatch(tree); !watching {
		t.Fatal("the watcher watches nothing")
	}
	// The tree and the directory in it.
	if got := inotifyWatches(t) - own; got != 2 {
		t.Fatalf("the request added %d inotify watches, want 2", got)
	}
	watcher.know(discovery, watcher.discoverySnapshot())
	if got := inotifyWatches(t) - own; got != 2 {
		t.Fatalf("after a discovery the tree holds %d inotify watches, want 2", got)
	}
	watcher.close()
	watcher.join()
	if got := inotifyWatches(t) - before; got != 0 {
		t.Fatalf("%d inotify watches are left after the close, want none", got)
	}
	if len(watcher.trees) != 0 || len(watcher.asked) != 0 {
		t.Fatalf("trees = %v, asked = %v after the close, want none", watcher.trees, watcher.asked)
	}
}
