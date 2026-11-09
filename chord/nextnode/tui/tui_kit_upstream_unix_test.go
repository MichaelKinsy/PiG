//go:build unix

package tui

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// signalInspector is Pi's `process.kill(launched.child.pid, "SIGUSR1")` and the 500 ms wait after it: the launcher runs in this
// process here, so the signal goes to it. (Node would open its inspector; Go takes no action on SIGUSR1, so the test cannot tell an
// ignoring launcher from any other: it shows the client survives the signal.)
func signalInspector(t *testing.T) {
	t.Helper()
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
}
