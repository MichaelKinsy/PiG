package host

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/test/next/host/ref-state.test.ts for the part that needs no host. Every case of upstream's file runs
// a host (createHost/loaded, host.hold, a raw provider or parent peer): `ref.state` of a local reference and of a remote
// one, and the reads and calls that follow the state. These are own tests of referenceState, on a batch, with the state
// the reference would read (`() => this.refState`) as a variable of the test; each names the case whose `ref.state`
// behavior it checks.

// refStateHarness is a reference state reading a variable, on a batch with a scheduler of its own.
type refStateHarness struct {
	t         *testing.T
	current   *RefState
	scheduler *chord.DeliveryScheduler
	batch     *state.Batch
	state     *referenceState
}

func newRefStateHarness(t *testing.T, initial *RefState) *refStateHarness {
	t.Helper()
	h := &refStateHarness{t: t, current: initial, scheduler: chord.NewDeliveryScheduler()}
	h.batch = state.NewBatch(func(err state.BatchError) { t.Errorf("batch reported %s: %v", err.Kind, err.Error) }, nil).UseScheduler(h.scheduler)
	h.state = newReferenceState(func() *RefState { return h.current }, h.batch, h.scheduler)
	return h
}

// move is the reference's state changing: its owner tells `ref.state`.
func (h *refStateHarness) move(next *RefState) {
	h.current = next
	h.state.moved()
}

func (h *refStateHarness) watch(ctx context.Context) chord.NativeWatchHandle[*RefState] {
	h.t.Helper()
	watch, err := h.state.Watch(ctx)
	if err != nil {
		h.t.Fatalf("Watch: %v", err)
	}
	return watch
}

func refStateTransientFailure(message string, retryAt int64) *RefState {
	cause := data.FailureCause{Kind: data.FailureTransient, Error: data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: message}, nil)}
	return &RefState{Kind: RefStateFailed, Cause: cause, RetryAt: &retryAt}
}

func refStateReleased() *RefState {
	return &RefState{Kind: RefStateEnded, Error: data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Message: "app.flaky: released"}, nil)}
}

// statesOf starts watch and collects its states (support.ts `statesOf`): the kind, a failed one with its cause's message.
func refStatesOf(watch chord.NativeWatchHandle[*RefState]) *nexttest.Collector[string] {
	states := &nexttest.Collector[string]{}
	watch.Start(func(_ context.Context, call chord.NativeWatchCall[*RefState]) error {
		if call.Kind == chord.WatchCallValue {
			states.Append(refStateText(call.Value))
		}
		return nil
	})
	return states
}

func refStateText(s *RefState) string {
	if s.Kind == RefStateFailed {
		return "failed: " + s.Cause.Error.Message
	}
	return string(s.Kind)
}

func refClosedWith(t *testing.T, watch chord.NativeWatchHandle[*RefState]) chord.WatchEnd {
	t.Helper()
	select {
	case <-watch.Closed():
		return watch.End()
	case <-time.After(5 * time.Second):
		t.Fatal("the watch did not end")
		return chord.WatchEnd{}
	}
}

// Mirrors "ref.state of a local reference › is watched from its current state: failed with retryAt, loading on the next
// attempt, live; release ends it".
func TestRefStateIsWatchedFromItsCurrentStateAndReleaseEndsIt(t *testing.T) {
	h := newRefStateHarness(t, refLoading)
	if h.state.Value() != refLoading {
		t.Fatalf("value = %v, want the loading state", h.state.Value())
	}
	watch := h.watch(chordctx.Background())
	if watch.Value() != refLoading {
		t.Fatalf("watch.Value() = %v, want the loading state", watch.Value())
	}
	states := refStatesOf(watch)
	failed := refStateTransientFailure("app.flaky(1): offline", 1_000)
	h.move(failed)
	if got := h.state.Value(); got != failed || got.RetryAt == nil || *got.RetryAt != 1_000 {
		t.Fatalf("value = %+v, want the failed state with retryAt 1000", got)
	}
	h.move(refLoading)
	h.move(refLive)
	released := refStateReleased()
	h.current = released
	h.state.released(released)
	if end := refClosedWith(t, watch); end.Kind != chord.WatchEnded {
		t.Fatalf("watch end = %v, want ended", end.Kind)
	}
	h.scheduler.Wait()
	if want := []string{"failed: app.flaky(1): offline", "loading", "live"}; !reflect.DeepEqual(states.Items(), want) {
		t.Fatalf("states = %v, want %v", states.Items(), want)
	}
	if got := h.state.Value(); got.Kind != RefStateEnded || got.Error.Code != data.CodeReleased {
		t.Fatalf("value = %+v, want ended released", got)
	}
}

