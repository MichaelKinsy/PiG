package node

import (
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// node-watch.ts close (:203-207): `if (this.#stopped) return; this.#stop(); await this.#running;`. Stop through the FileWatcher interface returns only after the rescan in progress has ended, so no rescan work outlives it.
// mutation-checked: not waiting on the running rescan returns at once and fails it.
func TestFileWatcherStopWaitsForTheRescanInProgress(t *testing.T) {
	env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir()})
	mustDo(t, env.CreateDir(background, "tree", nil))
	watcher := must(env.Watch(background, []durableenv.WatchTarget{{Path: "tree", Recursive: true}}, func(durableenv.FileChange) {}))
	node := watcher.(*nodeFileWatcher)
	rescan := make(chan struct{})
	node.mu.Lock()
	node.running = rescan
	node.mu.Unlock()
	stopped := make(chan error, 1)
	go func() { stopped <- watcher.Stop(background) }()
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned (%v) while a rescan was still running", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(rescan)
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after the rescan ended")
	}
}
