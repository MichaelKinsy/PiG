package client

// Ports packages/client/test/client.test.ts.

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

func TestClientUpstream(t *testing.T) {
	t.Parallel()
	t.Run("requires a canonical UUIDv4 server identity", func(t *testing.T) {
		// upstream: packages/client/test/client.test.ts:44
		t.Parallel()
		_, err := NewClient(ClientOptions{ServerId: "invalid-server", TransportFactory: callbackFactory(func(_ context.Context, _ ByteTransportHandlers, complete func(callbackByteTransport, error)) {
			complete(nil, errors.New("unreachable"))
		})})
		if err == nil || !strings.Contains(err.Error(), "serverId") {
			t.Fatalf("NewClient error = %v; want a serverId error", err)
		}
	})
	t.Run("Client service operations › connects only to the expected logical server", func(t *testing.T) {
		// upstream: packages/client/test/client.test.ts:51
		t.Parallel()
		client := mustConnectClient(t, newMemoryByteServer(""))
		if hello := client.Hello(); hello == nil || hello.ServerId != testServerId {
			t.Fatalf("hello = %+v; want serverId %s", hello, testServerId)
		}
		if err := client.Dispose(); err != nil {
			t.Fatal(err)
		}

		wrong := newMemoryByteServer("00000000-0000-4000-8000-000000000002")
		_, err := connectClient(t, wrong, "")
		if _, ok := errors.AsType[*protocol.ProtocolValidationError](err); !ok {
			t.Fatalf("connect to the wrong server = %v; want ProtocolValidationError", err)
		}
	})
	t.Run("Client service operations › updates attachment state from out-of-band server routing", func(t *testing.T) {
		// upstream: packages/client/test/client.test.ts:61
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		var changes []any
		if _, err := client.OnAttachmentChange(NewAttachmentChangeListener(func(attachment *protocol.SessionTarget) {
			if attachment == nil {
				changes = append(changes, nil)
				return
			}
			changes = append(changes, attachment.SessionId)
		})); err != nil {
			t.Fatal(err)
		}

		attachClient(t, client, server, "session-1")
		if attachment := client.Attachment(); attachment == nil || attachment.SessionId != "session-1" || attachment.AttachmentId != "attachment-session-1" {
			t.Fatalf("attachment = %+v", attachment)
		}
		assertMatchObject(t, wireValue(t, server.message(t, 1)), `{"type":"request","target":{"serverId":"`+testServerId+`"},"call":{"serviceId":"pi.session-management","member":"attach","args":["session-1"]}}`)

		server.send(t, protocol.AttachmentEnvelope{Attachment: nil})
		if attachment := client.Attachment(); attachment != nil {
			t.Fatalf("attachment = %+v; want undefined", attachment)
		}
		if !slices.Equal(changes, []any{"session-1", nil}) {
			t.Fatalf("changes = %v", changes)
		}
	})
	t.Run("Client service operations › buffers service updates until the subscription snapshot arrives", func(t *testing.T) {
		// upstream: packages/client/test/client.test.ts:81
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		attachClient(t, client, server, "session-1")
		target := *client.Attachment()
		transport := CreateClientServiceTransport(client, func() protocol.RpcTarget {
			if attachment := client.Attachment(); attachment != nil {
				return *attachment
			}
			return nil
		})
		var mu sync.Mutex
		var updates []chord.ServiceProviderUpdate
		delivered := make(chan struct{}, 8)
		type opened struct {
			subscription chord.ServiceSubscription
			err          error
		}
		opening := make(chan opened, 1)
		go func() {
			subscription, err := transport.Subscribe(context.Background(), "pi.models", chord.ServiceSingleton, func(_ context.Context, update chord.ServiceProviderUpdate) {
				mu.Lock()
				updates = append(updates, update)
				mu.Unlock()
				delivered <- struct{}{}
			})
			opening <- opened{subscription, err}
		}()
		count := func() int {
			mu.Lock()
			defer mu.Unlock()
			return len(updates)
		}
		server.waitForMessages(t, 3)
		assertMatchObject(t, wireValue(t, server.message(t, 2)), `{"type":"request","target":{"serverId":"`+target.ServerId+`","sessionId":"`+target.SessionId+`","attachmentId":"`+target.AttachmentId+`"},"call":{"serviceId":"$chord.service","member":"subscribe","args":["service-1","pi.models","singleton"]}}`)
		server.send(t, protocol.ServiceEventEnvelope{SubscriptionId: "service-1", Update: protocolValue(t, `{"type":"state","member":"state","sequence":1,"ops":[["s",["revision"],1]]}`)})
		if n := count(); n != 0 {
			t.Fatalf("updates before the snapshot = %d", n)
		}
		server.send(t, protocol.ResponseEnvelope{Id: "request-2", Ok: true, HasResult: true, Result: protocolValue(t, `{"serviceId":"pi.models","mode":"singleton","instances":[{"members":[{"name":"state","kind":"state","sequence":0,"ops":[["r",{"revision":0}]]}]}]}`)})
		var subscription chord.ServiceSubscription
		select {
		case result := <-opening:
			if result.err != nil {
				t.Fatal(result.err)
			}
			subscription = result.subscription
		case <-t.Context().Done():
			t.Fatal("subscribe did not settle")
		}
		if n := count(); n != 0 {
			t.Fatalf("updates before activate = %d", n)
		}
		server.send(t, protocol.ServiceEventEnvelope{SubscriptionId: "closed-subscription", Update: protocolValue(t, `{"type":"state","member":"state","sequence":99,"ops":[["s",99,99]]}`)})
		if !client.Connected() {
			t.Fatal("an update for an unknown subscription disconnected the client")
		}
		members, err := json.Marshal(subscription.Snapshot().Instances[0].Members)
		if err != nil {
			t.Fatal(err)
		}
		if !jsonEqual(decodeJSON(t, string(members)), decodeJSON(t, `[{"name":"state","kind":"state","sequence":0,"ops":[["r",{"revision":0}]]}]`)) {
			t.Fatalf("snapshot members = %s", members)
		}
		if err := subscription.Activate(); err != nil {
			t.Fatal(err)
		}
		waitDelivered := func(want int) {
			t.Helper()
			for count() < want {
				select {
				case <-delivered:
				case <-t.Context().Done():
					t.Fatalf("delivered %d updates; want %d", count(), want)
				}
			}
		}
		waitDelivered(1)
		mu.Lock()
		if updates[0].Type != "state" {
			t.Fatalf("first update type = %q", updates[0].Type)
		}
		mu.Unlock()
		server.send(t, protocol.ServiceEventEnvelope{SubscriptionId: "service-1", Update: protocolValue(t, `{"type":"state","member":"state","sequence":2,"ops":[["s",["revision"],2]]}`)})
		server.send(t, protocol.ServiceEventEnvelope{SubscriptionId: "service-1", Update: protocolValue(t, `{"type":"state","member":"state","sequence":3,"ops":[["#",0,["revision"]],["s",0,3]]}`)})
		waitDelivered(3)
		mu.Lock()
		ops, err := json.Marshal(updates[2].Ops)
		mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if string(ops) != `[["s",["revision"],3]]` {
			t.Fatalf("third update ops = %s", ops)
		}

		disposing := make(chan error, 1)
		go func() { disposing <- subscription.Close(context.Background()) }()
		server.waitForMessages(t, 4)
		assertMatchObject(t, wireValue(t, server.message(t, 3)), `{"call":{"serviceId":"$chord.service","member":"unsubscribe","args":["service-1"]}}`)
		server.send(t, protocol.ResponseEnvelope{Id: "request-3", Ok: true})
		select {
		case err := <-disposing:
			if err != nil {
				t.Fatal(err)
			}
		case <-t.Context().Done():
			t.Fatal("subscription close did not settle")
		}
	})
	t.Run("Client service operations › correlates out-of-order generic service responses", func(t *testing.T) {
		// upstream: packages/client/test/client.test.ts:168
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		first := begin(t, context.Background(), client, testServerTarget, serviceCall(t, "test", "first"))
		second := begin(t, context.Background(), client, testServerTarget, serviceCall(t, "test", "second"))
		server.waitForMessages(t, 3)
		server.send(t, protocol.ResponseEnvelope{Id: "request-2", Ok: true, HasResult: true, Result: "second"})
		server.send(t, protocol.ResponseEnvelope{Id: "request-1", Ok: true, HasResult: true, Result: "first"})
		firstResult, secondResult := await(t, first), await(t, second)
		if firstResult.err != nil || secondResult.err != nil || string(firstResult.value) != `"first"` || string(secondResult.value) != `"second"` {
			t.Fatalf("results = %s %v, %s %v", firstResult.value, firstResult.err, secondResult.value, secondResult.err)
		}
	})
	t.Run("Client service operations › exposes bounded server errors", func(t *testing.T) {
		// upstream: packages/client/test/client.test.ts:180
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		pending := begin(t, context.Background(), client, testServerTarget, serviceCall(t, "test", "missing"))
		server.waitForMessages(t, 2)
		server.send(t, protocol.ResponseEnvelope{Id: "request-1", Ok: false, Error: &protocol.ProtocolError{Code: "session_not_found", Message: "Unknown session"}})
		var serverError *ServerError
		if err := await(t, pending).err; !errors.As(err, &serverError) || serverError.Code != "session_not_found" {
			t.Fatalf("request error = %v; want ServerError session_not_found", err)
		}
	})
	t.Run("Client service operations › does not send a pre-aborted untyped RPC request", func(t *testing.T) {
		// upstream: packages/client/test/client.test.ts:195
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		ctx, abort := context.WithCancelCause(context.Background())
		reason := errors.New("already cancelled")
		abort(reason)

		if err := await(t, begin(t, ctx, client, testServerTarget, serviceCall(t, "test", "noop"))).err; err != reason { //nolint:errorlint // upstream asserts rejects.toBe(reason): the identical abort reason.
			t.Fatalf("request error = %v; want the abort reason", err)
		}
		if messages := server.snapshot(); len(messages) != 1 {
			t.Fatalf("server messages = %d; want only the hello", len(messages))
		}
	})
	t.Run("Client service operations › cancels one untyped RPC request without disconnecting", func(t *testing.T) {
		// upstream: packages/client/test/client.test.ts:209
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		ctx, abort := context.WithCancelCause(context.Background())
		reason := errors.New("stop this request")
		pending := begin(t, ctx, client, testServerTarget, serviceCall(t, "test", "mutate", map[string]any{"value": 42}))
		server.waitForMessages(t, 2)
		assertMatchObject(t, wireValue(t, server.message(t, 1)), `{"type":"request","id":"request-1","target":{"serverId":"`+testServerId+`"},"call":{"serviceId":"test","member":"mutate","args":[{"value":42}]}}`)

		abort(reason)
		if err := await(t, pending).err; err != reason { //nolint:errorlint // upstream asserts rejects.toBe(reason): the identical abort reason.
			t.Fatalf("request error = %v; want the abort reason", err)
		}
		server.waitForMessages(t, 3)
		got := wireValue(t, server.message(t, 2))
		if want := decodeJSON(t, `{"type":"cancel","id":"request-1","target":{"serverId":"`+testServerId+`"}}`); !jsonEqual(got, want) {
			t.Fatalf("cancel message = %v; want %v", got, want)
		}
		server.send(t, protocol.ResponseEnvelope{Id: "request-1", Ok: false, Error: &protocol.ProtocolError{Code: "cancelled", Message: "cancelled"}})
		if !client.Connected() {
			t.Fatal("the late response to a cancelled request disconnected the client")
		}
	})
	t.Run("Client service operations › rejects pending requests after disconnect or disposal", func(t *testing.T) {
		// upstream: packages/client/test/client.test.ts:241
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		pending := begin(t, context.Background(), client, testServerTarget, serviceCall(t, "test", "pending"))
		server.disconnect()
		var disconnected *DisconnectedError
		if err := await(t, pending).err; !errors.As(err, &disconnected) {
			t.Fatalf("pending request error = %v; want DisconnectedError", err)
		}
		if err := client.Dispose(); err != nil {
			t.Fatal(err)
		}
		var disposed *ClientDisposedError
		if _, err := client.Request(context.Background(), testServerTarget, serviceCall(t, "test", "disposed")); !errors.As(err, &disposed) {
			t.Fatalf("request after dispose = %v; want ClientDisposedError", err)
		}
	})
	t.Run("Client connection lifecycle › rejects server data delivered before the client hello is sent", func(t *testing.T) {
		// upstream: packages/client/test/client.test.ts:255
		t.Parallel()
		var mu sync.Mutex
		closeCount, sendCount := 0, 0
		hello, err := protocol.EncodeServerMessage(protocol.ServerHello{Version: protocol.ProtocolVersion, ServerId: testServerId}, protocol.FrameDecoderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		client, err := NewClient(ClientOptions{ServerId: testServerId, TransportFactory: callbackFactory(func(_ context.Context, handlers ByteTransportHandlers, complete func(callbackByteTransport, error)) {
			handlers.OnData(hello)
			complete(&funcTransport{
				send: func(_ []byte, done func(error)) {
					mu.Lock()
					sendCount++
					mu.Unlock()
					done(nil)
				},
				close: func() { mu.Lock(); closeCount++; mu.Unlock() },
			}, nil)
		})})
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Connect(t.Context())
		var validation *protocol.ProtocolValidationError
		if !errors.As(err, &validation) || validation.Message != "Received server data before the client hello was sent" {
			t.Fatalf("connect error = %v", err)
		}
		if state := client.ConnectionState(); state != Disconnected {
			t.Fatalf("connection state = %s", state)
		}
		mu.Lock()
		defer mu.Unlock()
		if sendCount != 0 || closeCount != 1 {
			t.Fatalf("send count = %d, close count = %d; want 0 and 1", sendCount, closeCount)
		}
	})
	t.Run("Client connection lifecycle › rejects typed handshake errors and closes the transport", func(t *testing.T) {
		// upstream: packages/client/test/client.test.ts:282
		t.Parallel()
		var mu sync.Mutex
		closeCount := 0
		helloError, err := protocol.EncodeServerMessage(protocol.ServerHelloError{Error: protocol.ProtocolError{Code: "version", Message: "Unsupported protocol version"}}, protocol.FrameDecoderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		client, err := NewClient(ClientOptions{ServerId: testServerId, TransportFactory: callbackFactory(func(_ context.Context, handlers ByteTransportHandlers, complete func(callbackByteTransport, error)) {
			complete(&funcTransport{
				send: func(_ []byte, done func(error)) {
					handlers.OnData(helloError)
					done(nil)
				},
				close: func() { mu.Lock(); closeCount++; mu.Unlock() },
			}, nil)
		})})
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Connect(t.Context())
		var serverError *ServerError
		if !errors.As(err, &serverError) || serverError.Code != "version" || serverError.Message != "Unsupported protocol version" {
			t.Fatalf("connect error = %v; want ServerError version", err)
		}
		if state := client.ConnectionState(); state != Disconnected {
			t.Fatalf("connection state = %s", state)
		}
		mu.Lock()
		defer mu.Unlock()
		if closeCount != 1 {
			t.Fatalf("close count = %d; want 1", closeCount)
		}
	})
	t.Run("Client connection lifecycle › rejects pending requests and reconnects through a fresh transport", func(t *testing.T) {
		// upstream: packages/client/test/client.test.ts:314
		t.Parallel()
		first, second := newMemoryByteServer(""), newMemoryByteServer("")
		var mu sync.Mutex
		connection := 0
		client, err := NewClient(ClientOptions{ServerId: testServerId, TransportFactory: callbackFactory(func(ctx context.Context, handlers ByteTransportHandlers, complete func(callbackByteTransport, error)) {
			mu.Lock()
			server := first
			if connection > 0 {
				server = second
			}
			connection++
			mu.Unlock()
			server.factory()(ctx, handlers, complete)
		})})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Dispose(); _ = client.WaitClosed(context.Background()) })
		var states []ConnectionState
		if _, err := client.OnConnectionStateChange(NewConnectionStateChangeListener(func(change ConnectionStateChange) {
			mu.Lock()
			states = append(states, change.State)
			mu.Unlock()
		})); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Connect(t.Context()); err != nil {
			t.Fatal(err)
		}
		attachClient(t, client, first, "session-1")
		target := client.Attachment()
		if target == nil {
			t.Fatal("Missing attachment")
		}
		pending := begin(t, context.Background(), client, *target, serviceCall(t, "test.session", "run"))
		first.waitForMessages(t, 3)
		assertMatchObject(t, wireValue(t, first.message(t, 2)), `{"call":{"serviceId":"test.session","member":"run"}}`)
		first.disconnect()

		var disconnected *DisconnectedError
		if err := await(t, pending).err; !errors.As(err, &disconnected) {
			t.Fatalf("pending request error = %v; want DisconnectedError", err)
		}
		hello, err := client.Reconnect(t.Context())
		if err != nil || hello.ServerId != testServerId {
			t.Fatalf("reconnect = %+v, %v", hello, err)
		}
		mu.Lock()
		connections, observed := connection, slices.Clone(states)
		mu.Unlock()
		if connections != 2 {
			t.Fatalf("transport attempts = %d; want 2", connections)
		}
		if !client.Connected() {
			t.Fatal("client is not connected after reconnect")
		}
		if n := len(second.snapshot()); n != 1 {
			t.Fatalf("second server messages = %d; want 1", n)
		}
		if want := []ConnectionState{Connecting, Connected, Disconnected, Connecting, Connected}; !slices.Equal(observed, want) {
			t.Fatalf("states = %v; want %v", observed, want)
		}
	})
	t.Run("Client connection lifecycle › reports transport failures without leaving requests pending", func(t *testing.T) {
		// upstream: packages/client/test/client.test.ts:341
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		pending := begin(t, context.Background(), client, testServerTarget, serviceCall(t, "test", "pending"))
		server.waitForMessages(t, 2)
		server.error(errors.New("read failed"))

		var disconnected *DisconnectedError
		err := await(t, pending).err
		if !errors.As(err, &disconnected) || disconnected.Message != "read failed" || disconnected.Cause == nil || disconnected.Cause.Error() != "read failed" {
			t.Fatalf("pending request error = %#v; want DisconnectedError read failed caused by read failed", err)
		}
		if state := client.ConnectionState(); state != Disconnected {
			t.Fatalf("connection state = %s", state)
		}
	})
	t.Run("Client connection lifecycle › disconnects on invalid or truncated server framing", func(t *testing.T) {
		// upstream: packages/client/test/client.test.ts:356
		t.Parallel()
		invalidServer := newMemoryByteServer("")
		invalidClient := mustConnectClient(t, invalidServer)
		payload, err := protocol.EncodeCbor(protocol.Object{{Key: "type", Value: "response"}, {Key: "id", Value: "unknown"}, {Key: "ok", Value: true}, {Key: "result", Value: float64(1)}}, protocol.CborOptions{})
		if err != nil {
			t.Fatal(err)
		}
		frame, err := protocol.EncodeFrame(payload)
		if err != nil {
			t.Fatal(err)
		}
		invalidServer.sendRaw(t, frame)
		if state := invalidClient.ConnectionState(); state != Disconnected {
			t.Fatalf("invalid frame: connection state = %s", state)
		}

		truncatedServer := newMemoryByteServer("")
		truncatedClient := mustConnectClient(t, truncatedServer)
		pending := begin(t, context.Background(), truncatedClient, testServerTarget, serviceCall(t, "test", "pending"))
		truncatedServer.waitForMessages(t, 2)
		truncatedServer.sendRaw(t, []byte{0, 0, 0, 2, 1})
		truncatedServer.disconnect()

		var validation *protocol.ProtocolValidationError
		if err := await(t, pending).err; !errors.As(err, &validation) || !regexp.MustCompile(`(?i)truncated`).MatchString(validation.Message) {
			t.Fatalf("pending request error = %v; want a truncated ProtocolValidationError", err)
		}
		if state := truncatedClient.ConnectionState(); state != Disconnected {
			t.Fatalf("truncated frame: connection state = %s", state)
		}
	})
	t.Run("Client connection lifecycle › disconnects when a response has no matching request", func(t *testing.T) {
		// upstream: packages/client/test/client.test.ts:376
		t.Parallel()
		server := newMemoryByteServer("")
		client := mustConnectClient(t, server)
		server.send(t, protocol.ResponseEnvelope{Id: "unknown-request", Ok: true, HasResult: true, Result: []any{}})

		if state := client.ConnectionState(); state != Disconnected {
			t.Fatalf("connection state = %s", state)
		}
		if n := server.closeCount(); n != 1 {
			t.Fatalf("client close count = %d; want 1", n)
		}
	})
}

// funcTransport is an inline callbackByteTransport, as upstream's object literals with send and close.
type funcTransport struct {
	send  func([]byte, func(error))
	close func()
}

func (transport *funcTransport) Submit(chunk []byte, complete func(error)) {
	transport.send(chunk, complete)
}
func (transport *funcTransport) Close() { transport.close() }

func jsonEqual(left, right any) bool {
	a, errLeft := json.Marshal(left)
	b, errRight := json.Marshal(right)
	return errLeft == nil && errRight == nil && string(a) == string(b)
}
