package client

// pi: packages/client/src/client.ts

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// scriptedTransport is a callbackByteTransport whose behaviour each test supplies. It counts Close calls.
type scriptedTransport struct {
	mu     sync.Mutex
	closes int
	closed chan struct{}
	frames [][]byte
	onSend func(frame []byte, complete func(error))
}

func (transport *scriptedTransport) Submit(chunk []byte, complete func(error)) {
	transport.mu.Lock()
	transport.frames = append(transport.frames, chunk)
	onSend := transport.onSend
	transport.mu.Unlock()
	if onSend == nil {
		complete(nil)
		return
	}
	onSend(chunk, complete)
}
func (transport *scriptedTransport) Close() {
	transport.mu.Lock()
	transport.closes++
	if transport.closed != nil && transport.closes == 1 {
		close(transport.closed)
	}
	transport.mu.Unlock()
}
func (transport *scriptedTransport) closeCount() int {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.closes
}

type stateRecorder struct {
	mu      sync.Mutex
	changes []ConnectionStateChange
}

func (recorder *stateRecorder) listener() *ConnectionStateChangeListener {
	return NewConnectionStateChangeListener(func(change ConnectionStateChange) {
		recorder.mu.Lock()
		recorder.changes = append(recorder.changes, change)
		recorder.mu.Unlock()
	})
}
func (recorder *stateRecorder) states() []ConnectionState {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	states := []ConnectionState{}
	for _, change := range recorder.changes {
		states = append(states, change.State)
	}
	return states
}
func (recorder *stateRecorder) last() ConnectionStateChange {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.changes[len(recorder.changes)-1]
}

