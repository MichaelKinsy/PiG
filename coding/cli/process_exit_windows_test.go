package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

// waitProcessExit returns once the process pid has terminated, or fails when ctx ends first. A process handle is signaled on the exit itself.
func waitProcessExit(ctx context.Context, pid int) error {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(handle)
	timeout := uint32(windows.INFINITE)
	if deadline, ok := ctx.Deadline(); ok {
		timeout = uint32(max(time.Until(deadline), 0).Milliseconds())
	}
	switch event, err := windows.WaitForSingleObject(handle, timeout); {
	case err != nil:
		return fmt.Errorf("wait for process %d: %w", pid, err)
	case event == windows.WAIT_OBJECT_0:
		return nil
	}
	return fmt.Errorf("process %d still running: %w", pid, context.DeadlineExceeded)
}
