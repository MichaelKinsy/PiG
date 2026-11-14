package readbarrier

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

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

// link is one connection between a client address and a fixture server, driven from both ends without a socket.
type link struct {
	barrier        *Barrier
	client, server net.Addr
}

func loopback(port int) net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port} }

// accept is the server accepting the connection. It returns the server's end.
func (l link) accept() net.Conn {
	listener := countingListener{Listener: &oneShotListener{conns: []net.Conn{addressConn{local: l.server, remote: l.client}}}, barrier: l.barrier}
	conn, _ := listener.Accept()
	return conn
}

// startRead is the client's transport entering a socket read. The returned function is that read returning n bytes.
func (l link) startRead() (finish func(n int)) {
	connection, _ := bodyreadhook.BeforeRead(addressConn{local: l.client, remote: l.server})
	return func(n int) { bodyreadhook.AfterRead(connection, n) }
}

// released reports, without waiting, whether the server may write its next record: WaitReading for a request that has already ended returns its first reading of the counts.
func (l link) released() bool {
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), http.LocalAddrContextKey, l.server))
	cancel()
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	request.RemoteAddr = l.client.String()
	return l.barrier.WaitReading(request)
}

// A client port reused by a later connection to the same server must not inherit the earlier connection's counts. The server writes a chunked terminator after an abort that the client never reads, so the earlier connection ends with written > read; the next connection on the same port then never reads "everything written".
func TestReusedClientPortDoesNotInheritStaleCounts(t *testing.T) {
	connection := link{barrier: New(t), client: loopback(40000), server: loopback(50000)}
	old := connection.accept()
	_, _ = old.Write([]byte("0\r\n\r\n")) // the terminator the aborted client never reads
	current := connection.accept()
	_, _ = current.Write([]byte("data: record\n\n"))

	finish := connection.startRead()
	finish(len("data: record\n\n"))
	connection.startRead()

	if !connection.released() {
		t.Fatal("WaitReading never returned for a connection that reused a client port")
	}
}

// Live connections to different servers can have one client address: the kernel chooses a local port per destination, so two servers on the loopback interface see the same client address at the same time, and parallel fixture servers share a barrier. This is the sequence recorded on Linux in TestRPC33AzureTickOrder (runtime/text/chunked and runtime/tool/buffered, both from 127.0.0.1:34830). Counted by client address alone, the second accept zeroed the first server's written bytes and both clients' reads went to one counter, so the first server waited for a client that had long been reading again, until the test's deadline.
func TestConnectionsThatShareAClientAddressAreCountedApart(t *testing.T) {
	barrier := New(t)
	chunked := link{barrier: barrier, client: loopback(34830), server: loopback(32926)}
	buffered := link{barrier: barrier, client: loopback(34830), server: loopback(46680)}

	chunkedServer := chunked.accept()
	chunkedRead := chunked.startRead()
	_, _ = chunkedServer.Write(make([]byte, 117)) // the response headers; the records follow one by one
	bufferedServer := buffered.accept()
	bufferedRead := buffered.startRead()
	chunkedRead(117)
	_, _ = bufferedServer.Write(make([]byte, 999)) // the headers and the whole body
	bufferedRead(999)
	chunked.startRead()

	if !chunked.released() {
		t.Fatal("the chunked server is not released although its client has read all 117 bytes and reads again: another server's connection from the same client address changed its counts")
	}
}

// The other half of the same fault: a read on another server's connection must not release a server whose own client is still handling the last record. Released early, the server writes two records into one socket read, and the client delivers them as one chunk instead of two.
func TestAReadOnAnotherServersConnectionDoesNotReleaseAServer(t *testing.T) {
	barrier := New(t)
	chunked := link{barrier: barrier, client: loopback(34830), server: loopback(32926)}
	buffered := link{barrier: barrier, client: loopback(34830), server: loopback(46680)}

	chunkedServer := chunked.accept()
	buffered.accept()
	chunkedRead := chunked.startRead()
	_, _ = chunkedServer.Write(make([]byte, 117))
	chunkedRead(117) // the client handles the record and has not come back to the socket
	buffered.startRead()

	if chunked.released() {
		t.Fatal("the chunked server is released by a read on another server's connection while its own client is still handling the last record")
	}
	chunked.startRead()
	if !chunked.released() {
		t.Fatal("the chunked server is not released although its client reads again")
	}
}
