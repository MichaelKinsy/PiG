package host

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/test/next/host/routines.test.ts. Upstream's cases build a host with `loaded` and an object whose
// factory declares the routines over its documents (`self.state(Doc)`, `ref.bump`), derives, references and links: those
// are later lanes. A case whose source is one of the test's own watchables (`jsonSource`, `writable`) and whose assertions
// are what the routine calls and reports runs here on a fake owner (routineOwner over a fake host), the owner's end being
// its context's abort; the vehicle `store(...)` is the only thing replaced. The cases that need a document, an object's
// end, a reference, a link or `host.close` are listed `missing` in the lane's rows, and the own tests below mirror what of
// them is the routines'. Handlers run on tasks of their own (a Go handler blocks where Pi's awaits), so a test waits for
// the calls it started (routineRig.quiesce) where Pi awaits its turns; time is the fake clock of Pi's `fakeTimers()`.

// routineRig is one owner of routines on a fake host.
type routineRig struct {
	t        testing.TB
	loop     *scopeLoop
	host     *scopeHost
	sched    *chord.DeliveryScheduler
	routines *routines
	owner    routineOwner
	end      func(cause error)

	mu       sync.Mutex
	admitted []<-chan struct{}
	cleanups []func()
	// onAdmit runs, in the section, when a call is admitted.
	onAdmit func()
	// resolve is the injected `resolveWatchable`.
	resolve func(value any) (state.DeriveSource, error)
}

func newRoutineRig(t testing.TB) *routineRig {
	t.Helper()
	loop := newScopeLoop()
	rig := &routineRig{t: t, loop: loop, host: newScopeHost(loop), sched: chord.NewDeliveryScheduler()}
	ctx, end := chordctx.WithCancel(chordctx.Background())
	rig.end = end
	t.Cleanup(func() { end(errors.New("the test ended")) })
	rig.resolve = func(any) (state.DeriveSource, error) { return nil, nil }
	rig.routines = newRoutines(func(value any) (state.DeriveSource, error) { return rig.resolve(value) })
	rig.owner = routineOwner{
		label:   "app.store",
		host:    rig.host,
		caller:  caller{incarnation: rig.host.Incarnation()},
		context: ctx,
		admit: func(done <-chan struct{}, cleanup func()) {
			rig.mu.Lock()
			rig.admitted = append(rig.admitted, done)
			rig.cleanups = append(rig.cleanups, cleanup)
			hook := rig.onAdmit
			rig.mu.Unlock()
			if hook != nil {
				hook()
			}
		},
	}
	return rig
}

// start is `routines.start(owner)` in a section.
func (r *routineRig) start() { r.loop.Run(func() { r.routines.start(r.owner) }) }

// quiesce waits for every admitted call to have settled and its continuation to have run.
func (r *routineRig) quiesce() {
	r.t.Helper()
	for done := 0; ; {
		r.mu.Lock()
		admitted := append([]<-chan struct{}{}, r.admitted...)
		r.mu.Unlock()
		for _, each := range admitted {
			<-each
		}
		r.loop.barrier()
		if done = len(admitted); done == r.admittedCount() {
			return
		}
	}
}

func (r *routineRig) admittedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.admitted)
}

// deliver waits for the frames the sources advanced to have reached their routines, and for the calls they started.
func (r *routineRig) deliver() {
	r.t.Helper()
	r.sched.Wait()
	r.quiesce()
}

// reports are the routine reports as `kind #position: message`.
func (r *routineRig) reports() []string {
	var out []string
	for _, report := range r.host.reported() {
		if report.Kind == ErrRoutineFailed {
			out = append(out, fmt.Sprintf("%s #%d: %s", report.Routine.Kind, report.Routine.Position, report.Error.Error()))
		}
	}
	return out
}

func wantStrings(t testing.TB, got []string, want ...string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// jsonFake is Pi's `jsonSource`: a JSON watchable (Chord's shared Watch) the test advances by hand.
type jsonFake struct {
	mu      sync.Mutex
	value   any
	sched   *chord.DeliveryScheduler
	watches []*chord.Watch[delta.JsonValue]
}

func newJSONFake(rig *routineRig, initial any) *jsonFake {
	return &jsonFake{value: initial, sched: rig.sched}
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
	watch.On(s.sched)
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
	for i, each := range s.watches {
		if each == watch {
			s.watches = append(s.watches[:i:i], s.watches[i+1:]...)
			return
		}
	}
}

func (s *jsonFake) snapshot() []*chord.Watch[delta.JsonValue] {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*chord.Watch[delta.JsonValue]{}, s.watches...)
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

func (s *jsonFake) fail(message string) {
	s.advance(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallFailed, Error: errors.New(message)})
}

func (s *jsonFake) end() {
	for _, watch := range s.snapshot() {
		watch.EndWith(chord.WatchEnd{Kind: chord.WatchEnded})
	}
}

func (s *jsonFake) count() int { return len(s.snapshot()) }

// nativeFake is Pi's `writable`: a native watchable the test sets and ends by hand.
type nativeFake struct {
	mu      sync.Mutex
	value   any
	sched   *chord.DeliveryScheduler
	watches []*chord.NativeWatch[any]
}

func newNativeFake(rig *routineRig, initial any) *nativeFake {
	return &nativeFake{value: initial, sched: rig.sched}
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
	watch.On(s.sched)
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
	for i, each := range s.watches {
		if each == watch {
			s.watches = append(s.watches[:i:i], s.watches[i+1:]...)
			return
		}
	}
}

func (s *nativeFake) snapshot() []*chord.NativeWatch[any] {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*chord.NativeWatch[any]{}, s.watches...)
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

// nObject is `{ n }`.
func nObject(n float64) *delta.JsonObject { return delta.JsonObjectOf("n", n) }

// replaceOps is `[["r", value]]`; setN is `[["s", ["n"], n]]`.
func replaceOps(value any) []delta.Op { return []delta.Op{{"r", value}} }
func setN(n float64) delta.Op         { return delta.Op{"s", []any{"n"}, n} }

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
	return append([]routineCall{}, l.calls...)
}

func (l *routineLog) len() int { return len(l.all()) }

