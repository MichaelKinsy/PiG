//go:build unix

package routing_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

func expectError(t *testing.T, err error, pattern string) {
	t.Helper()
	if err == nil || !regexp.MustCompile(pattern).MatchString(err.Error()) {
		t.Fatalf("error = %v, want match for /%s/", err, pattern)
	}
}

func shortDirectory(t *testing.T, prefix string) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}

// upstream: packages/server/test/server.test.ts:29 "requires explicit listeners and a canonical UUIDv4 server identity"
func TestServerRequiresExplicitListenersAndACanonicalServerIdentity(t *testing.T) {
	_, err := routing.NewServer(newTestServerHost(), routing.ServerOptions{ServerId: testServerID})
	expectError(t, err, "listeners")
	for _, serverID := range []string{"", "invalid-server"} {
		_, err := routing.NewServer(newTestServerHost(), routing.ServerOptions{Listeners: []routing.ServerListener{}, ServerId: serverID})
		expectError(t, err, "serverId")
	}
}

// upstream: packages/server/test/server.test.ts:35 "rejects concurrent start calls without leaking the Unix listener"
func TestServerRejectsConcurrentStartCallsWithoutLeakingTheUnixListener(t *testing.T) {
	path := filepath.Join(shortDirectory(t, "pss-"), "server.sock")
	inner, err := routing.CreateUnixListener(routing.UnixListenerOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	// The gate holds the first Start inside its listener, so the second call observes the in-progress state deterministically; upstream gets the same state synchronously from its single-threaded event loop.
	gated := &gatedListener{ServerListener: inner, entered: make(chan struct{}), release: make(chan struct{})}
	server := newTestServer(t, routing.ServerOptions{Listeners: []routing.ServerListener{gated}})
	t.Cleanup(func() { _ = server.Close() })
	starting := make(chan error, 1)
	go func() { starting <- startError(server) }()
	<-gated.entered
	expectError(t, startError(server), "starting")
	close(gated.release)
	if err := <-starting; err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket path after close = %v, want ENOENT", err)
	}
}

type gatedListener struct {
	routing.ServerListener
	entered chan struct{}
	release chan struct{}
}

func (listener *gatedListener) Start(accept routing.ByteConnectionAcceptor) error {
	close(listener.entered)
	<-listener.release
	return listener.ServerListener.Start(accept)
}

// timedOutConnection is upstream TimedOutConnection (server.test.ts:50-63): Send fails because a handshake timeout must use the terminal close frame.
type timedOutConnection struct {
	mu         sync.Mutex
	closed     bool
	finalChunk []byte
	done       chan struct{}
}

func (connection *timedOutConnection) Closed() bool {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	return connection.closed
}
func (*timedOutConnection) Send([]byte) error {
	return errors.New("handshake timeout must use the terminal close frame")
}
func (connection *timedOutConnection) Close(finalChunk []byte) error {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	if !connection.closed {
		connection.finalChunk = finalChunk
		connection.closed = true
		close(connection.done)
	}
	return nil
}

// upstream: packages/server/test/server.test.ts:45 "handshake timeout closes with a final hello_error frame"
func TestServerHandshakeTimeoutClosesWithAFinalHelloErrorFrame(t *testing.T) {
	server := newTestServer(t, routing.ServerOptions{Listeners: []routing.ServerListener{}, MaxFrameLength: new(float64(1024)), HandshakeTimeoutMs: new(float64(10))})
	connection := &timedOutConnection{done: make(chan struct{})}
	server.Accept(connection)
	select {
	case <-connection.done:
	case <-time.After(30 * time.Second):
		t.Fatal("the handshake timeout never closed the connection")
	}
	connection.mu.Lock()
	final := connection.finalChunk
	connection.mu.Unlock()
	if final == nil {
		t.Fatal("the connection closed without a final chunk")
	}
	decoder, err := protocol.NewServerMessageDecoder(protocol.FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := decoder.Push(final)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("final frames = %#v, want one hello_error", messages)
	}
	helloError, isHelloError := messages[0].(protocol.ServerHelloError)
	if !isHelloError || helloError.Error.Code != "invalid_request" {
		t.Fatalf("final frame = %#v, want hello_error invalid_request", messages[0])
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
}

// upstream: packages/server/test/server.test.ts:81 "rejects timeout values above Node's maximum timer delay"
func TestServerRejectsTimeoutValuesAboveNodesMaximumTimerDelay(t *testing.T) {
	path := "/tmp/pi-server-timeout-test.sock"
	_, err := routing.CreateUnixServer(newTestServerHost(), routing.UnixServerOptions{Path: path, ServerId: testServerID, HandshakeTimeoutMs: new(float64(2_147_483_648))})
	expectError(t, err, "handshakeTimeoutMs")
	_, err = routing.CreateUnixServer(newTestServerHost(), routing.UnixServerOptions{Path: path, ServerId: testServerID, GracefulCloseTimeoutMs: new(float64(2_147_483_648))})
	expectError(t, err, "gracefulCloseTimeoutMs")
}

// upstream: packages/server/test/server.test.ts:99 "rejects pending-byte limits smaller than one maximum frame"
func TestServerRejectsPendingByteLimitsSmallerThanOneMaximumFrame(t *testing.T) {
	path := filepath.Join(shortDirectory(t, "pss-"), "server.sock")
	_, err := routing.CreateUnixServer(newTestServerHost(), routing.UnixServerOptions{Path: path, ServerId: testServerID, MaxFrameLength: new(float64(128)), MaxPendingBytes: new(float64(131))})
	expectError(t, err, "maxPendingBytes")
}

// upstream: packages/server/test/server.test.ts:111 "rejects close and closed when listener shutdown fails"
func TestServerRejectsCloseAndClosedWhenListenerShutdownFails(t *testing.T) {
	failure := errors.New("listener close failed")
	listener := &failingCloseListener{failure: failure}
	server := newTestServer(t, routing.ServerOptions{Listeners: []routing.ServerListener{listener}})
	if err := startError(server); err != nil {
		t.Fatal(err)
	}
	// server.test.ts:127-128 rejects.toBe(failure) for both close() and closed: the listener's own error, not a wrapper.
	if err := server.Close(); err != failure { //nolint:errorlint // identity is the upstream contract (toBe).
		t.Fatalf("Close = %v, want the listener's own error", err)
	}
	<-server.Closed()
	if err := server.ClosedError(); err != failure { //nolint:errorlint // identity is the upstream contract (toBe).
		t.Fatalf("ClosedError = %v, want the listener's own error", err)
	}
	if !reflect.DeepEqual(listener.closes, 1) {
		t.Fatalf("listener closes = %d, want 1", listener.closes)
	}
}

type failingCloseListener struct {
	failure error
	closes  int
}

func (*failingCloseListener) Start(routing.ByteConnectionAcceptor) error { return nil }
func (listener *failingCloseListener) Close() error {
	listener.closes++
	return listener.failure
}
