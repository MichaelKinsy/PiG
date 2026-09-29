// Package readbarrier synchronizes a test fixture server with the owned provider transport's socket reads. Only tests import it.
package readbarrier

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/bodyreadhook"
)

// Barrier lets a fixture server write its next record only after the client has consumed every byte already sent and is reading the socket again: the deterministic form of a Node fixture's timer between writes, which always outlasts the client's microtask drain.
type Barrier struct {
	mu      sync.Mutex
	changed chan struct{}
	written map[string]int
	read    map[string]int
	blocked map[string]int
}

// New installs read hooks until t ends. Install it once per test function; parallel subtests share it and are told apart by client address.
func New(t *testing.T) *Barrier {
	t.Helper()
	barrier := &Barrier{changed: make(chan struct{}), written: map[string]int{}, read: map[string]int{}, blocked: map[string]int{}}
	restore := bodyreadhook.Install(&bodyreadhook.Hooks{
		BeforeRead: func(local string) {
			barrier.update(func() {
				if barrier.read[local] == barrier.written[local] {
					barrier.blocked[local] = barrier.written[local]
				}
			})
		},
		AfterRead: func(local string, n int) { barrier.update(func() { barrier.read[local] += n }) },
	})
	t.Cleanup(restore)
	return barrier
}

// Server starts handler on a listener that counts bytes written per client.
func (barrier *Barrier) Server(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.Listener = countingListener{Listener: server.Listener, barrier: barrier}
	server.Start()
	t.Cleanup(server.Close)
	return server
}

func (barrier *Barrier) update(change func()) {
	barrier.mu.Lock()
	change()
	close(barrier.changed)
	barrier.changed = make(chan struct{})
	barrier.mu.Unlock()
}

// WaitReading returns once the client at remote has read everything written to it and has started another socket read.
func (barrier *Barrier) WaitReading(ctx context.Context, remote string) bool {
	for {
		barrier.mu.Lock()
		reached := barrier.blocked[remote] == barrier.written[remote] && barrier.read[remote] == barrier.written[remote]
		changed := barrier.changed
		barrier.mu.Unlock()
		if reached {
			return true
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return false
		}
	}
}

type countingListener struct {
	net.Listener
	barrier *Barrier
}

func (listener countingListener) Accept() (net.Conn, error) {
	conn, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	remote := conn.RemoteAddr().String()
	// A client port can be reused once its earlier connection closed. That connection's counts are stale: the server writes a chunked terminator after an abort that the client never reads.
	listener.barrier.update(func() {
		delete(listener.barrier.written, remote)
		delete(listener.barrier.read, remote)
		delete(listener.barrier.blocked, remote)
	})
	return countingConn{Conn: conn, barrier: listener.barrier, remote: remote}, nil
}

type countingConn struct {
	net.Conn
	barrier *Barrier
	remote  string
}

// Write counts the bytes before sending them: a client can read them before Write returns, and a read must never appear to outrun the count.
func (conn countingConn) Write(p []byte) (int, error) {
	conn.barrier.update(func() { conn.barrier.written[conn.remote] += len(p) })
	n, err := conn.Conn.Write(p)
	if n < len(p) {
		conn.barrier.update(func() { conn.barrier.written[conn.remote] -= len(p) - n })
	}
	return n, err
}
