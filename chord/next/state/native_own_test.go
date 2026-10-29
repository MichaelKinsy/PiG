package state_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Go tests of NativeSource and FollowedSource on a source the test supplies (failable): the scenarios of
// native-source.test.ts, which upstream runs on registry values and writables of a ChordHost (those tests wait for the
// host, contracts, registries and documents packages).

// failable is a followed source of numbers that can fail and recover, as a registry value whose compose threw.
type failable struct {
	state.FollowedSource[float64]
	value   float64
	hasLast bool
	failure error
	// unfollowedCalls counts Unfollowed hooks.
	unfollowedCalls int
}

func newFailable(batch *state.Batch, value float64) *failable {
	source := &failable{value: value, hasLast: true}
	source.FollowedSource.Init(batch, source)
	return source
}

func (source *failable) Value() float64 { return source.value }

func (source *failable) Start() state.NativeStart[float64] {
	if source.failure != nil {
		return state.NativeStart[float64]{Kind: state.StartFailed, Error: source.failure, HasLast: source.hasLast, Last: source.value}
	}
	return state.NativeStart[float64]{Kind: state.StartValue, Value: source.value}
}

func (source *failable) Current() (float64, error) {
	start := source.Start()
	if start.Kind == state.StartFailed {
		return 0, start.Error
	}
	return start.Value, nil
}

func (source *failable) Unfollowed() { source.unfollowedCalls++ }

func (source *failable) change(t *testing.T, adopt func()) {
	t.Helper()
	if err := source.Batch().Within(func() error {
		adopt()
		source.Changed()
		return nil
	}, background); err != nil {
		t.Fatalf("Within: %v", err)
	}
}

func (source *failable) set(t *testing.T, value float64) {
	t.Helper()
	source.change(t, func() { source.value, source.failure, source.hasLast = value, nil, true })
}

func (source *failable) fail(t *testing.T, err error) {
	t.Helper()
	source.change(t, func() { source.failure = err })
}

func nativeCalls[T any](watch chord.NativeWatchHandle[T]) *nexttest.Collector[chord.NativeWatchCall[T]] {
	calls := &nexttest.Collector[chord.NativeWatchCall[T]]{}
	watch.Start(func(_ context.Context, call chord.NativeWatchCall[T]) error {
		calls.Append(call)
		return nil
	})
	return calls
}

func nativeWatchOf[T any](t *testing.T, source interface {
	Watch(context.Context) (chord.NativeWatchHandle[T], error)
}) chord.NativeWatchHandle[T] {
	t.Helper()
	watch, err := source.Watch(background)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	return watch
}

func requireNative[T any](t *testing.T, got *nexttest.Collector[chord.NativeWatchCall[T]], want ...chord.NativeWatchCall[T]) {
	t.Helper()
	if want == nil {
		want = []chord.NativeWatchCall[T]{}
	}
	if items := got.Items(); !reflect.DeepEqual(items, want) {
		t.Fatalf("calls = %+v, want %+v", items, want)
	}
}

func valueN(v float64) chord.NativeWatchCall[float64] {
	return chord.NativeWatchCall[float64]{Kind: chord.WatchCallValue, Value: v}
}

func failedN(err error) chord.NativeWatchCall[float64] {
	return chord.NativeWatchCall[float64]{Kind: chord.WatchCallFailed, Error: err}
}

// observer is a NativeObserver recording its calls and whether the batch was running for each.
type observer struct {
	batch *state.Batch
	// endedCh is closed by the first End; an abort reaches the observer on a goroutine of its own.
	endedCh  chan struct{}
	calls    []chord.NativeWatchCall[float64]
	inBatch  []bool
	ends     []chord.WatchEnd
	detaches func()
}

func (o *observer) Advance(call chord.NativeWatchCall[float64], _ context.Context) {
	o.calls = append(o.calls, call)
	o.inBatch = append(o.inBatch, o.batch.Running())
	if o.detaches != nil {
		o.detaches()
	}
}

func (o *observer) End(end chord.WatchEnd) {
	o.ends = append(o.ends, end)
	select {
	case <-o.endedCh:
	default:
		close(o.endedCh)
	}
}

func newObserver(batch *state.Batch) *observer {
	return &observer{batch: batch, endedCh: make(chan struct{})}
}

