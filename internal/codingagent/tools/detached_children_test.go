package tools

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

// The exit cleanup and a shell start share one lock: a start either lands in the set the cleanup kills, or comes after
// the cleanup and runs nothing. A shell started between its spawn and its registration would outlive pig.
func TestDetachedChildSetStartsNothingAfterTheExitCleanup(t *testing.T) {
	set := newDetachedChildSet()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	setProcessGroup(cmd)
	if err := set.start(cmd); err != nil {
		t.Fatal(err)
	}
	set.mu.Lock()
	_, tracked := set.processes[cmd.Process]
	set.mu.Unlock()
	if !tracked {
		t.Fatal("a started shell is not tracked")
	}
	set.untrack(cmd.Process)
	_ = cmd.Wait()
	releaseProcessGroup(cmd.Process)

	set.killAll()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pr.Close() }()
	late := exec.Command(os.Args[0], "-test.run=^$")
	late.Stdout = pw
	setProcessGroup(late)
	if err := set.start(late); !errors.Is(err, errPigExiting) {
		t.Fatalf("start after the exit cleanup = %v, want %v", err, errPigExiting)
	}
	if late.Process != nil {
		t.Fatal("a shell started after the exit cleanup")
	}
	if _, err := pw.Write([]byte("x")); err == nil {
		t.Fatal("the child's end of the output pipe is still open")
	}
}
