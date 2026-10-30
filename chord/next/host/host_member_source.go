package host

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/host/member-source.ts
//
// Member sources (slice 6a §1.2, §1.4): a local object's state member as its references and other objects' derives
// follow it across attempts. While an attempt is live it passes that attempt's member on; while the object is failed
// or activating again it keeps the last value and gets no frames; when a new attempt goes live it re-binds to that
// attempt's member of the same name (one frame from the last value), as a remote reference's re-hold does.

// memberKind is where a member source stands: following the live attempt's member, frozen at its last value, failed
// (its value threw when frozen, or a new attempt's variant lacks the member: a later attempt may bring it back; last is
// the last good value, when there was one) or ended with its object.
type memberKind uint8

const (
	memberBound memberKind = iota
	memberFrozen
	memberFailed
	memberEnded
)

type memberState struct {
	kind memberKind
	// source is the bound member.
	source state.DeriveSource
	// value is the frozen value.
	value delta.JsonValue
	// err is the failure of a failed member source, or the end of an ended one (a ChordError).
	err error
	// last is a failed member's last good value, when hasLast.
	last    delta.JsonValue
	hasLast bool
}

var memberEndedWatch = chord.WatchEnd{Kind: chord.WatchEnded}

// memberFramed is what a member source's watches had at a re-bind (hasValue false for no value: the member had failed),
// until the re-bind's frame.
type memberFramed struct {
	value    delta.JsonValue
	hasValue bool
}

// memberWatch adapts a Watch to the subscriber shape Pi's Watch has itself (`advance(call, context)`, `end(end)`,
// `endAfterDelivery()`): a subscriber is told apart by identity, so one adapter lives as long as its watch.
type memberWatch struct{ watch *chord.Watch[delta.JsonValue] }

var _ state.Subscriber = (*memberWatch)(nil)

func (w *memberWatch) Advance(call chord.WatchCall[delta.JsonValue], ctx context.Context) {
	w.watch.Advance(ctx, call)
}

func (w *memberWatch) End(end chord.WatchEnd) { w.watch.EndWith(end) }

func (w *memberWatch) EndAfterDelivery() { w.watch.EndAfterDelivery() }

// memberSource is one state member of a local object across its attempts. Bound, it is a dependent of the attempt's
// member (its own dependents are marked with it). Its watches and subscribers (holds over a link, member sources
// re-exposing it) subscribe to the bound member itself, so the member's frames reach them as they are, with no extra
// hop, and a derive member stays lazy while nothing follows it; a freeze or a re-bind moves them.
type memberSource struct {
	state.Source
	state memberState
	// scheduler is the delivery scheduler of the batch its watches run on, nil for the process-wide one.
	scheduler *chord.DeliveryScheduler
	// passed is its watches and subscribers, each subscribed to the bound member while bound; frozen, they get nothing.
	passed []state.Subscriber
	// framed is what its watches had at a re-bind, until the re-bind's frame.
	framed *memberFramed
}

var (
	_ state.SourceHandle = (*memberSource)(nil)
	_ state.Dependent    = (*memberSource)(nil)
)

// newMemberSource makes the member source following source on batch; scheduler is the one batch delivers watches on
// (Pi's watches reach the batch's microtask queue through the batch itself).
func newMemberSource(source state.DeriveSource, batch *state.Batch, scheduler *chord.DeliveryScheduler) *memberSource {
	m := &memberSource{state: memberState{kind: memberBound, source: source}, scheduler: scheduler}
	m.Init(batch, m)
	m.hook(source)
	return m
}

// Value is the bound member's value, the frozen one, or the failure.
func (m *memberSource) Value() (delta.JsonValue, error) {
	switch m.state.kind {
	case memberBound:
		return m.state.source.Value()
	case memberFrozen:
		return m.state.value, nil
	}
	return nil, m.state.err
}

