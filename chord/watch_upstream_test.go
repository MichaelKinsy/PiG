package chord

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// Ports packages/chord/test/watch.test.ts: the shared lifecycle through Watch, and what NativeWatch does differently.
// Upstream's flush() (a macrotask that lets the microtask queue drain) is DeliveryScheduler.Wait here.

type watchFrameSeen struct {
	value any
	ops   []delta.Op
}

// frame is a source's frame that is not a retirement.
func frame(value any, ops []delta.Op) WatchChange[any] {
	return WatchChange[any]{Value: value, Ops: ops}
}

// retirement is a source's retirement carrying value, with no ops.
func retirement(value any) WatchChange[any] { return WatchChange[any]{Value: value, Retired: true} }

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
func recordedWatch(value any) (*Watch[any], *DeliveryScheduler, *watchRecord) {
	scheduler := NewDeliveryScheduler()
	record := &watchRecord{}
	return NewWatch(value, record.detach, record.report).On(scheduler), scheduler, record
}

// collectListener returns a listener that appends each frame to the returned slice, read after scheduler.Wait.
func collectListener() (func(context.Context, WatchChange[any]) error, func() []watchFrameSeen) {
	var mu sync.Mutex
	var frames []watchFrameSeen
	return func(_ context.Context, change WatchChange[any]) error {
			mu.Lock()
			frames = append(frames, watchFrameSeen{change.Value, change.Ops})
			mu.Unlock()
			return nil
		}, func() []watchFrameSeen {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(frames)
		}
}

// collectChanges returns a listener that appends each change, retirement flag included, read after scheduler.Wait.
func collectChanges() (func(context.Context, WatchChange[any]) error, func() []WatchChange[any]) {
	var mu sync.Mutex
	var changes []WatchChange[any]
	return func(_ context.Context, change WatchChange[any]) error {
			mu.Lock()
			changes = append(changes, change)
			mu.Unlock()
			return nil
		}, func() []WatchChange[any] {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(changes)
		}
}

func expectChanges(t *testing.T, got []WatchChange[any], want ...WatchChange[any]) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("changes %#v, want %#v", got, want)
	}
	for i := range want {
		if !equalJSON(got[i].Value, want[i].Value) || !equalJSON(got[i].Ops, want[i].Ops) || got[i].Retired != want[i].Retired {
			t.Fatalf("change %d = %#v, want %#v", i, got[i], want[i])
		}
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

// maxPendingOps is chord/delta MaxPendingOps, upstream's MAX_PENDING_OPS.
const maxPendingOps = delta.MaxPendingOps

func TestWatchKeepsTheAcquisitionValueUntilStartThenDeliversTheFramesSinceInOneCall(t *testing.T) {
	watch, scheduler, _ := recordedWatch(map[string]any{"n": 0.0, "text": ""})
	ctx := context.Background()
	watch.Advance(ctx, frame(map[string]any{"n": 1.0, "text": ""}, []delta.Op{{"s", []any{"n"}, 1.0}}))
	watch.Advance(ctx, frame(map[string]any{"n": 2.0, "text": "a"}, []delta.Op{{"s", []any{"n"}, 2.0}, {"a", []any{"text"}, "a"}}))
	watch.Advance(ctx, frame(map[string]any{"n": 2.0, "text": "ab"}, []delta.Op{{"a", []any{"text"}, "b"}}))
	scheduler.Wait()
	if !deepEqualAny(watch.Value(), map[string]any{"n": 0.0, "text": ""}) {
		t.Fatalf("value before start = %v", watch.Value())
	}
	listener, frames := collectListener()
	watch.Start(listener)
	if got := frames(); len(got) != 0 {
		t.Fatalf("frames before the delivery ran: %v", got)
	}
	scheduler.Wait()
	expectFrames(t, frames(), watchFrameSeen{map[string]any{"n": 2.0, "text": "ab"}, []delta.Op{{"s", []any{"n"}, 2.0}, {"a", []any{"text"}, "ab"}}})
	if !deepEqualAny(watch.Value(), map[string]any{"n": 2.0, "text": "ab"}) {
		t.Fatalf("value after delivery = %v", watch.Value())
	}
}

func TestWatchPassesAFramesOpsOnByIdentityToAListenerThatKeepsUp(t *testing.T) {
	watch, scheduler, _ := recordedWatch(0.0)
	var mu sync.Mutex
	var delivered [][]delta.Op
	watch.Start(func(_ context.Context, change WatchChange[any]) error {
		ops := change.Ops
		mu.Lock()
		delivered = append(delivered, ops)
		mu.Unlock()
		return nil
	})
	first, second := rootOps(1.0), rootOps(2.0)
	watch.Advance(context.Background(), frame(1.0, first))
	scheduler.Wait()
	watch.Advance(context.Background(), frame(2.0, second))
	scheduler.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(delivered) != 2 || &delivered[0][0] != &first[0] || &delivered[1][0] != &second[0] {
		t.Fatalf("delivered %v", delivered)
	}
}

func TestWatchNeverInvokesTheListenerInline(t *testing.T) {
	watch, scheduler, _ := recordedWatch(0.0)
	var deliveredOn atomic.Uint64
	watch.Start(func(context.Context, WatchChange[any]) error {
		deliveredOn.Store(goroutineid.Current())
		return nil
	})
	watch.Advance(context.Background(), frame(1.0, rootOps(1.0)))
	scheduler.Wait()
	if got := deliveredOn.Load(); got == 0 || got == goroutineid.Current() {
		t.Fatalf("the listener ran on goroutine %d, the advancing one is %d", got, goroutineid.Current())
	}
}

func TestWatchAwaitsEachCallbackAndFoldsFramesAdvancedWhileOneIsInFlightIntoTheNextCall(t *testing.T) {
	watch, scheduler, _ := recordedWatch(map[string]any{"text": ""})
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	var mu sync.Mutex
	var calls []string
	var frames []watchFrameSeen
	watch.Start(func(_ context.Context, change WatchChange[any]) error {
		value := change.Value
		ops := change.Ops
		mu.Lock()
		text := value.(map[string]any)["text"].(string)
		calls = append(calls, "start "+text)
		frames = append(frames, watchFrameSeen{value, ops})
		first := len(frames) == 1
		mu.Unlock()
		if first {
			started <- struct{}{}
			<-gate
		}
		mu.Lock()
		calls = append(calls, "end "+text)
		mu.Unlock()
		return nil
	})
	ctx := context.Background()
	watch.Advance(ctx, frame(map[string]any{"text": "a"}, []delta.Op{{"a", []any{"text"}, "a"}}))
	<-started
	watch.Advance(ctx, frame(map[string]any{"text": "ab"}, []delta.Op{{"a", []any{"text"}, "b"}}))
	watch.Advance(ctx, frame(map[string]any{"text": "abc"}, []delta.Op{{"a", []any{"text"}, "c"}}))
	mu.Lock()
	if !slices.Equal(calls, []string{"start a"}) {
		t.Fatalf("calls with one in flight = %v", calls)
	}
	mu.Unlock()
	close(gate)
	scheduler.Wait()
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"start a", "end a", "start abc", "end abc"}; !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if !deepEqualAny(frames[1].ops, []delta.Op{{"a", []any{"text"}, "bc"}}) {
		t.Fatalf("folded ops = %v", frames[1].ops)
	}
}

