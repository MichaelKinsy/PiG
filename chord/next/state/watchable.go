package state

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/next/state/watchable.ts (Subscriber, WatchStart, handedOut, Dependent, Source, constant,
// Constant). Chord's JSON watchables: sources (documents and derives, with their watches and dependent derives) and
// constants.

// StartKind tags a WatchStart and a NativeStart: the value a new watch starts at, or the failure its source is in.
type StartKind string

const (
	// StartValue is a start at the source's value.
	StartValue StartKind = "value"
	// StartFailed is a start while the source is failed, with the failure and its last good value, if it had one.
	StartFailed StartKind = "failed"
)

// Subscriber is a synchronous subscriber of a source (a hold over a link, a member source): fed the source's call inside
// the batch that made it, before the batch ends, so everything one commit changed can leave in one message. The watch's
// own sink shape (Watch.Advance, Watch.EndWith). A Subscriber must be comparable (a pointer): a source tells them apart.
type Subscriber interface {
	// Advance is the source's call of this batch, in its batch's ctx: a value (the ops from the previous one, and whether
	// its document was retired in it, or for a derive one of its sources' documents), or a failure it may recover from;
	// the next value after a failure is [["r", value]].
	Advance(call chord.WatchCall[delta.JsonValue], ctx context.Context)
	// End is the source ended for good with end; nothing follows, and the subscriber is dropped.
	End(end chord.WatchEnd)
	// EndAfterDelivery is the source ended for good after its last call (a followed runtime ended): a subscriber
	// delivering later (a watch) ends ended once it delivered what it was given, others end now.
	EndAfterDelivery()
}

// WatchStart is what a new watch of a source starts at (design §11): its value (Kind StartValue, Value), or, while it is
// failed, the failure (Kind StartFailed, Error) and its last good value Last, when HasLast; with no last value a watch
// cannot start, there is nothing to start at. Pi's `last: undefined` is HasLast false, which a JSON null Last is not.
type WatchStart struct {
	Kind    StartKind
	Value   delta.JsonValue
	Error   error
	HasLast bool
	Last    delta.JsonValue
}

// HandedOut returns an error Chord hands to apps (a failure call, a report). Pi makes its stack text first, because V8
// keeps the frames' receivers, which would keep the host behind the throw alive for as long as an app keeps the error; a
// Go error keeps no frames, so it is returned as it is.
func HandedOut(err error) error { return err }

// Dependent is what reads a source and is marked by its changes: a derive, or a member source forwarding to derives
// (Source.dependents). A Dependent must be comparable (a pointer).
type Dependent interface {
	// Invalidate is the source changed in the current batch: flags only, no compute.
	Invalidate()
	// SourceRetired is a source's document was retired in the current batch (rule 21); visited keeps a diamond walked
	// once.
	SourceRetired(visited map[Dependent]struct{})
}

// DependentSet is Pi's `Set<Dependent>`: the derives and registry tables reading a source, in the order they started to;
// each removes itself when it stops. It is safe for concurrent use; Snapshot is never mutated.
type DependentSet struct{ set orderedSet[Dependent] }

// Add adds dependent unless it is a member.
func (s *DependentSet) Add(dependent Dependent) { s.set.Add(dependent) }

// Delete removes dependent and reports whether it was a member.
func (s *DependentSet) Delete(dependent Dependent) bool { return s.set.Delete(dependent) }

// Len is the number of dependents.
func (s *DependentSet) Len() int { return s.set.Len() }

// Clear removes every dependent.
func (s *DependentSet) Clear() { s.set.Clear() }

// Snapshot returns the dependents in the order they were added; the caller must not modify the slice, which a later Add
// or Delete does not change.
func (s *DependentSet) Snapshot() []Dependent { return s.set.Snapshot() }

// DeriveInput is what a derive reads: a JSON source or constant, or a native source (a writable, a registry value,
// ref.state).
type DeriveInput interface {
	// DeriveValue is the value the derive computes from: its source's `.value`, which a failing JSON source throws and a
	// native source keeps at its last good value.
	DeriveValue() (any, error)
	// DependentSet is the set the derive adds itself to; nil for a constant, which never changes.
	DependentSet() *DependentSet
}

// DeriveSource is a JSON watchable as Chord follows it (a state member, a JSON derive source): a Source, or a Constant.
type DeriveSource interface {
	DeriveInput
	// Value is the current value; the error is what Pi's getter throws (a failed derive's failure).
	Value() (delta.JsonValue, error)
}

// SourceImpl is what a Source needs of the type that embeds it, the Go form of Pi's abstract and virtual members. Value
// and Deliver are the embedder's own; Start and OpenWatch are promoted from Source unless the embedder defines them (an
// override calls the base through the embedded Source).
type SourceImpl interface {
	// Value is the current value; the error is what the getter throws.
	Value() (delta.JsonValue, error)
	// Deliver delivers this batch's frame to its watches; called once per batch that listed it with Changed.
	Deliver(ctx context.Context) error
	// Start is what a new watch starts at; a source that can fail and recover tells its failure (WatchStart).
	Start() WatchStart
	// OpenWatch is Watch without the Promise.
	OpenWatch(ctx context.Context) (*chord.Watch[delta.JsonValue], error)
}

// SourceHandle is a Source as other layers hold it, whatever embeds it: Pi's `Source` as a type. An embedder satisfies it
// through the members Source provides and the ones it defines (Value, Deliver).
type SourceHandle interface {
	DeriveSource
	Watched() bool
	Watch(ctx context.Context) (chord.WatchHandle[delta.JsonValue], error)
	OpenWatch(ctx context.Context) (*chord.Watch[delta.JsonValue], error)
	Start() WatchStart
	Subscribe(subscriber Subscriber)
	Unsubscribe(subscriber Subscriber)
	Deliver(ctx context.Context) error
}

// Source is a changing value on a host (a singleton document, a derive, a replica): its watches and subscribers get one
// frame per batch that changed it, and the derives reading it are marked dirty when it changes. It is embedded by value
// and initialized with Init: `s := &Foo{}; s.Source.Init(batch, s)`.
type Source struct {
	impl SourceImpl
	// batch is the host's batch.
	batch *Batch
	// dependents is the derives reading this source (and member sources forwarding to some).
	dependents  DependentSet
	watches     orderedSet[*chord.Watch[delta.JsonValue]]
	subscribers orderedSet[Subscriber]
}

// Init makes the source of batch, with impl its embedder.
func (s *Source) Init(batch *Batch, impl SourceImpl) {
	s.batch = batch
	s.impl = impl
}

// Batch is the host's batch.
func (s *Source) Batch() *Batch { return s.batch }

// DependentSet is the derives reading this source (and member sources forwarding to some); each removes itself when it
// stops.
func (s *Source) DependentSet() *DependentSet { return &s.dependents }

// DeriveValue is the value a derive reads: the embedder's Value.
func (s *Source) DeriveValue() (any, error) { return s.impl.Value() }

// Watched reports whether a watch or a subscriber follows this source.
func (s *Source) Watched() bool { return s.watches.Len() > 0 || s.subscribers.Len() > 0 }

// Watch is Pi's `watch(context)`, which resolves with OpenWatch's result or rejects with its throw: the call returns the
// watch or the error, and never suspends.
func (s *Source) Watch(ctx context.Context) (chord.WatchHandle[delta.JsonValue], error) {
	watch, err := s.impl.OpenWatch(ctx)
	if err != nil {
		return nil, err
	}
	return watch, nil
}

// OpenWatch is Watch without the Promise, for references, which must register the watch before anything else runs. On a
// failed source it opens at the last good value and its first call is the failure; with no value to start at it returns
// the failure.
func (s *Source) OpenWatch(ctx context.Context) (*chord.Watch[delta.JsonValue], error) {
	if err := s.batch.AssertOutside("watch"); err != nil {
		return nil, err
	}
	start := s.impl.Start()
	if start.Kind == StartFailed && !start.HasLast {
		return nil, start.Error
	}
	value := start.Value
	if start.Kind == StartFailed {
		value = start.Last
	}
	var watch *chord.Watch[delta.JsonValue]
	watch = chord.NewWatch(value, func() { s.watches.Delete(watch) }, s.batch.ReportListener)
	if scheduler := s.batch.deliveries(); scheduler != nil {
		watch.On(scheduler)
	}
	s.watches.Add(watch)
	if ctx.Done() != nil {
		watch.ObserveAbort(ctx)
	}
	if start.Kind == StartFailed {
		watch.Fail(ctx, start.Error)
	}
	return watch, nil
}

// Start is what a new watch starts at; a source that can fail and recover tells its failure (WatchStart). The default
// is the embedder's Value; a Value that fails is a start with no last value, which no watch can start at.
func (s *Source) Start() WatchStart {
	value, err := s.impl.Value()
	if err != nil {
		return WatchStart{Kind: StartFailed, Error: err}
	}
	return WatchStart{Kind: StartValue, Value: value}
}

// Subscribe feeds subscriber every frame from now on, inside each batch; the caller took the current value first.
func (s *Source) Subscribe(subscriber Subscriber) { s.subscribers.Add(subscriber) }

// Unsubscribe stops feeding subscriber.
func (s *Source) Unsubscribe(subscriber Subscriber) { s.subscribers.Delete(subscriber) }

// Deliver is the embedder's, which Source needs to list in the batch; see SourceImpl.
func (s *Source) Deliver(ctx context.Context) error { return s.impl.Deliver(ctx) }

// Changed records a change in the current batch: listed for a frame when watched (the embedder's `Watched` when it
// overrides it, as Pi's `changed` reads the virtual `watched`), and every dependent marked dirty. Pi's protected
// `changed`.
func (s *Source) Changed() {
	watched := s.Watched()
	if overriding, ok := s.impl.(interface{ Watched() bool }); ok {
		watched = overriding.Watched()
	}
	if watched {
		s.batch.Changed(s.impl)
	}
	for _, dependent := range s.dependents.Snapshot() {
		dependent.Invalidate()
	}
}

// RetiredInBatch records a retirement of this source's document in the current batch: every derive reading it, directly
// or through other derives, carries the flag on in its frame (rule 21). Called after Changed.
func (s *Source) RetiredInBatch() {
	dependents := s.dependents.Snapshot()
	if len(dependents) == 0 {
		return
	}
	// Allocated only for a retirement, which is rare.
	visited := map[Dependent]struct{}{}
	for _, dependent := range dependents {
		dependent.SourceRetired(visited)
	}
}

// Publish queues one frame on every watch and feeds it to every subscriber.
func (s *Source) Publish(value delta.JsonValue, ops []delta.Op, retired bool, ctx context.Context) {
	s.Forward(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallValue, Value: value, Ops: ops, Retired: retired}, ctx)
}

