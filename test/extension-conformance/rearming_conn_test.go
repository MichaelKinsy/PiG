package extensionconformance

import (
	"errors"
	"net"
	"os"
	"sync"
	"time"
)

// peerReadSlice is how long a read of a script host's connection waits before it is posted again.
//
// A read on a Windows AF_UNIX socket that is already pending when the peer closes can stay pending: the close is not delivered to it, and the same read posted again returns end of file at once. Nothing is wrong with the extension: the Rust and the Go SDK rows both stalled one run in some thousands on the hosted Windows runner, with the extension process already gone and its stderr empty, and a CPU-loaded host did not stall. The production host does not wait on such a read alone either: its heartbeat writes to the connection (conn.go, defaultHeartbeatInterval) and sees the closed peer. A script host stands in for it here, so its reads re-post a stalled read the same way: each read waits at most one slice, and a slice that ends with no byte read posts the read again until the deadline the test set. A byte read is never lost, and a deadline the test set still ends the read as before.
const peerReadSlice = 250 * time.Millisecond

// rearmingConn is the connection of a script host: reads that stall are posted again, as the production host's heartbeat finds a closed peer.
type rearmingConn struct {
	net.Conn
	slice time.Duration

	mu       sync.Mutex
	deadline time.Time
}

func newRearmingConn(conn net.Conn) *rearmingConn {
	return &rearmingConn{Conn: conn, slice: peerReadSlice}
}

func (c *rearmingConn) SetDeadline(t time.Time) error {
	c.setReadDeadline(t)
	return c.SetWriteDeadline(t)
}

func (c *rearmingConn) SetReadDeadline(t time.Time) error {
	c.setReadDeadline(t)
	return nil
}

func (c *rearmingConn) setReadDeadline(t time.Time) {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
}

func (c *rearmingConn) readDeadline() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deadline
}

func (c *rearmingConn) Read(p []byte) (int, error) {
	for {
		deadline := c.readDeadline()
		slice := time.Now().Add(c.slice)
		if !deadline.IsZero() && deadline.Before(slice) {
			slice = deadline
		}
		if err := c.Conn.SetReadDeadline(slice); err != nil {
			return 0, err
		}
		n, err := c.Conn.Read(p)
		if n > 0 || err == nil || !errors.Is(err, os.ErrDeadlineExceeded) {
			return n, err
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return 0, err
		}
	}
}
