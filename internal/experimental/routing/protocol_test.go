package routing_test

import (
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

// wireClient is upstream ProtocolTestClient over an in-memory WireChannel (packages/server/test/protocol.test.ts:9-44).
type wireClient struct {
	t        *testing.T
	handler  routing.ByteConnectionHandler
	decoder  *protocol.ServerMessageDecoder
	mu       sync.Mutex
	messages []protocol.ServerMessage
	closed   bool
	changed  chan struct{}
	// attachment is the client's current Session attachment, as ProtocolTestClient tracks it from attachment envelopes.
	attachment *protocol.SessionTarget
	requests   int
}

type memoryConnection struct{ client *wireClient }

func (connection memoryConnection) Closed() bool {
	connection.client.mu.Lock()
	defer connection.client.mu.Unlock()
	return connection.client.closed
}

func (connection memoryConnection) Send(chunk []byte) error {
	connection.client.receive(chunk)
	return nil
}

func (connection memoryConnection) Close(finalChunk []byte) error {
	if finalChunk != nil {
		connection.client.receive(finalChunk)
	}
	connection.client.markClosed()
	return nil
}

func connectWire(t *testing.T) *wireClient {
	t.Helper()
	server := newTestServer(t, routing.ServerOptions{Listeners: []routing.ServerListener{}})
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	})
	decoder, err := protocol.NewServerMessageDecoder(protocol.FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client := &wireClient{t: t, decoder: decoder, changed: make(chan struct{})}
	client.handler = server.Accept(memoryConnection{client: client})
	return client
}

func (client *wireClient) receive(chunk []byte) {
	messages, err := client.decoder.Push(chunk)
	if err != nil {
		client.t.Errorf("server sent an undecodable frame: %v", err)
		return
	}
	client.mu.Lock()
	for _, message := range messages {
		if envelope, ok := message.(protocol.AttachmentEnvelope); ok {
			client.attachment = envelope.Attachment
		}
	}
	client.messages = append(client.messages, messages...)
	client.wakeLocked()
	client.mu.Unlock()
}

func (client *wireClient) markClosed() {
	client.mu.Lock()
	client.closed = true
	client.wakeLocked()
	client.mu.Unlock()
}

func (client *wireClient) wakeLocked() {
	close(client.changed)
	client.changed = make(chan struct{})
}

func (client *wireClient) sendMessage(message protocol.ClientMessage) {
	client.t.Helper()
	client.handler.OnData(encodeClient(client.t, message))
}

func encodeClient(t *testing.T, message protocol.ClientMessage) []byte {
	t.Helper()
	frame, err := protocol.EncodeClientMessage(message, protocol.FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

// next returns the first received message the predicate accepts, waiting for the wire when needed.
func (client *wireClient) next(match func(protocol.ServerMessage) bool) protocol.ServerMessage {
	client.t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		client.mu.Lock()
		for _, message := range client.messages {
			if match(message) {
				client.mu.Unlock()
				return message
			}
		}
		closed, changed := client.closed, client.changed
		client.mu.Unlock()
		if closed {
			client.t.Fatal("wire client is closed")
		}
		select {
		case <-changed:
		case <-deadline:
			client.t.Fatal("no matching server message")
		}
	}
}

func (client *wireClient) waitForClose() {
	client.t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		client.mu.Lock()
		closed, changed := client.closed, client.changed
		client.mu.Unlock()
		if closed {
			return
		}
		select {
		case <-changed:
		case <-deadline:
			client.t.Fatal("wire connection never closed")
		}
	}
}

func (client *wireClient) hello(version float64) protocol.ServerMessage {
	client.t.Helper()
	client.sendMessage(protocol.ClientHello{Version: version})
	return client.next(func(message protocol.ServerMessage) bool {
		switch message.(type) {
		case protocol.ServerHello, protocol.ServerHelloError:
			return true
		}
		return false
	})
}

func isHelloError(message protocol.ServerMessage) bool {
	_, ok := message.(protocol.ServerHelloError)
	return ok
}

func isHello(message protocol.ServerMessage) bool {
	_, ok := message.(protocol.ServerHello)
	return ok
}

func isResponse(message protocol.ServerMessage) bool {
	_, ok := message.(protocol.ResponseEnvelope)
	return ok
}

func expectHelloError(t *testing.T, message protocol.ServerMessage, code string) protocol.ServerHelloError {
	t.Helper()
	failure, ok := message.(protocol.ServerHelloError)
	if !ok || failure.Error.Code != code {
		t.Fatalf("message = %#v, want hello_error %s", message, code)
	}
	return failure
}

func directoryListRequest() protocol.RequestEnvelope {
	return protocol.RequestEnvelope{
		Id:     "request-1",
		Target: protocol.ServerTarget{ServerId: testServerID},
		Call:   protocol.Object{{Key: "serviceId", Value: "pi.session-directory"}, {Key: "member", Value: "list"}, {Key: "args", Value: []any{}}},
	}
}

func expectInternalErrorResponse(t *testing.T, message protocol.ServerMessage) {
	t.Helper()
	response, ok := message.(protocol.ResponseEnvelope)
	if !ok || response.Ok || response.Error == nil || response.Error.Code != "internal_error" {
		t.Fatalf("message = %#v, want failed internal_error response", message)
	}
}

