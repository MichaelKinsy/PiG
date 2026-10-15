package codingagent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// A replicated-state listener failure that no onError handles reaches the interactive uncaught handler, as Pi's
// reportErrorAsync rethrow reaches interactive-mode.ts's uncaughtException handler (uncaughtCrash), after the change
// that delivered it returned; removing the handler restores the previous receiver.
func TestChordUncaughtFailuresReachTheInteractiveUncaughtHandler(t *testing.T) {
	failure := errors.New("listener failed")
	outer := make(chan error, 1)
	original := chord.SetUncaughtErrorReporter(func(err error) { outer <- err })
	t.Cleanup(func() { chord.SetUncaughtErrorReporter(original) })
	got := make(chan any, 1)
	restore := SetUncaughtGoroutineHandler(func(value any, _ []byte) { got <- value })
	state := chord.NewReplicatedState(delta.JsonObjectOf("n", 0.0))
	unsubscribe := state.Subscribe(func(value *delta.JsonObject, _ context.Context, delivery chord.ReplicatedStateDelivery) {
		if delivery.Kind == chord.DeliveryUpdate {
			panic(failure)
		}
	})
	defer unsubscribe()
	if err := state.Replace(context.Background(), delta.JsonObjectOf("n", 1.0)); err != nil {
		t.Fatalf("the listener failure reached Replace: %v", err)
	}
	select {
	case value := <-got:
		if !errors.Is(value.(error), failure) {
			t.Fatalf("handler got %v", value)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the uncaught handler never ran")
	}
	restore()
	if err := state.Replace(context.Background(), delta.JsonObjectOf("n", 2.0)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-outer:
		if !errors.Is(err, failure) {
			t.Fatalf("previous receiver got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the previous receiver was not restored")
	}
	select {
	case value := <-got:
		t.Fatalf("handler ran after restore: %v", value)
	default:
	}
}
