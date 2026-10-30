package host

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/host/routines.ts. Routines (§14): the standing behaviour an object or a facet declares,
// `self.on(source, fn)` and `self.onLive(fn)`. They start once their owner went live and stop when it ends (their ctx
// aborted, the drain waits for running calls); each call gets its own ctx; a call that throws is reported and called
// again after a backoff.
//
// Forced Go differences.
//   - A handler is `void | Promise<void>`: it returns nil (its synchronous part was all of it) or a *Handled (an async
//     handler's Promise: Threw for one that threw, Pending/Awaiting for one still running). Its synchronous part runs
//     inside the section that makes the call, as Pi's does, and the call settles a microtask after, or after the Handled
//     settled: the routines are continuations on the host's Loop (Queue is the microtask, a Handled's reaction is its
//     promise's `then`), not tasks, so the order of two handlers' first statements, a handler's against a method call
//     admitted in the same section, and a call's completion against a watch's frames is Pi's microtask order. A handler
//     that has to block (a transaction, a store) does it in Awaiting, on an owned task that settles its Handled inside a
//     section; where Pi's async function awaits across an `await`, the Go handler's code after the blocking step is that
//     task's. Pi's extra microtask ticks of an async function that returns a Promise (thenable resolution) are not
//     reproduced: the call settles one microtask after its Handled does.
//   - `void watch.closed.then(...)` is the one owned task that waits on a channel (the watch has no callback form of
//     Closed); everything else Pi awaits is a continuation inside a section: `start`'s awaits are resolved by callbacks of
//     the source's batch (`firstValue`) or never suspend (`watch`), `live`'s sleep is a loop timer, and a call's `.then` is
//     a loop.Queue microtask. A watch's frames are the watch's delivery tasks, which enter the loop with Run.
//   - `on` has no overloads in Go: On follows a JSON source (anything with Chord's shared `Watch`: a document handle, a
//     derive, a reference member; a Chord `Watchable` is one), OnNative a native one (Chord's shared `NativeWatch`); Pi decides by the class of the source's watch,
//     which is the method's choice here. `declared instanceof NativeSource` is the declared source implementing
//     state.NativeSourceHandle, and `declared.start().kind === "failed"` its CurrentAny failing. The ctx type parameter of
//     the handlers is type-level (owner class a). `WatchValueCall<T>` is chord.WatchCall[T] with Kind value, `Position` is
//     host_error.go's. The declarations a factory makes throw outside the factory in Pi: the methods of RoutineDeclarations
//     return that error; Routines' own `on`/`onLive` never throw.

// routineOwner is where routines run: their owner's label (for reports), its host, whom their calls act for, and the
// context of their calls, aborted when the owner ends. admit adds a running call to the owner's drain.
type routineOwner struct {
	label   string
	host    *chordHost
	caller  caller
	context context.Context
	// admit adds a running call to the drain; cleanup (its bookkeeping) runs once, a microtask after settle was called or
	// when the attempt ends with the call still running. settle is called when the call settled. It runs inside a section.
	admit func(cleanup func()) (settle func(err error))
}

// Handled is the Promise<void> an async routine handler returns; a handler that finished in its synchronous part
// returns nil (Pi's `void`).
type Handled struct {
	// err is what a handler that threw rejected with (later is nil).
	err error
	// later is the promise of one still running; nil for one that threw.
	later *keyedPromise[struct{}]
}

// Threw is the Promise an async handler that threw in its synchronous part returns: rejected with err.
func Threw(err error) *Handled { return &Handled{err: err} }

// Pending is the Promise of a handler that is still running; Resolve or Reject settles it, in a section.
func Pending(loop sectionLoop) *Handled { return &Handled{later: newKeyedPromise[struct{}](loop)} }

// Awaiting is a handler's blocking work (the awaits of Pi's async function): work runs on an owned task of the loop, off
// the loop, and its error (nil: resolved) settles the returned Handled in a section. It runs inside a section.
func Awaiting(loop sectionLoop, work func() error) *Handled {
	handled := Pending(loop)
	loop.Go(func() {
		err := work()
		loop.Run(func() {
			if err != nil {
				handled.Reject(err)
				return
			}
			handled.Resolve()
		})
	})
	return handled
}

