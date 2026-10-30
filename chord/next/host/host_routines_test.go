package host

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/test/next/host/routines.test.ts: routines (§14), `on` (the first call with the current value,
// folding, no overlap, retries after a throw with the whole value, retirements, the source ending or failing, native
// sources), `onLive` (returning, retries, the backoff reset after a long run, aborts), the drain and `host.close`, and what
// routines leave behind. Every case runs on a real host, as upstream's `store(...)` and `loaded(...)` do; time is the fake
// clock of Pi's `fakeTimers()` (scopeLoop), moved a timer at a time with the handlers' tasks settled in between
// (routinesEnv.advance: vi.advanceTimersByTimeAsync).
//
// Go forms. A handler is `func(call, ctx) *Handled`: nil is Pi's `async () => void`, Threw(err) an async handler that
// threw, and what Pi awaits (a gate, a transaction) is routinesEnv.awaiting, a blocking function on an owned task that
// settles the handler's Handled. Reading `Date.now()` in a handler is the loop's fake clock.

var routinesDoc = func() *documents.StateDefinition {
	definition, err := documents.DefineState("doc", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("n", 0.0) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

// routinesStore is a host-lifetime object with a counter document and `bump`; its factory declares routines.
var routinesStore = mustContract(contracts.Local("app.store", contracts.LocalDeclaration{
	Version: 1,
	Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int()), contracts.Mem("bump", contracts.NewMethod())},
}))

// routinesItem is the same with params: it lives while referenced.
var routinesItem = mustContract(contracts.Local("app.item", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
	Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int()), contracts.Mem("bump", contracts.NewMethod())},
}))

// bumpDoc counts one up in one transaction.
func bumpDoc(doc *singletonDoc, ctx context.Context) error {
	return doc.Change(func(draft *delta.Object) {
		n, _ := draft.Get("n").(float64)
		_ = draft.Set("n", n+1)
	}, ctx)
}

// setDoc sets n in one transaction.
func setDoc(doc *singletonDoc, n float64, ctx context.Context) error {
	return doc.Change(func(draft *delta.Object) { _ = draft.Set("n", n) }, ctx)
}

// taskCount counts the handler tasks of a test that are running (not parked on a gate): what the test's quiet waits for.
type taskCount struct {
	mu   sync.Mutex
	cond *sync.Cond
	n    int
}

func (c *taskCount) add(by int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cond == nil {
		c.cond = sync.NewCond(&c.mu)
	}
	c.n += by
	if c.n == 0 {
		c.cond.Broadcast()
	}
}

func (c *taskCount) idle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cond == nil {
		c.cond = sync.NewCond(&c.mu)
	}
	for c.n > 0 {
		c.cond.Wait()
	}
}

// gate is Pi's `Promise.withResolvers<void>()` a handler awaits and the test resolves: a handler task parked on it is not
// running, and firing it makes the tasks that wait on it running again before it returns, so the test's quiet sees them.
type routineGate struct {
	env     *routinesEnv
	mu      sync.Mutex
	fired   bool
	waiters int
	ch      chan struct{}
}

func (e *routinesEnv) newGate() *routineGate { return &routineGate{env: e, ch: make(chan struct{})} }

// wait parks the calling handler task until the gate fires.
func (g *routineGate) wait(t testing.TB) {
	g.mu.Lock()
	if g.fired {
		g.mu.Unlock()
		return
	}
	g.waiters++
	g.env.act.add(-1)
	g.mu.Unlock()
	select {
	case <-g.ch:
	case <-time.After(hangGuard):
		t.Error("the awaited gate never fired")
	}
}

// waitMain waits for the gate on the test's own goroutine.
func (g *routineGate) waitMain(t testing.TB) {
	t.Helper()
	select {
	case <-g.ch:
	case <-time.After(hangGuard):
		t.Fatal("the awaited gate never fired")
	}
}

// fire resolves the gate.
func (g *routineGate) fire() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fired {
		return
	}
	g.fired = true
	g.env.act.add(g.waiters)
	g.waiters = 0
	close(g.ch)
}

// routinesEnv is one test's host: its fake clock, its errors, and the handlers' tasks it waits for.
type routinesEnv struct {
	t       *testing.T
	loop    *scopeLoop
	host    Host
	errs    *hostErrors
	ref     *Reference
	act     taskCount
	started atomic.Int64
}

func newRoutinesEnv(t *testing.T) *routinesEnv {
	t.Helper()
	return &routinesEnv{t: t, loop: newScopeLoop()}
}

// implementation is what the factories of `Store` and `Item` return: the document's `n` and `bump`.
func (e *routinesEnv) implementation(self Self, doc *singletonDoc) contracts.Implementation {
	return contracts.Implementation{Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{
			"n": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
				return values[0].(*delta.JsonObject).Value("n"), nil
			}),
		},
		Methods: map[string]contracts.MethodFunction{
			"bump": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return nil, bumpDoc(doc, ctx) },
		},
	}}
}

// store loads a host serving `Store`; declare declares its routines over its document. It gets the reference to it.
func (e *routinesEnv) store(closeTimeout *time.Duration, declare func(doc *singletonDoc, self Self)) {
	e.t.Helper()
	e.host, e.errs = loaded(e.t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(routinesStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, routinesDoc)
			declare(doc, self)
			return e.implementation(self, doc), nil
		}, ServeOptions{})
	}, loadedOptions{Loop: e.loop, CloseTimeout: closeTimeout})
	e.ref = mustGet(e.t, e.host, routinesStore, jsonObject())
}

// item loads a host serving `Item` and gets object 1.
func (e *routinesEnv) item(closeTimeout *time.Duration, declare func(doc *singletonDoc, self Self)) {
	e.t.Helper()
	e.host, e.errs = loaded(e.t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(routinesItem, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, routinesDoc)
			declare(doc, self)
			return e.implementation(self, doc), nil
		}, ServeOptions{})
	}, loadedOptions{Loop: e.loop, CloseTimeout: closeTimeout})
	e.ref = mustGet(e.t, e.host, routinesItem, jsonObject("id", 1.0))
}

// bump is `await ref.bump(context)`.
func (e *routinesEnv) bump() {
	e.t.Helper()
	_, err := e.ref.Call("bump", nil, background)
	must(e.t, err)
}

// awaiting is what a handler awaits: fn runs on an owned task and settles the handler's Handled; the test's quiet waits for
// it while it runs (not while it is parked on a gate).
func (e *routinesEnv) awaiting(self Self, fn func() error) *Handled {
	loop := self.(*attempt).loop()
	e.act.add(1)
	e.started.Add(1)
	handled := Pending(loop)
	loop.Go(func() {
		err := fn()
		loop.Run(func() {
			if err != nil {
				handled.Reject(err)
				return
			}
			handled.Resolve()
		})
		e.act.add(-1)
	})
	return handled
}

// parked is what a handler awaits that only an abort or the clock ends (a sleep, its context): fn runs on an owned task the
// test's quiet does not wait for.
func (e *routinesEnv) parked(self Self, fn func() error) *Handled {
	return Awaiting(self.(*attempt).loop(), fn)
}

// quiet is `await flushed()`: every handler task finished or parked, every delivery made, and what they started too.
func (e *routinesEnv) quiet() {
	for {
		started := e.started.Load()
		e.act.idle()
		turn(e.host)
		if e.started.Load() == started {
			return
		}
	}
}

// nextTimerAt is when the first pending timer is due.
func (e *routinesEnv) nextTimerAt() (int64, bool) {
	l := e.loop.fakeLoop
	l.mu.Lock()
	defer l.mu.Unlock()
	at, found := int64(0), false
	for _, timer := range l.timers {
		if !found || timer.at < at {
			at, found = timer.at, true
		}
	}
	return at, found
}

// advance is `await vi.advanceTimersByTimeAsync(ms)`: a timer at a time, with the handlers' tasks settled before the next.
func (e *routinesEnv) advance(ms int64) {
	e.t.Helper()
	e.quiet()
	end := e.loop.Now() + ms
	for {
		at, found := e.nextTimerAt()
		if !found || at > end {
			break
		}
		e.loop.AdvanceAsync(at - e.loop.Now())
		e.quiet()
	}
	e.loop.AdvanceAsync(end - e.loop.Now())
	e.quiet()
}

