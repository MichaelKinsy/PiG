package tools

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

const detachedChildSleepEnv = "PIG_TEST_DETACHED_CHILD_SLEEP"

// TestDetachedChildSleepHelper is the child process TestDetachedChildSetKillsAStartedShell starts.
func TestDetachedChildSleepHelper(t *testing.T) {
	if os.Getenv(detachedChildSleepEnv) == "" {
		t.Skip("helper process")
	}
	time.Sleep(time.Minute)
}

// The kill and a shell start share one lock: the start registers the shell before the kill can run, so the kill finds
// it. A shell started between its spawn and its registration would outlive pig.
func TestDetachedChildSetKillsAStartedShell(t *testing.T) {
	set := newDetachedChildSet()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDetachedChildSleepHelper$")
	cmd.Env = append(os.Environ(), detachedChildSleepEnv+"=1")
	setProcessGroup(cmd)
	if err := set.start(cmd); err != nil {
		t.Fatal(err)
	}
	defer releaseProcessGroup(cmd.Process)
	set.mu.Lock()
	_, tracked := set.processes[cmd.Process]
	set.mu.Unlock()
	if !tracked {
		_ = cmd.Process.Kill()
		t.Fatal("a started shell is not tracked")
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	set.killAll()
	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the kill left the started shell running")
	}
	if len(set.processes) != 0 {
		t.Fatalf("the kill kept %d tracked shells", len(set.processes))
	}
}
