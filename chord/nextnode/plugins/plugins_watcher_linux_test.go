//go:build linux

package plugins

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Ports the Linux-only case of packages/chord/test/next-node/plugins/watcher.test.ts (`it.skipIf(process.platform !==
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
// nothing).
func readerWaits(path string) bool {
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	_ = syscall.Close(fd)
	return true
}

// writeWhenRead writes text into the pipe at path once a reader has it open (a non-blocking open for writing fails until
// then), and closes it: that reader reads text.
func writeWhenRead(t *testing.T, path, text string) {
	t.Helper()
	var fd int
	eventually(t, 10*time.Second, "a reader on "+path, func() bool {
		var err error
		fd, err = syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil && !errors.Is(err, syscall.ENXIO) {
			t.Fatal(err)
		}
		return err == nil
	})
	if _, err := syscall.Write(fd, []byte(text)); err != nil {
		t.Fatal(err)
	}
	_ = syscall.Close(fd)
}

// Ports watcher.test.ts "reads one path in order: a second comparison waits for the first, held on a pipe, and decides"
// (`it.skipIf(process.platform !== "linux")`).
func TestTheBaseMarkReadsOnePathInOrderASecondComparisonWaitsForTheFirstHeldOnAPipeAndDecides(t *testing.T) {
	_, f := based(t, nil)
	root := realOf(t, tempRoot(t))
	pipe := filepath.Join(root, "held.ts")
	fifo(t, pipe)
	// The first comparison opens the pipe and waits for a writer; a second one is queued behind it.
	f.recordBase(model.BaseInputs{pipe: hashOf("as built")})
	f.mark(pipe)
	f.settle()
	// The first read gets other bytes: marked. (Two readers at once would split this: one of them gets nothing.)
	writeWhenRead(t, pipe, "other bytes")
	f.baseIs(true)
	// Only now does the second read open the pipe, and it decides: the recorded bytes.
	writeWhenRead(t, pipe, "as built")
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
	f.recordBase(model.BaseInputs{pipe: hashOf("as built")})
	writeWhenRead(t, pipe, "as built")
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
	writeWhenRead(t, pipe, "as built")
	f.baseIs(false)
}
