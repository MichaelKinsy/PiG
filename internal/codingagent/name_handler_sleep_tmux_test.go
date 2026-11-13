package codingagent

import (
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The terminal stays live while a subprocess extension's session_info_changed handler sleeps. `/name` runs on the owner loop (handleSubmit ->
// dispatchSlash -> Session.SetSessionName), and a Python handler that makes no host call reports nothing to the host until it returns, so a loop
// that waited for the handler's first suspension would echo no key until the handler ended. Pi 1.5.2 echoes at once while a handler awaits
// (agent-session.ts:setSessionName, `void this._extensionRunner.emit`).
//
// The test runs the real binary in tmux with a Python extension whose handler sleeps until the test releases it, types during the sleep and
// reads the echo from the pane.
func TestTypingEchoesWhileASessionNameHandlerSleeps(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	pig := filepath.Join(work, "pig")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", pig, "./cmd/pig")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pig: %v\n%s", err, out)
	}
	// A directory extension is selected under its directory name, so the extension registers that name.
	extension := filepath.Join(work, "name-sleeper")
	started, release, ended := filepath.Join(work, "started"), filepath.Join(work, "release"), filepath.Join(work, "ended")
	source := fmt.Sprintf(`import os
import sys
import time

sys.path.insert(0, %q)

import pig_sdk


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("name-sleeper")

    def sleep_until_released(*args, **kwargs):
        open(%q, "w").close()
        deadline = time.monotonic() + 120
        while not os.path.exists(%q) and time.monotonic() < deadline:
            time.sleep(0.02)
        open(%q, "w").close()

    ext.on_event("session_info_changed", sleep_until_released)
    return ext
`, filepath.Join(root, "extensions", "sdk-py"), started, release, ended)
	for _, dir := range []string{extension, filepath.Join(work, "home"), filepath.Join(work, "agent"), filepath.Join(work, "cwd")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(extension, "name_sleeper.py"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	// A private server socket keeps the pane apart from the user's tmux and from other tests. The pane stays after pig exits, so a failed
	// start shows what pig printed.
	socket := fmt.Sprintf("pig-name-sleep-%d", rand.Uint32())
	config := filepath.Join(work, "tmux.conf")
	if err := os.WriteFile(config, []byte("set -g remain-on-exit on\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmux := func(args ...string) (string, error) {
		out, err := exec.CommandContext(t.Context(), "tmux", append([]string{"-u", "-f", config, "-L", socket}, args...)...).CombinedOutput()
		return string(out), err
	}
	home, agentDir := filepath.Join(work, "home"), filepath.Join(work, "agent")
	// tmux runs a command given as separate arguments itself, without a shell, so the pane's process is pig. new-session prints its process
	// ID, which is also the ID of the terminal session that pig leads.
	created, err := tmux("new-session", "-d", "-P", "-F", "#{pane_pid}", "-s", "pig", "-x", "120", "-y", "40", "-c", filepath.Join(work, "cwd"),
		"-e", "HOME="+home, "-e", "USERPROFILE="+home, "-e", "PIG_HOME="+home, "-e", "PI_HOME="+home,
		"-e", "PIG_CODING_AGENT_DIR="+agentDir, "-e", "PI_CODING_AGENT_DIR="+agentDir,
		"-e", "PI_OFFLINE=1", "-e", "TERM=xterm-256color", "-e", "ANTHROPIC_API_KEY=test-key",
		pig, "--offline", "-e", extension)
	if err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, created)
	}
	pigPID, pidErr := strconv.Atoi(strings.TrimSpace(created))
	// This cleanup runs before the removal of work, which t.TempDir registered first. kill-server hangs pig's terminal up and returns, and pig
	// then writes its exit record into the agent directory, so the cleanup returns only when pig's terminal session has no process left.
	t.Cleanup(func() {
		// The handler ends with the release file whether or not the test reached it.
		_ = os.WriteFile(release, nil, 0o600)
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
		if pidErr == nil {
			testenv.AwaitSessionExit(t, "pig", pigPID, testenv.LiveSessionProcesses)
		}
	})
	if pidErr != nil {
		t.Fatalf("tmux new-session printed %q, want the process ID of pig: %v", created, pidErr)
	}
	var pane string
	// shows polls the pane until it satisfies want, and reports whether it did before the deadline.
	shows := func(within time.Duration, want func(pane string) bool) bool {
		deadline := time.Now().Add(within)
		for {
			out, err := tmux("capture-pane", "-p", "-t", "pig")
			if err != nil {
				t.Fatalf("pig left the pane: %v: %s\nlast pane:\n%s", err, out, pane)
			}
			if pane = out; want(pane) {
				return true
			}
			if dead, _ := tmux("display-message", "-p", "-t", "pig", "#{pane_dead}"); strings.TrimSpace(dead) == "1" {
				t.Fatalf("pig exited:\n%s", pane)
			}
			if time.Now().After(deadline) {
				return false
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	keys := func(args ...string) {
		t.Helper()
		if out, err := tmux(append([]string{"send-keys", "-t", "pig"}, args...)...); err != nil {
			t.Fatalf("tmux send-keys %v: %v: %s", args, err, out)
		}
	}
	exists := func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	}

	// The editor is live once it echoes a typed token and removes it again.
	if !shows(time.Minute, func(pane string) bool { return strings.Contains(pane, "───") }) {
		t.Fatalf("the interactive screen did not come up:\n%s", pane)
	}
	keys("-l", "zqzq")
	if !shows(30*time.Second, func(pane string) bool { return strings.Contains(pane, "zqzq") }) {
		t.Fatalf("the editor did not echo before the test:\n%s", pane)
	}
	keys("BSpace", "BSpace", "BSpace", "BSpace")
	if !shows(30*time.Second, func(pane string) bool { return !strings.Contains(pane, "zqzq") }) {
		t.Fatalf("the editor did not clear before the test:\n%s", pane)
	}

	keys("-l", "/name sleeper")
	if !shows(30*time.Second, func(pane string) bool { return strings.Contains(pane, "/name sleeper") }) {
		t.Fatalf("the editor did not show the command:\n%s", pane)
	}
	keys("Enter")
	if !shows(30*time.Second, func(string) bool { return exists(started) }) {
		t.Fatalf("the session_info_changed handler did not start:\n%s", pane)
	}
	keys("-l", "qzxjkvw")
	echoed := shows(5*time.Second, func(pane string) bool { return strings.Contains(pane, "qzxjkvw") })
	if exists(ended) {
		t.Fatalf("the handler ended before the echo was read: the test did not hold it:\n%s", pane)
	}
	if !echoed {
		t.Errorf("typed text did not echo within 5 s while the session_info_changed handler slept: the input loop waits for the handler\n%s", pane)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !shows(30*time.Second, func(string) bool { return exists(ended) }) {
		t.Errorf("the released handler did not end:\n%s", pane)
	}
}