// settled is `await settled()`: a macrotask and a little (the call after a busy one waits for one).
func (e *routinesEnv) settled() { e.advance(5) }

// timers is `vi.getTimerCount()`.
func (e *routinesEnv) timers() int { return e.loop.TimerCount() }

// close closes the host.
func (e *routinesEnv) close() { closeHosts(e.t, e.host) }

// reports are the routine reports among the host's errors, as `kind #position: message`.
func (e *routinesEnv) reports() []string {
	var out []string
	for _, report := range e.errs.All() {
		if report.Kind == ErrRoutineFailed {
			out = append(out, fmt.Sprintf("%s #%d: %s", report.Routine.Kind, report.Routine.Position, report.Error.Error()))
		}
	}
	return out
}

// routineCall is what an `on` handler got.
type routineCall struct {
	value   any
	ops     []delta.Op
	retired bool
	at      int64
}

// routineLog is a handler's record of its calls.
type routineLog struct {
	mu    sync.Mutex
	calls []routineCall
}

func (l *routineLog) add(call routineCall) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, call)
	return len(l.calls)
}

func (l *routineLog) all() []routineCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.calls)
}

func (l *routineLog) len() int { return len(l.all()) }

func (l *routineLog) values() []any {
	var out []any
	for _, call := range l.all() {
		out = append(out, call.value)
	}
	return out
}

// handler is an `on` handler that records its calls (with the fake clock) and then runs after (nil: none), whose result is
// the handler's.
func (e *routinesEnv) handler(
	self Self, log *routineLog, after func(n int, call chord.WatchCall[delta.JsonValue], ctx Ctx) *Handled,
) OnHandler[delta.JsonValue] {
	loop := self.(*attempt).loop()
	return func(call chord.WatchCall[delta.JsonValue], ctx Ctx) *Handled {
		n := log.add(routineCall{value: call.Value, ops: call.Ops, retired: call.Retired, at: loop.Now()})
		if after == nil {
			return nil
		}
		return after(n, call, ctx)
	}
}

// nObject is `{ n }`; replaceOps is `[["r", value]]`; setN is `[["s", ["n"], n]]`.
func nObject(n float64) *delta.JsonObject { return delta.JsonObjectOf("n", n) }
func replaceOps(value any) []delta.Op     { return []delta.Op{{"r", value}} }
func setN(n float64) delta.Op             { return delta.Op{"s", []any{"n"}, n} }

func opsAsAny(ops []delta.Op) []any {
	out := make([]any, len(ops))
	for i, op := range ops {
		out[i] = op
	}
	return out
}

// wantCall is wantCalls' shorthand; at < 0 is not compared.
func wantCall(value any, ops []delta.Op, at int64) routineCall {
	return routineCall{value: value, ops: ops, at: at}
}

// wantCalls checks the value, ops, retirement and time (when given) of every call.
func wantCalls(t testing.TB, got []routineCall, want ...routineCall) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("calls = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		same := nexttest.JSONEqual(got[i].value, want[i].value) && got[i].retired == want[i].retired &&
			(want[i].ops == nil || nexttest.JSONEqual(opsAsAny(got[i].ops), opsAsAny(want[i].ops)))
		if want[i].at >= 0 && got[i].at != want[i].at {
			same = false
		}
		if !same {
			t.Fatalf("call %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func wantStrings(t testing.TB, got []string, want ...string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// jsonFake is Pi's `jsonSource`: a JSON watchable (Chord's shared Watch) the test advances by hand, as a Durable document
// would. Its watches deliver on the scheduler of the host that uses it (use).
type jsonFake struct {
	mu      sync.Mutex
	value   any
	sched   *chord.DeliveryScheduler
	watches []*chord.Watch[delta.JsonValue]
}

func newJSONFake(initial any) *jsonFake { return &jsonFake{value: initial} }

// use gives the source the delivery scheduler of self's host.
func (s *jsonFake) use(self Self) {
	s.mu.Lock()
	s.sched = self.(*attempt).object.host.scheduler
	s.mu.Unlock()
}

func (s *jsonFake) Value() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

func (s *jsonFake) Watch(ctx context.Context) (chord.WatchHandle[delta.JsonValue], error) {
	s.mu.Lock()
	var watch *chord.Watch[delta.JsonValue]
	watch = chord.NewWatch[delta.JsonValue](s.value, func() { s.drop(watch) }, func(error) {})
	if s.sched != nil {
		watch.On(s.sched)
	}
	s.watches = append(s.watches, watch)
	s.mu.Unlock()
	if ctx.Done() != nil {
		watch.ObserveAbort(ctx)
	}
	return watch, nil
}

func (s *jsonFake) drop(watch *chord.Watch[delta.JsonValue]) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at := slices.Index(s.watches, watch); at >= 0 {
		s.watches = slices.Delete(s.watches, at, at+1)
	}
}

func (s *jsonFake) snapshot() []*chord.Watch[delta.JsonValue] {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.watches)
}

// advance is Pi's `advance(call)`.
func (s *jsonFake) advance(call chord.WatchCall[delta.JsonValue]) {
	if call.Kind == chord.WatchCallValue {
		s.mu.Lock()
		s.value = call.Value
		s.mu.Unlock()
	}
	for _, watch := range s.snapshot() {
		watch.Advance(chordctx.Background(), call)
	}
}

func (s *jsonFake) change(n float64, ops ...delta.Op) {
	s.advance(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallValue, Value: nObject(n), Ops: ops})
}

func (s *jsonFake) end() {
	for _, watch := range s.snapshot() {
		watch.EndWith(chord.WatchEnd{Kind: chord.WatchEnded})
	}
}

// nativeFake is Pi's `writable`: a native watchable the test sets and ends by hand.
type nativeFake struct {
	mu      sync.Mutex
	value   any
	sched   *chord.DeliveryScheduler
	watches []*chord.NativeWatch[any]
}

func newNativeFake(initial any) *nativeFake { return &nativeFake{value: initial} }

// use gives the source the delivery scheduler of self's host.
func (s *nativeFake) use(self Self) {
	s.mu.Lock()
	s.sched = self.(*attempt).object.host.scheduler
	s.mu.Unlock()
}

func (s *nativeFake) Value() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

func (s *nativeFake) Watch(ctx context.Context) (chord.NativeWatchHandle[any], error) {
	s.mu.Lock()
	var watch *chord.NativeWatch[any]
	watch = chord.NewNativeWatch[any](s.value, func() { s.drop(watch) }, func(error) {}, nil)
	if s.sched != nil {
		watch.On(s.sched)
	}
	s.watches = append(s.watches, watch)
	s.mu.Unlock()
	if ctx.Done() != nil {
		watch.ObserveAbort(ctx)
	}
	return watch, nil
}

func (s *nativeFake) drop(watch *chord.NativeWatch[any]) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at := slices.Index(s.watches, watch); at >= 0 {
		s.watches = slices.Delete(s.watches, at, at+1)
	}
}

func (s *nativeFake) snapshot() []*chord.NativeWatch[any] {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.watches)
}

func (s *nativeFake) set(next any) {
	s.mu.Lock()
	s.value = next
	s.mu.Unlock()
	for _, watch := range s.snapshot() {
		watch.Advance(chordctx.Background(), next)
	}
}

func (s *nativeFake) end() {
	for _, watch := range s.snapshot() {
		watch.EndWith(chord.WatchEnd{Kind: chord.WatchEnded})
	}
}

func (s *nativeFake) count() int { return len(s.snapshot()) }

// --- on -------------------------------------------------------------------------------------------------------------

func TestOnCallsFirstWithTheCurrentValueAsRThenOncePerChangeWithItsOps(t *testing.T) {
	e := newRoutinesEnv(t)
	var log routineLog
	e.store(nil, func(doc *singletonDoc, self Self) { self.On(doc.s, e.handler(self, &log, nil)) })
	e.quiet()
	e.bump()
	e.quiet()
	wantCalls(t, log.all(), wantCall(nObject(0), replaceOps(nObject(0)), -1), wantCall(nObject(1), []delta.Op{setN(1)}, -1))
	e.close()
}