// handler is an `on` handler that records its calls and then runs after (which may be nil); its error is the call's.
func (l *routineLog) handler(rig *routineRig, after func(n int, call chord.WatchCall[delta.JsonValue], ctx Ctx) error) OnHandler[delta.JsonValue] {
	return func(call chord.WatchCall[delta.JsonValue], ctx Ctx) error {
		n := l.add(routineCall{value: call.Value, ops: call.Ops, retired: call.Retired, at: rig.loop.Now()})
		if after == nil {
			return nil
		}
		return after(n, call, ctx)
	}
}

// wantCalls checks the (value n, ops) of every call.
func wantCalls(t testing.TB, got []routineCall, want ...routineCall) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("calls = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		same := nexttest.JSONEqual(got[i].value, want[i].value) && got[i].retired == want[i].retired &&
			nexttest.JSONEqual(opsAsAny(got[i].ops), opsAsAny(want[i].ops))
		if want[i].at >= 0 && got[i].at != want[i].at {
			same = false
		}
		if !same {
			t.Fatalf("call %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func opsAsAny(ops []delta.Op) []any {
	out := make([]any, len(ops))
	for i, op := range ops {
		out[i] = op
	}
	return out
}

// call is wantCalls' shorthand; at < 0 is not compared.
func wantCall(value any, ops []delta.Op, at int64) routineCall {
	return routineCall{value: value, ops: ops, at: at}
}

func TestOnFollowsAnAppsJSONWatchableWhoseWatchIsChordsSharedWatchWithOpsAsItsTypeSays(t *testing.T) {
	rig := newRoutineRig(t)
	source := newJSONFake(rig, nObject(0))
	log := &routineLog{}
	rig.routines.On(source, log.handler(rig, nil))
	rig.start()
	rig.deliver()
	source.change(1, setN(1))
	rig.deliver()
	wantCalls(t, log.all(), wantCall(nObject(0), replaceOps(nObject(0)), -1), wantCall(nObject(1), []delta.Op{setN(1)}, -1))
}

func TestOnReportsARetirementAsRetiredFoldedWithTheChangesAroundItIntoOneCallAndGoesOn(t *testing.T) {
	rig := newRoutineRig(t)
	source := newJSONFake(rig, nObject(0))
	gate := make(chan struct{})
	log := &routineLog{}
	rig.routines.On(source, log.handler(rig, func(n int, _ chord.WatchCall[delta.JsonValue], _ Ctx) error {
		if n == 1 {
			<-gate
		}
		return nil
	}))
	rig.start()
	scopeEventually(t, "the first call", func() bool { return log.len() == 1 })
	// A Durable document retired (the last value, no ops) and created again, while the first call runs.
	source.advance(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallValue, Value: nObject(0), Ops: []delta.Op{}, Retired: true})
	source.advance(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallValue, Value: nObject(5), Ops: replaceOps(nObject(5))})
	rig.sched.Wait()
	close(gate)
	rig.quiesce()
	rig.loop.Advance(1)
	rig.quiesce()
	source.change(6, setN(6))
	rig.deliver()
	got := log.all()
	wantCalls(t, got,
		wantCall(nObject(0), replaceOps(nObject(0)), -1),
		routineCall{value: nObject(5), ops: replaceOps(nObject(5)), retired: true, at: -1},
		wantCall(nObject(6), []delta.Op{setN(6)}, -1))
}

func TestOnFoldsANativeSourceToItsNewestValueAfterABusyCall(t *testing.T) {
	rig := newRoutineRig(t)
	source := newNativeFake(rig, "a")
	gate := make(chan struct{})
	var mu sync.Mutex
	var calls []any
	rig.routines.OnNative(source, func(call chord.NativeWatchCall[any], _ Ctx) error {
		mu.Lock()
		calls = append(calls, call.Value)
		first := len(calls) == 1
		mu.Unlock()
		if first {
			<-gate
		}
		return nil
	})
	rig.start()
	scopeEventually(t, "the first call", func() bool { mu.Lock(); defer mu.Unlock(); return len(calls) == 1 })
	source.set("b")
	source.set("c")
	rig.sched.Wait()
	close(gate)
	rig.quiesce()
	rig.loop.Advance(1)
	rig.quiesce()
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(calls) != "[a c]" {
		t.Fatalf("calls = %v, want [a c]", calls)
	}
}

func TestOnTakesEveryValueABusyCallLeftBufferedBeforeCallingAgain0And20AlsoWhenItSetThemItself(t *testing.T) {
	rig := newRoutineRig(t)
	source := newNativeFake(rig, 0.0)
	var mu sync.Mutex
	var calls []any
	rig.routines.OnNative(source, func(call chord.NativeWatchCall[any], _ Ctx) error {
		mu.Lock()
		calls = append(calls, call.Value)
		mu.Unlock()
		if call.Value == 0.0 {
			for n := 1; n <= 20; n++ {
				source.set(float64(n))
			}
			// Pi's microtasks deliver the buffered values before the call's own end is seen; here the replay runs on the
			// watch's delivery task, so the call waits for it, in the order Pi gives.
			rig.sched.Wait()
		}
		return nil
	})
	rig.start()
	rig.quiesce()
	rig.loop.Advance(1)
	rig.quiesce()
	mu.Lock()
	if fmt.Sprint(calls) != "[0 20]" {
		t.Fatalf("calls = %v, want [0 20]", calls)
	}
	mu.Unlock()

	rig = newRoutineRig(t)
	later := newNativeFake(rig, 0.0)
	gate := make(chan struct{})
	var seen []any
	rig.routines.OnNative(later, func(call chord.NativeWatchCall[any], _ Ctx) error {
		mu.Lock()
		seen = append(seen, call.Value)
		mu.Unlock()
		if call.Value == 0.0 {
			<-gate
		}
		return nil
	})
	rig.start()
	scopeEventually(t, "the call with 0", func() bool { mu.Lock(); defer mu.Unlock(); return len(seen) == 1 })
	for n := 1; n <= 20; n++ {
		later.set(float64(n))
	}
	rig.sched.Wait()
	close(gate)
	rig.quiesce()
	rig.loop.Advance(1)
	rig.quiesce()
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(seen) != "[0 20]" {
		t.Fatalf("calls = %v, want [0 20]", seen)
	}
}