// Forward queues call on every watch and feeds it to every subscriber, as it is (a member source passes one on).
func (s *Source) Forward(call chord.WatchCall[delta.JsonValue], ctx context.Context) {
	for _, watch := range s.watches.Snapshot() {
		watch.Advance(ctx, call)
	}
	for _, subscriber := range s.subscribers.Snapshot() {
		subscriber.Advance(call, ctx)
	}
}

// FailWatches gives every watch and subscriber one failure call (design §11): the source cannot compute its value now
// and may recover; they stay open, and its next value is [["r", value]].
func (s *Source) FailWatches(err error, ctx context.Context) {
	s.Forward(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallFailed, Error: HandedOut(err)}, ctx)
}

// EndWatchesAfterDelivery ends every watch and subscriber for good once what they were given is delivered (a followed
// runtime ended after a last call, Durable's endAfterDelivery): ended; nothing follows.
func (s *Source) EndWatchesAfterDelivery() {
	for _, watch := range s.watches.Snapshot() {
		watch.EndAfterDelivery()
	}
	if s.subscribers.Len() == 0 {
		return
	}
	subscribers := s.subscribers.Snapshot()
	s.subscribers.Clear()
	for _, subscriber := range subscribers {
		subscriber.EndAfterDelivery()
	}
}

