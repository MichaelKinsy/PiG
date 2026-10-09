package tools

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

const detachedChildSleepEnv = "PIG_TEST_DETACHED_CHILD_SLEEP"

// TestDetachedChildSleepHelper is the child process TestTrackedShellKillsAStartedShell starts.
func TestDetachedChildSleepHelper(t *testing.T) {
	if os.Getenv(detachedChildSleepEnv) == "" {
		t.Skip("helper process")
	}
	time.Sleep(time.Minute)
}

// The kill and a shell start share one lock: the start registers the shell before the kill can run, so the kill finds
// it. A shell started between its spawn and its registration would outlive pig.
func TestTrackedShellKillsAStartedShell(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestDetachedChildSleepHelper$")
	cmd.Env = append(os.Environ(), detachedChildSleepEnv+"=1")
	setProcessGroup(cmd)
	if err := startTrackedShell(cmd); err != nil {
		t.Fatal(err)
	}
	defer releaseProcessGroup(cmd.Process)
	trackedDetachedChildren.mu.Lock()
	_, tracked := trackedDetachedChildren.processes[cmd.Process]
	trackedDetachedChildren.mu.Unlock()
	if !tracked {
		_ = cmd.Process.Kill()
		t.Fatal("a started shell is not tracked")
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	KillTrackedDetachedChildren()
	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the kill left the started shell running")
	}
	trackedDetachedChildren.mu.Lock()
	left := len(trackedDetachedChildren.processes)
	trackedDetachedChildren.mu.Unlock()
	if left != 0 {
		t.Fatalf("the kill kept %d tracked shells", left)
	}
}
