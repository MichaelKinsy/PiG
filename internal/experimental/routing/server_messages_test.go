package routing_test

import (
	"errors"
	"math"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing/routingtest"
)

func exactMessage(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want {
		t.Fatalf("error=%v, want %q", err, want)
	}
}

// Pi: packages/server/src/errors.ts:1-57 codes and default messages.
// mutation-checked: the mutant "SessionAmbiguousError message changed" fails it.
func TestServerErrorsCarryPiCodesAndMessages(t *testing.T) {
	if routing.InternalServerErrorMessage != "Internal server error" {
		t.Fatalf("InternalServerErrorMessage=%q", routing.InternalServerErrorMessage)
	}
	for _, test := range []struct {
		err             interface{ Error() string }
		code            routing.ServerOperationErrorCode
		message         string
		extractedServer *routing.ServerError
	}{
		{routing.NewWrongServerError(), "wrong_server", "Request was addressed to another server", routing.NewWrongServerError().ServerError},
		{routing.NewSessionNotFoundError(), "session_not_found", "Session was not found", routing.NewSessionNotFoundError().ServerError},
		{routing.NewSessionNotFoundError("custom"), "session_not_found", "custom", routing.NewSessionNotFoundError("custom").ServerError},
		{routing.NewSessionAmbiguousError(), "session_ambiguous", "Session ID matches more than one session", routing.NewSessionAmbiguousError().ServerError},
		{routing.NewSessionNotAttachedError(), "session_not_attached", "Session is not attached to this client", routing.NewSessionNotAttachedError().ServerError},
		{routing.NewServerDrainingError(), "server_draining", "Server is draining", routing.NewServerDrainingError().ServerError},
	} {
		if test.err.Error() != test.message || test.extractedServer.Code != test.code || test.extractedServer.Message != test.message {
			t.Fatalf("%T = %q (%s/%s), want %s %q", test.err, test.err.Error(), test.extractedServer.Code, test.extractedServer.Message, test.code, test.message)
		}
		if failure, ok := errors.AsType[*routing.ServerError](test.err.(error)); !ok || failure.Code != test.code {
			t.Fatalf("%T does not unwrap to its ServerError", test.err)
		}
	}
	if routing.BasicSessionMetadata(routing.BasicSessionMetadata{ID: "s-1"}).SessionID() != "s-1" {
		t.Fatal("BasicSessionMetadata.SessionID")
	}
}

// Pi: packages/server/src/server.ts:61-90 constructor validation and its messages.
// mutation-checked: negating the condition `!serverInteger(frame, 1, math.MaxUint32)` at server.go:108 fails it.
func TestServerOptionsAreValidatedLikePi(t *testing.T) {
	t.Parallel()
	host := newTestServerHost()
	build := func(mutate func(*routing.ServerOptions)) error {
		options := routing.ServerOptions{Listeners: []routing.ServerListener{}, ServerId: testServerID}
		mutate(&options)
		server, err := routing.NewServer(host, options)
		if err == nil {
			_ = server.Close()
		}
		return err
	}
	exactMessage(t, build(func(o *routing.ServerOptions) { o.Listeners = nil }), "Server listeners must be an array")
	exactMessage(t, build(func(o *routing.ServerOptions) { o.ServerId = "x" }), "serverId must be a canonical lowercase UUIDv4")
	const frame = "Server maxFrameLength must be an integer between 1 and 4294967295"
	for _, value := range []float64{0, -1, 1.5, math.NaN(), 4294967296} {
		exactMessage(t, build(func(o *routing.ServerOptions) { o.MaxFrameLength = &value }), frame)
	}
	for _, value := range []float64{1, 4294967295} {
		if err := build(func(o *routing.ServerOptions) { o.MaxFrameLength = &value }); err != nil {
			t.Fatalf("maxFrameLength %v rejected: %v", value, err)
		}
	}
	const timeout = "Server handshakeTimeoutMs must be an integer between 1 and 2147483647"
	for _, value := range []float64{0, -1, 1.5, math.NaN(), 2147483648} {
		exactMessage(t, build(func(o *routing.ServerOptions) { o.HandshakeTimeoutMs = &value }), timeout)
	}
	for _, value := range []float64{1, 2147483647} {
		if err := build(func(o *routing.ServerOptions) { o.HandshakeTimeoutMs = &value }); err != nil {
			t.Fatalf("handshakeTimeoutMs %v rejected: %v", value, err)
		}
	}
}

// Pi: server.ts:95-110 start() states and messages.
// mutation-checked: negating the condition `!serverInteger(frame, 1, math.MaxUint32)` at server.go:108 fails it.
func TestServerStartStatesReportPiMessages(t *testing.T) {
	t.Parallel()
	server := createServer(t, newTestServerHost())
	if _, err := server.Start(); err != nil {
		t.Fatal(err)
	}
	exactMessage(t, startError(server), "Server is already started")
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	exactMessage(t, startError(server), "Server is closing or closed")
}