// EndWatches ends every watch and subscriber for good (its owner ended): ended; nothing follows.
func (s *Source) EndWatches() {
	// Ending detaches, which deletes the watch from the set: the snapshot is not affected.
	for _, watch := range s.watches.Snapshot() {
		watch.EndWith(chord.WatchEnd{Kind: chord.WatchEnded})
	}
	if s.subscribers.Len() == 0 {
		return
	}
	// A snapshot: a subscriber may unsubscribe as it ends.
	subscribers := s.subscribers.Snapshot()
	s.subscribers.Clear()
	for _, subscriber := range subscribers {
		subscriber.End(chord.WatchEnd{Kind: chord.WatchEnded})
	}
}

// Constant is the watchable behind constant(value): a JSON watchable that never changes (start data, §18); usable as a
// state member and as a derive source. Its watches never get a frame and end only by stop or cancellation. The class is
// Constant and the app-facing typed function is ConstantOf, which Go cannot both name Constant.
type Constant struct{ value delta.JsonValue }

// NewConstant returns a constant holding value as it was given.
func NewConstant(value delta.JsonValue) *Constant { return &Constant{value: value} }

// Value is the constant's value; it never fails.
func (c *Constant) Value() (delta.JsonValue, error) { return c.value, nil }

// DeriveValue is the value a derive reads.
func (c *Constant) DeriveValue() (any, error) { return c.value, nil }

// DependentSet is nil: a constant never changes, so no derive registers with it.
func (c *Constant) DependentSet() *DependentSet { return nil }

// Watch is Pi's `watch(context)`; see Source.Watch.
func (c *Constant) Watch(ctx context.Context) (chord.WatchHandle[delta.JsonValue], error) {
	watch, err := c.OpenWatch(ctx)
	if err != nil {
		return nil, err
	}
	return watch, nil
}

// OpenWatch is as Source.OpenWatch; nothing is delivered, so nothing is reported. It never fails.
func (c *Constant) OpenWatch(ctx context.Context) (*chord.Watch[delta.JsonValue], error) {
	watch := chord.NewWatch(c.value, func() {}, func(error) {})
	if ctx.Done() != nil {
		watch.ObserveAbort(ctx)
	}
	return watch, nil
}

// ConstantOf is Pi's `constant(value)`: a watchable that never changes, typed as its value. It holds value as it was
// given.
func ConstantOf[T any](value T) chord.Watchable[T] { return &constantOf[T]{value: value} }

type constantOf[T any] struct{ value T }

func (c *constantOf[T]) Value() T { return c.value }

func (c *constantOf[T]) Watch(ctx context.Context) (chord.WatchHandle[T], error) {
	watch := chord.NewWatch(c.value, func() {}, func(error) {})
	if ctx.Done() != nil {
		watch.ObserveAbort(ctx)
	}
	return watch, nil
}