func TestOnReportsASourceWhoseWatchCannotBeTakenASourceThatEndedMeanwhileEndsItQuietly(t *testing.T) {
	rig := newRoutineRig(t)
	calls := 0
	rig.routines.OnNative(brokenWatchable{}, func(chord.NativeWatchCall[any], Ctx) error { calls++; return nil })
	rig.routines.OnNative(endedWatchable{}, func(chord.NativeWatchCall[any], Ctx) error { calls++; return nil })
	rig.start()
	rig.deliver()
	rig.loop.barrier()
	if calls != 0 {
		t.Fatalf("calls = %d, want none", calls)
	}
	wantStrings(t, rig.reports(), "on #1: no watch")
}

// brokenWatchable is `watch: async () => { throw new Error("no watch") }`.
type brokenWatchable struct{}

func (brokenWatchable) Value() any { return 0.0 }
func (brokenWatchable) Watch(context.Context) (chord.NativeWatchHandle[any], error) {
	return nil, errors.New("no watch")
}

// endedWatchable is a source whose watch ended while it was taken.
type endedWatchable struct{}

func (endedWatchable) Value() any { return 0.0 }
func (endedWatchable) Watch(context.Context) (chord.NativeWatchHandle[any], error) {
	watch := chord.NewNativeWatch[any](0.0, func() {}, func(error) {}, nil)
	watch.EndWith(chord.WatchEnd{Kind: chord.WatchEnded})
	return watch, nil
}

func TestOnFollowsANativeSourceOneCallPerNewValueWithoutOpsEndsWithIt(t *testing.T) {
	rig := newRoutineRig(t)
	source := newNativeFake(rig, "a")
	var mu sync.Mutex
	var calls []chord.NativeWatchCall[any]
	rig.routines.OnNative(source, func(call chord.NativeWatchCall[any], _ Ctx) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, call)
		return nil
	})
	rig.start()
	rig.deliver()
	source.set("b")
	source.set("c")
	rig.sched.Wait()
	rig.quiesce()
	rig.loop.Advance(1)
	rig.quiesce()
	mu.Lock()
	want := []chord.NativeWatchCall[any]{{Kind: "value", Value: "a"}, {Kind: "value", Value: "b"}, {Kind: "value", Value: "c"}}
	got := append([]chord.NativeWatchCall[any]{}, calls...)
	mu.Unlock()
	// Values arriving while a call runs fold: the call for "b" may be skipped, "a" and "c" never.
	if len(got) < 2 || got[0] != want[0] || got[len(got)-1] != want[2] {
		t.Fatalf("calls = %+v, want a first and c last", got)
	}
	source.end()
	scopeEventually(t, "the watch released", func() bool { return source.count() == 0 })
	wantStrings(t, rig.reports())
}

func TestOnLiveIsCalledOnceWhenItsObjectIsLiveReturningEndsIt(t *testing.T) {
	rig := newRoutineRig(t)
	var calls int
	rig.routines.OnLive(func(Ctx) error { calls++; return nil })
	rig.start()
	rig.quiesce()
	rig.loop.Advance(10_000)
	rig.quiesce()
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestOnLiveRestartsAfterAThrowWithTheBackoffResetAfterARunOfAtLeastAMinute(t *testing.T) {
	rig := newRoutineRig(t)
	var mu sync.Mutex
	var starts []int64
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(starts) }
	rig.routines.OnLive(func(ctx Ctx) error {
		mu.Lock()
		starts = append(starts, rig.loop.Now())
		n := len(starts)
		mu.Unlock()
		if n == 3 {
			if err := sleepOn(rig.loop, 60*time.Second, ctx); err != nil {
				return err
			}
		}
		return fmt.Errorf("run %d", n)
	})
	rig.start()
	rig.quiesce()
	rig.loop.Advance(1_000)
	rig.quiesce()
	// Pi advances 2 s + 60 s in one call, its microtasks running between timers; the third run's sleep is a timer of the
	// handler's own task here, which the clock reaches once it is set.
	rig.loop.Advance(2_000)
	scopeEventually(t, "the third run sleeping", func() bool { return count() == 3 && rig.loop.TimerCount() == 1 })
	rig.loop.Advance(60_000)
	rig.quiesce()
	rig.loop.Advance(1_000)
	rig.quiesce()
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(starts) != "[0 1000 3000 64000]" {
		t.Fatalf("starts = %v, want [0 1000 3000 64000]", starts)
	}
	wantStrings(t, rig.reports(), "onLive #1: run 1", "onLive #1: run 2", "onLive #1: run 3", "onLive #1: run 4")
}

func TestOnTypesTheirHandlerByTheSourcesKindOpsAndRetiredForAJSONWatchableTheValueAloneForANativeOne(t *testing.T) {
	// Carries "routines and the end › type their handler by the source's kind" (type-level upstream): the JSON handler's
	// call carries ops and retired, the native one's only the value, and RoutineDeclarations has one method per kind.
	json := reflect.TypeFor[OnHandler[delta.JsonValue]]()
	native := reflect.TypeFor[OnNativeHandler[any]]()
	for _, field := range []string{"Value", "Ops", "Retired"} {
		if _, ok := json.In(0).FieldByName(field); !ok {
			t.Fatalf("a JSON watch call has no %s", field)
		}
	}
	if _, ok := native.In(0).FieldByName("Value"); !ok {
		t.Fatal("a native watch call has no Value")
	}
	for _, field := range []string{"Ops", "Retired"} {
		if _, ok := native.In(0).FieldByName(field); ok {
			t.Fatalf("a native watch call has %s", field)
		}
	}
	if json.In(1) != reflect.TypeFor[Ctx]() || native.In(1) != reflect.TypeFor[Ctx]() {
		t.Fatal("a handler's ctx is not Ctx")
	}
	declarations := reflect.TypeFor[RoutineDeclarations]()
	var names []string
	for i := range declarations.NumMethod() {
		names = append(names, declarations.Method(i).Name)
	}
	if fmt.Sprint(names) != "[On OnLive OnNative]" {
		t.Fatalf("RoutineDeclarations = %v", names)
	}
}

