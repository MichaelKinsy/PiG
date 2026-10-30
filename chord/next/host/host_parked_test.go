package host

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
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
	skew atomic.Int64
}

var _ sectionLoop = (*parkedTestLoop)(nil)

func newParkedTestLoop() *parkedTestLoop {
	return &parkedTestLoop{fakeLoop: newFakeLoop(), real: NewLoop()}
}

func (l *parkedTestLoop) Now() int64      { return l.fakeLoop.Now() + l.skew.Load() }
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
	h.during["block"] = func() { h.loop.skew.Add(30) }
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
		if _, err := parkedResultOf(t, result); !errors.Is(err, released) {
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
	if _, err := parkedResultOf(t, second); !errors.Is(err, released) {
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

// --- upstream parked.test.ts cases that run on hosts ----------------------------------------------------------------

var parkedGated = mustContract(declareLocal("app.gated", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
	Members: []contracts.LocalEntry{contracts.Mem("log", contracts.NewMethod(contracts.Str(), contracts.Int()))},
}))

// skewedLoop is a scopeLoop whose clock a handler can move without any timer firing (`vi.setSystemTime(Date.now() + 30)`).
type skewedLoop struct {
	*scopeLoop
	skew atomic.Int64
}

func (l *skewedLoop) Now() int64 { return l.scopeLoop.Now() + l.skew.Load() }

// gatedHost is `gated()` of parked.test.ts: Gated served with an open that waits for gate; `log` records its argument and
// returns how many it recorded. In its handler a `log` of "release" releases the reference in ref, one of "call" makes the
// call `log("from a handler")` on it, and one of "block" takes 30 ms (the clock moves while no timer can fire).
type gatedHost struct {
	Host   Host
	Loop   *skewedLoop
	gate   chan error
	mu     sync.Mutex
	log    []string
	ref    *Reference
	nested chan error
}

// resolve is `gate.resolve()`; reject is `gate.reject(err)`.
func (g *gatedHost) resolve()         { g.gate <- nil }
func (g *gatedHost) reject(err error) { g.gate <- err }

func (g *gatedHost) logged() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.log...)
}

func gatedLocal(t *testing.T) *gatedHost {
	t.Helper()
	g := &gatedHost{gate: make(chan error, 1), Loop: &skewedLoop{scopeLoop: newScopeLoop()}, nested: make(chan error, 1)}
	g.Host, _ = loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(parkedGated, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			selfOpen(self, func(context.Context) error { return <-g.gate }, func(NativeCloseReason) {})
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"log": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					text := args.(string)
					g.mu.Lock()
					g.log = append(g.log, text)
					n := len(g.log)
					ref := g.ref
					g.mu.Unlock()
					switch text {
					case "release":
						ref.Release()
					case "call":
						go func() { _, err := ref.Call("log", "from a handler", background); g.nested <- err }()
					case "block":
						g.Loop.skew.Add(30)
					}
					return float64(n), nil
				},
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{Loop: g.Loop})
	return g
}

// startLog is `ref.log(text, context)` started in one section: the call, not yet awaited.
func startLog(t testing.TB, ref *Reference, text string, ctx context.Context) *parkedCallResult {
	t.Helper()
	b := ref.binding
	var result *parkedCallResult
	var err error
	b.loop.Run(func() { result, err = b.call("log", text, ctx) })
	if err != nil {
		failed := newParkedCallResult()
		failed.settle(nil, err)
		return failed
	}
	return result
}