func TestWatchDeliversTheNewestFramesContextWithoutItsCancellation(t *testing.T) {
	watch, scheduler, _ := recordedWatch(0.0)
	first := context.WithValue(context.Background(), watchContextKey{}, "first")
	second, cancel := context.WithCancel(context.WithValue(context.Background(), watchContextKey{}, "second"))
	var mu sync.Mutex
	var delivered []context.Context
	watch.Advance(first, frame(1.0, rootOps(1.0)))
	watch.Advance(second, frame(2.0, rootOps(2.0)))
	watch.Start(func(ctx context.Context, change WatchChange[any]) error {
		mu.Lock()
		delivered = append(delivered, ctx)
		mu.Unlock()
		return nil
	})
	cancel()
	scheduler.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(delivered) != 1 || delivered[0].Value(watchContextKey{}) != "second" || delivered[0].Done() != nil || delivered[0].Err() != nil {
		t.Fatalf("delivered %v", delivered)
	}
}

// A listener that takes long per call, as a slow client would: it gets far fewer calls than commits, each carrying at most one op per path written since the previous call, and a replica that applies them reaches the newest value.
func TestWatchWithASlowListenerGetsOneCallPerBatchWhoseOpsTurnThePreviousDeliveredValueIntoTheNewest(t *testing.T) {
	watch, scheduler, _ := recordedWatch(map[string]any{"text": "", "tokens": 0.0})
	gate := make(chan struct{}, 1)
	started := make(chan struct{}, 1)
	var mu sync.Mutex
	var calls []int
	var text string
	var tokens float64
	watch.Start(func(_ context.Context, change WatchChange[any]) error {
		ops := change.Ops
		mu.Lock()
		for _, op := range ops {
			switch op[0] {
			case "a":
				text += op[2].(string)
			case "s":
				tokens = op[2].(float64)
			}
		}
		calls = append(calls, len(ops))
		mu.Unlock()
		started <- struct{}{}
		<-gate
		return nil
	})
	ctx := context.Background()
	commits := 600
	var streamed string
	for index := 1; index <= commits; index++ {
		streamed += "tok "
		watch.Advance(ctx, frame(map[string]any{"text": streamed, "tokens": float64(index)}, []delta.Op{{"a", []any{"text"}, "tok "}, {"s", []any{"tokens"}, float64(index)}}))
		if index == 1 {
			<-started
		}
		if index%60 == 0 {
			// The listener finishes its call; the next one starts with everything advanced since.
			gate <- struct{}{}
			if index < commits {
				<-started
			}
		}
	}
	gate <- struct{}{}
	scheduler.Wait()
	mu.Lock()
	defer mu.Unlock()
	if text != streamed || tokens != float64(commits) {
		t.Fatalf("replica %q %v, want %q %v", text, tokens, streamed, commits)
	}
	if len(calls) < 10 || len(calls) > 12 {
		t.Fatalf("%d calls for %d commits, want about one per 60", len(calls), commits)
	}
	for _, ops := range calls {
		if ops > 2 {
			t.Fatalf("a call carried %d ops, want at most one per path", ops)
		}
	}
}

