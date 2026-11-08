//go:build !windows

package mcp

import (
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
)

// alive is whether process pid exists.
func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// exited waits for process pid to exit.
func exited(t testing.TB, pid int) {
	t.Helper()
	if err := p2test.Poll(func() (bool, error) { return !alive(pid), nil }, 10*time.Second, "process to exit"); err != nil {
		t.Fatalf("process %d: %v", pid, err)
	}
}
