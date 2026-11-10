//go:build !windows

package daemon

import (
	"syscall"
	"testing"
	"time"
)

// watchedProcess is a process the test watches by pid.
type watchedProcess struct{ pid int }

// watchProcess watches the process pid and kills it when the test ends.
func watchProcess(t *testing.T, pid int) watchedProcess {
	t.Helper()
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return watchedProcess{pid}
}

// exited reports whether the process is gone within timeout. A process that exited and is not reaped yet is not gone; the sleepers of these tests are orphans, which init reaps.
func (p watchedProcess) exited(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if syscall.Kill(p.pid, 0) != nil {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}
