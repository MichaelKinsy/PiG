package node

import (
	"os"
	"path/filepath"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// fsnotify's kqueue backend (macOS and the BSDs) names an event for a descriptor whose watch it already removed, such as a directory
// deleted under a recursive watch, with an empty path (and "." once cleaned). Every target is within such a path, so it reported the
// target itself, which Pi's daemon, on FSEvents, does not: on macOS the oracle's recursive-changes cases failed in about 1 of 3 runs on
// the change that removes a directory tree. The event is injected here because Linux's inotify never sends one.
func TestNodeFileWatcherReportsNoPathForAnEventWithoutAnAbsolutePath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "watched")
	mustDo(t, os.MkdirAll(filepath.Join(target, "sub"), 0o700))
	watcher, err := openFileWatcher([]durableenv.WatchTarget{{Path: target, Recursive: true}}, func(path string) string { return path }, func(durableenv.FileChange) {}, NodeWatchOptions{Mode: durableenv.WatchNative})
	mustDo(t, err)
	t.Cleanup(func() { _ = watcher.Close(background) })

	for _, name := range []string{"", "."} {
		watcher.onEvent(name)
	}
	watcher.mu.Lock()
	reported := len(watcher.events)
	watcher.mu.Unlock()
	if reported != 0 {
		t.Fatalf("events without an absolute path reported %d paths, want none", reported)
	}

	named := filepath.Join(watcher.targets[0].path, "sub")
	watcher.onEvent(named)
	watcher.mu.Lock()
	_, ok := watcher.events[named]
	watcher.mu.Unlock()
	if !ok {
		t.Fatalf("an event on %s reported nothing", named)
	}
}