func TestNativeSourceFailing(t *testing.T) {
	t.Run("a watch taken while it is failed opens at its last good value and gets the failure first", func(t *testing.T) {
		h := newHarness(t, nil)
		source := newFailable(h.batch, 1)
		source.fail(t, errors.New("negative: -1"))
		watch := nativeWatchOf[float64](t, source)
		if watch.Value() != 1 {
			t.Fatalf("Value = %v, want 1", watch.Value())
		}
		calls := nativeCalls(watch)
		h.delivered()
		requireNative(t, calls, failedN(source.failure))
	})

	t.Run("a watch on a failed source with no value is refused with the failure", func(t *testing.T) {
		h := newHarness(t, nil)
		source := newFailable(h.batch, 0)
		source.hasLast = false
		source.failure = errThrew
		if _, err := source.Watch(background); err != errThrew {
			t.Fatalf("Watch error = %v, want %v", err, errThrew)
		}
		if source.Watched() {
			t.Fatal("a refused watch follows the source")
		}
	})

	t.Run("gives one failure call per episode and delivers the value after it even when it equals the last one", func(t *testing.T) {
		h := newHarness(t, nil)
		source := newFailable(h.batch, 1)
		calls := nativeCalls(nativeWatchOf[float64](t, source))
		source.fail(t, errThrew)
		h.delivered()
		source.fail(t, errors.New("another failure"))
		h.delivered()
		source.set(t, 1)
		h.delivered()
		source.set(t, 2)
		h.delivered()
		requireNative(t, calls, failedN(errThrew), valueN(1), valueN(2))
	})

	t.Run("an observer started while its source is failed waits for the next good value, never called with the stale one", func(t *testing.T) {
		h := newHarness(t, nil)
		source := newFailable(h.batch, 0)
		source.hasLast = false
		source.failure = errors.New("negative: -1")
		o := newObserver(h.batch)
		if _, err := source.Observe(o, background); err != source.failure {
			t.Fatalf("Observe error = %v, want the failure (no value to start at)", err)
		}
		source.Subscribe(o)
		if !source.Watched() {
			t.Fatal("a subscriber does not count as following")
		}
		source.fail(t, errors.New("still failing"))
		if len(o.calls) != 0 {
			t.Fatalf("a failure reached an observer with no value: %+v", o.calls)
		}
		source.set(t, 4)
		if want := []chord.NativeWatchCall[float64]{valueN(4)}; !reflect.DeepEqual(o.calls, want) {
			t.Fatalf("calls = %+v, want %+v", o.calls, want)
		}
	})
}

func TestFollowedSourceFrames(t *testing.T) {
	t.Run("delivers a value only when it is another value than the followers have, one frame per batch", func(t *testing.T) {
		h := newHarness(t, nil)
		source := newFailable(h.batch, 1)
		calls := nativeCalls(nativeWatchOf[float64](t, source))
		source.set(t, 1)
		h.delivered()
		release := h.scheduler.Hold()
		if err := h.batch.Commit(func() error {
			source.change(t, func() { source.value = 5 })
			source.change(t, func() { source.value = 6 })
			return nil
		}, background); err != nil {
			t.Fatal(err)
		}
		release()
		h.delivered()
		requireNative(t, calls, valueN(6))
		if source.Value() != 6 {
			t.Fatalf("Value = %v", source.Value())
		}
	})

	t.Run("is not listed for a frame while nobody follows it, and keeps nothing for followers after the last leaves", func(t *testing.T) {
		h := newHarness(t, nil)
		source := newFailable(h.batch, 1)
		source.set(t, 2)
		watch := nativeWatchOf[float64](t, source)
		if _, err := watch.Stop(); err != nil {
			t.Fatal(err)
		}
		if source.unfollowedCalls != 1 {
			t.Fatalf("Unfollowed calls = %d, want 1", source.unfollowedCalls)
		}
		if source.Watched() {
			t.Fatal("a stopped watch follows the source")
		}
	})

	t.Run("refuses a watch inside a commit or a compute", func(t *testing.T) {
		h := newHarness(t, nil)
		source := newFailable(h.batch, 1)
		_ = h.batch.Compute(func() error {
			_, err := source.Watch(background)
			requireReentrant(t, "watch", err)
			_, err = source.Observe(newObserver(h.batch), background)
			requireReentrant(t, "observe", err)
			return nil
		})
	})
}

