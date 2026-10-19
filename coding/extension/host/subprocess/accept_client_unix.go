//go:build !windows

package subprocess

import (
	"net"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// interruptAccept ends the Accept blocked on ln, or the next one, without
// disturbing a connection that is already queued. A listener that cannot be
// interrupted is closed, which ends Accept too.
func interruptAccept(ln net.Listener) {
	if l, ok := ln.(deadlineListener); ok && l.SetDeadline(expiredDeadline) == nil {
		return
	}
	_ = ln.Close()
}

// acceptQueued takes the connection a client queued on ln before interruptAccept
// ended Accept, or returns nil when none is queued. A deadline cut Accept short
// whether or not Go's poller had yet noticed a client, so the kernel's accept
// queue decides: after the process has exited it can no longer change.
func acceptQueued(ln net.Listener) (net.Conn, error) {
	l, ok := ln.(interface {
		deadlineListener
		syscall.Conn
	})
	if !ok {
		return nil, nil
	}
	if err := l.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	raw, err := l.SyscallConn()
	if err != nil {
		return nil, err
	}
	var queued bool
	var pollErr error
	if err := raw.Control(func(fd uintptr) {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		for {
			n, err := unix.Poll(fds, 0)
			if err == unix.EINTR {
				continue
			}
			queued, pollErr = n > 0 && fds[0].Revents&unix.POLLIN != 0, err
			return
		}
	}); err != nil {
		return nil, err
	}
	if pollErr != nil || !queued {
		return nil, pollErr
	}
	// The client is already queued, so this Accept returns at once.
	return ln.Accept()
}
