package client

// pi: packages/client/src/connection.ts

import (
	"context"
	"errors"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// recordingTransport is a Pi-shaped ByteTransport: Send blocks until release lets it return or its ctx is cancelled, and records what it saw.
type recordingTransport struct {
	handlers ByteTransportHandlers
	hello    []byte
	mu       sync.Mutex
	chunks   [][]byte
	inFlight int
	maxSeen  int
	release  chan struct{}
	started  chan struct{}
	ctxErr   chan error
	closed   int
}

func newRecordingTransport() *recordingTransport {
	return &recordingTransport{release: make(chan struct{}, 16), started: make(chan struct{}, 16), ctxErr: make(chan error, 16)}
}

func (transport *recordingTransport) Send(ctx context.Context, chunk []byte) error {
	transport.mu.Lock()
	transport.chunks = append(transport.chunks, slices.Clone(chunk))
	transport.inFlight++
	transport.maxSeen = max(transport.maxSeen, transport.inFlight)
	transport.mu.Unlock()
	transport.started <- struct{}{}
	defer func() {
		transport.mu.Lock()
		transport.inFlight--
		transport.mu.Unlock()
	}()
	select {
	case <-transport.release:
		// The server answers the client hello on a later event-loop turn than the send that carried it.
		transport.mu.Lock()
		first := len(transport.chunks) == 1
		transport.mu.Unlock()
		if first && transport.hello != nil {
			go transport.handlers.OnData(transport.hello)
		}
		return nil
	case <-ctx.Done():
		transport.ctxErr <- context.Cause(ctx)
		return context.Cause(ctx)
	}
}

func (transport *recordingTransport) Close() {
	transport.mu.Lock()
	transport.closed++
	transport.mu.Unlock()
}

func newAdaptedConnection(t *testing.T, factory ByteTransportFactory) (*Connection, *struct {
	mu     sync.Mutex
	states []ConnectionState
}) {
	t.Helper()
	recorded := &struct {
		mu     sync.Mutex
		states []ConnectionState
	}{}
	connection, err := NewConnection(ConnectionOptions{
		TransportFactory: factory, ServerId: testServerId,
		OnHandshake: func(protocol.ServerHello) error { return nil },
		OnMessage:   func(protocol.ServerMessage) {},
		OnStateChange: func(change ConnectionStateChange) {
			recorded.mu.Lock()
			recorded.states = append(recorded.states, change.State)
			recorded.mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return connection, recorded
}

// connection.ts:120-127 awaits transportFactory(handlers) and fails the attempt with the rejection or the throw; packages/client transport.ts:3 send
// settles per chunk. A Pi-shaped ByteTransport therefore reaches the Connection through ClientOptions.TransportFactory: the hello it
// is sent first, later frames follow in invocation order, and no two Sends overlap.
// mutation-checked: a Submit that starts one goroutine per chunk, or drops the queue order, fails the order and overlap assertions.
func TestConnectionRunsAPiShapedTransportFactoryInOrder(t *testing.T) {
	transport := newRecordingTransport()
	hello := encodeServer(protocol.ServerHello{Version: protocol.ProtocolVersion, ServerId: testServerId})
	connection, _ := newAdaptedConnection(t, func(_ context.Context, handlers ByteTransportHandlers) (ByteTransport, error) {
		transport.handlers, transport.hello = handlers, hello
		return transport, nil
	})
	connected := make(chan error, 1)
	go func() {
		_, err := connection.Connect(t.Context())
		connected <- err
	}()
	<-transport.started // the client hello is the first Send
	transport.release <- struct{}{}
	if err := <-connected; err != nil {
		t.Fatalf("Connect = %v", err)
	}
	for _, frame := range [][]byte{[]byte("a"), []byte("b"), []byte("c")} {
		if err := connection.Send(frame); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		<-transport.started
		transport.release <- struct{}{}
	}
	connection.Disconnect(nil)
	transport.mu.Lock()
	defer transport.mu.Unlock()
	var got []string
	for _, chunk := range transport.chunks[1:] {
		got = append(got, string(chunk))
	}
	if !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("sends after the hello = %q; want a, b, c in invocation order", got)
	}
	if transport.maxSeen != 1 {
		t.Fatalf("%d Sends overlapped; a transport's sends are delivered one after another", transport.maxSeen)
	}
}

// connection.ts:123-126: a factory that rejects or throws fails the attempt with a DisconnectedError that carries its message.
// mutation-checked: an adapter that lets the panic escape, or ignores the error, fails it.
func TestConnectionFailsTheAttemptWhenAPiShapedFactoryErrsOrPanics(t *testing.T) {
	for name, factory := range map[string]ByteTransportFactory{
		"error": func(context.Context, ByteTransportHandlers) (ByteTransport, error) {
			return nil, errors.New("factory failed")
		},
		"panic": func(context.Context, ByteTransportHandlers) (ByteTransport, error) { panic("factory failed") },
	} {
		t.Run(name, func(t *testing.T) {
			connection, _ := newAdaptedConnection(t, factory)
			_, err := connection.Connect(t.Context())
			var disconnected *DisconnectedError
			if !errors.As(err, &disconnected) || disconnected.Message != "factory failed" {
				t.Fatalf("Connect = %v; want a DisconnectedError with the factory's message", err)
			}
			if state := connection.State(); state != Disconnected {
				t.Fatalf("state = %s", state)
			}
		})
	}
}

// Closing the Connection closes the transport and cancels the waits of the Sends it still owes, so a blocked Send cannot outlive the connection.
// mutation-checked: a Close that does not cancel the send context leaves the Send blocked and the receive times out.
func TestClosingAConnectionCancelsABlockedPiShapedSend(t *testing.T) {
	transport := newRecordingTransport()
	hello := encodeServer(protocol.ServerHello{Version: protocol.ProtocolVersion, ServerId: testServerId})
	connection, _ := newAdaptedConnection(t, func(_ context.Context, handlers ByteTransportHandlers) (ByteTransport, error) {
		transport.handlers, transport.hello = handlers, hello
		return transport, nil
	})
	connected := make(chan error, 1)
	go func() {
		_, err := connection.Connect(t.Context())
		connected <- err
	}()
	<-transport.started
	transport.release <- struct{}{}
	if err := <-connected; err != nil {
		t.Fatal(err)
	}
	if err := connection.Send([]byte("stuck")); err != nil {
		t.Fatal(err)
	}
	<-transport.started
	connection.Disconnect(nil)
	select {
	case err := <-transport.ctxErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked Send ended with %v; want the cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the blocked Send was not cancelled by closing the connection")
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.closed != 1 {
		t.Fatalf("transport closed %d times, want once", transport.closed)
	}
}

// unix.ts:160-176 send(): the unix transport's Send returns when the write settles, refuses a chunk when its ctx is already cancelled, and stops waiting
// for an admitted one when the ctx is cancelled.
// mutation-checked: a Send that ignores ctx writes the refused chunk or never returns.
func TestUnixByteTransportSendHonoursItsContext(t *testing.T) {
	t.Parallel()
	server, clientSide := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	transport := newUnixByteTransport(clientSide, 1<<20, ByteTransportHandlers{OnData: func([]byte) {}, OnClose: func() {}, OnError: func(error) {}})
	t.Cleanup(transport.Close)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := transport.Send(cancelled, []byte("refused")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Send with a cancelled ctx = %v; want the cancellation", err)
	}
	received := make(chan []byte, 1)
	go func() {
		data := make([]byte, 2)
		n, _ := server.Read(data)
		received <- data[:n]
	}()
	if err := transport.Send(t.Context(), []byte("ok")); err != nil {
		t.Fatalf("Send = %v", err)
	}
	if got := string(<-received); got != "ok" {
		t.Fatalf("the peer read %q; want only the admitted chunk", got)
	}
	waiting, stop := context.WithCancel(t.Context())
	go func() { time.Sleep(20 * time.Millisecond); stop() }()
	// Nothing reads from the peer now, so the write stays pending until the ctx ends the wait.
	if err := transport.Send(waiting, []byte("pending")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Send of a stalled write = %v; want the cancellation", err)
	}
}

// admittingTransport is a ByteTransport that also admits sends itself (Submit), as the unix transport does.
type admittingTransport struct {
	admitted atomic.Int32
	sent     atomic.Int32
}

func (transport *admittingTransport) Send(context.Context, []byte) error {
	transport.sent.Add(1)
	return nil
}
func (transport *admittingTransport) Submit(_ []byte, complete func(error)) {
	transport.admitted.Add(1)
	complete(nil)
}
func (transport *admittingTransport) Close() {}

// Pi's connection.ts:102-118 calls transport.send(frame) before Connection.send returns, so a transport that admits a send itself is
// handed the frame synchronously and is not driven through the one-at-a-time Send drain.
// mutation-checked: a Connection that always uses the drain admits the frame later and fails the count.
func TestConnectionAdmitsAFrameBeforeSendReturnsWhenTheTransportAdmitsItself(t *testing.T) {
	transport := &admittingTransport{}
	hello := encodeServer(protocol.ServerHello{Version: protocol.ProtocolVersion, ServerId: testServerId})
	connection, _ := newAdaptedConnection(t, func(_ context.Context, handlers ByteTransportHandlers) (ByteTransport, error) {
		go handlers.OnData(hello)
		return transport, nil
	})
	if _, err := connection.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := transport.admitted.Load()
	if err := connection.Send([]byte("frame")); err != nil {
		t.Fatal(err)
	}
	if got := transport.admitted.Load(); got != before+1 {
		t.Fatalf("admitted %d frames after Send returned, want %d: the frame must be admitted synchronously", got, before+1)
	}
	if transport.sent.Load() != 0 {
		t.Fatal("the blocking Send ran although the transport admits sends itself")
	}
}

// encodeServer frames one server message; the Unix-only and platform-neutral transport tests share it.
func encodeServer(message protocol.ServerMessage) []byte {
	frame, err := protocol.EncodeServerMessage(message, protocol.FrameDecoderOptions{})
	if err != nil {
		panic(err)
	}
	return frame
}