func TestFollowedSourceObservers(t *testing.T) {
	t.Run("an observer is called inside the batch that changed the source, with the value, and counts as following", func(t *testing.T) {
		h := newHarness(t, nil)
		source := newFailable(h.batch, 1)
		o := newObserver(h.batch)
		observation, err := source.Observe(o, background)
		if err != nil || observation.Value != 1 {
			t.Fatalf("Observe = %+v, %v", observation, err)
		}
		if !source.Watched() {
			t.Fatal("an observer does not count as following")
		}
		source.set(t, 2)
		source.fail(t, errThrew)
		if want := []chord.NativeWatchCall[float64]{valueN(2), failedN(errThrew)}; !reflect.DeepEqual(o.calls, want) {
			t.Fatalf("calls = %+v, want %+v", o.calls, want)
		}
		if !reflect.DeepEqual(o.inBatch, []bool{true, true}) {
			t.Fatalf("batch running during the calls = %v", o.inBatch)
		}
		observation.Detach()
		if source.Watched() || source.unfollowedCalls != 1 {
			t.Fatalf("a detached observer still follows: %v, %d", source.Watched(), source.unfollowedCalls)
		}
		source.set(t, 3)
		if len(o.calls) != 2 || len(o.ends) != 0 {
			t.Fatalf("a detached observer was called: %+v, ends %+v", o.calls, o.ends)
		}
	})

	t.Run("aborting the context detaches an observer (aborted), and a detached one's abort listener is gone", func(t *testing.T) {
		h := newHarness(t, nil)
		source := newFailable(h.batch, 1)
		o := newObserver(h.batch)
		cancellable, cancel := chordctx.WithCancel(background)
		if _, err := source.Observe(o, cancellable); err != nil {
			t.Fatal(err)
		}
		cancel(nil)
		// The abort is observed on a goroutine of its own, as AbortSignal's event: wait for it.
		<-o.endedCh
		if len(o.ends) != 1 || o.ends[0].Kind != chord.WatchAborted {
			t.Fatalf("ends = %+v, want one aborted", o.ends)
		}
		if source.Watched() {
			t.Fatal("an aborted observer still follows")
		}
	})

	t.Run("an observer taken with an aborted context ends aborted at once", func(t *testing.T) {
		h := newHarness(t, nil)
		source := newFailable(h.batch, 1)
		o := newObserver(h.batch)
		cancellable, cancel := chordctx.WithCancel(background)
		cancel(nil)
		observation, err := source.Observe(o, cancellable)
		if err != nil || observation.Value != 1 {
			t.Fatalf("Observe = %+v, %v", observation, err)
		}
		if len(o.ends) != 1 || o.ends[0].Kind != chord.WatchAborted || source.Watched() {
			t.Fatalf("ends = %+v, watched %v", o.ends, source.Watched())
		}
		observation.Detach()
	})

	t.Run("an observer may detach itself in its call", func(t *testing.T) {
		h := newHarness(t, nil)
		source := newFailable(h.batch, 1)
		o := newObserver(h.batch)
		other := newObserver(h.batch)
		observation, err := source.Observe(o, background)
		if err != nil {
			t.Fatal(err)
		}
		o.detaches = observation.Detach
		source.Subscribe(other)
		source.set(t, 2)
		if len(o.calls) != 1 || len(other.calls) != 1 {
			t.Fatalf("calls: %d and %d, want 1 and 1", len(o.calls), len(other.calls))
		}
		source.set(t, 3)
		if len(o.calls) != 1 || len(other.calls) != 2 {
			t.Fatalf("calls after detach: %d and %d, want 1 and 2", len(o.calls), len(other.calls))
		}
	})
}

