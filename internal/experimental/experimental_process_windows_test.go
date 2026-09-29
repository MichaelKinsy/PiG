//go:build windows

package experimental

import (
	"errors"
	"math"
	"testing"

	"golang.org/x/sys/windows"
)

// The Windows counterpart of process.kill(pid, 0) observes a native process handle without waiting. Access denial still means the process exists.
func processExists(t *testing.T, pid int) bool {
	t.Helper()
	if pid <= 0 || uint64(pid) > math.MaxUint32 {
		return false
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() {
		if err := windows.CloseHandle(handle); err != nil {
			t.Errorf("close worker %d observation handle: %v", pid, err)
		}
	}()
	state, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && state == uint32(windows.WAIT_TIMEOUT)
}
