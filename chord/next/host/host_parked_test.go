package host

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/test/next/host/parked.test.ts for the part that needs no host. Every case of upstream's file runs a
// host: a served `Gated` type whose open waits on a gate, calls through `ref.log(...)` on a loading local reference, and a
// raw provider or parent peer for the remote ones. They belong to the host core (reference.ts, object.ts) and the remote
// lane. These are own tests of parkedCalls on a fake loop with the sends of a reference replaced by a recording function,
// each naming the case whose queue behavior it checks.

// parkedTestLoop is a fake loop for the queue: fakeLoop's timers and microtasks, a clock a test can set apart from the
// timers (`vi.setSystemTime`), and sections and owned tasks of a real Loop (the abort listener of a caller's context runs on a
// goroutine of its own and enters a section).
type parkedTestLoop struct {
	*fakeLoop
	real *Loop
	// skew is what `vi.setSystemTime` added to the clock: no timer fires for it.
	skew int64
}

var _ sectionLoop = (*parkedTestLoop)(nil)

func newParkedTestLoop() *parkedTestLoop {
	return &parkedTestLoop{fakeLoop: newFakeLoop(), real: NewLoop()}
}

func (l *parkedTestLoop) Now() int64      { return l.fakeLoop.Now() + l.skew }
func (l *parkedTestLoop) Run(work func()) { l.real.Run(work) }
func (l *parkedTestLoop) Go(fn func())    { l.real.Go(fn) }

// parkedHarness is the calls queued on a reference `app.gated(1)`.
type parkedHarness struct {
	t     *testing.T
	loop  *parkedTestLoop
	calls *parkedCalls
	// log is the calls sent, by argument (the handler `log` records its argument and returns how many it recorded).
	log  []string
	live bool
	// during runs in the synchronous part of the handler of each call, by its argument.
	during map[string]func()
}

func newParkedHarness(t *testing.T, timeout time.Duration) *parkedHarness {
	t.Helper()
	loop := newParkedTestLoop()
	return &parkedHarness{t: t, loop: loop, calls: newParkedCalls(loop, timeout, "app.gated(1)"), live: true, during: map[string]func(){}}
}

// in runs work as a section of the loop, as the host's entry points do.
func (h *parkedHarness) in(work func()) { h.loop.Run(work) }

// send is the reference's send: the handler runs now, and its call settles with how many were recorded.
func (h *parkedHarness) send(_ string, args any, _ context.Context) *parkedCallResult {
	text := args.(string)
	h.log = append(h.log, text)
	if during := h.during[text]; during != nil {
		during()
	}
	sent := newParkedCallResult()
	sent.settle(len(h.log), nil)
	return sent
}

func (h *parkedHarness) flush() {
	h.t.Helper()
	h.in(func() { h.calls.flush(h.send, func() bool { return h.live }) })
}

// park queues the call `log(text)`.
func (h *parkedHarness) park(text string, ctx context.Context) *parkedCallResult {
	h.t.Helper()
	var result *parkedCallResult
	h.in(func() {
		var err error
		result, err = h.calls.park("log", text, ctx)
		if err != nil {
			h.t.Fatalf("park(%q): %v", text, err)
		}
	})
	return result
}

func (h *parkedHarness) advance(ms int64) { h.in(func() { h.loop.Advance(ms) }) }

// isSettled reports whether result is settled now, without waiting.
func isSettled(result *parkedCallResult) bool {
	select {
	case <-result.done:
		return true
	default:
		return false
	}
}

func parkedResultOf(t *testing.T, result *parkedCallResult) (any, error) {
	t.Helper()
	if !isSettled(result) {
		t.Fatal("the call is not settled")
	}
	return result.wait()
}

func parkTimeoutOf(t *testing.T, err error) *data.ChordError {
	t.Helper()
	chordErr, ok := data.IsChordErrorCode(err, data.CodeUnavailable)
	if !ok || chordErr.Reason != data.ReasonParkTimeout {
		t.Fatalf("error = %v, want unavailable/park_timeout", err)
	}
	return chordErr
}