// advanceDistinct advances watch by one frame per element of {k: [0..count-1]}, each setting its own path, and returns the newest value.
func advanceDistinct(watch *Watch[any], count int) any {
	values := []any{}
	for index := range count {
		values = append(values, float64(index))
		watch.Advance(context.Background(), frame(map[string]any{"k": slices.Clone(values)}, []delta.Op{{"s", []any{"k", index}, float64(index)}}))
	}
	return map[string]any{"k": values}
}

func TestWatchKeepsMaxPendingOpsPendingOpsAfterFoldingAndReplacesTheRootPastThem(t *testing.T) {
	exact, scheduler, _ := recordedWatch(map[string]any{"k": []any{}})
	advanceDistinct(exact, maxPendingOps)
	listener, frames := collectListener()
	exact.Start(listener)
	scheduler.Wait()
	if got := frames(); len(got) != 1 || len(got[0].ops) != maxPendingOps {
		t.Fatalf("exact: %d frames", len(frames()))
	}

	over, scheduler, _ := recordedWatch(map[string]any{"k": []any{}})
	newest := advanceDistinct(over, maxPendingOps+1)
	listener, frames = collectListener()
	over.Start(listener)
	scheduler.Wait()
	expectFrames(t, frames(), watchFrameSeen{newest, rootOps(newest)})
}

func TestWatchNeverFoldsTheInFlightCallIntoAnOverflow(t *testing.T) {
	watch, scheduler, _ := recordedWatch(map[string]any{"k": []any{}})
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	var mu sync.Mutex
	var frames []watchFrameSeen
	watch.Start(func(_ context.Context, change WatchChange[any]) error {
		value := change.Value
		ops := change.Ops
		mu.Lock()
		frames = append(frames, watchFrameSeen{value, ops})
		first := len(frames) == 1
		mu.Unlock()
		if first {
			started <- struct{}{}
			<-gate
		}
		return nil
	})
	watch.Advance(context.Background(), frame(map[string]any{"k": []any{}, "first": true}, []delta.Op{{"s", []any{"first"}, true}}))
	<-started
	newest := advanceDistinct(watch, maxPendingOps+1)
	close(gate)
	scheduler.Wait()
	mu.Lock()
	defer mu.Unlock()
	expectFrames(t, frames,
		watchFrameSeen{map[string]any{"k": []any{}, "first": true}, []delta.Op{{"s", []any{"first"}, true}}},
		watchFrameSeen{newest, rootOps(newest)})
}

func TestWatchStartsFoldingAgainAfterAnOverflowWasDelivered(t *testing.T) {
	watch, scheduler, _ := recordedWatch(map[string]any{"k": []any{}})
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	var mu sync.Mutex
	var frames []watchFrameSeen
	advanceDistinct(watch, maxPendingOps+1)
	watch.Start(func(_ context.Context, change WatchChange[any]) error {
		value := change.Value
		ops := change.Ops
		mu.Lock()
		frames = append(frames, watchFrameSeen{value, ops})
		mu.Unlock()
		if _, blocked := value.(map[string]any)["k"]; blocked {
			started <- struct{}{}
			<-gate
		}
		return nil
	})
	// The overflow is delivered while the listener is held, so the frames that follow wait behind it.
	<-started
	watch.Advance(context.Background(), frame(map[string]any{"n": 1.0}, rootOps(map[string]any{"n": 1.0})))
	watch.Advance(context.Background(), frame(map[string]any{"n": 2.0}, []delta.Op{{"s", []any{"n"}, 2.0}}))
	close(gate)
	scheduler.Wait()
	mu.Lock()
	defer mu.Unlock()
	want := watchFrameSeen{map[string]any{"n": 2.0}, []delta.Op{{"r", map[string]any{"n": 1.0}}, {"s", []any{"n"}, 2.0}}}
	expectFrames(t, frames[len(frames)-1:], want)
}

func TestWatchReportsARetirementInTheNextCallWithTheValueTheSourceGivesAndGoesOn(t *testing.T) {
	watch, scheduler, record := recordedWatch(1.0)
	listener, changes := collectChanges()
	watch.Start(listener)
	ctx := context.Background()
	watch.Advance(ctx, frame(2.0, rootOps(2.0)))
	scheduler.Wait()
	watch.Advance(ctx, retirement(2.0))
	scheduler.Wait()
	watch.Advance(ctx, frame(3.0, rootOps(3.0)))
	scheduler.Wait()
	expectChanges(t, changes(),
		WatchChange[any]{Value: 2.0, Ops: rootOps(2.0)},
		WatchChange[any]{Value: 2.0, Retired: true},
		WatchChange[any]{Value: 3.0, Ops: rootOps(3.0)})
	select {
	case <-watch.Closed():
		t.Fatalf("a retirement ended the watch %+v", watch.End())
	default:
	}
	if watch.Value() != 3.0 || record.detachedCount() != 0 {
		t.Fatalf("value %v detached %d, want 3 and no detach", watch.Value(), record.detachedCount())
	}
	if end, _ := watch.Stop(); end.Reason != WatchStopped {
		t.Fatalf("end %+v, want stopped", end)
	}
}