func TestFollowedSourceEnd(t *testing.T) {
	t.Run("ending the source ends its watches and observers ended and lets go of its batch", func(t *testing.T) {
		h := newHarness(t, nil)
		source := newFailable(h.batch, 1)
		watch := nativeWatchOf[float64](t, source)
		o := newObserver(h.batch)
		cancellable, cancel := chordctx.WithCancel(background)
		defer cancel(nil)
		if _, err := source.Observe(o, cancellable); err != nil {
			t.Fatal(err)
		}
		source.End()
		<-watch.Closed()
		if end := watch.End(); end.Kind != chord.WatchEnded {
			t.Fatalf("watch end = %+v, want ended", end)
		}
		if len(o.ends) != 1 || o.ends[0].Kind != chord.WatchEnded {
			t.Fatalf("observer ends = %+v", o.ends)
		}
		if source.Batch() != nil {
			t.Fatal("an ended source keeps its batch")
		}
		if source.DependentSet().Len() != 0 {
			t.Fatal("an ended source keeps dependents")
		}
		// An ended source gives a watch that has ended, and an observer that ends at once.
		late := nativeWatchOf[float64](t, source)
		<-late.Closed()
		if end := late.End(); end.Kind != chord.WatchEnded || late.Value() != 1 {
			t.Fatalf("late watch = %+v at %v", end, late.Value())
		}
		o2 := newObserver(h.batch)
		observation, err := source.Observe(o2, background)
		if err != nil || observation.Value != 1 || len(o2.ends) != 1 || o2.ends[0].Kind != chord.WatchEnded {
			t.Fatalf("late observer: %+v, %v, ends %+v", observation, err, o2.ends)
		}
	})
}

func TestNativeSourcesAsDeriveSources(t *testing.T) {
	t.Run("a derive reads JSON and native sources alike, marked when a native one changes", func(t *testing.T) {
		h := newHarness(t, nil)
		count := newFailable(h.batch, 2)
		doc := newDoc(h.batch, num(0))
		label := derived(h, []any{doc, count}, func(v []any) (delta.JsonValue, error) {
			return v[0].(*delta.JsonObject).Value("n").(float64) + v[1].(float64), nil
		})
		if got := mustValue(t, label.derive); got != float64(2) {
			t.Fatalf("label = %v, want 2", got)
		}
		count.set(t, 5)
		if got := mustValue(t, label.derive); got != float64(5) {
			t.Fatalf("label = %v, want 5", got)
		}
		mustSet(t, doc, num(1))
		if got := mustValue(t, label.derive); got != float64(6) {
			t.Fatalf("label = %v, want 6", got)
		}
	})

	t.Run("a derive over a failed native source keeps its last good value, which a contribution's current refuses", func(t *testing.T) {
		h := newHarness(t, nil)
		count := newFailable(h.batch, 3)
		label := derived(h, []any{count}, func(v []any) (delta.JsonValue, error) { return v[0].(float64) * 2, nil })
		count.fail(t, errThrew)
		if got := mustValue(t, label.derive); got != float64(6) {
			t.Fatalf("label = %v, want 6", got)
		}
		if _, err := count.CurrentAny(); err != errThrew {
			t.Fatalf("CurrentAny error = %v, want %v", err, errThrew)
		}
		var handle state.NativeSourceHandle = count
		if got := handle.DependentSet().Len(); got != 1 {
			t.Fatalf("dependents = %d, want 1", got)
		}
	})

	t.Run("a watched derive over a native source recomputes in the batch that changed it", func(t *testing.T) {
		h := newHarness(t, nil)
		count := newFailable(h.batch, 1)
		double := derived(h, []any{count}, func(v []any) (delta.JsonValue, error) { return v[0].(float64) * 2, nil })
		frames := nexttest.FramesOf(watchOf(t, double.derive))
		count.set(t, 4)
		h.delivered()
		want := []nexttest.Frame[delta.JsonValue]{{Value: float64(8), Ops: ops(op("r", float64(8)))}}
		if got := frames.Items(); !reflect.DeepEqual(got, want) {
			t.Fatalf("frames = %+v, want %+v", got, want)
		}
	})
}

func TestNativeSourceRelease(t *testing.T) {
	t.Run("a source nobody references is collected; a kept one after its end keeps no batch", func(t *testing.T) {
		h := newHarness(t, nil)
		ref := func() weak.Pointer[failable] {
			source := newFailable(h.batch, 1)
			source.set(t, 2)
			return weak.Make(source)
		}()
		nexttest.ExpectCollected(t, ref)
	})
}
