package tui

import (
	"testing"
	"time"
)

// A socket the sandbox closes while it is open still sits in l.sockets until its reader's end reaches the loop; a send on it in that
// window must drop it (the socket is closed, still in l.sockets): without `|| socket.closed` in send it panics on the closed out.
func TestALinkSocketTheSandboxClosesWhileOpenDropsASendBeforeItsEndIsSeen(t *testing.T) {
	l, release, _, closedByLauncher := socketLauncher(t)
	l.openSocket(1)
	socket := l.sockets[1]
	release()
	nextEvent(t, l)()
	if !socket.open {
		t.Fatal("the socket did not open")
	}
	if err := l.handle(SandboxMessage{Kind: "socket_close", Socket: 1}); err != nil {
		t.Fatal(err)
	}
	if l.sockets[1] != socket {
		t.Fatal("the socket was forgotten before its reader ended")
	}
	l.send(1, []byte("hello"))
	select {
	case <-closedByLauncher:
	case <-time.After(10 * time.Second):
		t.Fatal("the connection was not closed")
	}
	nextEvent(t, l)()
	if l.sockets[1] != nil {
		t.Fatal("the socket is still known after its reader ended")
	}
}
