package routing_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
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

func TestServerListenerComposition(t *testing.T) {
	// upstream: packages/server/test/listener.test.ts:28 "starts and closes every configured listener"
	t.Run("starts and closes every configured listener", func(t *testing.T) {
		first, second := &testListener{}, &testListener{}
		server := newTestServer(t, routing.ServerOptions{Listeners: []routing.ServerListener{first, second}})
		if err := server.Start(); err != nil {
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
		server := newTestServer(t, routing.ServerOptions{Listeners: []routing.ServerListener{first, second}})
		// listener.test.ts:48 rejects.toBe(failure): the listener's own error, not a wrapper.
		if err := server.Start(); err != failure { //nolint:errorlint // identity is the upstream contract (toBe).
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