// Start is what a new watch starts at: the bound member's start, the frozen value, or the failure with its last value.
// An ended member source is a failure with no last value (Pi's `start()` throws its end), which no watch starts at.
func (m *memberSource) Start() state.WatchStart {
	current := m.state
	switch current.kind {
	case memberBound:
		if handle, ok := current.source.(state.SourceHandle); ok {
			return handle.Start()
		}
		value, err := current.source.Value()
		if err != nil {
			return state.WatchStart{Kind: state.StartFailed, Error: err}
		}
		return state.WatchStart{Kind: state.StartValue, Value: value}
	case memberFrozen:
		return state.WatchStart{Kind: state.StartValue, Value: current.value}
	case memberFailed:
		return state.WatchStart{Kind: state.StartFailed, Error: current.err, HasLast: current.hasLast, Last: current.last}
	}
	return state.WatchStart{Kind: state.StartFailed, Error: current.err}
}

// OpenWatch is a watch subscribed to the bound member itself (as `Source.openWatch` starts one: on a failed member at
// its last good value, the failure first).
func (m *memberSource) OpenWatch(ctx context.Context) (*chord.Watch[delta.JsonValue], error) {
	if err := m.Batch().AssertOutside("watch"); err != nil {
		return nil, err
	}
	start := m.Start()
	if start.Kind == state.StartFailed && !start.HasLast {
		return nil, start.Error
	}
	value := start.Value
	if start.Kind == state.StartFailed {
		value = start.Last
	}
	subscriber := &memberWatch{}
	// The watch ends from goroutines of its own too (an aborted context's listener): its detach is a section of the host.
	watch := chord.NewWatch(value, func() { m.Batch().Section(func() { m.Unsubscribe(subscriber) }) }, m.Batch().ReportListener)
	subscriber.watch = watch
	if m.scheduler != nil {
		watch.On(m.scheduler)
	}
	m.Subscribe(subscriber)
	if ctx.Done() != nil {
		watch.ObserveAbort(ctx)
	}
	if start.Kind == state.StartFailed {
		watch.Fail(ctx, state.HandedOut(start.Error))
	}
	return watch, nil
}

// Watched is whether a watch or a subscriber follows it (through the bound member).
func (m *memberSource) Watched() bool { return len(m.passed) > 0 }

// Subscribe feeds subscriber the bound member's frames (it subscribes there) and the re-binds' frames.
func (m *memberSource) Subscribe(subscriber state.Subscriber) {
	if !slices.Contains(m.passed, subscriber) {
		m.passed = append(m.passed, subscriber)
	}
	if handle, ok := m.boundHandle(); ok {
		handle.Subscribe(subscriber)
	}
}

// Unsubscribe is as Source.Unsubscribe.
func (m *memberSource) Unsubscribe(subscriber state.Subscriber) {
	if at := slices.Index(m.passed, subscriber); at >= 0 {
		m.passed = slices.Delete(m.passed, at, at+1)
	}
	if handle, ok := m.boundHandle(); ok {
		handle.Unsubscribe(subscriber)
	}
}

// Invalidate is the bound member changed: its dependents are marked (its frames reach the subscribers directly).
func (m *memberSource) Invalidate() {
	for _, dependent := range m.DependentSet().Snapshot() {
		dependent.Invalidate()
	}
}

// SourceRetired is the bound member's document was retired: its dependents carry the flag on.
func (m *memberSource) SourceRetired(visited map[state.Dependent]struct{}) {
	for _, dependent := range m.DependentSet().Snapshot() {
		dependent.SourceRetired(visited)
	}
}

// freeze is its object failed live and its admitted commits adopted: it keeps the member's value (or error) from now on.
func (m *memberSource) freeze() {
	current := m.state
	if current.kind != memberBound {
		return
	}
	m.leave()
	if handle, ok := current.source.(state.SourceHandle); ok {
		start := handle.Start()
		if start.Kind == state.StartValue {
			m.state = memberState{kind: memberFrozen, value: start.Value}
		} else {
			m.state = memberState{kind: memberFailed, err: start.Error, last: start.Last, hasLast: start.HasLast}
		}
		return
	}
	value, err := current.source.Value()
	if err != nil {
		// A source with no start of its own (a constant) never fails.
		m.state = memberState{kind: memberFailed, err: err}
		return
	}
	m.state = memberState{kind: memberFrozen, value: value}
}

