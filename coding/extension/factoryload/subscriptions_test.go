package factoryload

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// An unsubscribe forgets its subscription: the API keeps only live ones, so a long-lived extension that subscribes and unsubscribes does not grow the list its reload would walk.
func TestAPIForgetsUnsubscribedSubscriptions(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	a := newAPI(&extension.Extension{}, runtime, ".", extension.CreateEventBus())
	events := apiEvents{a}
	const n = 10000
	for range n {
		events.On("c", func(any) {})()
	}
	if got := len(a.subscriptions); got != 0 {
		t.Fatalf("subscriptions after %d subscribe/unsubscribe pairs = %d, want 0", n, got)
	}
}

// retire ends the live subscriptions in subscription order and skips the ones already unsubscribed.
func TestAPIRetireEndsLiveSubscriptionsInOrder(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	bus := extension.CreateEventBus()
	a := newAPI(&extension.Extension{}, runtime, ".", bus)
	events := apiEvents{a}
	var calls []string
	offs := map[string]func(){}
	for _, name := range []string{"x", "y", "z"} {
		offs[name] = events.On("ch", func(any) { calls = append(calls, name) })
	}
	offs["y"]()
	bus.Emit("ch", nil)
	got := slices.Clone(calls)
	a.retire("stale")
	calls = nil
	bus.Emit("ch", nil)
	if !slices.Equal(got, []string{"x", "z"}) || len(calls) != 0 {
		t.Fatalf("before retire %v, after retire %v; want [x z] then none", got, calls)
	}
	if len(a.subscriptions) != 0 {
		t.Fatalf("subscriptions after retire = %d, want 0", len(a.subscriptions))
	}
}
