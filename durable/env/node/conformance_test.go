// Ports packages/durable/test/env-node-conformance.test.ts.

package node

// pi: packages/durable/src/env/node-watch.ts

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable/durabletest"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// gitBash is the shell the conformance cases use on Windows, where `sh` is not a program.
func conformanceShell() ([]string, bool) {
	if runtime.GOOS != "windows" {
		return nil, false
	}
	programFiles := os.Getenv("ProgramFiles")
	if programFiles == "" {
		programFiles = `C:\Program Files`
	}
	// Git Bash's `ln -s` copies instead of linking unless native symlinks are enabled.
	return []string{filepath.Join(programFiles, "Git", "bin", "bash.exe"), "-c"}, true
}

func withTestEnv(t *testing.T, options NodeExecutionEnvOptions) durabletest.EnvConformanceProvider {
	return func(use func(durableenv.ExecutionEnv) error) error {
		options.Cwd = t.TempDir()
		return use(NewNodeExecutionEnv(options))
	}
}

func TestNodeExecutionEnvConformance(t *testing.T) {
	shell, noSymlinks := conformanceShell()
	// upstream: packages/durable/test/env-node-conformance.test.ts:33
	durabletest.RegisterEnvConformance(t, "NodeExecutionEnv conformance", func(use func(durableenv.ExecutionEnv) error) error {
		return withTestEnv(t, NodeExecutionEnvOptions{})(use)
	}, durabletest.EnvConformanceRegisterOptions{Shell: shell, Symlinks: new(!noSymlinks)})
	// upstream: packages/durable/test/env-node-conformance.test.ts:48
	durabletest.RegisterEnvConformance(t, "NodeExecutionEnv conformance with polling watches", func(use func(durableenv.ExecutionEnv) error) error {
		return withTestEnv(t, NodeExecutionEnvOptions{Watch: NodeWatchOptions{Mode: durableenv.WatchPolling, PollIntervalMs: new(100)}})(use)
	}, durabletest.EnvConformanceRegisterOptions{Shell: shell, Symlinks: new(!noSymlinks)})
}

// mutation-checked: zeroing the results of NodeExecutionEnv.Watch fails it
// mutation-checked: dropping the reads and writes of NodeWatchOptions.MaxDirectories, WatchTarget.Path, WatchTarget.Recursive fails it
func TestNodeExecutionEnvWatchLimits(t *testing.T) {
	t.Run("refuses a tree over the directory budget and stops with an error when one grows past it", func(t *testing.T) {
		// upstream: packages/durable/test/env-node-conformance.test.ts:63
		env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir(), Watch: NodeWatchOptions{MaxDirectories: new(3)}})
		mustDo(t, env.CreateDir(background, "tree/a/b", nil))
		mustDo(t, env.CreateDir(background, "tree/c", nil))
		_, err := env.Watch(background, []durableenv.WatchTarget{{Path: "tree", Recursive: true}}, func(durableenv.WatchChange) {})
		if fileErr, ok := errors.AsType[*durableenv.FileError](err); !ok || fileErr.Code != durableenv.FileErrorInvalid {
			t.Fatalf("Watch error = %v, want an invalid FileError", err)
		}

		mustDo(t, env.Remove(background, "tree/c", &durableenv.RemoveOptions{Recursive: true}))
		changes := make(chan durableenv.WatchChange, 100)
		watcher := must(env.Watch(background, []durableenv.WatchTarget{{Path: "tree", Recursive: true}}, func(change durableenv.WatchChange) { changes <- change }))
		defer func() { _ = watcher.Close(background) }()
		mustDo(t, env.CreateDir(background, "tree/d", nil))
		deadline := time.After(3 * time.Second)
		for {
			select {
			case change := <-changes:
				if failure, ok := change.(durableenv.WatchChangeError); ok {
					if failure.Error.Code != durableenv.FileErrorInvalid {
						t.Fatalf("error code = %s, want invalid", failure.Error.Code)
					}
					return
				}
			case <-deadline:
				t.Fatal("the watcher did not stop with an error")
			}
		}
	})
}

