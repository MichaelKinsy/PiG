//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package cli

import (
	"context"
	"errors"
	"fmt"
	"testing"
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
	defer func() { _ = unix.Close(kq) }()
	return waitProcessExitKevent(ctx, pid, func(changes, events []unix.Kevent_t, timeout *unix.Timespec) (int, error) {
		return unix.Kevent(kq, changes, events, timeout)
	})
}

func waitProcessExitKevent(ctx context.Context, pid int, kevent func(changes, events []unix.Kevent_t, timeout *unix.Timespec) (int, error)) error {
	change := []unix.Kevent_t{{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}}
	// Register before waiting so EINTR during the wait cannot leave the registration state ambiguous,
	// and a process that is already a zombie or gone reports ESRCH immediately.
	for {
		_, err := kevent(change, nil, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if errors.Is(err, unix.ESRCH) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("register exit filter on %d: %w", pid, err)
		}
		break
	}
	var event [1]unix.Kevent_t
	for ctx.Err() == nil {
		var timeout *unix.Timespec
		if deadline, ok := ctx.Deadline(); ok {
			ts := unix.NsecToTimespec(max(time.Until(deadline), 0).Nanoseconds())
			timeout = &ts
		}
		n, err := kevent(nil, event[:], timeout)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return fmt.Errorf("kevent on %d: %w", pid, err)
		}
		if n <= 0 {
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

// On Darwin and BSDs, kevent(2) returns n == -1 with errno EINTR when a signal (such as Go's SIGURG async preemption) interrupts the wait.
// waitProcessExit must retry until ctx ends or NOTE_EXIT actually arrives rather than treating n == -1 as a process exit.
func TestWaitProcessExitKeventRetriesAfterEINTR(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	err := waitProcessExitKevent(ctx, 42, func(changes, events []unix.Kevent_t, _ *unix.Timespec) (int, error) {
		calls++
		switch calls {
		case 1:
			if len(changes) != 1 || len(events) != 0 {
				t.Fatalf("call 1: changes=%d events=%d, want 1 and 0", len(changes), len(events))
			}
			return 0, nil
		case 2:
			if len(changes) != 0 || len(events) != 1 {
				t.Fatalf("call 2: changes=%d events=%d, want 0 and 1", len(changes), len(events))
			}
			return -1, unix.EINTR
		case 3:
			cancel()
			return 0, nil
		default:
			t.Fatalf("unexpected kevent call %d", calls)
			return 0, nil
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitProcessExitKevent after EINTR = %v, want context.Canceled", err)
	}
	if calls != 3 {
		t.Fatalf("kevent calls = %d, want 3", calls)
	}
}
