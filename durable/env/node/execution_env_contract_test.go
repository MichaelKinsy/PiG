package node

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// The local environment satisfies the portable ExecutionEnv; these checks go through the interface the way a conversation's tools do.
// Pi packages/durable/src/env/index.ts:171 FileSystem (id :176, absolutePath :178, joinPath :179, watch :213, createTempDir :230,
// createTempFile :231, cleanup :235) as NodeExecutionEnv implements it: one id "node:local" (env/node.ts:605), absolutePath resolves
// against the cwd (node.ts:644), joinPath joins the parts (node.ts:648), createTempDir and createTempFile create what they name with
// the prefix and suffix (node.ts:1195, node.ts:1206), and cleanup kills the running commands without removing files (node.ts:1221).
func TestExecutionEnvFileSystemContract(t *testing.T) {
	local, root := newTestEnv(t)
	var fs durableenv.ExecutionEnv = local

	// id: every local environment shares one id, whatever its cwd.
	other := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir()})
	if fs.Id() == "" || fs.Id() != other.Id() {
		t.Fatalf("ids %q and %q must be equal and non-empty for local environments", fs.Id(), other.Id())
	}

	// absolutePath resolves against the cwd; joinPath joins with the platform separator.
	abs, err := fs.AbsolutePath(background, "sub/file.txt")
	if err != nil || abs != filepath.Join(root, "sub", "file.txt") {
		t.Fatalf("AbsolutePath = %q, %v", abs, err)
	}
	joined, err := fs.JoinPath(background, []string{"a", "b", "c.txt"})
	if err != nil || joined != filepath.Join("a", "b", "c.txt") {
		t.Fatalf("JoinPath = %q, %v", joined, err)
	}

	// createTempDir and createTempFile create what they name, with the requested prefix and suffix, and cleanup removes them.
	prefix := "contract-"
	dir, err := fs.CreateTempDir(background, &prefix)
	if err != nil {
		t.Fatal(err)
	}
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() || !strings.HasPrefix(filepath.Base(dir), prefix) {
		t.Fatalf("temp dir %q: %v", dir, statErr)
	}
	file, err := fs.CreateTempFile(background, &durableenv.CreateTempFileOptions{Prefix: "note-", Suffix: ".txt"})
	if err != nil {
		t.Fatal(err)
	}
	if info, statErr := os.Stat(file); statErr != nil || info.IsDir() || !strings.HasPrefix(filepath.Base(file), "note-") || !strings.HasSuffix(file, ".txt") {
		t.Fatalf("temp file %q: %v", file, statErr)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir); _ = os.RemoveAll(filepath.Dir(file)) })

	// cleanup kills the commands the environment still runs (it removes no files): durable/src/env/index.ts:321 ShellEnv.cleanup "Kill every command this environment still runs", index.ts:235 FileSystem.cleanup, node.ts:1221 cleanup kills each active child process tree and forgets it.
	if runtime.GOOS != "windows" {
		done := make(chan error, 1)
		go func() {
			result, err := fs.Exec(background, "sleep 60", nil)
			if err == nil && result.ExitCode == 0 {
				err = errors.New("a killed command reported success")
			}
			done <- err
		}()
		time.Sleep(300 * time.Millisecond)
		if err := fs.Cleanup(background); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("Cleanup did not stop the running command")
		}
	}
	for _, path := range []string{dir, file} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Errorf("Cleanup must not remove temp files: %v", statErr)
		}
	}

	// watch reports a change below a watched directory and stops after Close.
	changed := make(chan durableenv.WatchChange, 16)
	watcher, err := fs.Watch(background, []durableenv.WatchTarget{{Path: root, Recursive: true}}, func(change durableenv.WatchChange) { changed <- change })
	if err != nil {
		t.Fatal(err)
	}
	if mode := watcher.Mode(); mode != durableenv.WatchNative && mode != durableenv.WatchPolling {
		t.Fatalf("mode = %q", mode)
	}
	if err := os.WriteFile(filepath.Join(root, "watched.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case change := <-changed:
		if paths, ok := change.(durableenv.WatchChangePaths); ok && len(paths.Paths) == 0 {
			t.Fatalf("a path change named no path: %+v", change)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no change reported for a file created under a watched directory")
	}
	if err := watcher.Close(background); err != nil {
		t.Fatal(err)
	}
	if err := watcher.Close(background); err != nil {
		t.Fatalf("Close must be idempotent: %v", err)
	}
}

// Results carry a value or the failure; GetOrThrow turns a failure into a Go error.
func TestEnvResultErrAndOk(t *testing.T) {
	failure := &durableenv.FileError{Code: "not_found", Message: "missing"}
	failed := durableenv.Err[string, *durableenv.FileError](failure)
	if failed.Ok || failed.Error != failure {
		t.Fatalf("Err = %+v", failed)
	}
	// Identity, not errors.Is: Pi getOrThrow throws result.error itself (packages/durable/src/env/index.ts:15), not a wrapper.
	if _, err := durableenv.GetOrThrow(failed); err != error(failure) { //nolint:errorlint // the same error value is the contract
		t.Fatalf("GetOrThrow(Err) = %v, want the failure", err)
	}
	ok := durableenv.Ok[string, *durableenv.FileError]("value")
	if value, err := durableenv.GetOrThrow(ok); !ok.Ok || err != nil || value != "value" {
		t.Fatalf("Ok = %+v, GetOrThrow = %q, %v", ok, value, err)
	}
}
