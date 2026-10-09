package inproc

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/extensiontest"
)

// Pi runner.ts:1020-1040 awaits every snapshotted handler, reports rejection, and keeps the last explicit action.
func TestCacheWarmingDecisionAwaitsAndReportsHandlers(t *testing.T) {
	entered := make(chan struct{})
	order := []string{}
	runner := NewRunner([]extension.Extension{
		{Path: "first", Handlers: map[string][]extension.HandlerFn{"cache_warming_decision": {func(args ...any) (any, error) {
			order = append(order, "first")
			close(entered)
			<-args[1].(context.Context).Done()
			return nil, errors.New("decision failed")
		}}}},
		{Path: "last", Handlers: map[string][]extension.HandlerFn{"cache_warming_decision": {func(...any) (any, error) {
			order = append(order, "last")
			return &extension.CacheWarmingDecisionEventResult{Action: new(extension.CacheWarmingActionStop)}, nil
		}}}},
	}, t.TempDir())
	var reported []string
	runner.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err.Event+":"+err.Error) })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan extension.CacheWarmingAction, 1)
	go func() {
		action, err := runner.EmitCacheWarmingDecision(ctx, extension.CacheWarmingDecisionEvent{Type: "cache_warming_decision", Action: extension.CacheWarmingActionWarm})
		if err != nil {
			t.Error(err)
		}
		done <- action
	}()
	<-entered
	select {
	case <-done:
		t.Fatal("emitter returned before the awaited handler")
	default:
	}
	cancel()
	if action := <-done; action != extension.CacheWarmingActionStop {
		t.Fatalf("action = %q", action)
	}
	if !slices.Equal(order, []string{"first", "last"}) {
		t.Fatalf("order = %v", order)
	}
	if !slices.Equal(reported, []string{"cache_warming_decision:decision failed"}) {
		t.Fatalf("reported = %v", reported)
	}
}

// A typed API.OnCacheWarmingDecision handler (types.ts: ExtensionHandler<CacheWarmingDecisionEvent, CacheWarmingDecisionEventResult>)
// sees the event and its explicit action replaces the default; a handler that sets no action keeps it.
// Pi: packages/coding-agent/src/core/extensions/types.ts:1558 (API.on).
func TestTypedOnCacheWarmingDecisionHandlerOverridesAction(t *testing.T) {
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	var seen extension.CacheWarmingDecisionEvent
	api.OnCacheWarmingDecision(func(_ context.Context, evt extension.CacheWarmingDecisionEvent) (extension.CacheWarmingDecisionEventResult, error) {
		seen = evt
		return extension.CacheWarmingDecisionEventResult{Action: new(extension.CacheWarmingActionStop)}, nil
	})
	api.OnCacheWarmingDecision(func(context.Context, extension.CacheWarmingDecisionEvent) (extension.CacheWarmingDecisionEventResult, error) {
		return extension.CacheWarmingDecisionEventResult{}, nil
	})
	handlers := make([]extension.HandlerFn, 0, len(fake.OnCacheWarmingDecisionHandlers))
	for _, typed := range fake.OnCacheWarmingDecisionHandlers {
		handlers = append(handlers, func(args ...any) (any, error) {
			result, err := typed(context.Background(), args[0].(extension.CacheWarmingDecisionEvent))
			return &result, err
		})
	}
	runner := NewRunner([]extension.Extension{{Path: "typed", Handlers: map[string][]extension.HandlerFn{"cache_warming_decision": handlers}}}, t.TempDir())
	event := extension.CacheWarmingDecisionEvent{Type: "cache_warming_decision", WarmCost: 1, MissCost: 2, ContinuationProbability: 0.5, Action: extension.CacheWarmingActionWarm}
	action, err := runner.EmitCacheWarmingDecision(t.Context(), event)
	if err != nil {
		t.Fatal(err)
	}
	if action != extension.CacheWarmingActionStop {
		t.Fatalf("action = %q, want the typed handler's stop", action)
	}
	if seen != event {
		t.Fatalf("handler saw %+v, want %+v", seen, event)
	}
}