func TestWatchFoldsARetirementAndTheFramesAroundItIntoOneCallThatReportsIt(t *testing.T) {
	watch, scheduler, _ := recordedWatch(map[string]any{"n": 0.0})
	ctx := context.Background()
	watch.Advance(ctx, frame(map[string]any{"n": 1.0}, []delta.Op{{"s", []any{"n"}, 1.0}}))
	watch.Advance(ctx, retirement(map[string]any{"n": 1.0}))
	watch.Advance(ctx, frame(map[string]any{"m": 1.0}, rootOps(map[string]any{"m": 1.0})))
	listener, changes := collectChanges()
	watch.Start(listener)
	scheduler.Wait()
	expectChanges(t, changes(), WatchChange[any]{Value: map[string]any{"m": 1.0}, Ops: rootOps(map[string]any{"m": 1.0}), Retired: true})
	watch.Advance(ctx, frame(map[string]any{"m": 2.0}, []delta.Op{{"s", []any{"m"}, 2.0}}))
	scheduler.Wait()
	expectChanges(t, changes()[1:], WatchChange[any]{Value: map[string]any{"m": 2.0}, Ops: []delta.Op{{"s", []any{"m"}, 2.0}}})
	_, _ = watch.Stop()
}

func TestWatchKeepsReportingTheRetirementPastAnOverflow(t *testing.T) {
	watch, scheduler, _ := recordedWatch(map[string]any{"k": []any{}})
	watch.Advance(context.Background(), retirement(map[string]any{"k": []any{}}))
	newest := advanceDistinct(watch, maxPendingOps+1)
	listener, changes := collectChanges()
	watch.Start(listener)
	scheduler.Wait()
	expectChanges(t, changes(), WatchChange[any]{Value: newest, Ops: rootOps(newest), Retired: true})
	_, _ = watch.Stop()
}

func TestWatchTreatsANullValueAsAValueNeverAsARetirement(t *testing.T) {
	watch, scheduler, _ := recordedWatch(1.0)
	listener, frames := collectListener()
	watch.Start(listener)
	watch.Advance(context.Background(), frame(nil, rootOps(nil)))
	scheduler.Wait()
	watch.Advance(context.Background(), frame(2.0, rootOps(2.0)))
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
	watch, _, _ := recordedWatch(1.0)
	watch.Advance(context.Background(), retirement(1.0))
	watch.SourceEnded(nil)
	mustEnd(t, watch, watch.End, WatchEnded)
	if watch.Value() != 1.0 {
		t.Fatalf("value = %v, want the acquisition value", watch.Value())
	}
}

func TestWatchEndAfterDeliveryTakesNoMoreFramesDeliversWhatIsPendingThenEndsEndedAndOnlyThenDetaches(t *testing.T) {
	watch, scheduler, record := recordedWatch(1.0)
	listener, changes := collectChanges()
	watch.Start(listener)
	ctx := context.Background()
	watch.Advance(ctx, retirement(1.0))
	watch.EndAfterDelivery()
	// The source sends nothing more: a later frame is not taken.
	watch.Advance(ctx, frame(2.0, rootOps(2.0)))
	scheduler.Wait()
	<-watch.Closed()
	expectChanges(t, changes(), WatchChange[any]{Value: 1.0, Retired: true})
	if end := watch.End(); end.Reason != WatchEnded || record.detachedCount() != 1 {
		t.Fatalf("end %+v detached %d, want ended and one detach", end, record.detachedCount())
	}
}

func TestWatchEndAfterDeliveryWaitsForStartToDeliverAndEndsAtOnceWithNothingPending(t *testing.T) {
	watch, scheduler, record := recordedWatch(1.0)
	watch.Advance(context.Background(), frame(2.0, rootOps(2.0)))
	watch.EndAfterDelivery()
	scheduler.Wait()
	select {
	case <-watch.Closed():
		t.Fatalf("a never-started watch ended with a frame pending: %+v", watch.End())
	default:
	}
	if record.detachedCount() != 0 {
		t.Fatalf("detached %d times before start", record.detachedCount())
	}
	listener, frames := collectListener()
	watch.Start(listener)
	mustEnd(t, watch, watch.End, WatchEnded)
	expectFrames(t, frames(), watchFrameSeen{2.0, rootOps(2.0)})
	if record.detachedCount() != 1 {
		t.Fatalf("detached %d times", record.detachedCount())
	}

	idle, _, idleRecord := recordedWatch(1.0)
	idleListener, idleFrames := collectListener()
	idle.Start(idleListener)
	idle.EndAfterDelivery()
	mustEnd(t, idle, idle.End, WatchEnded)
	if len(idleFrames()) != 0 || idleRecord.detachedCount() != 1 {
		t.Fatalf("frames %v detached %d, want none and one detach", idleFrames(), idleRecord.detachedCount())
	}
}

