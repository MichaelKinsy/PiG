package host

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/host/follow.ts
//
// Followed sources (slice 6b D9, R4, §3): `self.follow(attach)`, a Chord source over a synchronous observer of another
// runtime (Durable's `observeConversation`, `observeTaskGraph`, `observeDoc`). Attached during its attempt's
// activation, detached at the attempt's end. Calls inside a running batch (Durable adopting in the store's commit step)
// are marked in that batch, calls outside one are grouped into one batch on the next microtask, so a derive over two
// views of one commit computes once with both changes.

// FollowObserver is the observer a followed runtime is given: Chord's watch shapes (Durable's `Observer`). Both calls
// run inside a section of the host's loop (the runtime's commit step is one).
type FollowObserver[T any] interface {
	// Advance is one change, synchronously and quickly: Chord only records it.
	Advance(call chord.WatchCall[T], ctx context.Context)
	// End is the followed source ended: `failed` (its runtime failed), or `ended` for good.
	End(end chord.WatchEnd)
}

// FollowAttachment is what attaching gives: the value every later call starts from, and how to detach (Durable's
// `Observation`).
type FollowAttachment[T any] struct {
	Value  T
	Detach func()
}

// FollowAttach is what `self.follow` takes: attaches observer, returning the attachment, nil for an absent address (Pi
// resolves with `undefined`), or the error Pi's promise rejects with. It blocks until the runtime attached and is called
// outside a section of the host's loop. The observer takes calls of any value (it only records them), so T is the
// attachment's alone.
type FollowAttach[T any] func(observer FollowObserver[any], ctx context.Context) (*FollowAttachment[T], error)

// followOwner is who a followed source belongs to: its attempt (R4) and its context.
type followOwner struct {
	label string
	ctx   context.Context
	// ended is the followed runtime ended its observer: `failed` fails the attempt `transient` (live or activating, its
	// retry attaches again); `ended` while the attempt activates fails it `transient` too (it would go live on a source
	// that ended); `ended` while live is final for the source only.
	ended func(end chord.WatchEnd)
}

// followKind is where a followed source stands: waiting for its attachment (with the runtime's end when it came first),
// attached, ended by the runtime (its value stays; detached at its attempt's end), or detached by its attempt.
type followKind uint8

const (
	followAttaching followKind = iota
	followAttached
	followEnded
	followDetached
)

type followState struct {
	kind followKind
	// end is the runtime's end that came first, while attaching.
	end *chord.WatchEnd
	// detach detaches the observer, once attached or ended.
	detach func()
}

// followEnding is the runtime's end of a followed source that still has to reach its watches.
type followEnding uint8

const (
	endingNone followEnding = iota
	// endingQueued: the runtime's end, still to reach the watches after the queued calls' batch.
	endingQueued
	// endingFrame: the runtime's end, still to reach the watches after the current batch's frame (a retirement and an
	// end in one batch: the retirement first).
	endingFrame
)

var followNoOps = []delta.Op{}

var followEndedWatch = chord.WatchEnd{Kind: chord.WatchEnded}

// followSource is a followed source: the runtime's value, its calls applied in order, its frames delivered in the host's
// batch.
type followSource struct {
	state.Source
	attachFn FollowAttach[delta.JsonValue]
	owner    followOwner
	loop     sectionLoop
	state    followState
	value    delta.JsonValue
	hasValue bool
	// queued is the calls not applied yet: before the attachment resolved, and outside a batch until the next microtask.
	queued []chord.WatchCall[delta.JsonValue]
	// scheduled is whether a microtask is queued to apply queued in a batch of its own.
	scheduled bool
	ending    followEnding
	// ops is the ops of the current batch's frame (kept only while watched), and retired whether a retirement came in it.
	ops     []delta.Op
	retired bool
	listed  bool
}

var _ state.SourceHandle = (*followSource)(nil)

// newFollowSource makes the source of attach, owned by owner, on batch. loop is the host's: the continuation of attach
// runs in one of its sections.
func newFollowSource(attach FollowAttach[delta.JsonValue], owner followOwner, batch *state.Batch, loop sectionLoop) *followSource {
	s := &followSource{attachFn: attach, owner: owner, loop: loop, state: followState{kind: followAttaching}}
	s.Init(batch, s)
	return s
}

// Value is the runtime's value; it fails before the source attached.
func (s *followSource) Value() (delta.JsonValue, error) {
	if !s.hasValue {
		return nil, data.NewChordError(data.ChordErrorInit{
			Code:    data.CodeNotReady,
			Message: s.owner.label + ": a followed source read before it attached",
		}, nil)
	}
	return s.value, nil
}

// followObserverOf is the observer a followed source gives its runtime.
type followObserverOf struct{ source *followSource }

func (o followObserverOf) Advance(call chord.WatchCall[delta.JsonValue], _ context.Context) {
	o.source.advance(call)
}

func (o followObserverOf) End(end chord.WatchEnd) { o.source.end(end) }

