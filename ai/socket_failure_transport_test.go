package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func socketOpError(op string, errno syscall.Errno) error {
	return &net.OpError{Op: op, Net: "tcp", Err: os.NewSyscallError(op, errno)}
}

// fetchFailureText is the message Pi's formatProviderError(normalizeProviderError(error)) gives the fetch rejection in err.
func fetchFailureText(err error) string {
	if rejection, ok := errors.AsType[*nodeTransportError](err); ok {
		return FormatProviderError(NormalizeProviderError(rejection))
	}
	return fmt.Sprint(err)
}

// Node names every write to a socket `write`. Package net names the vectored write of a request body (net.Buffers) "writev" on Unix and
// "wsasend" on Windows; unnamed, its reset was a bare "Connection error.".
func TestSocketWritesAreNamedAsNodeNamesThem(t *testing.T) {
	for _, test := range []struct {
		name          string
		cause         error
		message, code string
	}{
		{"a write reset", socketOpError("write", socketResetErrno), "write ECONNRESET", "ECONNRESET"},
		{"a write to a shut down socket", socketOpError("write", socketPipeErrno), "write EPIPE", "EPIPE"},
		{"a vectored write reset", socketOpError(vectoredWriteOp, socketResetErrno), "write ECONNRESET", "ECONNRESET"},
		{"a vectored write to a shut down socket", socketOpError(vectoredWriteOp, socketPipeErrno), "write EPIPE", "EPIPE"},
		{"a vectored write behind net/http's text", fmt.Errorf("net/http: HTTP/1.x transport connection broken: %w", socketOpError(vectoredWriteOp, socketPipeErrno)), "write EPIPE", "EPIPE"},
		{"a read reset", socketOpError("read", socketResetErrno), "read ECONNRESET", "ECONNRESET"},
		{"a write on a connection this side closed", &net.OpError{Op: vectoredWriteOp, Net: "tcp", Err: net.ErrClosed}, "other side closed", "UND_ERR_SOCKET"},
	} {
		if message, code := nodeConnectionFailure(test.cause); message != test.message || code != test.code {
			t.Errorf("%s: nodeConnectionFailure = %q, %q, want %q, %q", test.name, message, code, test.message, test.code)
		}
	}
	reset := &nodeTransportError{message: "Connection error.", cause: &nodeTransportError{message: "fetch failed", cause: socketOpError(vectoredWriteOp, socketResetErrno)}}
	if got, want := WithCauseDetail(reset), "Connection error.: write ECONNRESET"; got != want {
		t.Errorf("a vectored write reset through the SDK: WithCauseDetail = %q, want %q", got, want)
	}
}

// The connection's record is read by the request that failed on it. A connection that served one request and is reset under the next
// reports the reset to that request; the request after it, which fails on its own dial, reports the dial.
func TestConnectionFailureIsReportedToItsOwnRequest(t *testing.T) {
	var requests atomic.Int32
	var mu sync.Mutex
	var remotes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		remotes = append(remotes, r.RemoteAddr)
		mu.Unlock()
		if requests.Add(1) == 1 {
			_, _ = io.Copy(io.Discard, r.Body)
			_, _ = io.WriteString(w, "ok")
			return
		}
		connection, _ := closeHTTPConnection(t, w)
		if tcp, ok := connection.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
		_ = connection.Close()
	}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	client := streamingHTTPClient()
	post := func(body string) (*http.Response, error) {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return client.Do(request) //nolint:gosec // G704: the request goes to the test's own loopback server.
	}
	response, err := post("hello")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()

	response, err = post(strings.Repeat("x", 1<<20))
	if err == nil {
		_ = response.Body.Close()
		t.Fatal("the request on the reset connection was answered")
	}
	if got, want := fetchFailureText(err), "fetch failed: read ECONNRESET"; got != want {
		t.Errorf("the request on the reset connection: %q, want %q", got, want)
	}
	mu.Lock()
	reused := len(remotes) == 2 && remotes[0] == remotes[1]
	mu.Unlock()
	if !reused {
		t.Fatalf("server saw connections %q, want the second request on the first one's connection", remotes)
	}

	server.Close()
	response, err = post("hello")
	if err == nil {
		_ = response.Body.Close()
		t.Fatal("the request to the closed server was answered")
	}
	if got, want := fetchFailureText(err), "fetch failed: connect ECONNREFUSED "+address; got != want {
		t.Errorf("the request after it, on its own dial: %q, want %q", got, want)
	}
}