func serverFrame(t *testing.T, message protocol.ServerMessage) []byte {
	t.Helper()
	frame, err := protocol.EncodeServerMessage(message, protocol.FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func scriptedClient(t *testing.T, factory callbackByteTransportFactory) (*Client, *stateRecorder) {
	t.Helper()
	client, err := NewClient(ClientOptions{ServerId: testServerId, TransportFactory: callbackFactory(factory)})
	if err != nil {
		t.Fatal(err)
	}
	recorder := &stateRecorder{}
	if _, err := client.OnConnectionStateChange(recorder.listener()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Dispose()
		_ = client.WaitClosed(context.Background())
	})
	return client, recorder
}

func requireValidation(t *testing.T, err error, message string) {
	t.Helper()
	validation, ok := errors.AsType[*protocol.ProtocolValidationError](err)
	if !ok || validation.Message != message {
		t.Fatalf("error=%T %v, want ProtocolValidationError %q", err, err, message)
	}
}
func requireDisconnected(t *testing.T, err error, message string) {
	t.Helper()
	_ = asDisconnected(t, err, message)
}
func asDisconnected(t *testing.T, err error, message string) *DisconnectedError {
	t.Helper()
	disconnected, ok := errors.AsType[*DisconnectedError](err)
	if !ok || disconnected.Message != message {
		t.Fatalf("error=%T %v, want DisconnectedError %q", err, err, message)
	}
	return disconnected
}

// Pi: packages/client/src/connection.ts:42-53 validates maxFrameLength at construction; connection.ts:31 MAX_UINT32.
func TestClientConnectionRejectsInvalidMaxFrameLength(t *testing.T) {
	t.Parallel()
	factory := func(context.Context, ByteTransportHandlers, func(callbackByteTransport, error)) {}
	for _, limit := range []float64{0, -1, 1.5, math.NaN(), math.Inf(1), 4294967296} {
		_, err := NewClient(ClientOptions{ServerId: testServerId, TransportFactory: callbackFactory(factory), MaxFrameLength: &limit})
		if err == nil || err.Error() != "Client maxFrameLength must be an integer between 1 and 4294967295" {
			t.Fatalf("limit %v error=%v", limit, err)
		}
	}
	for _, limit := range []float64{1, 4294967295} {
		client, err := NewClient(ClientOptions{ServerId: testServerId, TransportFactory: callbackFactory(factory), MaxFrameLength: &limit})
		if err != nil || client.connection.MaxFrameLength() != limit {
			t.Fatalf("limit %v rejected: %v", limit, err)
		}
	}
	client, err := NewClient(ClientOptions{ServerId: testServerId, TransportFactory: callbackFactory(factory)})
	if err != nil || client.connection.MaxFrameLength() != 16777216 {
		t.Fatalf("default limit = %v, %v", client.connection.MaxFrameLength(), err)
	}
}

// Pi: connection.ts:129-196 #handleMessage and #handleClose; errors.ts:19-34 toDisconnectedError; client.test.ts handshake cases.
func TestClientHandshakeFailuresCarryPiErrorsAndCloseOnlyLocalFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		script func(t *testing.T, handlers ByteTransportHandlers, complete func(callbackByteTransport, error), transport *scriptedTransport)
		check  func(t *testing.T, err error)
		closes int
	}{
		{"typed hello error", func(t *testing.T, handlers ByteTransportHandlers, complete func(callbackByteTransport, error), transport *scriptedTransport) {
			transport.onSend = func(_ []byte, done func(error)) {
				handlers.OnData(serverFrame(t, protocol.ServerHelloError{Error: protocol.ProtocolError{Code: "denied", Message: "no entry"}}))
				done(nil)
			}
			complete(transport, nil)
		}, func(t *testing.T, err error) {
			failure, ok := errors.AsType[*ServerError](err)
			if !ok || failure.Code != "denied" || failure.Error() != "no entry" {
				t.Fatalf("error=%T %v", err, err)
			}
		}, 1},
		{"first message is not a hello", func(t *testing.T, handlers ByteTransportHandlers, complete func(callbackByteTransport, error), transport *scriptedTransport) {
			transport.onSend = func(_ []byte, done func(error)) {
				handlers.OnData(serverFrame(t, protocol.AttachmentEnvelope{}))
				done(nil)
			}
			complete(transport, nil)
		}, func(t *testing.T, err error) { requireValidation(t, err, "Expected server hello as first message") }, 1},
		{"hello from another server", func(t *testing.T, handlers ByteTransportHandlers, complete func(callbackByteTransport, error), transport *scriptedTransport) {
			transport.onSend = func(_ []byte, done func(error)) {
				handlers.OnData(serverFrame(t, protocol.ServerHello{Version: protocol.ProtocolVersion, ServerId: "00000000-0000-4000-8000-000000000002"}))
				done(nil)
			}
			complete(transport, nil)
		}, func(t *testing.T, err error) {
			requireValidation(t, err, `Connected server "00000000-0000-4000-8000-000000000002" does not match "`+testServerId+`"`)
		}, 1},
		{"malformed frame", func(t *testing.T, handlers ByteTransportHandlers, complete func(callbackByteTransport, error), transport *scriptedTransport) {
			transport.onSend = func(_ []byte, done func(error)) {
				handlers.OnData([]byte{0, 0, 0, 1, 0xff})
				done(nil)
			}
			complete(transport, nil)
		}, func(t *testing.T, err error) {
			requireValidation(t, err, "Invalid server protocol frame: CBOR break marker is not supported")
		}, 1},
		{"remote close before hello", func(t *testing.T, handlers ByteTransportHandlers, complete func(callbackByteTransport, error), transport *scriptedTransport) {
			transport.onSend = func(_ []byte, done func(error)) { handlers.OnClose(); done(nil) }
			complete(transport, nil)
		}, func(t *testing.T, err error) { requireDisconnected(t, err, "Byte transport closed") }, 0},
		{"remote close inside a frame", func(t *testing.T, handlers ByteTransportHandlers, complete func(callbackByteTransport, error), transport *scriptedTransport) {
			transport.onSend = func(_ []byte, done func(error)) {
				handlers.OnData([]byte{0, 0})
				handlers.OnClose()
				done(nil)
			}
			complete(transport, nil)
		}, func(t *testing.T, err error) {
			requireValidation(t, err, "Invalid server protocol framing: Truncated frame at end of stream")
		}, 0},
		{"transport error", func(t *testing.T, handlers ByteTransportHandlers, complete func(callbackByteTransport, error), transport *scriptedTransport) {
			transport.onSend = func(_ []byte, done func(error)) { handlers.OnError(errors.New("socket reset")); done(nil) }
			complete(transport, nil)
		}, func(t *testing.T, err error) {
			disconnected := asDisconnected(t, err, "socket reset")
			if disconnected.Cause == nil || disconnected.Cause.Error() != "socket reset" {
				t.Fatalf("cause=%v", disconnected.Cause)
			}
		}, 1},
		{"hello write rejected", func(t *testing.T, handlers ByteTransportHandlers, complete func(callbackByteTransport, error), transport *scriptedTransport) {
			transport.onSend = func(_ []byte, done func(error)) { done(errors.New("write failed")) }
			complete(transport, nil)
		}, func(t *testing.T, err error) { requireDisconnected(t, err, "write failed") }, 1},
		{"hello write throws", func(t *testing.T, handlers ByteTransportHandlers, complete func(callbackByteTransport, error), transport *scriptedTransport) {
			transport.onSend = func([]byte, func(error)) { panic(errors.New("send threw")) }
			complete(transport, nil)
		}, func(t *testing.T, err error) { requireDisconnected(t, err, "send threw") }, 1},
		{"factory failure", func(t *testing.T, handlers ByteTransportHandlers, complete func(callbackByteTransport, error), transport *scriptedTransport) {
			complete(nil, errors.New("dial failed"))
		}, func(t *testing.T, err error) { requireDisconnected(t, err, "dial failed") }, 0},
		{"factory throws", func(t *testing.T, handlers ByteTransportHandlers, complete func(callbackByteTransport, error), transport *scriptedTransport) {
			panic(errors.New("factory threw"))
		}, func(t *testing.T, err error) { requireDisconnected(t, err, "factory threw") }, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			transport := &scriptedTransport{}
			client, recorder := scriptedClient(t, func(_ context.Context, handlers ByteTransportHandlers, complete func(callbackByteTransport, error)) {
				test.script(t, handlers, complete, transport)
			})
			_, err := client.Connect(t.Context())
			if err == nil {
				t.Fatal("handshake succeeded")
			}
			test.check(t, err)
			if got := transport.closeCount(); got != test.closes {
				t.Fatalf("local close count=%d, want %d", got, test.closes)
			}
			states := recorder.states()
			if len(states) != 2 || states[0] != Connecting || states[1] != Disconnected || !errors.Is(recorder.last().Error, err) {
				t.Fatalf("states=%v last error=%v, want the connect error", states, recorder.last().Error)
			}
			if client.ConnectionState() != Disconnected || client.Hello() != nil {
				t.Fatalf("state=%s hello=%v", client.ConnectionState(), client.Hello())
			}
		})
	}
	t.Run("a DisconnectedError from the transport is kept as is", func(t *testing.T) {
		t.Parallel()
		failure := &DisconnectedError{Message: "custom", Cause: errors.New("root")}
		transport := &scriptedTransport{}
		client, _ := scriptedClient(t, func(_ context.Context, handlers ByteTransportHandlers, complete func(callbackByteTransport, error)) {
			transport.onSend = func([]byte, func(error)) { handlers.OnError(failure) }
			complete(transport, nil)
		})
		if _, err := client.Connect(t.Context()); err != failure { //nolint:errorlint // identity of the transport's DisconnectedError.
			t.Fatalf("error=%v, want the same DisconnectedError", err)
		}
	})
}

