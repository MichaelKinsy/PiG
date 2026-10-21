//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// waitProcessExit returns once the process pid has terminated, whether or not its parent has reaped it, or fails when ctx ends first.
// A NOTE_EXIT filter fires on the exit itself; a process already gone cannot be registered.
func waitProcessExit(ctx context.Context, pid int) error {
	kq, err := unix.Kqueue()
	if err != nil {
		return fmt.Errorf("kqueue: %w", err)
	}
	defer unix.Close(kq)
	change := []unix.Kevent_t{{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}}
	var event [1]unix.Kevent_t
	for ctx.Err() == nil {
		var timeout *unix.Timespec
		if deadline, ok := ctx.Deadline(); ok {
			ts := unix.NsecToTimespec(max(time.Until(deadline), 0).Nanoseconds())
			timeout = &ts
		}
		n, err := unix.Kevent(kq, change, event[:], timeout)
		change = nil
		if errors.Is(err, unix.ESRCH) {
			return nil
		}
		if err != nil && !errors.Is(err, unix.EINTR) {
			return fmt.Errorf("kevent on %d: %w", pid, err)
		}
		if n == 0 {
			continue
		}
		if event[0].Flags&unix.EV_ERROR != 0 {
			if errors.Is(unix.Errno(event[0].Data), unix.ESRCH) {
				return nil
			}
			return fmt.Errorf("register exit filter on %d: %w", pid, unix.Errno(event[0].Data))
		}
		return nil
	}
	return fmt.Errorf("process %d still running: %w", pid, ctx.Err())
}