func TestWatchEndAfterDeliveryEndsANeverStartedWatchAtItsSourcesEndDroppingWhatIsPending(t *testing.T) {
	watch, _, record := recordedWatch(1.0)
	watch.Advance(context.Background(), retirement(1.0))
	watch.EndAfterDelivery()
	failure := errors.New("source failed")
	watch.SourceEnded(failure)
	if end := mustEnd(t, watch, watch.End, WatchFailed); !errors.Is(end.Error, failure) {
		t.Fatalf("end %+v", end)
	}
	if record.detachedCount() != 1 {
		t.Fatalf("detached %d times", record.detachedCount())
	}
	expectPanic(t, "Watch is stopped", func() { watch.Start(func(context.Context, WatchChange[any]) error { return nil }) })
}

// endingWhileRunning is a started watch whose listener is in a call held by gate, with nothing pending, then EndAfterDelivery.
type endingWatch struct {
	watch     *Watch[any]
	scheduler *DeliveryScheduler
	record    *watchRecord
	release   func()
	returned  *atomic.Bool
}

func endingWhileRunning(t *testing.T, settle func() error) endingWatch {
	t.Helper()
	watch, scheduler, record := recordedWatch(0.0)
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	returned := &atomic.Bool{}
	watch.Start(func(_ context.Context, change WatchChange[any]) error {
		if change.Value != 1.0 {
			t.Errorf("value %v, want 1", change.Value)
		}
		entered <- struct{}{}
		<-gate
		err := settle()
		returned.Store(true)
		return err
	})
	watch.Advance(context.Background(), WatchChange[any]{Value: 1.0, Ops: rootOps(1.0), Retired: true})
	<-entered
	watch.EndAfterDelivery()
	select {
	case <-watch.Closed():
		t.Fatalf("the watch ended while its call runs: %+v", watch.End())
	default:
	}
	return endingWatch{watch, scheduler, record, sync.OnceFunc(func() { close(gate) }), returned}
}

func TestWatchEndAfterDeliveryEndsEndedOnlyAfterTheRunningCallReturnsWhenNothingIsPending(t *testing.T) {
	ending := endingWhileRunning(t, func() error { return nil })
	if ending.record.detachedCount() != 0 {
		t.Fatalf("detached %d times while the call runs", ending.record.detachedCount())
	}
	ending.release()
	mustEnd(t, ending.watch, ending.watch.End, WatchEnded)
	if !ending.returned.Load() || ending.record.detachedCount() != 1 {
		t.Fatalf("returned %v detached %d, want the call returned and one detach", ending.returned.Load(), ending.record.detachedCount())
	}
}

func TestWatchEndAfterDeliveryEndsListenerErrorAndReportsItWhenTheRunningCallFails(t *testing.T) {
	failure := errors.New("listener failed")
	ending := endingWhileRunning(t, func() error { return failure })
	ending.release()
	end := mustEnd(t, ending.watch, ending.watch.End, WatchListenerError)
	if !errors.Is(end.Error, failure) {
		t.Fatalf("end %+v", end)
	}
	ending.record.mu.Lock()
	defer ending.record.mu.Unlock()
	if len(ending.record.reported) != 1 || !errors.Is(ending.record.reported[0], failure) || ending.record.detached != 1 {
		t.Fatalf("reported %v detached %d", ending.record.reported, ending.record.detached)
	}
}

func TestWatchEndAfterDeliveryEndsStoppedWhenStoppedWhileEndingWithoutAwaitingTheRunningCall(t *testing.T) {
	ending := endingWhileRunning(t, func() error { return nil })
	if end, _ := ending.watch.Stop(); end.Reason != WatchStopped {
		t.Fatalf("end %+v", end)
	}
	if ending.record.detachedCount() != 1 || ending.returned.Load() {
		t.Fatalf("detached %d returned %v, want one detach and the call still running", ending.record.detachedCount(), ending.returned.Load())
	}
	ending.release()
	ending.scheduler.Wait()
	if end := ending.watch.End(); end.Reason != WatchStopped {
		t.Fatalf("end %+v, want stopped", end)
	}
}