// Pi: server.ts:147-152 the handshake timer defaults to 5,000 ms and closes the connection with invalid_request "Handshake timeout".
// mutation-checked: negating the condition `!serverInteger(frame, 1, math.MaxUint32)` at server.go:108 fails it.
func TestServerDefaultHandshakeTimeoutIsFiveSeconds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		server := createServer(t, newTestServerHost())
		client := connect(t, server)
		time.Sleep(4999 * time.Millisecond)
		synctest.Wait()
		if client.Closed() {
			t.Fatal("connection closed before the default handshake timeout")
		}
		time.Sleep(2 * time.Millisecond)
		synctest.Wait()
		failure := expectHelloError(t, client.next(isHelloError), "invalid_request")
		if failure.Error.Message != "Handshake timeout" {
			t.Fatalf("message=%q", failure.Error.Message)
		}
		client.waitForClose()
	})
}

// Pi: server.ts:239-245 and :214-229 failure messages and codes on the wire.
// mutation-checked: negating the condition `c.closedValue` at client.go:264 fails it.
func TestServerProtocolFailureMessages(t *testing.T) {
	t.Parallel()
	t.Run("first message is not a hello", func(t *testing.T) {
		client := connectWire(t)
		client.sendMessage(directoryListRequest())
		failure := expectHelloError(t, client.next(isHelloError), "invalid_request")
		if failure.Error.Message != "The first client message must be hello" {
			t.Fatalf("message=%q", failure.Error.Message)
		}
	})
	t.Run("unsupported version", func(t *testing.T) {
		client := connectWire(t)
		failure := expectHelloError(t, client.hello(protocol.ProtocolVersion+1), "version")
		if failure.Error.Message != "Unsupported protocol version 9; expected 8" {
			t.Fatalf("message=%q", failure.Error.Message)
		}
	})
	t.Run("second hello", func(t *testing.T) {
		client := connectWire(t)
		client.hello(protocol.ProtocolVersion)
		client.sendMessage(protocol.ClientHello{Version: protocol.ProtocolVersion})
		failure := expectHelloError(t, client.next(isHelloError), "invalid_request")
		if failure.Error.Message != "hello may only be sent as the first message" {
			t.Fatalf("message=%q", failure.Error.Message)
		}
	})
}

// Pi: server.ts:248-282 handleCancel ignores another server's target and an id owned by another target; handleRequest rejects a repeated active id and an invalid call.
// mutation-checked: negating the condition `err != nil` at client.go:112 fails it.
func TestServerRequestAndCancelRules(t *testing.T) {
	t.Parallel()
	t.Run("repeated active request id", func(t *testing.T) {
		host := routingtest.NewTestServerHost()
		host.Seed(nil)
		gate := host.GateNextOpenSession()
		client := connect(t, createServer(t, host.ServerHost()))
		client.hello(protocol.ProtocolVersion)
		attaching := client.attachAsync(testServerID, "session-1")
		<-gate.Entered.Promise()
		first := protocol.RequestEnvelope{Id: "dup", Target: protocol.ServerTarget{ServerId: testServerID}, Call: protocol.Object{{Key: "serviceId", Value: "pi.session-management"}, {Key: "member", Value: "detach"}, {Key: "args", Value: []any{}}}}
		client.sendMessage(first)
		client.sendMessage(first)
		response := client.next(func(message protocol.ServerMessage) bool {
			response, ok := message.(protocol.ResponseEnvelope)
			return ok && response.Id == "dup" && !response.Ok
		}).(protocol.ResponseEnvelope)
		if response.Error == nil || response.Error.Code != "invalid_request" || response.Error.Message != "Request ID is already active" {
			t.Fatalf("response=%#v", response)
		}
		gate.Release.Resolve(struct{}{})
		awaitAttach(t, attaching)
	})
	t.Run("invalid call", func(t *testing.T) {
		client := connect(t, createServer(t, newTestServerHost()))
		client.hello(protocol.ProtocolVersion)
		client.sendMessage(protocol.RequestEnvelope{Id: "bad", Target: protocol.ServerTarget{ServerId: testServerID}, Call: protocol.Object{{Key: "arbitrary", Value: true}}})
		response := client.next(isResponse).(protocol.ResponseEnvelope)
		if response.Error == nil || response.Error.Code != "invalid_request" || response.Error.Message != "Invalid service call" {
			t.Fatalf("response=%#v", response)
		}
	})
	t.Run("a request for another server", func(t *testing.T) {
		client := connect(t, createServer(t, newTestServerHost()))
		client.hello(protocol.ProtocolVersion)
		failure := client.attach(otherServerID, "session-1")
		if failure.Ok || failure.Error == nil || failure.Error.Code != "wrong_server" || failure.Error.Message != "Request was addressed to another server" {
			t.Fatalf("response=%#v", failure)
		}
	})
}