// Resolve settles a Pending handler as resolved. It runs inside a section.
func (h *Handled) Resolve() { h.later.resolve(struct{}{}) }

// Reject settles a Pending handler as rejected with err. It runs inside a section.
func (h *Handled) Reject(err error) { h.later.reject(err) }

// OnHandler is a routine's handler of a JSON source: the value call (the value, the ops since the previous call, whether
// the source's document was retired since then; Kind is always value) and the call's ctx. Failures are not calls of a
// handler (design §11): the routine waits for the next value. It returns nil when it finished in its synchronous part, or
// the Handled of its async remainder (a rejection is the handler's throw).
type OnHandler[T any] func(call chord.WatchCall[T], ctx Ctx) *Handled

// OnNativeHandler is a routine's handler of a native source: the value call (the value; Kind is always value) and the
// call's ctx.
type OnNativeHandler[T any] func(call chord.NativeWatchCall[T], ctx Ctx) *Handled

// OnLiveHandler is an `onLive` routine: called once its owner is live.
type OnLiveHandler func(ctx Ctx) *Handled

// JSONSource is a JSON source an `on` follows: whatever has Chord's shared `Watch` (a document handle, a derive, a
// followed source, a reference member, any `chord.Watchable[delta.JsonValue]`). Pi's `Watchable<T>` is a type-level check;
// the source's identity decides what Chord follows (`resolveWatchable`).
type JSONSource interface {
	Watch(ctx context.Context) (chord.WatchHandle[delta.JsonValue], error)
}

// NativeSource is a native source an `OnNative` follows: whatever has Chord's shared `NativeWatch` (a writable, a registry
// value, `ref.state`, any `chord.NativeWatchable[any]`).
type NativeSource interface {
	Watch(ctx context.Context) (chord.NativeWatchHandle[any], error)
}

// RoutineDeclarations is what the declarations of an owner (`on`, `onLive`) take; part of the factory's `self`. Pi's
// declarations throw outside the factory: the attempt's panic (the factory runs under tryPlugin, which returns it as
// the activation's error).
type RoutineDeclarations interface {
	// On declares a routine over source: fn gets its current value first (ops [["r", value]]), then one call per change,
	// never overlapping: changes during a call fold into one next call (the newest value, the ops since the previous call,
	// `Retired` when any of them was a retirement). After a call that threw, it is called again with the whole newest
	// value (`Retired` again if the call that threw had it) after a backoff (1 s doubling to 60 s), or at once on a change
	// during the backoff. Ends with its source. A JSON watchable is one whose watch is Chord's shared `Watch` (every Chord
	// and Durable source).
	On(source JSONSource, fn OnHandler[delta.JsonValue])
	// OnNative declares a routine over a native source (a registry value, `ref.state`): one whose watch is Chord's shared
	// `NativeWatch`. As On without ops: after a busy call, one call with the newest value.
	OnNative(source NativeSource, fn OnNativeHandler[any])
	// OnLive declares a routine called once its owner is live. Returning ends it; an error is reported and restarts it
	// after a backoff (1 s doubling to 60 s, reset after a run of a minute); the owner's end aborts its ctx.
	OnLive(fn OnLiveHandler)
}

// declaration is a declared routine: its kind and position in reports (`{ kind: "on", position: 2 }`), its source and its
// handler.
type declaration struct {
	kind PositionKind
	at   Position
	// json and onJSON are an `on` over a JSON source; native and onNative one over a native source.
	json     JSONSource
	onJSON   OnHandler[delta.JsonValue]
	native   NativeSource
	onNative OnNativeHandler[any]
	// live is an `onLive`'s handler.
	live OnLiveHandler
}

// called is how one routine call ended.
type called struct {
	failed bool
	err    error
}

// longRun: one run of an `onLive` that lasted this long resets its backoff.
const longRun = 60 * time.Second

