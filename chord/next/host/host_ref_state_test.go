package host

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/test/next/host/ref-state.test.ts. The own tests of referenceState (on a batch, with the state the
// reference would read (`() => this.refState`) as a variable of the test) come first, each naming the case whose `ref.state`
// behavior it checks; the TestRefState... tests that run a host (`ref.state` of a local reference and of a remote one, and the
// reads and calls that follow the state) are upstream's cases on a real host.

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
	// Each state is delivered before the next move, as the awaits of upstream's test let it.
	h.scheduler.Wait()
	if got := h.state.Value(); got != failed || got.RetryAt == nil || *got.RetryAt != 1_000 {
		t.Fatalf("value = %+v, want the failed state with retryAt 1000", got)
	}
	h.move(refLoading)
	h.scheduler.Wait()
	h.move(refLive)
	h.scheduler.Wait()
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

var refFlaky = mustContract(contracts.Local("app.flaky", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
	Members: []contracts.LocalEntry{
		contracts.Mem("n", contracts.Int()),
		contracts.Mem("get", contracts.NewMethod(contracts.TSchema{}, contracts.Int())),
	},
}))

// flaky is a host that serves `Flaky`, whose open throws while left is above 0 (broken while broken): Pi's `flaky(failing)`.
func flaky(t *testing.T, left int32, broken bool, loop sectionLoop) Host {
	t.Helper()
	host, start := flakyGated(t, left, broken, loop)
	start()
	return host
}

// flakyGated is flaky whose open waits for start. Pi's open is an `await` of the activation: its outcome reaches the
// reference only after the test's first `await`; the open of an owned task would race the test's synchronous reads, so the
// test starts it where Pi's test first awaits.
func flakyGated(t *testing.T, left int32, broken bool, loop sectionLoop) (Host, func()) {
	t.Helper()
	var remaining atomic.Int32
	remaining.Store(left)
	gate := make(chan struct{})
	var once sync.Once
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(refFlaky, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			id := params.Value("id")
			self.Open(func(ctx context.Context) (any, error) {
				select {
				case <-gate:
				case <-ctx.Done():
					return nil, context.Cause(ctx)
				}
				if remaining.Add(-1) < 0 {
					return nil, nil
				}
				if broken {
					return nil, data.Broken(errors.New("no configuration"))
				}
				return nil, errors.New("offline")
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(id)},
				Methods: map[string]contracts.MethodFunction{
					"get": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return id, nil },
				},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop})
	return host, func() { once.Do(func() { close(gate) }) }
}

// "is watched from its current state: failed with retryAt, loading on the next attempt, live; release ends it".
func TestRefStateOfALocalReferenceIsWatchedFromItsCurrentStateFailedWithRetryAtLoadingOnTheNextAttemptLiveReleaseEndsIt(t *testing.T) {
	loop := newScopeLoop()
	host, start := flakyGated(t, 1, false, loop)
	ref := mustHold(t, host, refFlaky, jsonObject("id", 1.0))
	if got := stateOfRef(ref); got != refLoading {
		t.Fatalf("state = %+v, want loading", got)
	}
	watch, err := refStateWatch(ref, background)
	must(t, err)
	if watch.Value() != refLoading {
		t.Fatalf("watch value = %+v, want loading", watch.Value())
	}
	states := statesOf(t, watch)
	start()
	failed := untilKind(t, ref, RefStateFailed)
	if failed.Cause.Kind != data.FailureTransient || failed.Cause.Error.Code != data.CodeUnavailable ||
		failed.Cause.Error.Message != "app.flaky(1): offline" || failed.RetryAt == nil || *failed.RetryAt != 1_000 {
		t.Fatalf("state = %+v (cause %+v), want failed transient offline retrying at 1000", failed, failed.Cause)
	}
	loop.AdvanceAsync(1_000)
	untilKind(t, ref, RefStateLive)
	turn(host)
	ref.Release()
	<-watch.Closed()
	if end := watch.End(); end.Kind != chord.WatchEnded {
		t.Fatalf("watch end = %+v, want ended", end)
	}
	if got, want := states.All(), []string{"failed: app.flaky(1): offline", "loading", "live"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %v, want %v", got, want)
	}
	if ended := stateOfRef(ref); ended.Kind != RefStateEnded || ended.Error.Code != data.CodeReleased {
		t.Fatalf("state after release = %+v, want ended released", ended)
	}
	closeHosts(t, host)
}

