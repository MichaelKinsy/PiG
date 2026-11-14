package ai

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// socketFailure is how a dialed connection ended, kept so that a failed request reports what Node's socket reports.
//
// Node serves a socket on one thread: libuv reads a ready socket before it writes to it (uv__stream_io), and undici keeps every socket
// reading. A peer's reset or close therefore reaches Node as the read's result, whatever the request is doing: `read ECONNRESET` for a
// reset and the end of the stream (SocketError `other side closed`) for a close, also while megabytes of the request body are still
// being written. Go reads and writes a connection on two goroutines, and net/http returns the result it receives first and drops the
// other (persistConn.roundTrip selects on the write's result, the read's and the connection's close). The write of the request body
// then reported the peer's reset as well: ECONNRESET when the write met the reset, after which the read sees the end of the stream, and
// EPIPE when the read met it first.
//
// The one write Node issues before its loop has polled the socket is the connection's first, on connect: there the reset is the write's
// in Node too (`write ECONNRESET` for a peer that resets on accept).
type socketFailure struct {
	// socket is whether the connection is a kernel socket, the only kind on which a pending read is known to return once a write has
	// failed with EPIPE. It is set when the connection is wrapped and not changed after.
	socket bool
	// reading counts the reads in flight, ended the reads that have ended and waiting the writes that wait for one to end; wrote is set
	// once a write succeeded.
	reading atomic.Int32
	ended   atomic.Uint32
	waiting atomic.Int32
	wrote   atomic.Bool

	mu sync.Mutex
	// read is the first result that ended the read side: the socket's error, or io.EOF for the peer's close.
	read error
	// reset is the first write error that carried the peer's reset (ECONNRESET), and firstWrite whether no write had succeeded before it.
	reset      error
	firstWrite bool
	// closed is set when this side closes the connection.
	closed bool
	// wake is closed when a read ends or the connection is closed while a write waits.
	wake chan struct{}
}

// readEnded records the result of a read that was counted in reading.
func (failure *socketFailure) readEnded(err error) {
	if err != nil {
		failure.readFailed(err)
	}
	failure.ended.Add(1)
	failure.reading.Add(-1)
	if failure.waiting.Load() != 0 {
		failure.wakeWaiting(false)
	}
}

// connClosed records that this side closed the connection, which ends every wait for the read side.
func (failure *socketFailure) connClosed() {
	failure.wakeWaiting(true)
}

func (failure *socketFailure) wakeWaiting(closed bool) {
	failure.mu.Lock()
	if closed {
		failure.closed = true
	}
	if failure.wake != nil {
		close(failure.wake)
		failure.wake = nil
	}
	failure.mu.Unlock()
}

func (failure *socketFailure) readFailed(err error) {
	// A passed deadline does not end the socket, and a closed connection is this side's doing.
	if errors.Is(err, net.ErrClosed) {
		return
	}
	if timeout, ok := errors.AsType[net.Error](err); ok && timeout.Timeout() {
		return
	}
	failure.mu.Lock()
	if failure.read == nil {
		failure.read = err
	}
	failure.mu.Unlock()
}

// writeEnded records the result of a write. established is whether connection establishment has completed, after which net/http's read
// loop reads the connection.
func (failure *socketFailure) writeEnded(err error, established bool) {
	if err == nil {
		if !failure.wrote.Load() {
			failure.wrote.Store(true)
		}
		return
	}
	switch nodeerrno.ErrorCode(err) {
	case "ECONNRESET":
		failure.mu.Lock()
		if failure.reset == nil {
			failure.reset, failure.firstWrite = err, !failure.wrote.Load()
		}
		failure.mu.Unlock()
	case "EPIPE":
		// The socket was shut down before this write, by a reset the read met first or by one that followed the peer's close. The read
		// says which of the two; its result is recorded before this write's error reaches net/http.
		if failure.socket {
			failure.awaitRead(established)
		}
	}
}

// awaitRead returns once the read side's result is recorded, a read has ended without one, or this side has closed the connection. With
// no read in flight it waits for the next one only on an established connection, where net/http's read loop is about to read (under
// load the request's write can fail before that loop's first read has begun); before that, in a TLS handshake or a proxy CONNECT, the
// writer itself reads next. It is called for a kernel socket that is shut down, on which a read returns at once. Every end of the wait is
// an event of the connection: a read's return or its close, which is also how net/http ends the connection of a cancelled request. No
// timer is involved.
func (failure *socketFailure) awaitRead(established bool) {
	failure.waiting.Add(1)
	defer failure.waiting.Add(-1)
	ended := failure.ended.Load()
	for {
		failure.mu.Lock()
		if failure.read != nil || failure.closed || failure.ended.Load() != ended || (failure.reading.Load() == 0 && !established) {
			failure.mu.Unlock()
			return
		}
		if failure.wake == nil {
			failure.wake = make(chan struct{})
		}
		wake := failure.wake
		failure.mu.Unlock()
		<-wake
	}
}

// reported is the error Node's socket reports for a connection whose request failed with err.
func (failure *socketFailure) reported(err error) error {
	failure.mu.Lock()
	defer failure.mu.Unlock()
	switch {
	case failure.read != nil && !errors.Is(failure.read, io.EOF):
		return failure.read
	case failure.reset != nil && failure.firstWrite:
		return failure.reset
	case failure.reset != nil:
		// The write took the reset that Node's read reports.
		read := &net.OpError{Op: "read", Err: failure.reset}
		if op, ok := errors.AsType[*net.OpError](failure.reset); ok {
			read.Net, read.Source, read.Addr, read.Err = op.Net, op.Source, op.Addr, op.Err
		}
		return read
	case failure.read != nil:
		return failure.read
	}
	return err
}

// nodeSocketFailure is the cause of "fetch failed" for a request that failed with err on conn, the provider connection it was sent on
// (nil when it had none): for a socket failure, the one Node's socket reports for the connection (socketFailure), otherwise err.
func nodeSocketFailure(conn *idleTimeoutConn, err error) error {
	if conn == nil {
		return err
	}
	switch _, code := nodeConnectionFailure(err); code {
	case "ECONNRESET", "EPIPE", "UND_ERR_SOCKET":
		return conn.failure.reported(err)
	}
	return err
}
