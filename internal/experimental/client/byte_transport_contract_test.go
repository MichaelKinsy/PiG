//go:build !windows

package client

// pi: packages/client/src/transport.ts

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// transportEvents records what a ByteTransportHandlers received, in order.
type transportEvents struct {
	mu     sync.Mutex
	data   []byte
	closes int
	errs   []error
	signal chan struct{}
}

func newTransportEvents() *transportEvents { return &transportEvents{signal: make(chan struct{}, 16)} }

func (e *transportEvents) handlers() ByteTransportHandlers {
	return ByteTransportHandlers{
		OnData: func(chunk []byte) {
			e.mu.Lock()
			e.data = append(e.data, chunk...)
			e.mu.Unlock()
			e.signal <- struct{}{}
		},
		OnClose: func() {
			e.mu.Lock()
			e.closes++
			e.mu.Unlock()
			e.signal <- struct{}{}
		},
		OnError: func(err error) {
			e.mu.Lock()
			e.errs = append(e.errs, err)
			e.mu.Unlock()
			e.signal <- struct{}{}
		},
	}
}

func (e *transportEvents) waitFor(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		e.mu.Lock()
		ok := done()
		e.mu.Unlock()
		if ok {
			return
		}
		select {
		case <-e.signal:
		case <-deadline:
			t.Fatal("timed out waiting for the transport handlers")
		}
	}
}

