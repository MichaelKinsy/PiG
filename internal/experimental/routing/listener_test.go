package routing_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing/routingtest"
)

// upstream: packages/server/test/listener.test.ts TestListener.
type testListener struct {
	mu         sync.Mutex
	accept     routing.ByteConnectionAcceptor
	startCount int
	closeCount int
	startError error
}

func (listener *testListener) Start(accept routing.ByteConnectionAcceptor) error {
	listener.mu.Lock()
	defer listener.mu.Unlock()
	listener.startCount++
	listener.accept = accept
	return listener.startError
}

func (listener *testListener) Close() error {
	listener.mu.Lock()
	defer listener.mu.Unlock()
	listener.closeCount++
	return nil
}

func (listener *testListener) counts() (accepting bool, closes int) {
	listener.mu.Lock()
	defer listener.mu.Unlock()
	return listener.accept != nil, listener.closeCount
}

// newTestServer is new Server(new TestServerHost(), options) with the shared test server ID when options omit one.
func newTestServer(t *testing.T, options routing.ServerOptions) *routing.Server {
	t.Helper()
	if options.ServerId == "" {
		options.ServerId = testServerID
	}
	server, err := routing.NewServer(newTestServerHost(), options)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

// createTestServer is upstream createTestServer(options).server; an option the server rejects fails the test.
func createTestServer(t *testing.T, options routingtest.TestServerOptions) *routing.Server {
	t.Helper()
	created, err := routingtest.CreateTestServer(options)
	if err != nil {
		t.Fatal(err)
	}
	return created.Server
}

// TestServerListenerComposition: packages/server/src/listener.ts:4-8 start(accept) and close() are called on every configured listener (server.ts start/close).
// mutation-checked: Server.Start not calling ServerListener.Start, and Server.Close not calling ServerListener.Close, fail it.
func TestServerListenerComposition(t *testing.T) {
	// upstream: packages/server/test/listener.test.ts:28 "starts and closes every configured listener"
	t.Run("starts and closes every configured listener", func(t *testing.T) {
		first, second := &testListener{}, &testListener{}
		server := createTestServer(t, routingtest.TestServerOptions{Listeners: []routing.ServerListener{first, second}})
		if _, err := server.Start(); err != nil {
			t.Fatal(err)
		}
		for _, listener := range []*testListener{first, second} {
			if accepting, _ := listener.counts(); !accepting {
				t.Fatal("listener did not receive the connection acceptor")
			}
		}
		if err := server.Close(); err != nil {
			t.Fatal(err)
		}
		for _, listener := range []*testListener{first, second} {
			if _, closes := listener.counts(); closes != 1 {
				t.Fatalf("listener close count = %d, want 1", closes)
			}
		}
	})

	// upstream: packages/server/test/listener.test.ts:42 "closes previously started listeners when startup fails"
	t.Run("closes previously started listeners when startup fails", func(t *testing.T) {
		first := &testListener{}
		failure := errors.New("listener failed")
		second := &testListener{startError: failure}
		server := createTestServer(t, routingtest.TestServerOptions{Listeners: []routing.ServerListener{first, second}})
		// listener.test.ts:48 rejects.toBe(failure): the listener's own error, not a wrapper.
		if _, err := server.Start(); err != failure { //nolint:errorlint // identity is the upstream contract (toBe).
			t.Fatalf("Start = %v, want the listener's own error", err)
		}
		if _, closes := first.counts(); closes != 1 {
			t.Fatalf("started listener close count = %d, want 1", closes)
		}
		if _, closes := second.counts(); closes != 0 {
			t.Fatalf("failed listener close count = %d, want 0", closes)
		}
	})
}

// upstream: packages/server/src/listener.ts:4-8 ServerListener.start(accept) / close(): a listener is driven through its interface, start hands it the acceptor, close is observable on the same value, and a start failure is the listener's own error.
func TestServerListenerInterfaceStartAndClose(t *testing.T) {
	impl := &testListener{}
	var listener routing.ServerListener = impl
	if err := listener.Start(func(routing.ByteConnection) routing.ByteConnectionHandler { return routing.ByteConnectionHandler{} }); err != nil {
		t.Fatal(err)
	}
	if accepting, closes := impl.counts(); !accepting || closes != 0 {
		t.Fatalf("after Start accepting = %v, closes = %d", accepting, closes)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, closes := impl.counts(); closes != 1 {
		t.Fatalf("closes = %d, want 1", closes)
	}
	failing := &testListener{startError: errors.New("bind failed")}
	listener = failing
	if err := listener.Start(nil); err == nil || err.Error() != "bind failed" {
		t.Fatalf("Start error = %v", err)
	}
}
