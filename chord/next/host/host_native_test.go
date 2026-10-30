package host

import (
	"context"
	"errors"
	"fmt"
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
)

// Ports packages/chord/test/next/host/native.test.ts for the part that needs no host. Every case of upstream's file runs a
// host (loaded, host.get/hold/close, `self.open` in a served factory); the natives of an attempt, its closing in reverse
// order, the host's close collecting throws, and the object's context belong to the attempt (attempt.ts) and the host core.
// These are own tests of nativeHandle, within and drained on a fake loop, with the behavior each upstream case asserts of
// the handle itself, each naming its case. `facets › cannot open natives` is a type-level check (see tests.json).

// nativeHarness is the owner of the natives of `app.res(1)`: the reports it received and the loop it runs on.
type nativeHarness struct {
	t    *testing.T
	loop *parkedTestLoop
	mu   sync.Mutex
	// reported is what the natives reported, in order.
	reported []HostError
}

func newNativeHarness(t *testing.T) *nativeHarness {
	t.Helper()
	h := &nativeHarness{t: t, loop: newParkedTestLoop()}
	// Every closer is an owned task: a test ends when each of them has returned.
	t.Cleanup(h.loop.real.Wait)
	return h
}

func (h *nativeHarness) owner(position int) nativeOwner {
	return nativeOwner{
		object:   "app.res(1)",
		position: position,
		context:  chordctx.Background(),
		report: func(err HostError) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.reported = append(h.reported, err)
		},
		loop: h.loop,
	}
}

func (h *nativeHarness) reports() []HostError {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]HostError(nil), h.reported...)
}

// async runs call on a goroutine of its own and returns where its result arrives: a blocking call of the host, whose
// caller is off the loop.
func nativeAsync[T any](call func() T) <-chan T {
	result := make(chan T, 1)
	go func() { result <- call() }()
	return result
}

func nativeResultOf[T any](t *testing.T, result <-chan T) T {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("the call did not return")
		var zero T
		return zero
	}
}

