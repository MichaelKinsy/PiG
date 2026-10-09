//go:build unix

package routing_test

// pi: packages/server/src/transports/unix/listener.ts

import (
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

// Pi: packages/server/src/listener.ts ServerListener (start passes authorized connections to accept; close stops listening) as packages/server/src/transports/unix/listener.ts implements it (a second start fails with "Unix listener is already started", a start after close with "Unix listener is closing or closed", close is idempotent and removes the socket). The listener is used only through the routing.ServerListener interface.
// Pi source: packages/server/src/transports/unix/listener.ts:29-49 (UnixListener implements ServerListener).
// mutation-checked: the mutant "a second Start succeeds" fails it.
func TestServerListenerContractThroughTheInterface(t *testing.T) {
	t.Parallel()
	path := filepath.Join(shortDirectory(t, "psl-"), "s.sock")
	created, err := routing.CreateUnixListener(routing.UnixListenerOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	var listener routing.ServerListener = created
	accepted := make(chan struct{}, 1)
	acceptor := func(routing.ByteConnection) routing.ByteConnectionHandler {
		accepted <- struct{}{}
		return routing.ByteConnectionHandler{OnData: func([]byte) {}, OnClose: func() {}, OnError: func(error) {}}
	}
	if err := listener.Start(acceptor); err != nil {
		t.Fatal(err)
	}
	exactMessage(t, listener.Start(acceptor), "Unix listener is already started")
	connection, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	select {
	case <-accepted:
	case <-time.After(30 * time.Second):
		t.Fatal("the listener never passed the connection to accept")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil { // idempotent
		t.Fatal(err)
	}
	if _, err := net.Dial("unix", path); err == nil {
		t.Fatal("the socket still accepts connections after close")
	}
	exactMessage(t, listener.Start(acceptor), "Unix listener is closing or closed")
}

// Pi: packages/server/src/transports/unix/preset.ts createUnixServer passes path, mode, maxFrameLength, maxPendingBytes, gracefulCloseTimeoutMs and onError to createUnixListener, and maxFrameLength to the Server; the listener validates them (listener.ts resolveUnixListenerOptions) so an invalid value is rejected when the server is created.
// Pi source: packages/server/src/transports/unix/listener.ts:392-396 (resolveUnixListenerOptions).
// mutation-checked: the mutant "CreateUnixServer drops the listener MaxFrameLength" fails it.
func TestUnixServerOptionsReachTheListener(t *testing.T) {
	t.Parallel()
	path := filepath.Join(shortDirectory(t, "pss-"), "s.sock")
	small := 10.0
	create := func(mutate func(*routing.UnixServerOptions)) error {
		options := routing.UnixServerOptions{Path: path, ServerId: testServerID}
		mutate(&options)
		server, err := routing.CreateUnixServer(newTestServerHost(), options)
		if err == nil {
			_ = server.Close()
		}
		return err
	}
	for name, tc := range map[string]struct {
		mutate  func(*routing.UnixServerOptions, float64)
		message string
		bad     []float64
		good    []float64
	}{
		"maxFrameLength":         {func(o *routing.UnixServerOptions, v float64) { o.MaxFrameLength = &v }, "Server maxFrameLength must be an integer between 1 and 4294967295", []float64{0, 1.5, 4294967296}, []float64{1000}},
		"maxPendingBytes":        {func(o *routing.UnixServerOptions, v float64) { o.MaxFrameLength = &small; o.MaxPendingBytes = &v }, "Server maxPendingBytes must be a safe integer at least maxFrameLength + 4", []float64{13, 1.5}, []float64{14}},
		"gracefulCloseTimeoutMs": {func(o *routing.UnixServerOptions, v float64) { o.GracefulCloseTimeoutMs = &v }, "Server gracefulCloseTimeoutMs must be an integer between 1 and 2147483647", []float64{0, 1.5, 2147483648}, []float64{1, 2147483647}},
		"mode":                   {func(o *routing.UnixServerOptions, v float64) { o.Mode = &v }, "Server Unix socket mode must be an integer between 0 and 0o777", []float64{-1, 0o1000, 1.5}, []float64{0, 0o777}},
	} {
		for _, value := range tc.bad {
			if err := create(func(o *routing.UnixServerOptions) { tc.mutate(o, value) }); err == nil || err.Error() != tc.message {
				t.Errorf("%s %v: error = %v, want %q", name, value, err, tc.message)
			}
		}
		for _, value := range tc.good {
			if err := create(func(o *routing.UnixServerOptions) { tc.mutate(o, value) }); err != nil {
				t.Errorf("%s %v rejected: %v", name, value, err)
			}
		}
	}
}

// Pi: packages/server/src/transports/unix/preset.ts createUnixServer passes handshakeTimeoutMs to the Server, whose handshake timer (server.ts) closes a connection that never sends its hello.
// Pi source: packages/server/src/server.ts:147-153 (handshakeTimeout).
// mutation-checked: the mutant "CreateUnixServer drops HandshakeTimeoutMs" fails it.
func TestUnixServerHandshakeTimeoutClosesASilentConnection(t *testing.T) {
	t.Parallel()
	path := filepath.Join(shortDirectory(t, "psh-"), "s.sock")
	server, err := routing.CreateUnixServer(newTestServerHost(), routing.UnixServerOptions{Path: path, ServerId: testServerID, HandshakeTimeoutMs: new(float64(50))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	if err := startError(server); err != nil {
		t.Fatal(err)
	}
	connection, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	// The default timeout is five seconds; closing within two shows the configured 50 ms reached the server.
	if err := connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, connection); err != nil {
		t.Fatalf("the silent connection stayed open past the handshake timeout: %v", err)
	}
}