// bind is a new attempt went live: follows its member source, inside a batch. Watches get one frame from the last value
// (a full one after a failure); derives downstream are marked and recompute, a failed one included (self-healing).
func (m *memberSource) bind(source state.DeriveSource) {
	if m.state.kind == memberEnded {
		return
	}
	m.leave()
	if m.Watched() {
		value, ok := m.shown()
		m.framed = &memberFramed{value: value, hasValue: ok}
	}
	m.state = memberState{kind: memberBound, source: source}
	m.hook(source)
	// Marks the derives downstream: a failed one recomputes too (self-healing).
	m.Changed()
}

// vanish is a new attempt's variant lacks the member, inside a batch: reads fail with err; watches and subscribers get
// one failure call and stay open (a later attempt may bring the member back, as `[["r", value]]`).
func (m *memberSource) vanish(err *data.ChordError, ctx context.Context) {
	if m.state.kind == memberEnded {
		return
	}
	last, hasLast := m.lastGood()
	m.leave()
	m.state = memberState{kind: memberFailed, err: err, last: last, hasLast: hasLast}
	m.call(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallFailed, Error: state.HandedOut(err)}, ctx)
	m.Changed()
}

// objectEnded is its object ended, inside a batch: reads fail with err, watches end `ended`, and derives over it are
// marked (their next read fails with it, no cached value stays). Its other subscribers end with their owners (a hold
// with its object).
func (m *memberSource) objectEnded(err *data.ChordError) {
	m.leave()
	m.state = memberState{kind: memberEnded, err: err}
	m.framed = nil
	// A copy: an ending watch unsubscribes itself.
	passed := slices.Clone(m.passed)
	for _, subscriber := range passed {
		subscriber.End(memberEndedWatch)
	}
	m.passed = nil
	for _, dependent := range m.DependentSet().Snapshot() {
		dependent.Invalidate()
	}
}

// Deliver delivers a re-bind's frame: the diff from the watches' value to the new member's.
func (m *memberSource) Deliver(ctx context.Context) error {
	framed := m.framed
	m.framed = nil
	if framed == nil {
		return nil
	}
	after, err := m.Value()
	if err != nil {
		if framed.hasValue {
			m.call(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallFailed, Error: state.HandedOut(err)}, ctx)
		}
		return nil
	}
	before := framed.value
	if framed.hasValue && state.Identical(after, before) {
		return nil
	}
	var ops []delta.Op
	if framed.hasValue {
		ops = delta.DiffRevisions(before, after)
	} else {
		ops = []delta.Op{{"r", after}}
	}
	if len(ops) > 0 {
		m.call(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallValue, Value: after, Ops: ops}, ctx)
	}
	return nil
}

// call is a call of its own (a re-bind's frame, a vanished member) to its watches and subscribers.
func (m *memberSource) call(call chord.WatchCall[delta.JsonValue], ctx context.Context) {
	for _, subscriber := range slices.Clone(m.passed) {
		subscriber.Advance(call, ctx)
	}
}

// shown is the value its watches have before a re-bind; none when they had a failure (their next value is whole).
func (m *memberSource) shown() (delta.JsonValue, bool) {
	start := m.Start()
	if start.Kind == state.StartValue {
		return start.Value, true
	}
	return nil, false
}

// lastGood is its last good value, which watches opened while it is failed start at; none when it never had one.
func (m *memberSource) lastGood() (delta.JsonValue, bool) {
	start := m.Start()
	if start.Kind == state.StartValue {
		return start.Value, true
	}
	return start.Last, start.HasLast
}

// boundHandle is the bound member when it is a Source (Pi's `instanceof Source`: a constant is not).
func (m *memberSource) boundHandle() (state.SourceHandle, bool) {
	if m.state.kind != memberBound {
		return nil, false
	}
	handle, ok := m.state.source.(state.SourceHandle)
	return handle, ok
}

// hook starts reading source: its changes mark this source's dependents, its frames reach the subscribers.
func (m *memberSource) hook(source state.DeriveSource) {
	handle, ok := source.(state.SourceHandle)
	if !ok {
		return
	}
	handle.DependentSet().Add(m)
	for _, subscriber := range m.passed {
		handle.Subscribe(subscriber)
	}
}

// leave stops following the bound source, when bound: no more marks or frames from it.
func (m *memberSource) leave() {
	handle, ok := m.boundHandle()
	if !ok {
		return
	}
	handle.DependentSet().Delete(m)
	for _, subscriber := range m.passed {
		handle.Unsubscribe(subscriber)
	}
}