// waitForTimers waits until the fake clock has n timers: the blocking call under test made its timer.
func nativeWaitForTimers(t *testing.T, loop *fakeLoop, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for loop.TimerCount() != n {
		if time.Now().After(deadline) {
			t.Fatalf("timers = %d, want %d", loop.TimerCount(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func nativeOpener[T any](value T) func(context.Context) (T, error) {
	return func(context.Context) (T, error) { return value, nil }
}

// readText is what reading a native gave, as the test of Pi pushes it: the value, or the error as its JSON.
func nativeReadText[T any](native Native[T]) string {
	value, err := native.Value()
	if err == nil {
		return fmt.Sprint(value)
	}
	chordErr, ok := data.IsChordError(err)
	if !ok {
		return err.Error()
	}
	return fmt.Sprintf("%s/%s: %s", chordErr.Code, chordErr.Reason, chordErr.Message)
}

// Mirrors "opening › throws not_ready before its open resolved and released once its close started": a reads before it is
// open (in the factory), b in a's opener, a in b's close (b closes first, a is still open) and in a's own close.
func TestNativeThrowsNotReadyBeforeItsOpenResolvedAndReleasedOnceItsCloseStarted(t *testing.T) {
	h := newNativeHarness(t)
	var reads nexttest.Collector[string]
	var a, b *nativeHandle[string]
	a = newNativeHandle(func(context.Context) (string, error) {
		reads.Append(nativeReadText[string](b))
		return "a", nil
	}, func(string, NativeCloseReason, context.Context) error {
		reads.Append(nativeReadText[string](a))
		return nil
	}, time.Second, h.owner(1))
	b = newNativeHandle(nativeOpener("b"), func(string, NativeCloseReason, context.Context) error {
		reads.Append(nativeReadText[string](a))
		return nil
	}, time.Second, h.owner(2))
	// In the factory.
	reads.Append(nativeReadText[string](a))
	for _, native := range []*nativeHandle[string]{a, b} {
		if err := native.open(chordctx.Background()); err != nil {
			t.Fatalf("open: %v", err)
		}
	}
	for _, native := range []*nativeHandle[string]{b, a} {
		if failure := native.close(NativeCloseReason{Kind: NativeCloseEnd}); failure != nil {
			t.Fatalf("close: %+v", failure)
		}
	}
	want := []string{
		// In the factory, and b in a's opener.
		"not_ready/: app.res(1) native #1: not open yet",
		"not_ready/: app.res(1) native #2: not open yet",
		// a in b's close (b closes first), then in a's own close.
		"a",
		"released/closed: app.res(1) native #1: closed",
	}
	if got := reads.Items(); !reflect.DeepEqual(got, want) {
		t.Fatalf("reads = %v, want %v", got, want)
	}
	if reports := h.reports(); len(reports) != 0 {
		t.Fatalf("reports = %v, want none", reports)
	}
}

// Mirrors "opening › a partly failed attempt closes what it opened, with reason end": the second open fails with the
// error it returned, a closes with the reason, and b, never open, closes nothing.
func TestNativeAPartlyFailedAttemptClosesWhatItOpenedWithReasonEnd(t *testing.T) {
	h := newNativeHarness(t)
	var closes nexttest.Collector[string]
	a := newNativeHandle(nativeOpener("a"), func(value string, reason NativeCloseReason, _ context.Context) error {
		closes.Append(value + " " + string(reason.Kind))
		return nil
	}, time.Second, h.owner(1))
	offline := errors.New("offline")
	b := newNativeHandle(func(context.Context) (string, error) { return "", offline }, func(string, NativeCloseReason, context.Context) error {
		closes.Append("b")
		return nil
	}, time.Second, h.owner(2))
	if err := a.open(chordctx.Background()); err != nil {
		t.Fatalf("open a: %v", err)
	}
	if err := b.open(chordctx.Background()); !errors.Is(err, offline) {
		t.Fatalf("open b = %v, want the opener's error", err)
	}
	for _, native := range []*nativeHandle[string]{b, a} {
		if failure := native.close(NativeCloseReason{Kind: NativeCloseEnd}); failure != nil {
			t.Fatalf("close: %+v", failure)
		}
	}
	if want := []string{"a end"}; !reflect.DeepEqual(closes.Items(), want) {
		t.Fatalf("closes = %v, want %v", closes.Items(), want)
	}
}

// Mirrors "opening › aborts the opener's context when the object ends while opening; a late value is closed once (end|close)":
// the native closed while its open is pending is abandoned; the value that arrives late is closed at once, with the reason
// of the close, under the object's context (not the opener's aborted one), and closed once.
func TestNativeALateValueIsClosedOnce(t *testing.T) {
	for _, kind := range []NativeCloseKind{NativeCloseEnd, NativeCloseClose} {
		t.Run(string(kind), func(t *testing.T) {
			h := newNativeHarness(t)
			entered := make(chan struct{})
			late := make(chan string)
			var closes nexttest.Collector[string]
			native := newNativeHandle(func(context.Context) (string, error) {
				close(entered)
				return <-late, nil
			}, func(value string, reason NativeCloseReason, ctx context.Context) error {
				// The close runs under the object's context, not under the opener's aborted one.
				closes.Append(fmt.Sprint(value, " ", reason.Kind, " ", ctx.Err() != nil))
				return nil
			}, time.Second, h.owner(1))
			opener, cancel := chordctx.WithCancel(chordctx.Background())
			opening := nativeAsync(func() error { return native.open(opener) })
			<-entered
			if failure := native.close(NativeCloseReason{Kind: kind}); failure != nil {
				t.Fatalf("close while opening: %+v", failure)
			}
			if got := nativeReadText[string](native); got != "released/closed: app.res(1) native #1: closed" {
				t.Fatalf("value while abandoned = %q, want released", got)
			}
			// The object ended: the opener's context is aborted.
			cancel(errors.New("left"))
			late <- "value"
			if err := nativeResultOf(t, opening); err != nil {
				t.Fatalf("open = %v, want nil", err)
			}
			if failure := native.close(NativeCloseReason{Kind: kind}); failure != nil {
				t.Fatalf("second close: %+v", failure)
			}
			if want := []string{fmt.Sprint("value ", kind, " false")}; !reflect.DeepEqual(closes.Items(), want) {
				t.Fatalf("closes = %v, want %v", closes.Items(), want)
			}
		})
	}
}

// Own test of the failure of a late value's close (`if (failure !== undefined) this.#owner.report(failure)`): it is reported,
// not returned.
func TestNativeAFailingCloseOfALateValueIsReported(t *testing.T) {
	h := newNativeHarness(t)
	entered := make(chan struct{})
	late := make(chan string)
	boom := errors.New("boom")
	native := newNativeHandle(func(context.Context) (string, error) {
		close(entered)
		return <-late, nil
	}, func(string, NativeCloseReason, context.Context) error { return boom }, time.Second, h.owner(3))
	opening := nativeAsync(func() error { return native.open(chordctx.Background()) })
	<-entered
	native.close(NativeCloseReason{Kind: NativeCloseEnd})
	late <- "value"
	if err := nativeResultOf(t, opening); err != nil {
		t.Fatalf("open = %v, want nil", err)
	}
	want := []HostError{{Kind: ErrCloseFailed, Object: "app.res(1)", Native: Position{Kind: PositionNative, Position: 3}, Error: boom}}
	if got := h.reports(); !reflect.DeepEqual(got, want) {
		t.Fatalf("reports = %+v, want %+v", got, want)
	}
}

// Mirrors "closing › collects every throwing close in host.close; an object ending in the background reports each": a close
// that throws is returned as `close_failed` naming the object and the position, with the error it threw; a panic is the
// same throw.
func TestNativeACloseThatThrowsIsReturnedAsCloseFailed(t *testing.T) {
	h := newNativeHarness(t)
	thrown := errors.New("close 1b threw")
	b := newNativeHandle(nativeOpener("b"), func(string, NativeCloseReason, context.Context) error { return thrown }, time.Second, h.owner(2))
	if err := b.open(chordctx.Background()); err != nil {
		t.Fatalf("open: %v", err)
	}
	failure := b.close(NativeCloseReason{Kind: NativeCloseEnd})
	want := &HostError{Kind: ErrCloseFailed, Object: "app.res(1)", Native: Position{Kind: PositionNative, Position: 2}, Error: thrown}
	if !reflect.DeepEqual(failure, want) {
		t.Fatalf("close = %+v, want %+v", failure, want)
	}
	// A close failing is not a timeout: nothing is reported besides.
	if reports := h.reports(); len(reports) != 0 {
		t.Fatalf("reports = %v, want none", reports)
	}
	panicking := newNativeHandle(nativeOpener("a"), func(string, NativeCloseReason, context.Context) error { panic("close 1a threw") }, time.Second, h.owner(1))
	if err := panicking.open(chordctx.Background()); err != nil {
		t.Fatalf("open: %v", err)
	}
	failure = panicking.close(NativeCloseReason{Kind: NativeCloseClose})
	if failure == nil || failure.Kind != ErrCloseFailed || failure.Error == nil || failure.Error.Error() != "close 1a threw" || failure.Native.Position != 1 {
		t.Fatalf("close after a panic = %+v, want close_failed with the panic's message", failure)
	}
}

// Mirrors "closing › timeouts › report a close still running after its closeTimeout, abort its context then, and close the
// others": a close that does not return is reported at its closeTimeout, never before, with its context aborted by an
// `aborted` error naming the native; the native itself was closed at once, and a close that does stop (here, by that
// context) lets the call return.
func TestNativeReportsACloseStillRunningAfterItsCloseTimeoutAndAbortsItsContext(t *testing.T) {
	h := newNativeHarness(t)
	entered := make(chan context.Context, 1)
	native := newNativeHandle(nativeOpener("b"), func(_ string, _ NativeCloseReason, ctx context.Context) error {
		entered <- ctx
		<-ctx.Done()
		return nil
	}, 500*time.Millisecond, h.owner(2))
	if err := native.open(chordctx.Background()); err != nil {
		t.Fatalf("open: %v", err)
	}
	closing := nativeAsync(func() *closeFailed { return native.close(NativeCloseReason{Kind: NativeCloseClose}) })
	closeCtx := <-entered
	nativeWaitForTimers(t, h.loop.fakeLoop, 1)
	h.loop.Advance(499)
	if len(h.reports()) != 0 || closeCtx.Err() != nil {
		t.Fatalf("after 499 ms: reports %v, close context error %v; want none, nil", h.reports(), closeCtx.Err())
	}
	h.loop.Advance(1)
	if failure := nativeResultOf(t, closing); failure != nil {
		t.Fatalf("close = %+v, want nil: a close that timed out is reported, not returned", failure)
	}
	want := []HostError{{Kind: ErrCloseTimeout, Object: "app.res(1)", Native: Position{Kind: PositionNative, Position: 2}}}
	if got := h.reports(); !reflect.DeepEqual(got, want) {
		t.Fatalf("reports = %+v, want %+v", got, want)
	}
	cause := context.Cause(closeCtx)
	chordErr, ok := data.IsChordErrorCode(cause, data.CodeAborted)
	if !ok || chordErr.Message != "app.res(1) native #2: close timed out" {
		t.Fatalf("close context cause = %v, want aborted: app.res(1) native #2: close timed out", cause)
	}
	if got := nativeReadText[string](native); got != "released/closed: app.res(1) native #2: closed" {
		t.Fatalf("value after the close = %q, want released", got)
	}
	if h.loop.TimerCount() != 0 {
		t.Fatalf("timers = %d, want 0", h.loop.TimerCount())
	}
}

// Own test: a close that settles in time reports nothing and leaves no timer; the close runs under the object's context,
// which is not aborted.
func TestNativeACloseInTimeReportsNothingAndLeavesNoTimer(t *testing.T) {
	h := newNativeHarness(t)
	var aborted []bool
	native := newNativeHandle(nativeOpener("v"), func(_ string, _ NativeCloseReason, ctx context.Context) error {
		aborted = append(aborted, ctx.Err() != nil)
		return nil
	}, time.Second, h.owner(1))
	if err := native.open(chordctx.Background()); err != nil {
		t.Fatalf("open: %v", err)
	}
	if failure := native.close(NativeCloseReason{Kind: NativeCloseEnd}); failure != nil {
		t.Fatalf("close = %+v", failure)
	}
	if want := []bool{false}; !reflect.DeepEqual(aborted, want) {
		t.Fatalf("aborted = %v, want %v", aborted, want)
	}
	if reports := h.reports(); len(reports) != 0 || h.loop.TimerCount() != 0 {
		t.Fatalf("reports %v timers %d, want none, 0", reports, h.loop.TimerCount())
	}
}

// Own test of the opener's own throw: a panic in the opener is its rejection, and the native stays opening (Pi's state after
// a rejected open), so a close finds nothing to close.
func TestNativeAnOpenerThatPanicsRejectsTheOpen(t *testing.T) {
	h := newNativeHarness(t)
	closes := 0
	native := newNativeHandle(func(context.Context) (string, error) { panic(errors.New("no luck")) },
		func(string, NativeCloseReason, context.Context) error { closes++; return nil }, time.Second, h.owner(1))
	if err := native.open(chordctx.Background()); err == nil || err.Error() != "no luck" {
		t.Fatalf("open = %v, want no luck", err)
	}
	if got := nativeReadText[string](native); got != "not_ready/: app.res(1) native #1: not open yet" {
		t.Fatalf("value = %q, want not_ready", got)
	}
	if failure := native.close(NativeCloseReason{Kind: NativeCloseEnd}); failure != nil || closes != 0 {
		t.Fatalf("close = %+v closes %d, want nil 0", failure, closes)
	}
}

// Own test of `within`: settled in time, a rejection first, and a timeout that leaves the work running.
func TestWithin(t *testing.T) {
	t.Run("settled", func(t *testing.T) {
		loop := newFakeLoop()
		work := newNativeWork()
		work.settle(nil)
		if result, err := within(loop, work, time.Second); result != withinSettled || err != nil {
			t.Fatalf("within = %v, %v; want settled, nil", result, err)
		}
		if loop.TimerCount() != 0 {
			t.Fatalf("timers = %d, want 0", loop.TimerCount())
		}
	})
	t.Run("rejected first", func(t *testing.T) {
		loop := newFakeLoop()
		work := newNativeWork()
		boom := errors.New("boom")
		waiting := nativeAsync(func() error { _, err := within(loop, work, time.Second); return err })
		nativeWaitForTimers(t, loop, 1)
		work.settle(boom)
		if err := nativeResultOf(t, waiting); !errors.Is(err, boom) {
			t.Fatalf("within error = %v, want boom", err)
		}
		nativeWaitForTimers(t, loop, 0)
	})
	t.Run("timeout, the work runs on", func(t *testing.T) {
		loop := newFakeLoop()
		work := newNativeWork()
		waiting := nativeAsync(func() withinResult { result, _ := within(loop, work, time.Second); return result })
		nativeWaitForTimers(t, loop, 1)
		loop.Advance(999)
		loop.Advance(1)
		if result := nativeResultOf(t, waiting); result != withinTimeout {
			t.Fatalf("within = %v, want timeout", result)
		}
		work.settle(errors.New("late"))
		// A second settle of the work counts for nothing, and a settled one is found settled later.
		if result, err := within(loop, work, time.Second); result != withinSettled || err == nil || err.Error() != "late" {
			t.Fatalf("within after the work settled = %v, %v; want settled, late", result, err)
		}
	})
	t.Run("a promise settled before the timer ran wins", func(t *testing.T) {
		loop := newFakeLoop()
		work := newNativeWork()
		waiting := nativeAsync(func() withinResult { result, _ := within(loop, work, time.Second); return result })
		nativeWaitForTimers(t, loop, 1)
		// Both ready: the work settled in an earlier microtask than the timer's callback.
		work.settle(nil)
		loop.Advance(1_000)
		if result := nativeResultOf(t, waiting); result != withinSettled {
			t.Fatalf("within = %v, want settled", result)
		}
	})
}

// Own test of `drained`: at once when there is nothing to wait for, settled when every piece settled (a rejection counts, as
// `Promise.allSettled` never rejects), a timeout while one runs.
func TestDrained(t *testing.T) {
	loop := newFakeLoop()
	if result := drained(loop, nil, time.Second); result != withinSettled || loop.TimerCount() != 0 {
		t.Fatalf("drained of nothing = %v, timers %d; want settled, 0", result, loop.TimerCount())
	}
	done, failed, running := newNativeWork(), newNativeWork(), newNativeWork()
	done.settle(nil)
	failed.settle(errors.New("failed"))
	if result := drained(loop, []*nativeWork{done, failed}, time.Second); result != withinSettled {
		t.Fatalf("drained of settled work = %v, want settled", result)
	}
	waiting := nativeAsync(func() withinResult { return drained(loop, []*nativeWork{done, running, failed}, time.Second) })
	nativeWaitForTimers(t, loop, 1)
	loop.Advance(1_000)
	if result := nativeResultOf(t, waiting); result != withinTimeout {
		t.Fatalf("drained with work running = %v, want timeout", result)
	}
	running.settle(nil)
	if result := drained(loop, []*nativeWork{done, running, failed}, time.Second); result != withinSettled {
		t.Fatalf("drained once it settled = %v, want settled", result)
	}
}

// --- upstream native.test.ts cases that run on hosts (loaded, host.get/hold/close, `self.open` in a served factory) ----

var nativeRes = mustContract(declareLocal("app.res", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
	Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
}))

// serveRes serves `Res` with factory, which declares its natives; `n` is constant(id).
func serveRes(factory func(id float64, self Self)) func(h FacetHost, _ FacetSelf) error {
	return func(h FacetHost, _ FacetSelf) error {
		return h.Serve(nativeRes, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			id := params.Value("id").(float64)
			factory(id, self)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(id)},
			}}, nil
		}, ServeOptions{})
	}
}

