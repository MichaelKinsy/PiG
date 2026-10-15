package node

import (
	"sync/atomic"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// TestFileWatcherCloseIsIdempotentAndEndsReporting ports the FileWatcher contract of packages/durable/src/env/index.ts:136 ("Stop watching; no
// onChange call starts after this resolves. Idempotent.") over node-watch.ts's #closed gates (:204-220): a change made while the watch runs
// is reported, a second Close is a no-op that succeeds, and a change made after Close is never reported.
func TestFileWatcherCloseIsIdempotentAndEndsReporting(t *testing.T) {
	env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir()})
	mustDo(t, env.CreateDir(background, "tree", nil))
	var reported atomic.Int32
	watcher := must(env.Watch(background, []durableenv.WatchTarget{{Path: "tree", Recursive: true}}, func(change durableenv.WatchChange) {
		if _, ok := change.(durableenv.WatchChangePaths); ok {
			reported.Add(1)
		}
	}))
	mustDo(t, env.WriteFile(background, "tree/before.txt", "x"))
	for deadline := time.Now().Add(5 * time.Second); reported.Load() == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("a change made while watching was never reported")
		}
	}
	mustDo(t, watcher.Close(background))
	mustDo(t, watcher.Close(background))
	seen := reported.Load()
	mustDo(t, env.WriteFile(background, "tree/after.txt", "x"))
	time.Sleep(500 * time.Millisecond)
	if got := reported.Load(); got != seen {
		t.Fatalf("%d change(s) were reported after Close", got-seen)
	}
}
