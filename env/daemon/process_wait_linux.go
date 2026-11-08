//go:build linux

package daemon

// Ports packages/env/daemon/src/sys/unix.rs (Group::wait_for_exit)

import "golang.org/x/sys/unix"

// waitForExit waits until the program has exited, leaving it unreaped: its pid, which is the group's id, names no other
// process until it is reaped.
func (g *programGroup) waitForExit() error {
	var info unix.Siginfo
	for {
		if err := unix.Waitid(unix.P_PID, g.pid, &info, unix.WEXITED|unix.WNOWAIT, nil); err != unix.EINTR {
			return err
		}
	}
}
