package routing_test

import (
	"testing"

	"errors"

	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

// upstream: packages/server/src/server.ts:103-111 start(): Promise<this> resolves with the started server itself; a second start rejects ("already started") and resolves with nothing.
func TestServerStartReturnsTheStartedServer(t *testing.T) {
	server := newTestServer(t, routing.ServerOptions{Listeners: []routing.ServerListener{}})
	started, err := server.Start()
	if err != nil || started != server {
		t.Fatalf("Start = %p, %v; want the server %p", started, err, server)
	}
	if again, err := server.Start(); err == nil || again != nil {
		t.Fatalf("second Start = %v, %v; want no server and an error", again, err)
	}
}

// Pi packages/server/src/server.ts:95 declares start(): Promise<this>, and startInternal resolves to `this` once every listener started (server.ts:111). A listener failure rejects instead (server.ts:112-130), so Go returns a nil server with the error.
func TestServerStartReturnsTheServerItself(t *testing.T) {
	listener := &testListener{}
	server := newTestServer(t, routing.ServerOptions{Listeners: []routing.ServerListener{listener}})
	t.Cleanup(func() { _ = server.Close() })
	started, err := server.Start()
	if err != nil {
		t.Fatal(err)
	}
	if started != server {
		t.Fatalf("Start returned %p, want the server %p", started, server)
	}
	if accepting, _ := listener.counts(); !accepting {
		t.Fatal("Start returned before the listener started")
	}

	failure := errors.New("listen failed")
	failing := newTestServer(t, routing.ServerOptions{Listeners: []routing.ServerListener{&testListener{startError: failure}}})
	again, err := failing.Start()
	if !errors.Is(err, failure) {
		t.Fatalf("Start error = %v, want the listener failure", err)
	}
	if again != nil {
		t.Fatalf("failed Start returned %p, want nil", again)
	}
}
