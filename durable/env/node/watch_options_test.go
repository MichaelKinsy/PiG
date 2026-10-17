package node

import (
	"sync"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// NodeWatchOptions.Mode forces the watcher's mode, and PollIntervalMs sets the snapshot interval of polling mode (the default is 2000 ms).
// Pi: packages/durable/src/env/node-watch.ts:14 (mode), packages/durable/src/env/node-watch.ts:16 (pollIntervalMs), packages/durable/src/env/node-watch.ts:182 (mode)
// mutation-checked: ignoring Mode, or ignoring PollIntervalMs (a change then waits for the 2 s default), fails it
func TestNodeWatchOptionsForceTheModeAndSetThePollInterval(t *testing.T) {
	for _, mode := range []durableenv.WatchMode{durableenv.WatchNative, durableenv.WatchPolling} {
		env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir(), Watch: NodeWatchOptions{Mode: mode, PollIntervalMs: new(30)}})
		mustDo(t, env.WriteFile(background, "watched.txt", "one"))
		var mu sync.Mutex
		changed := make(chan struct{}, 8)
		watcher := must(env.Watch(background, []durableenv.WatchTarget{{Path: "watched.txt"}}, func(change durableenv.WatchChange) {
			mu.Lock()
			defer mu.Unlock()
			if _, ok := change.(durableenv.WatchChangePaths); ok {
				changed <- struct{}{}
			}
		}))
		if got := watcher.Mode(); got != mode {
			t.Fatalf("Mode %q: the watcher runs in %q", mode, got)
		}
		if mode == durableenv.WatchPolling {
			mustDo(t, env.WriteFile(background, "watched.txt", "two, longer"))
			select {
			case <-changed:
			case <-time.After(1500 * time.Millisecond):
				t.Fatal("a 30 ms poll interval must report the change well before the 2 s default")
			}
		}
		mustDo(t, watcher.Close(background))
	}
}
