//go:build !windows

package plugins

import (
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

// Not upstream's: the start of the Node subprocess where the test needs a process that does not connect (a script `--require`d
// before the driver, which blocks it) or another `node` on PATH (a shell script): unix only.

// blockedStart is a NodeOptions whose process prints its pid and blocks before the driver connects: pids receives the pid
// once the process is running.
func blockedStart(t *testing.T) (NodeOptions, <-chan int) {
	t.Helper()
	block := filepath.Join(t.TempDir(), "block.cjs")
	script := `process.stdout.write("pid " + process.pid + "\n");
Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0);
`
	if err := os.WriteFile(block, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	pids := make(chan int, 1)
	return NodeOptions{
		CacheDir: t.TempDir(),
		ExecArgv: []string{"--require", block},
		OnOutput: func(stream NodeStream, chunk []byte) {
			if stream != NodeStdout {
				return
			}
			if pid, ok := strings.CutPrefix(strings.TrimSpace(string(chunk)), "pid "); ok {
				if n, err := strconv.Atoi(pid); err == nil {
					select {
					case pids <- n:
					default:
					}
				}
			}
		},
	}, pids
}

// processGone reports whether pid is no process any more: the one a failed start killed was waited for, so it is not even a
// zombie.
func processGone(pid int) bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }

// A start that is aborted while its process runs kills the process and returns the cause, before Load returns.
func TestNodeAbortedStartWhileTheProcessRunsKillsItAndReturnsTheCause(t *testing.T) {
	options, pids := blockedStart(t)
	ctx, cancel := chordctx.WithCancel(background)
	loaded := make(chan error, 1)
	go func() {
		_, _, err := NewPluginRuntime(options).Load(RuntimeLoad{Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal}, ctx)
		loaded <- err
	}()
	pid := <-pids
	cancel(errors.New("stop it"))
	err := <-loaded
	if err == nil || !strings.Contains(err.Error(), "stop it") {
		t.Fatalf("error = %v, want the cause", err)
	}
	if !processGone(pid) {
		t.Fatalf("process %d is running after the aborted start returned", pid)
	}
}

// A start whose process does not connect within ReadyTimeout kills it and says what it was waiting for.
func TestNodeAStartThatDoesNotConnectInTimeKillsTheProcess(t *testing.T) {
	options, pids := blockedStart(t)
	timeout := 200 * time.Millisecond
	options.ReadyTimeout = &timeout
	_, _, err := NewPluginRuntime(options).Load(RuntimeLoad{Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal}, background)
	if err == nil || !strings.Contains(err.Error(), "did not connect within 200ms") {
		t.Fatalf("error = %v", err)
	}
	if pid := <-pids; !processGone(pid) {
		t.Fatalf("process %d is running after the failed start returned", pid)
	}
}

// Every failed start leaves no socket directory behind (the one the process was to connect to).
func TestNodeAFailedStartRemovesItsSocketDirectory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	options, pids := blockedStart(t)
	timeout := 100 * time.Millisecond
	options.ReadyTimeout = &timeout
	if _, _, err := NewPluginRuntime(options).Load(RuntimeLoad{Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal}, background); err == nil {
		t.Fatal("the start succeeded")
	}
	<-pids
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("left in TMPDIR: %v", entries)
	}
}

// fakeNode puts a `node` on PATH (alone) that runs script.
func fakeNode(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "node"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestNodeOlderThanTheMinimumNamesTheRequirementAndTheVersionFound(t *testing.T) {
	fakeNode(t, "echo v22.18.0")
	_, _, err := NewPluginRuntime(NodeOptions{CacheDir: t.TempDir()}).Load(RuntimeLoad{Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal}, background)
	want := "evaluating plugins needs Node.js 22.19.0 or newer; found v22.18.0"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestNodeWhoseVersionIsNoVersionIsRefused(t *testing.T) {
	fakeNode(t, "echo not a version")
	_, _, err := NewPluginRuntime(NodeOptions{CacheDir: t.TempDir()}).Load(RuntimeLoad{Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal}, background)
	want := `evaluating plugins needs Node.js 22.19.0 or newer; node --version returned "not a version"`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestNodeThatFailsItsVersionNamesWhatItSaid(t *testing.T) {
	fakeNode(t, "echo broken install >&2; exit 4")
	_, _, err := NewPluginRuntime(NodeOptions{CacheDir: t.TempDir()}).Load(RuntimeLoad{Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal}, background)
	if err == nil || !strings.Contains(err.Error(), "node --version failed: broken install") {
		t.Fatalf("error = %v", err)
	}
}

// A file the plugin imports and the build cannot read (here: forbidden) fails the build with Node's text, as Pi's build does
// (its `readFile`): `EACCES: permission denied, open '<path>'`, after the position of the import. (A user that can read every file reads this one too.)
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