// recorded is a mutex-guarded list of what handlers of other goroutines saw.
type seenList[T any] struct {
	mu   sync.Mutex
	list []T
}

func (r *seenList[T]) add(item T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, item)
}

func (r *seenList[T]) all() []T {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]T(nil), r.list...)
}

func (r *seenList[T]) len() int { return len(r.all()) }

// Mirrors native.test.ts "opening › opens one after another in declaration order; a later opener reads an earlier handle".
func TestHostNativesOpenOneAfterAnotherInDeclarationOrderALaterOpenerReadsAnEarlierHandle(t *testing.T) {
	var events seenList[string]
	first := make(chan string)
	host, _ := loaded(t, serveRes(func(_ float64, self Self) {
		a := self.Open(func(context.Context) (any, error) {
			events.add("open a")
			return <-first, nil
		}, func(any, NativeCloseReason, context.Context) error { return nil })
		self.Open(func(context.Context) (any, error) {
			value, err := a.Value()
			events.add(fmt.Sprintf("open b with %v", value))
			return "b", err
		}, func(any, NativeCloseReason, context.Context) error { return nil })
	}), loadedOptions{})
	defer closeHosts(t, host)
	got := make(chan error, 1)
	go func() {
		_, err := host.Get(nativeRes, jsonObject("id", float64(1)), background, GetOptions{})
		got <- err
	}()
	scopeEventually(t, "open a", func() bool { return events.len() == 1 })
	if !reflect.DeepEqual(events.all(), []string{"open a"}) {
		t.Fatalf("events = %v, want [open a]", events.all())
	}
	first <- "a"
	must(t, <-got)
	if !reflect.DeepEqual(events.all(), []string{"open a", "open b with a"}) {
		t.Fatalf("events = %v, want [open a, open b with a]", events.all())
	}
}

