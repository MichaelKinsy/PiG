package session

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// TestCommittedWatchTakesNoFrameAfterARetiredValue ports observation.ts advance()/retire() (`if (this.#end !== undefined || this.#retired)
// return;`): once the watch is retired, a later advance is dropped even while the retirement frame still waits for delivery, and the watch
// ends with reason "retired" after delivering it.
func TestCommittedWatchTakesNoFrameAfterARetiredValue(t *testing.T) {
	scheduler := newPending()
	watch := newCommittedWatch[durable.JsonObject](delta.NewJsonObject(0), func() {}, nil, scheduler)
	ctx := context.Background()
	watch.Advance(ctx, delta.JsonObjectOf("v", 1.0), nil)
	watch.Retire(ctx)
	watch.Advance(ctx, delta.JsonObjectOf("v", 2.0), nil)
	if len(watch.pending) != 2 {
		t.Fatalf("pending %d frames, want the value and the retirement only", len(watch.pending))
	}
	var delivered []bool
	watch.Start(func(_ context.Context, value durable.JsonObject, _ []durable.Op) error {
		delivered = append(delivered, value != nil)
		return nil
	})
	scheduler.wait()
	<-watch.Closed()
	if end := watch.End(); end.Reason != durable.WatchRetired {
		t.Fatalf("end = %+v, want retired", end)
	}
	if len(delivered) != 2 || !delivered[0] || delivered[1] {
		t.Fatalf("delivered %v, want the value then the retirement", delivered)
	}
}

// TestCommittedWatchRejectsASecondCancellationObserver ports observation.ts:205: installing a cancellation signal twice throws
// "Watch cancellation is already installed", and the first signal still cancels the watch.
func TestCommittedWatchRejectsASecondCancellationObserver(t *testing.T) {
	watch := newCommittedWatch[durable.JsonObject](delta.NewJsonObject(0), func() {}, nil, newPending())
	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	watch.ObserveCancellation(first)
	func() {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Error("a second cancellation observer was accepted")
			} else if err, ok := recovered.(error); !ok || err.Error() != "Watch cancellation is already installed" {
				t.Errorf("panic %v, want the already-installed error", recovered)
			}
		}()
		watch.ObserveCancellation(context.Background())
	}()
	cancelFirst()
	<-watch.Closed()
	if end := watch.End(); end.Reason != durable.WatchCancelled {
		t.Fatalf("end = %+v, want cancelled", end)
	}
}