// Own tests: the routines' part of the cases that need a document, an object's end or a reference. Each names the Pi case
// it mirrors; the owner's end is its context's abort and a document is a jsonFake.

// Mirrors "on › never overlaps calls: changes during a call fold into the next, which is not aborted by them".
func TestOnNeverOverlapsCallsChangesDuringACallFoldIntoTheNextWhichIsNotAbortedByThem(t *testing.T) {
	rig := newRoutineRig(t)
	source := newJSONFake(rig, nObject(0))
	gate := make(chan struct{})
	var mu sync.Mutex
	running, overlapped, aborted := 0, false, false
	log := &routineLog{}
	rig.routines.On(source, log.handler(rig, func(n int, _ chord.WatchCall[delta.JsonValue], ctx Ctx) error {
		mu.Lock()
		overlapped = overlapped || running > 0
		running++
		mu.Unlock()
		if n == 2 {
			<-gate
		}
		mu.Lock()
		aborted = aborted || ctx.Err() != nil
		running--
		mu.Unlock()
		return nil
	}))
	rig.start()
	rig.deliver()
	source.change(1, setN(1))
	rig.sched.Wait()
	scopeEventually(t, "the second call, which waits for the gate", func() bool { return log.len() == 2 })
	source.change(2, setN(2))
	source.change(3, setN(3))
	rig.sched.Wait()
	close(gate)
	rig.quiesce()
	rig.loop.Advance(1)
	rig.quiesce()
	got := log.all()
	if len(got) != 3 || !nexttest.JSONEqual(got[0].value, nObject(0)) || !nexttest.JSONEqual(got[1].value, nObject(1)) || !nexttest.JSONEqual(got[2].value, nObject(3)) {
		t.Fatalf("calls = %+v, want n 0, 1, 3", got)
	}
	nexttest.RequireJSONEqual(t, opsAsAny(got[2].ops), opsAsAny([]delta.Op{setN(3)}))
	mu.Lock()
	defer mu.Unlock()
	if overlapped || aborted {
		t.Fatalf("overlapped %v, aborted %v", overlapped, aborted)
	}
}

// Mirrors "on › folds every change made during the first call into one next call: [0, 3], not [0, 1, 3]".
func TestOnFoldsEveryChangeMadeDuringTheFirstCallIntoOneNextCall(t *testing.T) {
	rig := newRoutineRig(t)
	source := newJSONFake(rig, nObject(0))
	gate := make(chan struct{})
	log := &routineLog{}
	rig.routines.On(source, log.handler(rig, func(n int, _ chord.WatchCall[delta.JsonValue], _ Ctx) error {
		if n == 1 {
			<-gate
		}
		return nil
	}))
	rig.start()
	scopeEventually(t, "the first call", func() bool { return log.len() == 1 })
	for n := 1.0; n <= 3; n++ {
		source.change(n, setN(n))
	}
	rig.sched.Wait()
	close(gate)
	rig.quiesce()
	rig.loop.Advance(1)
	rig.quiesce()
	wantCalls(t, log.all(), wantCall(nObject(0), replaceOps(nObject(0)), -1), wantCall(nObject(3), []delta.Op{setN(3)}, -1))
}

// Mirrors "on › after a throw › reports, keeps the watch, and calls again with [['r', value]] after 1 s doubling; success
// resets".
func TestOnAfterAThrowReportsKeepsTheWatchAndCallsAgainWithTheWholeValueAfter1sDoublingSuccessResets(t *testing.T) {
	rig := newRoutineRig(t)
	source := newJSONFake(rig, nObject(0))
	var mu sync.Mutex
	failing := 3
	log := &routineLog{}
	rig.routines.On(source, log.handler(rig, func(n int, _ chord.WatchCall[delta.JsonValue], _ Ctx) error {
		mu.Lock()
		defer mu.Unlock()
		failing--
		if failing+1 > 0 {
			return fmt.Errorf("fail %d", n)
		}
		return nil
	}))
	rig.start()
	rig.quiesce()
	rig.loop.Advance(1_000)
	rig.quiesce()
	rig.loop.Advance(2_000)
	rig.quiesce()
	rig.loop.Advance(4_000)
	rig.quiesce()
	times := func() (out []int64) {
		for _, call := range log.all() {
			out = append(out, call.at)
		}
		return out
	}
	if fmt.Sprint(times()) != "[0 1000 3000 7000]" {
		t.Fatalf("call times = %v, want [0 1000 3000 7000]", times())
	}
	for _, call := range log.all() {
		if len(call.ops) != 1 || call.ops[0][0] != "r" {
			t.Fatalf("a retry's ops = %v, want a replacement", call.ops)
		}
	}
	wantStrings(t, rig.reports(), "on #1: fail 1", "on #1: fail 2", "on #1: fail 3")
	// Succeeded: the next change gets its ops, and a later throw backs off from 1 s again.
	source.change(1, setN(1))
	rig.deliver()
	last := log.all()[log.len()-1]
	if !nexttest.JSONEqual(last.value, nObject(1)) || !nexttest.JSONEqual(opsAsAny(last.ops), opsAsAny([]delta.Op{setN(1)})) {
		t.Fatalf("last call = %+v", last)
	}
	mu.Lock()
	failing = 1
	mu.Unlock()
	source.change(2, setN(2))
	rig.deliver()
	rig.loop.Advance(1_000)
	rig.quiesce()
	if all := times(); fmt.Sprint(all[len(all)-2:]) != "[7000 8000]" {
		t.Fatalf("call times = %v, want 7000 then 8000 last", all)
	}
	if rig.loop.TimerCount() != 0 {
		t.Fatalf("timers left = %d", rig.loop.TimerCount())
	}
}

