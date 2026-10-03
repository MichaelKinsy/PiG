package client

// Ports packages/client/test/support.ts.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

const testServerId = "00000000-0000-4000-8000-000000000001"

var testServerTarget = protocol.ServerTarget{ServerId: testServerId}

// memoryByteServer is upstream's MemoryByteServer: it decodes client frames, answers hello, and delivers server frames through the current connection's handlers.
type memoryByteServer struct {
	serverId         string
	mu               sync.Mutex
	messages         []protocol.ClientMessage
	clientCloseCount int
	handlers         *ByteTransportHandlers
	decoder          *protocol.ClientMessageDecoder
	changed          chan struct{}
}

func newMemoryByteServer(serverId string) *memoryByteServer {
	if serverId == "" {
		serverId = testServerId
	}
	return &memoryByteServer{serverId: serverId, changed: make(chan struct{})}
}

// factory connects every transport attempt to this server, as `(handlers) => server.connect(handlers)`.
func (server *memoryByteServer) factory() ByteTransportFactory {
	return func(_ context.Context, handlers ByteTransportHandlers, complete func(ByteTransport, error)) {
		complete(server.connect(handlers), nil)
	}
}

func (server *memoryByteServer) connect(handlers ByteTransportHandlers) ByteTransport {
	decoder, err := protocol.NewClientMessageDecoder(protocol.FrameDecoderOptions{})
	if err != nil {
		panic(err)
	}
	server.mu.Lock()
	server.handlers = &handlers
	server.decoder = decoder
	server.mu.Unlock()
	return &memoryTransport{server: server, handlers: &handlers}
}

type memoryTransport struct {
	server   *memoryByteServer
	handlers *ByteTransportHandlers
	closed   bool
}

func (transport *memoryTransport) Send(chunk []byte, complete func(error)) {
	server := transport.server
	server.mu.Lock()
	messages, err := server.decoder.Push(chunk)
	if err != nil {
		server.mu.Unlock()
		complete(err)
		return
	}
	server.mu.Unlock()
	for _, message := range messages {
		server.mu.Lock()
		server.messages = append(server.messages, message)
		close(server.changed)
		server.changed = make(chan struct{})
		server.mu.Unlock()
		if _, ok := message.(protocol.ClientHello); ok {
			if err := server.trySend(protocol.ServerHello{Version: protocol.ProtocolVersion, ServerId: server.serverId}); err != nil {
				complete(err)
				return
			}
		}
	}
	complete(nil)
}

func (transport *memoryTransport) Close() {
	server := transport.server
	server.mu.Lock()
	defer server.mu.Unlock()
	if transport.closed {
		return
	}
	transport.closed = true
	server.clientCloseCount++
	if server.handlers == transport.handlers {
		server.handlers = nil
	}
}

// waitForMessages blocks until the server has decoded count client messages.
func (server *memoryByteServer) waitForMessages(t *testing.T, count int) {
	t.Helper()
	for {
		server.mu.Lock()
		if len(server.messages) >= count {
			server.mu.Unlock()
			return
		}
		changed := server.changed
		server.mu.Unlock()
		select {
		case <-changed:
		case <-t.Context().Done():
			t.Fatalf("server received %d messages; want %d", len(server.snapshot()), count)
		}
	}
}

func (server *memoryByteServer) snapshot() []protocol.ClientMessage {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([]protocol.ClientMessage(nil), server.messages...)
}

func (server *memoryByteServer) message(t *testing.T, index int) protocol.ClientMessage {
	t.Helper()
	messages := server.snapshot()
	if index >= len(messages) {
		t.Fatalf("server has %d messages; want index %d", len(messages), index)
	}
	return messages[index]
}

func (server *memoryByteServer) closeCount() int {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.clientCloseCount
}

func (server *memoryByteServer) currentHandlers() *ByteTransportHandlers {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.handlers
}

func (server *memoryByteServer) trySend(message protocol.ServerMessage) error {
	handlers := server.currentHandlers()
	if handlers == nil {
		return errors.New("No client connection")
	}
	frame, err := protocol.EncodeServerMessage(message, protocol.FrameDecoderOptions{})
	if err != nil {
		return err
	}
	handlers.OnData(frame)
	return nil
}

func (server *memoryByteServer) send(t *testing.T, message protocol.ServerMessage) {
	t.Helper()
	if err := server.trySend(message); err != nil {
		t.Fatal(err)
	}
}

func (server *memoryByteServer) sendRaw(t *testing.T, chunk []byte) {
	t.Helper()
	handlers := server.currentHandlers()
	if handlers == nil {
		t.Fatal("No client connection")
	}
	handlers.OnData(chunk)
}