// "reads and calls follow the state: not_ready before live, the cause's error while failed, released after".
func TestRefStateOfALocalReferenceReadsAndCallsFollowTheStateNotReadyBeforeLiveTheCausesErrorWhileFailedReleasedAfter(t *testing.T) {
	host, start := flakyGated(t, 1, true, nil)
	ref := mustHold(t, host, refFlaky, jsonObject("id", 1.0))
	if _, err := ref.Member("n").Value(); err == nil || err.Error() != "app.flaky(1): not live yet" {
		t.Fatalf("n before live = %v, want not live yet", err)
	}
	start()
	failed := untilKind(t, ref, RefStateFailed)
	cause := "app.flaky(1): no configuration"
	if failed.Cause.Kind != data.FailureBroken || failed.Cause.Error.Code != data.CodeUnavailable ||
		failed.Cause.Error.Message != cause || failed.RetryAt != nil {
		t.Fatalf("state = %+v, want failed broken %q without retryAt", failed, cause)
	}
	if _, err := ref.Member("n").Value(); err == nil || err.Error() != cause {
		t.Fatalf("n while failed = %v, want %q", err, cause)
	}
	_, err := refCall(ref, "get", nil)
	if got := rejection(t, err); !reflect.DeepEqual(got, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: cause}, nil).ToJSON()) {
		t.Fatalf("get while failed rejected with %v", got)
	}
	ref.Release()
	if _, err := ref.Member("n").Value(); err == nil || err.Error() != "app.flaky: released" {
		t.Fatalf("n after release = %v, want released", err)
	}
	_, err = refCall(ref, "get", nil)
	want := data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Message: "app.flaky: released"}, nil).ToJSON()
	if got := rejection(t, err); !reflect.DeepEqual(got, want) {
		t.Fatalf("get after release rejected with %v, want %v", got, want)
	}
	closeHosts(t, host)
}

// "retry renews a broken object: loading, then live".
func TestRefStateOfALocalReferenceRetryRenewsABrokenObjectLoadingThenLive(t *testing.T) {
	host := flaky(t, 1, true, nil)
	ref := mustHold(t, host, refFlaky, jsonObject("id", 1.0))
	untilKind(t, ref, RefStateFailed)
	// Pi reads the state right after `ref.retry()`, in the same synchronous section: the attempt's open runs in a later
	// turn. Read in a section of its own, the open (a task on another goroutine) may have gone live already.
	var afterRetry *RefState
	ref.binding.loop.Run(func() {
		ref.Retry()
		afterRetry = ref.State().Value()
	})
	if afterRetry.Kind != RefStateLoading {
		t.Fatalf("state after retry = %+v, want loading", afterRetry)
	}
	untilKind(t, ref, RefStateLive)
	got, err := refCall(ref, "get", nil)
	must(t, err)
	if got != 1.0 {
		t.Fatalf("get = %v, want 1", got)
	}
	closeHosts(t, host)
}

// "is made on first access, once; a watch taken after release has ended".
func TestRefStateOfALocalReferenceIsMadeOnFirstAccessOnceAWatchTakenAfterReleaseHasEnded(t *testing.T) {
	host := flaky(t, 0, false, nil)
	ref := mustGet(t, host, refFlaky, jsonObject("id", 1.0))
	if ref.State() != ref.State() {
		t.Fatal("ref.state is not one object")
	}
	if got := stateOfRef(ref); got != refLive {
		t.Fatalf("state = %+v, want live", got)
	}
	ref.Release()
	late, err := refStateWatch(ref, background)
	must(t, err)
	if value := late.Value(); value.Kind != RefStateEnded || value.Error.Code != data.CodeReleased {
		t.Fatalf("late watch value = %+v, want ended released", value)
	}
	<-late.Closed()
	if end := late.End(); end.Kind != chord.WatchEnded {
		t.Fatalf("late watch end = %+v, want ended", end)
	}
	closeHosts(t, host)
}

// "a watch ends cancelled with its context".
func TestRefStateOfALocalReferenceAWatchEndsCancelledWithItsContext(t *testing.T) {
	host := flaky(t, 0, false, nil)
	ref := mustHold(t, host, refFlaky, jsonObject("id", 1.0))
	cancellable, cancel := chordctx.WithCancel(background)
	watch, err := refStateWatch(ref, cancellable)
	must(t, err)
	cancel(nil)
	<-watch.Closed()
	if end := watch.End(); end.Kind != chord.WatchAborted {
		t.Fatalf("watch end = %+v, want aborted", end)
	}
	closeHosts(t, host)
}

// --- ref.state of a remote reference ---------------------------------------------------------------------------------------

func offlineCause(kind string) link.WireCause {
	return link.WireCause{Kind: kind, Error: link.WireErrorOf(data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "offline"}, nil))}
}