// Mirrors "on › after a throw › retries a change folded behind the throwing call after the backoff, once: no third call,
// no timer".
func TestOnAfterAThrowRetriesAChangeFoldedBehindTheThrowingCallAfterTheBackoffOnce(t *testing.T) {
	rig := newRoutineRig(t)
	source := newJSONFake(rig, nObject(0))
	gate := make(chan struct{})
	log := &routineLog{}
	rig.routines.On(source, log.handler(rig, func(n int, _ chord.WatchCall[delta.JsonValue], _ Ctx) error {
		if n == 1 {
			<-gate
			return errors.New("first")
		}
		return nil
	}))
	rig.start()
	scopeEventually(t, "the first call", func() bool { return log.len() == 1 })
	source.change(1, setN(1))
	rig.sched.Wait()
	close(gate)
	rig.quiesce()
	// Pi advances 5 s in one call: the retry at 1 s runs with the clock at 1 s. Its handler reads the clock on its own
	// task here, so the clock moves to the timer first.
	rig.loop.Advance(1_000)
	rig.quiesce()
	rig.loop.Advance(4_000)
	rig.quiesce()
	wantCalls(t, log.all(), wantCall(nObject(0), replaceOps(nObject(0)), 0), wantCall(nObject(1), replaceOps(nObject(1)), 1_000))
	if rig.loop.TimerCount() != 0 {
		t.Fatalf("timers left = %d", rig.loop.TimerCount())
	}
}

// Mirrors "on › after a throw › backs off after a throw even when the failed call changed its own source: no burst".
func TestOnAfterAThrowBacksOffEvenWhenTheFailedCallChangedItsOwnSourceNoBurst(t *testing.T) {
	rig := newRoutineRig(t)
	source := newJSONFake(rig, nObject(0))
	log := &routineLog{}
	rig.routines.On(source, log.handler(rig, func(n int, _ chord.WatchCall[delta.JsonValue], _ Ctx) error {
		if n > 100 {
			return nil
		}
		// A reconciler that records its attempt in its own source, then fails.
		source.change(float64(n), setN(float64(n)))
		rig.sched.Wait()
		return errors.New("downstream unavailable")
	}))
	rig.start()
	rig.quiesce()
	wantCalls(t, log.all(), wantCall(nObject(0), replaceOps(nObject(0)), 0))
	rig.loop.Advance(1_000)
	rig.quiesce()
	rig.loop.Advance(2_000)
	rig.quiesce()
	got := log.all()
	if len(got) != 3 || got[1].at != 1_000 || got[2].at != 3_000 || len(rig.reports()) != 3 {
		t.Fatalf("calls %+v, reports %v, want three calls at 0, 1000 and 3000", got, rig.reports())
	}
}

// Mirrors "on › after a throw › retries with the value that arrived during the failed call, as a whole value".
func TestOnAfterAThrowRetriesWithTheValueThatArrivedDuringTheFailedCallAsAWholeValue(t *testing.T) {
	rig := newRoutineRig(t)
	source := newJSONFake(rig, nObject(0))
	log := &routineLog{}
	rig.routines.On(source, log.handler(rig, func(n int, _ chord.WatchCall[delta.JsonValue], _ Ctx) error {
		if n > 1 {
			return nil
		}
		source.change(1, setN(1))
		rig.sched.Wait()
		return errors.New("once")
	}))
	rig.start()
	rig.quiesce()
	rig.loop.Advance(1_000)
	rig.quiesce()
	wantCalls(t, log.all(), wantCall(nObject(0), replaceOps(nObject(0)), 0), wantCall(nObject(1), replaceOps(nObject(1)), 1_000))
}

// Mirrors "on › after a throw › folds changes made during a slow retry into one call with the newest value".
func TestOnAfterAThrowFoldsChangesMadeDuringASlowRetryIntoOneCallWithTheNewestValue(t *testing.T) {
	rig := newRoutineRig(t)
	source := newJSONFake(rig, nObject(0))
	gate := make(chan struct{})
	log := &routineLog{}
	rig.routines.On(source, log.handler(rig, func(n int, _ chord.WatchCall[delta.JsonValue], _ Ctx) error {
		switch n {
		case 1:
			return errors.New("first")
		case 2:
			<-gate
		}
		return nil
	}))
	rig.start()
	rig.quiesce()
	rig.loop.Advance(1_000)
	scopeEventually(t, "the slow retry", func() bool { return log.len() == 2 })
	for n := 1.0; n <= 3; n++ {
		source.change(n, setN(n))
	}
	rig.sched.Wait()
	close(gate)
	rig.quiesce()
	rig.loop.Advance(1)
	rig.quiesce()
	wantCalls(t, log.all(),
		wantCall(nObject(0), replaceOps(nObject(0)), -1),
		wantCall(nObject(0), replaceOps(nObject(0)), -1),
		wantCall(nObject(3), []delta.Op{setN(3)}, -1))
}

// Mirrors "on › after a throw › waits for the next good value when its source fails during the backoff: the retry is
// dropped".
func TestOnAfterAThrowWaitsForTheNextGoodValueWhenItsSourceFailsDuringTheBackoffTheRetryIsDropped(t *testing.T) {
	rig := newRoutineRig(t)
	source := newJSONFake(rig, nObject(0))
	log := &routineLog{}
	rig.routines.On(source, log.handler(rig, func(_ int, call chord.WatchCall[delta.JsonValue], _ Ctx) error {
		if nexttest.JSONEqual(call.Value, nObject(1)) {
			return errors.New("handler threw")
		}
		return nil
	}))
	rig.start()
	rig.deliver()
	source.change(1, setN(1))
	rig.deliver()
	source.fail("compute")
	rig.deliver()
	rig.loop.Advance(2_000)
	rig.quiesce()
	if log.len() != 2 || rig.loop.TimerCount() != 0 {
		t.Fatalf("calls = %d, timers = %d, want 2 calls and no timer", log.len(), rig.loop.TimerCount())
	}
	source.change(3, delta.Op{"r", nObject(3)})
	rig.deliver()
	got := log.all()
	if len(got) != 3 || !nexttest.JSONEqual(got[2].value, nObject(3)) {
		t.Fatalf("calls = %+v, want a third call with n 3", got)
	}
	wantStrings(t, rig.reports(), "on #1: handler threw")
}

// Mirrors "on › after a throw › clears its retry timer when its object ends during the backoff".
func TestOnAfterAThrowClearsItsRetryTimerWhenItsOwnerEndsDuringTheBackoff(t *testing.T) {
	rig := newRoutineRig(t)
	source := newJSONFake(rig, nObject(0))
	rig.routines.On(source, (&routineLog{}).handler(rig, func(int, chord.WatchCall[delta.JsonValue], Ctx) error {
		return errors.New("always")
	}))
	rig.start()
	rig.quiesce()
	if rig.loop.TimerCount() != 1 {
		t.Fatalf("timers = %d, want the backoff's", rig.loop.TimerCount())
	}
	rig.end(errors.New("owner ended"))
	scopeEventually(t, "the retry timer cleared", func() bool { return rig.loop.TimerCount() == 0 })
}