// Pi: connection.ts:55-98 connect/disconnect, #openTransport:111-127 (a transport that finishes opening after the attempt ended is closed), client.ts:131-135 connect clears hello.
func TestClientConnectionLifecycleAndStaleTransport(t *testing.T) {
	t.Parallel()
	t.Run("connect while connecting or connected", func(t *testing.T) {
		t.Parallel()
		var release func(callbackByteTransport, error)
		attempts := make(chan struct{}, 4)
		client, _ := scriptedClient(t, func(_ context.Context, _ ByteTransportHandlers, complete func(callbackByteTransport, error)) {
			release = complete
			attempts <- struct{}{}
		})
		done := make(chan error, 1)
		go func() { _, err := client.Connect(t.Context()); done <- err }()
		<-attempts
		_, err := client.Connect(t.Context())
		requireDisconnected(t, err, "Client is already connecting")
		client.Disconnect("Client disconnected")
		requireDisconnected(t, <-done, "Client disconnected")
		release(&scriptedTransport{}, nil) // the factory attempt must finish for WaitClosed
		server := newMemoryByteServer("")
		connected := mustConnectClient(t, server)
		_, err = connected.Connect(t.Context())
		requireDisconnected(t, err, "Client is already connected")
	})
	t.Run("a transport that opens after the attempt ended is closed unused", func(t *testing.T) {
		t.Parallel()
		var complete func(callbackByteTransport, error)
		attempts := make(chan struct{}, 1)
		transport := &scriptedTransport{closed: make(chan struct{})}
		client, _ := scriptedClient(t, func(_ context.Context, _ ByteTransportHandlers, done func(callbackByteTransport, error)) {
			complete = done
			attempts <- struct{}{}
		})
		result := make(chan error, 1)
		go func() { _, err := client.Connect(t.Context()); result <- err }()
		<-attempts
		client.Disconnect("Client disconnected")
		requireDisconnected(t, <-result, "Client disconnected")
		// Pi closes the late transport in the continuation that awaited the factory (connection.ts:128-131), after the factory settles.
		complete(transport, nil)
		select {
		case <-transport.closed:
		case <-time.After(10 * time.Second):
			t.Fatal("the transport that opened after the attempt ended was never closed")
		}
		if transport.closeCount() != 1 {
			t.Fatalf("stale transport close count=%d, want 1", transport.closeCount())
		}
		transport.mu.Lock()
		sent := len(transport.frames)
		transport.mu.Unlock()
		if sent != 0 {
			t.Fatalf("stale transport received %d frames", sent)
		}
	})
	t.Run("disconnect reasons and reconnect", func(t *testing.T) {
		t.Parallel()
		server := newMemoryByteServer("")
		client, err := NewClient(ClientOptions{ServerId: testServerId, TransportFactory: callbackFactory(server.factory())})
		if err != nil {
			t.Fatal(err)
		}
		recorder := &stateRecorder{}
		if _, err := client.OnConnectionStateChange(recorder.listener()); err != nil {
			t.Fatal(err)
		}
		client.Disconnect("Client disconnected") // disconnected: no event
		if len(recorder.states()) != 0 {
			t.Fatalf("disconnect while disconnected emitted %v", recorder.states())
		}
		hello, err := client.Connect(t.Context())
		if err != nil || hello.ServerId != testServerId || client.Hello() == nil || client.ServerId() != testServerId {
			t.Fatalf("connect: %v %v", hello, err)
		}
		client.Disconnect("Client disconnected")
		requireDisconnected(t, recorder.last().Error, "Client disconnected")
		if server.closeCount() != 1 || client.Hello() != nil {
			t.Fatalf("closes=%d hello=%v", server.closeCount(), client.Hello())
		}
		if _, err := client.Reconnect(t.Context()); err != nil || client.Hello() == nil || !client.Connected() {
			t.Fatalf("reconnect: %v", err)
		}
		client.Disconnect("operator request")
		requireDisconnected(t, recorder.last().Error, "operator request")
		want := []ConnectionState{Connecting, Connected, Disconnected, Connecting, Connected, Disconnected}
		got := recorder.states()
		if len(got) != len(want) {
			t.Fatalf("states=%v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("states=%v, want %v", got, want)
			}
		}
		if err := client.Dispose(); err != nil {
			t.Fatal(err)
		}
		_, err = client.Connect(t.Context())
		if _, ok := errors.AsType[*ClientDisposedError](err); !ok || err.Error() != "Client is disposed" {
			t.Fatalf("connect after dispose=%v", err)
		}
	})
}