// No provider path speaks HTTP/2 (the streaming transport is HTTP/1.1, as Pi's undici dispatcher is), so a connection carries one request
// at a time and its failure belongs to that request.
func TestProviderTransportSpeaksHTTP1ToAnHTTP2Server(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Proto)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := newStreamingHTTPClient(true).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	proto, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.ProtoMajor != 1 || string(proto) != "HTTP/1.1" {
		t.Fatalf("response proto %s, server saw %s, want HTTP/1.1", response.Proto, proto)
	}
}

// halfClosedSocket is a kernel socket whose sending this side has shut down. Its first write begins once a read is pending on it.
type halfClosedSocket struct {
	*net.TCPConn
	readOnce, writeOnce sync.Once
	reading, writing    chan struct{}
}

func (c *halfClosedSocket) Read(p []byte) (int, error) {
	c.readOnce.Do(func() { close(c.reading) })
	return c.TCPConn.Read(p)
}

func (c *halfClosedSocket) Write(p []byte) (int, error) {
	<-c.reading
	c.writeOnce.Do(func() { close(c.writing) })
	return c.TCPConn.Write(p)
}

// A write that fails with EPIPE waits for the read in flight, whose result says how the connection ended. On a kernel socket the peer's
// reset shuts both directions down, so that read returns at once. The one way to fail a write with EPIPE while the read stays pending is
// this side's own shutdown of its sending, which no provider path does and this test does under the connection, with a peer that never
// sends and never closes. The wait then ends with the connection: net/http closes the connection of a cancelled request, the read returns,
// and the request is the abort Pi reports.
func TestSocketFailureWaitEndsWithTheCancelledRequest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		if conn, err := listener.Accept(); err == nil {
			accepted <- conn
		}
	}()
	writing := make(chan struct{})
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		socket := &halfClosedSocket{TCPConn: conn.(*net.TCPConn), reading: make(chan struct{}), writing: writing}
		if err := socket.CloseWrite(); err != nil {
			return nil, err
		}
		return newIdleTimeoutConn(socket, network), nil
	}}
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+listener.Addr().String()+"/", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		response, err := (&nodeFetchTransport{base: transport, connectionIdle: true}).RoundTrip(request)
		if response != nil {
			_ = response.Body.Close()
		}
		done <- err
	}()
	<-writing
	cancel()
	if err := <-done; err == nil || err.Error() != "This operation was aborted" {
		t.Fatalf("cancelled request: %v, want This operation was aborted", err)
	}
	select {
	case conn := <-accepted:
		_ = conn.Close()
	default:
	}
}

// silentConn is a connection that is not a kernel socket: its read returns only when it is closed, and every write fails with EPIPE.
type silentConn struct {
	once   sync.Once
	closed chan struct{}
}

func (c *silentConn) Read([]byte) (int, error) {
	<-c.closed
	return 0, net.ErrClosed
}

func (c *silentConn) Write([]byte) (int, error) {
	return 0, socketOpError("write", socketPipeErrno)
}

func (c *silentConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}
func (c *silentConn) LocalAddr() net.Addr              { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1} }
func (c *silentConn) RemoteAddr() net.Addr             { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 2} }
func (c *silentConn) SetDeadline(time.Time) error      { return nil }
func (c *silentConn) SetReadDeadline(time.Time) error  { return nil }
func (c *silentConn) SetWriteDeadline(time.Time) error { return nil }

// Only a kernel socket promises that a pending read returns once a write has failed with EPIPE. A connection of another kind whose peer
// never sends and never closes is not waited for: the request fails with the write's own error.
func TestSocketFailureDoesNotWaitOnAConnectionThatIsNotASocket(t *testing.T) {
	transport := &http.Transport{DialContext: func(_ context.Context, network, _ string) (net.Conn, error) {
		return newIdleTimeoutConn(&silentConn{closed: make(chan struct{})}, network), nil
	}}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://socket-failure.invalid/", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&nodeFetchTransport{base: transport, connectionIdle: true}).RoundTrip(request)
	if err == nil {
		_ = response.Body.Close()
		t.Fatal("the request was answered")
	}
	if got, want := fetchFailureText(err), "fetch failed: write EPIPE"; got != want {
		t.Fatalf("request = %q, want %q", got, want)
	}
}
