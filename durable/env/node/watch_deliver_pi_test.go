package node

import (
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// TestWatcherDeliversOnlyWhileOpenAndSurvivesAThrowingCallback ports node-watch.ts #deliver (:216-223): `if (this.#closed) return;` gates every
// change kind (paths, overflow, error), and a throwing onChange is swallowed so the watch keeps reporting ("A throwing callback must not stop
// watching"). This is the FileWatcher contract of packages/durable/src/env/index.ts:136: no onChange call starts after Close.
func TestWatcherDeliversOnlyWhileOpenAndSurvivesAThrowingCallback(t *testing.T) {
	env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir()})
	mustDo(t, env.CreateDir(background, "tree", nil))
	var got []durableenv.WatchChange
	throw := false
	watcher := must(env.Watch(background, []durableenv.WatchTarget{{Path: "tree", Recursive: true}}, func(change durableenv.WatchChange) {
		got = append(got, change)
		if throw {
			panic("callback failure")
		}
	})).(*nodeFileWatcher)
	changes := []durableenv.WatchChange{
		durableenv.WatchChangePaths{Paths: []string{"tree/a"}},
		durableenv.WatchChangeOverflow{},
		durableenv.WatchChangeError{Error: &durableenv.FileError{Code: "unknown", Message: "boom"}},
	}

	for _, change := range changes {
		watcher.deliver(change)
	}
	if len(got) != len(changes) {
		t.Fatalf("an open watcher delivered %d of %d changes", len(got), len(changes))
	}

	throw = true
	got = nil
	watcher.deliver(changes[0])
	throw = false
	watcher.deliver(changes[1])
	if len(got) != 2 {
		t.Fatalf("after a throwing callback the watcher delivered %d changes, want the throwing one and the next", len(got))
	}

	mustDo(t, watcher.Close(background))
	got = nil
	for _, change := range changes {
		watcher.deliver(change)
	}
	if len(got) != 0 {
		t.Fatalf("a closed watcher delivered %d change(s), want none", len(got))
	}
}
