// Package bodyreadhook lets a test fixture synchronize with a provider's HTTP connection reads. Production code never installs hooks; with none installed a socket read costs one atomic load.
package bodyreadhook

import (
	"net"
	"slices"
	"sync"
	"sync/atomic"
)

// Connection identifies one client connection by both of its addresses. The client's address alone does not: the kernel gives one local port to live connections to different servers.
type Connection struct {
	Client, Server string
}

// Hooks observe socket reads on the owned provider transport, keyed by the client connection.
type Hooks struct {
	// BeforeRead runs before the transport reads the socket.
	BeforeRead func(connection Connection)
	// AfterRead runs after the socket read returns n bytes.
	AfterRead func(connection Connection, n int)
}

var (
	mu        sync.Mutex
	installed atomic.Pointer[[]*Hooks]
)

// Install adds hooks until the returned restore function runs. Parallel tests may install their own hooks; each sees every read and filters by address.
func Install(hooks *Hooks) (restore func()) {
	mu.Lock()
	defer mu.Unlock()
	next := append(slices.Clone(current()), hooks)
	installed.Store(&next)
	return func() {
		mu.Lock()
		defer mu.Unlock()
		next := slices.DeleteFunc(slices.Clone(current()), func(h *Hooks) bool { return h == hooks })
		installed.Store(&next)
	}
}

func current() []*Hooks {
	if list := installed.Load(); list != nil {
		return *list
	}
	return nil
}

// BeforeRead notifies installed hooks. It returns the connection for AfterRead and reports whether any hook is installed, so the caller can skip AfterRead and the address lookups otherwise.
func BeforeRead(conn net.Conn) (connection Connection, observed bool) {
	list := current()
	if len(list) == 0 {
		return Connection{}, false
	}
	connection = Connection{Client: conn.LocalAddr().String(), Server: conn.RemoteAddr().String()}
	for _, hooks := range list {
		if hooks.BeforeRead != nil {
			hooks.BeforeRead(connection)
		}
	}
	return connection, true
}

// AfterRead notifies installed hooks of a completed read.
func AfterRead(connection Connection, n int) {
	for _, hooks := range current() {
		if hooks.AfterRead != nil {
			hooks.AfterRead(connection, n)
		}
	}
}