// Pi: client.ts:293-310 #handleMessage, connection.ts:189-196 handshake messages after connect, client.ts:158-170 serviceCatalogue.
func TestClientConnectedProtocolViolationsDisconnect(t *testing.T) {
	t.Parallel()
	hello := protocol.ServerHello{Version: protocol.ProtocolVersion, ServerId: testServerId}
	for _, test := range []struct {
		name    string
		trigger func(t *testing.T, server *memoryByteServer, client *Client)
		message string
	}{
		{"second hello", func(t *testing.T, server *memoryByteServer, _ *Client) { server.send(t, hello) }, "Unexpected handshake message"},
		{"hello error after connect", func(t *testing.T, server *memoryByteServer, _ *Client) {
			server.send(t, protocol.ServerHelloError{Error: protocol.ProtocolError{Code: "c", Message: "m"}})
		}, "Unexpected handshake message"},
		{"response without a request", func(t *testing.T, server *memoryByteServer, _ *Client) {
			server.send(t, protocol.ResponseEnvelope{Id: "request-9", Ok: true})
		}, "Response has no matching request"},
		{"attachment of another server", func(t *testing.T, server *memoryByteServer, _ *Client) {
			server.send(t, protocol.AttachmentEnvelope{Attachment: &protocol.SessionTarget{ServerId: "00000000-0000-4000-8000-000000000002", SessionId: "s", AttachmentId: "a"}})
		}, "Attachment update belongs to another server"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := newMemoryByteServer("")
			client := mustConnectClient(t, server)
			recorder := &stateRecorder{}
			if _, err := client.OnConnectionStateChange(recorder.listener()); err != nil {
				t.Fatal(err)
			}
			pending := begin(t, context.Background(), client, testServerTarget, serviceCall(t, "test", "wait"))
			server.waitForMessages(t, 2)
			test.trigger(t, server, client)
			requireValidation(t, recorder.last().Error, test.message)
			requireValidation(t, await(t, pending).err, test.message)
			if client.Connected() || server.closeCount() != 1 {
				t.Fatalf("connected=%v closes=%d", client.Connected(), server.closeCount())
			}
		})
	}
	t.Run("an attachment of this server is not a violation", func(t *testing.T) {
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		server.send(t, protocol.AttachmentEnvelope{Attachment: &protocol.SessionTarget{ServerId: testServerId, SessionId: "s", AttachmentId: "a"}})
		if !client.Connected() || client.Attachment() == nil {
			t.Fatalf("connected=%v attachment=%v", client.Connected(), client.Attachment())
		}
	})
	t.Run("a malformed catalogue disconnects and rejects with the validation error", func(t *testing.T) {
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		type catalogue struct {
			entries []chord.ServiceCatalogueEntry
			err     error
		}
		result := make(chan catalogue, 1)
		go func() {
			entries, err := client.ServiceCatalogue(context.Background(), testServerTarget)
			result <- catalogue{entries, err}
		}()
		server.waitForMessages(t, 2)
		server.send(t, protocol.ResponseEnvelope{Id: "request-1", Ok: true, HasResult: true, Result: protocolValue(t, `{"not":"a catalogue"}`)})
		outcome := <-result
		if _, ok := errors.AsType[*protocol.ProtocolValidationError](outcome.err); !ok || outcome.entries != nil {
			t.Fatalf("catalogue=%v err=%T %v", outcome.entries, outcome.err, outcome.err)
		}
		if client.Connected() {
			t.Fatal("malformed catalogue left the client connected")
		}
	})
	t.Run("a valid catalogue", func(t *testing.T) {
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		result := make(chan []chord.ServiceCatalogueEntry, 1)
		go func() {
			entries, err := client.ServiceCatalogue(context.Background(), testServerTarget)
			if err != nil {
				t.Error(err)
			}
			result <- entries
		}()
		server.waitForMessages(t, 2)
		assertMatchObject(t, wireValue(t, server.message(t, 1)), `{"type":"request","id":"request-1","call":{"serviceId":"$chord.service","member":"catalogue"}}`)
		server.send(t, protocol.ResponseEnvelope{Id: "request-1", Ok: true, HasResult: true, Result: protocolValue(t, `[]`)})
		if entries := <-result; len(entries) != 0 || !client.Connected() {
			t.Fatalf("entries=%v connected=%v", entries, client.Connected())
		}
	})
}