// Mirrors "calls on a loading local reference › are queued and sent in the order they were made once it is live, before later
// calls".
func TestParkedCallsAreQueuedAndSentInTheOrderTheyWereMade(t *testing.T) {
	h := newParkedHarness(t, time.Second)
	first, second := h.park("a", chordctx.Background()), h.park("b", chordctx.Background())
	if !h.calls.waiting() {
		t.Fatal("waiting() is false with two calls queued")
	}
	if len(h.log) != 0 {
		t.Fatalf("sent before flush = %v", h.log)
	}
	h.flush()
	third := h.park("c", chordctx.Background())
	// A call sent is settled as the call it becomes a microtask later: not yet.
	if isSettled(first) {
		t.Fatal("a sent call settled before its microtask")
	}
	h.loop.Flush()
	h.flush()
	h.loop.Flush()
	for i, result := range []*parkedCallResult{first, second, third} {
		if value, err := parkedResultOf(t, result); err != nil || value != i+1 {
			t.Fatalf("call %d = %v, %v; want %d, nil", i, value, err, i+1)
		}
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(h.log, want) {
		t.Fatalf("log = %v, want %v", h.log, want)
	}
	if h.calls.waiting() || h.loop.TimerCount() != 0 {
		t.Fatalf("waiting %v timers %d after sending, want false 0", h.calls.waiting(), h.loop.TimerCount())
	}
}

// Mirrors "keep their order when a handler calls the reference while they are sent: its call queues behind them": the call a
// handler makes is queued behind the rest, sent in the same flush, and a new call waits behind calls being sent.
func TestParkedCallsKeepTheirOrderWhenAHandlerCallsWhileTheyAreSent(t *testing.T) {
	h := newParkedHarness(t, time.Second)
	var waitingDuringSend bool
	h.during["call"] = func() {
		waitingDuringSend = h.calls.waiting()
		if _, err := h.calls.park("log", "from a handler", chordctx.Background()); err != nil {
			t.Errorf("park from a handler: %v", err)
		}
	}
	first, second := h.park("call", chordctx.Background()), h.park("b", chordctx.Background())
	h.flush()
	h.loop.Flush()
	if want := []string{"call", "b", "from a handler"}; !reflect.DeepEqual(h.log, want) {
		t.Fatalf("log = %v, want %v", h.log, want)
	}
	if !waitingDuringSend {
		t.Fatal("a call made while the queue was sent did not see it waiting")
	}
	for _, result := range []*parkedCallResult{first, second} {
		if _, err := parkedResultOf(t, result); err != nil {
			t.Fatalf("call error = %v", err)
		}
	}
	if h.calls.waiting() {
		t.Fatal("calls are still queued")
	}
}

// Mirrors "are checked against their deadline when sent: one that passed while earlier ones ran is not sent": the clock moves
// while no timer can fire (`vi.setSystemTime`).
func TestParkedCallsAreCheckedAgainstTheirDeadlineWhenSent(t *testing.T) {
	h := newParkedHarness(t, 10*time.Millisecond)
	h.during["block"] = func() { h.loop.skew += 30 }
	first, second := h.park("block", chordctx.Background()), h.park("b", chordctx.Background())
	h.flush()
	h.loop.Flush()
	if value, err := parkedResultOf(t, first); err != nil || value != 1 {
		t.Fatalf("first = %v, %v; want 1, nil", value, err)
	}
	_, err := parkedResultOf(t, second)
	_ = parkTimeoutOf(t, err)
	if want := []string{"block"}; !reflect.DeepEqual(h.log, want) {
		t.Fatalf("log = %v, want %v", h.log, want)
	}
	if h.calls.waiting() || h.loop.TimerCount() != 0 {
		t.Fatalf("waiting %v timers %d, want false 0", h.calls.waiting(), h.loop.TimerCount())
	}
}

// Mirrors "reject unavailable/park_timeout at the reference's readyTimeout, measured from each call".
func TestParkedCallsRejectAtTheReadyTimeoutMeasuredFromEachCall(t *testing.T) {
	h := newParkedHarness(t, 100*time.Millisecond)
	first := h.park("a", chordctx.Background())
	h.advance(60)
	second := h.park("b", chordctx.Background())
	h.advance(40)
	_, err := parkedResultOf(t, first)
	chordErr := parkTimeoutOf(t, err)
	if want := "app.gated(1): a call waited 100 ms for its reference to go live"; chordErr.Message != want {
		t.Fatalf("message = %q, want %q", chordErr.Message, want)
	}
	if isSettled(second) {
		t.Fatal("the second call left the queue with the first")
	}
	h.flush()
	h.loop.Flush()
	if value, err := parkedResultOf(t, second); err != nil || value != 1 {
		t.Fatalf("second = %v, %v; want 1, nil", value, err)
	}
	if want := []string{"b"}; !reflect.DeepEqual(h.log, want) {
		t.Fatalf("log = %v, want %v", h.log, want)
	}
}

// The deadline's milliseconds are written as JavaScript writes the number: a fraction keeps its digits.
func TestParkedCallsTheTimeoutInTheMessageIsAJavaScriptNumber(t *testing.T) {
	h := newParkedHarness(t, 1500*time.Microsecond)
	result := h.park("a", chordctx.Background())
	h.advance(1)
	_, err := parkedResultOf(t, result)
	if want := "app.gated(1): a call waited 1.5 ms for its reference to go live"; parkTimeoutOf(t, err).Message != want {
		t.Fatalf("error = %v, want message %q", err, want)
	}
}

// Mirrors "reject at once when their caller cancels; the others stay queued".
func TestParkedCallsRejectAtOnceWhenTheirCallerCancels(t *testing.T) {
	h := newParkedHarness(t, time.Second)
	ctx, cancel := chordctx.WithCancel(chordctx.Background())
	cancelled := h.park("a", ctx)
	kept := h.park("b", chordctx.Background())
	reason := errors.New("user cancelled")
	cancel(reason)
	if _, err := cancelled.wait(); !errors.Is(err, reason) {
		t.Fatalf("cancelled call error = %v, want the user's reason", err)
	}
	h.in(func() {
		if !h.calls.waiting() || h.loop.TimerCount() != 1 {
			t.Errorf("waiting %v timers %d after the cancel, want true 1", h.calls.waiting(), h.loop.TimerCount())
		}
	})
	h.flush()
	h.loop.Flush()
	if value, err := parkedResultOf(t, kept); err != nil || value != 1 {
		t.Fatalf("kept = %v, %v; want 1, nil", value, err)
	}
	if want := []string{"b"}; !reflect.DeepEqual(h.log, want) {
		t.Fatalf("log = %v, want %v", h.log, want)
	}
}

// Mirrors `signal?.throwIfAborted()`: a call whose context is aborted already is not queued, and returns the abort reason.
func TestParkedCallsAnAbortedCallerIsNotQueued(t *testing.T) {
	h := newParkedHarness(t, time.Second)
	ctx, cancel := chordctx.WithCancel(chordctx.Background())
	reason := errors.New("gone before it began")
	cancel(reason)
	h.in(func() {
		result, err := h.calls.park("log", "a", ctx)
		if result != nil || !errors.Is(err, reason) {
			t.Errorf("park = %v, %v; want nil, the abort reason", result, err)
		}
		if h.calls.waiting() || h.loop.TimerCount() != 0 {
			t.Errorf("waiting %v timers %d, want false 0", h.calls.waiting(), h.loop.TimerCount())
		}
	})
}

// Mirrors "reject released when the reference is released; nothing runs", and "reject with the cause when the object fails":
// the queue is rejected with the error the reference gives, every call, deadlines and cancellations stopped.
func TestParkedCallsRejectedWithTheQueue(t *testing.T) {
	h := newParkedHarness(t, time.Second)
	ctx, _ := chordctx.WithCancel(chordctx.Background())
	results := []*parkedCallResult{h.park("a", chordctx.Background()), h.park("b", ctx)}
	released := data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Message: "app.gated: released"}, nil)
	h.in(func() { h.calls.reject(released) })
	for i, result := range results {
		if _, err := parkedResultOf(t, result); err != released {
			t.Fatalf("call %d error = %v, want the released error", i, err)
		}
	}
	h.flush()
	if len(h.log) != 0 || h.calls.waiting() || h.loop.TimerCount() != 0 {
		t.Fatalf("log %v waiting %v timers %d, want none, false, 0", h.log, h.calls.waiting(), h.loop.TimerCount())
	}
}