// Mirrors parked.test.ts "calls on a loading local reference › are queued and sent in the order they were made once it is live, before later calls".
func TestHostParkedCallsOnALoadingLocalReferenceAreQueuedAndSentInTheOrderTheyWereMade(t *testing.T) {
	g := gatedLocal(t)
	defer closeHosts(t, g.Host)
	ref, err := g.Host.Hold(parkedGated, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	first := startLog(t, ref, "a", background)
	second := startLog(t, ref, "b", background)
	turn(g.Host)
	if got := g.logged(); len(got) != 0 {
		t.Fatalf("log before live = %v, want none", got)
	}
	g.resolve()
	awaitRefState(t, "live", ref, func(s *RefState) bool { return s.Kind == RefStateLive })
	third := startLog(t, ref, "c", background)
	for i, result := range []*parkedCallResult{first, second, third} {
		if got, err := result.wait(); err != nil || got != float64(i+1) {
			t.Fatalf("call %d = %v, %v; want %d", i, got, err, i+1)
		}
	}
	if got := g.logged(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("log = %v, want [a b c]", got)
	}
}

// Mirrors "keep their order when a handler calls the reference while they are sent: its call queues behind them".
func TestHostParkedCallsKeepTheirOrderWhenAHandlerCallsTheReferenceWhileTheyAreSent(t *testing.T) {
	g := gatedLocal(t)
	defer closeHosts(t, g.Host)
	ref, err := g.Host.Hold(parkedGated, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	g.mu.Lock()
	g.ref = ref
	g.mu.Unlock()
	calls := []*parkedCallResult{startLog(t, ref, "call", background), startLog(t, ref, "b", background)}
	g.resolve()
	for _, call := range calls {
		if _, err := call.wait(); err != nil {
			t.Fatal(err)
		}
	}
	must(t, <-g.nested)
	if got := g.logged(); !reflect.DeepEqual(got, []string{"call", "b", "from a handler"}) {
		t.Fatalf("log = %v, want [call b from a handler]", got)
	}
}

// Mirrors "are checked against their deadline when sent: one that passed while earlier ones ran is not sent". Go form: the
// queue's send-time check is TestParkedCallsAreCheckedAgainstTheirDeadlineWhenSent, whose send runs the handler's
// synchronous part (the clock move) before the next call is checked; a host's handler is an owned task that starts after
// its call was admitted, so the clock cannot move between two sends of one section. Here the check runs on the host: a
// call parked past its deadline while the object was still loading is not sent when the object goes live.
func TestHostParkedCallsAreCheckedAgainstTheirDeadlineWhenSent(t *testing.T) {
	g := gatedLocal(t)
	defer closeHosts(t, g.Host)
	ready := 10 * time.Millisecond
	ref, err := g.Host.Hold(parkedGated, jsonObject("id", float64(1)), background, GetOptions{ReadyTimeout: &ready})
	must(t, err)
	expired := startLog(t, ref, "a", background)
	// The clock moves past the deadline without its timer having fired (`vi.setSystemTime`): the timer's job is pending.
	g.Loop.skew.Add(30)
	g.resolve()
	awaitRefState(t, "live", ref, func(s *RefState) bool { return s.Kind == RefStateLive })
	_, err = expired.wait()
	if chordErr, ok := data.IsChordError(err); !ok || chordErr.Code != data.CodeUnavailable || chordErr.Reason != data.ReasonParkTimeout {
		t.Fatalf("call = %v, want unavailable/park_timeout", err)
	}
	if got := g.logged(); len(got) != 0 {
		t.Fatalf("log = %v, want none: the call passed its deadline", got)
	}
}

// Mirrors "reject unavailable/park_timeout at the reference's readyTimeout, measured from each call".
func TestHostParkedCallsRejectAtTheReferencesReadyTimeoutMeasuredFromEachCall(t *testing.T) {
	g := gatedLocal(t)
	defer closeHosts(t, g.Host)
	ready := 100 * time.Millisecond
	ref, err := g.Host.Hold(parkedGated, jsonObject("id", float64(1)), background, GetOptions{ReadyTimeout: &ready})
	must(t, err)
	first := startLog(t, ref, "a", background)
	g.Loop.AdvanceAsync(60)
	second := startLog(t, ref, "b", background)
	g.Loop.AdvanceAsync(40)
	_, err = first.wait()
	wantScopeError(t, err, data.CodeUnavailable, data.ReasonParkTimeout, "app.gated(1): a call waited 100 ms for its reference to go live")
	g.resolve()
	if got, err := second.wait(); err != nil || got != float64(1) {
		t.Fatalf("second = %v, %v; want 1", got, err)
	}
	if got := g.logged(); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("log = %v, want [b]", got)
	}
}

// Mirrors "reject at once when their caller cancels; the others stay queued".
func TestHostParkedCallsRejectAtOnceWhenTheirCallerCancelsTheOthersStayQueued(t *testing.T) {
	g := gatedLocal(t)
	defer closeHosts(t, g.Host)
	ref, err := g.Host.Hold(parkedGated, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	cancellable, cancel := chordctx.WithCancel(background)
	cancelled := startLog(t, ref, "a", cancellable)
	kept := startLog(t, ref, "b", background)
	cancel(errors.New("user cancelled"))
	if _, err := cancelled.wait(); err == nil || err.Error() != "user cancelled" {
		t.Fatalf("cancelled call = %v, want user cancelled", err)
	}
	g.resolve()
	if got, err := kept.wait(); err != nil || got != float64(1) {
		t.Fatalf("kept = %v, %v; want 1", got, err)
	}
	if got := g.logged(); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("log = %v, want [b]", got)
	}
}

// Mirrors "reject released when the reference is released; nothing runs".
func TestHostParkedCallsRejectReleasedWhenTheReferenceIsReleasedNothingRuns(t *testing.T) {
	g := gatedLocal(t)
	defer closeHosts(t, g.Host)
	ref, err := g.Host.Hold(parkedGated, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	call := startLog(t, ref, "a", background)
	ref.Release()
	_, err = call.wait()
	wantScopeError(t, err, data.CodeReleased, "", "app.gated: released")
	g.resolve()
	turn(g.Host)
	if got := g.logged(); len(got) != 0 {
		t.Fatalf("log = %v, want none", got)
	}
}

// Mirrors "reject with the cause when the object fails".
func TestHostParkedCallsRejectWithTheCauseWhenTheObjectFails(t *testing.T) {
	g := gatedLocal(t)
	defer closeHosts(t, g.Host)
	ref, err := g.Host.Hold(parkedGated, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	call := startLog(t, ref, "a", background)
	g.reject(errors.New("offline"))
	_, err = call.wait()
	wantScopeError(t, err, data.CodeUnavailable, "", "app.gated(1): offline")
}

// Mirrors "stop being sent when one's handler releases the reference: the rest reject released (race 15)".
func TestHostParkedCallsStopBeingSentWhenOnesHandlerReleasesTheReference(t *testing.T) {
	g := gatedLocal(t)
	defer closeHosts(t, g.Host)
	ref, err := g.Host.Hold(parkedGated, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	g.mu.Lock()
	g.ref = ref
	g.mu.Unlock()
	first := startLog(t, ref, "release", background)
	second := startLog(t, ref, "b", background)
	g.resolve()
	_, err = second.wait()
	wantScopeError(t, err, data.CodeReleased, "", "app.gated: released")
	_, _ = first.wait()
	if got := g.logged(); !reflect.DeepEqual(got, []string{"release"}) {
		t.Fatalf("log = %v, want [release]", got)
	}
}

// Mirrors "calls on a loading remote reference › keep one deadline across reconnects; nothing is sent for them on either link".
func TestHostParkedCallsOnALoadingRemoteReferenceKeepOneDeadlineAcrossReconnects(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		loop := newScopeLoop()
		p := rawProvider(t, rawLinkedOptions{Loop: loop})
		defer closeHosts(t, p.Client)
		ready := time.Second
		notes, err := p.Client.Hold(Notes, jsonObject("session", "S1"), background, GetOptions{ReadyTimeout: &ready})
		must(t, err)
		call := startNotesCall(t, notes, "wait")
		loop.AdvanceAsync(600)
		p.Peer.Close()
		turn(p.Client)
		next := rawParent(t, p.Client, link.PartLimits{})
		next.Next("hold")
		loop.AdvanceAsync(400)
		_, err = call.wait()
		if chordErr, ok := data.IsChordError(err); !ok || chordErr.Code != data.CodeUnavailable || chordErr.Reason != data.ReasonParkTimeout {
			t.Fatalf("call = %v, want unavailable/park_timeout", err)
		}
		if got := [][]string{p.Peer.Kinds(), next.Kinds()}; !reflect.DeepEqual(got, [][]string{{"hold"}, {}}) {
			t.Fatalf("kinds = %v, want [[hold] []]", got)
		}
	})
}

// startNotesCall is `notes.method(context)` started in one section.
func startNotesCall(t testing.TB, ref *Reference, method string) *parkedCallResult {
	t.Helper()
	b := ref.binding
	var result *parkedCallResult
	var err error
	b.loop.Run(func() { result, err = b.call(method, nil, background) })
	if err != nil {
		failed := newParkedCallResult()
		failed.settle(nil, err)
		return failed
	}
	return result
}

// Mirrors "are resolved against the binding they are sent on: indexes from the new variant, missing methods invalid".
func TestHostParkedCallsOnALoadingRemoteReferenceAreResolvedAgainstTheBindingTheyAreSentOn(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		p := rawProvider(t, rawLinkedOptions{})
		defer closeHosts(t, p.Client)
		notes, err := p.Client.Hold(Notes, jsonObject("session", "S1"), background, GetOptions{})
		must(t, err)
		// Typed for the editor: before its first binding the reference may become either variant.
		add := startCallWith(t, notes, "add", jsonObject("text", "a"))
		wait := startNotesCall(t, notes, "wait")
		hold := p.Peer.Next("hold").(*link.HoldMessage).Hold
		reader := wireNames(t, Notes, "reader")
		editor := wireNames(t, Notes, "editor")
		notesPart, err := link.JsonPart([]any{})
		must(t, err)
		countPart, err := link.JsonPart(float64(0))
		must(t, err)
		p.Peer.Send(&link.BoundMessage{Holds: []link.BoundEntry{{
			Hold: hold, Variant: reader.Variant, Failed: []link.FailedMember{},
			Members: []link.BoundMember{{Member: reader.State["notes"], Value: notesPart}, {Member: reader.State["count"], Value: countPart}},
		}}})
		_, err = add.wait()
		wantScopeError(t, err, data.CodeInvalid, data.ReasonMember, `app.notes(S1) (variant reader) has no method "add"`)
		sent := p.Peer.Next("call").(*link.CallMessage)
		if sent.Method != reader.Methods["wait"] {
			t.Fatalf("method = %d, want the reader's wait (%d)", sent.Method, reader.Methods["wait"])
		}
		if sent.Method == editor.Methods["wait"] {
			t.Fatal("the call went out with the editor's index")
		}
		resultPart, err := link.JsonPart(nil)
		must(t, err)
		p.Peer.Send(&link.ResultMessage{Call: sent.Call, Value: resultPart})
		if got, err := wait.wait(); err != nil || got != nil {
			t.Fatalf("wait = %v, %v; want null", got, err)
		}
	})
}

