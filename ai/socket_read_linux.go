//go:build linux

package ai

import (
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
)

// socketReader reads a connected TCP or Unix socket with read(2) issued as a raw system call inside the runtime poller's RawConn.Read, so a read neither enters nor leaves a blocking system call. The socket is non-blocking, so read(2) never blocks; the poller still parks the goroutine until the socket is readable or its deadline passes. A blocking system call entry wakes the Go runtime's monitor thread whenever every P was idle, which then polls every 20 µs until the process is idle again; a streamed response read one small chunk at a time paid that wakeup per chunk. Every result equals net.Conn.Read's on the same socket: data, io.EOF at end of stream, and the same *net.OpError for an error, deadline or close.
type socketReader struct {
	conn    net.Conn
	raw     syscall.RawConn
	network string

	mu     sync.Mutex
	buffer []byte
	n      int
	errno  syscall.Errno
	readFn func(fd uintptr) bool
}

// newSocketReader returns a raw reader for a *net.TCPConn or *net.UnixConn dialed on network, and nil for any other connection.
func newSocketReader(conn net.Conn, network string) func([]byte) (int, error) {
	var socket syscall.Conn
	switch typed := conn.(type) {
	case *net.TCPConn:
		socket = typed
	case *net.UnixConn:
		socket = typed
	default:
		return nil
	}
	raw, err := socket.SyscallConn()
	if err != nil {
		return nil
	}
	reader := &socketReader{conn: conn, raw: raw, network: network}
	reader.readFn = reader.readSocket
	return reader.read
}

// readSocket is internal/poll FD.Read's loop body: EINTR retries, EAGAIN waits for readiness, and any other result ends the read.
func (reader *socketReader) readSocket(fd uintptr) bool {
	for {
		n, errno := readSocketOnce(fd, reader.buffer)
		switch errno {
		case syscall.EINTR:
			continue
		case syscall.EAGAIN:
			return false
		case 0:
			reader.n, reader.errno = int(n), 0
		default:
			reader.n, reader.errno = 0, errno
		}
		return true
	}
}

func (reader *socketReader) read(p []byte) (int, error) {
	if len(p) == 0 {
		// An empty read returns before any system call.
		return reader.conn.Read(p)
	}
	reader.mu.Lock()
	reader.buffer, reader.n, reader.errno = p, 0, 0
	err := reader.raw.Read(reader.readFn)
	n, errno := reader.n, reader.errno
	reader.buffer = nil
	reader.mu.Unlock()
	if err != nil {
		// RawConn.Read reports a deadline, close or poller failure as the OpError net.Conn.Read builds, under the operation name "raw-read".
		var opError *net.OpError
		if errors.As(err, &opError) && opError.Op == "raw-read" {
			readError := *opError
			readError.Op = "read"
			return 0, &readError
		}
		return 0, err
	}
	if errno != 0 {
		return 0, &net.OpError{Op: "read", Net: reader.network, Source: reader.conn.LocalAddr(), Addr: reader.conn.RemoteAddr(), Err: os.NewSyscallError("read", errno)}
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}
