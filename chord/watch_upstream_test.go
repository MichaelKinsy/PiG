package chord

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// Ports packages/chord/test/watch.test.ts: the shared lifecycle through Watch, and what NativeWatch does differently.
// Upstream's flush() (a macrotask that lets the microtask queue drain) is DeliveryScheduler.Wait here.

type watchFrameSeen struct {
	value any
	ops   []delta.Op
}

type watchRecord struct {
	mu       sync.Mutex
	detached int
	reported []error
}

func (r *watchRecord) detach() { r.mu.Lock(); r.detached++; r.mu.Unlock() }
func (r *watchRecord) report(err error) {
	r.mu.Lock()
	r.reported = append(r.reported, err)
	r.mu.Unlock()
}
func (r *watchRecord) detachedCount() int { r.mu.Lock(); defer r.mu.Unlock(); return r.detached }

// recordedWatch is a watch whose detach and reports are recorded.
func recordedWatch(value any, replace func() any) (*Watch[any], *DeliveryScheduler, *watchRecord) {
	scheduler := NewDeliveryScheduler()
	record := &watchRecord{}
	return NewWatch(value, record.detach, record.report, replace).On(scheduler), scheduler, record
}

// collectListener returns a listener that appends each frame to the returned slice, read after scheduler.Wait.
func collectListener() (func(context.Context, any, []delta.Op) error, func() []watchFrameSeen) {
	var mu sync.Mutex
	var frames []watchFrameSeen
	return func(_ context.Context, value any, ops []delta.Op) error {
			mu.Lock()
			frames = append(frames, watchFrameSeen{value, ops})
			mu.Unlock()
			return nil
		}, func() []watchFrameSeen {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(frames)
		}
}

func rootOps(value any) []delta.Op { return []delta.Op{{"r", value}} }

