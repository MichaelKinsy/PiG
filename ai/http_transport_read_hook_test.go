package ai

import (
	"errors"
	"io"
	"net"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/bodyreadhook"
)

// The read barrier's hooks see every socket read of a provider connection, each with the connection's two addresses and its byte count.
// idleTimeoutConn.Read counts the read for socketFailure and reads through idleTimeoutConn.read, which calls the hooks around the
// socket read.
func TestIdleTimeoutConnReadsAreSeenByTheReadHooks(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	const record = "0123456789"
	next := make(chan struct{})
	go func() {
		server, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = server.Close() }()
		for range next {
			if _, err := io.WriteString(server, record); err != nil {
				return
			}
		}
	}()
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn := newIdleTimeoutConn(client, "tcp")
	defer func() { _ = conn.Close() }()
	want := bodyreadhook.Connection{Client: client.LocalAddr().String(), Server: client.RemoteAddr().String()}
	var mu sync.Mutex
	var before, after, bytes int
	restore := bodyreadhook.Install(&bodyreadhook.Hooks{
		BeforeRead: func(connection bodyreadhook.Connection) {
			if connection == want {
				mu.Lock()
				before++
				mu.Unlock()
			}
		},
		AfterRead: func(connection bodyreadhook.Connection, n int) {
			if connection == want {
				mu.Lock()
				after++
				bytes += n
				mu.Unlock()
			}
		},
	})
	defer restore()
	buffer := make([]byte, 64)
	reads := 0
	for range 3 {
		next <- struct{}{}
		for got := 0; got < len(record); {
			n, err := conn.Read(buffer)
			reads++
			if err != nil {
				t.Fatal(err)
			}
			got += n
		}
	}
	// The server closes: the read of the end of the stream is seen as well.
	close(next)
	n, err := conn.Read(buffer)
	reads++
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("read after the server closed = %d, %v, want the end of the stream", n, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if before != reads || after != reads || bytes != 3*len(record) {
		t.Fatalf("hooks saw %d reads begin and %d end with %d bytes, want %d reads and %d bytes", before, after, bytes, reads, 3*len(record))
	}
}