// routines are an owner's routines: declared while its factory (or setup) runs, started once when it went live.
type routines struct {
	declared []declaration
	ons      int
	lives    int
}

// on declares `on(source, fn)` over a JSON source.
func (r *routines) on(source JSONSource, fn OnHandler[delta.JsonValue]) {
	r.ons++
	r.declared = append(r.declared, declaration{kind: PositionOn, at: Position{Kind: PositionOn, Position: r.ons}, json: source, onJSON: fn})
}

// onNative declares `on(source, fn)` over a native source.
func (r *routines) onNative(source NativeSource, fn OnNativeHandler[any]) {
	r.ons++
	r.declared = append(r.declared, declaration{kind: PositionOn, at: Position{Kind: PositionOn, Position: r.ons}, native: source, onNative: fn})
}

// onLive declares `onLive(fn)`.
func (r *routines) onLive(fn OnLiveHandler) {
	r.lives++
	r.declared = append(r.declared, declaration{kind: PositionOnLive, at: Position{Kind: PositionOnLive, Position: r.lives}, live: fn})
}

// start starts every routine for owner, which went live; they stop when its context is aborted. A routine whose
// synchronous start ends the owner (an `onLive` releasing its last reference) starts none of the later ones. It runs
// inside a section.
func (r *routines) start(owner routineOwner) {
	for _, declared := range r.declared {
		if owner.context.Err() != nil {
			return
		}
		if declared.kind == PositionOn {
			newOnRoutine(owner, declared).start()
		} else {
			(&liveRoutine{owner: owner, declaration: declared}).run()
		}
	}
}

// liveRoutine is `live`'s state: an `onLive`'s handler runs until it returns or its owner ends; an error is reported and
// the handler restarts after the backoff.
type liveRoutine struct {
	owner       routineOwner
	declaration declaration
	delay       time.Duration
}

// run is one iteration of `live`'s loop: while the owner runs, call the handler, then wait out the backoff after an error.
func (l *liveRoutine) run() {
	owner := l.owner
	if owner.context.Err() != nil {
		return
	}
	loop := owner.host.loop()
	started := loop.Now()
	call(owner, l.declaration.live, func(done called) {
		if !done.failed || owner.context.Err() != nil {
			return
		}
		routineFailed(owner, l.declaration.at, done.err)
		if loop.Now()-started >= longRun.Milliseconds() {
			l.delay = 0
		}
		l.delay = nextDelay(l.delay)
		l.sleep()
	})
}

// sleep waits out the backoff (`sleep(delay, owner.context)`): the owner's end ends the routine, leaving no timer or
// listener behind.
func (l *liveRoutine) sleep() {
	owner := l.owner
	if owner.context.Err() != nil {
		return
	}
	loop := owner.host.loop()
	var timer Timer
	var stopAbort func() bool
	timer = loop.AfterFunc(l.delay, func() {
		owner.host.enter(func() {
			stopAbort()
			// The promise resolves in the timer's section; `live` resumes a microtask later.
			loop.Queue(l.run)
		})
	})
	stopAbort = context.AfterFunc(owner.context, func() {
		owner.host.enter(func() { timer.Stop() })
	})
}

// call runs one routine call under its own ctx (its `ctx.get` references released when it settles), admitted to the owner's
// drain before the handler runs: a handler may end its own owner before its first await. The handler's synchronous part
// runs here; after gets how it ended, a microtask after it settled; it never fails. It runs inside a section.
func call(owner routineOwner, invoke func(ctx Ctx) *Handled, after func(done called)) {
	loop := owner.host.loop()
	ctx := newCallContext(owner.context, owner.caller, owner.host)
	settle := owner.admit(ctx.settle)
	finish := func(done called) {
		ctx.settle()
		settle(done.err)
		loop.Queue(func() { after(done) })
	}
	// Plugin code: a panic in the synchronous part is the rejection.
	result, err := tryPlugin(func() (any, error) { return invoke(ctx), nil })
	handled, _ := result.(*Handled)
	if err != nil {
		handled = Threw(err)
	}
	switch {
	case handled == nil:
		loop.Queue(func() { finish(called{}) })
	case handled.later == nil:
		loop.Queue(func() { finish(called{failed: true, err: handled.err}) })
	default:
		handled.later.then(func(_ struct{}, err error) { finish(called{failed: err != nil, err: err}) })
	}
}

