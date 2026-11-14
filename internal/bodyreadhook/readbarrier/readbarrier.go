// Package readbarrier synchronizes a test fixture server with the owned provider transport's socket reads. Only tests import it.
package readbarrier

import (
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
	// The counts are per connection, not per client address: parallel fixture servers share a barrier, and the kernel gives one local port to live connections to different servers.
	written map[bodyreadhook.Connection]int
	read    map[bodyreadhook.Connection]int
	blocked map[bodyreadhook.Connection]int
}

// New installs read hooks until t ends. Install it once per test function; parallel subtests share it and are told apart by connection.
func New(t *testing.T) *Barrier {
	t.Helper()
	barrier := &Barrier{changed: make(chan struct{}), written: map[bodyreadhook.Connection]int{}, read: map[bodyreadhook.Connection]int{}, blocked: map[bodyreadhook.Connection]int{}}
	restore := bodyreadhook.Install(&bodyreadhook.Hooks{
		BeforeRead: func(connection bodyreadhook.Connection) {
			barrier.update(func() {
				if barrier.read[connection] == barrier.written[connection] {
					barrier.blocked[connection] = barrier.written[connection]
				}
			})
		},
		AfterRead: func(connection bodyreadhook.Connection, n int) {
			barrier.update(func() { barrier.read[connection] += n })
		},
	})
	t.Cleanup(restore)
	return barrier
}

// Server starts handler on a listener that counts bytes written per connection.
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

// WaitReading returns once the client of r has read everything written to it and has started another socket read. It reports false when the request ends first.
func (barrier *Barrier) WaitReading(r *http.Request) bool {
	connection := bodyreadhook.Connection{Client: r.RemoteAddr, Server: r.Context().Value(http.LocalAddrContextKey).(net.Addr).String()}
	for {
		barrier.mu.Lock()
		reached := barrier.blocked[connection] == barrier.written[connection] && barrier.read[connection] == barrier.written[connection]
		changed := barrier.changed
		barrier.mu.Unlock()
		if reached {
			return true
		}
		select {
		case <-changed:
		case <-r.Context().Done():
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
	connection := bodyreadhook.Connection{Client: conn.RemoteAddr().String(), Server: conn.LocalAddr().String()}
	// A client port can be reused for the same server once its earlier connection closed. That connection's counts are stale: the server writes a chunked terminator after an abort that the client never reads.
	listener.barrier.update(func() {
		delete(listener.barrier.written, connection)
		delete(listener.barrier.read, connection)
		delete(listener.barrier.blocked, connection)
	})
	return countingConn{Conn: conn, barrier: listener.barrier, connection: connection}, nil
}

type countingConn struct {
	net.Conn
	barrier    *Barrier
	connection bodyreadhook.Connection
}

// Write counts the bytes before sending them: a client can read them before Write returns, and a read must never appear to outrun the count.
func (conn countingConn) Write(p []byte) (int, error) {
	conn.barrier.update(func() { conn.barrier.written[conn.connection] += len(p) })
	n, err := conn.Conn.Write(p)
	if n < len(p) {
		conn.barrier.update(func() { conn.barrier.written[conn.connection] -= len(p) - n })
	}
	return n, err
}