// Mirrors "on › after a throw › reports a retirement again on the retry after the call that got it threw".
func TestOnAfterAThrowReportsARetirementAgainOnTheRetryAfterTheCallThatGotItThrew(t *testing.T) {
	rig := newRoutineRig(t)
	source := newJSONFake(rig, nObject(0))
	log := &routineLog{}
	rig.routines.On(source, log.handler(rig, func(n int, _ chord.WatchCall[delta.JsonValue], _ Ctx) error {
		if n == 2 {
			return errors.New("retirement not handled")
		}
		return nil
	}))
	rig.start()
	rig.deliver()
	source.advance(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallValue, Value: nObject(0), Ops: []delta.Op{}, Retired: true})
	rig.deliver()
	rig.loop.Advance(1_000)
	rig.quiesce()
	rig.loop.Advance(10_000)
	rig.quiesce()
	got := log.all()[1:]
	if len(got) != 2 || !got[0].retired || len(got[0].ops) != 0 || !got[1].retired ||
		!nexttest.JSONEqual(opsAsAny(got[1].ops), opsAsAny(replaceOps(nObject(0)))) {
		t.Fatalf("calls after the first = %+v, want the retirement twice, the second as a whole value", got)
	}
}

// Mirrors "on › after a throw › calls at once with the newest whole value when the source changes during the backoff".
func TestOnAfterAThrowCallsAtOnceWithTheNewestWholeValueWhenTheSourceChangesDuringTheBackoff(t *testing.T) {
	rig := newRoutineRig(t)
	source := newJSONFake(rig, nObject(0))
	log := &routineLog{}
	rig.routines.On(source, log.handler(rig, func(n int, _ chord.WatchCall[delta.JsonValue], _ Ctx) error {
		if n == 1 {
			return errors.New("first")
		}
		return nil
	}))
	rig.start()
	rig.quiesce()
	rig.loop.Advance(500)
	source.change(1, setN(1))
	rig.deliver()
	source.change(2, setN(2))
	rig.deliver()
	wantCalls(t, log.all(),
		wantCall(nObject(0), replaceOps(nObject(0)), 0),
		wantCall(nObject(1), replaceOps(nObject(1)), 500),
		wantCall(nObject(2), []delta.Op{setN(2)}, 500))
	// The backoff timer is gone: nothing calls again.
	rig.loop.Advance(10_000)
	rig.quiesce()
	if log.len() != 3 {
		t.Fatalf("calls = %d, want 3", log.len())
	}
}

// Mirrors "on › ends without a report when its source ends".
func TestOnEndsWithoutAReportWhenItsSourceEnds(t *testing.T) {
	rig := newRoutineRig(t)
	source := newJSONFake(rig, nObject(0))
	log := &routineLog{}
	rig.routines.On(source, log.handler(rig, nil))
	rig.start()
	rig.deliver()
	source.end()
	scopeEventually(t, "the watch released", func() bool { return source.count() == 0 })
	rig.loop.barrier()
	wantCalls(t, log.all(), wantCall(nObject(0), replaceOps(nObject(0)), -1))
	wantStrings(t, rig.reports())
}

// Mirrors "on › is not called for its source's failures: it waits for the next value (a derive healing)".
func TestOnIsNotCalledForItsSourcesFailuresItWaitsForTheNextValue(t *testing.T) {
	rig := newRoutineRig(t)
	source := newJSONFake(rig, nObject(0))
	log := &routineLog{}
	rig.routines.On(source, log.handler(rig, nil))
	rig.start()
	rig.deliver()
	source.fail("compute")
	rig.deliver()
	source.fail("compute")
	rig.deliver()
	if log.len() != 1 {
		t.Fatalf("calls = %d, want only the first", log.len())
	}
	source.change(3, delta.Op{"r", nObject(3)})
	rig.deliver()
	wantCalls(t, log.all(), wantCall(nObject(0), replaceOps(nObject(0)), -1), wantCall(nObject(3), replaceOps(nObject(3)), -1))
	wantStrings(t, rig.reports())
}

// Mirrors "on › drops a value queued behind a busy call when a failure follows it; the next call is the healed value".
func TestOnDropsAValueQueuedBehindABusyCallWhenAFailureFollowsItTheNextCallIsTheHealedValue(t *testing.T) {
	rig := newRoutineRig(t)
	source := newJSONFake(rig, nObject(0))
	gate := make(chan struct{})
	log := &routineLog{}
	rig.routines.On(source, log.handler(rig, func(n int, _ chord.WatchCall[delta.JsonValue], _ Ctx) error {
		if n == 2 {
			<-gate
		}
		return nil
	}))
	rig.start()
	rig.deliver()
	source.change(1, setN(1))
	scopeEventually(t, "the busy call", func() bool { return log.len() == 2 })
	// Queued behind the busy call, then replaced by the failure.
	source.change(2, setN(2))
	rig.sched.Wait()
	source.fail("compute")
	rig.sched.Wait()
	close(gate)
	rig.quiesce()
	rig.loop.Advance(1)
	rig.quiesce()
	if log.len() != 2 {
		t.Fatalf("calls = %d, want the first two only", log.len())
	}
	source.change(3, delta.Op{"r", nObject(3)})
	rig.deliver()
	rig.loop.Advance(1)
	rig.quiesce()
	got := log.all()
	if len(got) != 3 || !nexttest.JSONEqual(got[2].value, nObject(3)) {
		t.Fatalf("calls = %+v, want n 0, 1, 3", got)
	}
	wantStrings(t, rig.reports())
}

