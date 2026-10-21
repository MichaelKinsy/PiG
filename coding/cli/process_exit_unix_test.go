//go:build unix

package cli

import (
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A SIGKILLed extension process is a zombie until its parent reaps it, and a new parent reaps it only once it runs, so a kill(pid, 0) probe cannot tell it from a live process. waitProcessExit must end on the exit, not on the reaping.
func TestWaitProcessExitDoesNotWaitForReaping(t *testing.T) {
	cmd, _ := startProcessExitHelper(t, "exit")
	if err := waitProcessExit(testbudget.Context(t), cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	// The test process is the parent and has not called Wait: the child is a zombie, which kill(pid, 0) still reports as present.
	if err := syscall.Kill(cmd.Process.Pid, 0); err != nil {
		t.Fatalf("the exited child was reaped before the check (%v), so the test does not show the zombie", err)
	}
}