func (server *memoryByteServer) disconnect() {
	server.mu.Lock()
	handlers := server.handlers
	server.handlers = nil
	server.mu.Unlock()
	if handlers != nil {
		handlers.OnClose()
	}
}

func (server *memoryByteServer) error(err error) {
	server.mu.Lock()
	handlers := server.handlers
	server.handlers = nil
	server.mu.Unlock()
	if handlers != nil {
		handlers.OnError(err)
	}
}

// serviceCall builds an untyped Chord call from JSON-compatible arguments.
func serviceCall(t *testing.T, serviceId, member string, args ...any) chord.ServiceCall {
	t.Helper()
	raw := make([]json.RawMessage, 0, len(args))
	for _, arg := range args {
		data, err := json.Marshal(arg)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, data)
	}
	return chord.ServiceCall{ServiceId: serviceId, Member: member, Args: raw}
}

// wireValue is a client message as the JSON value upstream's assertions read.
func wireValue(t *testing.T, message protocol.ClientMessage) any {
	t.Helper()
	frame, err := protocol.EncodeClientMessage(message, protocol.FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := protocol.DecodeCbor(frame[4:], protocol.CborOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return jsonValue(t, decoded)
}

func jsonValue(t *testing.T, value any) any {
	t.Helper()
	raw, err := protocol.ToJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return decodeJSON(t, string(raw))
}

func decodeJSON(t *testing.T, text string) any {
	t.Helper()
	var out any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func protocolValue(t *testing.T, text string) any {
	t.Helper()
	value, err := protocol.FromJSON(json.RawMessage(text))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// matchObject is vitest's toMatchObject: objects match on the expected keys, arrays element by element at equal length, other values by equality.
func matchObject(got, want any) bool {
	switch want := want.(type) {
	case map[string]any:
		object, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range want {
			actual, present := object[key]
			if !present || !matchObject(actual, value) {
				return false
			}
		}
		return true
	case []any:
		array, ok := got.([]any)
		if !ok || len(array) != len(want) {
			return false
		}
		for index := range want {
			if !matchObject(array[index], want[index]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(got, want)
	}
}

func assertMatchObject(t *testing.T, got any, want string) {
	t.Helper()
	if !matchObject(got, decodeJSON(t, want)) {
		data, _ := json.Marshal(got)
		t.Fatalf("value %s does not match %s", data, want)
	}
}

type invocationResult struct {
	value json.RawMessage
	err   error
}

// begin admits one request synchronously, as upstream's request() sends before returning its Promise, and resolves on the returned channel.
func begin(t *testing.T, ctx context.Context, client *Client, target protocol.RpcTarget, call chord.ServiceCall) <-chan invocationResult {
	t.Helper()
	result := make(chan invocationResult, 1)
	operation, err := client.BeginInvoke(ctx, target, call)
	if err != nil {
		result <- invocationResult{err: err}
		return result
	}
	go func() {
		value, err := operation.Wait(context.Background())
		result <- invocationResult{value, err}
	}()
	return result
}

func await(t *testing.T, result <-chan invocationResult) invocationResult {
	t.Helper()
	select {
	case outcome := <-result:
		return outcome
	case <-t.Context().Done():
		t.Fatal("request did not settle")
		return invocationResult{}
	}
}

func connectClient(t *testing.T, server *memoryByteServer, expectedServerId string) (*Client, error) {
	t.Helper()
	if expectedServerId == "" {
		expectedServerId = testServerId
	}
	return Connect(t.Context(), ClientOptions{ServerId: expectedServerId, TransportFactory: server.factory()})
}

func mustConnectClient(t *testing.T, server *memoryByteServer) *Client {
	t.Helper()
	client, err := connectClient(t, server, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Dispose(); err != nil {
			t.Error(err)
		}
		if err := client.WaitClosed(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return client
}

// attachClient answers an attach request with an out-of-band attachment update before the response, as upstream's helper does.
func attachClient(t *testing.T, client *Client, server *memoryByteServer, sessionId string) {
	t.Helper()
	expected := len(server.snapshot()) + 1
	attaching := begin(t, context.Background(), client, testServerTarget, serviceCall(t, "pi.session-management", "attach", sessionId))
	server.waitForMessages(t, expected)
	request, ok := server.message(t, expected-1).(protocol.RequestEnvelope)
	if !ok {
		t.Fatal("Missing attach request")
	}
	server.send(t, protocol.AttachmentEnvelope{Attachment: &protocol.SessionTarget{ServerId: testServerId, SessionId: sessionId, AttachmentId: "attachment-" + sessionId}})
	server.send(t, protocol.ResponseEnvelope{Id: request.Id, Ok: true, HasResult: true, Result: nil})
	if outcome := await(t, attaching); outcome.err != nil {
		t.Fatal(outcome.err)
	}
}