// routineFailed reports a failed routine call or source (Pi's `failed`; renamed: `failed` is the reference state alias).
func routineFailed(owner routineOwner, routine Position, err error) {
	owner.host.report(HostError{Kind: ErrRoutineFailed, Object: owner.label, Routine: routine, Error: err})
}

// onStateKind is where an `on` routine stands. `starting`: taking its watch; `idle`: waiting for a change; `calling`: a
// call runs (changes meanwhile wait, folded, for the next); `settling`: a call succeeded while changes arrived, and the
// next one waits for one macrotask, so every value its watch still buffers (a native watch replays them one per
// microtask) is taken first; `backoff`: the last call threw and its timer calls again, unless a change during the backoff
// calls first; `ended`: its watch ended (the owner ended, or the source ended or failed).
type onStateKind int

const (
	stateStarting onStateKind = iota
	stateIdle
	stateCalling
	stateEnded
	stateSettling
	stateBackoff
)

// onState is an onStateKind with the timer of settling and backoff.
type onState struct {
	kind  onStateKind
	timer Timer
}

// basis is what the next call's ops are relative to: `none` before the first call and after a throw (the handler may not
// have applied what it got: the next call gets the whole value), `last` after a call that succeeded.
type basis int

const (
	basisNone basis = iota
	basisLast
)

// pendingFrame is the newest value no call got yet.
type pendingFrame struct{ value any }

// failingKind is what `failing` says of a source.
type failingKind int

const (
	failingNone failingKind = iota
	// failingFailed: failed now.
	failingFailed
	// failingNoValue: a JSON source with no value yet, with the source to follow.
	failingNoValue
)

// openedWatch is a source's watch as an `on` routine uses it, erased over the watch's class: a JSON one (Watch) or a
// native one (NativeWatch).
type openedWatch struct {
	value any
	start func(listener func(call watchedCall))
	// stop ends the watch (the owner ended); it runs inside a section.
	stop   func()
	closed <-chan struct{}
	end    func() chord.WatchEnd
}

// watchedCall is a call of a watch's listener, JSON or native: a value (Ops and Retired are a JSON watch's) or a failure.
type watchedCall struct {
	failed  bool
	value   any
	ops     []delta.Op
	retired bool
}

// onRoutine is one `on` routine. Its watch's listener never throws (a throwing listener would end the watch) and never
// waits: it keeps the newest frame, folding JSON ops, and the routine makes one call at a time from it, the first call and
// the retries after a throw through the same pending frame. All of it runs inside sections.
type onRoutine struct {
	owner       routineOwner
	declaration declaration
	state       onState
	// native: a native source's watch yields values without ops; decided by the declaration's source class.
	native bool
	// pending is the newest value no call got yet.
	pending    *pendingFrame
	pendingOps delta.PendingOps
	// retired: one of the frames since the last call was a retirement (JSON sources).
	retired bool
	basis   basis
	// last is the value of the last call: a retry after a throw calls with it.
	last any
	// lastRetired: the last call reported a retirement: a retry after a throw reports it again.
	lastRetired bool
	delay       time.Duration
	// failed: the source failed after its last value: no call until its next value (a retry waits too).
	failed bool
}

func newOnRoutine(owner routineOwner, declared declaration) *onRoutine {
	return &onRoutine{
		owner: owner, declaration: declared,
		state: onState{kind: stateStarting}, native: declared.native != nil,
	}
}

func (o *onRoutine) loop() sectionLoop { return o.owner.host.loop() }

