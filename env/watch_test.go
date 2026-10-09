package env

// Beyond packages/env/test: docs/protocol.md and docs/semantics.md say a watcher "opens the watcher again once a new
// daemon starts and reports overflow" after the connection was lost, which Pi's tests leave unchecked.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

type changes struct {
	mu    sync.Mutex
	items []durableenv.WatchChange
}

func (c *changes) add(change durableenv.WatchChange) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = append(c.items, change)
}

// waitFor returns once an item satisfies match, scanning from index start; it reports the index after it.
func (c *changes) waitFor(t *testing.T, start int, within time.Duration, match func(durableenv.WatchChange) bool) int {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		for index := start; index < len(c.items); index++ {
			if match(c.items[index]) {
				c.mu.Unlock()
				return index + 1
			}
		}
		c.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	t.Fatalf("no matching change within %s; saw %v from %d", within, c.items, start)
	return 0
}

func isOverflow(change durableenv.WatchChange) bool {
	_, ok := change.(durableenv.WatchChangeOverflow)
	return ok
}

func pathsInclude(path string) func(durableenv.WatchChange) bool {
	return func(change durableenv.WatchChange) bool {
		paths, ok := change.(durableenv.WatchChangePaths)
		if !ok {
			return false
		}
		return slices.Contains(paths.Paths, path)
	}
}

func TestRemoteWatcherOpensAgainAfterALostConnectionAndReportsOverflow(t *testing.T) {
	env, connection := remoteEnvironment(t)
	ctx := context.Background()
	// remote-env.ts #resolve reports the watched path as given, so a symlinked temp root (macOS /var -> /private/var) stays unresolved.
	root := env.Cwd()
	var seen changes
	// upstream: packages/env/src/remote-env.ts:809 watch opens a daemon watcher and opens it again after a lost connection.
	watcher := must(env.Watch(ctx, []durableenv.WatchTarget{{Path: "."}}, seen.add))
	defer func() { _ = watcher.Close(ctx) }()
	if watcher.Mode() != durableenv.WatchNative && watcher.Mode() != durableenv.WatchPolling {
		t.Fatalf("mode %q", watcher.Mode())
	}
	mustDo(env.WriteFile(ctx, "before.txt", "1"))
	next := seen.waitFor(t, 0, 10*time.Second, pathsInclude(filepath.Join(root, "before.txt")))

	// Kill the daemon: the connection is lost, and the watcher opens again once a daemon can start.
	info := must(connection.Info(ctx))
	mustDo(must(os.FindProcess(info.Pid)).Kill())
	next = seen.waitFor(t, next, 20*time.Second, isOverflow)
	if again := must(connection.Info(ctx)); again.Pid == info.Pid {
		t.Fatalf("the daemon was not started again: pid %d", again.Pid)
	}
	// The new watcher reports later changes.
	mustDo(env.WriteFile(ctx, "after.txt", "2"))
	seen.waitFor(t, next, 20*time.Second, pathsInclude(filepath.Join(root, "after.txt")))

	// A closed watcher reports nothing, whatever the connection does.
	mustDo(watcher.Close(ctx))
	seen.mu.Lock()
	count := len(seen.items)
	seen.mu.Unlock()
	mustDo(env.WriteFile(ctx, "closed.txt", "3"))
	time.Sleep(500 * time.Millisecond)
	seen.mu.Lock()
	defer seen.mu.Unlock()
	if len(seen.items) != count {
		t.Fatalf("a closed watcher reported %v", seen.items[count:])
	}
}

// Pi source: packages/env/src/remote-env.ts, packages/env/src/watch.ts
// mutation-checked: dropping the reads and writes of RemoteExecutionEnv.Connection, RemoteWatchOptions.MaxDirectories fails it
// Pi: packages/env/src/remote-env.ts:29 (connection)
// Pi: packages/env/src/watch.ts:13 (maxDirectories)
// packages/env/src/watch.ts:13 `maxDirectories` caps the directories one watch opens; packages/env/src/remote-env.ts:354 the environment reuses its `connection`.
// packages/env/src/watch.ts:13: RemoteWatchOptions.maxDirectories bounds a watch; remote-env.ts:750 createDir builds the tree and connection.ts reaches the daemon.
func TestRemoteWatcherFailsToOpenOverTheDirectoryBudgetLikeNodeFileWatcher(t *testing.T) {
	env, _ := remoteEnvironment(t)
	ctx := context.Background()
	// A budget below the tree: opening fails with an invalid FileError and no watcher is left behind.
	small := NewRemoteExecutionEnv(RemoteExecutionEnvOptions{Connection: env.Connection, ID: "pi-env:test", Cwd: env.Cwd(), Watch: RemoteWatchOptions{MaxDirectories: new(2)}})
	mustDo(small.CreateDir(ctx, "tree/a/b", nil))
	mustDo(small.CreateDir(ctx, "tree/c", nil))
	_, err := small.Watch(ctx, []durableenv.WatchTarget{{Path: "tree", Recursive: true}}, func(durableenv.WatchChange) {})
	if code := errorCodeOf(err); code != "invalid" {
		t.Fatalf("watch of a tree over the budget: code %s (%v), want invalid", code, err)
	}
}

// packages/env/src/watch.ts:93 spreads the options into the watch request, so a set maxDirectories of 0 reaches the daemon, which
// takes it as the limit (daemon/src/watch.rs:725-727, :285-286): every directory is over the budget. Only an unset limit is the default.
func TestRemoteWatcherSendsASetZeroDirectoryBudget(t *testing.T) {
	env, _ := remoteEnvironment(t)
	ctx := context.Background()
	zero := NewRemoteExecutionEnv(RemoteExecutionEnvOptions{Connection: env.Connection, ID: "pi-env:test", Cwd: env.Cwd(), Watch: RemoteWatchOptions{MaxDirectories: new(0)}})
	mustDo(zero.CreateDir(ctx, "tree", nil))
	_, err := zero.Watch(ctx, []durableenv.WatchTarget{{Path: "tree", Recursive: true}}, func(durableenv.WatchChange) {})
	if code := errorCodeOf(err); code != "invalid" {
		t.Fatalf("watch with a budget of 0 directories: code %s (%v), want invalid", code, err)
	}
	watcher, err := env.Watch(ctx, []durableenv.WatchTarget{{Path: "tree", Recursive: true}}, func(durableenv.WatchChange) {})
	if err != nil {
		t.Fatalf("an unset budget is the daemon default: %v", err)
	}
	_ = watcher.Close(ctx)
}