// Pi: client.ts:243-262 #request preconditions, client.ts:340-352 dispose, :171-176 listener registration after dispose, errors.ts:20-26.
func TestClientRequestPreconditionsAndDisposal(t *testing.T) {
	t.Parallel()
	t.Run("a disconnected client rejects requests", func(t *testing.T) {
		t.Parallel()
		client, _ := scriptedClient(t, newMemoryByteServer("").factory())
		_, err := client.Request(context.Background(), testServerTarget, serviceCall(t, "test", "x"))
		requireDisconnected(t, err, "Client is disconnected")
	})
	t.Run("disposal rejects pending work, clears state and refuses new listeners", func(t *testing.T) {
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		var attachmentChanges []*protocol.SessionTarget
		if _, err := client.OnAttachmentChange(NewAttachmentChangeListener(func(target *protocol.SessionTarget) { attachmentChanges = append(attachmentChanges, target) })); err != nil {
			t.Fatal(err)
		}
		attachClient(t, client, server, "s1")
		pending := begin(t, context.Background(), client, testServerTarget, serviceCall(t, "test", "wait"))
		server.waitForMessages(t, 3)
		if err := client.Dispose(); err != nil {
			t.Fatal(err)
		}
		if err := client.Dispose(); err != nil {
			t.Fatal(err)
		}
		if _, ok := errors.AsType[*ClientDisposedError](await(t, pending).err); !ok {
			t.Fatal("pending request was not rejected with ClientDisposedError")
		}
		if !client.Disposed() || client.Connected() || client.Hello() != nil || client.Attachment() != nil {
			t.Fatalf("disposed=%v connected=%v hello=%v attachment=%v", client.Disposed(), client.Connected(), client.Hello(), client.Attachment())
		}
		if len(attachmentChanges) != 2 || attachmentChanges[1] != nil {
			t.Fatalf("attachment changes=%v, want attach then clear", attachmentChanges)
		}
		if _, err := client.OnConnectionStateChange(NewConnectionStateChangeListener(func(ConnectionStateChange) {})); err == nil || err.Error() != "Client is disposed" {
			t.Fatalf("OnConnectionStateChange after dispose=%v", err)
		}
		if _, err := client.OnAttachmentChange(NewAttachmentChangeListener(func(*protocol.SessionTarget) {})); err == nil || err.Error() != "Client is disposed" {
			t.Fatalf("OnAttachmentChange after dispose=%v", err)
		}
	})
	t.Run("pending requests fail with the connection's disconnect reason", func(t *testing.T) {
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		pending := begin(t, context.Background(), client, testServerTarget, serviceCall(t, "test", "wait"))
		server.waitForMessages(t, 2)
		client.Disconnect("operator request")
		requireDisconnected(t, await(t, pending).err, "operator request")
	})
	t.Run("request ids and the target are sent as given", func(t *testing.T) {
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		session := protocol.SessionTarget{ServerId: testServerId, SessionId: "s", AttachmentId: "a"}
		first := begin(t, context.Background(), client, session, serviceCall(t, "svc", "one"))
		second := begin(t, context.Background(), client, testServerTarget, serviceCall(t, "svc", "two"))
		server.waitForMessages(t, 3)
		assertMatchObject(t, wireValue(t, server.message(t, 1)), `{"type":"request","id":"request-1","target":{"serverId":"`+testServerId+`","sessionId":"s","attachmentId":"a"},"call":{"serviceId":"svc","member":"one"}}`)
		assertMatchObject(t, wireValue(t, server.message(t, 2)), `{"type":"request","id":"request-2","call":{"serviceId":"svc","member":"two"}}`)
		server.send(t, protocol.ResponseEnvelope{Id: "request-1", Ok: true})
		server.send(t, protocol.ResponseEnvelope{Id: "request-2", Ok: true, HasResult: true, Result: nil})
		if outcome := await(t, first); outcome.err != nil || outcome.value != nil {
			t.Fatalf("omitted result = %q, %v; want nothing", outcome.value, outcome.err)
		}
		if outcome := await(t, second); outcome.err != nil || string(outcome.value) != "null" {
			t.Fatalf("null result = %q, %v", outcome.value, outcome.err)
		}
	})
}

