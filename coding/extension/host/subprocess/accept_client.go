package subprocess

import (
	"context"
	"net"
	"time"
)

// acceptOutcome says how awaitConnection ended.
type acceptOutcome int

const (
	// acceptConnected means the extension process connected; the connection is returned.
	acceptConnected acceptOutcome = iota
	// acceptProcessExited means the process exited without ever connecting.
	acceptProcessExited
	// acceptCancelled means the caller's context ended first.
	acceptCancelled
	// acceptFailed means the listener failed; the error is returned.
	acceptFailed
)

// deadlineListener is a listener whose Accept ends with os.ErrDeadlineExceeded
// once its deadline has passed, whether that Accept is already blocked or not
// yet started.
type deadlineListener interface {
	net.Listener
	SetDeadline(time.Time) error
}

// acceptGateFunc lets a test run an extension's Accept in a chosen order
// against the process's exit, to force the order a loaded machine produces:
// accept is the listener's own Accept and exited closes when the process ends.
type acceptGateFunc func(accept func() (net.Conn, error), exited <-chan struct{}) (net.Conn, error)

// expiredDeadline is a deadline long past. Setting it ends Accept at once.
var expiredDeadline = time.Unix(1, 0)

// awaitConnection waits for the extension process behind ln to connect, to
// exit, or for ctx to end. A connection the process made before it exited
// always outranks the exit.
//
// Upstream awaits the factory with no deadline and reports the loader's own
// error as the load error: "Extension does not export a valid factory
// function: <path>" (packages/coding-agent/src/core/extensions/loader.ts:646-648)
// or "Failed to load extension: <message>" (loader.ts:654-655). A Node runner
// reports that error over the extension's socket: it connects, writes a
// load_failed frame, closes, and exits (runtime-node reportLoadFailure), so on a
// loaded machine the host looks only after the process has ended. The
// connection is then in the listener's accept queue and the exit is already
// observable. A host that chooses between them by which it notices first, as a
// select over an Accept goroutine and the exit channel does, loses the error
// and reports process_exited for an extension that exited only after it had
// said why.
//
// A process that exited can connect no more, so whatever it connected is
// already queued. awaitConnection therefore accepts on the calling goroutine
// and lets the exit or the cancellation only interrupt that Accept. An
// interrupted Accept can leave a queued connection unread, so the kernel's
// queue, which is authoritative, is asked before the process is called gone.
//
// ln serves one extension process. awaitConnection may leave it interrupted;
// the caller closes it.
func (h *Host) awaitConnection(ctx context.Context, ln net.Listener, exited <-chan struct{}) (net.Conn, acceptOutcome, error) {
	accept := ln.Accept
	if h.acceptGate != nil {
		accept = func() (net.Conn, error) { return h.acceptGate(ln.Accept, exited) }
	}
	stop, interrupted := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(interrupted)
		select {
		case <-exited:
		case <-ctx.Done():
		case <-stop:
			return
		}
		interruptAccept(ln)
	}()
	conn, err := accept()
	close(stop)
	<-interrupted
	if ctx.Err() != nil {
		if conn != nil {
			_ = conn.Close()
		}
		return nil, acceptCancelled, ctx.Err()
	}
	if err == nil {
		return conn, acceptConnected, nil
	}
	select {
	case <-exited:
	default:
		return nil, acceptFailed, err
	}
	// The exit ended Accept, or the process exited as it failed.
	conn, err = acceptQueued(ln)
	switch {
	case err != nil:
		return nil, acceptFailed, err
	case conn != nil:
		return conn, acceptConnected, nil
	}
	return nil, acceptProcessExited, nil
}
