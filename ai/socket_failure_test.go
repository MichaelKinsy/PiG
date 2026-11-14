package ai

import (
	"errors"
	"io"
	"net"
	"os"
	"runtime"
	"testing"
	"time"
)

// socketFailure's record, result by result, and what a failed request then reports: the read's own error; `read ECONNRESET` for a reset
// that a write after the connection's first met; `write ECONNRESET` for a reset the first write met (Node writes on connect before its
// loop has polled the socket: a peer that resets on accept); the end of the stream for a close, also when a later write fails.
func TestSocketFailureReportsTheConnectionsEndInNodesOrder(t *testing.T) {
	reset := func(op string) error { return socketOpError(op, socketResetErrno) }
	pipe := func(op string) error { return socketOpError(op, socketPipeErrno) }
	closedWrite := &net.OpError{Op: "write", Net: "tcp", Err: net.ErrClosed}
	closedRead := &net.OpError{Op: "read", Net: "tcp", Err: net.ErrClosed}
	deadline := &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}
	type step struct {
		read bool
		err  error
	}
	wrote := func(err error) step { return step{false, err} }
	read := func(err error) step { return step{true, err} }
	for _, test := range []struct {
		name   string
		steps  []step
		failed error
		want   string
	}{
		{"the read met the reset, then the write a shut down socket", []step{wrote(nil), read(reset("read")), wrote(pipe(vectoredWriteOp))}, pipe(vectoredWriteOp), "read ECONNRESET"},
		{"the read met the reset, then the write the closed connection", []step{wrote(nil), read(reset("read"))}, closedWrite, "read ECONNRESET"},
		{"the write met the reset: the request failed with the write", []step{wrote(nil), wrote(reset(vectoredWriteOp)), read(io.EOF)}, reset(vectoredWriteOp), "read ECONNRESET"},
		{"the write met the reset: the request failed with the read's end", []step{wrote(nil), wrote(reset(vectoredWriteOp)), read(io.EOF)}, io.EOF, "read ECONNRESET"},
		{"the write met the reset: the request failed with the read of the closed connection", []step{wrote(nil), wrote(reset("write")), read(closedRead)}, closedRead, "read ECONNRESET"},
		{"the connection's first write met the reset", []step{wrote(reset("write")), read(io.EOF)}, io.EOF, "write ECONNRESET"},
		{"the peer closed, then a write met the shut down socket", []step{wrote(nil), read(io.EOF), wrote(pipe("write"))}, pipe("write"), "other side closed"},
		{"the peer closed", []step{wrote(nil), read(io.EOF)}, io.EOF, "other side closed"},
		{"a passed read deadline is not the connection's end", []step{wrote(nil), read(deadline), wrote(pipe("write"))}, pipe("write"), "write EPIPE"},
		{"this side's close is not the connection's end", []step{wrote(nil), read(closedRead)}, closedWrite, "other side closed"},
		{"nothing recorded", nil, reset("read"), "read ECONNRESET"},
	} {
		conn := &idleTimeoutConn{}
		for _, s := range test.steps {
			if s.read {
				conn.failure.reading.Add(1)
				conn.failure.readEnded(s.err)
			} else {
				conn.failure.writeEnded(s.err, false)
			}
		}
		if got, _ := nodeConnectionFailure(nodeSocketFailure(conn, test.failed)); got != test.want {
			t.Errorf("%s: reported %q, want %q", test.name, got, test.want)
		}
	}

	// A failure that is not the socket's keeps its own error, whatever the connection recorded: a request's own stream error, the headers
	// timeout, a refused port.
	conn := &idleTimeoutConn{}
	conn.failure.reading.Add(1)
	conn.failure.readEnded(reset("read"))
	for _, own := range []error{errors.New("stream error: stream ID 3; CANCEL"), deadline, errFetchBadPort} {
		if got := nodeSocketFailure(conn, own); !errors.Is(got, own) || errors.Is(got, socketResetErrno) {
			t.Errorf("%v on a reset connection: reported %v, want the request's own error", own, got)
		}
	}
	// A request that had no connection (a failed dial) reports its own error.
	if got := nodeSocketFailure(nil, io.EOF); !errors.Is(got, io.EOF) {
		t.Errorf("without a connection: reported %v, want the request's own error", got)
	}
}

// A write that fails with EPIPE on a kernel socket holds its error until the read in flight has ended: net/http cannot report the write
// before the connection has the read's result. The read ends here once the write is seen waiting.
func TestSocketFailureWriteWaitsForTheReadInFlight(t *testing.T) {
	conn := &idleTimeoutConn{}
	conn.failure.socket = true
	conn.failure.reading.Add(1)
	failed := socketOpError("write", socketPipeErrno)
	done := make(chan string, 1)
	go func() {
		conn.failure.writeEnded(failed, false)
		message, _ := nodeConnectionFailure(nodeSocketFailure(conn, failed))
		done <- message
	}()
	var got string
	for ended := false; got == ""; {
		select {
		case got = <-done:
		default:
			if !ended && conn.failure.waiting.Load() != 0 {
				conn.failure.readEnded(socketOpError("read", socketResetErrno))
				ended = true
			}
			runtime.Gosched()
		}
	}
	if got != "read ECONNRESET" {
		t.Fatalf("the write reported %q, want the read's result, read ECONNRESET", got)
	}
}

// Under load the request's write can fail before net/http's read loop has begun its first read. On an established connection the write
// then waits for that read: a peer that closed and then reset (the write's EPIPE) is the end of the stream the read reports.
func TestSocketFailureWriteWaitsForAReadThatHasNotBegun(t *testing.T) {
	conn := &idleTimeoutConn{}
	conn.failure.socket = true
	failed := socketOpError("write", socketPipeErrno)
	done := make(chan string, 1)
	go func() {
		conn.failure.writeEnded(failed, true)
		message, _ := nodeConnectionFailure(nodeSocketFailure(conn, failed))
		done <- message
	}()
	var got string
	for ended := false; got == ""; {
		select {
		case got = <-done:
		default:
			if !ended && conn.failure.waiting.Load() != 0 {
				conn.failure.reading.Add(1)
				conn.failure.readEnded(io.EOF)
				ended = true
			}
			runtime.Gosched()
		}
	}
	if got != "other side closed" {
		t.Fatalf("the write reported %q, want the read's result, other side closed", got)
	}
}

// The connection's close ends the wait of a write whose read never comes; the request then reports the write's own error.
func TestSocketFailureWaitEndsWhenTheConnectionIsClosed(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = server.Close() }()
	conn := &idleTimeoutConn{Conn: client, timeout: func() time.Duration { return 0 }}
	conn.failure.socket = true
	failed := socketOpError("write", socketPipeErrno)
	done := make(chan string, 1)
	go func() {
		conn.failure.writeEnded(failed, true)
		message, _ := nodeConnectionFailure(nodeSocketFailure(conn, failed))
		done <- message
	}()
	var got string
	for closed := false; got == ""; {
		select {
		case got = <-done:
		default:
			if !closed && conn.failure.waiting.Load() != 0 {
				_ = conn.Close()
				closed = true
			}
			runtime.Gosched()
		}
	}
	if got != "write EPIPE" {
		t.Fatalf("the write reported %q, want its own error, write EPIPE", got)
	}
}