func TestOnNeverOverlapsCallsChangesDuringACallFoldIntoTheNextWhichIsNotAbortedByThem(t *testing.T) {
	e := newRoutinesEnv(t)
	gate := e.newGate()
	var log routineLog
	var running atomic.Int32
	var overlapped, aborted atomic.Bool
	e.store(nil, func(doc *singletonDoc, self Self) {
		self.On(doc.s, e.handler(self, &log, func(n int, _ chord.WatchCall[delta.JsonValue], ctx Ctx) *Handled {
			if running.Load() > 0 {
				overlapped.Store(true)
			}
			running.Add(1)
			finish := func() {
				if ctx.Err() != nil {
					aborted.Store(true)
				}
				running.Add(-1)
			}
			if n == 2 {
				return e.awaiting(self, func() error {
					gate.wait(t)
					finish()
					return nil
				})
			}
			finish()
			return nil
		}))
	})
	e.quiet()
	e.bump()
	e.quiet()
	e.bump()
	e.bump()
	gate.fire()
	e.settled()
	wantCalls(t, log.all(),
		wantCall(nObject(0), replaceOps(nObject(0)), -1), wantCall(nObject(1), []delta.Op{setN(1)}, -1),
		wantCall(nObject(3), []delta.Op{setN(3)}, -1))
	if overlapped.Load() || aborted.Load() {
		t.Fatalf("overlapped %v, aborted %v, want neither", overlapped.Load(), aborted.Load())
	}
	e.close()
}

func TestOnAfterAThrowReportsKeepsTheWatchAndCallsAgainWithTheWholeValueAfter1sDoublingSuccessResets(t *testing.T) {
	e := newRoutinesEnv(t)
	var log routineLog
	var failing atomic.Int32
	failing.Store(3)
	e.store(nil, func(doc *singletonDoc, self Self) {
		self.On(doc.s, e.handler(self, &log, func(n int, _ chord.WatchCall[delta.JsonValue], _ Ctx) *Handled {
			if failing.Add(-1) >= 0 {
				return Threw(fmt.Errorf("fail %d", n))
			}
			return nil
		}))
	})
	e.advance(0)
	e.advance(1_000)
	e.advance(2_000)
	e.advance(4_000)
	calls := log.all()
	var at []int64
	for _, call := range calls {
		at = append(at, call.at)
		if call.ops[0][0] != "r" {
			t.Fatalf("a call without ops [r ...]: %+v", call)
		}
	}
	if !reflect.DeepEqual(at, []int64{0, 1_000, 3_000, 7_000}) {
		t.Fatalf("calls at %v, want [0 1000 3000 7000]", at)
	}
	wantStrings(t, e.reports(), "on #1: fail 1", "on #1: fail 2", "on #1: fail 3")
	// Succeeded: the next change gets its ops, and a later throw backs off from 1 s again.
	e.bump()
	e.advance(0)
	last := log.all()[log.len()-1]
	wantCalls(t, []routineCall{last}, wantCall(nObject(1), []delta.Op{setN(1)}, -1))
	failing.Store(1)
	e.bump()
	e.advance(0)
	e.advance(1_000)
	all := log.all()
	if got := []int64{all[len(all)-2].at, all[len(all)-1].at}; !reflect.DeepEqual(got, []int64{7_000, 8_000}) {
		t.Fatalf("last two calls at %v, want [7000 8000]", got)
	}
	if got := e.timers(); got != 0 {
		t.Fatalf("timers = %d, want 0", got)
	}
	e.close()
}

func TestOnAfterAThrowRetriesAChangeFoldedBehindTheThrowingCallAfterTheBackoffOnceNoThirdCallNoTimer(t *testing.T) {
	e := newRoutinesEnv(t)
	gate := e.newGate()
	var log routineLog
	e.store(nil, func(doc *singletonDoc, self Self) {
		self.On(doc.s, e.handler(self, &log, func(n int, _ chord.WatchCall[delta.JsonValue], _ Ctx) *Handled {
			if n != 1 {
				return nil
			}
			return e.awaiting(self, func() error {
				gate.wait(t)
				return errors.New("first")
			})
		}))
	})
	e.advance(0)
	e.bump()
	gate.fire()
	e.advance(0)
	e.advance(5_000)
	wantCalls(t, log.all(), wantCall(nObject(0), replaceOps(nObject(0)), 0), wantCall(nObject(1), replaceOps(nObject(1)), 1_000))
	if got := e.timers(); got != 0 {
		t.Fatalf("timers = %d, want 0", got)
	}
	e.close()
}

func TestOnAfterAThrowMakesOneRetryPerThrowAChangeDuringAFailingRetryWaitsForItsBackoffNoTimerLeft(t *testing.T) {
	e := newRoutinesEnv(t)
	var log routineLog
	e.item(nil, func(doc *singletonDoc, self Self) {
		self.On(doc.s, e.handler(self, &log, func(n int, _ chord.WatchCall[delta.JsonValue], _ Ctx) *Handled {
			// The retry at 1 s changes the source, then throws too.
			return e.awaiting(self, func() error {
				if n == 2 {
					if err := bumpDoc(doc, background); err != nil {
						return err
					}
				}
				if n <= 2 {
					return fmt.Errorf("fail %d", n)
				}
				return nil
			})
		}))
	})
	e.advance(0)
	e.advance(1_000)
	e.advance(10_000)
	wantCalls(t, log.all(), wantCall(nObject(0), nil, 0), wantCall(nObject(0), nil, 1_000), wantCall(nObject(1), nil, 3_000))
	wantStrings(t, e.reports(), "on #1: fail 1", "on #1: fail 2")
	if got := e.timers(); got != 0 {
		t.Fatalf("timers = %d, want 0", got)
	}
	e.ref.Release()
	e.advance(0)
	if got := e.timers(); got != 0 {
		t.Fatalf("timers after release = %d, want 0", got)
	}
	e.close()
}

func TestOnAfterAThrowBacksOffEvenWhenTheFailedCallChangedItsOwnSourceNoBurst(t *testing.T) {
	e := newRoutinesEnv(t)
	var log routineLog
	e.store(nil, func(doc *singletonDoc, self Self) {
		self.On(doc.s, e.handler(self, &log, func(n int, _ chord.WatchCall[delta.JsonValue], ctx Ctx) *Handled {
			if n > 100 {
				return nil
			}
			// A reconciler that records its attempt in its own source, then fails.
			return e.awaiting(self, func() error {
				if err := bumpDoc(doc, ctx); err != nil {
					return err
				}
				return errors.New("downstream unavailable")
			})
		}))
	})
	e.advance(0)
	if got := len(log.all()); got != 1 || log.all()[0].at != 0 {
		t.Fatalf("calls = %+v, want one at 0", log.all())
	}
	e.advance(1_000)
	e.advance(2_000)
	var at []int64
	for _, call := range log.all() {
		at = append(at, call.at)
	}
	if !reflect.DeepEqual(at, []int64{0, 1_000, 3_000}) || len(e.reports()) != 3 {
		t.Fatalf("calls at %v, reports %v, want [0 1000 3000] and 3 reports", at, e.reports())
	}
	e.close()
}

func TestOnAfterAThrowRetriesWithTheValueThatArrivedDuringTheFailedCallAsAWholeValue(t *testing.T) {
	e := newRoutinesEnv(t)
	var log routineLog
	e.store(nil, func(doc *singletonDoc, self Self) {
		self.On(doc.s, e.handler(self, &log, func(n int, _ chord.WatchCall[delta.JsonValue], ctx Ctx) *Handled {
			if n > 1 {
				return nil
			}
			return e.awaiting(self, func() error {
				if err := bumpDoc(doc, ctx); err != nil {
					return err
				}
				return errors.New("once")
			})
		}))
	})
	e.advance(0)
	e.advance(1_000)
	wantCalls(t, log.all(), wantCall(nObject(0), replaceOps(nObject(0)), 0), wantCall(nObject(1), replaceOps(nObject(1)), 1_000))
	e.close()
}