// upstream: packages/server/test/protocol.test.ts:51 "requires hello as the first message"
func TestServerProtocolRequiresHelloAsTheFirstMessage(t *testing.T) {
	client := connectWire(t)
	client.sendMessage(directoryListRequest())
	expectHelloError(t, client.next(isHelloError), "invalid_request")
	client.waitForClose()
}

// upstream: packages/server/test/protocol.test.ts:66 "rejects unsupported protocol versions"
func TestServerProtocolRejectsUnsupportedProtocolVersions(t *testing.T) {
	client := connectWire(t)
	expectHelloError(t, client.hello(protocol.ProtocolVersion+1), "version")
	client.waitForClose()
}

// upstream: packages/server/test/protocol.test.ts:75 "accepts fragmented hello and request frames"
func TestServerProtocolAcceptsFragmentedHelloAndRequestFrames(t *testing.T) {
	client := connectWire(t)
	hello := encodeClient(t, protocol.ClientHello{Version: protocol.ProtocolVersion})
	client.handler.OnData(hello[:len(hello)/2])
	client.handler.OnData(hello[len(hello)/2:])
	reply, ok := client.next(isHello).(protocol.ServerHello)
	if !ok || reply.ServerId != testServerID {
		t.Fatalf("hello reply = %#v", reply)
	}
	request := encodeClient(t, directoryListRequest())
	client.handler.OnData(request[:len(request)/2])
	client.handler.OnData(request[len(request)/2:])
	expectInternalErrorResponse(t, client.next(isResponse))
}

// upstream: packages/server/test/protocol.test.ts:97 "rejects hostile framed input" (malformed CBOR, schema-invalid CBOR, oversized frame)
func TestServerProtocolRejectsHostileFramedInput(t *testing.T) {
	frame := func(payload []byte) []byte {
		framed, err := protocol.EncodeFrame(payload)
		if err != nil {
			t.Fatal(err)
		}
		return framed
	}
	schemaInvalid, err := protocol.EncodeCbor(protocol.Object{{Key: "type", Value: "hello"}, {Key: "version", Value: float64(1)}, {Key: "extra", Value: true}}, protocol.CborOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		label string
		bytes []byte
	}{
		{"malformed CBOR", frame([]byte{0xff})},
		{"schema-invalid CBOR", frame(schemaInvalid)},
		{"oversized frame", []byte{1, 0, 0, 1}},
	} {
		t.Run(input.label, func(t *testing.T) {
			client := connectWire(t)
			client.handler.OnData(input.bytes)
			expectHelloError(t, client.next(isHelloError), "invalid_request")
			client.waitForClose()
		})
	}
}

// upstream: packages/server/test/protocol.test.ts:111 "rejects a second hello after completing the handshake"
func TestServerProtocolRejectsASecondHelloAfterCompletingTheHandshake(t *testing.T) {
	client := connectWire(t)
	client.hello(protocol.ProtocolVersion)
	client.sendMessage(protocol.ClientHello{Version: protocol.ProtocolVersion})
	failure := expectHelloError(t, client.next(isHelloError), "invalid_request")
	if !regexp.MustCompile("first message").MatchString(failure.Error.Message) {
		t.Fatalf("second hello message = %q, want a first-message violation", failure.Error.Message)
	}
	client.waitForClose()
}

// upstream: packages/server/test/protocol.test.ts:122 "processes a hello and request coalesced in one byte chunk"
func TestServerProtocolProcessesAHelloAndRequestCoalescedInOneByteChunk(t *testing.T) {
	client := connectWire(t)
	wire := append(encodeClient(t, protocol.ClientHello{Version: protocol.ProtocolVersion}), encodeClient(t, directoryListRequest())...)
	client.handler.OnData(wire)
	client.next(isHello)
	response, ok := client.next(isResponse).(protocol.ResponseEnvelope)
	if !ok || response.Id != "request-1" {
		t.Fatalf("response = %#v, want request-1", response)
	}
	expectInternalErrorResponse(t, response)
}

// upstream: packages/server/test/protocol.test.ts:145 "reports a truncated final frame when the peer closes"
func TestServerProtocolReportsATruncatedFinalFrameWhenThePeerCloses(t *testing.T) {
	var mu sync.Mutex
	var reported []error
	failed := make(chan struct{}, 1)
	server := newTestServer(t, routing.ServerOptions{Listeners: []routing.ServerListener{}, OnError: func(err error) {
		mu.Lock()
		reported = append(reported, err)
		mu.Unlock()
		select {
		case failed <- struct{}{}:
		default:
		}
	}})
	t.Cleanup(func() { _ = server.Close() })
	connection := &recordingConnection{}
	handler := server.Accept(connection)
	handler.OnData([]byte{0, 0, 0, 2, 1})
	handler.OnClose()
	select {
	case <-failed:
	case <-time.After(30 * time.Second):
		t.Fatal("the truncated final frame was never reported")
	}
	if connection.Closed() {
		t.Fatal("the server closed a connection the peer had already closed")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reported) != 1 || !regexp.MustCompile("(?i)truncated").MatchString(reported[0].Error()) {
		t.Fatalf("reported errors = %v, want one truncated-frame error", reported)
	}
}

type recordingConnection struct {
	mu     sync.Mutex
	closed bool
}

func (connection *recordingConnection) Closed() bool {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	return connection.closed
}
func (*recordingConnection) Send([]byte) error { return nil }
func (connection *recordingConnection) Close([]byte) error {
	connection.mu.Lock()
	connection.closed = true
	connection.mu.Unlock()
	return nil
}