// Pi: client.ts:70-75,96-110,171-182,316-343 listener Sets, attachment de-duplication and #reportListenerError.
func TestClientListenerIdentityErrorsAndAttachmentChanges(t *testing.T) {
	t.Parallel()
	server := newMemoryByteServer("")
	var reported []error
	var reportedMu sync.Mutex
	onListenerError := ListenerErrorHandler(func(err error) {
		reportedMu.Lock()
		reported = append(reported, err)
		reportedMu.Unlock()
		panic("diagnostics must not affect the client")
	})
	client, err := Connect(t.Context(), ClientOptions{ServerId: testServerId, TransportFactory: callbackFactory(server.factory()), OnListenerError: onListenerError})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Dispose(); _ = client.WaitClosed(context.Background()) })
	var calls []string
	var mu sync.Mutex
	record := func(name string) { mu.Lock(); calls = append(calls, name); mu.Unlock() }
	same := NewAttachmentChangeListener(func(target *protocol.SessionTarget) {
		if target == nil {
			record("same:nil")
		} else {
			record("same:" + target.SessionId)
		}
	})
	if _, err := client.OnAttachmentChange(same); err != nil {
		t.Fatal(err)
	}
	if _, err := client.OnAttachmentChange(same); err != nil { // the same identity is one registration
		t.Fatal(err)
	}
	if _, err := client.OnAttachmentChange(NewAttachmentChangeListener(func(*protocol.SessionTarget) { panic(errors.New("listener failed")) })); err != nil {
		t.Fatal(err)
	}
	removeLater, err := client.OnAttachmentChange(NewAttachmentChangeListener(func(*protocol.SessionTarget) { record("removed") }))
	if err != nil {
		t.Fatal(err)
	}
	removeLater()
	removeLater()
	target := &protocol.SessionTarget{ServerId: testServerId, SessionId: "s1", AttachmentId: "a1"}
	server.send(t, protocol.AttachmentEnvelope{Attachment: target})
	server.send(t, protocol.AttachmentEnvelope{Attachment: &protocol.SessionTarget{ServerId: testServerId, SessionId: "s1", AttachmentId: "a1"}}) // equal value: no change
	server.send(t, protocol.AttachmentEnvelope{Attachment: &protocol.SessionTarget{ServerId: testServerId, SessionId: "s1", AttachmentId: "a2"}})
	server.send(t, protocol.AttachmentEnvelope{})
	server.send(t, protocol.AttachmentEnvelope{}) // already cleared
	mu.Lock()
	got := append([]string(nil), calls...)
	mu.Unlock()
	want := []string{"same:s1", "same:s1", "same:nil"}
	if len(got) != len(want) {
		t.Fatalf("calls=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("calls=%v, want %v", got, want)
		}
	}
	reportedMu.Lock()
	failures := append([]error(nil), reported...)
	reportedMu.Unlock()
	if len(failures) != 3 || failures[0].Error() != "listener failed" {
		t.Fatalf("reported=%v, want the listener failure for each of the three changes", failures)
	}
	if !client.Connected() {
		t.Fatal("a failing listener or diagnostics handler changed the connection")
	}
	// A disconnect clears the attachment and reports a clear to its listeners.
	server.send(t, protocol.AttachmentEnvelope{Attachment: target})
	client.Disconnect("Client disconnected")
	if client.Attachment() != nil {
		t.Fatalf("attachment after disconnect=%v", client.Attachment())
	}
	mu.Lock()
	defer mu.Unlock()
	if calls[len(calls)-1] != "same:nil" || calls[len(calls)-2] != "same:s1" {
		t.Fatalf("calls=%v", calls)
	}
}

