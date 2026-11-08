//go:build darwin

package daemon

// Ports packages/env/daemon/src/sys/unix.rs (Group::wait_for_exit)

import (
	"golang.org/x/sys/unix"
)

// waitForExit waits until the program has exited, leaving it unreaped: its pid, which is the group's id, names no other
// process until it is reaped. macOS has no waitid in x/sys/unix; a kqueue reports the exit before the parent reaps it, and
// reports ESRCH for a program that already exited and is not reaped yet or never was.
func (g *programGroup) waitForExit() error {
	queue, err := unix.Kqueue()
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(queue) }()
	change := []unix.Kevent_t{{Ident: uint64(g.pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}}
	var events [1]unix.Kevent_t
	for {
		count, err := unix.Kevent(queue, change, events[:], nil)
		switch {
		case err == unix.EINTR:
			continue
		case err == unix.ESRCH:
			return nil
		case err != nil:
			return err
		case count == 0:
			continue
		case events[0].Flags&unix.EV_ERROR != 0 && events[0].Data != int64(unix.ESRCH):
			return unix.Errno(events[0].Data)
		}
		return nil
	}
}