// Mirrors "stop being sent when one's handler releases the reference: the rest reject released (race 15)": the handler of
// the first call releases the reference (it rejects the queue and it is no longer live): the second is not sent.
func TestParkedCallsStopBeingSentWhenAHandlerReleasesTheReference(t *testing.T) {
	h := newParkedHarness(t, time.Second)
	released := data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Message: "app.gated: released"}, nil)
	h.during["release"] = func() {
		h.live = false
		h.calls.reject(released)
	}
	first, second := h.park("release", chordctx.Background()), h.park("b", chordctx.Background())
	h.flush()
	h.loop.Flush()
	if _, err := parkedResultOf(t, second); err != released {
		t.Fatalf("second error = %v, want the released error", err)
	}
	if _, err := parkedResultOf(t, first); err != nil {
		t.Fatalf("first error = %v, want its own result", err)
	}
	if want := []string{"release"}; !reflect.DeepEqual(h.log, want) {
		t.Fatalf("log = %v, want %v", h.log, want)
	}
}

// Own test of `if (!live()) return`: while the reference is not live the rest stays queued, their deadlines running.
func TestParkedCallsStayQueuedWhileTheReferenceIsNotLive(t *testing.T) {
	h := newParkedHarness(t, time.Second)
	result := h.park("a", chordctx.Background())
	h.live = false
	h.flush()
	if len(h.log) != 0 || !h.calls.waiting() || isSettled(result) {
		t.Fatalf("log %v waiting %v settled %v, want none, true, false", h.log, h.calls.waiting(), isSettled(result))
	}
	h.advance(1_000)
	_, err := parkedResultOf(t, result)
	_ = parkTimeoutOf(t, err)
}