// start takes the watch and calls with its value. On a failed Chord source it waits for its first good value instead (a
// source that never had one is followed until it has). A source that cannot be watched otherwise is reported; an owner or
// source that ended meanwhile ends the routine. It runs inside a section; what Pi awaits resumes a microtask later.
func (o *onRoutine) start() {
	failing, source := o.failing()
	if failing == failingNoValue {
		o.firstValue(source, func(got bool) {
			if !got {
				o.state = onState{kind: stateEnded}
				return
			}
			// Asked again: a failure may have followed the first value before this resumed (one receive turn).
			o.start()
		})
		return
	}
	opened, err := o.open()
	o.loop().Queue(func() { o.watching(failing, opened, err) })
}

// open is `source.watch(owner.context)`, erased. The watch is not given the owner's abort to observe: its listener would
// end it on a goroutine of its own, outside the section that aborts the owner; the routine stops it from a section
// (`watching`).
func (o *onRoutine) open() (openedWatch, error) {
	ctx := context.WithoutCancel(o.owner.context)
	if o.native {
		watch, err := o.declaration.native.Watch(ctx)
		if err != nil {
			return openedWatch{}, err
		}
		return openedWatch{
			value: watch.Value(),
			start: func(listener func(call watchedCall)) {
				watch.Start(func(_ context.Context, call chord.NativeWatchCall[any]) error {
					listener(watchedCall{failed: call.Kind != chord.WatchCallValue, value: call.Value})
					return nil
				})
			},
			stop:   func() { _, _ = watch.Stop() },
			closed: watch.Closed(),
			end:    watch.End,
		}, nil
	}
	watch, err := o.declaration.json.Watch(ctx)
	if err != nil {
		return openedWatch{}, err
	}
	return openedWatch{
		value: watch.Value(),
		start: func(listener func(call watchedCall)) {
			watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
				listener(watchedCall{failed: call.Kind != chord.WatchCallValue, value: call.Value, ops: call.Ops, retired: call.Retired})
				return nil
			})
		},
		stop:   func() { _, _ = watch.Stop() },
		closed: watch.Closed(),
		end:    watch.End,
	}, nil
}

// watching is `start` after `await source.watch(...)`.
func (o *onRoutine) watching(failing failingKind, opened openedWatch, err error) {
	loop := o.loop()
	if err != nil {
		o.state = onState{kind: stateEnded}
		if o.owner.context.Err() == nil {
			routineFailed(o.owner, o.declaration.at, err)
		}
		return
	}
	// The owner's end ends the watch, from a section.
	stopAbort := context.AfterFunc(o.owner.context, func() { o.owner.host.enter(opened.stop) })
	// `void watch.closed.then((end) => this.#end(end))`: an owned task waits for the close.
	loop.Go(func() {
		<-opened.closed
		stopAbort()
		o.owner.host.enter(func() { o.end(opened.end()) })
	})
	// Both listeners return at once: they enter the loop, keep the frame and return.
	_, startErr := tryPlugin(func() (any, error) {
		opened.start(func(call watchedCall) {
			o.owner.host.enter(func() {
				if call.failed {
					o.failure()
				} else {
					o.frame(call)
				}
			})
		})
		return nil, nil
	})
	if startErr != nil {
		// The watch ended while it was taken (the owner or the source ended): `closed` says how.
		return
	}
	// A watch opened on a failed source holds its last good value, a stale one: its first call is the failure.
	if failing == failingFailed {
		o.state = onState{kind: stateIdle}
		return
	}
	o.pending = &pendingFrame{value: opened.value}
	o.next()
}

// failing says whether the source is a Chord source failed now (failingFailed), or a JSON one with no value yet
// (failingNoValue, with the source to follow); failingNone otherwise (Durable's views tell their failures through their
// watches).
func (o *onRoutine) failing() (failingKind, state.SourceHandle) {
	if o.native {
		// A native source opens its watch at its last good value while failed (a registry whose `compose` threw): its first
		// call is the failure, not that stale value.
		if handle, ok := o.declaration.native.(state.NativeSourceHandle); ok {
			if _, err := handle.CurrentAny(); err != nil {
				return failingFailed, nil
			}
		}
		return failingNone, nil
	}
	resolved, err := resolveWatchable(unwrapMember(o.declaration.json))
	if err != nil {
		// Not bound or ended: the watch says so.
		return failingNone, nil
	}
	source, ok := resolved.(state.SourceHandle)
	if !ok {
		return failingNone, nil
	}
	start := source.Start()
	if start.Kind == state.StartValue {
		return failingNone, nil
	}
	if !start.HasLast {
		return failingNoValue, source
	}
	return failingFailed, nil
}

