package host

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/host/reference-source.ts
//
// A reference's member as consumers follow it (decision "References are the only indirection", §7): a consumer's
// watches and derives over `ref.member` (or a key of a keyed member) attach here, and this passes them straight through
// to the object instance's own document or derive (its watches and subscribers subscribe there: no extra hop on
// commits, the commit's value and ops by reference). When the reference's target moves to a new instance (or a new
// object of its identity), it moves them and sends one frame, a diff from the last value; while it points at nothing it
// keeps the last value (a pointer, no copy), with one `failed` call when its target failed (none while it loads).

// sourceKind is where a reference's source stands: never had a value (`unbound`: reads fail with what its reference has
// instead), following an instance's member, holding its last value while pointing at nothing (`failure` while its target
// is failed: new watches start with it; `told`: its watches got a failure since that value, so the next is whole), failed
// (a member its target's live variant lacks, or its compute threw when it was held: reads fail, `last` is the last good
// value) or ended with its reference (nothing kept).
type sourceKind uint8

const (
	sourceUnbound sourceKind = iota
	sourceBound
	sourceHeld
	sourceFailed
	sourceEnded
)

type sourceState struct {
	kind sourceKind
	// source is the bound member.
	source state.DeriveSource
	// value is the held value.
	value delta.JsonValue
	// failure is the failure a held value is held with; nil while its target loads (Pi's `NO_FAILURE`).
	failure error
	told    bool
	// err is the failure of a failed source, or the end of an ended one.
	err error
	// last is a failed source's last good value, when hasLast.
	last    delta.JsonValue
	hasLast bool
}

// sourceOwner is a reference owning its sources: what a read of one that never had a value fails with.
type sourceOwner interface {
	none() error
}

var sourceEndedWatch = chord.WatchEnd{Kind: chord.WatchEnded}

// sourceFramed is what a source's watches had at a re-bind (hasValue false for no value: they had a failure), until the
// re-bind's frame.
type sourceFramed struct {
	value    delta.JsonValue
	hasValue bool
}

// sourceWatch adapts a Watch to the subscriber shape Pi's Watch has itself: a subscriber is told apart by identity, so
// one adapter lives as long as its watch.
type sourceWatch struct{ watch *chord.Watch[delta.JsonValue] }

var _ state.Subscriber = (*sourceWatch)(nil)

func (w *sourceWatch) Advance(call chord.WatchCall[delta.JsonValue], ctx context.Context) {
	w.watch.Advance(ctx, call)
}

func (w *sourceWatch) End(end chord.WatchEnd) { w.watch.EndWith(end) }

func (w *sourceWatch) EndAfterDelivery() { w.watch.EndAfterDelivery() }

// referenceSource is one member (or key) of one reference. Bound, it is a dependent of the instance's member (its own
// dependents are marked with it); its watches and subscribers subscribe to that member itself, so the member's frames
// reach them as they are, and a derive member stays lazy while nothing follows it; holding or moving moves them.
type referenceSource struct {
	state.Source
	state sourceState
	// scheduler is the delivery scheduler of the batch its watches run on.
	scheduler *chord.DeliveryScheduler
	// passed is its watches and subscribers, each subscribed to the bound member while bound; held, they get nothing.
	passed []state.Subscriber
	// framed is what its watches had at a re-bind, until the re-bind's frame.
	framed *sourceFramed
	// owner is its reference.
	owner sourceOwner
}

var (
	_ state.SourceHandle = (*referenceSource)(nil)
	_ state.Dependent    = (*referenceSource)(nil)
)

func newReferenceSource(batch *state.Batch, scheduler *chord.DeliveryScheduler, owner sourceOwner) *referenceSource {
	s := &referenceSource{scheduler: scheduler, owner: owner}
	s.Init(batch, s)
	return s
}

// unbound is whether it never had a value (a watch waits for its reference's values first).
func (s *referenceSource) unbound() bool { return s.state.kind == sourceUnbound }

// Value is the bound member's value, the held one, or the failure.
func (s *referenceSource) Value() (delta.JsonValue, error) {
	switch s.state.kind {
	case sourceBound:
		return s.state.source.Value()
	case sourceHeld:
		// A derive over a failed reference's member computes on its last value too (owner, G2: A).
		return s.state.value, nil
	case sourceUnbound:
		return nil, s.owner.none()
	}
	return nil, s.state.err
}

// Start is what a new watch starts at: the bound member's start, the held value (after its failure), or the failure.
// An unbound or ended source is a failure with no last value (Pi's `start()` throws), which no watch starts at.
func (s *referenceSource) Start() state.WatchStart {
	current := s.state
	switch current.kind {
	case sourceBound:
		if handle, ok := current.source.(state.SourceHandle); ok {
			return handle.Start()
		}
		value, err := current.source.Value()
		if err != nil {
			return state.WatchStart{Kind: state.StartFailed, Error: err}
		}
		return state.WatchStart{Kind: state.StartValue, Value: value}
	case sourceHeld:
		if current.failure == nil {
			return state.WatchStart{Kind: state.StartValue, Value: current.value}
		}
		return state.WatchStart{Kind: state.StartFailed, Error: current.failure, HasLast: true, Last: current.value}
	case sourceFailed:
		return state.WatchStart{Kind: state.StartFailed, Error: current.err, HasLast: current.hasLast, Last: current.last}
	case sourceUnbound:
		return state.WatchStart{Kind: state.StartFailed, Error: s.owner.none()}
	}
	return state.WatchStart{Kind: state.StartFailed, Error: current.err}
}