// Mirrors "on › releases its watch when its object ends; a call that throws as it ends is not reported".
func TestOnReleasesItsWatchWhenItsOwnerEndsACallThatThrowsAsItEndsIsNotReported(t *testing.T) {
	rig := newRoutineRig(t)
	source := newNativeFake(rig, 0.0)
	rig.routines.OnNative(source, func(_ chord.NativeWatchCall[any], ctx Ctx) error {
		UntilAborted(ctx)
		return errors.New("stopping")
	})
	rig.start()
	scopeEventually(t, "the watch taken", func() bool { return source.count() == 1 })
	rig.end(errors.New("owner ended"))
	rig.quiesce()
	scopeEventually(t, "the watch released", func() bool { return source.count() == 0 })
	wantStrings(t, rig.reports())
}

// Mirrors "onLive › is aborted by its object's end: no report, no restart".
func TestOnLiveIsAbortedByItsOwnersEndNoReportNoRestart(t *testing.T) {
	rig := newRoutineRig(t)
	var mu sync.Mutex
	calls := 0
	rig.routines.OnLive(func(ctx Ctx) error {
		mu.Lock()
		calls++
		mu.Unlock()
		return sleepOn(rig.loop, 1_000_000*time.Millisecond, ctx)
	})
	rig.start()
	scopeEventually(t, "the sleeping handler", func() bool { return rig.loop.TimerCount() == 1 })
	rig.end(errors.New("owner ended"))
	rig.quiesce()
	rig.loop.Advance(10_000)
	rig.quiesce()
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 || rig.loop.TimerCount() != 0 {
		t.Fatalf("calls %d, timers %d, want one call and no timer", calls, rig.loop.TimerCount())
	}
	wantStrings(t, rig.reports())
}

// Mirrors "routines and the end › never start a routine call once the object ended in its first turn: no call, no report,
// no rejection": an owner already ended starts nothing, and a routine whose start ends the owner starts none of the later
// ones (its admission, which runs in the start's section, ends it here).
func TestRoutinesNeverStartARoutineCallOnceTheOwnerEndedInItsFirstTurn(t *testing.T) {
	rig := newRoutineRig(t)
	var mu sync.Mutex
	var calls []string
	record := func(name string) OnLiveHandler {
		return func(Ctx) error { mu.Lock(); calls = append(calls, name); mu.Unlock(); return nil }
	}
	source := newJSONFake(rig, nObject(0))
	rig.routines.On(source, func(chord.WatchCall[delta.JsonValue], Ctx) error {
		mu.Lock()
		calls = append(calls, "on")
		mu.Unlock()
		return nil
	})
	rig.routines.OnLive(record("onLive #1"))
	rig.routines.OnLive(record("onLive #2"))
	// The `on` routine is declared first: it starts, but its first call comes a microtask later; `onLive #1`'s admission
	// ends the owner, so `onLive #2` never starts and the `on` routine makes no call.
	rig.onAdmit = func() { rig.end(errors.New("owner ended")) }
	rig.start()
	rig.quiesce()
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(calls) != "[onLive #1]" {
		t.Fatalf("calls = %v, want only onLive #1", calls)
	}
	wantStrings(t, rig.reports())
	// An owner that ended before it started starts nothing.
	ended := newRoutineRig(t)
	ended.end(errors.New("owner ended"))
	ended.routines.OnLive(func(Ctx) error { t.Error("an onLive ran for an owner that ended"); return nil })
	ended.start()
	ended.quiesce()
	if ended.admittedCount() != 0 {
		t.Fatalf("admitted = %d", ended.admittedCount())
	}
}

// Mirrors "routines and the end › are in the drain before their handler runs": a call is admitted, with the cleanup that
// settles its ctx, before its handler starts, and its done channel closes when it settled.
func TestRoutinesAreInTheDrainBeforeTheirHandlerRuns(t *testing.T) {
	rig := newRoutineRig(t)
	var admittedWhenStarted int
	rig.routines.OnLive(func(Ctx) error {
		admittedWhenStarted = rig.admittedCount()
		return nil
	})
	rig.start()
	rig.quiesce()
	if admittedWhenStarted != 1 {
		t.Fatalf("calls admitted when the handler started = %d, want 1", admittedWhenStarted)
	}
	select {
	case <-rig.admitted[0]:
	default:
		t.Fatal("the admitted call is not done after it settled")
	}
}

// Mirrors "routines and the end › make no call queued behind a busy one once the object ended".
func TestRoutinesMakeNoCallQueuedBehindABusyOneOnceTheOwnerEnded(t *testing.T) {
	rig := newRoutineRig(t)
	source := newJSONFake(rig, nObject(0))
	gate := make(chan struct{})
	log := &routineLog{}
	rig.routines.On(source, log.handler(rig, func(int, chord.WatchCall[delta.JsonValue], Ctx) error {
		<-gate
		return nil
	}))
	rig.start()
	scopeEventually(t, "the busy call", func() bool { return log.len() == 1 })
	source.change(1, setN(1))
	rig.sched.Wait()
	rig.end(errors.New("owner ended"))
	close(gate)
	rig.quiesce()
	rig.loop.Advance(1_000)
	rig.quiesce()
	wantCalls(t, log.all(), wantCall(nObject(0), replaceOps(nObject(0)), -1))
}

// Mirrors "routines and the end › release their calls' ctx.get references when each call settles".
func TestRoutinesReleaseTheirCallsCtxGetReferencesWhenEachCallSettles(t *testing.T) {
	rig := newRoutineRig(t)
	Other := scopeItem(t, "app.other")
	binding := &scopeBinding{reference: "other"}
	rig.host.acquire = func(scopeAcquire) (acquired, error) { return binding, nil }
	rig.routines.OnLive(func(ctx Ctx) error {
		_, err := ctx.Get(Other, delta.JsonObjectOf("id", 1.0), GetOptions{})
		return err
	})
	rig.start()
	rig.quiesce()
	if binding.released() != 1 {
		t.Fatalf("the call's reference was released %d times, want once", binding.released())
	}
}

// Mirrors "routines and the end › act as a remote object's principal".
func TestRoutinesActAsTheOwnersCallerPrincipal(t *testing.T) {
	rig := newRoutineRig(t)
	guest := delta.JsonObjectOf("kind", "guest", "session", "S1")
	rig.owner.caller = caller{principal: guest, incarnation: "peer"}
	var seen seenCaller
	rig.routines.OnLive(func(ctx Ctx) error {
		seen = seenCaller{ctx.Principal(), ctx.Incarnation()}
		return nil
	})
	rig.start()
	rig.quiesce()
	if seen.principal != guest || seen.incarnation != "peer" {
		t.Fatalf("seen = %+v", seen)
	}
}

