package readbarrier

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/bodyreadhook"
)

type addressConn struct {
	net.Conn
	local, remote net.Addr
}

func (c addressConn) LocalAddr() net.Addr       { return c.local }
func (c addressConn) RemoteAddr() net.Addr      { return c.remote }
func (addressConn) Write(p []byte) (int, error) { return len(p), nil }

type oneShotListener struct {
	net.Listener
	conns []net.Conn
}

func (l *oneShotListener) Accept() (net.Conn, error) {
	conn := l.conns[0]
	l.conns = l.conns[1:]
	return conn, nil
}

// A client port reused by a later connection must not inherit the earlier connection's counts. The server writes a chunked terminator after an abort that the client never reads, so the earlier connection ends with written > read; the next connection on the same port then never reads "everything written".
func TestReusedClientPortDoesNotInheritStaleCounts(t *testing.T) {
	address := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40000}
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
	defer restore()
	listener := countingListener{Listener: &oneShotListener{conns: []net.Conn{
		addressConn{remote: address}, addressConn{remote: address},
	}}, barrier: barrier}
	client := addressConn{local: address}

	old, _ := listener.Accept()
	_, _ = old.Write([]byte("0\r\n\r\n")) // the terminator the aborted client never reads
	current, _ := listener.Accept()
	_, _ = current.Write([]byte("data: record\n\n"))

	bodyreadhook.BeforeRead(client)
	bodyreadhook.AfterRead(address.String(), len("data: record\n\n"))
	bodyreadhook.BeforeRead(client)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if !barrier.WaitReading(ctx, address.String()) {
		t.Fatal("WaitReading never returned for a connection that reused a client port")
	}
}
