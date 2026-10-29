package state_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Go tests of Source, its watches and subscribers on a document the test supplies: the scenarios of watchable.test.ts
// "watches" and "subscribers", which upstream runs on a ChordHost with a defineState counter (those tests wait for the
// host, contracts and documents packages).

func setOps(n float64) []delta.Op { return ops(op("s", delta.Path{"n"}, n)) }

func TestSourceWatches(t *testing.T) {
	t.Run("start from the value at acquisition; a listener that keeps up gets each commit", func(t *testing.T) {
		h := newHarness(t, nil)
		doc := newDoc(h.batch, num(0))
		watch := watchOf(t, doc)
		if !reflect.DeepEqual(watch.Value(), any(num(0))) {
			t.Fatalf("Value = %v, want {n:0}", watch.Value())
		}
		frames := nexttest.FramesOf(watch)
		mustSet(t, doc, num(1))
		h.delivered()
		mustSet(t, doc, num(2))
		h.delivered()
		want := []nexttest.Frame[delta.JsonValue]{{Value: num(1), Ops: setOps(1)}, {Value: num(2), Ops: setOps(2)}}
		if got := frames.Items(); !reflect.DeepEqual(got, want) {
			t.Fatalf("frames = %+v, want %+v", got, want)
		}
	})

	t.Run("give a busy listener one call with the newest value and the folded ops", func(t *testing.T) {
		h := newHarness(t, nil)
		doc := newDoc(h.batch, num(0))
		watch := watchOf(t, doc)
		var frames nexttest.Collector[nexttest.Frame[delta.JsonValue]]
		release := make(chan struct{})
		watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
			if call.Kind != chord.WatchCallValue {
				return nil
			}
			// The first call stays busy until the test lets it return.
			if frames.Append(nexttest.Frame[delta.JsonValue]{Value: call.Value, Ops: call.Ops}) == 1 {
				<-release
			}
			return nil
		})
		mustSet(t, doc, num(1))
		if err := h.scheduler.AwaitEntered(background); err != nil {
			t.Fatal(err)
		}
		mustSet(t, doc, num(2))
		mustSet(t, doc, num(3))
		close(release)
		h.delivered()
		// Two commits while busy: one call, the second `s` on the same path replaced the first.
		want := []nexttest.Frame[delta.JsonValue]{{Value: num(1), Ops: setOps(1)}, {Value: num(3), Ops: setOps(3)}}
		if got := frames.Items(); !reflect.DeepEqual(got, want) {
			t.Fatalf("frames = %+v, want %+v", got, want)
		}
	})

	t.Run("end aborted when their context aborts", func(t *testing.T) {
		h := newHarness(t, nil)
		doc := newDoc(h.batch, num(0))
		cancellable, cancel := chordctx.WithCancel(background)
		watch, err := doc.Watch(cancellable)
		if err != nil {
			t.Fatal(err)
		}
		cancel(nil)
		<-watch.Closed()
		if end := watch.End(); end != (chord.WatchEnd{Kind: chord.WatchAborted}) {
			t.Fatalf("end = %+v, want aborted", end)
		}
		if doc.Watched() {
			t.Fatal("the aborted watch still counts as watching")
		}
	})

	t.Run("end with listener_error when the listener fails, reported to the batch", func(t *testing.T) {
		h := newHarness(t, nil)
		doc := newDoc(h.batch, num(0))
		watch := watchOf(t, doc)
		boom := errors.New("listener threw")
		watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error { return boom })
		mustSet(t, doc, num(1))
		<-watch.Closed()
		if end := watch.End(); end != (chord.WatchEnd{Kind: chord.WatchListenerError, Error: boom}) {
			t.Fatalf("end = %+v, want listener_error", end)
		}
		h.delivered()
		if got := h.errors.all(); len(got) != 1 || got[0].Kind != state.BatchListenerError || got[0].Error != boom {
			t.Fatalf("reported = %+v, want the listener's error", got)
		}
	})

	t.Run("a stopped watch is dropped from the source", func(t *testing.T) {
		h := newHarness(t, nil)
		doc := newDoc(h.batch, num(0))
		watch := watchOf(t, doc)
		if !doc.Watched() {
			t.Fatal("an open watch does not count as watching")
		}
		if end, err := watch.Stop(); err != nil || end.Kind != chord.WatchStopped {
			t.Fatalf("Stop = %+v, %v", end, err)
		}
		if doc.Watched() {
			t.Fatal("a stopped watch still counts as watching")
		}
	})

	t.Run("refuse a watch inside a commit or a compute (invalid/reentrant)", func(t *testing.T) {
		h := newHarness(t, nil)
		doc := newDoc(h.batch, num(0))
		_ = h.batch.Compute(func() error {
			_, err := doc.Watch(background)
			requireReentrant(t, "watch in a compute", err)
			return nil
		})
		_ = h.batch.Commit(func() error {
			_, err := doc.OpenWatch(background)
			requireReentrant(t, "openWatch in a commit", err)
			return nil
		}, background)
	})
}

