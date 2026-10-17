package node

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// A target that is a symbolic link to a file reports changes to that file whatever its native watcher's events are
// named: upstream's #onLinkedFileEvent ignores the event's filename (packages/durable/src/env/node-watch.ts). fsnotify's
// kqueue backend (macOS and the BSDs) registers such a watch under the path with every link resolved and names its
// events by that path, so the watcher maps that path back to the target. The event is injected here because Linux's
// inotify names it by the link.
func TestNodeFileWatcherReportsALinkedTargetForAnEventNamedByTheResolvedFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows, which also polls")
	}
	root := t.TempDir()
	mustDo(t, os.MkdirAll(filepath.Join(root, "data"), 0o700))
	mustDo(t, os.MkdirAll(filepath.Join(root, "config"), 0o700))
	mustDo(t, os.WriteFile(filepath.Join(root, "data", "real.md"), []byte("one"), 0o600))
	link := filepath.Join(root, "config", "AGENTS.md")
	testenv.Symlink(t, filepath.Join("..", "data", "real.md"), link)
	resolved, err := filepath.EvalSymlinks(link)
	mustDo(t, err)

	var mu sync.Mutex
	var reported []string
	watcher, err := openFileWatcher([]durableenv.WatchTarget{{Path: link}}, func(path string) string { return path }, func(change durableenv.WatchChange) {
		if paths, ok := change.(durableenv.WatchChangePaths); ok {
			mu.Lock()
			reported = append(reported, paths.Paths...)
			mu.Unlock()
		}
	}, NodeWatchOptions{Mode: durableenv.WatchNative})
	mustDo(t, err)
	t.Cleanup(func() { _ = watcher.Close(background) })
	if watcher.Mode() != durableenv.WatchNative {
		t.Skip("no native watcher available")
	}

	watcher.onEvent(resolved)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := slices.Clone(reported)
		mu.Unlock()
		if slices.Contains(got, link) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reported %q after an event on %s, want the linked target %s", got, resolved, link)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Replacing the link with a plain file drops the alias: the old file no longer reports the target.
	mustDo(t, os.Remove(link))
	mustDo(t, os.WriteFile(link, []byte("plain"), 0o600))
	<-watcher.flush()
	watcher.mu.Lock()
	_, aliased := watcher.linkAliases[resolved]
	watcher.mu.Unlock()
	if aliased {
		t.Fatalf("the alias %s outlived the link it resolved", resolved)
	}
}
