package session

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
)

// Upstream observation.ts SessionSourceAttachment.publish drains from a microtask inside try/catch and disposes the
// attachment on an unexpected listener failure; #drain resets #delivering in a finally.
func TestSessionSourceAttachmentIsolatesListenerFailure(t *testing.T) {
	scheduler := newPending()
	released := 0
	source := newCommittedStateSource[durable.JsonObject](durable.JsonObject{"v": 0.0}, func() { released++ }, scheduler)
	failing, err := source.Attach()
	if err != nil {
		t.Fatal(err)
	}
	healthy, err := source.Attach()
	if err != nil {
		t.Fatal(err)
	}
	var delivered []int
	if err := failing.Activate(func(chord.ReplicatedStateSourceFrame[durable.JsonObject]) { panic("listener failed") }); err != nil {
		t.Fatal(err)
	}
	if err := healthy.Activate(func(frame chord.ReplicatedStateSourceFrame[durable.JsonObject]) {
		delivered = append(delivered, frame.Cursor)
	}); err != nil {
		t.Fatal(err)
	}
	source.Advance(context.Background(), durable.JsonObject{"v": 1.0}, []durable.Op{{"s", []any{"v"}, 1.0}})
	scheduler.wait()
	if !failing.(*sessionSourceAttachment[durable.JsonObject]).disposed {
		t.Fatal("a failing listener disposes its attachment")
	}
	source.Advance(context.Background(), durable.JsonObject{"v": 2.0}, []durable.Op{{"s", []any{"v"}, 2.0}})
	scheduler.wait()
	if len(delivered) != 2 || delivered[0] != 1 || delivered[1] != 2 {
		t.Fatalf("the healthy attachment keeps receiving frames: %v", delivered)
	}
	if released != 0 {
		t.Fatal("the source stays attached while one attachment remains")
	}
	healthy.Dispose()
	if released != 1 {
		t.Fatal("the last disposal releases the source")
	}
}

// A synchronous activation drain that panics propagates, as upstream's activate() throws, and leaves the attachment
// able to deliver later frames because delivering is reset.
func TestSessionSourceAttachmentResetsDeliveringAfterPanic(t *testing.T) {
	scheduler := newPending()
	source := newCommittedStateSource[durable.JsonObject](durable.JsonObject{}, func() {}, scheduler)
	attachment, err := source.Attach()
	if err != nil {
		t.Fatal(err)
	}
	source.Advance(context.Background(), durable.JsonObject{"v": 1.0}, nil)
	calls := 0
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the activation drain propagates the listener panic")
			}
		}()
		_ = attachment.Activate(func(chord.ReplicatedStateSourceFrame[durable.JsonObject]) {
			calls++
			if calls == 1 {
				panic("first frame failed")
			}
		})
	}()
	source.Advance(context.Background(), durable.JsonObject{"v": 2.0}, nil)
	scheduler.wait()
	if calls != 2 {
		t.Fatalf("calls %d, want 2", calls)
	}
}

// Upstream CommittedWatch.advance overflows to `this.#replace?.() ?? value`: a replacement source that yields null
// falls back to the newest value.
func TestCommittedWatchOverflowReplacementFallsBackToValue(t *testing.T) {
	scheduler := newPending()
	watch := newCommittedWatch[durable.JsonObject](durable.JsonObject{}, func() {}, func() durable.JsonObject { return nil }, scheduler)
	for index := range maxPendingWatchFrames + 1 {
		watch.Advance(context.Background(), durable.JsonObject{"v": float64(index)}, nil)
	}
	if len(watch.pending) != 1 {
		t.Fatalf("pending %d", len(watch.pending))
	}
	frame := watch.pending[0]
	if frame.value == nil || frame.value["v"] != float64(maxPendingWatchFrames) {
		t.Fatalf("the overflow frame is the newest value: %#v", frame.value)
	}
	if replaced, ok := frame.ops[0][1].(durable.JsonObject); !ok || replaced["v"] != float64(maxPendingWatchFrames) {
		t.Fatalf("the overflow op replaces the root with the newest value: %#v", frame.ops)
	}
	_, _ = watch.Stop()
}
