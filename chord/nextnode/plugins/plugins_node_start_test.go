package plugins

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
)

// Not upstream's: the start of the Node subprocess where the test needs a process that does not connect (a script `--require`d
// before the driver, which blocks it) or another `node` on PATH (fakeNode, a script of the platform's shell: plugins_node_unix_test.go,
// plugins_node_windows_test.go).

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

// A start whose process does not connect within ReadyTimeout kills it and says what it was waiting for. The timeout is long
// enough for the process to reach the script that prints its pid before it blocks (a process of a slow machine takes more than
// a few hundred milliseconds to start, and one killed before it printed has no pid to look for).
func TestNodeAStartThatDoesNotConnectInTimeKillsTheProcess(t *testing.T) {
	options, pids := blockedStart(t)
	timeout := 5 * time.Second
	options.ReadyTimeout = &timeout
	_, _, err := NewPluginRuntime(options).Load(RuntimeLoad{Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal}, background)
	if err == nil || !strings.Contains(err.Error(), "did not connect within 5s") {
		t.Fatalf("error = %v", err)
	}
	select {
	case pid := <-pids:
		if !processGone(pid) {
			t.Fatalf("process %d is running after the failed start returned", pid)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the process never printed its pid")
	}
}

func TestNodeOlderThanTheMinimumNamesTheRequirementAndTheVersionFound(t *testing.T) {
	fakeNode(t, "v22.18.0", "", 0)
	_, _, err := NewPluginRuntime(NodeOptions{CacheDir: t.TempDir()}).Load(RuntimeLoad{Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal}, background)
	want := "evaluating plugins needs Node.js 22.19.0 or newer; found v22.18.0 at "
	if path, lookErr := exec.LookPath("node"); lookErr != nil || err == nil || err.Error() != want+path {
		t.Fatalf("error = %v, want %q followed by the path of the node found", err, want)
	}
}

func TestNodeWhoseVersionIsNoVersionIsRefused(t *testing.T) {
	fakeNode(t, "not a version", "", 0)
	_, _, err := NewPluginRuntime(NodeOptions{CacheDir: t.TempDir()}).Load(RuntimeLoad{Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal}, background)
	want := `evaluating plugins needs Node.js 22.19.0 or newer; node --version returned "not a version"`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestNodeThatFailsItsVersionNamesWhatItSaid(t *testing.T) {
	fakeNode(t, "", "broken install", 4)
	_, _, err := NewPluginRuntime(NodeOptions{CacheDir: t.TempDir()}).Load(RuntimeLoad{Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal}, background)
	if err == nil || !strings.Contains(err.Error(), "node --version failed: broken install") {
		t.Fatalf("error = %v", err)
	}
}
