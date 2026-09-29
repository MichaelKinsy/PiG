//go:build !windows

package experimental

import (
	"errors"
	"syscall"
	"testing"
)

// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:830-837.
// The workers are children of this test process, so an exited worker stays a zombie until os/exec's reaper goroutine collects it; kill(pid, 0) still succeeds for that zombie. A zombie has exited, so it does not exist for this check.
func processExists(t *testing.T, pid int) bool {
	t.Helper()
	err := syscall.Kill(pid, 0)
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	return !processExited(t, pid)
}
