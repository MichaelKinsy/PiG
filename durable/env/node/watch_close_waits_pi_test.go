package node

import (
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// node-watch.ts close (:203-207): `if (this.#closed) return; this.#stop(); await this.#running;`. Close through the FileWatcher interface returns only after the rescan in progress has ended, so no rescan work outlives it.
// mutation-checked: not waiting on the running rescan returns at once and fails it.
func TestFileWatcherCloseWaitsForTheRescanInProgress(t *testing.T) {
	env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir()})
	mustDo(t, env.CreateDir(background, "tree", nil))
	watcher := must(env.Watch(background, []durableenv.WatchTarget{{Path: "tree", Recursive: true}}, func(durableenv.WatchChange) {}))
	node := watcher.(*nodeFileWatcher)
	rescan := make(chan struct{})
	node.mu.Lock()
	node.running = rescan
	node.mu.Unlock()
	closed := make(chan error, 1)
	go func() { closed <- watcher.Close(background) }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned (%v) while a rescan was still running", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(rescan)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the rescan ended")
	}
}