// OpenWatch is a watch subscribed to the bound member itself (as `Source.openWatch` starts one: on a failed member at
// its last good value, the failure first).
func (s *referenceSource) OpenWatch(ctx context.Context) (*chord.Watch[delta.JsonValue], error) {
	if err := s.Batch().AssertOutside("watch"); err != nil {
		return nil, err
	}
	start := s.Start()
	if start.Kind == state.StartFailed && !start.HasLast {
		return nil, start.Error
	}
	value := start.Value
	if start.Kind == state.StartFailed {
		value = start.Last
	}
	subscriber := &sourceWatch{}
	// The watch ends from goroutines of its own too (an aborted context's listener): its detach is a section of the host.
	watch := chord.NewWatch(value, func() { s.Batch().Section(func() { s.Unsubscribe(subscriber) }) }, s.Batch().ReportListener)
	subscriber.watch = watch
	if s.scheduler != nil {
		watch.On(s.scheduler)
	}
	s.Subscribe(subscriber)
	if ctx.Done() != nil {
		watch.ObserveAbort(ctx)
	}
	if start.Kind == state.StartFailed {
		watch.Fail(ctx, state.HandedOut(start.Error))
	}
	return watch, nil
}

// inUse is whether anything follows it: a watch, a subscriber or a derive (moving or ending it then needs a batch).
func (s *referenceSource) inUse() bool { return s.Watched() || s.DependentSet().Len() > 0 }

// Watched is whether a watch or a subscriber follows it (through the bound member).
func (s *referenceSource) Watched() bool { return len(s.passed) > 0 }

// Subscribe feeds subscriber the bound member's frames (it subscribes there) and the moves' frames.
func (s *referenceSource) Subscribe(subscriber state.Subscriber) {
	if !slices.Contains(s.passed, subscriber) {
		s.passed = append(s.passed, subscriber)
	}
	if handle, ok := s.boundHandle(); ok {
		handle.Subscribe(subscriber)
	}
}

// Unsubscribe is as Source.Unsubscribe.
func (s *referenceSource) Unsubscribe(subscriber state.Subscriber) {
	if at := slices.Index(s.passed, subscriber); at >= 0 {
		s.passed = slices.Delete(s.passed, at, at+1)
	}
	if handle, ok := s.boundHandle(); ok {
		handle.Unsubscribe(subscriber)
	}
}

// Invalidate is the bound member changed: its dependents are marked (its frames reach the subscribers directly).
func (s *referenceSource) Invalidate() {
	for _, dependent := range s.DependentSet().Snapshot() {
		dependent.Invalidate()
	}
}

// SourceRetired is the bound member's document was retired: its dependents carry the flag on.
func (s *referenceSource) SourceRetired(visited map[state.Dependent]struct{}) {
	for _, dependent := range s.DependentSet().Snapshot() {
		dependent.SourceRetired(visited)
	}
}

// bind follows source, an instance's member, inside a batch: watches get one frame from the last value (a full one after
// a failure); derives downstream are marked and recompute, a failed one included (self-healing).
func (s *referenceSource) bind(source state.DeriveSource) {
	if s.state.kind == sourceEnded || (s.state.kind == sourceBound && s.state.source == source) {
		return
	}
	s.leave()
	if s.Watched() {
		value, ok := s.shown()
		s.framed = &sourceFramed{value: value, hasValue: ok}
	}
	s.state = sourceState{kind: sourceBound, source: source}
	s.hook(source)
	// Marks the derives downstream: a failed one recomputes too (self-healing).
	s.Changed()
}

// hold: its target loads (a restart, a retry, a new object not live yet): it keeps its last value, and tells nothing.
func (s *referenceSource) hold() {
	switch s.state.kind {
	case sourceHeld:
		// Its watches had the failure: the next value stays whole (`told`).
		s.state.failure = nil
	case sourceBound:
		s.keep(nil)
	}
}

// fail: its target failed (err: its cause's, a refused version's, a removed type's), inside a batch: it keeps its last
// value, its watches and subscribers get one failure call (once per failure) and stay open.
func (s *referenceSource) fail(err *data.ChordError, ctx context.Context) {
	// Pi makes the error's stack text now (`handedOut`), so it keeps none of the failed instance's frames: a Go error keeps
	// no frames.
	switch s.state.kind {
	case sourceHeld:
		if s.state.failure != nil {
			return
		}
		s.state = sourceState{kind: sourceHeld, value: s.state.value, failure: err, told: true}
	case sourceBound:
		if !s.keep(err) {
			return
		}
	default:
		return
	}
	s.call(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallFailed, Error: err}, ctx)
}