// packages/durable/src/env/node-watch.ts (1.0.4) fails a watch with permission_denied when a target, or a watched
// directory itself, cannot be read for lack of permission. Upstream has no test for it.
// packages/durable/src/env/node-watch.ts:14-16: NodeWatchOptions.mode forces native or polling and pollIntervalMs sets the polling interval.
func TestNodeExecutionEnvWatchPermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits do not deny this process")
	}
	for _, mode := range []durableenv.WatchMode{durableenv.WatchNative, durableenv.WatchPolling} {
		t.Run(string(mode), func(t *testing.T) {
			_, root := newTestEnv(t)
			env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root, Watch: NodeWatchOptions{Mode: mode, PollIntervalMs: new(100)}})
			mustDo(t, os.MkdirAll(filepath.Join(root, "locked", "inner"), 0o700))
			mustDo(t, os.Chmod(filepath.Join(root, "locked"), 0))
			t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "locked"), 0o700) })
			// The target itself cannot be stat'ed.
			_, err := env.Watch(background, []durableenv.WatchTarget{{Path: "locked/inner"}}, func(durableenv.WatchChange) {})
			if fileErr, ok := errors.AsType[*durableenv.FileError](err); !ok || fileErr.Code != durableenv.FileErrorPermissionDenied {
				t.Fatalf("Watch error = %v, want a permission_denied FileError", err)
			}
			// A target directory that cannot be listed.
			mustDo(t, os.Chmod(filepath.Join(root, "locked"), 0o700))
			mustDo(t, os.Chmod(filepath.Join(root, "locked", "inner"), 0))
			t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "locked", "inner"), 0o700) })
			_, err = env.Watch(background, []durableenv.WatchTarget{{Path: "locked/inner"}}, func(durableenv.WatchChange) {})
			if fileErr, ok := errors.AsType[*durableenv.FileError](err); !ok || fileErr.Code != durableenv.FileErrorPermissionDenied {
				t.Fatalf("Watch error = %v, want a permission_denied FileError", err)
			}
		})
	}
}

// mutation-checked: zeroing the results of NodeExecutionEnv.OpenBinaryReader fails it
// mutation-checked: dropping the reads and writes of FileError.Code fails it
// mutation-checked: negating the condition at binary_reader.go:81 or :94 fails it.
func TestNodeExecutionEnvReaders(t *testing.T) {
	t.Run("reads ranges spanning several internal chunks exactly", func(t *testing.T) {
		// upstream: packages/durable/test/env-node-conformance.test.ts:103
		env, root := newTestEnv(t)
		data := make([]byte, 5*1024*1024/2)
		for index := range data {
			data[index] = byte(index * 31 % 251)
		}
		mustDo(t, os.WriteFile(filepath.Join(root, "big.bin"), data, 0o600))
		reader := must(env.OpenBinaryReader(background, "big.bin", nil))
		defer func() { _ = reader.Close(background) }()
		all := must(reader.Read(background, 0, int64(len(data))+10))
		if !bytes.Equal(all, data) {
			t.Fatalf("read %d bytes, want the %d bytes of the file", len(all), len(data))
		}
		middle := must(reader.Read(background, 1024*1024-3, 7))
		if !bytes.Equal(middle, data[1024*1024-3:1024*1024+4]) {
			t.Fatalf("middle = %v", middle)
		}
	})

	t.Run("refuses a FIFO without waiting for a writer", func(t *testing.T) {
		// upstream: packages/durable/test/env-node-conformance.test.ts:120
		if runtime.GOOS == "windows" {
			t.Skip("no FIFOs on Windows")
		}
		env, root := newTestEnv(t)
		if output, err := exec.Command("mkfifo", filepath.Join(root, "pipe")).CombinedOutput(); err != nil {
			t.Fatalf("mkfifo: %v: %s", err, output)
		}
		done := make(chan error, 1)
		go func() {
			_, err := env.OpenBinaryReader(background, "pipe", nil)
			done <- err
		}()
		select {
		case err := <-done:
			if fileErr, ok := errors.AsType[*durableenv.FileError](err); !ok || fileErr.Code != durableenv.FileErrorInvalid {
				t.Fatalf("error = %v, want an invalid FileError", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("opening a FIFO waited for a writer")
		}
	})
}
