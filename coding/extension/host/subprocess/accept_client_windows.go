//go:build windows

package subprocess

import "net"

// interruptAccept ends the Accept blocked on ln, or the next one, without
// disturbing a connection that is already queued. A listener that cannot be
// interrupted is closed, which ends Accept too.
func interruptAccept(ln net.Listener) {
	switch l := ln.(type) {
	case *pipeListener:
		l.interrupt()
	case deadlineListener:
		if l.SetDeadline(expiredDeadline) == nil {
			return
		}
		_ = ln.Close()
	default:
		_ = ln.Close()
	}
}

// acceptQueued takes the connection a client queued on ln before interruptAccept
// ended Accept, or returns nil when none is queued. A named pipe's Accept
// returns a connected client even after an interrupt, so none is left. A Go,
// Rust or Python runner's AF_UNIX listener has no readiness query on Windows,
// so an Accept that the expired deadline ended before it started can leave a
// client unread. Those runners report no load error over the socket, so the
// cost is that a runner which connected and then exited is process_exited,
// not register_failed.
func acceptQueued(net.Listener) (net.Conn, error) { return nil, nil }
