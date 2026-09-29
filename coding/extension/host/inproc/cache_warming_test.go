package inproc

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
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