// vanish: its target's live variant lacks the member, inside a batch: reads fail with err; watches and subscribers get
// one failure call and stay open (a later instance may bring the member back, as `[["r", value]]`).
func (s *referenceSource) vanish(err *data.ChordError, ctx context.Context) {
	if s.state.kind == sourceEnded {
		return
	}
	var last delta.JsonValue
	hasLast := false
	if s.state.kind != sourceUnbound {
		last, hasLast = s.lastGood()
	}
	s.leave()
	s.state = sourceState{kind: sourceFailed, err: state.HandedOut(err), last: last, hasLast: hasLast}
	s.call(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallFailed, Error: err}, ctx)
	s.Changed()
}

// end: its reference ended, inside a batch: reads fail with err, watches end `ended`, and derives over it are marked
// (their next read fails with it, no cached value stays).
func (s *referenceSource) end(err *data.ChordError) {
	s.leave()
	s.state = sourceState{kind: sourceEnded, err: err}
	s.framed = nil
	// A copy: an ending watch unsubscribes itself.
	passed := slices.Clone(s.passed)
	s.passed = nil
	for _, subscriber := range passed {
		subscriber.End(sourceEndedWatch)
	}
	for _, dependent := range s.DependentSet().Snapshot() {
		dependent.Invalidate()
	}
}

// Deliver delivers a move's frame: the diff from the watches' value to the new member's.
func (s *referenceSource) Deliver(ctx context.Context) error {
	framed := s.framed
	s.framed = nil
	if framed == nil {
		return nil
	}
	after, err := s.Value()
	if err != nil {
		if framed.hasValue {
			s.call(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallFailed, Error: state.HandedOut(err)}, ctx)
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
		s.call(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallValue, Value: after, Ops: ops}, ctx)
	}
	return nil
}

// keep stops following the bound member and keeps its value (failure: the failure it is held with); a member whose
// value throws becomes failed. It reports whether it holds a value.
func (s *referenceSource) keep(failure error) bool {
	bound := s.state
	handle, isSource := bound.source.(state.SourceHandle)
	var start state.WatchStart
	var value delta.JsonValue
	if isSource {
		start = handle.Start()
	} else {
		var err error
		value, err = bound.source.Value()
		if err != nil {
			// A source with no start of its own (a constant) never fails.
			s.leave()
			s.state = sourceState{kind: sourceFailed, err: state.HandedOut(err)}
			return false
		}
	}
	s.leave()
	if !isSource || start.Kind == state.StartValue {
		if isSource {
			value = start.Value
		}
		s.state = sourceState{kind: sourceHeld, value: value, failure: failure, told: failure != nil}
		return true
	}
	s.state = sourceState{kind: sourceFailed, err: state.HandedOut(start.Error), last: start.Last, hasLast: start.HasLast}
	return false
}

// call is a call of its own (a move's frame, a failure) to its watches and subscribers.
func (s *referenceSource) call(call chord.WatchCall[delta.JsonValue], ctx context.Context) {
	for _, subscriber := range slices.Clone(s.passed) {
		subscriber.Advance(call, ctx)
	}
}

// shown is the value its watches have before a move; none when they had a failure since it (their next value is whole,
// `[["r", value]]`, even when unchanged).
func (s *referenceSource) shown() (delta.JsonValue, bool) {
	if s.state.kind == sourceUnbound || (s.state.kind == sourceHeld && s.state.told) {
		return nil, false
	}
	start := s.Start()
	if start.Kind == state.StartValue {
		return start.Value, true
	}
	return nil, false
}

// lastGood is its last good value, which watches opened while it is failed start at; none when it never had one.
func (s *referenceSource) lastGood() (delta.JsonValue, bool) {
	start := s.Start()
	if start.Kind == state.StartValue {
		return start.Value, true
	}
	return start.Last, start.HasLast
}

// boundHandle is the bound member when it is a Source (Pi's `instanceof Source`: a constant is not).
func (s *referenceSource) boundHandle() (state.SourceHandle, bool) {
	if s.state.kind != sourceBound {
		return nil, false
	}
	handle, ok := s.state.source.(state.SourceHandle)
	return handle, ok
}

// hook starts reading source: its changes mark this source's dependents, its frames reach the subscribers.
func (s *referenceSource) hook(source state.DeriveSource) {
	handle, ok := source.(state.SourceHandle)
	if !ok {
		return
	}
	handle.DependentSet().Add(s)
	for _, subscriber := range s.passed {
		handle.Subscribe(subscriber)
	}
}

// leave stops following the bound source, when bound: no more marks or frames from it.
func (s *referenceSource) leave() {
	handle, ok := s.boundHandle()
	if !ok {
		return
	}
	handle.DependentSet().Delete(s)
	for _, subscriber := range s.passed {
		handle.Unsubscribe(subscriber)
	}
}
