package subprocess

import (
	"errors"
	"io"
	"net"
	"syscall"
	"testing"
	"time"
)

// closeFailsConn is a transport whose close really fails: the socket is closed, then the error is reported.
type closeFailsConn struct {
	net.Conn
	err error
}

func (c closeFailsConn) Close() error {
	_ = c.Conn.Close()
	return c.err
}

// transportPairs makes the host's end of a transport and the peer's end of it, over a loopback socket and over an in-process pipe.
func transportPairs(t *testing.T) map[string]func() (host, peer net.Conn) {
	t.Helper()
	return map[string]func() (net.Conn, net.Conn){
		"tcp": func() (net.Conn, net.Conn) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			accepted := make(chan net.Conn, 1)
			go func() {
				c, err := listener.Accept()
				if err != nil {
					accepted <- nil
					return
				}
				accepted <- c
			}()
			host, err := net.Dial("tcp", listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			peer := <-accepted
			if peer == nil {
				t.Fatal("the peer was not accepted")
			}
			return host, peer
		},
		"pipe": net.Pipe,
	}
}

// Closing a connection whose peer already closed its end succeeds, as stopping an RPC client whose process already exited does in Pi (rpc-client.ts:145-168: `stop` returns for a process that exited on its own; the exit only rejects the pending requests, :107-112, and kill of an exited process does not throw). The peer's close ends the read loop, which closes the socket itself, so Close finds the transport closed. Here the peer's close has fully run (Done) before Close, the interleaving that otherwise depends on scheduling.
func TestCloseAfterThePeerClosedSucceeds(t *testing.T) {
	t.Parallel()
	for name, pair := range transportPairs(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			hostEnd, peerEnd := pair()
			conn := NewConn("peer-closes-first", hostEnd)
			conn.Start(t.Context())
			if err := peerEnd.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-conn.Done():
			case <-time.After(10 * time.Second):
				t.Fatal("the connection did not end when the peer closed")
			}
			if conn.failureError() == nil {
				t.Fatal("the peer's close left no failure on the connection")
			}
			if err := conn.Close("test closes the connection"); err != nil {
				t.Fatalf("Close after the peer closed = %v, want nil", err)
			}
			if err := conn.Close("second"); err != nil {
				t.Fatalf("second Close = %v, want nil", err)
			}
		})
	}
}

// A close that really fails still surfaces, and only the first closer sees it.
func TestCloseReturnsARealTransportCloseFailure(t *testing.T) {
	t.Parallel()
	for name, pair := range transportPairs(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			hostEnd, peerEnd := pair()
			defer func() { _ = peerEnd.Close() }()
			boom := errors.New("transport close failed")
			conn := NewConn("close-fails", closeFailsConn{Conn: hostEnd, err: boom})
			conn.Start(t.Context())
			// The peer drains the shutdown frame so only the transport's close can fail.
			go func() {
				buf := make([]byte, 4096)
				for {
					if _, err := peerEnd.Read(buf); err != nil {
						return
					}
				}
			}()
			if err := conn.Close("test closes the connection"); !errors.Is(err, boom) {
				t.Fatalf("Close = %v, want %v", err, boom)
			}
			if err := conn.Close("second"); err != nil {
				t.Fatalf("second Close = %v, want nil", err)
			}
		})
	}
}

// The closed-transport classes are the ones a peer's own close produces.
func TestTransportAlreadyClosedClasses(t *testing.T) {
	t.Parallel()
	for name, err := range map[string]error{
		"net closed":  &net.OpError{Op: "close", Net: "unix", Err: net.ErrClosed},
		"transport":   &TransportError{Extension: "x", Operation: "read", Err: io.EOF},
		"closed pipe": io.ErrClosedPipe,
		"short frame": &TransportError{Extension: "x", Operation: "read", Err: io.ErrUnexpectedEOF},
		"broken pipe": &TransportError{Extension: "x", Operation: "write", Err: syscall.EPIPE},
		"conn reset":  &TransportError{Extension: "x", Operation: "read", Err: syscall.ECONNRESET},
	} {
		if !transportAlreadyClosed(err) {
			t.Errorf("%s: %v not classed as an already closed transport", name, err)
		}
	}
	for name, err := range map[string]error{
		"nil":          nil,
		"other":        errors.New("disk on fire"),
		"unresponsive": &ExtensionUnresponsiveError{},
	} {
		if transportAlreadyClosed(err) {
			t.Errorf("%s: %v classed as an already closed transport", name, err)
		}
	}
}