func TestWatchChangeObjectsCarryPlainDataOnlyTheFoldedChangeBuiltByTheWatch(t *testing.T) {
	watch, scheduler, _ := recordedWatch(map[string]any{"n": 0.0})
	listener, changes := collectChanges()
	watch.Start(listener)
	ctx := context.Background()
	watch.Advance(ctx, frame(map[string]any{"n": 1.0}, []delta.Op{{"s", []any{"n"}, 1.0}}))
	scheduler.Wait()
	// Folded: the watch builds the change. A Hold keeps the first frame from starting a call, as the microtask upstream
	// queues for one publication does.
	release := scheduler.Hold()
	watch.Advance(ctx, frame(map[string]any{"n": 2.0}, []delta.Op{{"s", []any{"n"}, 2.0}}))
	watch.Advance(ctx, retirement(map[string]any{"n": 2.0}))
	release()
	scheduler.Wait()
	expectChanges(t, changes(),
		WatchChange[any]{Value: map[string]any{"n": 1.0}, Ops: []delta.Op{{"s", []any{"n"}, 1.0}}},
		WatchChange[any]{Value: map[string]any{"n": 2.0}, Ops: []delta.Op{{"s", []any{"n"}, 2.0}}, Retired: true})
	_, _ = watch.Stop()
}

func TestWatchPassesAFrameDeliveredAloneOnAsTheSourceMadeIt(t *testing.T) {
	watch, scheduler, _ := recordedWatch(0.0)
	listener, changes := collectChanges()
	watch.Start(listener)
	alone := frame(1.0, rootOps(1.0))
	watch.Advance(context.Background(), alone)
	scheduler.Wait()
	got := changes()
	if len(got) != 1 || got[0].Value != alone.Value || got[0].Retired || &got[0].Ops[0] != &alone.Ops[0] {
		t.Fatalf("changes %#v, want the frame the source made %#v", got, alone)
	}
	_, _ = watch.Stop()
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
	watch, _, record := recordedWatch(0.0)
	noop := func(context.Context, WatchChange[any]) error { return nil }
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
	late, _, _ := recordedWatch(0.0)
	_, _ = late.Stop()
	expectPanic(t, "Watch is stopped", func() { late.Start(noop) })
}

func TestWatchEndsEndedOrFailedFromItsSourceDroppingPendingFrames(t *testing.T) {
	watch, scheduler, _ := recordedWatch(0.0)
	listener, frames := collectListener()
	watch.Start(listener)
	watch.SourceEnded(nil)
	watch.Advance(context.Background(), frame(1.0, rootOps(1.0)))
	scheduler.Wait()
	if got := frames(); len(got) != 0 {
		t.Fatalf("frames after the end: %v", got)
	}
	mustEnd(t, watch, watch.End, WatchEnded)

	failure := errors.New("source failed")
	failed, _, _ := recordedWatch(0.0)
	failed.SourceEnded(failure)
	failed.SourceEnded(nil)
	if end := mustEnd(t, failed, failed.End, WatchFailed); !errors.Is(end.Error, failure) {
		t.Fatalf("end %+v", end)
	}
}

