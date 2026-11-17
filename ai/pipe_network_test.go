package ai

import (
	"context"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
)

// A test of what a timeout does to a stream is a test of time, and a real clock leaves the order of two timers to the scheduler. Inside a
// testing/synctest bubble the clock moves only when every goroutine waits, so a stall of the host moves nothing and a gap of 120 ms is shorter
// than a timeout of 300 ms in every run. A loopback socket blocks in the kernel, which does not count as waiting, so the bubble's clock would
// stand still while a goroutine reads one: the server of such a test is reached over net.Pipe.
//
// The code under test is the product's own. The pipe replaces what is below it: the dispatcher's transport (the shared one for the current
// settings, built by the product) dials the pipe server through its DialContext option, the connection wrapped as the product wraps a dialed
// one (newIdleTimeoutConn), and everything above the connection (the idle deadlines, the event idle body, the provider's request transport, net/http's
// own timers) runs unchanged. What the pipe does not carry is a kernel socket and TLS record timing: tests of those stay on real sockets, and
// end on events (TestProvidersReuseOneConnectionAcrossBuilds, TestStreamingClientWithoutRetryReusesTheConnection, TestHTTPIdleTimeoutBeginsAfterTLS).

// clearProxyEnvironment removes the proxy variables, so that the streaming transport dials the pipe server and not a proxy. Run it before the
// bubble: the environment is not the bubble's.
func clearProxyEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "no_proxy", "NO_PROXY"} {
		t.Setenv(name, "")
	}
}

// pipeServer serves a handler over net.Pipe connections. It is also the net.Listener of its http.Server.
type pipeServer struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
	// dials counts the connections the dispatcher opened to the server.
	dials atomic.Int64
}

// newPipeServer starts handler on a pipe server inside the bubble of t and shuts it down when t ends.
func newPipeServer(t testing.TB, handler http.Handler) *pipeServer {
	t.Helper()
	p := &pipeServer{conns: make(chan net.Conn), done: make(chan struct{})}
	server := &http.Server{Handler: handler}
	var serving sync.WaitGroup
	serving.Go(func() { _ = server.Serve(p) })
	t.Cleanup(func() {
		_ = server.Close()
		serving.Wait()
	})
	return p
}

func (p *pipeServer) Accept() (net.Conn, error) {
	select {
	case conn := <-p.conns:
		return conn, nil
	case <-p.done:
		return nil, net.ErrClosed
	}
}

func (p *pipeServer) Close() error {
	p.once.Do(func() { close(p.done) })
	return nil
}

func (p *pipeServer) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// dial opens a connection to the server: the client's end is returned, the server's is accepted by its http.Server.
func (p *pipeServer) dial(ctx context.Context) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case p.conns <- server:
		p.dials.Add(1)
		return client, nil
	case <-p.done:
		_ = client.Close()
		_ = server.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		_ = client.Close()
		_ = server.Close()
		return nil, ctx.Err()
	}
}

// attach makes the dispatcher's transport for the current settings dial this server whatever the address, and gives the transport back when t
// ends. The product builds the transport (newStreamingTransport, shared as the provider clients share it: a settings change replaces it first,
// so that it is built here, inside the bubble) and keeps every other field; the test replaces only DialContext, with the dial the product has
// there: a connection wrapped by newIdleTimeoutConn. The idle timeout must be configured before.
func (p *pipeServer) attach(t testing.TB) *http.Transport {
	t.Helper()
	idleTimeoutMs := ConfiguredHTTPIdleTimeoutMs()
	if err := ConfigureHTTPDispatcher(idleTimeoutMs + 1); err != nil {
		t.Fatal(err)
	}
	baseStreamingHTTPClient(false)
	if err := ConfigureHTTPDispatcher(idleTimeoutMs); err != nil {
		t.Fatal(err)
	}
	transport := baseHTTPTransport(t, baseStreamingHTTPClient(false))
	original := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		conn, err := p.dial(ctx)
		if err != nil {
			return nil, err
		}
		return newIdleTimeoutConn(conn, network), nil
	}
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		transport.DialContext = original
	})
	return transport
}
