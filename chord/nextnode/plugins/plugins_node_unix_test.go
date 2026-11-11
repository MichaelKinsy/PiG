//go:build !windows

package plugins

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
)

// Not upstream's: the parts of the start tests (plugins_node_start_test.go) and the file tests that only a unix has: a pid
// probe, a `node` that is a shell script, the socket directory of the listener, a file that has no permission.

// processGone reports whether pid is no process any more: the one a failed start killed was waited for, so it is not even a
// zombie.
func processGone(pid int) bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }

// endedStart is a NodeOptions whose process ends before the driver connects.
func endedStart(t *testing.T) NodeOptions {
	t.Helper()
	end := filepath.Join(t.TempDir(), "end.cjs")
	if err := os.WriteFile(end, []byte("process.exit(3);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return NodeOptions{CacheDir: t.TempDir(), ExecArgv: []string{"--require", end}}
}

// socketTemp makes TMPDIR a new empty directory and returns it. Its name is short on purpose: the listener's socket path lives
// under it, and a unix socket path of about 100 bytes or more cannot be bound, which a subtest's t.TempDir (named for the test)
// reaches. The test's own temporary root is made first, so the directories the test makes with t.TempDir stay out of the new one.
func socketTemp(t *testing.T) string {
	t.Helper()
	t.TempDir()
	dir, err := os.MkdirTemp("", "ns-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMPDIR", dir)
	return dir
}

// requireEmpty fails the test if dir has an entry.
func requireEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("left in TMPDIR: %v", entries)
	}
}

// Every failed start leaves no socket directory behind (the one the process was to connect to). Each start fails after the
// directory exists, by an event and not by a timer: a timer short enough to be quick fires before a loaded machine has started
// the process (or even found Node), so the test would wait for a pid that never comes.
func TestNodeAFailedStartRemovesItsSocketDirectory(t *testing.T) {
	load := func(options NodeOptions, ctx context.Context) error {
		_, _, err := NewPluginRuntime(options).Load(RuntimeLoad{Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal}, ctx)
		return err
	}
	t.Run("the process ends before it connects", func(t *testing.T) {
		tmp := socketTemp(t)
		err := load(endedStart(t), background)
		if err == nil || !strings.Contains(err.Error(), "ended before it connected") {
			t.Fatalf("error = %v", err)
		}
		requireEmpty(t, tmp)
	})
	t.Run("the start is aborted while the process runs", func(t *testing.T) {
		tmp := socketTemp(t)
		options, pids := blockedStart(t)
		ctx, cancel := chordctx.WithCancel(background)
		loaded := make(chan error, 1)
		go func() { loaded <- load(options, ctx) }()
		select {
		case <-pids:
		case err := <-loaded:
			t.Fatalf("the start ended before its process ran: %v", err)
		}
		cancel(errors.New("stop it"))
		if err := <-loaded; err == nil || !strings.Contains(err.Error(), "stop it") {
			t.Fatalf("error = %v, want the cause", err)
		}
		requireEmpty(t, tmp)
	})
}

// writeFakeNode writes a `node` into dir (made if need be) that prints stdout, writes stderr and exits with code, and returns
// its path.
func writeFakeNode(t *testing.T, dir, stdout, stderr string, code int) string {
	t.Helper()
	script := "#!/bin/sh\n"
	if stdout != "" {
		script += "echo " + shellQuote(stdout) + "\n"
	}
	if stderr != "" {
		script += "echo " + shellQuote(stderr) + " >&2\n"
	}
	script += "exit " + strconv.Itoa(code) + "\n"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "node")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeNode puts a `node` on PATH (alone, with no other place searched) that prints stdout, writes stderr and exits with code.
func fakeNode(t *testing.T, stdout, stderr string, code int) {
	t.Helper()
	isolateNodeSearch(t)
	t.Setenv("PATH", filepath.Dir(writeFakeNode(t, t.TempDir(), stdout, stderr, code)))
}

func shellQuote(text string) string { return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'" }

// A file the plugin imports and the build cannot read (here: forbidden) fails the build with Node's text, as Pi's build does
// (its `readFile`): `EACCES: permission denied, open '<path>'`, after the position of the import, where Pi puts the line of esbuild's own library (D222). (A user that can read every file reads this one too.) Unix only: on Windows `os.Chmod` only
// toggles the read-only attribute, which does not make a file unreadable.
func TestEsbuildBuildAForbiddenInputFailsTheBuildWithNodesText(t *testing.T) {
	root := tempRoot(t)
	dir := writePlugin(t, root, "locked", "@test/locked", map[string]string{
		"worker.ts": "import { a } from \"./locked.ts\";\nexport default { setup: () => a };",
		"locked.ts": "export const a = 1;",
	}, nil)
	locked := filepath.Join(dir, "locked.ts")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })
	if _, err := os.ReadFile(locked); err == nil {
		t.Fatal("this user reads a file with no permission: the case needs a user that does not")
	}
	plugins, plugin := esbuildDiscovered(t, "@test/locked", root)
	options := esbuildNode
	options.Plugins = plugins
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "worker", options)), "worker.ts:1:19: EACCES: permission denied, open '"+filepath.Join(plugin.Dir, "locked.ts")+"'")
}

// A Value waiting when the process dies ends with the process's exit, not also with a cancellation nobody asked for (the
// reads' context is the host's life, which ends right after the pending requests settle). A stopped process holds the read.
func TestNodeMemberValueInFlightWhenTheProcessDiesReturnsItsExitOnly(t *testing.T) {
	node := rfNode(t)
	ref := rfGet(t, node)
	if err := node.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := ref.Member("count").Value()
		result <- err
	}()
	eventually(t, 15*time.Second, "the read to be sent", func() bool {
		node.mu.Lock()
		defer node.mu.Unlock()
		return len(node.pending) > 0
	})
	node.kill()
	select {
	case err := <-result:
		if _, ok := errors.AsType[*NodeExit](err); !ok || errors.Is(err, context.Canceled) {
			t.Fatalf("Value = %v, want the *NodeExit alone (canceled: %v)", err, errors.Is(err, context.Canceled))
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the read did not return")
	}
}
