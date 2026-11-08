//go:build unix

package tui

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// signalInspector is Pi's `process.kill(launched.child.pid, "SIGUSR1")` and the 500 ms wait after it: the launcher runs in this
// process here, so the signal goes to it, and Go ends a process on SIGUSR1 unless it is ignored (Node would open its inspector).
func signalInspector(t *testing.T) {
	t.Helper()
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
}