// Pi: client.ts:120-156 subscribeService, :148-154 dispose unsubscribes only a current target, :331-339 #targetIsCurrent, :346-350 transport adapter.
func TestClientSubscriptionDisposalAndServiceTransportTarget(t *testing.T) {
	t.Parallel()
	subscribe := func(t *testing.T, server *memoryByteServer, client *Client, target protocol.RpcTarget) *ServiceSubscription {
		t.Helper()
		result := make(chan *ServiceSubscription, 1)
		expected := len(server.snapshot()) + 1
		go func() {
			subscription, err := client.SubscribeService(context.Background(), target, "pi.models", chord.ServiceSingleton, func(chord.ServiceProviderUpdate) error { return nil })
			if err != nil {
				t.Error(err)
			}
			result <- subscription
		}()
		server.waitForMessages(t, expected)
		request := server.message(t, expected-1).(protocol.RequestEnvelope)
		server.send(t, protocol.ResponseEnvelope{Id: request.Id, Ok: true, HasResult: true, Result: protocolValue(t, `{"serviceId":"pi.models","mode":"singleton","instances":[{"members":[{"name":"state","kind":"state","sequence":0,"ops":[["r",{"revision":0}]]}]}]}`)})
		return <-result
	}
	t.Run("server target unsubscribes while its hello is current", func(t *testing.T) {
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		subscription := subscribe(t, server, client, testServerTarget)
		done := make(chan error, 1)
		go func() { done <- subscription.Dispose() }()
		server.waitForMessages(t, 3)
		assertMatchObject(t, wireValue(t, server.message(t, 2)), `{"call":{"serviceId":"$chord.service","member":"unsubscribe","args":["service-1"]}}`)
		server.send(t, protocol.ResponseEnvelope{Id: "request-2", Ok: true})
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if err := subscription.Dispose(); err != nil || len(server.snapshot()) != 3 {
			t.Fatalf("second dispose sent another request: %v, %d messages", err, len(server.snapshot()))
		}
	})
	t.Run("a session target no longer attached is not unsubscribed", func(t *testing.T) {
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		attachClient(t, client, server, "s1")
		session := *client.Attachment()
		subscription := subscribe(t, server, client, session)
		server.send(t, protocol.AttachmentEnvelope{Attachment: &protocol.SessionTarget{ServerId: testServerId, SessionId: "s1", AttachmentId: "other"}})
		before := len(server.snapshot())
		if err := subscription.Dispose(); err != nil {
			t.Fatal(err)
		}
		if after := len(server.snapshot()); after != before {
			t.Fatalf("disposing a stale-target subscription sent %d requests", after-before)
		}
	})
	t.Run("a server target of another server is not current", func(t *testing.T) {
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		subscription := subscribe(t, server, client, testServerTarget)
		subscription.Target = protocol.ServerTarget{ServerId: "00000000-0000-4000-8000-000000000002"}
		before := len(server.snapshot())
		if err := subscription.Dispose(); err != nil || len(server.snapshot()) != before {
			t.Fatalf("dispose=%v sent=%d", err, len(server.snapshot())-before)
		}
	})
	t.Run("a disconnected client does not unsubscribe", func(t *testing.T) {
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		subscription := subscribe(t, server, client, testServerTarget)
		client.Disconnect("Client disconnected")
		before := len(server.snapshot())
		if err := subscription.Dispose(); err != nil || len(server.snapshot()) != before {
			t.Fatalf("dispose=%v sent=%d", err, len(server.snapshot())-before)
		}
	})
	t.Run("the service transport has no target", func(t *testing.T) {
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		transport := CreateClientServiceTransport(client, func() protocol.RpcTarget { return nil })
		if _, err := transport.Invoke(context.Background(), serviceCall(t, "svc", "m")); err == nil || err.Error() != "Remote service target is unavailable" {
			t.Fatalf("invoke error=%v", err)
		}
		if _, err := transport.Subscribe(context.Background(), "svc", chord.ServiceSingleton, func(context.Context, chord.ServiceProviderUpdate) {}); err == nil || err.Error() != "Remote service target is unavailable" {
			t.Fatalf("subscribe error=%v", err)
		}
		if got := len(server.snapshot()); got != 1 {
			t.Fatalf("no request should be sent without a target, server has %d messages", got)
		}
	})
}