// Mirrors "calls on a loading remote reference › keep one deadline across reconnects; nothing is sent for them on either
// link": a deadline is set once, when the call is parked, and a link dropping and coming back sends nothing and renews
// nothing.
func TestParkedCallsKeepOneDeadlineAcrossReconnects(t *testing.T) {
	h := newParkedHarness(t, time.Second)
	result := h.park("a", chordctx.Background())
	h.advance(600)
	// The link drops and a new one holds: the reference is not live, so a flush sends nothing.
	h.live = false
	h.flush()
	h.advance(400)
	_, err := parkedResultOf(t, result)
	_ = parkTimeoutOf(t, err)
	if len(h.log) != 0 {
		t.Fatalf("sent = %v, want nothing", h.log)
	}
}

// Own test of the queue's order after calls left it: forty calls, the odd ones cancelled (enough to compact), then flushed.
func TestParkedCallsKeepTheirOrderAfterManyLeft(t *testing.T) {
	h := newParkedHarness(t, time.Second)
	var kept []*parkedCallResult
	var cancelled []*parkedCallResult
	cancels := map[*parkedCallResult]func(error){}
	for i := range 40 {
		text := string(rune('A' + i))
		if i%5 != 0 {
			ctx, cancel := chordctx.WithCancel(chordctx.Background())
			result := h.park(text, ctx)
			cancels[result] = cancel
			cancelled = append(cancelled, result)
			continue
		}
		kept = append(kept, h.park(text, chordctx.Background()))
	}
	for _, result := range cancelled {
		cancels[result](errors.New("cancelled"))
		if _, err := result.wait(); err == nil {
			t.Fatal("a cancelled call was not rejected")
		}
	}
	h.flush()
	h.loop.Flush()
	var want []string
	for i := 0; i < 40; i += 5 {
		want = append(want, string(rune('A'+i)))
	}
	if !reflect.DeepEqual(h.log, want) {
		t.Fatalf("log = %v, want %v", h.log, want)
	}
	for i, result := range kept {
		if value, err := parkedResultOf(t, result); err != nil || value != i+1 {
			t.Fatalf("kept call %d = %v, %v; want %d", i, value, err, i+1)
		}
	}
}

// Own test of Promise semantics the queue relies on: a result settles once, and `then` runs a microtask after it settled,
// also when it was settled already.
func TestParkedCallResultSettlesOnceAndThenRunsAsAMicrotask(t *testing.T) {
	loop := newFakeLoop()
	result := newParkedCallResult()
	var order []string
	result.then(loop, func(value any, err error) { order = append(order, "then1") })
	result.settle(1, nil)
	result.settle(2, errors.New("late"))
	result.then(loop, func(value any, err error) { order = append(order, "then2") })
	if len(order) != 0 {
		t.Fatalf("callbacks ran before the microtask turn: %v", order)
	}
	loop.Flush()
	if want := []string{"then1", "then2"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if value, err := result.wait(); err != nil || value != 1 {
		t.Fatalf("result = %v, %v; want 1, nil", value, err)
	}
}