func TestOnAfterAThrowFoldsChangesMadeDuringASlowRetryIntoOneCallWithTheNewestValue(t *testing.T) {
	e := newRoutinesEnv(t)
	gate := e.newGate()
	var log routineLog
	e.store(nil, func(doc *singletonDoc, self Self) {
		self.On(doc.s, e.handler(self, &log, func(n int, _ chord.WatchCall[delta.JsonValue], _ Ctx) *Handled {
			switch n {
			case 1:
				return Threw(errors.New("first"))
			case 2:
				return e.awaiting(self, func() error {
					gate.wait(t)
					return nil
				})
			}
			return nil
		}))
	})
	e.advance(0)
	e.advance(1_000)
	for range 3 {
		e.bump()
	}
	gate.fire()
	e.advance(0)
	wantCalls(t, log.all(),
		wantCall(nObject(0), replaceOps(nObject(0)), -1), wantCall(nObject(0), replaceOps(nObject(0)), -1),
		wantCall(nObject(3), []delta.Op{setN(3)}, -1))
	e.close()
}

func TestOnAfterAThrowWaitsForTheNextGoodValueWhenItsSourceFailsDuringTheBackoffTheRetryIsDropped(t *testing.T) {
	e := newRoutinesEnv(t)
	var log routineLog
	var set func(n float64) error
	e.store(nil, func(doc *singletonDoc, self Self) {
		failing := self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
			n := values[0].(*delta.JsonObject).Value("n").(float64)
			if n < 0 {
				return nil, errors.New("compute")
			}
			return n, nil
		})
		set = func(n float64) error { return setDoc(doc, n, background) }
		self.On(failing.(JSONSource), e.handler(self, &log, func(_ int, call chord.WatchCall[delta.JsonValue], _ Ctx) *Handled {
			if call.Value == 1.0 {
				return Threw(errors.New("handler threw"))
			}
			return nil
		}))
	})
	e.advance(0)
	must(t, set(1))
	e.advance(0)
	must(t, set(-1))
	e.advance(2_000)
	if got := log.values(); !reflect.DeepEqual(got, []any{0.0, 1.0}) {
		t.Fatalf("calls = %v, want [0 1]", got)
	}
	if got := e.timers(); got != 0 {
		t.Fatalf("timers = %d, want 0", got)
	}
	must(t, set(3))
	e.advance(0)
	if got := log.values(); !reflect.DeepEqual(got, []any{0.0, 1.0, 3.0}) {
		t.Fatalf("calls = %v, want [0 1 3]", got)
	}
	wantStrings(t, e.reports(), "on #1: handler threw")
	e.close()
}

func TestOnAfterAThrowClearsItsRetryTimerWhenItsObjectEndsDuringTheBackoff(t *testing.T) {
	e := newRoutinesEnv(t)
	e.item(nil, func(doc *singletonDoc, self Self) {
		self.On(doc.s, func(chord.WatchCall[delta.JsonValue], Ctx) *Handled { return Threw(errors.New("always")) })
	})
	e.advance(0)
	if got := e.timers(); got != 1 {
		t.Fatalf("timers = %d, want 1", got)
	}
	e.ref.Release()
	e.advance(0)
	if got := e.timers(); got != 0 {
		t.Fatalf("timers after release = %d, want 0", got)
	}
	e.close()
}

func TestOnAfterAThrowReportsARetirementAgainOnTheRetryAfterTheCallThatGotItThrew(t *testing.T) {
	e := newRoutinesEnv(t)
	source := newJSONFake(nObject(0))
	var log routineLog
	e.store(nil, func(_ *singletonDoc, self Self) {
		source.use(self)
		self.On(source, e.handler(self, &log, func(n int, _ chord.WatchCall[delta.JsonValue], _ Ctx) *Handled {
			if n == 2 {
				return Threw(errors.New("retirement not handled"))
			}
			return nil
		}))
	})
	e.advance(0)
	source.advance(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallValue, Value: nObject(0), Ops: []delta.Op{}, Retired: true})
	e.advance(0)
	e.advance(1_000)
	e.advance(10_000)
	calls := log.all()[1:]
	wantCalls(t, calls,
		routineCall{value: nObject(0), ops: []delta.Op{}, retired: true, at: -1},
		routineCall{value: nObject(0), ops: replaceOps(nObject(0)), retired: true, at: -1})
	e.close()
}

func TestOnAfterAThrowCallsAtOnceWithTheNewestWholeValueWhenTheSourceChangesDuringTheBackoff(t *testing.T) {
	e := newRoutinesEnv(t)
	var log routineLog
	e.store(nil, func(doc *singletonDoc, self Self) {
		self.On(doc.s, e.handler(self, &log, func(n int, _ chord.WatchCall[delta.JsonValue], _ Ctx) *Handled {
			if n == 1 {
				return Threw(errors.New("first"))
			}
			return nil
		}))
	})
	e.advance(0)
	e.advance(500)
	e.bump()
	e.advance(0)
	e.bump()
	e.advance(0)
	wantCalls(t, log.all(),
		wantCall(nObject(0), replaceOps(nObject(0)), 0), wantCall(nObject(1), replaceOps(nObject(1)), 500),
		wantCall(nObject(2), []delta.Op{setN(2)}, 500))
	// The backoff timer is gone: nothing calls again.
	e.advance(10_000)
	if got := log.len(); got != 3 {
		t.Fatalf("calls = %d, want 3", got)
	}
	e.close()
}

func TestOnFoldsEveryChangeMadeDuringTheFirstCallIntoOneNextCall03Not013(t *testing.T) {
	e := newRoutinesEnv(t)
	gate := e.newGate()
	var log routineLog
	e.store(nil, func(doc *singletonDoc, self Self) {
		self.On(doc.s, e.handler(self, &log, func(n int, _ chord.WatchCall[delta.JsonValue], _ Ctx) *Handled {
			if n != 1 {
				return nil
			}
			return e.awaiting(self, func() error {
				gate.wait(t)
				return nil
			})
		}))
	})
	e.quiet()
	for range 3 {
		e.bump()
	}
	e.quiet()
	gate.fire()
	e.settled()
	wantCalls(t, log.all(), wantCall(nObject(0), replaceOps(nObject(0)), -1), wantCall(nObject(3), []delta.Op{setN(3)}, -1))
	e.close()
}

func TestOnFollowsAnAppsJSONWatchableWhoseWatchIsChordsSharedWatchWithOpsAsItsTypeSays(t *testing.T) {
	e := newRoutinesEnv(t)
	source := newJSONFake(nObject(0))
	var log routineLog
	e.store(nil, func(_ *singletonDoc, self Self) {
		source.use(self)
		self.On(source, e.handler(self, &log, nil))
	})
	e.quiet()
	source.change(1, setN(1))
	e.quiet()
	wantCalls(t, log.all(), wantCall(nObject(0), replaceOps(nObject(0)), -1), wantCall(nObject(1), []delta.Op{setN(1)}, -1))
	e.close()
}

func TestOnReportsARetirementAsRetiredFoldedWithTheChangesAroundItIntoOneCallAndGoesOn(t *testing.T) {
	e := newRoutinesEnv(t)
	gate := e.newGate()
	source := newJSONFake(nObject(0))
	var log routineLog
	e.store(nil, func(_ *singletonDoc, self Self) {
		source.use(self)
		self.On(source, e.handler(self, &log, func(n int, _ chord.WatchCall[delta.JsonValue], _ Ctx) *Handled {
			if n != 1 {
				return nil
			}
			return e.awaiting(self, func() error {
				gate.wait(t)
				return nil
			})
		}))
	})
	e.quiet()
	// A Durable document retired (the last value, no ops) and created again, while the first call runs.
	source.advance(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallValue, Value: nObject(0), Ops: []delta.Op{}, Retired: true})
	source.advance(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallValue, Value: nObject(5), Ops: replaceOps(nObject(5))})
	gate.fire()
	e.settled()
	source.change(6, setN(6))
	e.quiet()
	wantCalls(t, log.all(),
		wantCall(nObject(0), replaceOps(nObject(0)), -1),
		routineCall{value: nObject(5), ops: replaceOps(nObject(5)), retired: true, at: -1},
		wantCall(nObject(6), []delta.Op{setN(6)}, -1))
	e.close()
}

