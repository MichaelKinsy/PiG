package client

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

type failedStartupTransport struct {
	failure   error
	closed    chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func (transport *failedStartupTransport) Send(_ []byte, complete func(error)) {
	complete(transport.failure)
}
func (transport *failedStartupTransport) Close() {
	transport.closeOnce.Do(func() { close(transport.closed) })
}
func (transport *failedStartupTransport) Done() <-chan struct{} { return transport.done }

// upstream: packages/client/src/client.ts:117-125 awaits disposal on startup failure. Go-owned transport work must be joined before the failed constructor loses its only client reference.
func TestConnectFailureJoinsTransportLifetime(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("hello send failed")
		transport := &failedStartupTransport{failure: failure, closed: make(chan struct{}), done: make(chan struct{})}
		release := sync.OnceFunc(func() { close(transport.done) })
		finished := make(chan struct{})
		var result *Client
		var connectError error
		go func() {
			defer close(finished)
			result, connectError = Connect(t.Context(), ClientOptions{ServerId: "00000000-0000-4000-8000-000000000001", TransportFactory: func(_ context.Context, _ ByteTransportHandlers, complete func(ByteTransport, error)) {
				complete(transport, nil)
			}})
		}()
		t.Cleanup(func() { release(); <-finished })
		<-transport.closed
		synctest.Wait()
		select {
		case <-finished:
			t.Fatal("failed Connect returned before its native transport retired")
		default:
		}
		release()
		<-finished
		if result != nil || !errors.Is(connectError, failure) {
			t.Fatalf("Connect=%v, %v; want nil and original failure", result, connectError)
		}
	})
}

type invocationTestTransport struct {
	handlers ByteTransportHandlers
	request  *protocol.RequestEnvelope
	closed   bool
}

func (transport *invocationTestTransport) Send(frame []byte, complete func(error)) {
	decoder, err := protocol.NewClientMessageDecoder(protocol.FrameDecoderOptions{})
	if err != nil {
		complete(err)
		return
	}
	messages, err := decoder.Push(frame)
	if err != nil {
		complete(err)
		return
	}
	for _, message := range messages {
		switch message := message.(type) {
		case protocol.ClientHello:
			wire, err := protocol.EncodeServerMessage(protocol.ServerHello{Version: 8, ServerId: "00000000-0000-4000-8000-000000000001"}, protocol.FrameDecoderOptions{})
			if err != nil {
				complete(err)
				return
			}
			transport.handlers.OnData(wire)
		case protocol.RequestEnvelope:
			transport.request = &message
		}
	}
	complete(nil)
}
func (transport *invocationTestTransport) Close() { transport.closed = true }

// packages/client/src/client.ts:220-289 admits and sends before returning its Promise. Cancelling a separate observer must not change that operation's original Background Context.
func TestBeginInvokeAdmitsBeforeReturnAndSeparatesWaitCancellation(t *testing.T) {
	t.Parallel()
	transport := &invocationTestTransport{}
	client, err := Connect(t.Context(), ClientOptions{ServerId: "00000000-0000-4000-8000-000000000001", TransportFactory: func(_ context.Context, handlers ByteTransportHandlers, complete func(ByteTransport, error)) {
		transport.handlers = handlers
		complete(transport, nil)
	}})
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
	operation, err := client.BeginInvoke(context.Background(), protocol.ServerTarget{ServerId: client.ServerId()}, chord.ServiceCall{ServiceId: "test", Member: "prompt", Args: []json.RawMessage{}})
	if err != nil {
		t.Fatal(err)
	}
	if transport.request == nil || transport.request.Id != "request-1" {
		t.Fatal("BeginInvoke returned before actual frame admission")
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	reason := errors.New("presentation closed")
	cancel(reason)
	if _, err := operation.Wait(ctx); !errors.Is(err, reason) {
		t.Fatalf("wait cancellation=%v", err)
	}
	if !client.Connected() {
		t.Fatal("cancelling observer disconnected the operation")
	}
	wire, err := protocol.EncodeServerMessage(protocol.ResponseEnvelope{Id: "request-1", Ok: true, HasResult: true, Result: "completed"}, protocol.FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	transport.handlers.OnData(wire)
	result, err := operation.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `"completed"` {
		t.Fatalf("result=%s", result)
	}
}