func TestRefStateOfARemoteReference(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// "loading until bound, loading while its link is down, failed with retryAt from retryIn, live again".
		t.Run("loading until bound, loading while its link is down, failed with retryAt from retryIn, live again", func(t *testing.T) {
			loop := newScopeLoop()
			p := rawProvider(t, rawLinkedOptions{Loop: loop})
			notes := mustHold(t, p.Client, Notes, jsonObject("session", "S1"))
			watch, err := refStateWatch(notes, background)
			must(t, err)
			states := statesOf(t, watch)
			bindNext(t, p.Peer)
			untilKind(t, notes, RefStateLive)
			p.Peer.Close()
			untilKind(t, notes, RefStateLoading)
			next := rawParent(t, p.Client, link.PartLimits{})
			hold := next.Next("hold").(*link.HoldMessage).Hold
			next.Send(&link.FailedMessage{Hold: hold, Cause: offlineCause("transient"), RetryIn: link.Some(500)})
			failed := untilKind(t, notes, RefStateFailed)
			if failed.Cause.Kind != data.FailureTransient || failed.Cause.Error.Message != "offline" || failed.RetryAt == nil || *failed.RetryAt != 500 {
				t.Fatalf("state = %+v (cause %+v), want failed transient offline retrying at 500", failed, failed.Cause)
			}
			next.Send(&link.LoadingMessage{Hold: hold})
			untilKind(t, notes, RefStateLoading)
			bindHold(t, next, hold)
			untilKind(t, notes, RefStateLive)
			turn(p.Client)
			want := []string{"live", "loading", "failed: offline", "loading", "live"}
			if got := states.All(); !reflect.DeepEqual(got, want) {
				t.Fatalf("states = %v, want %v", got, want)
			}
		})

		// "a failed with retryIn 0 retries now: retryAt is the current time".
		t.Run("a failed with retryIn 0 retries now: retryAt is the current time", func(t *testing.T) {
			loop := newScopeLoop()
			p := rawProvider(t, rawLinkedOptions{Loop: loop})
			loop.Advance(2_000)
			notes := mustHold(t, p.Client, Notes, jsonObject("session", "S1"))
			hold := p.Peer.Next("hold").(*link.HoldMessage).Hold
			p.Peer.Send(&link.FailedMessage{Hold: hold, Cause: offlineCause("transient"), RetryIn: link.Some(0)})
			failed := untilKind(t, notes, RefStateFailed)
			if failed.RetryAt == nil || *failed.RetryAt != 2_000 {
				t.Fatalf("retryAt = %v, want 2000", failed.RetryAt)
			}
		})

		// "variant: undefined before the first binding, the last bound one while loading or failed".
		t.Run("variant: undefined before the first binding, the last bound one while loading or failed", func(t *testing.T) {
			p := rawProvider(t, rawLinkedOptions{})
			notes := mustHold(t, p.Client, Notes, jsonObject("session", "S1"))
			if _, bound, err := notes.Variant(); err != nil || bound {
				t.Fatalf("variant before binding: bound %v, err %v, want unbound", bound, err)
			}
			bindNext(t, p.Peer, jsonObject("text", "a"))
			untilKind(t, notes, RefStateLive)
			if variant, bound, _ := notes.Variant(); !bound || variant != "editor" {
				t.Fatalf("variant = %q (bound %v), want editor", variant, bound)
			}
			p.Peer.Close()
			untilKind(t, notes, RefStateLoading)
			variant, _, _ := notes.Variant()
			if count, err := notes.Member("count").Value(); variant != "editor" || err != nil || count != 1.0 {
				t.Fatalf("variant %q, count %v (%v), want editor and 1", variant, count, err)
			}
			next := rawParent(t, p.Client, link.PartLimits{})
			hold := next.Next("hold").(*link.HoldMessage).Hold
			next.Send(&link.FailedMessage{Hold: hold, Cause: offlineCause("broken")})
			failed := untilKind(t, notes, RefStateFailed)
			if failed.RetryAt != nil {
				t.Fatalf("retryAt = %v, want none", failed.RetryAt)
			}
			if variant, _, _ := notes.Variant(); variant != "editor" {
				t.Fatalf("variant while failed = %q, want editor", variant)
			}
			if _, err := notes.Member("count").Value(); err == nil || err.Error() != "offline" {
				t.Fatalf("count while failed = %v, want offline", err)
			}
			notes.Release()
			if _, _, err := notes.Variant(); err == nil || err.Error() != "app.notes: released" {
				t.Fatalf("variant after release = %v, want released", err)
			}
		})
	})
}
