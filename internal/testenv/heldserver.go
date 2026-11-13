package testenv

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
)

// ServeLoopback listens on a loopback port and runs serve on each connection it accepts, closing the connection when serve returns. When the
// test ends it closes the listener and every connection still open, and waits for the serve calls to return.
func ServeLoopback(t testing.TB, serve func(conn net.Conn)) net.Addr {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu      sync.Mutex
		closing bool
		open    = map[net.Conn]struct{}{}
		serving sync.WaitGroup
	)
	serving.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			if closing {
				mu.Unlock()
				_ = conn.Close()
				return
			}
			open[conn] = struct{}{}
			serving.Go(func() {
				serve(conn)
				_ = conn.Close()
				mu.Lock()
				delete(open, conn)
				mu.Unlock()
			})
			mu.Unlock()
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		closing = true
		for conn := range open {
			_ = conn.Close()
		}
		mu.Unlock()
		serving.Wait()
	})
	return listener.Addr()
}

// Await blocks until events delivers a value and returns it. It sets no time bound: an event that never comes holds the test until its
// deadline, whose goroutine dump shows what the test waited for. what names the event in the failure when the test's context ends first.
func Await[T any](t testing.TB, events <-chan T, what string) T {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-t.Context().Done():
		t.Fatalf("the test ended while it waited for %s", what)
		var none T
		return none
	}
}

// HeldAnswer is what a HeldRequestServer does with one request.
type HeldAnswer struct {
	// Body, when not empty, is answered whole as a text/event-stream response; the connection then serves the next request.
	Body string
	// Headers answers the response headers of an event stream and nothing after them. With neither field set nothing is answered at all.
	Headers bool
}

// heldServerEvents is how many Received and Closed events a HeldRequestServer buffers before a connection waits for the test to read them.
const heldServerEvents = 256

// HeldRequestServer is an HTTP/1.1 server over raw connections, for tests of what a client does with a request that the server holds. It reads
// each request whole and answers it as its plan says; after a held request it only reads, so it sees the peer close the connection when that
// happens. (A net/http handler is told of the peer's close by the server's background read, which starts only once the handler has read the
// request body to its end: a handler that never reads the body is never told.)
type HeldRequestServer struct {
	// URL is the server's base URL.
	URL string
	// Received gets one value for each held request, once it was read whole and its response headers, if the plan has any, were written.
	Received chan struct{}
	// Closed gets one value for each connection that ended: true when the peer closed it while the server held a request on it.
	Closed chan bool

	plan     func(request int) HeldAnswer
	requests atomic.Int32
	open     atomic.Int32
}

// NewHeldRequestServer starts a server that treats its n-th request, counted from 1 over all connections, as plan(n) says.
func NewHeldRequestServer(t testing.TB, plan func(request int) HeldAnswer) *HeldRequestServer {
	t.Helper()
	server := &HeldRequestServer{Received: make(chan struct{}, heldServerEvents), Closed: make(chan bool, heldServerEvents), plan: plan}
	server.URL = "http://" + ServeLoopback(t, server.serve).String()
	return server
}

// Requests is the number of requests read whole so far.
func (server *HeldRequestServer) Requests() int { return int(server.requests.Load()) }

// Open is the number of connections being served. A connection leaves the count before its Closed event is sent.
func (server *HeldRequestServer) Open() int { return int(server.open.Load()) }

func (server *HeldRequestServer) serve(conn net.Conn) {
	server.open.Add(1)
	held := false
	defer func() {
		server.open.Add(-1)
		server.Closed <- held
	}()
	reader := bufio.NewReader(conn)
	for {
		request, err := http.ReadRequest(reader)
		if err != nil {
			return
		}
		if _, err := io.Copy(io.Discard, request.Body); err != nil {
			return
		}
		answer := server.plan(int(server.requests.Add(1)))
		if answer.Body != "" {
			if _, err := fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: %d\r\n\r\n%s", len(answer.Body), answer.Body); err != nil {
				return
			}
			continue
		}
		if answer.Headers {
			if _, err := io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n"); err != nil {
				return
			}
		}
		held = true
		server.Received <- struct{}{}
		// Nothing more is sent: the read returns when the peer closes the connection.
		_, _ = reader.ReadByte()
		return
	}
}