// Mirrors "opening › opens nothing for an object ended before its first open started".
func TestHostNativesOpenNothingForAnObjectEndedBeforeItsFirstOpenStarted(t *testing.T) {
	var opens atomic.Int32
	host, _ := loaded(t, serveRes(func(_ float64, self Self) {
		self.Open(func(context.Context) (any, error) { opens.Add(1); return nil, nil }, func(any, NativeCloseReason, context.Context) error { return nil })
	}), loadedOptions{})
	// Held and released in one synchronous section: its attempt did not start its first open yet.
	h := host.(*chordHost)
	var err error
	h.enter(func() {
		var ref *Reference
		ref, err = host.Hold(nativeRes, jsonObject("id", float64(1)), background, GetOptions{})
		if err == nil {
			ref.Release()
		}
	})
	must(t, err)
	turn(host)
	must(t, host.Close(background))
	if got := opens.Load(); got != 0 {
		t.Fatalf("opens = %d, want 0", got)
	}
}

// Mirrors "opening › throws not_ready before its open resolved and released once its close started".
func TestHostNativesThrowNotReadyBeforeTheirOpenResolvedAndReleasedOnceTheirCloseStarted(t *testing.T) {
	var reads seenList[any]
	read := func(native Native[any]) {
		value, err := native.Value()
		if chordErr, ok := data.IsChordError(err); ok {
			reads.add(chordErr.ToJSON())
		} else if err != nil {
			reads.add(err)
		} else {
			reads.add(value)
		}
	}
	host, _ := loaded(t, serveRes(func(_ float64, self Self) {
		var a, b Native[any]
		a = self.Open(func(context.Context) (any, error) { read(b); return "a", nil },
			func(any, NativeCloseReason, context.Context) error { read(a); return nil })
		b = self.Open(func(context.Context) (any, error) { return "b", nil },
			func(any, NativeCloseReason, context.Context) error { read(a); return nil })
		read(a)
	}), loadedOptions{})
	defer closeHosts(t, host)
	ref, err := host.Get(nativeRes, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	ref.Release()
	scopeEventually(t, "both closes ran", func() bool { return reads.len() == 4 })
	notReady := func(n int) *delta.JsonObject {
		return jsonObject("code", "not_ready", "message", fmt.Sprintf("app.res(1) native #%d: not open yet", n))
	}
	want := []any{
		// In the factory, and b in a's opener.
		notReady(1), notReady(2),
		// a in b's close (b closes first), then in a's own close.
		"a", jsonObject("code", "released", "reason", "closed", "message", "app.res(1) native #1: closed"),
	}
	nexttest.RequireJSONEqual(t, reads.all(), want)
}

// Mirrors "opening › a partly failed attempt closes what it opened, with reason end".
func TestHostNativesAPartlyFailedAttemptClosesWhatItOpenedWithReasonEnd(t *testing.T) {
	var closes seenList[string]
	host, errs := loaded(t, serveRes(func(_ float64, self Self) {
		self.Open(func(context.Context) (any, error) { return "a", nil },
			func(value any, reason NativeCloseReason, _ context.Context) error {
				closes.add(fmt.Sprintf("%v %s", value, reason.Kind))
				return nil
			})
		self.Open(func(context.Context) (any, error) { return nil, errors.New("offline") },
			func(any, NativeCloseReason, context.Context) error { closes.add("b"); return nil })
	}), loadedOptions{})
	defer closeHosts(t, host)
	ready := 10 * time.Millisecond
	_, err := host.Get(nativeRes, jsonObject("id", float64(1)), background, GetOptions{ReadyTimeout: &ready})
	wantScopeError(t, err, data.CodeUnavailable, "", "app.res(1): offline")
	scopeEventually(t, "a closed", func() bool { return closes.len() >= 1 })
	if !reflect.DeepEqual(closes.all(), []string{"a end"}) {
		t.Fatalf("closes = %v, want [a end]", closes.all())
	}
	if got := failures(errs.All()); !reflect.DeepEqual(got, []string{"app.res(1): transient"}) {
		t.Fatalf("failures = %v, want [app.res(1): transient]", got)
	}
}

// Mirrors "opening › aborts the opener's context when the object ends while opening; a late value is closed once (%s)".
func TestHostNativesAbortTheOpenersContextWhenTheObjectEndsWhileOpeningALateValueIsClosedOnce(t *testing.T) {
	for _, reason := range []string{"end", "close"} {
		t.Run(reason, func(t *testing.T) {
			opener := make(chan context.Context, 1)
			late := make(chan string, 1)
			var closes seenList[[3]any]
			host, _ := loaded(t, serveRes(func(_ float64, self Self) {
				self.Open(func(ctx context.Context) (any, error) { opener <- ctx; return <-late, nil },
					// The close runs under the object's context, not under the opener's aborted one.
					func(value any, why NativeCloseReason, ctx context.Context) error {
						closes.add([3]any{value, string(why.Kind), ctx.Err() != nil})
						return nil
					})
			}), loadedOptions{})
			cancellable, cancel := chordctx.WithCancel(background)
			got := make(chan error, 1)
			go func() {
				_, err := host.Get(nativeRes, jsonObject("id", float64(1)), cancellable, GetOptions{})
				got <- err
			}()
			openContext := <-opener
			want := "left"
			if reason == "end" {
				cancel(errors.New("left"))
			} else {
				want = "app.res(1): ended"
				must(t, host.Close(background))
			}
			if err := <-got; err == nil || err.Error() != want {
				t.Fatalf("get = %v, want %q", err, want)
			}
			if openContext.Err() == nil {
				t.Fatal("the opener's context was not aborted")
			}
			late <- "value"
			scopeEventually(t, "the late value closed", func() bool { return closes.len() == 1 })
			must(t, host.Close(background))
			if got, want := closes.all(), [][3]any{{"value", reason, false}}; !reflect.DeepEqual(got, want) {
				t.Fatalf("closes = %v, want %v", got, want)
			}
		})
	}
}

// Mirrors "closing › closes in reverse, with reason end when the object ends and close when the host closes".
func TestHostNativesCloseInReverseWithReasonEndWhenTheObjectEndsAndCloseWhenTheHostCloses(t *testing.T) {
	var closes seenList[string]
	host, _ := loaded(t, serveRes(func(id float64, self Self) {
		for _, name := range []string{"a", "b"} {
			value := fmt.Sprintf("%v%s", id, name)
			self.Open(func(context.Context) (any, error) { return value, nil },
				func(value any, reason NativeCloseReason, _ context.Context) error {
					closes.add(fmt.Sprintf("%v %s", value, reason.Kind))
					return nil
				})
		}
	}), loadedOptions{})
	one, err := host.Get(nativeRes, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	_, err = host.Get(nativeRes, jsonObject("id", float64(2)), background, GetOptions{})
	must(t, err)
	one.Release()
	scopeEventually(t, "object 1 closed", func() bool { return closes.len() == 2 })
	must(t, host.Close(background))
	if want := []string{"1b end", "1a end", "2b close", "2a close"}; !reflect.DeepEqual(closes.all(), want) {
		t.Fatalf("closes = %v, want %v", closes.all(), want)
	}
}

// Mirrors "closing › collects every throwing close in host.close; an object ending in the background reports each".
func TestHostNativesCollectEveryThrowingCloseInHostCloseAnObjectEndingInTheBackgroundReportsEach(t *testing.T) {
	thrown := func(name string) error { return fmt.Errorf("close %s threw", name) }
	host, errs := loaded(t, serveRes(func(id float64, self Self) {
		for _, name := range []string{"a", "b"} {
			label := fmt.Sprintf("%v%s", id, name)
			self.Open(func(context.Context) (any, error) { return name, nil },
				func(any, NativeCloseReason, context.Context) error { return thrown(label) })
		}
	}), loadedOptions{})
	one, err := host.Get(nativeRes, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	_, err = host.Get(nativeRes, jsonObject("id", float64(2)), background, GetOptions{})
	must(t, err)
	one.Release()
	scopeEventually(t, "both closes reported", func() bool { return errs.Len() == 2 })
	reported := errs.All()
	for i, position := range []int{2, 1} {
		got := reported[i]
		want := fmt.Sprintf("close 1%s threw", []string{"b", "a"}[i])
		if got.Kind != ErrCloseFailed || got.Object != "app.res(1)" || got.Native != (Position{Kind: PositionNative, Position: position}) || got.Error == nil || got.Error.Error() != want {
			t.Fatalf("report %d = %+v, want close_failed app.res(1) native %d: %s", i, got, position, want)
		}
	}
	closed := host.Close(background)
	if closed == nil {
		t.Fatal("close returned nil, want the collected closes")
	}
	joined, ok := errors.Unwrap(closed).(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("close error = %T, want a joined error", errors.Unwrap(closed))
	}
	var texts []string
	for _, each := range joined.Unwrap() {
		texts = append(texts, each.Error())
	}
	if want := []string{thrown("2b").Error(), thrown("2a").Error()}; !reflect.DeepEqual(texts, want) {
		t.Fatalf("collected = %v, want %v", texts, want)
	}
}

// Mirrors "closing › timeouts › report a close still running after its closeTimeout, abort its context then, and close the others".
func TestHostNativesReportACloseStillRunningAfterItsCloseTimeoutAbortItsContextAndCloseTheOthers(t *testing.T) {
	loop := newScopeLoop()
	var events seenList[string]
	var slow seenList[context.Context]
	never := make(chan struct{})
	defer close(never)
	closeTimeout := 10 * time.Second
	own := 500 * time.Millisecond
	host, errs := loaded(t, serveRes(func(_ float64, self Self) {
		self.Open(func(context.Context) (any, error) { return "a", nil },
			func(any, NativeCloseReason, context.Context) error { events.add("close a"); return nil })
		self.Open(func(context.Context) (any, error) { return "b", nil },
			func(_ any, _ NativeCloseReason, ctx context.Context) error { slow.add(ctx); <-never; return nil },
			NativeOptions{CloseTimeout: &own})
	}), loadedOptions{CloseTimeout: &closeTimeout, Loop: loop})
	defer closeHosts(t, host)
	ref, err := host.Get(nativeRes, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	ref.Release()
	scopeEventually(t, "the slow close started", func() bool { return slow.len() == 1 })
	loop.AdvanceAsync(499)
	if events.len() != 0 || slow.all()[0].Err() != nil {
		t.Fatalf("after 499 ms: events = %v, aborted = %v; want none and false", events.all(), slow.all()[0].Err() != nil)
	}
	loop.AdvanceAsync(1)
	scopeEventually(t, "close a", func() bool { return events.len() == 1 })
	if slow.all()[0].Err() == nil {
		t.Fatal("the slow close's context was not aborted at its closeTimeout")
	}
	want := []HostError{{Kind: ErrCloseTimeout, Object: "app.res(1)", Native: Position{Kind: PositionNative, Position: 2}}}
	if got := errs.All(); !reflect.DeepEqual(got, want) {
		t.Fatalf("errors = %+v, want %+v", got, want)
	}
}
