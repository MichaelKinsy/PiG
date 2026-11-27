package host

import (
	"context"
	"fmt"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/host/follow-over.ts
//
// A follow over a changing source (ERNDL-1191; decision "A follow re-attaches over a changing source"): the attach of
// `self.follow(over, attach)`. To the followed source (host_follow.go) it is one attachment for its attempt; behind it,
// it attaches to each value of `over` (a registry value, a writable), each attachment with its own observer, so that
// only the current one's calls and end count; it is a dependent of `over` and reconciles with its value after each
// batch that changed it. A new value is one replacement, with no failure in between; a failure of the value still
// given while live is a failure call (a failed member), retried after a backoff.

// FollowOverAttach is what `self.FollowOver(over, attach)` takes: attaches observer for value, a value of over (never
// nil: then nothing is followed), returning the attachment, nil for an absent address.
type FollowOverAttach[S, T any] func(value S, observer FollowObserver[any], ctx context.Context) (*FollowAttachment[T], error)

// followOverSource is the narrow interface satisfied by *registries.RegistryValueSource and *state.WritableSource[any]
// (and any *state.FollowedSource[any]).
type followOverSource interface {
	Start() state.NativeStart[any]
	DependentSet() *state.DependentSet
	Batch() *state.Batch
}

// followOverOwner is who a follow over a changing source belongs to: its attempt.
type followOverOwner struct {
	label string
	// ctx is what its attachments run under once its attempt is live: the object's context under the attempt's signal.
	ctx context.Context
	// loop is the host's loop for sections, microtasks, tasks, and backoff timers.
	loop sectionLoop
	// metrics is the host's telemetry metrics for `chord.follow.attaches` (may be nil).
	metrics telemetry.TelemetryMetrics
	// live reports whether its attempt is live: before, a failure of the value given is its activation's (R4).
	live func() bool
	// report reports a failure while live (`follow_failed`), once per failure episode, or a runtime's detach that threw.
	report func(err error)
	// waiting records what the activation waits for (a value of over), named in its deadline's error; "" for nothing.
	waiting func(what string)
}

// followedOver checks the source of `self.FollowOver(over, attach)` (declaration policy).
func followedOver(source any, label string) (followOverSource, error) {
	if s, ok := source.(followOverSource); ok && s != nil {
		return s, nil
	}
	return nil, fmt.Errorf("%s: self.follow(over, attach) follows registry values and writables", label)
}

// followOver builds the FollowAttach of a source followed over over: attach for each of its values, for owner.
func followOver(over followOverSource, attach FollowOverAttach[any, delta.JsonValue], owner followOverOwner) FollowAttach[delta.JsonValue] {
	return func(observer FollowObserver[any], ctx context.Context) (*FollowAttachment[delta.JsonValue], error) {
		fo := &followOverState{
			over:       over,
			attach:     attach,
			owner:      owner,
			observer:   observer,
			activation: ctx,
		}
		return fo.first()
	}
}

type followOverHeldKind uint8

const (
	heldNone followOverHeldKind = iota
	heldValue
	heldFailed
)

type followOverHeld struct {
	kind    followOverHeldKind
	value   delta.JsonValue
	err     error
	hasLast bool
	last    delta.JsonValue
}

type followOverAttachment struct {
	forVal      any
	held        followOverHeld
	observation *FollowAttachment[delta.JsonValue]
}

type followOverState struct {
	over       followOverSource
	attach     FollowOverAttach[any, delta.JsonValue]
	owner      followOverOwner
	observer   FollowObserver[any]
	activation context.Context

	firstDone chan struct{}
	firstAtt  *FollowAttachment[delta.JsonValue]
	firstErr  error
	firstOpen bool

	current     *followOverAttachment
	moving      bool
	reconciling bool
	hasIdle     bool
	idle        any
	checking    bool
	leaving     []*FollowAttachment[delta.JsonValue]
	failing     bool
	detached    bool
	hasDelay    bool
	delay       time.Duration
	timer       Timer
}

var _ state.Dependent = (*followOverState)(nil)

func (f *followOverState) first() (*FollowAttachment[delta.JsonValue], error) {
	f.firstDone = make(chan struct{})
	f.firstOpen = true
	f.owner.loop.Run(func() {
		f.over.DependentSet().Add(f)
		f.follow("value")
	})
	select {
	case <-f.firstDone:
		if f.firstErr != nil {
			f.owner.loop.Run(f.detach)
			return nil, f.firstErr
		}
		return f.firstAtt, nil
	case <-f.activation.Done():
		f.owner.loop.Run(f.detach)
		return nil, context.Cause(f.activation)
	}
}

func (f *followOverState) resolveFirst(att *FollowAttachment[delta.JsonValue]) {
	if !f.firstOpen {
		return
	}
	f.firstOpen = false
	f.firstAtt = att
	close(f.firstDone)
}

func (f *followOverState) rejectFirst(err error) {
	if !f.firstOpen {
		return
	}
	f.firstOpen = false
	f.firstErr = err
	close(f.firstDone)
}

// Invalidate implements state.Dependent: over changed in the current batch (marking only).
func (f *followOverState) Invalidate() {
	f.moving = true
	if f.hasIdle && !f.checking {
		f.checking = true
		f.over.Batch().Flushing(func(context.Context) error { f.checkIdle(); return nil })
	}
	if f.reconciling {
		return
	}
	f.reconciling = true
	f.owner.loop.Queue(f.reconcile)
}

func (f *followOverState) SourceRetired(map[state.Dependent]struct{}) {}

func (f *followOverState) checkIdle() {
	f.checking = false
	if f.hasIdle {
		val, ok := f.given()
		if !ok || !state.Identical(val, f.idle) {
			f.hasIdle = false
			f.idle = nil
		}
	}
}

func (f *followOverState) sealed() bool {
	return f.detached || f.activation.Err() != nil
}

func (f *followOverState) activating() bool {
	return f.firstOpen || !f.owner.live()
}

// given is what over gives now: its value, its last good one while it is failed, ok=false for nil/none.
func (f *followOverState) given() (any, bool) {
	start := f.over.Start()
	var v any
	if start.Kind == state.StartValue {
		v = start.Value
	} else if start.HasLast {
		v = start.Last
	} else {
		return nil, false
	}
	if v == nil {
		return nil, false
	}
	return v, true
}

func (f *followOverState) follow(reason string) {
	if f.sealed() || f.current != nil {
		return
	}
	f.left()
	if f.sealed() {
		return
	}
	value, ok := f.given()
	f.moving = false
	if f.firstOpen {
		if !ok {
			f.owner.waiting("a value of " + f.named())
		} else {
			f.owner.waiting("")
		}
	}
	if !ok {
		return
	}
	att := &followOverAttachment{forVal: value}
	f.current = att
	f.run(att, reason)
}

func (f *followOverState) reconcile() {
	f.reconciling = false
	if f.sealed() {
		return
	}
	value, ok := f.given()
	f.moving = false
	if f.current != nil && ok && state.Identical(f.current.forVal, value) {
		return
	}
	if f.current == nil && f.hasIdle && ok && state.Identical(f.idle, value) {
		return
	}
	f.letGo()
	if f.timer != nil {
		f.timer.Stop()
		f.timer = nil
	}
	f.hasIdle = false
	f.idle = nil
	f.follow("value")
}

func (f *followOverState) run(att *followOverAttachment, reason string) {
	ctx := f.owner.ctx
	if f.firstOpen {
		ctx = f.activation
	}
	if f.owner.metrics != nil {
		f.owner.metrics.Counter("chord.follow.attaches", telemetry.InstrumentOptions{}).Add(1, telemetry.SpanAttributes{
			telemetry.Attr("reason", reason),
		})
	}
	obs := followOverObserver{state: f, att: att}
	startTask(f.owner.loop, func() {
		attached, err := f.attach(att.forVal, obs, ctx)
		f.owner.loop.Run(func() {
			if err != nil {
				if f.counts(att) {
					f.fail(err)
				}
				return
			}
			if !f.counts(att) || f.sealed() {
				if f.current == att {
					f.current = nil
				}
				if attached != nil {
					f.cleanUp(attached)
				}
				return
			}
			if attached == nil {
				f.fail(data.NewChordError(data.ChordErrorInit{
					Code:    data.CodeUnavailable,
					Message: f.owner.label + ": the followed address is absent",
				}, nil))
				return
			}
			held := att.held
			att.held = followOverHeld{}
			att.observation = attached
			value := attached.Value
			if held.kind == heldValue {
				value = held.value
			} else if held.kind == heldFailed && held.hasLast {
				value = held.last
			}
			if f.firstOpen && held.kind == heldFailed {
				f.current = nil
				f.detach()
				f.cleanUp(attached)
				f.rejectFirst(held.err)
				return
			}
			f.hasIdle = false
			f.idle = nil
			if f.firstOpen {
				f.resolveFirst(&FollowAttachment[delta.JsonValue]{
					Value:  value,
					Detach: func() { f.detach() },
				})
			} else {
				f.failing = false
				f.hasDelay = false
				f.delay = 0
				f.observer.Advance(chord.WatchCall[any]{
					Kind:  chord.WatchCallValue,
					Value: value,
					Ops:   []delta.Op{{"r", value}},
				}, ctx)
			}
			if held.kind == heldFailed {
				f.observer.Advance(chord.WatchCall[any]{Kind: chord.WatchCallFailed, Error: held.err}, ctx)
			}
		})
	})
}

type followOverObserver struct {
	state *followOverState
	att   *followOverAttachment
}

func (o followOverObserver) Advance(call chord.WatchCall[any], ctx context.Context) {
	o.state.owner.loop.Run(func() {
		f := o.state
		if !f.counts(o.att) {
			return
		}
		if o.att.observation == nil {
			o.att.held = foldHeld(o.att.held, call)
		} else if call.Kind == chord.WatchCallFailed && f.activating() {
			f.fail(call.Error)
		} else {
			f.observer.Advance(call, ctx)
		}
	})
}

func (o followOverObserver) End(end chord.WatchEnd) {
	o.state.owner.loop.Run(func() {
		f := o.state
		if f.counts(o.att) {
			f.ended(end)
		}
	})
}

func (o followOverObserver) Section(work func()) {
	o.state.owner.loop.Run(work)
}

func (f *followOverState) counts(att *followOverAttachment) bool {
	if att != f.current {
		return false
	}
	if !f.moving {
		return true
	}
	if val, ok := f.given(); ok && state.Identical(val, att.forVal) {
		f.moving = false
		return true
	}
	f.letGo()
	return false
}

func (f *followOverState) ended(end chord.WatchEnd) {
	if end.Kind == chord.WatchFailed {
		f.fail(end.Error)
		return
	}
	var endedVal any
	hasEnded := false
	if f.current != nil {
		endedVal = f.current.forVal
		hasEnded = true
	}
	f.letGo()
	if f.activating() {
		f.observer.End(end)
	} else if hasEnded {
		f.idle = endedVal
		f.hasIdle = true
	}
}

func (f *followOverState) fail(err error) {
	var failedVal any
	hasFailed := false
	if f.current != nil {
		failedVal = f.current.forVal
		hasFailed = true
	}
	f.letGo()
	if f.sealed() {
		return
	}
	if f.activating() {
		if f.firstOpen {
			f.rejectFirst(err)
		} else {
			f.observer.End(chord.WatchEnd{Kind: chord.WatchFailed, Error: err})
		}
		return
	}
	f.idle = failedVal
	f.hasIdle = hasFailed
	f.delay = nextDelay(f.delay)
	f.hasDelay = true
	if f.timer != nil {
		f.timer.Stop()
	}
	f.timer = f.owner.loop.AfterFunc(f.delay, func() {
		f.timer = nil
		f.hasIdle = false
		f.idle = nil
		f.follow("retry")
	})
	if f.failing {
		return
	}
	f.failing = true
	f.owner.report(state.HandedOut(err))
	f.observer.Advance(chord.WatchCall[any]{Kind: chord.WatchCallFailed, Error: err}, f.owner.ctx)
}

func (f *followOverState) letGo() {
	current := f.current
	if current == nil {
		return
	}
	f.current = nil
	current.held = followOverHeld{}
	if current.observation == nil {
		return
	}
	f.leaving = append(f.leaving, current.observation)
	if len(f.leaving) == 1 {
		f.owner.loop.Queue(f.left)
	}
}

func (f *followOverState) left() {
	if len(f.leaving) == 0 {
		return
	}
	leaving := f.leaving
	f.leaving = nil
	for _, obs := range leaving {
		f.cleanUp(obs)
	}
}

func (f *followOverState) cleanUp(obs *FollowAttachment[delta.JsonValue]) {
	if obs.Detach == nil {
		return
	}
	if _, err := tryPlugin(func() (any, error) { obs.Detach(); return nil, nil }); err != nil {
		f.owner.report(state.HandedOut(err))
	}
}

func (f *followOverState) named() string {
	if reg, ok := f.over.(*registries.RegistryValueSource); ok {
		return "registry " + reg.Name
	}
	return "its writable"
}

func (f *followOverState) detach() {
	f.detached = true
	if f.timer != nil {
		f.timer.Stop()
		f.timer = nil
	}
	f.hasIdle = false
	f.idle = nil
	f.over.DependentSet().Delete(f)
	current := f.current
	f.current = nil
	f.left()
	if current != nil && current.observation != nil {
		f.cleanUp(current.observation)
	}
}

func foldHeld(held followOverHeld, call chord.WatchCall[any]) followOverHeld {
	if call.Kind == chord.WatchCallValue {
		return followOverHeld{kind: heldValue, value: call.Value}
	}
	var hasLast bool
	var last delta.JsonValue
	if held.kind == heldValue {
		hasLast, last = true, held.value
	} else if held.kind == heldFailed && held.hasLast {
		hasLast, last = true, held.last
	}
	return followOverHeld{kind: heldFailed, err: call.Error, hasLast: hasLast, last: last}
}