func TestOnFoldsANativeSourceToItsNewestValueAfterABusyCall(t *testing.T) {
	e := newRoutinesEnv(t)
	gate := e.newGate()
	source := newNativeFake("a")
	var calls atomic.Pointer[[]any]
	record := func(value any) int {
		for {
			old := calls.Load()
			var next []any
			if old != nil {
				next = slices.Clone(*old)
			}
			next = append(next, value)
			if calls.CompareAndSwap(old, &next) {
				return len(next)
			}
		}
	}
	e.store(nil, func(_ *singletonDoc, self Self) {
		source.use(self)
		self.OnNative(source, func(call chord.NativeWatchCall[any], _ Ctx) *Handled {
			if record(call.Value) != 1 {
				return nil
			}
			return e.awaiting(self, func() error {
				gate.wait(t)
				return nil
			})
		})
	})
	e.quiet()
	source.set("b")
	source.set("c")
	e.quiet()
	gate.fire()
	e.settled()
	if got := *calls.Load(); !reflect.DeepEqual(got, []any{"a", "c"}) {
		t.Fatalf("calls = %v, want [a c]", got)
	}
	e.close()
}

// "takes every value a busy call left buffered before calling again: [0, 20], also when it set them itself".
func TestOnTakesEveryValueABusyCallLeftBufferedBeforeCallingAgain0And20AlsoWhenItSetThemItself(t *testing.T) {
	e := newRoutinesEnv(t)
	source := newNativeFake(0.0)
	var mu sync.Mutex
	var calls []any
	e.store(nil, func(_ *singletonDoc, self Self) {
		source.use(self)
		self.OnNative(source, func(call chord.NativeWatchCall[any], _ Ctx) *Handled {
			mu.Lock()
			calls = append(calls, call.Value)
			mu.Unlock()
			if call.Value == 0.0 {
				for n := 1; n <= 20; n++ {
					source.set(float64(n))
				}
			}
			return nil
		})
	})
	e.settled()
	mu.Lock()
	got := slices.Clone(calls)
	mu.Unlock()
	if !reflect.DeepEqual(got, []any{0.0, 20.0}) {
		t.Fatalf("calls = %v, want [0 20]", got)
	}
	e.close()

	second := newRoutinesEnv(t)
	gate := second.newGate()
	later := newNativeFake(0.0)
	var seenMu sync.Mutex
	var seen []any

	second.store(nil, func(_ *singletonDoc, self Self) {
		later.use(self)
		self.OnNative(later, func(call chord.NativeWatchCall[any], _ Ctx) *Handled {
			seenMu.Lock()
			seen = append(seen, call.Value)
			seenMu.Unlock()
			if call.Value != 0.0 {
				return nil
			}
			return second.awaiting(self, func() error {
				gate.wait(t)
				return nil
			})
		})
	})
	second.quiet()
	for n := 1; n <= 20; n++ {
		later.set(float64(n))
	}
	// No flush in between: the watch still holds the values when the call ends.
	gate.fire()
	second.settled()
	seenMu.Lock()
	defer seenMu.Unlock()
	if !reflect.DeepEqual(seen, []any{0.0, 20.0}) {
		t.Fatalf("seen = %v, want [0 20]", seen)
	}
	second.close()
}

type brokenWatchable struct{}

func (brokenWatchable) Watch(context.Context) (chord.NativeWatchHandle[any], error) {
	return nil, errors.New("no watch")
}

type endedWatchable struct{}

func (endedWatchable) Watch(context.Context) (chord.NativeWatchHandle[any], error) {
	watch := chord.NewNativeWatch[any](0.0, func() {}, func(error) {}, nil)
	watch.EndWith(chord.WatchEnd{Kind: chord.WatchEnded})
	return watch, nil
}

func TestOnReportsASourceWhoseWatchCannotBeTakenASourceThatEndedMeanwhileEndsItQuietly(t *testing.T) {
	e := newRoutinesEnv(t)
	var calls atomic.Int32
	e.store(nil, func(_ *singletonDoc, self Self) {
		self.OnNative(brokenWatchable{}, func(chord.NativeWatchCall[any], Ctx) *Handled {
			calls.Add(1)
			return nil
		})
		self.OnNative(endedWatchable{}, func(chord.NativeWatchCall[any], Ctx) *Handled {
			calls.Add(1)
			return nil
		})
	})
	e.quiet()
	if got := calls.Load(); got != 0 {
		t.Fatalf("calls = %d, want 0", got)
	}
	wantStrings(t, e.reports(), "on #1: no watch")
	e.close()
}

func TestOnEndsWithoutAReportWhenItsSourceEnds(t *testing.T) {
	e := newRoutinesEnv(t)
	other := mustContract(contracts.Local("app.other", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	var otherDoc *singletonDoc
	var log routineLog
	var errs *hostErrors
	e.host, errs = loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(other, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			otherDoc = stateOf(self, routinesDoc)
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"n": self.Derive([]any{otherDoc.s}, func(values []any) (delta.JsonValue, error) { return values[0].(*delta.JsonObject).Value("n"), nil }),
			}}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(routinesItem, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, routinesDoc)
			// Another object's document: it ends with that object.
			self.On(otherDoc.s, e.handler(self, &log, nil))
			return e.implementation(self, doc), nil
		}, ServeOptions{})
	}, loadedOptions{Loop: e.loop})
	e.errs = errs
	first := mustGet(t, e.host, other, jsonObject("id", 1.0))
	mustGet(t, e.host, routinesItem, jsonObject("id", 1.0))
	e.quiet()
	first.Release()
	e.quiet()
	wantCalls(t, log.all(), wantCall(nObject(0), replaceOps(nObject(0)), -1))
	if got := errs.Len(); got != 0 {
		t.Fatalf("errors = %v, want none", errs.All())
	}
	e.close()
}

func TestOnIsNotCalledForItsSourcesFailuresItWaitsForTheNextValueADeriveHealing(t *testing.T) {
	e := newRoutinesEnv(t)
	var log routineLog
	e.store(nil, func(doc *singletonDoc, self Self) {
		failing := self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
			n := values[0].(*delta.JsonObject).Value("n").(float64)
			if n > 0 && n < 3 {
				return nil, errors.New("compute")
			}
			return n, nil
		})
		self.On(failing.(JSONSource), e.handler(self, &log, nil))
	})
	e.quiet()
	e.bump()
	e.bump()
	e.quiet()
	wantCalls(t, log.all(), wantCall(0.0, replaceOps(0.0), -1))
	e.bump()
	e.quiet()
	wantCalls(t, log.all(), wantCall(0.0, replaceOps(0.0), -1), wantCall(3.0, replaceOps(3.0), -1))
	// The derive's owner reports the failure (once for the episode); the routine reports nothing.
	all := e.errs.All()
	if len(all) != 1 || all[0].Kind != ErrDeriveFailed || all[0].Error == nil || all[0].Error.Error() != "compute" {
		t.Fatalf("errors = %+v, want one derive_failed compute", all)
	}
	e.close()
}

func TestOnDropsAValueQueuedBehindABusyCallWhenAFailureFollowsItTheNextCallIsTheHealedValue(t *testing.T) {
	e := newRoutinesEnv(t)
	gate, entered := e.newGate(), e.newGate()
	var log routineLog
	var set func(n float64) error
	e.store(nil, func(doc *singletonDoc, self Self) {
		failing := self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
			n := values[0].(*delta.JsonObject).Value("n").(float64)
			if n < 0 {
				return nil, errors.New("compute")
			}
			return n, nil
		})
		set = func(n float64) error { return setDoc(doc, n, background) }
		self.On(failing.(JSONSource), e.handler(self, &log, func(_ int, call chord.WatchCall[delta.JsonValue], _ Ctx) *Handled {
			if call.Value != 1.0 {
				return nil
			}
			entered.fire()
			return e.awaiting(self, func() error {
				gate.wait(t)
				return nil
			})
		}))
	})
	e.quiet()
	must(t, set(1))
	entered.waitMain(t)
	// Queued behind the busy call, then replaced by the failure.
	must(t, set(2))
	must(t, set(-1))
	gate.fire()
	e.settled()
	if got := log.values(); !reflect.DeepEqual(got, []any{0.0, 1.0}) {
		t.Fatalf("calls = %v, want [0 1]", got)
	}
	must(t, set(3))
	e.settled()
	if got := log.values(); !reflect.DeepEqual(got, []any{0.0, 1.0, 3.0}) {
		t.Fatalf("calls = %v, want [0 1 3]", got)
	}
	wantStrings(t, e.reports())
	e.close()
}

