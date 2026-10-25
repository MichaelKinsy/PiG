package session

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// Upstream CommittedWatch.advance overflows to `this.#replace?.() ?? value`: a replacement source that yields null
// falls back to the newest value.
func TestCommittedWatchOverflowReplacementFallsBackToValue(t *testing.T) {
	scheduler := newPending()
	watch := newCommittedWatch[durable.JsonObject](delta.NewJsonObject(0), func() {}, func() durable.JsonObject { return nil }, scheduler)
	for index := range maxPendingWatchFrames + 1 {
		watch.Advance(context.Background(), delta.JsonObjectOf("v", float64(index)), nil)
	}
	if len(watch.pending) != 1 {
		t.Fatalf("pending %d", len(watch.pending))
	}
	frame := watch.pending[0]
	if frame.value == nil || frame.value.Value("v") != float64(maxPendingWatchFrames) {
		t.Fatalf("the overflow frame is the newest value: %#v", frame.value)
	}
	if replaced, ok := frame.ops[0][1].(durable.JsonObject); !ok || replaced.Value("v") != float64(maxPendingWatchFrames) {
		t.Fatalf("the overflow op replaces the root with the newest value: %#v", frame.ops)
	}
	_, _ = watch.Stop()
}

// session-watches.test.ts "ends retired only on its source's retirement, never on a null value": a frame whose value is
// null is an ordinary frame; only the source's explicit retirement ends the watch retired.
func TestCommittedWatchEndsRetiredOnlyOnItsSourcesRetirement(t *testing.T) {
	scheduler := newPending()
	watch := newCommittedWatch[any](1.0, func() {}, nil, scheduler)
	watch.report = func(error) {}
	type frame struct {
		value any
		ops   []durable.Op
	}
	var frames []frame
	watch.Start(func(_ context.Context, value any, ops []durable.Op) error {
		frames = append(frames, frame{value, ops})
		return nil
	})
	watch.Advance(context.Background(), nil, []durable.Op{{"r", nil}})
	watch.Advance(context.Background(), 2.0, []durable.Op{{"r", 2.0}})
	scheduler.wait()
	want := []frame{{nil, []durable.Op{{"r", nil}}}, {2.0, []durable.Op{{"r", 2.0}}}}
	if len(frames) != len(want) || frames[0].value != nil || frames[1].value != 2.0 {
		t.Fatalf("frames %#v, want %#v", frames, want)
	}
	select {
	case <-watch.Closed():
		t.Fatalf("a null value ended the watch %+v", watch.End())
	default:
	}
	if end, _ := watch.Stop(); end.Reason != durable.WatchStopped {
		t.Fatalf("end %+v, want stopped", end)
	}
}

// A retirement delivers [["r", null]] with the zero value as the watch's last frame, then ends it retired.
func TestCommittedWatchRetireDeliversTheLastFrameThenEndsRetired(t *testing.T) {
	scheduler := newPending()
	released := 0
	watch := newCommittedWatch[durable.JsonObject](delta.JsonObjectOf("v", 0.0), func() { released++ }, nil, scheduler)
	var values []durable.JsonObject
	var ops [][]durable.Op
	watch.Start(func(_ context.Context, value durable.JsonObject, frameOps []durable.Op) error {
		values = append(values, value)
		ops = append(ops, frameOps)
		return nil
	})
	watch.Retire(context.Background())
	// Neither a frame nor a second retirement follows the retirement.
	watch.Advance(context.Background(), delta.JsonObjectOf("v", 1.0), []durable.Op{{"r", 1.0}})
	watch.Retire(context.Background())
	scheduler.wait()
	<-watch.Closed()
	if len(values) != 1 || values[0] != nil || len(ops[0]) != 1 || ops[0][0][0] != "r" || ops[0][0][1] != nil {
		t.Fatalf("frames %#v %#v, want one [[r null]] frame with a nil value", values, ops)
	}
	if end := watch.End(); end.Reason != durable.WatchRetired || released != 1 {
		t.Fatalf("end %+v released %d, want retired once detached", end, released)
	}
	if watch.Value() != nil {
		t.Fatalf("value %#v, want the retirement's nil", watch.Value())
	}
}

// session-watches.test.ts "retains neither its Session nor its listener once stopped, while still referenced": an ended
// watch drops every reference into its source. Upstream observes it through garbage collection; the Go form checks the
// references themselves.
func TestCommittedWatchDropsItsReferencesOnceEnded(t *testing.T) {
	scheduler := newPending()
	watch := newCommittedWatch[durable.JsonObject](delta.JsonObjectOf("v", 0.0), func() {}, func() durable.JsonObject { return nil }, scheduler)
	watch.report = func(error) {}
	watch.Start(func(context.Context, durable.JsonObject, []durable.Op) error { return nil })
	watch.Advance(context.Background(), delta.JsonObjectOf("v", 1.0), []durable.Op{{"s", "/v", 1.0}})
	scheduler.wait()
	if end, _ := watch.Stop(); end.Reason != durable.WatchStopped {
		t.Fatalf("end %+v, want stopped", end)
	}
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.detach != nil || watch.report != nil || watch.replace != nil || watch.listener != nil || watch.scheduler != nil || watch.pending != nil {
		t.Fatalf("an ended watch holds its source: %+v", watch)
	}
	if watch.value == nil || watch.value.Value("v") != 1.0 {
		t.Fatalf("what it delivered last stays readable, got %#v", watch.value)
	}
}