// startCallWith is `ref.method(args, context)` started in one section.
func startCallWith(t testing.TB, ref *Reference, method string, args delta.JsonValue) *parkedCallResult {
	t.Helper()
	b := ref.binding
	var result *parkedCallResult
	var err error
	b.loop.Run(func() { result, err = b.call(method, args, background) })
	if err != nil {
		failed := newParkedCallResult()
		failed.settle(nil, err)
		return failed
	}
	return result
}

// Mirrors "on a reference released while loading without a link: nothing is sent, the next connect holds nothing (race 3)".
func TestHostParkedCallsOnAReferenceReleasedWhileLoadingWithoutALinkNothingIsSent(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		client := newHost(t, HostOptions{OnError: func(HostError) {}})
		defer closeHosts(t, client)
		must(t, client.Load(nil, background))
		notes, err := client.Hold(Notes, jsonObject("session", "S1"), background, GetOptions{})
		must(t, err)
		other, err := client.Hold(Notes, jsonObject("session", "S2"), background, GetOptions{})
		must(t, err)
		call := startNotesCall(t, notes, "wait")
		notes.Release()
		_, err = call.wait()
		wantScopeError(t, err, data.CodeReleased, "", "app.notes: released")
		peer, ends := rawParentLinks(t, client)
		held := peer.Next("hold").(*link.HoldMessage)
		if got := partValue(t, held.Params); !reflect.DeepEqual(got, jsonObject("session", "S2")) {
			nexttest.RequireJSONEqual(t, got, jsonObject("session", "S2"))
		}
		transported(ends[0], ends[1])
		if kinds := peer.Kinds(); len(kinds) != 0 {
			t.Fatalf("kinds = %v, want none", kinds)
		}
		if kind := other.State().Value().Kind; kind != RefStateLoading {
			t.Fatalf("state = %s, want loading", kind)
		}
	})
}