var (
	routinesFailing = mustContract(contracts.Local("app.failing", contracts.LocalDeclaration{
		Version: 1,
		Members: []contracts.LocalEntry{contracts.Mem("t", contracts.Int()), contracts.Mem("set", contracts.NewMethod(contracts.Int()))},
	}))
	routinesViewer = mustContract(contracts.Local("app.on-viewer", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("m", contracts.NewMethod())},
	}))
)

func TestOnStartsOnASourceFailedNowNoCallAndNoReportUntilItsFirstGoodValueAReferenceMember(t *testing.T) {
	e := newRoutinesEnv(t)
	var log routineLog
	var errs *hostErrors
	e.host, errs = loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(routinesFailing, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, routinesDoc)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"t": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					n := values[0].(*delta.JsonObject).Value("n").(float64)
					if n == 1 {
						return nil, errors.New("t threw")
					}
					return n, nil
				})},
				Methods: map[string]contracts.MethodFunction{
					"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return nil, setDoc(doc, args.(float64), ctx)
					},
				},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(routinesViewer, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.On(self.Get(routinesFailing, jsonObject()).Member("t"), e.handler(self, &log, nil))
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"m": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{Loop: e.loop})
	e.errs = errs
	failing := mustGet(t, e.host, routinesFailing, jsonObject())
	_, err := refCall(failing, "set", 1.0)
	must(t, err)
	viewer := mustGet(t, e.host, routinesViewer, jsonObject("id", 1.0))
	e.settled()
	if got := log.len(); got != 0 {
		t.Fatalf("calls = %v, want none", log.all())
	}
	_, err = refCall(failing, "set", 2.0)
	must(t, err)
	e.settled()
	wantCalls(t, log.all(), wantCall(2.0, replaceOps(2.0), -1))
	var kinds []HostErrorKind
	for _, each := range errs.All() {
		kinds = append(kinds, each.Kind)
	}
	if !reflect.DeepEqual(kinds, []HostErrorKind{ErrDeriveFailed}) {
		t.Fatalf("error kinds = %v, want [derive_failed]", kinds)
	}
	viewer.Release()
	e.close()
}

func TestOnFollowsAReferenceMemberTheProvidersValueFirstThenItsChanges(t *testing.T) {
	e := newRoutinesEnv(t)
	var log routineLog
	var bumper func() error
	e.host, e.errs = loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(routinesStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, routinesDoc)
			bumper = func() error { return bumpDoc(doc, background) }
			return e.implementation(self, doc), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(routinesItem, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, routinesDoc)
			provider := self.Get(routinesStore, jsonObject())
			self.On(provider.Member("n"), e.handler(self, &log, nil))
			return e.implementation(self, doc), nil
		}, ServeOptions{})
	}, loadedOptions{Loop: e.loop})
	mustGet(t, e.host, routinesItem, jsonObject("id", 1.0))
	e.quiet()
	must(t, bumper())
	e.quiet()
	wantCalls(t, log.all(), wantCall(0.0, replaceOps(0.0), -1), wantCall(1.0, replaceOps(1.0), -1))
	e.close()
}

func TestOnConvergesWhenItWritesItsOwnSourceAnUnchangedDraftCommitsNothing(t *testing.T) {
	e := newRoutinesEnv(t)
	var calls atomic.Int32
	e.store(nil, func(doc *singletonDoc, self Self) {
		self.On(doc.s, func(call chord.WatchCall[delta.JsonValue], ctx Ctx) *Handled {
			calls.Add(1)
			value := call.Value.(*delta.JsonObject).Value("n").(float64)
			return e.awaiting(self, func() error { return setDoc(doc, min(value+1, 3), ctx) })
		})
	})
	// 0 → 1 → 2 → 3, then the write of 3 changes nothing and nothing calls again (the handlers' tasks and the deliveries
	// they cause are waited for, not timed).
	e.quiet()
	if got := calls.Load(); got != 4 {
		t.Fatalf("calls = %d, want 4", got)
	}
	e.close()
}

func TestOnFollowsANativeSourceOneCallPerNewValueWithoutOpsEndsWithIt(t *testing.T) {
	e := newRoutinesEnv(t)
	source := newNativeFake("a")
	var mu sync.Mutex
	var calls []chord.NativeWatchCall[any]
	e.store(nil, func(_ *singletonDoc, self Self) {
		source.use(self)
		self.OnNative(source, func(call chord.NativeWatchCall[any], _ Ctx) *Handled {
			mu.Lock()
			calls = append(calls, call)
			mu.Unlock()
			return nil
		})
	})
	e.quiet()
	source.set("b")
	source.set("c")
	e.settled()
	mu.Lock()
	var values []any
	for _, call := range calls {
		if call.Kind != chord.WatchCallValue {
			t.Fatalf("call %+v is not a value call", call)
		}
		values = append(values, call.Value)
	}
	mu.Unlock()
	if !reflect.DeepEqual(values, []any{"a", "b", "c"}) {
		t.Fatalf("calls = %v, want [a b c]", values)
	}
	source.end()
	e.quiet()
	if got := source.count(); got != 0 || e.errs.Len() != 0 {
		t.Fatalf("watches = %d, errors = %v, want none", got, e.errs.All())
	}
	e.close()
}

func TestOnReleasesItsWatchWhenItsObjectEndsACallThatThrowsAsItEndsIsNotReported(t *testing.T) {
	e := newRoutinesEnv(t)
	source := newNativeFake(0.0)
	e.item(nil, func(_ *singletonDoc, self Self) {
		source.use(self)
		self.OnNative(source, func(_ chord.NativeWatchCall[any], ctx Ctx) *Handled {
			return e.parked(self, func() error {
				<-ctx.Done()
				return errors.New("stopping")
			})
		})
	})
	e.quiet()
	if got := source.count(); got != 1 {
		t.Fatalf("watches = %d, want 1", got)
	}
	e.ref.Release()
	e.quiet()
	if got := source.count(); got != 0 || e.errs.Len() != 0 {
		t.Fatalf("watches = %d, errors = %v, want none", got, e.errs.All())
	}
	e.close()
}

// --- onLive ---------------------------------------------------------------------------------------------------------

func TestOnLiveIsCalledOnceWhenItsObjectIsLiveReturningEndsIt(t *testing.T) {
	e := newRoutinesEnv(t)
	var calls atomic.Int32
	e.store(nil, func(_ *singletonDoc, self Self) {
		self.OnLive(func(Ctx) *Handled {
			calls.Add(1)
			return nil
		})
	})
	e.advance(10_000)
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
	e.close()
}

func TestOnLiveRestartsAfterAThrowWithTheBackoffResetAfterARunOfAtLeastAMinute(t *testing.T) {
	e := newRoutinesEnv(t)
	var mu sync.Mutex
	var starts []int64
	e.store(nil, func(_ *singletonDoc, self Self) {
		loop := self.(*attempt).loop()
		self.OnLive(func(ctx Ctx) *Handled {
			mu.Lock()
			starts = append(starts, loop.Now())
			n := len(starts)
			mu.Unlock()
			if n == 3 {
				return e.parked(self, func() error {
					if err := sleepOn(self.(*attempt).loop(), 60*time.Second, ctx); err != nil {
						return err
					}
					return fmt.Errorf("run %d", n)
				})
			}
			return Threw(fmt.Errorf("run %d", n))
		})
	})
	e.advance(0)
	e.advance(1_000)
	e.advance(2_000 + 60_000)
	e.advance(1_000)
	mu.Lock()
	got := slices.Clone(starts)
	mu.Unlock()
	if !reflect.DeepEqual(got, []int64{0, 1_000, 3_000, 64_000}) {
		t.Fatalf("starts = %v, want [0 1000 3000 64000]", got)
	}
	wantStrings(t, e.reports(), "onLive #1: run 1", "onLive #1: run 2", "onLive #1: run 3", "onLive #1: run 4")
	e.close()
}

