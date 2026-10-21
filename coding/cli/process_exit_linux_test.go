package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// waitProcessExit returns once the process pid has terminated, whether or not its parent has reaped it, or fails when ctx ends first.
// A pidfd becomes readable when its process terminates, a zombie included, so the wait ends on the exit itself.
func waitProcessExit(ctx context.Context, pid int) error {
	fd, err := unix.PidfdOpen(pid, 0)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("pidfd_open %d: %w", pid, err)
	}
	defer func() { _ = unix.Close(fd) }()
	for ctx.Err() == nil {
		timeout := -1
		if deadline, ok := ctx.Deadline(); ok {
			// A deadline already past polls once without blocking; a partial millisecond rounds up.
			timeout = max(int((time.Until(deadline)+time.Millisecond-1)/time.Millisecond), 0)
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, timeout)
		if err != nil && !errors.Is(err, unix.EINTR) {
			return fmt.Errorf("poll pidfd of %d: %w", pid, err)
		}
		if n > 0 {
			return nil
		}
	}
	return fmt.Errorf("process %d still running: %w", pid, ctx.Err())
}
