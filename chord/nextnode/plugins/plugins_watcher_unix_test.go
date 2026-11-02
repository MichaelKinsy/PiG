//go:build !windows

package plugins

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// watcher.ts "reports a failure to watch once and stops watching": a directory the watcher cannot watch (one its user cannot
// read: inotify and kqueue refuse it), as upstream's `fs.watch` throws for it. The root is the one that fails, so nothing is
// watched. Unix only: on Windows `os.Chmod` only toggles the read-only attribute, which cannot deny a directory to its owner.
func TestTheWatcherReportsAFailureToWatchOnceAndStopsWatching(t *testing.T) {
	root := realOf(t, tempRoot(t))
	base := realOf(t, tempRoot(t))
	if err := os.Chmod(root, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })
	if _, err := os.ReadDir(root); err == nil {
		t.Fatal("this user reads a directory with no permission: the case needs a user that does not")
	}
	f := watcherOn(t, root, base)
	if got := f.failed(); len(got) != 1 || !errors.Is(got[0], fs.ErrPermission) {
		t.Fatalf("failures = %v, want the permission error once (the root and the base each tried to watch)", got)
	}
	// Later discoveries and marks report nothing more (the root readable again: it is the watcher that gave up), and the marks
	// still work.
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	f.know(f.discoverySnapshot())
	f.mark(filepath.Join(root, "c"))
	if got := f.failed(); len(got) != 1 {
		t.Fatalf("failures = %v, want one", got)
	}
	if !f.marks().Set {
		t.Fatal("marks stopped working")
	}
	f.run(func() {
		// As upstream: a tree registered after the failure (the base) is listed, but none is watched.
		if len(f.watcher.watched) != 0 || f.watcher.fs != nil {
			t.Errorf("watches = %v, want none", f.watcher.watched)
		}
	})
}
