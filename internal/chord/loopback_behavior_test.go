package chord

// Pins packages/chord/src/services/loopback.ts createLoopbackServiceTransport: invoke, beginInvoke and subscribe reach the
// provider unchanged, so the caller's context (values and cancellation) is the context the member runs under and the
// member's result and error come back as they are. The services tests drive the loopback only through bindings.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestLoopbackTransportForwardsInvokeAdmissionAndSubscribeToTheProvider(t *testing.T) {
	t.Parallel()
	def := DefineService[*FacetServiceImplementation]("test.loopback.forward")
	type contextKey struct{}
	var seen []any
	failure := errors.New("member failed")
	implementation := &FacetServiceImplementation{Members: []FacetServiceMember{
		{Name: "echo", Invoke: func(ctx context.Context, args []json.RawMessage) (json.RawMessage, error) {
			seen = append(seen, ctx.Value(contextKey{}))
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if len(args) == 0 {
				return nil, failure
			}
			return args[0], nil
		}},
	}}
	provider, err := NewRemoteServiceProvider(SingletonService(def))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Dispose() })
	if err := Provide(provider, def, implementation); err != nil {
		t.Fatal(err)
	}
	transport := NewLoopbackTransport(provider)
	ctx := context.WithValue(t.Context(), contextKey{}, "caller")
	call := ServiceCall{ServiceId: def.Id(), Member: "echo", Args: []json.RawMessage{json.RawMessage(`{"a":1}`)}}

	got, err := transport.Invoke(ctx, call)
	if err != nil || string(got) != `{"a":1}` {
		t.Fatalf("Invoke = %s, %v", got, err)
	}
	if _, err := transport.Invoke(ctx, ServiceCall{ServiceId: def.Id(), Member: "echo"}); !errors.Is(err, failure) {
		t.Fatalf("a member error = %v, want it unchanged", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := transport.Invoke(cancelled, call); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled caller = %v, want the member to see the cancellation", err)
	}
	initiator, ok := transport.(interface {
		BeginInvoke(context.Context, ServiceCall) (*ServiceInvocation, error)
	})
	if !ok {
		t.Fatal("the loopback does not expose the provider's admission boundary")
	}
	// A member without an admission boundary reports the provider's own error; the loopback adds nothing.
	_, direct := provider.BeginInvoke(ctx, call)
	_, viaLoopback := initiator.BeginInvoke(ctx, call)
	if direct == nil || !errors.Is(viaLoopback, direct) {
		t.Fatalf("BeginInvoke through the loopback = %v, directly = %v; want the same error", viaLoopback, direct)
	}
	for i, value := range seen {
		if value != "caller" {
			t.Fatalf("member call %d saw context value %v, want the caller's", i, value)
		}
	}
	subscription, err := transport.Subscribe(ctx, def.Id(), ServiceSingleton, func(context.Context, ServiceProviderUpdate) {})
	if err != nil || subscription == nil || subscription.Snapshot().ServiceId != def.Id() {
		t.Fatalf("Subscribe = %+v, %v", subscription, err)
	}
	_ = subscription.Close(ctx)
}