func TestSourceEnds(t *testing.T) {
	t.Run("ending the watches ends every watch and subscriber ended", func(t *testing.T) {
		h := newHarness(t, nil)
		doc := newDoc(h.batch, num(0))
		watch := watchOf(t, doc)
		sub := &recorder{}
		doc.Subscribe(sub)
		doc.EndWatches()
		<-watch.Closed()
		if end := watch.End(); end.Kind != chord.WatchEnded {
			t.Fatalf("end = %+v, want ended", end)
		}
		if len(sub.ends) != 1 || sub.ends[0].Kind != chord.WatchEnded {
			t.Fatalf("subscriber ends = %+v", sub.ends)
		}
		if doc.Watched() {
			t.Fatal("an ended source is still watched")
		}
	})

	t.Run("ending after delivery lets a watch deliver what it was given first", func(t *testing.T) {
		h := newHarness(t, nil)
		doc := newDoc(h.batch, num(0))
		watch := watchOf(t, doc)
		sub := &recorder{}
		doc.Subscribe(sub)
		var frames nexttest.Collector[nexttest.Frame[delta.JsonValue]]
		watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
			frames.Append(nexttest.Frame[delta.JsonValue]{Value: call.Value, Ops: call.Ops})
			return nil
		})
		mustSet(t, doc, num(1))
		doc.EndWatchesAfterDelivery()
		<-watch.Closed()
		if end := watch.End(); end.Kind != chord.WatchEnded {
			t.Fatalf("end = %+v, want ended", end)
		}
		if got, want := frames.Items(), []nexttest.Frame[delta.JsonValue]{{Value: num(1), Ops: setOps(1)}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("frames = %+v, want %+v", got, want)
		}
		if sub.afterDelivery != 1 {
			t.Fatalf("subscriber endAfterDelivery calls = %d, want 1", sub.afterDelivery)
		}
	})
}

// recorder is a Subscriber that records what it is fed.
type recorder struct {
	calls         []chord.WatchCall[delta.JsonValue]
	ends          []chord.WatchEnd
	afterDelivery int
}

func (r *recorder) Advance(call chord.WatchCall[delta.JsonValue], _ context.Context) {
	r.calls = append(r.calls, call)
}
func (r *recorder) End(end chord.WatchEnd) { r.ends = append(r.ends, end) }
func (r *recorder) EndAfterDelivery()      { r.afterDelivery++ }

func TestSourceSubscribers(t *testing.T) {
	t.Run("get each batch's frame synchronously, inside the commit, and count as watching", func(t *testing.T) {
		h := newHarness(t, nil)
		doc := newDoc(h.batch, num(0))
		sub := &recorder{}
		doc.Subscribe(sub)
		if !doc.Watched() {
			t.Fatal("a subscriber does not count as watching")
		}
		mustSet(t, doc, num(1))
		want := []chord.WatchCall[delta.JsonValue]{{Kind: chord.WatchCallValue, Value: num(1), Ops: setOps(1)}}
		if !reflect.DeepEqual(sub.calls, want) {
			t.Fatalf("calls = %+v, want %+v", sub.calls, want)
		}
		doc.Unsubscribe(sub)
		if doc.Watched() {
			t.Fatal("an unsubscribed source is still watched")
		}
		mustSet(t, doc, num(2))
		if len(sub.calls) != 1 {
			t.Fatalf("calls after unsubscribe = %d, want 1", len(sub.calls))
		}
	})
}

// flaky is a source that can fail and recover: it tells its start.
type flaky struct {
	state.Source
	start state.WatchStart
}

func newFlaky(batch *state.Batch, start state.WatchStart) *flaky {
	source := &flaky{start: start}
	source.Init(batch, source)
	return source
}

func (source *flaky) Value() (delta.JsonValue, error) {
	if source.start.Kind == state.StartFailed {
		if !source.start.HasLast {
			return nil, source.start.Error
		}
		return source.start.Last, nil
	}
	return source.start.Value, nil
}

func (source *flaky) Start() state.WatchStart { return source.start }

func (source *flaky) Deliver(context.Context) error { return nil }

func TestSourceFailedStart(t *testing.T) {
	t.Run("a watch taken while it is failed opens at its last good value and gets the failure first", func(t *testing.T) {
		h := newHarness(t, nil)
		source := newFlaky(h.batch, state.WatchStart{Kind: state.StartFailed, Error: errThrew, HasLast: true, Last: float64(5)})
		watch := watchOf(t, source)
		if watch.Value() != float64(5) {
			t.Fatalf("Value = %v, want 5", watch.Value())
		}
		calls := nexttest.ChangesOf(watch)
		h.delivered()
		requireCalls(t, "watch", calls, failedCall(errThrew))
	})

	t.Run("with no value to start at, the watch is refused with the failure", func(t *testing.T) {
		h := newHarness(t, nil)
		source := newFlaky(h.batch, state.WatchStart{Kind: state.StartFailed, Error: errThrew})
		if _, err := source.Watch(background); err != errThrew {
			t.Fatalf("Watch error = %v, want %v", err, errThrew)
		}
		if source.Watched() {
			t.Fatal("a refused watch counts as watching")
		}
	})
}

func TestConstantWatch(t *testing.T) {
	t.Run("a watch ends aborted when its context aborts, and a constant is no dependency", func(t *testing.T) {
		constant := state.NewConstant(float64(1))
		if constant.DependentSet() != nil {
			t.Fatal("a constant has dependents")
		}
		cancellable, cancel := chordctx.WithCancel(background)
		watch, err := constant.Watch(cancellable)
		if err != nil {
			t.Fatal(err)
		}
		cancel(nil)
		<-watch.Closed()
		if end := watch.End(); end.Kind != chord.WatchAborted {
			t.Fatalf("end = %+v, want aborted", end)
		}
	})
}

func TestConstantOf(t *testing.T) {
	t.Run("a typed constant holds its value as given and its watch starts at it", func(t *testing.T) {
		start := state.ConstantOf(delta.JsonObjectOf("id", "S1"))
		value := start.Value()
		watch, err := start.Watch(background)
		if err != nil {
			t.Fatal(err)
		}
		if watch.Value() != value {
			t.Fatal("the watch does not start at the constant's value")
		}
		if end, err := watch.Stop(); err != nil || end.Kind != chord.WatchStopped {
			t.Fatalf("Stop = %+v, %v", end, err)
		}
	})
}
