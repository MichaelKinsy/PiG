package node

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// When no native watch can be created (out of inotify instances), the watcher polls and reports overflow, as node-watch.ts does when its first fs.watch throws: #reconcileWatchers catches the error (node-watch.ts:458-462) and #switchToPolling delivers { overflow: true } (node-watch.ts:469-478), during open. Changes are then found by polling.
func TestNodeFileWatcherReportsOverflowWhenNoNativeWatchCanBeCreated(t *testing.T) {
	saved := newNativeWatcher
	newNativeWatcher = func() (*fsnotify.Watcher, error) { return nil, errors.New("too many open files") }
	t.Cleanup(func() { newNativeWatcher = saved })

	env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir(), Watch: NodeWatchOptions{Mode: durableenv.WatchNative, PollIntervalMs: new(30)}})
	mustDo(t, env.WriteFile(background, "watched.txt", "one"))
	var mu sync.Mutex
	var changes []durableenv.WatchChange
	changed := make(chan struct{}, 8)
	watcher := must(env.Watch(background, []durableenv.WatchTarget{{Path: "."}}, func(change durableenv.WatchChange) {
		mu.Lock()
		changes = append(changes, change)
		mu.Unlock()
		if _, ok := change.(durableenv.WatchChangePaths); ok {
			changed <- struct{}{}
		}
	}))
	defer func() { mustDo(t, watcher.Close(background)) }()
	mu.Lock()
	opened := append([]durableenv.WatchChange(nil), changes...)
	mu.Unlock()
	if len(opened) != 1 {
		t.Fatalf("open delivered %#v, want one overflow", opened)
	}
	if _, ok := opened[0].(durableenv.WatchChangeOverflow); !ok {
		t.Fatalf("open delivered %#v, want an overflow", opened[0])
	}
	if got := watcher.Mode(); got != durableenv.WatchPolling {
		t.Fatalf("Mode() = %q, want polling", got)
	}
	mustDo(t, env.WriteFile(background, "watched.txt", "two, longer"))
	select {
	case <-changed:
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("polling did not report the change")
	}
}