// attach attaches (activation, after the natives opened): the calls that came meanwhile apply on the attachment's value,
// and an end that came meanwhile takes effect then (the value stays). A rejected attachment, or nil (an absent address),
// is an error; an attachment arriving after its attempt detached the source is detached at once. Pi's `await` on the
// attachment is this call blocking; the resumption is a section of the host's loop.
func (s *followSource) attach(ctx context.Context) error {
	attached, err := s.attachFn(followObserverOf{source: s}, ctx)
	if err != nil {
		return err
	}
	if attached == nil {
		return data.NewChordError(data.ChordErrorInit{
			Code:    data.CodeUnavailable,
			Message: s.owner.label + ": the followed address is absent",
		}, nil)
	}
	s.loop.Run(func() {
		current := s.state
		if current.kind != followAttaching {
			attached.Detach()
			return
		}
		s.value, s.hasValue = attached.Value, true
		// Nothing watches it yet (members are checked after this): the queued calls only move the value on.
		for _, call := range s.queued {
			if call.Kind == chord.WatchCallValue {
				s.value = call.Value
			}
		}
		s.queued = nil
		if current.end != nil && current.end.Kind == chord.WatchEnded {
			s.state = followState{kind: followEnded, detach: attached.Detach}
		} else {
			s.state = followState{kind: followAttached, detach: attached.Detach}
		}
	})
	return nil
}

// detach is its attempt ended: the observer is detached (also after the runtime ended it) and its watches end `ended`.
func (s *followSource) detach() {
	current := s.state
	s.state = followState{kind: followDetached}
	s.queued = nil
	if current.kind == followAttached || current.kind == followEnded {
		current.detach()
	}
	s.EndWatches()
}

// OpenWatch is a watch of an ended source: it opens at its value and ends `ended` at once.
func (s *followSource) OpenWatch(ctx context.Context) (*chord.Watch[delta.JsonValue], error) {
	watch, err := s.Source.OpenWatch(ctx)
	if err != nil {
		return nil, err
	}
	if s.over() {
		watch.EndWith(followEndedWatch)
	}
	return watch, nil
}

// Subscribe is a subscriber of an ended source (a reference's watch through its member source): it ends `ended` at once.
func (s *followSource) Subscribe(subscriber state.Subscriber) {
	if s.over() {
		subscriber.End(followEndedWatch)
	} else {
		s.Source.Subscribe(subscriber)
	}
}

// Deliver delivers the frame of the batch that listed it.
func (s *followSource) Deliver(ctx context.Context) error {
	ops := s.ops
	retired := s.retired
	s.ops = nil
	s.retired = false
	s.listed = false
	if len(ops) == 0 {
		ops = followNoOps
	}
	s.Publish(s.value, ops, retired, ctx)
	if s.ending == endingFrame {
		s.ending = endingNone
		s.EndWatchesAfterDelivery()
	}
	return nil
}

// over is whether nothing reaches its watches any more: ended by the runtime (what it gave delivered) or detached.
func (s *followSource) over() bool {
	kind := s.state.kind
	return (kind == followEnded && s.ending == endingNone) || kind == followDetached
}

// advance is a call of the runtime: before the attachment, queued; inside a running batch, applied there (after what was
// queued); outside one, queued for one batch on the next microtask.
func (s *followSource) advance(call chord.WatchCall[delta.JsonValue]) {
	kind := s.state.kind
	if kind == followEnded || kind == followDetached {
		return
	}
	if kind == followAttaching || !s.Batch().Running() {
		s.queued = append(s.queued, call)
		if kind == followAttached {
			s.schedule()
		}
		return
	}
	s.applyQueued()
	s.apply(call)
}

// end is the runtime ended its observer (R4); its attempt is told. `ended` is final: what came before reaches the
// watches first (the queued calls in their batch, a call of the running batch in its frame), then they end, the value
// staying. A `failed` attempt detaches the source.
func (s *followSource) end(end chord.WatchEnd) {
	current := s.state
	if current.kind == followEnded || current.kind == followDetached {
		return
	}
	if current.kind == followAttaching {
		if current.end == nil {
			s.state = followState{kind: followAttaching, end: &end}
		}
		s.owner.ended(end)
		return
	}
	if end.Kind == chord.WatchEnded {
		s.state = followState{kind: followEnded, detach: current.detach}
		if len(s.queued) > 0 && !s.Batch().Running() {
			s.ending = endingQueued
		} else {
			// Inside a running batch the queued calls join it (none is queued outside one: they wait for their batch).
			s.applyQueued()
			s.endAfterFrame()
		}
	}
	s.owner.ended(end)
}

// endAfterFrame ends the watches once this batch's frame reached them, at once when the source is in none.
func (s *followSource) endAfterFrame() {
	// No frame to wait for (nothing watches it): the end is over now, and a later watch ends at once.
	if s.listed {
		s.ending = endingFrame
		return
	}
	s.ending = endingNone
	s.EndWatchesAfterDelivery()
}

func (s *followSource) schedule() {
	if s.scheduled {
		return
	}
	s.scheduled = true
	s.Batch().Soon(func() {
		s.scheduled = false
		kind := s.state.kind
		if kind == followAttached || kind == followEnded {
			s.applyQueued()
		}
		if s.ending == endingQueued {
			s.endAfterFrame()
		}
	}, s.owner.ctx)
}

func (s *followSource) applyQueued() {
	queued := s.queued
	if len(queued) == 0 {
		return
	}
	s.queued = nil
	for _, call := range queued {
		s.apply(call)
	}
}

// apply applies one value call inside the running batch: the new value, its ops for the frame, the source marked once.
func (s *followSource) apply(call chord.WatchCall[delta.JsonValue]) {
	// The runtime's failures are final: it ends its observer instead of sending one.
	if call.Kind != chord.WatchCallValue {
		return
	}
	s.value = call.Value
	if call.Retired {
		s.retired = true
	}
	if !s.Watched() {
		s.Changed()
		if call.Retired {
			s.RetiredInBatch()
		}
		return
	}
	s.ops = append(s.ops, call.Ops...)
	if !s.listed {
		s.listed = true
		s.Changed()
	}
	if call.Retired {
		s.RetiredInBatch()
	}
}