func TestOnLiveIsAbortedByItsOwnersEndNoReportNoRestart(t *testing.T) {
	e := newRoutinesEnv(t)
	var calls atomic.Int32
	e.item(nil, func(_ *singletonDoc, self Self) {
		self.OnLive(func(ctx Ctx) *Handled {
			calls.Add(1)
			return e.parked(self, func() error { return sleepOn(self.(*attempt).loop(), 1_000_000*time.Millisecond, ctx) })
		})
	})
	e.advance(0)
	e.ref.Release()
	e.advance(10_000)
	if got, errs, timers := calls.Load(), e.errs.Len(), e.timers(); got != 1 || errs != 0 || timers != 0 {
		t.Fatalf("calls %d, errors %d, timers %d, want 1, 0, 0", got, errs, timers)
	}
	e.close()
}

// --- routines and the end ---------------------------------------------------------------------------------------------

func TestRoutinesNeverStartARoutineCallOnceTheObjectEndedInItsFirstTurnNoCallNoReportNoRejection(t *testing.T) {
	e := newRoutinesEnv(t)
	var mu sync.Mutex
	var calls []string
	var held *Reference
	record := func(text string) {
		mu.Lock()
		calls = append(calls, text)
		mu.Unlock()
	}
	e.host, e.errs = loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(routinesItem, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, routinesDoc)
			self.On(doc.s, func(chord.WatchCall[delta.JsonValue], Ctx) *Handled {
				record("on")
				return nil
			})
			self.OnLive(func(Ctx) *Handled {
				record("onLive #1")
				held.Release()
				return nil
			})
			self.OnLive(func(Ctx) *Handled {
				record("onLive #2")
				return nil
			})
			return e.implementation(self, doc), nil
		}, ServeOptions{})
	}, loadedOptions{Loop: e.loop})
	held = mustHold(t, e.host, routinesItem, jsonObject("id", 1.0))
	e.quiet()
	e.advance(10)
	mu.Lock()
	got := slices.Clone(calls)
	mu.Unlock()
	if !reflect.DeepEqual(got, []string{"onLive #1"}) || e.errs.Len() != 0 {
		t.Fatalf("calls %v, errors %v, want [onLive #1] and none", got, e.errs.All())
	}
	e.close()
}

func TestRoutinesAreInTheDrainBeforeTheirHandlerRunsAHandlerReleasingTheLastReferenceKeepsNativesOpen(t *testing.T) {
	e := newRoutinesEnv(t)
	gate := e.newGate()
	var mu sync.Mutex
	var events []string
	record := func(text string) {
		mu.Lock()
		events = append(events, text)
		mu.Unlock()
	}
	e.item(nil, func(doc *singletonDoc, self Self) {
		selfOpen(self, func(context.Context) error { return nil }, func(NativeCloseReason) { record("native closed") })
		self.On(doc.s, func(call chord.WatchCall[delta.JsonValue], _ Ctx) *Handled {
			if call.Value.(*delta.JsonObject).Value("n") == 0.0 {
				return nil
			}
			// Ends its own object before its first await.
			e.ref.Release()
			return e.awaiting(self, func() error {
				gate.wait(t)
				record("handler done")
				return nil
			})
		})
	})
	e.quiet()
	// The call may see its object end: its own routine ends it.
	_, _ = refCall(e.ref, "bump", nil)
	e.quiet()
	mu.Lock()
	early := slices.Clone(events)
	mu.Unlock()
	if len(early) != 0 {
		t.Fatalf("events = %v, want none yet", early)
	}
	gate.fire()
	e.quiet()
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(events, []string{"handler done", "native closed"}) {
		t.Fatalf("events = %v, want [handler done, native closed]", events)
	}
}

func TestRoutinesMakeNoCallQueuedBehindABusyOneOnceTheObjectEnded(t *testing.T) {
	e := newRoutinesEnv(t)
	gate := e.newGate()
	var log routineLog
	e.item(nil, func(doc *singletonDoc, self Self) {
		self.On(doc.s, e.handler(self, &log, func(int, chord.WatchCall[delta.JsonValue], Ctx) *Handled {
			return e.awaiting(self, func() error {
				gate.wait(t)
				return nil
			})
		}))
	})
	e.quiet()
	e.bump()
	e.quiet()
	e.ref.Release()
	gate.fire()
	e.quiet()
	wantCalls(t, log.all(), wantCall(nObject(0), replaceOps(nObject(0)), -1))
	e.close()
}

func TestRoutinesReleaseTheirCallsCtxGetReferencesWhenEachCallSettles(t *testing.T) {
	e := newRoutinesEnv(t)
	other := mustContract(contracts.Local("app.other", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	e.host, e.errs = loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(other, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, routinesDoc)
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"n": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) { return values[0].(*delta.JsonObject).Value("n"), nil }),
			}}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(routinesStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, routinesDoc)
			self.OnLive(func(ctx Ctx) *Handled {
				return e.awaiting(self, func() error {
					_, err := ctx.Get(other, jsonObject("id", 1.0), GetOptions{})
					return err
				})
			})
			return e.implementation(self, doc), nil
		}, ServeOptions{})
	}, loadedOptions{Loop: e.loop})
	e.quiet()
	var references int
	e.host.(*chordHost).enter(func() {
		value, err := e.host.References(other).Value()
		must(t, err)
		references = reflect.ValueOf(value).Len()
	})
	if references != 0 {
		t.Fatalf("references of Other = %d, want 0", references)
	}
	e.close()
}

func TestRoutinesActAsTheOwnersCallerPrincipal(t *testing.T) {
	seen := make(chan any, 1)
	watcher := mustContract(contracts.Remote("app.watcher", contracts.RemoteDeclaration{
		Version:     1,
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
		Variants:    []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{})},
	}))
	l := linked(t, func(h FacetHost) error {
		return h.Serve(watcher, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			self.OnLive(func(ctx Ctx) *Handled {
				seen <- ctx.Principal()
				return nil
			})
			return contracts.Implementation{Variant: "v"}, nil
		}, ServeOptions{})
	}, linkedOptions{Child: guestPrincipal("S1")})
	_, err := l.Client.Get(watcher, jsonObject(), background, GetOptions{})
	must(t, err)
	if got := receive(t, seen); !reflect.DeepEqual(got, any(guestPrincipal("S1"))) {
		t.Fatalf("principal = %v, want guest S1", got)
	}
}

// Carries "routines and the end › type their handler by the source's kind: ops and retired for a JSON watchable, the value
// alone for a native one" (a type-level check upstream, owner class a): the Go mechanism is the handler types.
func TestOnTypesTheirHandlerByTheSourcesKindOpsAndRetiredForAJSONWatchableTheValueAloneForANativeOne(t *testing.T) {
	json := reflect.TypeFor[OnHandler[delta.JsonValue]]()
	native := reflect.TypeFor[OnNativeHandler[any]]()
	for _, field := range []string{"Value", "Ops", "Retired"} {
		if _, ok := json.In(0).FieldByName(field); !ok {
			t.Errorf("a JSON handler's call has no %s", field)
		}
	}
	if _, ok := native.In(0).FieldByName("Value"); !ok {
		t.Error("a native handler's call has no Value")
	}
	for _, field := range []string{"Ops", "Retired"} {
		if _, ok := native.In(0).FieldByName(field); ok {
			t.Errorf("a native handler's call has %s", field)
		}
	}
	for _, handler := range []reflect.Type{json, native, reflect.TypeFor[OnLiveHandler]()} {
		if handler.In(handler.NumIn()-1) != reflect.TypeFor[Ctx]() {
			t.Errorf("%v does not take a Ctx last", handler)
		}
		if handler.Out(0) != reflect.TypeFor[*Handled]() {
			t.Errorf("%v does not return a *Handled", handler)
		}
	}
	var names []string
	for method := range reflect.TypeFor[RoutineDeclarations]().Methods() {
		names = append(names, method.Name)
	}
	if fmt.Sprint(names) != "[On OnLive OnNative]" {
		t.Fatalf("RoutineDeclarations = %v", names)
	}
}