type seenCaller struct {
	principal   *delta.JsonObject
	incarnation string
}

// Mirrors "routines and the end › leave nothing of an ended object: its routines' watches and contexts are collected".
func TestRoutinesLeaveNothingOfAnEndedOwnerItsWatchesAndContextsAreCollected(t *testing.T) {
	rig := newRoutineRig(t)
	native := newNativeFake(rig, 0.0)
	source := newJSONFake(rig, nObject(0))
	var mu sync.Mutex
	var contexts []weak.Pointer[callContext]
	remember := func(ctx Ctx) {
		mu.Lock()
		defer mu.Unlock()
		contexts = append(contexts, weak.Make(ctx.(*callContext)))
	}
	rig.routines.OnNative(native, func(_ chord.NativeWatchCall[any], ctx Ctx) error { remember(ctx); return nil })
	rig.routines.On(source, func(_ chord.WatchCall[delta.JsonValue], ctx Ctx) error { remember(ctx); return nil })
	rig.start()
	rig.deliver()
	rig.end(errors.New("owner ended"))
	scopeEventually(t, "the watches released", func() bool { return native.count() == 0 && source.count() == 0 })
	rig.quiesce()
	mu.Lock()
	refs := contexts
	mu.Unlock()
	if len(refs) != 2 {
		t.Fatalf("calls = %d, want 2", len(refs))
	}
	rig.mu.Lock()
	rig.cleanups = nil
	rig.mu.Unlock()
	nexttest.ExpectCollected(t, refs...)
}

// failingSource is a Chord JSON source (a state.Source) that is failed until it is given a value: Pi's failed derive or
// reference member.
type failingSource struct {
	state.Source
	mu      sync.Mutex
	err     error
	value   any
	hasLast bool
}

func newFailingSource(rig *routineRig, err error) *failingSource {
	source := &failingSource{err: err}
	source.Init(state.NewBatch(func(state.BatchError) {}, nil).UseScheduler(rig.sched), source)
	return source
}

// failingWatchable is the source as an `on` takes it: a watchable of JSON (the typed handle layer's adapter of a Source,
// whose Value is a getter that throws).
type failingWatchable struct{ source *failingSource }

func (w failingWatchable) Value() any {
	value, _ := w.source.Value()
	return value
}

func (w failingWatchable) Watch(ctx context.Context) (chord.WatchHandle[delta.JsonValue], error) {
	return w.source.Watch(ctx)
}

func (s *failingSource) Value() (delta.JsonValue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return s.value, nil
}

func (s *failingSource) Deliver(context.Context) error { return nil }

func (s *failingSource) Start() state.WatchStart {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		return state.WatchStart{Kind: state.StartValue, Value: s.value}
	}
	return state.WatchStart{Kind: state.StartFailed, Error: s.err, HasLast: s.hasLast, Last: s.value}
}

// give makes the source healthy at value, as a first good value: the subscribers get it, inside the section of the change.
func (s *failingSource) give(rig *routineRig, value any) {
	s.mu.Lock()
	s.err, s.value, s.hasLast = nil, value, true
	s.mu.Unlock()
	rig.loop.Run(func() {
		s.Forward(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallValue, Value: value, Ops: replaceOps(value)}, chordctx.Background())
	})
}

// failAgain fails the source after a good value, as a failure following a first value in one receive turn.
func (s *failingSource) failAgain(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

// Mirrors "on › starts on a source failed now: no call and no report until its first good value (a reference member)".
func TestOnStartsOnASourceFailedNowNoCallAndNoReportUntilItsFirstGoodValue(t *testing.T) {
	rig := newRoutineRig(t)
	source := newFailingSource(rig, errors.New("t threw"))
	rig.resolve = func(value any) (state.DeriveSource, error) {
		if watchable, ok := value.(failingWatchable); ok {
			return watchable.source, nil
		}
		return nil, nil
	}
	log := &routineLog{}
	rig.routines.On(failingWatchable{source}, log.handler(rig, nil))
	rig.start()
	rig.quiesce()
	rig.loop.barrier()
	if log.len() != 0 || len(rig.host.reported()) != 0 {
		t.Fatalf("calls %d, reports %v, want none until the first good value", log.len(), rig.host.reported())
	}
	source.give(rig, float64(2))
	scopeEventually(t, "the first call", func() bool { return log.len() == 1 })
	rig.quiesce()
	wantCalls(t, log.all(), wantCall(float64(2), replaceOps(float64(2)), -1))
	wantStrings(t, rig.reports())
}

// Mirrors "on › on a remote member with no value yet, a first value followed at once by a failure calls nothing": the
// first value resumes `start`, which asks again and finds the source failed (with that value as its last): the watch
// opens on it and its first call is the failure, so nothing is called until the next value.
func TestOnWithNoValueYetAFirstValueFollowedAtOnceByAFailureCallsNothing(t *testing.T) {
	rig := newRoutineRig(t)
	source := newFailingSource(rig, errors.New("count threw"))
	rig.resolve = func(any) (state.DeriveSource, error) { return source, nil }
	log := &routineLog{}
	rig.routines.On(failingWatchable{source}, log.handler(rig, nil))
	rig.start()
	rig.quiesce()
	// Two state messages in one receive turn: the first value, then a failure again.
	source.mu.Lock()
	source.err, source.value, source.hasLast = nil, float64(5), true
	source.mu.Unlock()
	rig.loop.Run(func() {
		source.Forward(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallValue, Value: float64(5), Ops: replaceOps(float64(5))}, chordctx.Background())
		source.failAgain(errors.New("count threw"))
	})
	rig.loop.barrier()
	rig.deliver()
	if log.len() != 0 {
		t.Fatalf("calls = %+v, want none", log.all())
	}
	source.give(rig, float64(6))
	rig.deliver()
	scopeEventually(t, "the call with 6", func() bool { return log.len() == 1 })
	if got := log.all()[0].value; got != float64(6) {
		t.Fatalf("value = %v, want 6", got)
	}
}
