package node

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// Pi durable/src/env/node.ts NodeExecutionEnv.id is the stable environment identity.
func TestNodeExecutionEnvIdUpstream(t *testing.T) {
	env, _ := newTestEnv(t)
	if env.Id() != "node:local" {
		t.Fatalf("Id = %q", env.Id())
	}
}

// Pi ShellExecOptions.cwd: a command runs in the option's directory instead of the environment's cwd.
func TestShellExecutesInTheCwdOption(t *testing.T) {
	skipOnWindows(t)
	env, root := newTestEnv(t)
	mustDo(t, os.MkdirAll(filepath.Join(root, "sub"), 0o700))
	_, output, err := collectShellOutput(env, `pwd`, &durableenv.ShellExecOptions{Cwd: filepath.Join(root, "sub")}, background)
	mustDo(t, err)
	canonical, evalErr := filepath.EvalSymlinks(filepath.Join(root, "sub"))
	mustDo(t, evalErr)
	if strings.TrimSpace(output) != canonical {
		t.Fatalf("pwd = %q, want %q", output, canonical)
	}
}

// Pi NodeExecutionEnv.openDirReader pages a directory: every entry is returned once across pages and Done ends the scan.
func TestNodeExecutionEnvOpenDirReaderPagesEntries(t *testing.T) {
	env, root := newTestEnv(t)
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		mustDo(t, os.WriteFile(filepath.Join(root, name), []byte(name), 0o600))
	}
	reader, err := env.OpenDirReader(background, ".")
	mustDo(t, err)
	defer func() { _ = reader.Close(background) }()
	var names []string
	for range 10 {
		page, nextErr := reader.Next(background, 2)
		mustDo(t, nextErr)
		if len(page.Entries) > 2 {
			t.Fatalf("page has %d entries, want at most 2", len(page.Entries))
		}
		for _, entry := range page.Entries {
			names = append(names, entry.Name)
		}
		if page.Done {
			break
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"a.txt", "b.txt", "c.txt"}) {
		t.Fatalf("names = %v", names)
	}
}

// Pi WatchTarget.exclude: names and hidden entries below a recursive target do not report changes.
func TestWatchTargetExcludeSuppressesNamedAndHiddenChanges(t *testing.T) {
	env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir(), Watch: NodeWatchOptions{Mode: durableenv.WatchPolling, PollIntervalMs: new(20)}})
	mustDo(t, env.CreateDir(background, "tree", nil))
	changes := make(chan durableenv.WatchChange, 100)
	watcher, err := env.Watch(background, []durableenv.WatchTarget{{
		Path: "tree", Recursive: true, Exclude: &durableenv.WatchExclude{Hidden: true, Names: []string{"skip.txt"}},
	}}, func(change durableenv.WatchChange) { changes <- change })
	mustDo(t, err)
	defer func() { _ = watcher.Close(background) }()

	mustDo(t, env.WriteFile(background, "tree/skip.txt", "x"))
	mustDo(t, env.WriteFile(background, "tree/.hidden", "x"))
	mustDo(t, env.WriteFile(background, "tree/keep.txt", "x"))

	deadline := time.After(5 * time.Second)
	for {
		select {
		case change := <-changes:
			paths, ok := change.(durableenv.WatchChangePaths)
			if !ok {
				continue
			}
			for _, path := range paths.Paths {
				if strings.HasSuffix(path, "skip.txt") || strings.HasSuffix(path, ".hidden") {
					t.Fatalf("excluded path reported: %q", path)
				}
				if strings.HasSuffix(path, "keep.txt") {
					return
				}
			}
		case <-deadline:
			t.Fatal("the non-excluded file never reported a change")
		}
	}
}

// Pi packages/durable/src/env/node-watch.ts:182 NodeWatchOptions.mode: a forced mode is the watcher's mode (node-watch.ts:199),
// whichever mode the platform would choose by default.
func TestNodeWatchOptionsModeForcesTheWatcherMode(t *testing.T) {
	for _, mode := range []durableenv.WatchMode{durableenv.WatchNative, durableenv.WatchPolling} {
		env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir(), Watch: NodeWatchOptions{Mode: mode}})
		watcher, err := env.Watch(background, []durableenv.WatchTarget{{Path: "."}}, func(durableenv.WatchChange) {})
		mustDo(t, err)
		if got := watcher.Mode(); got != mode {
			t.Errorf("forced mode %q: watcher mode = %q", mode, got)
		}
		mustDo(t, watcher.Close(background))
	}
}
