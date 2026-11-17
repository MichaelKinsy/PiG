package cli

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// pig additive (D232): listenMaxConnections bounds the connections a --listen server holds at once: each one builds a full session, and with the isolated default its own extension processes, so an unbounded count is bounded only by memory and descriptors. A connection still waiting for the build lock counts.
const listenMaxConnections = 64

// pig additive (D232): listenRefusalWriteTimeout bounds the one line a refused connection is sent, written inline in the accept loop, so a peer that never reads cannot hold the loop.
var listenRefusalWriteTimeout = time.Second

// listenAdmission counts the live connections of a --listen server and refuses the one past max. pig additive (D232).
type listenAdmission struct {
	mu     sync.Mutex
	max    int
	live   int
	atCap  bool
	notify func(max int) // called once on entering the at-cap state
}

func newListenAdmission(max int, notify func(max int)) *listenAdmission {
	return &listenAdmission{max: max, notify: notify}
}

// serve admits conn and runs handle on its own goroutine in wg, closing conn and freeing the slot when handle returns on any path; past the cap it writes the refusal line and closes conn without starting a goroutine.
func (a *listenAdmission) serve(wg *sync.WaitGroup, conn net.Conn, handle func()) {
	a.mu.Lock()
	if a.live >= a.max {
		first := !a.atCap
		a.atCap = true
		a.mu.Unlock()
		if first {
			a.notify(a.max)
		}
		refuseConnection(conn, a.max)
		return
	}
	a.live++
	a.mu.Unlock()
	wg.Go(func() {
		defer a.release()
		defer func() { _ = conn.Close() }()
		handle()
	})
}

func (a *listenAdmission) release() {
	a.mu.Lock()
	a.live--
	a.atCap = false
	a.mu.Unlock()
}

func (a *listenAdmission) liveCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.live
}

// refuseConnection sends the response rpc mode gives an error that belongs to no command (Pi's rpc-mode.ts error(undefined, "parse", ...): no id, command "parse") and closes conn.
func refuseConnection(conn net.Conn, max int) {
	_ = conn.SetWriteDeadline(time.Now().Add(listenRefusalWriteTimeout)) //portlint:allow clock a socket write deadline is a wall-clock bound on the peer, not session time
	writeJSONLine(conn, rpcError(nil, "parse", fmt.Sprintf("too many connections (max %d)", max)))
	_ = conn.Close()
}