// Mirrors "a watch taken after release has ended" (ref.state of a local reference, "is made on first access, once; a watch
// taken after release has ended"): an ended reference keeps no host, and its watch is over at once.
func TestRefStateAWatchTakenAfterReleaseHasEnded(t *testing.T) {
	h := newRefStateHarness(t, refLive)
	released := refStateReleased()
	h.current = released
	h.state.released(released)
	if h.state.Batch() != nil {
		t.Fatal("a released reference state keeps its batch")
	}
	late := h.watch(chordctx.Background())
	if got := late.Value(); got.Kind != RefStateEnded || got.Error.Code != data.CodeReleased {
		t.Fatalf("late watch value = %+v, want ended released", got)
	}
	if end := refClosedWith(t, late); end.Kind != chord.WatchEnded {
		t.Fatalf("late watch end = %v, want ended", end.Kind)
	}
}

// An ended reference's state that still has a batch (a reference that ended before its state was made) gives an ended watch
// too, and keeps nothing of it.
func TestRefStateAnEndedStateGivesAnEndedWatch(t *testing.T) {
	h := newRefStateHarness(t, refStateReleased())
	watch := h.watch(chordctx.Background())
	if end := refClosedWith(t, watch); end.Kind != chord.WatchEnded {
		t.Fatalf("watch end = %v, want ended", end.Kind)
	}
	if watches := len(h.state.snapshot()); watches != 0 {
		t.Fatalf("watches kept = %d, want 0", watches)
	}
}

// Mirrors "a watch ends cancelled with its context".
func TestRefStateAWatchEndsAbortedWithItsContext(t *testing.T) {
	h := newRefStateHarness(t, refLoading)
	ctx, cancel := chordctx.WithCancel(chordctx.Background())
	watch := h.watch(ctx)
	cancel(nil)
	if end := refClosedWith(t, watch); end.Kind != chord.WatchAborted {
		t.Fatalf("watch end = %v, want aborted", end.Kind)
	}
	if watches := len(h.state.snapshot()); watches != 0 {
		t.Fatalf("watches kept = %d, want 0", watches)
	}
}

// Own test of "each watch gets every new state, in its own context": the frame of each watch carries the context it was
// taken in.
func TestRefStateEachWatchGetsEveryNewStateInItsOwnContext(t *testing.T) {
	h := newRefStateHarness(t, refLoading)
	key := chordctx.NewKey[string]("watcher")
	var order nexttest.Collector[string]
	for _, name := range []string{"first", "second"} {
		watch := h.watch(chordctx.WithValue(chordctx.Background(), key, name))
		watch.Start(func(ctx context.Context, call chord.NativeWatchCall[*RefState]) error {
			who, _ := chordctx.Value(ctx, key)
			order.Append(who + " " + refStateText(call.Value))
			return nil
		})
	}
	h.move(refLive)
	h.move(refStateTransientFailure("offline", 500))
	h.scheduler.Wait()
	// Each watch saw both states, in order; the two watches' calls interleave as the scheduler runs them.
	byWatcher := map[string][]string{}
	for _, entry := range order.Items() {
		for _, name := range []string{"first", "second"} {
			if len(entry) > len(name) && entry[:len(name)+1] == name+" " {
				byWatcher[name] = append(byWatcher[name], entry[len(name)+1:])
			}
		}
	}
	want := []string{"live", "failed: offline"}
	for _, name := range []string{"first", "second"} {
		if !reflect.DeepEqual(byWatcher[name], want) {
			t.Errorf("%s watch saw %v, want %v", name, byWatcher[name], want)
		}
	}
}

