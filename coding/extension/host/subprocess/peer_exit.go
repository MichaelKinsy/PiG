package subprocess

import (
	"context"
	"io"
	"os"
	"time"
)

// peerExitGrace is how long a read waits for a byte once the peer's process has exited. A process that exited has written everything it will write, and the socket holds it, so a read that finds nothing for this long is at the end of the stream. A read that finds a byte starts the wait again.
// pig divergence (D56): the subprocess's exit bounds the host's read of its socket; upstream extensions run in process.
const peerExitGrace = time.Second

// watchPeerExit gives the connection the exit of the process behind it: once exited closes, the read loop ends with io.EOF, as it does when the socket reports the peer's close, and any data the process wrote before it exited is read first.
//
// The socket is what ends the read in the normal case, and a read already pending when the peer closes is not always woken: on a Windows AF_UNIX socket it can stay pending (the conformance host's reads see it once in some thousands of runs), and then an extension that exited while it loaded, sat idle or shut down was never seen to be gone: the heartbeat writes only while a request is in flight. The process is the host's own source of truth that nothing more will arrive. It is called before Start, with the channel the process reaper closes; a nil channel (an in-process extension) does nothing.
func (c *Conn) watchPeerExit(exited <-chan struct{}) {
	c.peerExit = exited
}

// endReadOnPeerExit is run by Start. It marks the peer gone when its process exits and wakes the pending read, which ends after peerExitGrace.
func (c *Conn) endReadOnPeerExit(ctx context.Context) {
	select {
	case <-c.peerExit:
	case <-ctx.Done():
		return
	}
	c.peerGone.Store(true)
	_ = c.conn.SetReadDeadline(time.Now().Add(peerExitGrace))
}

// peerReader is the read side of a connection whose peer's process exit is watched. Until the process has exited it is the socket; after, each read waits at most peerExitGrace and reports the end of the stream instead of a timeout, so the error the read loop reports is the one a closed socket gives.
type peerReader struct{ c *Conn }

func (r peerReader) Read(p []byte) (int, error) {
	if r.c.peerGone.Load() {
		_ = r.c.conn.SetReadDeadline(time.Now().Add(peerExitGrace))
	}
	n, err := r.c.conn.Read(p)
	if n == 0 && err != nil && r.c.peerGone.Load() && os.IsTimeout(err) {
		return 0, io.EOF
	}
	return n, err
}
