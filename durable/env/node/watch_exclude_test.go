package node

import (
	"slices"
	"sync"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// upstream: packages/durable/src/env/node-watch.ts excluded()/scan: a recursive watch skips names starting with "." when exclude.hidden is set and names listed in exclude.names, at every depth, and reports every other change.
// Pi source: packages/durable/src/env/node-watch.ts (excluded)
// mutation-checked: dropping the hidden or the names predicate each fail it
// mutation-checked: dropping the reads and writes of WatchTarget.Exclude fails it
// Pi: packages/durable/src/env/index.ts:116 (exclude)
func TestWatchTargetExcludeSkipsHiddenAndNamedEntries(t *testing.T) {
	env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir()})
	mustDo(t, env.CreateDir(background, "tree/nested", nil))
	var mu sync.Mutex
	var reported []string
	watcher := must(env.Watch(background, []durableenv.WatchTarget{{Path: "tree", Recursive: true, Exclude: &durableenv.WatchExclude{Hidden: true, Names: []string{"skip"}}}}, func(change durableenv.WatchChange) {
		if paths, ok := change.(durableenv.WatchChangePaths); ok {
			mu.Lock()
			reported = append(reported, paths.Paths...)
			mu.Unlock()
		}
	}))
	defer func() { _ = watcher.Close(background) }()
	has := func(suffix string) bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.ContainsFunc(reported, func(path string) bool { return len(path) >= len(suffix) && path[len(path)-len(suffix):] == suffix })
	}
	wait := func(suffix string) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !has(suffix); time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				mu.Lock()
				defer mu.Unlock()
				t.Fatalf("no change reported for %s; reported %q", suffix, reported)
			}
		}
	}
	for _, name := range []string{"tree/.hidden", "tree/skip", "tree/nested/.dot", "tree/nested/skip"} {
		mustDo(t, env.WriteFile(background, name, "x"))
	}
	mustDo(t, env.WriteFile(background, "tree/kept.txt", "x"))
	wait("kept.txt")
	mustDo(t, env.WriteFile(background, "tree/nested/deep.txt", "x"))
	wait("deep.txt")
	mu.Lock()
	defer mu.Unlock()
	for _, path := range reported {
		for _, excluded := range []string{".hidden", "skip", ".dot"} {
			if len(path) >= len(excluded) && path[len(path)-len(excluded):] == excluded {
				t.Fatalf("reported excluded entry %s (all: %q)", path, reported)
			}
		}
	}
}