// firstValueSubscriber follows a source for its first good value: a subscriber of its own, since a watch has nothing to
// start at.
type firstValueSubscriber struct{ done func(got bool) }

func (s *firstValueSubscriber) Advance(call chord.WatchCall[delta.JsonValue], _ context.Context) {
	if call.Kind == chord.WatchCallValue {
		s.done(true)
	}
}
func (s *firstValueSubscriber) End(chord.WatchEnd) { s.done(false) }
func (s *firstValueSubscriber) EndAfterDelivery()  { s.done(false) }

// firstValue follows source, failed with no value, until its first good value (then(true)), or until it or the owner ends
// (then(false)); then runs a microtask after.
func (o *onRoutine) firstValue(source state.SourceHandle, then func(got bool)) {
	ctx := o.owner.context
	loop := o.loop()
	var stopAbort func() bool
	resolved := false
	subscriber := &firstValueSubscriber{}
	subscriber.done = func(got bool) {
		if resolved {
			return
		}
		resolved = true
		source.Unsubscribe(subscriber)
		if stopAbort != nil {
			stopAbort()
		}
		loop.Queue(func() { then(got) })
	}
	if ctx.Err() != nil {
		subscriber.done(false)
		return
	}
	source.Subscribe(subscriber)
	stopAbort = context.AfterFunc(ctx, func() {
		o.owner.host.enter(func() { subscriber.done(false) })
	})
}

// failure: the source failed: what was pending goes (a value a failure followed is stale), a pending retry or settle is
// dropped, and the next call waits for the next value, as a whole value; a running call runs on.
func (o *onRoutine) failure() {
	current := o.state
	if current.kind == stateEnded {
		return
	}
	o.failed = true
	o.pending = nil
	o.pendingOps.Take()
	o.basis = basisNone
	if current.kind == stateBackoff || current.kind == stateSettling {
		current.timer.Stop()
		o.state = onState{kind: stateIdle}
	}
}

// frame is a frame of the watch: kept as the newest; it is called at once when nothing runs or waits, during a backoff too
// (the backoff ends); while a call runs or settles it waits for the next call.
func (o *onRoutine) frame(call watchedCall) {
	current := o.state
	if current.kind == stateEnded {
		return
	}
	o.failed = false
	o.pending = &pendingFrame{value: call.value}
	if !o.native {
		// A JSON watch's call, as its class decided.
		o.pendingOps.Push(call.ops)
		if call.retired {
			o.retired = true
		}
	}
	if current.kind == stateBackoff {
		current.timer.Stop()
	}
	if current.kind == stateBackoff || current.kind == stateIdle {
		o.next()
	}
}

// next makes the next call from the pending frame, if there is one and the owner still runs; otherwise idles.
func (o *onRoutine) next() {
	pending := o.pending
	if pending == nil || o.aborted() {
		o.state = onState{kind: stateIdle}
		return
	}
	o.pending = nil
	value := pending.value
	o.state = onState{kind: stateCalling}
	o.last = value
	call(o.owner, o.invocation(value), o.called)
}