// Pi: client.ts:243-246 #request returns a rejected Promise before registering anything when the client is disposed or disconnected; the Go admission boundary reports the same errors without a pending entry.
func TestClientBeginInvokeRejectsBeforeRegisteringWhenUnavailable(t *testing.T) {
	t.Parallel()
	client, _ := scriptedClient(t, newMemoryByteServer("").factory())
	if operation, err := client.BeginInvoke(context.Background(), testServerTarget, serviceCall(t, "svc", "m")); operation != nil {
		t.Fatalf("operation=%v for a disconnected client", operation)
	} else {
		requireDisconnected(t, err, "Client is disconnected")
	}
	if len(client.pending) != 0 {
		t.Fatalf("pending=%d", len(client.pending))
	}
	if err := client.Dispose(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.BeginInvoke(context.Background(), testServerTarget, serviceCall(t, "svc", "m")); err == nil || err.Error() != "Client is disposed" {
		t.Fatalf("BeginInvoke after dispose=%v", err)
	}
}

// Pi: client.ts:121-156 and :266-286: updates received after the snapshot are decoded in order and delivered only after start(); start is idempotent.
// client.ts:178 and :212-215: the subscription's id is `service-<n>` and its snapshot is the decoded subscribe result.
func TestClientSubscriptionHoldsHydratedUpdatesUntilStart(t *testing.T) {
	t.Parallel()
	server := newMemoryByteServer("")
	client := mustConnectClient(t, server)
	var mu sync.Mutex
	var sequences []float64
	delivered := make(chan struct{}, 8)
	opened := make(chan *ServiceSubscription, 1)
	go func() {
		subscription, err := client.SubscribeService(context.Background(), testServerTarget, "pi.models", chord.ServiceSingleton, func(update chord.ServiceProviderUpdate) error {
			mu.Lock()
			sequences = append(sequences, float64(update.Sequence))
			mu.Unlock()
			delivered <- struct{}{}
			return nil
		})
		if err != nil {
			t.Error(err)
		}
		opened <- subscription
	}()
	server.waitForMessages(t, 2)
	server.send(t, protocol.ResponseEnvelope{Id: "request-1", Ok: true, HasResult: true, Result: protocolValue(t, `{"serviceId":"pi.models","mode":"singleton","instances":[{"members":[{"name":"state","kind":"state","sequence":0,"ops":[["r",{"revision":0}]]}]}]}`)})
	subscription := <-opened
	if subscription.Id != "service-1" {
		t.Fatalf("subscription id %q, want service-1", subscription.Id)
	}
	if snap := subscription.Snapshot; snap.ServiceId != "pi.models" || snap.Mode != chord.ServiceSingleton || len(snap.Instances) != 1 {
		t.Fatalf("snapshot %+v, want the decoded pi.models singleton with one instance", snap)
	}
	for sequence := 1; sequence <= 2; sequence++ {
		server.send(t, protocol.ServiceEventEnvelope{SubscriptionId: "service-1", Update: protocolValue(t, `{"type":"state","member":"state","sequence":`+string(rune('0'+sequence))+`,"ops":[["s",["revision"],`+string(rune('0'+sequence))+`]]}`)})
	}
	// server.send dispatches synchronously, so both updates are decoded by now: held in the queue, none admitted for delivery.
	active := subscription.active
	active.mu.Lock()
	queued, admitted := len(active.queued), len(active.delivery)
	active.mu.Unlock()
	mu.Lock()
	admitted += len(sequences)
	mu.Unlock()
	if queued != 2 || admitted != 0 {
		t.Fatalf("before Start: %d queued, %d admitted; want 2 queued, none admitted", queued, admitted)
	}
	subscription.Start()
	subscription.Start()
	<-delivered
	<-delivered
	// Dispose joins every admitted delivery, so a duplicate from the second Start would be recorded before it returns.
	disposed := make(chan error, 1)
	go func() { disposed <- subscription.Dispose() }()
	server.waitForMessages(t, 3)
	server.send(t, protocol.ResponseEnvelope{Id: "request-2", Ok: true})
	if err := <-disposed; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := append([]float64(nil), sequences...)
	mu.Unlock()
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("delivered sequences=%v, want [1 2] once each", got)
	}
}

// Pi: connection.ts:101-103 #handleData stops at the first message that disconnects: later messages of the same chunk are not handled.
func TestClientIgnoresMessagesAfterAFailureInTheSameChunk(t *testing.T) {
	t.Parallel()
	server := newMemoryByteServer("")
	client := mustConnectClient(t, server)
	var changes []*protocol.SessionTarget
	if _, err := client.OnAttachmentChange(NewAttachmentChangeListener(func(target *protocol.SessionTarget) { changes = append(changes, target) })); err != nil {
		t.Fatal(err)
	}
	chunk := append(serverFrame(t, protocol.ServerHello{Version: protocol.ProtocolVersion, ServerId: testServerId}),
		serverFrame(t, protocol.AttachmentEnvelope{Attachment: &protocol.SessionTarget{ServerId: testServerId, SessionId: "s", AttachmentId: "a"}})...)
	server.sendRaw(t, chunk)
	if client.Connected() || client.Attachment() != nil || len(changes) != 0 {
		t.Fatalf("connected=%v attachment=%v changes=%v", client.Connected(), client.Attachment(), changes)
	}
}

// A factory that completes with neither a transport nor an error fails the attempt rather than dereferencing nothing.
func TestClientRejectsAFactoryThatReturnsNoTransport(t *testing.T) {
	t.Parallel()
	client, _ := scriptedClient(t, func(_ context.Context, _ ByteTransportHandlers, complete func(callbackByteTransport, error)) {
		complete(nil, nil)
	})
	_, err := client.Connect(t.Context())
	requireDisconnected(t, err, "Byte transport factory returned no transport")
}

// Pi: client.ts:96-100 onConnectionStateChange keeps listeners in a Set, so registering one listener twice delivers each change once; an unsubscribed listener is not called.
func TestClientConnectionStateListenerIdentity(t *testing.T) {
	t.Parallel()
	server := newMemoryByteServer("")
	client, err := NewClient(ClientOptions{ServerId: testServerId, TransportFactory: callbackFactory(server.factory())})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Dispose(); _ = client.WaitClosed(context.Background()) })
	recorder := &stateRecorder{}
	listener := recorder.listener()
	if _, err := client.OnConnectionStateChange(listener); err != nil {
		t.Fatal(err)
	}
	remove, err := client.OnConnectionStateChange(listener)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := recorder.states(); len(got) != 2 || got[0] != Connecting || got[1] != Connected {
		t.Fatalf("states=%v, want each change once", got)
	}
	remove()
	client.Disconnect("Client disconnected")
	if got := recorder.states(); len(got) != 2 {
		t.Fatalf("removed listener saw %v", got)
	}
}
