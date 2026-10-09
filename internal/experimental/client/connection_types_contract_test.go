package client

// pi: packages/client/src/types.ts

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

type recordedChange struct {
	state ConnectionState
	err   error
}

// connectRecorded connects a Connection over a recordingTransport and returns it with the state changes it reports.
func connectRecorded(t *testing.T) (*Connection, *recordingTransport, func() []recordedChange) {
	t.Helper()
	transport := newRecordingTransport()
	hello := encodeServer(protocol.ServerHello{Version: protocol.ProtocolVersion, ServerId: testServerId})
	var mu sync.Mutex
	var changes []recordedChange
	connection, err := NewConnection(ConnectionOptions{
		TransportFactory: func(_ context.Context, handlers ByteTransportHandlers) (ByteTransport, error) {
			transport.handlers, transport.hello = handlers, hello
			return transport, nil
		},
		ServerId:    testServerId,
		OnHandshake: func(protocol.ServerHello) error { return nil },
		OnMessage:   func(protocol.ServerMessage) {},
		OnStateChange: func(change ConnectionStateChange) {
			mu.Lock()
			changes = append(changes, recordedChange{change.State, change.Error})
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := connection.Connect(t.Context())
		done <- err
	}()
	<-transport.started
	transport.release <- struct{}{}
	if err := <-done; err != nil {
		t.Fatalf("Connect = %v", err)
	}
	return connection, transport, func() []recordedChange {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedChange(nil), changes...)
	}
}

// types.ts ConnectionState / ConnectionStateChange and connection.ts: a new Connection is "disconnected", and a connection moves through
// "connecting" to "connected", reporting each committed state.
func TestConnectionStatesAreReportedInOrder(t *testing.T) {
	connection, _, changes := connectRecorded(t)
	if connection.State() != Connected {
		t.Fatalf("state = %q", connection.State())
	}
	got := changes()
	if len(got) != 2 || got[0].state != Connecting || got[1].state != Connected || got[0].err != nil || got[1].err != nil {
		t.Fatalf("changes = %+v; want connecting then connected without errors", got)
	}
	fresh, err := NewConnection(ConnectionOptions{TransportFactory: nil})
	if err == nil || fresh != nil {
		t.Fatalf("a Connection without a factory and callbacks = %v, %v", fresh, err)
	}
}

// connection.ts:93-95 disconnect(reason = "Client disconnected"): a live connection ends "disconnected" with a DisconnectedError carrying the
// reason, the transport is closed, and a Send afterwards is refused; disconnecting a disconnected connection is a no-op that reports nothing.
func TestConnectionDisconnectReportsItsReasonOnce(t *testing.T) {
	connection, transport, changes := connectRecorded(t)
	connection.Disconnect(nil)
	connection.Disconnect(errors.New("ignored second reason"))
	got := changes()
	if len(got) != 3 || got[2].state != Disconnected {
		t.Fatalf("changes = %+v; want one disconnected report", got)
	}
	var disconnected *DisconnectedError
	if !errors.As(got[2].err, &disconnected) || disconnected.Error() != "Client disconnected" {
		t.Fatalf("reason = %v; want DisconnectedError \"Client disconnected\"", got[2].err)
	}
	transport.mu.Lock()
	closed := transport.closed
	transport.mu.Unlock()
	if closed != 1 || connection.State() != Disconnected {
		t.Fatalf("closed=%d state=%q", closed, connection.State())
	}
	err := connection.Send([]byte("late"))
	if !errors.As(err, &disconnected) || disconnected.Error() != "Client is disconnected" {
		t.Fatalf("Send after disconnect = %v", err)
	}
}

// connection.ts:98-100 fail(error): the connection ends "disconnected" and reports the given error itself.
func TestConnectionFailReportsTheGivenError(t *testing.T) {
	connection, _, changes := connectRecorded(t)
	cause := errors.New("server sent garbage")
	connection.Fail(cause)
	got := changes()
	if connection.State() != Disconnected || len(got) != 3 || got[2].state != Disconnected || got[2].err != cause { //nolint:errorlint // the same error value is reported, not a wrapper
		t.Fatalf("state=%q changes=%+v", connection.State(), got)
	}
}

// connection.ts:68-70: connect() on a connection that is not "disconnected" rejects with DisconnectedError(`Client is already ${state}`).
func TestConnectionRefusesASecondConnect(t *testing.T) {
	connection, _, _ := connectRecorded(t)
	_, err := connection.Connect(t.Context())
	var disconnected *DisconnectedError
	if !errors.As(err, &disconnected) || disconnected.Error() != "Client is already connected" {
		t.Fatalf("second Connect = %v", err)
	}
}

// connection.ts:49-58: maxFrameLength defaults to the protocol's limit and must be a safe integer in 1..4294967295.
func TestConnectionMaxFrameLengthIsValidated(t *testing.T) {
	options := func(limit *float64) ConnectionOptions {
		return ConnectionOptions{
			TransportFactory: func(context.Context, ByteTransportHandlers) (ByteTransport, error) { return nil, errors.New("unused") },
			ServerId:         testServerId, MaxFrameLength: limit,
			OnHandshake: func(protocol.ServerHello) error { return nil }, OnMessage: func(protocol.ServerMessage) {},
			OnStateChange: func(ConnectionStateChange) {},
		}
	}
	def, err := NewConnection(options(nil))
	if err != nil || def.MaxFrameLength() != float64(protocol.DefaultMaxFrameLength) {
		t.Fatalf("default = %v, %v", def, err)
	}
	for _, valid := range []float64{1, 4096, 4294967295} {
		c, err := NewConnection(options(&valid))
		if err != nil || c.MaxFrameLength() != valid {
			t.Fatalf("%v rejected: %v", valid, err)
		}
	}
	for _, invalid := range []float64{0, -1, 1.5, 4294967296, math.NaN(), math.Inf(1)} {
		if _, err := NewConnection(options(&invalid)); err == nil || err.Error() != "Client maxFrameLength must be an integer between 1 and 4294967295" {
			t.Fatalf("%v = %v", invalid, err)
		}
	}
}