func TestRoutinesAreAbortedByHostCloseWhichDrainsTheirRunningCallsBoundedByCloseTimeout(t *testing.T) {
	e := newRoutinesEnv(t)
	var mu sync.Mutex
	var ended []string
	closeTimeout := 50 * time.Millisecond
	e.store(&closeTimeout, func(_ *singletonDoc, self Self) {
		self.OnLive(func(ctx Ctx) *Handled {
			return e.parked(self, func() error {
				<-ctx.Done()
				mu.Lock()
				ended = append(ended, "object")
				mu.Unlock()
				return nil
			})
		})
		self.OnLive(func(Ctx) *Handled {
			// Never stops: the drain gives up at closeTimeout.
			return Pending(self.(*attempt).loop())
		})
	})
	done := make(chan error, 1)
	go func() { done <- e.host.Close(background) }()
	// The first routine ends once the close aborted it; the second never stops, so the drain waits for the host's timer,
	// which the fake clock fires once the first has ended.
	scopeEventually(t, "the first routine to end", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(ended) == 1
	})
	e.loop.AdvanceAsync(50)
	must(t, receive(t, done))
	mu.Lock()
	defer mu.Unlock()
	all := e.errs.All()
	if !reflect.DeepEqual(ended, []string{"object"}) || len(all) != 1 || all[0].Kind != ErrDrainTimeout || all[0].Object != "app.store" {
		t.Fatalf("ended %v, errors %+v, want [object] and one drain_timeout of app.store", ended, all)
	}
}

func TestRoutinesLeaveNothingOfAnEndedObjectItsWatchesAndContextsAreCollected(t *testing.T) {
	e := newRoutinesEnv(t)
	source := newNativeFake(0.0)
	var mu sync.Mutex
	var refs []weak.Pointer[callContext]
	e.item(nil, func(doc *singletonDoc, self Self) {
		source.use(self)
		keep := func(ctx Ctx) {
			mu.Lock()
			refs = append(refs, weak.Make(ctx.(*callContext)))
			mu.Unlock()
		}
		self.OnNative(source, func(_ chord.NativeWatchCall[any], ctx Ctx) *Handled {
			keep(ctx)
			return nil
		})
		self.On(doc.s, func(_ chord.WatchCall[delta.JsonValue], ctx Ctx) *Handled {
			keep(ctx)
			return nil
		})
	})
	e.quiet()
	e.ref.Release()
	e.quiet()
	mu.Lock()
	held := slices.Clone(refs)
	mu.Unlock()
	if got := source.count(); got != 0 || len(held) != 2 {
		t.Fatalf("watches %d, calls %d, want 0 and 2", got, len(held))
	}
	nexttest.ExpectCollected(t, held...)
}

// --- the routines cases that need raw peers or links ------------------------------------------------------------------------

var routinesViewerOnly = mustContract(contracts.Local("app.viewer", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
}))

// refStateSource is `ref.state` as an OnNative source: the watch of its *RefState values with the value type erased.
type refStateSource struct{ source chord.NativeWatchable[*RefState] }

type erasedStateWatch struct{ chord.NativeWatchHandle[*RefState] }

func (w erasedStateWatch) Value() any { return w.NativeWatchHandle.Value() }
func (w erasedStateWatch) Start(listener func(ctx context.Context, call chord.NativeWatchCall[any]) error) {
	w.NativeWatchHandle.Start(func(ctx context.Context, call chord.NativeWatchCall[*RefState]) error {
		return listener(ctx, chord.NativeWatchCall[any]{Kind: call.Kind, Value: call.Value, Error: call.Error})
	})
}

func (s refStateSource) Watch(ctx context.Context) (chord.NativeWatchHandle[any], error) {
	watch, err := s.source.Watch(ctx)
	if err != nil {
		return nil, err
	}
	return erasedStateWatch{watch}, nil
}

// "on a remote member with no value yet, a first value followed at once by a failure calls nothing".
func TestOnOnARemoteMemberWithNoValueYetAFirstValueFollowedAtOnceByAFailureCallsNothing(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		var mu sync.Mutex
		var calls []any
		client := newHost(t, HostOptions{OnError: func(HostError) {}})
		must(t, client.Load([]HostFacet{{Name: "@test/client", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(routinesViewerOnly, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				self.On(self.Get(Notes, jsonObject("session", "S1")).Member("count"), func(call chord.WatchCall[delta.JsonValue], _ Ctx) *Handled {
					mu.Lock()
					calls = append(calls, call.Value)
					mu.Unlock()
					return nil
				})
				return contracts.Implementation{}, nil
			}, ServeOptions{})
		})}}, background))
		peer := rawParent(t, client, link.PartLimits{})
		getting := asyncGet(client, routinesViewerOnly, jsonObject("id", 1.0), background, GetOptions{})
		hold := peer.Next("hold").(*link.HoldMessage).Hold
		editor := editorWire(t)
		failed := []link.FailedMember{{Member: editor.State["count"], Error: link.WireErrorOf(data.NewPluginError("internal", "count threw"))}}
		notesPart, err := link.JsonPart([]any{})
		must(t, err)
		peer.Send(&link.BoundMessage{Holds: []link.BoundEntry{{
			Hold: hold, Variant: editor.Variant, Members: []link.BoundMember{{Member: editor.State["notes"], Value: notesPart}}, Failed: failed,
		}}})
		awaitReference(t, getting)
		value := func(n float64) []link.StateMember {
			part, err := link.JsonPart([]any{[]any{"r", n}})
			must(t, err)
			return []link.StateMember{{Member: editor.State["count"], Ops: part}}
		}
		// Two state messages in one receive turn: the first value, then a failure again.
		peer.Send(&link.StateMessage{Holds: []link.StateEntry{{Hold: hold, Members: value(5), Failed: []link.FailedMember{}, Retired: []int{}}}})
		peer.Send(&link.StateMessage{Holds: []link.StateEntry{{Hold: hold, Members: []link.StateMember{}, Failed: failed, Retired: []int{}}}})
		turn(client)
		scopeEventually(t, "the failure to be applied", func() bool { turn(client); return true })
		mu.Lock()
		if len(calls) != 0 {
			t.Fatalf("calls = %v, want none", calls)
		}
		mu.Unlock()
		peer.Send(&link.StateMessage{Holds: []link.StateEntry{{Hold: hold, Members: value(6), Failed: []link.FailedMember{}, Retired: []int{}}}})
		scopeEventually(t, "the call with 6", func() bool { turn(client); mu.Lock(); defer mu.Unlock(); return len(calls) == 1 })
		mu.Lock()
		defer mu.Unlock()
		if !reflect.DeepEqual(calls, []any{6.0}) {
			t.Fatalf("calls = %v, want [6]", calls)
		}
		closeHosts(t, client)
	})
}

// "follows ref.state: a reference over a link going loading and live again".
func TestOnFollowsRefStateAReferenceOverALinkGoingLoadingAndLiveAgain(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		address := Server.Address(nil)
		server := newHost(t, HostOptions{Address: &address, Principal: serverPrincipal(), OnError: func(HostError) {}})
		must(t, server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			_, err := serveNotes(h, nil)
			return err
		})}}, background))
		var states strs
		client := newHost(t, HostOptions{OnError: func(HostError) {}})
		must(t, client.Load([]HostFacet{{Name: "@test/client", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(routinesViewerOnly, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				notes := self.Get(Notes, jsonObject("session", "S1"))
				self.OnNative(refStateSource{notes.State()}, func(call chord.NativeWatchCall[any], _ Ctx) *Handled {
					states.add(string(call.Value.(*RefState).Kind))
					return nil
				})
				return contracts.Implementation{}, nil
			}, ServeOptions{})
		})}}, background))
		connect := func() *link.MemoryLink {
			a, b := link.MemoryLinkPair()
			must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
			must(t, client.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
			return a
		}
		first := connect()
		mustGet(t, client, routinesViewerOnly, jsonObject("id", 1.0))
		scopeEventually(t, "the first live", func() bool { turn(client); return states.len() >= 1 })
		first.Close()
		scopeEventually(t, "loading", func() bool { turn(client); return states.len() >= 2 })
		connect()
		scopeEventually(t, "live again", func() bool { turn(client); return states.len() >= 3 })
		if got := states.all(); !reflect.DeepEqual(got, []string{"live", "loading", "live"}) {
			t.Fatalf("states = %v, want [live loading live]", got)
		}
		closeHosts(t, client, server)
	})
}