func expectFrames(t *testing.T, got []watchFrameSeen, want ...watchFrameSeen) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("frames %#v, want %#v", got, want)
	}
	for i := range want {
		if !equalJSON(got[i].value, want[i].value) || !equalJSON(got[i].ops, want[i].ops) {
			t.Fatalf("frame %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func equalJSON(a, b any) bool { return deepEqualAny(a, b) }

func deepEqualAny(a, b any) bool {
	switch x := a.(type) {
	case []delta.Op:
		y, ok := b.([]delta.Op)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !deepEqualAny([]any(x[i]), []any(y[i])) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !deepEqualAny(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			if !deepEqualAny(v, y[k]) {
				return false
			}
		}
		return true
	}
	return a == b
}

func mustEnd(t *testing.T, watch interface{ Closed() <-chan struct{} }, end func() WatchEnd, reason WatchEndReason) WatchEnd {
	t.Helper()
	<-watch.Closed()
	got := end()
	if got.Reason != reason {
		t.Fatalf("end = %+v, want %s", got, reason)
	}
	return got
}

type watchContextKey struct{}

func TestWatchKeepsTheAcquisitionValueUntilStartAndDeliversExactFramesInOrder(t *testing.T) {
	watch, scheduler, _ := recordedWatch(map[string]any{"n": 0.0}, nil)
	ctx := context.Background()
	watch.Advance(ctx, map[string]any{"n": 1.0}, []delta.Op{{"s", []any{"n"}, 1.0}})
	watch.Advance(ctx, map[string]any{"n": 2.0}, []delta.Op{{"s", []any{"n"}, 2.0}})
	scheduler.Wait()
	if !deepEqualAny(watch.Value(), map[string]any{"n": 0.0}) {
		t.Fatalf("value before start = %v", watch.Value())
	}
	listener, frames := collectListener()
	watch.Start(listener)
	scheduler.Wait()
	expectFrames(t, frames(),
		watchFrameSeen{map[string]any{"n": 1.0}, []delta.Op{{"s", []any{"n"}, 1.0}}},
		watchFrameSeen{map[string]any{"n": 2.0}, []delta.Op{{"s", []any{"n"}, 2.0}}})
	if !deepEqualAny(watch.Value(), map[string]any{"n": 2.0}) {
		t.Fatalf("value after delivery = %v", watch.Value())
	}
}

func TestWatchNeverInvokesTheListenerInline(t *testing.T) {
	watch, scheduler, _ := recordedWatch(0.0, nil)
	var deliveredOn atomic.Uint64
	watch.Start(func(context.Context, any, []delta.Op) error {
		deliveredOn.Store(goroutineid.Current())
		return nil
	})
	watch.Advance(context.Background(), 1.0, rootOps(1.0))
	scheduler.Wait()
	if got := deliveredOn.Load(); got == 0 || got == goroutineid.Current() {
		t.Fatalf("the listener ran on goroutine %d, the advancing one is %d", got, goroutineid.Current())
	}
}

func TestWatchAwaitsEachCallbackAndBuffersFramesAdvancedWhileOneIsInFlight(t *testing.T) {
	watch, scheduler, _ := recordedWatch(0.0, nil)
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	var mu sync.Mutex
	var calls []string
	record := func(entry string) { mu.Lock(); calls = append(calls, entry); mu.Unlock() }
	watch.Start(func(_ context.Context, value any, _ []delta.Op) error {
		record("start " + string(rune('0'+int(value.(float64)))))
		if value == 1.0 {
			started <- struct{}{}
			<-gate
		}
		record("end " + string(rune('0'+int(value.(float64)))))
		return nil
	})
	ctx := context.Background()
	watch.Advance(ctx, 1.0, rootOps(1.0))
	<-started
	watch.Advance(ctx, 2.0, rootOps(2.0))
	watch.Advance(ctx, 3.0, rootOps(3.0))
	mu.Lock()
	if !slices.Equal(calls, []string{"start 1"}) {
		t.Fatalf("calls with one in flight = %v", calls)
	}
	mu.Unlock()
	close(gate)
	scheduler.Wait()
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"start 1", "end 1", "start 2", "end 2", "start 3", "end 3"}; !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestWatchDeliversAContextWithTheFramesValuesAndWithoutItsCancellation(t *testing.T) {
	watch, scheduler, _ := recordedWatch(0.0, nil)
	frameCtx, cancel := context.WithCancel(context.WithValue(context.Background(), watchContextKey{}, "first"))
	var delivered context.Context
	watch.Start(func(ctx context.Context, _ any, _ []delta.Op) error { delivered = ctx; return nil })
	watch.Advance(frameCtx, 1.0, rootOps(1.0))
	cancel()
	scheduler.Wait()
	if delivered.Value(watchContextKey{}) != "first" || delivered.Done() != nil || delivered.Err() != nil {
		t.Fatalf("delivery context value %v done %v err %v", delivered.Value(watchContextKey{}), delivered.Done(), delivered.Err())
	}
}

func TestWatchCollapses101PendingFramesToOneRootReplacementOfTheNewestValue(t *testing.T) {
	watch, scheduler, _ := recordedWatch(0.0, nil)
	for n := 1; n <= 101; n++ {
		watch.Advance(context.Background(), float64(n), rootOps(float64(n)))
	}
	listener, frames := collectListener()
	watch.Start(listener)
	scheduler.Wait()
	expectFrames(t, frames(), watchFrameSeen{101.0, rootOps(101.0)})
}

func TestWatchKeeps100PendingFramesExact(t *testing.T) {
	watch, scheduler, _ := recordedWatch(0.0, nil)
	for n := 1; n <= 100; n++ {
		watch.Advance(context.Background(), float64(n), rootOps(float64(n)))
	}
	listener, frames := collectListener()
	watch.Start(listener)
	scheduler.Wait()
	got := frames()
	if len(got) != 100 {
		t.Fatalf("%d frames, want 100", len(got))
	}
	for i, frame := range got {
		if frame.value != float64(i+1) {
			t.Fatalf("frame %d = %v", i, frame.value)
		}
	}
}

func TestWatchDeliversTheValueOfReplaceOnOverflow(t *testing.T) {
	watch, scheduler, _ := recordedWatch(0.0, func() any { return "replacement" })
	for n := 1; n <= 101; n++ {
		watch.Advance(context.Background(), float64(n), nil)
	}
	listener, frames := collectListener()
	watch.Start(listener)
	scheduler.Wait()
	expectFrames(t, frames(), watchFrameSeen{"replacement", rootOps("replacement")})
}

// An overflow whose replacement is null falls back to the newest value (`this.#replace?.() ?? value`).
func TestWatchOverflowReplacementFallsBackToTheNewestValue(t *testing.T) {
	watch, scheduler, _ := recordedWatch(0.0, func() any { return nil })
	for n := 1; n <= 101; n++ {
		watch.Advance(context.Background(), float64(n), nil)
	}
	listener, frames := collectListener()
	watch.Start(listener)
	scheduler.Wait()
	expectFrames(t, frames(), watchFrameSeen{101.0, rootOps(101.0)})
}

func TestWatchNeverFoldsTheInFlightFrameIntoAnOverflow(t *testing.T) {
	watch, scheduler, _ := recordedWatch(0.0, nil)
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	var mu sync.Mutex
	var values []any
	watch.Start(func(_ context.Context, value any, _ []delta.Op) error {
		mu.Lock()
		values = append(values, value)
		mu.Unlock()
		if value == 1.0 {
			started <- struct{}{}
			<-gate
		}
		return nil
	})
	watch.Advance(context.Background(), 1.0, rootOps(1.0))
	<-started
	for n := 2; n <= 102; n++ {
		watch.Advance(context.Background(), float64(n), rootOps(float64(n)))
	}
	close(gate)
	scheduler.Wait()
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(values, []any{1.0, 102.0}) {
		t.Fatalf("values = %v, want [1 102]", values)
	}
}

func TestWatchRetirementDeliversTheLastFrameThenEndsRetired(t *testing.T) {
	watch, scheduler, record := recordedWatch(1.0, nil)
	listener, frames := collectListener()
	watch.Start(listener)
	ctx := context.Background()
	watch.Advance(ctx, 2.0, rootOps(2.0))
	watch.Retire(ctx)
	watch.Advance(ctx, 3.0, rootOps(3.0))
	scheduler.Wait()
	mustEnd(t, watch, watch.End, WatchRetired)
	expectFrames(t, frames(), watchFrameSeen{2.0, rootOps(2.0)}, watchFrameSeen{nil, rootOps(nil)})
	if watch.Value() != nil || record.detachedCount() != 1 {
		t.Fatalf("value %v detached %d, want nil and one detach", watch.Value(), record.detachedCount())
	}
}

func TestWatchRetirementDropsAnOverflowingBacklog(t *testing.T) {
	watch, scheduler, _ := recordedWatch(0.0, nil)
	for n := 1; n <= 100; n++ {
		watch.Advance(context.Background(), float64(n), rootOps(float64(n)))
	}
	watch.Retire(context.Background())
	listener, frames := collectListener()
	watch.Start(listener)
	scheduler.Wait()
	mustEnd(t, watch, watch.End, WatchRetired)
	expectFrames(t, frames(), watchFrameSeen{nil, rootOps(nil)})
}

func TestWatchEndsRetiredOnlyOnItsSourcesRetirementNeverOnANullValue(t *testing.T) {
	watch, scheduler, _ := recordedWatch(1.0, nil)
	listener, frames := collectListener()
	watch.Start(listener)
	watch.Advance(context.Background(), nil, rootOps(nil))
	watch.Advance(context.Background(), 2.0, rootOps(2.0))
	scheduler.Wait()
	expectFrames(t, frames(), watchFrameSeen{nil, rootOps(nil)}, watchFrameSeen{2.0, rootOps(2.0)})
	select {
	case <-watch.Closed():
		t.Fatalf("a null value ended the watch %+v", watch.End())
	default:
	}
	if end, _ := watch.Stop(); end.Reason != WatchStopped {
		t.Fatalf("end %+v, want stopped", end)
	}
}

func TestWatchDiscardsARetirementStillPendingWhenTheSourceEnds(t *testing.T) {
	watch, _, _ := recordedWatch(1.0, nil)
	watch.Retire(context.Background())
	watch.SourceEnded(nil)
	mustEnd(t, watch, watch.End, WatchEnded)
	if watch.Value() != 1.0 {
		t.Fatalf("value = %v, want the acquisition value", watch.Value())
	}
}

func expectPanic(t *testing.T, message string, call func()) {
	t.Helper()
	defer func() {
		recovered := recover()
		if err, ok := recovered.(error); !ok || err.Error() != message {
			t.Fatalf("panic %v, want %q", recovered, message)
		}
	}()
	call()
}

func TestWatchStopsIdempotentlyAndRejectsARepeatedOrLateStart(t *testing.T) {
	watch, _, record := recordedWatch(0.0, nil)
	noop := func(context.Context, any, []delta.Op) error { return nil }
	watch.Start(noop)
	expectPanic(t, "Watch is already started", func() { watch.Start(noop) })
	for range 2 {
		if end, _ := watch.Stop(); end.Reason != WatchStopped {
			t.Fatalf("end %+v", end)
		}
	}
	if record.detachedCount() != 1 {
		t.Fatalf("detached %d times", record.detachedCount())
	}
	late, _, _ := recordedWatch(0.0, nil)
	_, _ = late.Stop()
	expectPanic(t, "Watch is stopped", func() { late.Start(noop) })
}

func TestWatchEndsEndedOrFailedFromItsSourceDroppingPendingFrames(t *testing.T) {
	watch, scheduler, _ := recordedWatch(0.0, nil)
	listener, frames := collectListener()
	watch.Start(listener)
	watch.SourceEnded(nil)
	watch.Advance(context.Background(), 1.0, rootOps(1.0))
	scheduler.Wait()
	if got := frames(); len(got) != 0 {
		t.Fatalf("frames after the end: %v", got)
	}
	mustEnd(t, watch, watch.End, WatchEnded)

	failure := errors.New("source failed")
	failed, _, _ := recordedWatch(0.0, nil)
	failed.SourceEnded(failure)
	failed.SourceEnded(nil)
	if end := mustEnd(t, failed, failed.End, WatchFailed); !errors.Is(end.Error, failure) {
		t.Fatalf("end %+v", end)
	}
}

func TestWatchStopsFutureDeliveryWithoutAbortingOrAwaitingAnInFlightCallback(t *testing.T) {
	watch, scheduler, _ := recordedWatch(0.0, nil)
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	var mu sync.Mutex
	var calls []string
	var inFlight context.Context
	watch.Start(func(ctx context.Context, value any, _ []delta.Op) error {
		mu.Lock()
		calls = append(calls, "start")
		inFlight = ctx
		mu.Unlock()
		started <- struct{}{}
		<-gate
		mu.Lock()
		calls = append(calls, "end")
		mu.Unlock()
		return nil
	})
	watch.Advance(context.Background(), 1.0, rootOps(1.0))
	<-started
	watch.Advance(context.Background(), 2.0, rootOps(2.0))
	if end, _ := watch.Stop(); end.Reason != WatchStopped {
		t.Fatalf("end %+v", end)
	}
	if inFlight.Err() != nil {
		t.Fatal("stop aborted the in-flight callback")
	}
	close(gate)
	scheduler.Wait()
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(calls, []string{"start", "end"}) {
		t.Fatalf("calls = %v", calls)
	}
}

func TestWatchEndsCancelledWhenItsAcquisitionContextEndsAlsoWhenAlreadyDone(t *testing.T) {
	watch, _, record := recordedWatch(0.0, nil)
	ctx, cancel := context.WithCancel(context.Background())
	watch.ObserveCancellation(ctx)
	expectPanic(t, "Watch cancellation is already installed", func() { watch.ObserveCancellation(ctx) })
	cancel()
	mustEnd(t, watch, watch.End, WatchCancelled)
	if record.detachedCount() != 1 {
		t.Fatalf("detached %d times", record.detachedCount())
	}

	done, cancelDone := context.WithCancel(context.Background())
	cancelDone()
	aborted, _, _ := recordedWatch(0.0, nil)
	aborted.ObserveCancellation(done)
	mustEnd(t, aborted, aborted.End, WatchCancelled)

	cancelled, _, _ := recordedWatch(0.0, nil)
	cancelled.Cancel()
	mustEnd(t, cancelled, cancelled.End, WatchCancelled)
}

func TestWatchEndsListenerErrorOnAFailureReportsItAndDeliversNothingMore(t *testing.T) {
	watch, scheduler, record := recordedWatch(0.0, nil)
	failure := errors.New("listener failed")
	var mu sync.Mutex
	var values []any
	watch.Start(func(_ context.Context, value any, _ []delta.Op) error {
		mu.Lock()
		values = append(values, value)
		mu.Unlock()
		return failure
	})
	watch.Advance(context.Background(), 1.0, rootOps(1.0))
	watch.Advance(context.Background(), 2.0, rootOps(2.0))
	end := mustEnd(t, watch, watch.End, WatchListenerError)
	scheduler.Wait()
	if !errors.Is(end.Error, failure) {
		t.Fatalf("end %+v", end)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(values, []any{1.0}) || len(record.reported) != 1 || !errors.Is(record.reported[0], failure) || record.detachedCount() != 1 {
		t.Fatalf("values %v reported %v detached %d", values, record.reported, record.detachedCount())
	}
}

// A listener's panic with a value that is not an error ends the watch with an error that carries its text, as upstream
// wraps a thrown non-Error in an Error.
func TestWatchWrapsAPanickedNonErrorInAnError(t *testing.T) {
	watch, _, _ := recordedWatch(0.0, nil)
	watch.Start(func(context.Context, any, []delta.Op) error { panic("plain") })
	watch.Advance(context.Background(), 1.0, rootOps(1.0))
	end := mustEnd(t, watch, watch.End, WatchListenerError)
	if end.Error == nil || end.Error.Error() != "plain" {
		t.Fatalf("end %+v, want an error with the text plain", end)
	}
}

// Upstream observes the references through garbage collection; the Go form checks the references themselves: an ended
// watch holds no detach, report, replace, listener, scheduler or pending frame, and keeps its last value readable.
func TestWatchRetainsNeitherItsSourceNorItsListenerOnceEnded(t *testing.T) {
	ends := map[string]func(*Watch[any]){
		"stop":   func(w *Watch[any]) { _, _ = w.Stop() },
		"cancel": func(w *Watch[any]) { w.Cancel() },
		"end":    func(w *Watch[any]) { w.SourceEnded(nil) },
		"retire": func(w *Watch[any]) { w.Retire(context.Background()) },
	}
	for name, end := range ends {
		t.Run(name, func(t *testing.T) {
			watch, scheduler, _ := recordedWatch(0.0, func() any { return 1.0 })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			watch.ObserveCancellation(ctx)
			watch.Start(func(context.Context, any, []delta.Op) error { return nil })
			watch.Advance(context.Background(), 1.0, rootOps(1.0))
			scheduler.Wait()
			if name != "stop" {
				watch.mu.Lock()
				open := watch.detach != nil && watch.listening && watch.stopAfter != nil
				watch.mu.Unlock()
				if !open {
					t.Fatal("an open watch holds its source")
				}
			}
			end(watch)
			<-watch.Closed()
			scheduler.Wait()
			watch.mu.Lock()
			defer watch.mu.Unlock()
			if watch.detach != nil || watch.report != nil || watch.replace != nil || watch.listening || watch.scheduler != nil || watch.pending != nil || watch.stopAfter != nil {
				t.Fatalf("an ended watch holds its source: %+v", watch)
			}
			if name != "retire" && watch.value != 1.0 {
				t.Fatalf("last value = %v", watch.value)
			}
		})
	}
}

func TestNativeWatchDeliversValuesByIdentityInOrderWithAContextNeverInline(t *testing.T) {
	type thing struct{ run int }
	first, second := &thing{1}, &thing{2}
	scheduler := NewDeliveryScheduler()
	watch := NewNativeWatch(&thing{0}, func() {}, func(error) {}).On(scheduler)
	type seen struct {
		value *thing
		frame any
	}
	var mu sync.Mutex
	var delivered []seen
	var handle NativeWatchHandle[*thing] = watch
	handle.Start(func(ctx context.Context, value *thing) error {
		mu.Lock()
		delivered = append(delivered, seen{value, ctx.Value(watchContextKey{})})
		mu.Unlock()
		return nil
	})
	watch.Advance(context.WithValue(context.Background(), watchContextKey{}, "first"), first)
	watch.Advance(context.WithValue(context.Background(), watchContextKey{}, "second"), second)
	scheduler.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(delivered) != 2 || delivered[0].value != first || delivered[1].value != second || delivered[0].frame != "first" || delivered[1].frame != "second" {
		t.Fatalf("delivered %#v", delivered)
	}
	if handle.Value() != second {
		t.Fatal("value is not the last delivered")
	}
	select {
	case <-handle.Closed():
		t.Fatal("an open watch is closed")
	default:
	}
	if end, _ := handle.Stop(); end.Reason != WatchStopped {
		t.Fatalf("end %+v", end)
	}
	<-handle.Closed()
}

func TestNativeWatchCollapses101PendingValuesToTheNewest(t *testing.T) {
	scheduler := NewDeliveryScheduler()
	watch := NewNativeWatch(0, func() {}, func(error) {}).On(scheduler)
	for n := 1; n <= 101; n++ {
		watch.Advance(context.Background(), n)
	}
	var values []int
	watch.Start(func(_ context.Context, value int) error { values = append(values, value); return nil })
	scheduler.Wait()
	if !slices.Equal(values, []int{101}) {
		t.Fatalf("values = %v", values)
	}
}

func TestNativeWatchEndsWithEveryReasonOfItsLifecycle(t *testing.T) {
	create := func() NativeWatchHandle[int] {
		return NewNativeWatch(0, func() {}, func(error) {}).On(NewDeliveryScheduler())
	}
	stopped := create()
	if end, _ := stopped.Stop(); end.Reason != WatchStopped {
		t.Fatalf("end %+v", end)
	}
	cancelled := create()
	done, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled.(*NativeWatch[int]).ObserveCancellation(done)
	mustEnd(t, cancelled, cancelled.End, WatchCancelled)
	ended := create()
	ended.(*NativeWatch[int]).SourceEnded(nil)
	mustEnd(t, ended, ended.End, WatchEnded)
	failure := errors.New("source failed")
	failed := create()
	failed.(*NativeWatch[int]).SourceEnded(failure)
	if end := mustEnd(t, failed, failed.End, WatchFailed); !errors.Is(end.Error, failure) {
		t.Fatalf("end %+v", end)
	}
	var reported []error
	var mu sync.Mutex
	throwing := NewNativeWatch(0, func() {}, func(err error) { mu.Lock(); reported = append(reported, err); mu.Unlock() }).On(NewDeliveryScheduler())
	var throwingHandle NativeWatchHandle[int] = throwing
	listenerFailure := errors.New("listener failed")
	throwingHandle.Start(func(context.Context, int) error { return listenerFailure })
	throwing.Advance(context.Background(), 1)
	if end := mustEnd(t, throwingHandle, throwingHandle.End, WatchListenerError); !errors.Is(end.Error, listenerFailure) {
		t.Fatalf("end %+v", end)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reported) != 1 || !errors.Is(reported[0], listenerFailure) {
		t.Fatalf("reported %v", reported)
	}
}

func TestNativeWatchRetainsNeitherItsSourceNorItsListenerOnceStopped(t *testing.T) {
	scheduler := NewDeliveryScheduler()
	watch := NewNativeWatch(0, func() {}, func(error) {}).On(scheduler)
	watch.Start(func(context.Context, int) error { return nil })
	watch.Advance(context.Background(), 1)
	scheduler.Wait()
	watch.mu.Lock()
	open := watch.detach != nil && watch.report != nil && watch.listening
	watch.mu.Unlock()
	if !open {
		t.Fatal("an open watch holds its source")
	}
	_, _ = watch.Stop()
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.detach != nil || watch.report != nil || watch.listening || watch.scheduler != nil || watch.pending != nil {
		t.Fatalf("a stopped watch holds its source: %+v", watch)
	}
	if watch.value != 1 {
		t.Fatalf("last value = %v", watch.value)
	}
}

// A NativeWatch is used through NativeWatchHandle, the interface a source hands out: value, start, stop and closed.
func TestNativeWatchHandleInterface(t *testing.T) {
	scheduler := NewDeliveryScheduler()
	var handle NativeWatchHandle[int] = NewNativeWatch(0, func() {}, func(error) {}).On(scheduler)
	if handle.Value() != 0 {
		t.Fatalf("acquisition value = %d", handle.Value())
	}
	var mu sync.Mutex
	var seen []int
	handle.Start(func(_ context.Context, value int) error {
		mu.Lock()
		seen = append(seen, value)
		mu.Unlock()
		return nil
	})
	handle.(*NativeWatch[int]).Advance(context.Background(), 1)
	scheduler.Wait()
	end, err := handle.Stop()
	if err != nil || end.Reason != WatchStopped || handle.Value() != 1 {
		t.Fatalf("stop = %+v, %v, value %d", end, err, handle.Value())
	}
	<-handle.Closed()
	if handle.End().Reason != WatchStopped {
		t.Fatalf("end = %+v", handle.End())
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(seen, []int{1}) {
		t.Fatalf("seen = %v", seen)
	}
}

// watchableCounter is a Watchable of an integer that a source changes: each Watch is a Watch handle that starts at the current value.
type watchableCounter struct {
	scheduler *DeliveryScheduler
	mu        sync.Mutex
	value     int
	watches   []*Watch[int]
}

func (c *watchableCounter) Value() int { c.mu.Lock(); defer c.mu.Unlock(); return c.value }

func (c *watchableCounter) Watch(ctx context.Context) (WatchHandle[int], error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	watch := NewWatch(c.value, func() {}, func(error) {}, nil).On(c.scheduler)
	watch.ObserveCancellation(ctx)
	c.watches = append(c.watches, watch)
	return watch, nil
}

func (c *watchableCounter) set(ctx context.Context, value int) {
	c.mu.Lock()
	c.value = value
	watches := slices.Clone(c.watches)
	c.mu.Unlock()
	for _, watch := range watches {
		watch.Advance(ctx, value, rootOps(float64(value)))
	}
}

// packages/chord/src/watch.ts:33-42 Watchable (Pi a2fd25f56): a value that changes and can be watched; each watch holds the current value, then one frame per change,
// and cancelling the context ends it.
func TestWatchableWatchHoldsTheCurrentValueThenOneFramePerChangeAndEndsWithItsContext(t *testing.T) {
	scheduler := NewDeliveryScheduler()
	counter := &watchableCounter{scheduler: scheduler, value: 1}
	var source Watchable[int] = counter
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handle, err := source.Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if source.Value() != 1 || handle.Value() != 1 {
		t.Fatalf("value %d, watch value %d, want the current value 1", source.Value(), handle.Value())
	}
	var mu sync.Mutex
	var values []int
	handle.Start(func(_ context.Context, value int, _ []delta.Op) error {
		mu.Lock()
		values = append(values, value)
		mu.Unlock()
		return nil
	})
	counter.set(context.Background(), 2)
	counter.set(context.Background(), 3)
	scheduler.Wait()
	mu.Lock()
	if !slices.Equal(values, []int{2, 3}) || source.Value() != 3 {
		t.Fatalf("frames %v value %d, want [2 3] and 3", values, source.Value())
	}
	mu.Unlock()
	cancel()
	<-handle.Closed()
	if end := handle.End(); end.Reason != WatchCancelled {
		t.Fatalf("end %+v, want cancelled", end)
	}
}

// cellValue is a value a NativeWatchable holds by identity.
type cellValue struct{ n int }

// nativeWatchableCell is a NativeWatchable of a value held by identity: each Watch is a NativeWatch that starts at the current value.
type nativeWatchableCell struct {
	scheduler *DeliveryScheduler
	mu        sync.Mutex
	value     *cellValue
	watches   []*NativeWatch[*cellValue]
}

func (c *nativeWatchableCell) Value() *cellValue { c.mu.Lock(); defer c.mu.Unlock(); return c.value }

func (c *nativeWatchableCell) Watch(ctx context.Context) (NativeWatchHandle[*cellValue], error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	watch := NewNativeWatch(c.value, func() {}, func(error) {}).On(c.scheduler)
	watch.ObserveCancellation(ctx)
	c.watches = append(c.watches, watch)
	return watch, nil
}

func (c *nativeWatchableCell) set(ctx context.Context, value *cellValue) {
	c.mu.Lock()
	c.value = value
	watches := slices.Clone(c.watches)
	c.mu.Unlock()
	for _, watch := range watches {
		watch.Advance(ctx, value)
	}
}

// packages/chord/src/watch.ts:44-53 NativeWatchable (Pi 5e106ab1c): an in-process value that changes and can be watched; each watch holds the current value,
// then one frame per new value by identity, and cancelling the context ends it.
func TestNativeWatchableWatchHoldsTheCurrentValueThenOneFramePerNewValueAndEndsWithItsContext(t *testing.T) {
	scheduler := NewDeliveryScheduler()
	first, second, third := &cellValue{1}, &cellValue{2}, &cellValue{3}
	cell := &nativeWatchableCell{scheduler: scheduler, value: first}
	var source NativeWatchable[*cellValue] = cell
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handle, err := source.Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if source.Value() != first || handle.Value() != first {
		t.Fatalf("value %p, watch value %p, want the current value %p", source.Value(), handle.Value(), first)
	}
	var mu sync.Mutex
	var values []*cellValue
	handle.Start(func(_ context.Context, value *cellValue) error {
		mu.Lock()
		values = append(values, value)
		mu.Unlock()
		return nil
	})
	cell.set(context.Background(), second)
	cell.set(context.Background(), third)
	scheduler.Wait()
	mu.Lock()
	if len(values) != 2 || values[0] != second || values[1] != third || source.Value() != third {
		t.Fatalf("frames %v value %p, want the second and third values by identity and the third as the current value", values, source.Value())
	}
	mu.Unlock()
	cancel()
	<-handle.Closed()
	if end := handle.End(); end.Reason != WatchCancelled {
		t.Fatalf("end %+v, want cancelled", end)
	}
}
