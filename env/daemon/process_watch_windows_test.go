//go:build windows

package daemon

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// watchedProcess is a process the test holds a handle to, so its pid names no other process while the test runs.
type watchedProcess struct{ handle windows.Handle }

// watchProcess opens the process pid and kills it when the test ends.
func watchProcess(t *testing.T, pid int) watchedProcess {
	t.Helper()
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		t.Fatalf("open process %d: %v", pid, err)
	}
	t.Cleanup(func() {
		_ = windows.TerminateProcess(handle, 1)
		_ = windows.CloseHandle(handle)
	})
	return watchedProcess{handle}
}

// exited reports whether the process ended within timeout.
func (p watchedProcess) exited(timeout time.Duration) bool {
	event, err := windows.WaitForSingleObject(p.handle, uint32(timeout.Milliseconds()))
	return err == nil && event == windows.WAIT_OBJECT_0
}