// Own test of the identity rule ("a change of state is a new object (a native watch compares by identity)"): the same
// state announces nothing, another object with the same content does.
func TestRefStateAnnouncesOnlyAnotherStateObject(t *testing.T) {
	h := newRefStateHarness(t, refLoading)
	states := refStatesOf(h.watch(chordctx.Background()))
	h.move(refLoading)
	h.move(refLoading)
	h.scheduler.Wait()
	if n := states.Len(); n != 0 {
		t.Fatalf("calls after the same state = %d, want 0", n)
	}
	first := refStateTransientFailure("offline", 1)
	h.move(first)
	h.move(first)
	h.move(refStateTransientFailure("offline", 1))
	h.scheduler.Wait()
	if want := []string{"failed: offline", "failed: offline"}; !reflect.DeepEqual(states.Items(), want) {
		t.Fatalf("states = %v, want %v", states.Items(), want)
	}
}

// dependentCounter is a derive reading the reference state: it counts its marks.
type dependentCounter struct{ marks int }

func (d *dependentCounter) Invalidate()                                { d.marks++ }
func (d *dependentCounter) SourceRetired(map[state.Dependent]struct{}) {}

// Own test of "Derives reading it are marked in a batch of its own when it changes", and a last time when it ends.
func TestRefStateMarksTheDerivesReadingIt(t *testing.T) {
	h := newRefStateHarness(t, refLoading)
	derive := &dependentCounter{}
	h.state.DependentSet().Add(derive)
	h.move(refLive)
	h.move(refLive)
	if derive.marks != 1 {
		t.Fatalf("marks after a change and a repeat = %d, want 1", derive.marks)
	}
	released := refStateReleased()
	h.current = released
	h.state.released(released)
	if derive.marks != 2 {
		t.Fatalf("marks after the release = %d, want 2", derive.marks)
	}
	if h.state.Batch() != nil || h.state.DependentSet().Len() != 0 {
		t.Fatal("an ended reference state keeps its batch or its derives")
	}
}

// Own test of `batch.assertOutside("watch")`: a watch taken inside a commit is refused, invalid/reentrant.
func TestRefStateAWatchInsideACommitIsRefused(t *testing.T) {
	h := newRefStateHarness(t, refLoading)
	var watchErr error
	if err := h.batch.Commit(func() error {
		_, watchErr = h.state.Watch(chordctx.Background())
		return nil
	}, chordctx.Background()); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if chordErr, ok := data.IsChordErrorCode(watchErr, data.CodeInvalid); !ok || chordErr.Reason != data.ReasonReentrant {
		t.Fatalf("watch inside a commit = %v, want invalid/reentrant", watchErr)
	}
}

// Own test of `batch === undefined`: a reference state made without a host gives an ended watch at its state.
func TestRefStateWithoutABatchGivesAnEndedWatch(t *testing.T) {
	rs := newReferenceState(func() *RefState { return refLive }, nil, nil)
	watch, err := rs.Watch(chordctx.Background())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if watch.Value() != refLive {
		t.Fatalf("watch.Value() = %v, want the live state", watch.Value())
	}
	if end := refClosedWith(t, watch); end.Kind != chord.WatchEnded {
		t.Fatalf("watch end = %v, want ended", end.Kind)
	}
}

// Own test of the singletons ("the states without fields, one object each"): LOADING and LIVE are two distinct objects, and
// each waiting state is a new one.
func TestRefStateSingletonsAreDistinctObjects(t *testing.T) {
	if refLoading == refLive || refLoading.Kind != RefStateLoading || refLive.Kind != RefStateLive {
		t.Fatalf("refLoading %+v refLive %+v", refLoading, refLive)
	}
	a, b := waitingOn("x", data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "m"}, nil)), waitingOn("x", data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "m"}, nil))
	if a == b {
		t.Fatal("two waiting states are one object")
	}
}
