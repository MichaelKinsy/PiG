//go:build linux || darwin || dragonfly || freebsd || netbsd || openbsd

package workers

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// streamHolds is how many bytes the pipe or Unix socket behind raw holds unread (what a read could take now), or -1 when it
// cannot say. The kernel counts a stream's bytes the same for a process's own and for what any holder of its write end wrote.
func streamHolds(raw syscall.RawConn) int {
	held := -1
	if err := raw.Control(func(descriptor uintptr) {
		if count, err := unix.IoctlGetInt(int(descriptor), fionread); err == nil {
			held = count
		}
	}); err != nil {
		return -1
	}
	return held
}
