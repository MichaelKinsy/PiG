package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// upstream: services/connection.ts:createClientServiceTransport(client, () => ({ serverId })) for the server source and () => client.attachment for the Session source. The adapter maps the source's target to the client's RPC target without losing or inventing a field.
func TestClientSourceTargetsAndCataloguesMapExactly(t *testing.T) {
	session, attachment := "session", "attachment"
	if got := sourceTarget(services.ServiceTarget{ServerID: "server"}); !reflect.DeepEqual(got, protocol.ServerTarget{ServerId: "server"}) {
		t.Fatalf("server target = %#v", got)
	}
	if got := sourceTarget(services.ServiceTarget{ServerID: "server", SessionID: &session, AttachmentID: &attachment}); !reflect.DeepEqual(got, protocol.SessionTarget{ServerId: "server", SessionId: "session", AttachmentId: "attachment"}) {
		t.Fatalf("session target = %#v", got)
	}
	if got := sourceAttachment(nil); got != nil {
		t.Fatalf("nil attachment = %#v", got)
	}
	if got := sourceAttachment(&protocol.SessionTarget{ServerId: "server", SessionId: "session", AttachmentId: "attachment"}); !reflect.DeepEqual(got, &services.SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "attachment"}) {
		t.Fatalf("attachment = %#v", got)
	}
	got := sourceCatalogue([]services.ServiceCatalogueEntry{{ServiceID: "pi.models", Mode: "singleton"}, {ServiceID: "pi.keyed", Mode: "keyed"}})
	want := []chord.ServiceCatalogueEntry{{ServiceId: "pi.models", Mode: chord.ServiceSingleton}, {ServiceId: "pi.keyed", Mode: chord.ServiceKeyed}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalogue = %#v, want %#v", got, want)
	}
	if got := sourceCatalogue(nil); got == nil || len(got) != 0 {
		t.Fatalf("empty catalogue = %#v", got)
	}
}

// upstream: services/connection.ts:ServerServiceSource.connection is a ReplicatedState value; the adapter exposes a detached typed replica whose hydration carries the source's delivery kind and sequence.
func TestSourceStateReplicaHydratesCurrentValue(t *testing.T) {
	state := &services.SourceState[services.ServerConnectionState]{}
	replica := sourceStateReplica[services.ServerConnectionState]{source: state}
	first := replica.Value()
	if first == nil || *first != (services.ServerConnectionState{}) {
		t.Fatalf("value = %v", first)
	}
	*first = services.ServerConnectionState{Status: "mutated"}
	if got := replica.Value(); *got != (services.ServerConnectionState{}) {
		t.Fatalf("value aliases the caller's copy: %v", got)
	}
	var kinds []string
	stop, err := replica.Subscribe(func(value *services.ServerConnectionState, _ context.Context, delivery chord.ReplicatedStateDelivery) {
		kinds = append(kinds, delivery.Kind)
		if *value != (services.ServerConnectionState{}) {
			t.Errorf("hydrated value = %v", value)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	stop()
	if !reflect.DeepEqual(kinds, []string{"hydrate"}) {
		t.Fatalf("deliveries = %v", kinds)
	}
}

// upstream: services/connection.ts creates every remote binding with `bound: false` and rejects local services at the remote boundary.
func TestClientBindingAdapterRejectsLocalServices(t *testing.T) {
	adapter := &clientBindingAdapter{}
	const want = "Local services cannot cross a remote binding"
	if _, err := adapter.Use(services.Service{ID: "pi.local.presentation-ui", Local: true}); err == nil || err.Error() != want {
		t.Fatalf("Use = %v", err)
	}
	if _, err := adapter.Observe(services.Service{ID: "pi.local.presentation-ui", Local: true}, func(context.Context, any) error { return nil }); err == nil || err.Error() != want {
		t.Fatalf("Observe = %v", err)
	}
}

type recordingTransportClient struct {
	mu       sync.Mutex
	requests int
}

func (c *recordingTransportClient) Invoke(context.Context, chord.ServiceCall) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests++
	return nil, errors.New("unexpected request")
}

func (c *recordingTransportClient) Subscribe(context.Context, string, chord.ServiceMode, chord.UpdateListener) (chord.ServiceSubscription, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests++
	return nil, errors.New("unexpected subscription")
}

// upstream: services/connection.ts creates each remote binding with `bound: false`; only the routed binding's first Ready (or a later updateBound) binds it. A binding that has not been told it is bound sends nothing to the client.
func TestClientSourceBindingStartsUnbound(t *testing.T) {
	transport := &recordingTransportClient{}
	options := clientSourceOptions(nil, ClientServiceSourceOptions{wrapTransport: func(chord.RemoteServiceTransport, func() protocol.RpcTarget) chord.RemoteServiceTransport {
		return transport
	}})
	binding := options.NewBinding(services.RemoteServiceBindingOptions{
		ServiceBindingOptions: services.ServiceBindingOptions{Services: []services.Service{{ID: "pi.models"}}, AssertAccess: func() error { return nil }, OnError: func(error) {}},
		GetTarget:             func() *services.ServiceTarget { return &services.ServiceTarget{ServerID: "server"} },
	})
	t.Cleanup(func() { _ = binding.Dispose(context.Background()) })
	if _, err := binding.Use(services.Service{ID: "pi.models"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	binding.Ready(t.Context(), func(err error) { done <- err })
	if err := <-done; err != nil {
		t.Fatalf("unbound Ready = %v", err)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.requests != 0 {
		t.Fatalf("an unbound binding contacted the client %d times", transport.requests)
	}
}
