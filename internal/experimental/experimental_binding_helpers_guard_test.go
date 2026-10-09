package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/client"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// This external byte peer mirrors packages/client/test/support.ts while using the real Chord endpoint. It is not a replacement experimental server/runtime.
type bindingFixturePeer struct {
	mu                 sync.Mutex
	decoder            *protocol.ClientMessageDecoder
	handlers           client.ByteTransportHandlers
	endpoint           chord.RemoteServiceEndpoint
	queued             [][]byte
	delivering, closed bool
}

func (peer *bindingFixturePeer) send(message protocol.ServerMessage) {
	wire, err := protocol.EncodeServerMessage(message, protocol.FrameDecoderOptions{})
	if err != nil {
		peer.handlers.OnError(err)
		return
	}
	peer.mu.Lock()
	peer.queued = append(peer.queued, wire)
	if peer.delivering {
		peer.mu.Unlock()
		return
	}
	peer.delivering = true
	peer.mu.Unlock()
	for {
		peer.mu.Lock()
		if peer.closed || len(peer.queued) == 0 {
			peer.delivering = false
			peer.mu.Unlock()
			return
		}
		frame := peer.queued[0]
		peer.queued = peer.queued[1:]
		peer.mu.Unlock()
		peer.handlers.OnData(frame)
	}
}

// Send is Pi's ByteTransport.send: it returns when the peer has taken the chunk.
func (peer *bindingFixturePeer) Send(ctx context.Context, chunk []byte) error {
	done := make(chan error, 1)
	peer.Submit(chunk, func(err error) { done <- err })
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// Submit admits a chunk synchronously, which Connection uses directly for a transport that has it.
func (peer *bindingFixturePeer) Submit(chunk []byte, complete func(error)) {
	peer.mu.Lock()
	messages, err := peer.decoder.Push(chunk)
	peer.mu.Unlock()
	if err != nil {
		complete(err)
		return
	}
	for _, message := range messages {
		switch message := message.(type) {
		case protocol.ClientHello:
			peer.send(protocol.ServerHello{Version: protocol.ProtocolVersion, ServerId: "00000000-0000-4000-8000-000000000001"})
		case protocol.RequestEnvelope:
			raw, err := protocol.ToJSON(message.Call)
			if err != nil {
				complete(err)
				return
			}
			call, err := chord.ParseServiceCall(raw)
			if err != nil {
				complete(err)
				return
			}
			result, err := peer.endpoint.Invoke(context.Background(), call, func(_ context.Context, id string, update chord.ServiceProviderUpdate) error {
				data, err := json.Marshal(update)
				if err != nil {
					return err
				}
				value, err := protocol.FromJSON(data)
				if err != nil {
					return err
				}
				peer.send(protocol.ServiceEventEnvelope{SubscriptionId: id, Update: value})
				return nil
			})
			if err != nil {
				peer.send(protocol.ResponseEnvelope{Id: message.Id, Error: &protocol.ProtocolError{Code: "fixture_error", Message: err.Error()}})
				continue
			}
			response := protocol.ResponseEnvelope{Id: message.Id, Ok: true, HasResult: len(result) > 0}
			if response.HasResult {
				response.Result, err = protocol.FromJSON(result)
				if err != nil {
					complete(err)
					return
				}
			}
			peer.send(response)
		}
	}
	complete(nil)
}
func (peer *bindingFixturePeer) Close() {
	peer.mu.Lock()
	peer.closed = true
	peer.queued = nil
	peer.mu.Unlock()
}

type bindingFixtureManagement struct{ peer *bindingFixturePeer }

func (manager bindingFixtureManagement) Create(context.Context, services.SessionCreateOptions) (services.SessionSummary, error) {
	return services.SessionSummary{}, errors.New("unused fixture create")
}
func (manager bindingFixtureManagement) Remove(context.Context, string) error {
	return errors.New("unused fixture remove")
}
func (manager bindingFixtureManagement) Attach(_ context.Context, id string) error {
	manager.peer.send(protocol.AttachmentEnvelope{Attachment: &protocol.SessionTarget{ServerId: "00000000-0000-4000-8000-000000000001", SessionId: id, AttachmentId: "attachment-" + id}})
	return nil
}
func (manager bindingFixtureManagement) Detach(context.Context) error {
	manager.peer.send(protocol.AttachmentEnvelope{})
	return nil
}

func TestExperimentalBindingHelpersUseRealSourcesAndEndpoint(t *testing.T) {
	t.Parallel()
	provider, err := chord.NewRemoteServiceProvider(chord.SingletonService(services.SessionManagementDefinition))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := provider.Dispose(); err != nil {
			t.Error(err)
		}
	})
	decoder, err := protocol.NewClientMessageDecoder(protocol.FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	peer := &bindingFixturePeer{decoder: decoder, endpoint: chord.CreateRemoteServiceEndpoint(provider)}
	t.Cleanup(peer.endpoint.Dispose)
	if err := chord.Provide[services.SessionManagement](provider, services.SessionManagementDefinition, bindingFixtureManagement{peer}); err != nil {
		t.Fatal(err)
	}
	connected, err := client.Connect(t.Context(), client.ClientOptions{ServerId: "00000000-0000-4000-8000-000000000001", TransportFactory: func(_ context.Context, handlers client.ByteTransportHandlers) (client.ByteTransport, error) {
		peer.handlers = handlers
		return peer, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := connected.Dispose(); err != nil {
			t.Error(err)
		}
		if err := connected.WaitClosed(context.Background()); err != nil {
			t.Error(err)
		}
	})
	binding := createSessionServiceBinding(t, connected, []string{}, ClientServiceSourceOptions{})
	if err := binding.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	attachSession(t, connected, "demo")
	if err := binding.WhenAttached(t.Context(), "demo"); err != nil {
		t.Fatal(err)
	}
	if state := binding.Attachment().Value(); state.Status != "attached" || state.SessionID != "demo" {
		t.Fatalf("attachment=%+v", state)
	}
	wrap, release, _ := delaySessionServiceSubscription(t, "demo", "blocked")
	decorated := wrap(client.CreateClientServiceTransport(connected, func() protocol.RpcTarget { return *connected.Attachment() }), func() protocol.RpcTarget { return *connected.Attachment() })
	defer release()
	ctx, cancel := context.WithCancelCause(t.Context())
	reason := errors.New("fixture cancelled")
	cancel(reason)
	result := make(chan error, 1)
	go func() {
		_, err := decorated.Subscribe(ctx, "blocked", chord.ServiceSingleton, func(context.Context, chord.ServiceProviderUpdate) {})
		result <- err
	}()
	release()
	release()
	if err := <-result; !errors.Is(err, reason) {
		t.Fatalf("forwarded cancellation=%v, want %v", err, reason)
	}
	if err := binding.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
}