// invocation is the handler's call with value: a native one with the value alone, a JSON one with the folded ops or the
// whole value, and whether a retirement came since the last call.
func (o *onRoutine) invocation(value any) func(ctx Ctx) *Handled {
	if o.native {
		fn := o.declaration.onNative
		return func(ctx Ctx) *Handled {
			return fn(chord.NativeWatchCall[any]{Kind: chord.WatchCallValue, Value: value}, ctx)
		}
	}
	fn := o.declaration.onJSON
	taken := o.pendingOps.Take()
	// A JSON source's values are JSON.
	var ops []delta.Op
	if o.basis == basisNone || taken.Replace {
		ops = []delta.Op{{"r", value}}
	} else if ops = taken.Ops; ops == nil {
		ops = []delta.Op{}
	}
	retired := o.retired
	o.retired = false
	o.lastRetired = retired
	return func(ctx Ctx) *Handled {
		return fn(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallValue, Value: value, Ops: ops, Retired: retired}, ctx)
	}
}

// called: a call settled. After a success the next frame's ops build on it, and changes that arrived during the call are
// called after one macrotask (`settling`). After a throw (reported) the backoff always starts, also when changes arrived
// during the call (a handler that changes its own source and throws would feed itself otherwise); its timer calls with the
// newest value, or the last call's, as a whole value.
func (o *onRoutine) called(done called) {
	if o.state.kind == stateEnded {
		return
	}
	loop := o.loop()
	if !done.failed {
		o.basis = basisLast
		o.delay = 0
		switch {
		case o.pending != nil:
			o.state = onState{kind: stateSettling, timer: loop.AfterFunc(0, func() { o.owner.host.enter(o.next) })}
		case o.owner.host.scheduler.Busy():
			// Frames are on their way (Pi's watch replays them a microtask each, ahead of the one macrotask a call that saw
			// changes waits): the next call waits for them, as it would for the macrotask.
			o.state = onState{kind: stateSettling, timer: o.afterDeliveries()}
		default:
			o.state = onState{kind: stateIdle}
		}
		return
	}
	if o.aborted() {
		return
	}
	routineFailed(o.owner, o.declaration.at, done.err)
	o.basis = basisNone
	// The handler may not have applied the retirement either.
	if o.lastRetired {
		o.retired = true
	}
	o.delay = nextDelay(o.delay)
	// The source failed meanwhile: the retry waits for its next value, never the stale last one.
	if o.failed {
		o.state = onState{kind: stateIdle}
		return
	}
	timer := loop.AfterFunc(o.delay, func() {
		o.owner.host.enter(func() {
			if o.pending == nil {
				o.pending = &pendingFrame{value: o.last}
			}
			o.next()
		})
	})
	o.state = onState{kind: stateBackoff, timer: timer}
}

// settleTasksRunning counts the tasks of afterDeliveries still waiting: what a test that moves a fake clock waits for, as
// Pi's `advanceTimersByTimeAsync` awaits the microtasks between timers.
var settleTasksRunning atomic.Int64

// deliveriesWait is the Timer of a routine that settles once the host's deliveries in flight entered: Stop cancels it.
type deliveriesWait struct{ stopped atomic.Bool }

// Stop implements Timer.
func (d *deliveriesWait) Stop() bool { return !d.stopped.Swap(true) }

// afterDeliveries makes the next call (or the idle state) wait for every delivery in flight: an owned task waits for the
// host's scheduler, and the decision is a section after it.
func (o *onRoutine) afterDeliveries() Timer {
	wait := &deliveriesWait{}
	host := o.owner.host
	settleTasksRunning.Add(1)
	host.loop().Go(func() {
		defer settleTasksRunning.Add(-1)
		host.scheduler.Wait()
		host.enter(func() {
			if !wait.stopped.Swap(true) {
				o.next()
			}
		})
	})
	return wait
}

// end: the watch ended: the owner ended (aborted), or the source ended (no report) or failed (reported once).
func (o *onRoutine) end(end chord.WatchEnd) {
	current := o.state
	o.state = onState{kind: stateEnded}
	if current.kind == stateSettling || current.kind == stateBackoff {
		current.timer.Stop()
	}
	o.pending = nil
	o.pendingOps.Take()
	o.retired = false
	if end.Kind == chord.WatchFailed && !o.aborted() {
		routineFailed(o.owner, o.declaration.at, end.Error)
	}
}

func (o *onRoutine) aborted() bool { return o.owner.context.Err() != nil }