func TestWatchStopsFutureDeliveryWithoutAbortingOrAwaitingAnInFlightCallback(t *testing.T) {
	watch, scheduler, _ := recordedWatch(0.0)
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	var mu sync.Mutex
	var calls []string
	var inFlight context.Context
	watch.Start(func(ctx context.Context, change WatchChange[any]) error {
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
	watch.Advance(context.Background(), frame(1.0, rootOps(1.0)))
	<-started
	watch.Advance(context.Background(), frame(2.0, rootOps(2.0)))
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
	watch, _, record := recordedWatch(0.0)
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
	aborted, _, _ := recordedWatch(0.0)
	aborted.ObserveCancellation(done)
	mustEnd(t, aborted, aborted.End, WatchCancelled)

	cancelled, _, _ := recordedWatch(0.0)
	cancelled.Cancel()
	mustEnd(t, cancelled, cancelled.End, WatchCancelled)
}

func TestWatchEndsListenerErrorOnAFailureReportsItAndDeliversNothingMore(t *testing.T) {
	watch, scheduler, record := recordedWatch(0.0)
	failure := errors.New("listener failed")
	var mu sync.Mutex
	var values []any
	watch.Start(func(_ context.Context, change WatchChange[any]) error {
		value := change.Value
		mu.Lock()
		values = append(values, value)
		mu.Unlock()
		return failure
	})
	watch.Advance(context.Background(), frame(1.0, rootOps(1.0)))
	watch.Advance(context.Background(), frame(2.0, rootOps(2.0)))
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
	watch, _, _ := recordedWatch(0.0)
	watch.Start(func(context.Context, WatchChange[any]) error { panic("plain") })
	watch.Advance(context.Background(), frame(1.0, rootOps(1.0)))
	end := mustEnd(t, watch, watch.End, WatchListenerError)
	if end.Error == nil || end.Error.Error() != "plain" {
		t.Fatalf("end %+v, want an error with the text plain", end)
	}
}

// Upstream observes the references through garbage collection; the Go form checks the references themselves: an ended
// watch holds no detach, report, replace, listener, scheduler or pending frame, and keeps its last value readable.
func TestWatchRetainsNeitherItsSourceNorItsListenerOnceEnded(t *testing.T) {
	ends := map[string]func(*Watch[any]){
		"stop":             func(w *Watch[any]) { _, _ = w.Stop() },
		"cancel":           func(w *Watch[any]) { w.Cancel() },
		"end":              func(w *Watch[any]) { w.SourceEnded(nil) },
		"endAfterDelivery": func(w *Watch[any]) { w.EndAfterDelivery() },
	}
	for name, end := range ends {
		t.Run(name, func(t *testing.T) {
			watch, scheduler, _ := recordedWatch(0.0)
			ctx := t.Context()
			watch.ObserveCancellation(ctx)
			watch.Start(func(context.Context, WatchChange[any]) error { return nil })
			watch.Advance(context.Background(), frame(1.0, rootOps(1.0)))
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
			if watch.detach != nil || watch.report != nil || watch.listening || watch.scheduler != nil || watch.newest != nil || watch.ops.Size() != 0 || watch.stopAfter != nil {
				t.Fatalf("an ended watch holds its source: %+v", watch)
			}
			if watch.value != 1.0 {
				t.Fatalf("last value = %v", watch.value)
			}
		})
	}
}

// A fold or an overflow drops the received batches: an unstarted watch fed four batches of 4,000 sets each overflows at
// the third and ignores the fourth, and keeps none of them while it waits.
func TestWatchKeepsNoOpsAFoldOrAnOverflowDroppedWhileItWaits(t *testing.T) {
	watch, scheduler, _ := recordedWatch(nil)
	batches := make([]weak.Pointer[delta.Op], 0, 4)
	var newest any
	feed := func() {
		for round := range 4 {
			ops := make([]delta.Op, 0, 4_000)
			for index := round * 4_000; index < (round+1)*4_000; index++ {
				ops = append(ops, delta.Op{"s", []any{"k" + strconv.Itoa(index)}, 1.0})
			}
			batches = append(batches, weak.Make(&ops[0]))
			newest = map[string]any{"round": float64(round)}
			watch.Advance(context.Background(), frame(newest, ops))
		}
	}
	feed()
	for range 5 {
		runtime.GC()
	}
	for index, batch := range batches {
		if batch.Value() != nil {
			t.Fatalf("batch %d is still referenced by the waiting watch", index)
		}
	}
	listener, changes := collectChanges()
	watch.Start(listener)
	scheduler.Wait()
	expectChanges(t, changes(), WatchChange[any]{Value: newest, Ops: rootOps(newest)})
	_, _ = watch.Stop()
}

func TestNativeWatchDeliversValuesByIdentityInOrderWithAContextNeverInline(t *testing.T) {
	type thing struct{ run int }
	first, second := &thing{1}, &thing{2}
	scheduler := NewDeliveryScheduler()
	watch := NewNativeWatch(&thing{0}, func() {}, func(error) {}, nil).On(scheduler)
	type seen struct {
		value *thing
		frame any
	}
	var mu sync.Mutex
	var delivered []seen
	var handle NativeWatchHandle[*thing] = watch
	handle.Start(func(ctx context.Context, change NativeWatchChange[*thing]) error {
		mu.Lock()
		delivered = append(delivered, seen{change.Value, ctx.Value(watchContextKey{})})
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

func TestNativeWatchCollapses101PendingValuesToTheValueOfReplace(t *testing.T) {
	scheduler := NewDeliveryScheduler()
	watch := NewNativeWatch("", func() {}, func(error) {}, func() string { return "replacement" }).On(scheduler)
	for n := 1; n <= 101; n++ {
		watch.Advance(context.Background(), "value "+string(rune('0'+n%10)))
	}
	var values []string
	watch.Start(func(_ context.Context, change NativeWatchChange[string]) error {
		values = append(values, change.Value)
		return nil
	})
	scheduler.Wait()
	if !slices.Equal(values, []string{"replacement"}) {
		t.Fatalf("values = %v", values)
	}
}

func TestNativeWatchCollapses101PendingValuesToTheNewest(t *testing.T) {
	scheduler := NewDeliveryScheduler()
	watch := NewNativeWatch(0, func() {}, func(error) {}, nil).On(scheduler)
	for n := 1; n <= 101; n++ {
		watch.Advance(context.Background(), n)
	}
	var values []int
	watch.Start(func(_ context.Context, change NativeWatchChange[int]) error {
		values = append(values, change.Value)
		return nil
	})
	scheduler.Wait()
	if !slices.Equal(values, []int{101}) {
		t.Fatalf("values = %v", values)
	}
}

func TestNativeWatchEndsWithEveryReasonOfItsLifecycle(t *testing.T) {
	create := func() NativeWatchHandle[int] {
		return NewNativeWatch(0, func() {}, func(error) {}, nil).On(NewDeliveryScheduler())
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
	throwing := NewNativeWatch(0, func() {}, func(err error) { mu.Lock(); reported = append(reported, err); mu.Unlock() }, nil).On(NewDeliveryScheduler())
	var throwingHandle NativeWatchHandle[int] = throwing
	listenerFailure := errors.New("listener failed")
	throwingHandle.Start(func(context.Context, NativeWatchChange[int]) error { return listenerFailure })
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
	watch := NewNativeWatch(0, func() {}, func(error) {}, nil).On(scheduler)
	watch.Start(func(context.Context, NativeWatchChange[int]) error { return nil })
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
	if watch.detach != nil || watch.report != nil || watch.listening || watch.scheduler != nil || len(watch.values) != 0 || watch.replace != nil {
		t.Fatalf("a stopped watch holds its source: %+v", watch)
	}
	if watch.value != 1 {
		t.Fatalf("last value = %v", watch.value)
	}
}

// A NativeWatch is used through NativeWatchHandle, the interface a source hands out: value, start, stop and closed.
func TestNativeWatchHandleInterface(t *testing.T) {
	scheduler := NewDeliveryScheduler()
	var handle NativeWatchHandle[int] = NewNativeWatch(0, func() {}, func(error) {}, nil).On(scheduler)
	if handle.Value() != 0 {
		t.Fatalf("acquisition value = %d", handle.Value())
	}
	var mu sync.Mutex
	var seen []int
	handle.Start(func(_ context.Context, change NativeWatchChange[int]) error {
		mu.Lock()
		seen = append(seen, change.Value)
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
	watch := NewWatch(c.value, func() {}, func(error) {}).On(c.scheduler)
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
		watch.Advance(ctx, WatchChange[int]{Value: value, Ops: rootOps(float64(value))})
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
	handle.Start(func(_ context.Context, change WatchChange[int]) error {
		mu.Lock()
		values = append(values, change.Value)
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
	watch := NewNativeWatch(c.value, func() {}, func(error) {}, nil).On(c.scheduler)
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
	handle.Start(func(_ context.Context, change NativeWatchChange[*cellValue]) error {
		mu.Lock()
		values = append(values, change.Value)
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

func TestNativeWatchEndAfterDeliveryDeliversWhatIsPendingThenEndsEnded(t *testing.T) {
	scheduler := NewDeliveryScheduler()
	finishing := NewNativeWatch(0, func() {}, func(error) {}, nil).On(scheduler)
	ctx := context.Background()
	finishing.Advance(ctx, 1)
	finishing.Advance(ctx, 2)
	finishing.EndAfterDelivery()
	finishing.Advance(ctx, 3)
	var mu sync.Mutex
	var finished []int
	finishing.Start(func(_ context.Context, change NativeWatchChange[int]) error {
		mu.Lock()
		finished = append(finished, change.Value)
		mu.Unlock()
		return nil
	})
	mustEnd(t, finishing, finishing.End, WatchEnded)
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(finished, []int{1, 2}) {
		t.Fatalf("finished = %v, want [1 2]", finished)
	}
}

// A backlog that never drains is delivered in order: each call queues one more value behind a backlog of 50, so the queue
// never empties for 1,000 values.
func TestNativeWatchDeliversABacklogThatNeverDrainsInOrder(t *testing.T) {
	scheduler := NewDeliveryScheduler()
	watch := NewNativeWatch(0, func() {}, func(error) {}, nil).On(scheduler)
	var mu sync.Mutex
	var values []int
	next := 50
	ctx := context.Background()
	watch.Start(func(_ context.Context, change NativeWatchChange[int]) error {
		mu.Lock()
		values = append(values, change.Value)
		more := next < 1_000
		queued := next
		if more {
			next++
		}
		mu.Unlock()
		if more {
			watch.Advance(ctx, queued)
		}
		return nil
	})
	for n := range 50 {
		watch.Advance(ctx, n)
	}
	scheduler.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(values) != 1_000 {
		t.Fatalf("%d values, want 1000", len(values))
	}
	for n, value := range values {
		if value != n {
			t.Fatalf("value %d is %d", n, value)
		}
	}
	_, _ = watch.Stop()
}

func TestDeliverySchedulerHoldFoldsTheFramesAdvancedWhileItIsOpenIntoOneCallPerWatch(t *testing.T) {
	scheduler := NewDeliveryScheduler()
	first, second := NewWatch[any](0.0, func() {}, func(error) {}).On(scheduler), NewWatch[any](0.0, func() {}, func(error) {}).On(scheduler)
	firstListener, firstChanges := collectChanges()
	secondListener, secondChanges := collectChanges()
	first.Start(firstListener)
	second.Start(secondListener)
	outer := scheduler.Hold()
	inner := scheduler.Hold()
	first.Advance(context.Background(), frame(1.0, rootOps(1.0)))
	second.Advance(context.Background(), frame(1.0, rootOps(1.0)))
	first.Advance(context.Background(), frame(2.0, rootOps(2.0)))
	inner()
	scheduler.Wait()
	if len(firstChanges()) != 0 || len(secondChanges()) != 0 {
		t.Fatal("a delivery started while a hold was open")
	}
	outer()
	scheduler.Wait()
	expectChanges(t, firstChanges(), WatchChange[any]{Value: 2.0, Ops: rootOps(2.0)})
	expectChanges(t, secondChanges(), WatchChange[any]{Value: 1.0, Ops: rootOps(1.0)})
	if first.Value() != 2.0 {
		t.Fatalf("value %v", first.Value())
	}
}
