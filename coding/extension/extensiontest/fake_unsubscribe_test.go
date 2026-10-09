package extensiontest

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// packages/coding-agent/src/core/extensions/types.ts:1558 `on(event, handler): () => void`: the returned function removes that
// registration only, a second call does nothing, and the other handlers keep their order.
// Pi: packages/coding-agent/src/core/extensions/types.ts:1558 (API.on).
func TestOnReturnsAnIdempotentUnsubscribeThatRemovesOnlyItsHandler(t *testing.T) {
	var fake Fake
	var api extension.API = &fake
	var order []string
	handler := func(name string) func(ctx context.Context, evt extension.SessionStartEvent) error {
		return func(context.Context, extension.SessionStartEvent) error { order = append(order, name); return nil }
	}
	api.OnSessionStart(handler("a"))
	unsubscribeB := api.OnSessionStart(handler("b"))
	api.OnSessionStart(handler("c"))
	unsubscribeB()
	unsubscribeB()
	if len(fake.OnSessionStartHandlers) != 2 {
		t.Fatalf("%d handlers after unsubscribe, want 2", len(fake.OnSessionStartHandlers))
	}
	for _, h := range fake.OnSessionStartHandlers {
		_ = h(context.Background(), extension.SessionStartEvent{})
	}
	if len(order) != 2 || order[0] != "a" || order[1] != "c" {
		t.Fatalf("remaining handlers ran %v, want [a c]", order)
	}
}

// Two registrations of the same function value are two subscriptions: each unsubscribe removes one.
func TestUnsubscribeRemovesOneRegistrationOfARepeatedHandler(t *testing.T) {
	var fake Fake
	same := func(context.Context, extension.TurnStartEvent) error { return nil }
	first := fake.OnTurnStart(same)
	second := fake.OnTurnStart(same)
	first()
	if len(fake.OnTurnStartHandlers) != 1 {
		t.Fatalf("%d handlers after one unsubscribe, want 1", len(fake.OnTurnStartHandlers))
	}
	first()
	if len(fake.OnTurnStartHandlers) != 1 {
		t.Fatal("a repeated unsubscribe removed the other registration")
	}
	second()
	if len(fake.OnTurnStartHandlers) != 0 {
		t.Fatalf("%d handlers after both unsubscribes, want 0", len(fake.OnTurnStartHandlers))
	}
}