func connectTransport(t *testing.T, serve func(net.Conn), handlers ByteTransportHandlers) ByteTransport {
	t.Helper()
	path := filepath.Join(shortDirectory(t, "pi-transport-contract-"), "pi.sock")
	listenUnix(t, path, serve)
	factory, err := CreateUnixTransportFactory(UnixTransportOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := factory(t.Context(), handlers)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	t.Cleanup(transport.Close)
	startReading(transport)
	return transport
}

// startReading plays the Connection's part: handlers deliver nothing until it has taken the transport.
func startReading(transport ByteTransport) {
	if reader, ok := transport.(interface{ startReading() }); ok {
		reader.startReading()
	}
}

// transport.ts ByteTransport.send: "Calls must be delivered in invocation order." Chunks admitted without waiting for the earlier writes to
// settle reach the peer in that order, and each admission settles once.
func TestPiByteTransportSendContractKeepsInvocationOrder(t *testing.T) {
	received := make(chan []byte, 1)
	events := newTransportEvents()
	transport := connectTransport(t, func(conn net.Conn) {
		var all []byte
		buffer := make([]byte, 4096)
		for len(all) < len("alpha-beta-gamma-delta") {
			n, err := conn.Read(buffer)
			all = append(all, buffer[:n]...)
			if err != nil {
				break
			}
		}
		received <- all
	}, events.handlers())
	submitter, ok := transport.(callbackByteTransport)
	if !ok {
		t.Fatal("the Unix transport does not admit chunks synchronously")
	}
	settled := make(chan int, 4)
	for index, chunk := range []string{"alpha-", "beta-", "gamma-", "delta"} {
		submitter.Submit([]byte(chunk), func(err error) {
			if err != nil {
				t.Errorf("chunk %d settled with %v", index, err)
			}
			settled <- index
		})
	}
	select {
	case got := <-received:
		if string(got) != "alpha-beta-gamma-delta" {
			t.Fatalf("peer received %q", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the peer did not receive the chunks")
	}
	for want := range 4 {
		if got := <-settled; got != want {
			t.Fatalf("settlement %d was for chunk %d", want, got)
		}
	}
}

// transport.ts ByteTransport.close: "Implementations must make repeated calls harmless." A local close is not a remote notification, and a send
// after it is refused.
func TestByteTransportCloseIsIdempotentAndSilent(t *testing.T) {
	events := newTransportEvents()
	transport := connectTransport(t, func(conn net.Conn) { _, _ = conn.Read(make([]byte, 1)) }, events.handlers())
	transport.Close()
	transport.Close()
	transport.Close()
	if err := transport.Send(t.Context(), []byte("late")); err == nil {
		t.Fatal("a send after close succeeded")
	}
	time.Sleep(100 * time.Millisecond)
	events.mu.Lock()
	defer events.mu.Unlock()
	if events.closes != 0 || len(events.errs) != 0 {
		t.Fatalf("a local close reported closes=%d errors=%v", events.closes, events.errs)
	}
}

// transport.ts ByteTransportHandlers: onData delivers inbound byte chunks in order whatever their boundaries, and onClose reports one orderly
// terminal close, with no onError.
func TestByteTransportHandlersDeliverDataThenOneOrderlyClose(t *testing.T) {
	events := newTransportEvents()
	connectTransport(t, func(conn net.Conn) {
		for _, part := range []string{"one", "two", "three"} {
			_, _ = conn.Write([]byte(part))
			time.Sleep(5 * time.Millisecond)
		}
		_ = conn.Close()
	}, events.handlers())
	events.waitFor(t, func() bool { return events.closes > 0 })
	time.Sleep(50 * time.Millisecond)
	events.mu.Lock()
	defer events.mu.Unlock()
	if string(events.data) != "onetwothree" || events.closes != 1 || len(events.errs) != 0 {
		t.Fatalf("data=%q closes=%d errors=%v", events.data, events.closes, events.errs)
	}
}

// transport.ts ByteTransportHandlers.onError: "Reports a terminal transport failure." A peer that closes with unread data in its receive queue
// resets the connection, which is a failure and not an orderly close.
func TestByteTransportHandlersReportATerminalFailure(t *testing.T) {
	events := newTransportEvents()
	accepted := make(chan struct{})
	transport := connectTransport(t, func(conn net.Conn) {
		close(accepted)
		time.Sleep(200 * time.Millisecond)
		_ = conn.Close()
	}, events.handlers())
	<-accepted
	if err := transport.Send(t.Context(), []byte("unread by the peer")); err != nil {
		t.Fatal(err)
	}
	events.waitFor(t, func() bool { return events.closes+len(events.errs) > 0 })
	events.mu.Lock()
	defer events.mu.Unlock()
	if len(events.errs) != 1 || events.closes != 0 {
		t.Fatalf("closes=%d errors=%v; want one failure and no orderly close", events.closes, events.errs)
	}
}

// transport.ts ByteTransportFactory: "Creates a fresh connected, authenticated transport"; every call returns a new transport, and a failed
// attempt rejects with the error instead of returning one.
func TestByteTransportFactoryReturnsAFreshTransportOrTheError(t *testing.T) {
	path := filepath.Join(shortDirectory(t, "pi-transport-factory-"), "pi.sock")
	listenUnix(t, path, func(conn net.Conn) { _, _ = conn.Read(make([]byte, 1)) })
	factory, err := CreateUnixTransportFactory(UnixTransportOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	first, err := factory(t.Context(), ByteTransportHandlers{OnData: func([]byte) {}, OnClose: func() {}, OnError: func(error) {}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := factory(t.Context(), ByteTransportHandlers{OnData: func([]byte) {}, OnClose: func() {}, OnError: func(error) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	defer second.Close()
	if first == second {
		t.Fatal("two factory calls returned the same transport")
	}
	missing, err := CreateUnixTransportFactory(UnixTransportOptions{Path: filepath.Join(filepath.Dir(path), "absent.sock")})
	if err != nil {
		t.Fatal(err)
	}
	if transport, err := missing(context.Background(), ByteTransportHandlers{}); err == nil || transport != nil {
		t.Fatalf("a factory for an absent socket returned %v, %v", transport, err)
	}
}

// errors.ts (declared beside the transport types in transport.go): ServerError keeps the server's code and message, DisconnectedError keeps its
// cause and defaults its message, ClientDisposedError has no cause; each sets its name.
func TestTransportErrorClassesAreTheNamedErrorsOfTheClient(t *testing.T) {
	server := NewServerError(protocol.ProtocolError{Code: "invalid_request", Message: "bad call"})
	if server.Name() != "ServerError" || server.Code != "invalid_request" || server.Error() != "bad call" {
		t.Errorf("ServerError = %q %q %q", server.Name(), server.Code, server.Error())
	}
	cause := errors.New("socket reset")
	disconnected := NewDisconnectedError("lost", cause)
	if disconnected.Name() != "DisconnectedError" || disconnected.Error() != "lost" || !errors.Is(disconnected, cause) || !errors.Is(disconnected.Unwrap(), cause) {
		t.Errorf("DisconnectedError = %q %q unwrap=%v", disconnected.Name(), disconnected.Error(), disconnected.Unwrap())
	}
	if got := disconnectedError(); got.Error() != "Client is disconnected" {
		t.Errorf("default message = %q", got.Error())
	}
	disposed := NewClientDisposedError()
	if disposed.Name() != "ClientDisposedError" || disposed.Error() != "Client is disposed" || disposed.Cause() != nil {
		t.Errorf("ClientDisposedError = %q %q cause=%v", disposed.Name(), disposed.Error(), disposed.Cause())
	}
}
