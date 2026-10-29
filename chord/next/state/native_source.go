package state

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
)

// Ports packages/chord/src/next/state/native-source.ts (NativeObserver, NativeObservation, NativeStart, NativeSource,
// FollowedSource). Native sources (W4, slice 8 §2): in-process values that change, compared by identity (a writable, a
// registry's composed value, ref.state). Derives and registry tables depend on them and the batch marks them; the
// followed ones (writables, registry values) give their watches and observers one call per batch that changed them.

// NativeObserver is a synchronous observer of a native source (Durable's observer shape): Advance is called inside the
// batch that changed the source, so it only records; End once, when the source ended (ended) or the observer's context was
// aborted (aborted). An observer must be comparable (a pointer): a source tells them apart.
type NativeObserver[T any] interface {
	// Advance is the source's call of this batch: its new value, or a failure it may recover from.
	Advance(call chord.NativeWatchCall[T], ctx context.Context)
	// End is the observer is detached for good with end.
	End(end chord.WatchEnd)
}

// NativeObservation is what Observe gives: the value the observer starts at, and how to detach it (Durable's
// Observation).
type NativeObservation[T any] struct {
	Value  T
	Detach func()
}

// NativeStart is what a new watch of a native source starts at: its value (Kind StartValue, Value), or while it is
// failed, the failure (Kind StartFailed, Error) and its last good value Last, when HasLast (a watch cannot start without
// one). Pi's `last: { value } | undefined` is HasLast and Last.
type NativeStart[T any] struct {
	Kind    StartKind
	Value   T
	Error   error
	HasLast bool
	Last    T
}

// NativeHooks is what a NativeSource needs of the type that embeds it: Value is the embedder's own (Pi's abstract
// `value`); Start and Current are promoted from NativeSource unless the embedder defines them.
type NativeHooks[T any] interface {
	// Value is the source's value.
	Value() T
	// Start is what a new watch starts at; a source that can fail and recover tells its failure.
	Start() NativeStart[T]
	// Current is its value now as a contribution reads it: its failure while it is failed, where Value keeps the last
	// good value for readers.
	Current() (T, error)
}

// NativeSourceHandle is a native source of any value type as a contribution or a registry table holds it: the type-erased
// view of NativeSource.
type NativeSourceHandle interface {
	DeriveInput
	// CurrentAny is Current of the embedder, erased.
	CurrentAny() (any, error)
}

// NativeSource is a native source as derives and registry tables read it: its value (Start while it may fail), the
// dependents a change marks (Changed, inside a batch), and the host's batch until it ended. How its watches get their calls
// is the embedder's (FollowedSource for writables and registry values; ref.state gives each state to its watches). It is
// embedded by value and initialized with Init.
type NativeSource[T any] struct {
	hooks NativeHooks[T]
	// dependents is the derives and registry tables reading this source; each removes itself when it stops.
	dependents DependentSet
	// batch is the host's batch; dropped when the source ended, so an ended source keeps no host.
	batch *Batch
}

// Init makes the source of batch, with hooks its embedder; batch nil makes it ended (a released reference's state).
func (n *NativeSource[T]) Init(batch *Batch, hooks NativeHooks[T]) {
	n.batch = batch
	n.hooks = hooks
}

// DependentSet is the derives and registry tables reading this source.
func (n *NativeSource[T]) DependentSet() *DependentSet { return &n.dependents }

// Start is what a new watch starts at; a source that can fail and recover tells its failure. The default is the
// embedder's Value.
func (n *NativeSource[T]) Start() NativeStart[T] {
	return NativeStart[T]{Kind: StartValue, Value: n.hooks.Value()}
}

// Current is its value now as a contribution reads it: its failure while it is failed, where Value keeps the last good
// value for readers (a registry whose compose failed). The default is the embedder's Value.
func (n *NativeSource[T]) Current() (T, error) { return n.hooks.Value(), nil }

// CurrentAny is the embedder's Current, erased.
func (n *NativeSource[T]) CurrentAny() (any, error) { return n.hooks.Current() }

// DeriveValue is the value a derive reads, Pi's `.value`: the source's value, at its last good one while it is failed; a
// failed source that never had one returns the failure. It reads Start, so an embedder that overrides Start needs no
// Value for it.
func (n *NativeSource[T]) DeriveValue() (any, error) {
	start := n.hooks.Start()
	if start.Kind == StartValue {
		return start.Value, nil
	}
	if !start.HasLast {
		return nil, start.Error
	}
	return start.Last, nil
}

// Batch is the host's batch, nil once the source ended.
func (n *NativeSource[T]) Batch() *Batch { return n.batch }

// Changed records a change inside the current batch: every dependent marked. Pi's protected `changed`.
func (n *NativeSource[T]) Changed() {
	for _, dependent := range n.dependents.Snapshot() {
		dependent.Invalidate()
	}
}

// End ends the source for good: its dependents dropped, the batch let go. Pi's protected `end`.
func (n *NativeSource[T]) End() {
	n.batch = nil
	n.dependents.Clear()
}

// startValue is the value a watch starts at, or the failure when there is none to start at.
func startValue[T any](start NativeStart[T]) (T, error) {
	if start.Kind == StartValue {
		return start.Value, nil
	}
	if !start.HasLast {
		var zero T
		return zero, start.Error
	}
	return start.Last, nil
}

// shownKind is what the watches and observers were last given: a value, a failure (one call per failure episode), or
// nothing yet (a source with no value, followed for its first one).
type shownKind uint8

const (
	shownNone shownKind = iota
	shownValue
	shownFailed
)

type shown[T any] struct {
	kind  shownKind
	value T
}

// FollowedHooks is what a FollowedSource needs of the type that embeds it: NativeHooks, and the members an embedder
// overrides, all promoted from FollowedSource unless it defines them.
type FollowedHooks[T any] interface {
	NativeHooks[T]
	// Watched reports whether a watch or an observer follows it (a registry value also while readers wait for its first
	// value).
	Watched() bool
	// Unfollowed is nothing follows it any more (no watch, no observer): the embedder drops what it kept for followers.
	Unfollowed()
}

type observerEntry[T any] struct {
	observer NativeObserver[T]
	// cleanup removes what the observer registered (its abort listener); nil for none.
	cleanup func()
}

// FollowedSource is a native source its followers get through the batch (a writable, a registry value): a change lists
// it for one frame when someone follows it; the frame is its value then, delivered only when it is another value
// (identity) than the one its followers have, or after a failure, whose call comes once per episode.
//
// A watch ends itself, and an abort detaches an observer, from goroutines of their own: mu guards the followers, and
// nothing calls out while it is held.
type FollowedSource[T any] struct {
	NativeSource[T]
	impl FollowedHooks[T]
	mu   sync.Mutex
	// watches is what follows it through chord.NativeWatch.
	watches []*chord.NativeWatch[T]
	// observers are synchronous observers and Chord's own subscribers (retryOn), fed inside the batch, each with its cleanup.
	observers []*observerEntry[T]
	// hasShown and shown: what the followers have; none while nobody follows (so no old value is kept).
	hasShown bool
	shown    shown[T]
	// listed is set while listed for a frame in the current batch.
	listed bool
}

// Init makes the source of batch, with impl its embedder; batch nil makes it ended.
func (f *FollowedSource[T]) Init(batch *Batch, impl FollowedHooks[T]) {
	f.NativeSource.Init(batch, impl)
	f.impl = impl
}

// Watched reports whether a watch or an observer follows it.
func (f *FollowedSource[T]) Watched() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.watches) > 0 || len(f.observers) > 0
}

// Watch is Pi's `watch(context)`, which resolves with OpenWatch's result or rejects with its throw: the call returns the
// watch or the error, and never suspends.
func (f *FollowedSource[T]) Watch(ctx context.Context) (chord.NativeWatchHandle[T], error) {
	watch, err := f.OpenWatch(ctx)
	if err != nil {
		return nil, err
	}
	return watch, nil
}

// OpenWatch is Watch without the Promise. On a failed source it opens at the last good value and its first call is the
// failure; with no value to start at it returns the failure. An ended source gives a watch that has ended.
func (f *FollowedSource[T]) OpenWatch(ctx context.Context) (*chord.NativeWatch[T], error) {
	batch := f.batch
	if batch == nil {
		value, err := startValue(f.impl.Start())
		if err != nil {
			return nil, err
		}
		ended := chord.NewNativeWatch(value, func() {}, func(error) {}, nil)
		ended.EndWith(chord.WatchEnd{Kind: chord.WatchEnded})
		return ended, nil
	}
	if err := batch.AssertOutside("watch"); err != nil {
		return nil, err
	}
	start := f.impl.Start()
	value, err := startValue(start)
	if err != nil {
		return nil, err
	}
	f.follow(start.Kind, start.Value)
	var watch *chord.NativeWatch[T]
	watch = chord.NewNativeWatch(value, func() { f.unfollow(watch) }, batch.ReportListener, nil)
	if scheduler := batch.deliveries(); scheduler != nil {
		watch.On(scheduler)
	}
	f.mu.Lock()
	f.watches = append(f.watches, watch)
	f.mu.Unlock()
	if ctx.Done() != nil {
		watch.ObserveAbort(ctx)
	}
	if start.Kind == StartFailed {
		watch.Fail(ctx, HandedOut(start.Error))
	}
	return watch, nil
}

// Observe attaches a synchronous observer at the current value (Durable's observeXXX); it counts as following, so a lazy
// value is computed in the batch that changed it. It returns the failure when there is no value to start at; aborting ctx
// detaches it (aborted); the source's end detaches it too (ended), its abort listener removed.
func (f *FollowedSource[T]) Observe(observer NativeObserver[T], ctx context.Context) (NativeObservation[T], error) {
	if f.batch != nil {
		if err := f.batch.AssertOutside("observe"); err != nil {
			return NativeObservation[T]{}, err
		}
	}
	start := f.impl.Start()
	value, err := startValue(start)
	if err != nil {
		return NativeObservation[T]{}, err
	}
	if f.batch == nil {
		observer.End(chord.WatchEnd{Kind: chord.WatchEnded})
		return NativeObservation[T]{Value: value, Detach: func() {}}, nil
	}
	if ctx.Err() != nil {
		observer.End(chord.WatchEnd{Kind: chord.WatchAborted})
		return NativeObservation[T]{Value: value, Detach: func() {}}, nil
	}
	f.follow(start.Kind, start.Value)
	entry := &observerEntry[T]{observer: observer}
	f.mu.Lock()
	f.observers = append(f.observers, entry)
	f.mu.Unlock()
	if ctx.Done() != nil {
		stop := context.AfterFunc(ctx, func() {
			if f.removeObserver(entry) {
				observer.End(chord.WatchEnd{Kind: chord.WatchAborted})
			}
		})
		f.mu.Lock()
		present := f.hasObserver(entry)
		if present {
			entry.cleanup = func() { stop() }
		}
		f.mu.Unlock()
		if !present {
			stop()
		}
	}
	return NativeObservation[T]{Value: value, Detach: func() { f.Unsubscribe(observer) }}, nil
}

// Subscribe feeds observer every call from now on, inside each batch, also while the source has no value (Chord's own
// followers: retryOn). Nothing is delivered for the current value.
func (f *FollowedSource[T]) Subscribe(observer NativeObserver[T]) {
	if f.batch == nil {
		return
	}
	f.followOrNone()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, entry := range f.observers {
		if entry.observer == observer {
			entry.cleanup = nil
			return
		}
	}
	f.observers = append(f.observers, &observerEntry[T]{observer: observer})
}

// Unsubscribe stops feeding observer; the observer is not told.
func (f *FollowedSource[T]) Unsubscribe(observer NativeObserver[T]) {
	f.mu.Lock()
	var removed *observerEntry[T]
	for i, entry := range f.observers {
		if entry.observer == observer {
			removed = entry
			f.observers = append(append([]*observerEntry[T]{}, f.observers[:i]...), f.observers[i+1:]...)
			break
		}
	}
	f.mu.Unlock()
	if removed == nil {
		return
	}
	if removed.cleanup != nil {
		removed.cleanup()
	}
	f.settleFollowers()
}

// removeObserver detaches entry for an abort and reports whether it was attached.
func (f *FollowedSource[T]) removeObserver(entry *observerEntry[T]) bool {
	f.mu.Lock()
	present := f.hasObserver(entry)
	f.mu.Unlock()
	if !present {
		return false
	}
	f.Unsubscribe(entry.observer)
	return true
}

func (f *FollowedSource[T]) hasObserver(entry *observerEntry[T]) bool {
	for _, each := range f.observers {
		if each == entry {
			return true
		}
	}
	return false
}

// Deliver delivers this batch's frame: the new value when the followers have another, or a failure once per episode.
func (f *FollowedSource[T]) Deliver(ctx context.Context) error {
	f.mu.Lock()
	f.listed = false
	has, current := f.hasShown, f.shown
	f.mu.Unlock()
	if !has {
		return nil
	}
	f.deliver(current, ctx)
	// Readers waiting for a first value pulled it through this delivery: nothing is kept for them.
	f.settleFollowers()
	return nil
}

// Changed records a change inside the current batch: every dependent marked, and the source listed for a frame when someone
// follows it (or Watched says so).
func (f *FollowedSource[T]) Changed() {
	f.NativeSource.Changed()
	f.List()
}

// List lists the source for a frame in the current batch when someone follows it (or Watched says so), its dependents left
// as they are (marked already). Pi's protected `list`.
func (f *FollowedSource[T]) List() {
	if f.batch == nil {
		return
	}
	f.mu.Lock()
	listed := f.listed
	f.mu.Unlock()
	if listed || !f.impl.Watched() {
		return
	}
	f.mu.Lock()
	f.listed = true
	// Only readers waiting for a first value follow it without a follower's record: they have none. Not computed here:
	// marking runs inside adoptions, the value is pulled in the delivery.
	if !f.hasShown {
		f.hasShown, f.shown = true, shown[T]{kind: shownNone}
	}
	f.mu.Unlock()
	f.batch.Changed(f)
}

// Unfollowed is nothing follows it any more (no watch, no observer): an embedder drops what it kept for followers.
func (f *FollowedSource[T]) Unfollowed() {}

// End ends the source for good: watches and observers end ended (their abort listeners removed). Pi's protected `end`.
func (f *FollowedSource[T]) End() {
	f.NativeSource.End()
	f.mu.Lock()
	f.hasShown, f.shown = false, shown[T]{}
	watches := f.watches
	observers := f.observers
	f.observers = nil
	f.mu.Unlock()
	// Ending detaches, which removes the watch from the followers (safe over this copy) and runs no listener.
	for _, watch := range watches {
		watch.EndWith(chord.WatchEnd{Kind: chord.WatchEnded})
	}
	for _, entry := range observers {
		if entry.cleanup != nil {
			entry.cleanup()
		}
		entry.observer.End(chord.WatchEnd{Kind: chord.WatchEnded})
	}
}

func (f *FollowedSource[T]) deliver(current shown[T], ctx context.Context) {
	start, none := f.startOrNone()
	if !none && start.Kind == StartFailed {
		// A failure reaches followers that had a value: those without one wait for the first.
		if current.kind != shownValue {
			return
		}
		f.record(shown[T]{kind: shownFailed})
		err := HandedOut(start.Error)
		f.mu.Lock()
		watches, observers := f.watches, f.observerList()
		f.mu.Unlock()
		for _, watch := range watches {
			watch.Fail(ctx, err)
		}
		for _, entry := range observers {
			entry.observer.Advance(chord.NativeWatchCall[T]{Kind: chord.WatchCallFailed, Error: err}, ctx)
		}
		return
	}
	if none || (current.kind == shownValue && Identical(current.value, start.Value)) {
		return
	}
	value := start.Value
	f.record(shown[T]{kind: shownValue, value: value})
	f.mu.Lock()
	watches, observers := f.watches, f.observerList()
	f.mu.Unlock()
	for _, watch := range watches {
		watch.Advance(ctx, value)
	}
	// A copy: an observer may detach itself in its call.
	for _, entry := range observers {
		entry.observer.Advance(chord.NativeWatchCall[T]{Kind: chord.WatchCallValue, Value: value}, ctx)
	}
}

// observerList is a copy of the observers, under mu.
func (f *FollowedSource[T]) observerList() []*observerEntry[T] {
	return append([]*observerEntry[T](nil), f.observers...)
}

// record sets what the followers have, unless nobody follows any more.
func (f *FollowedSource[T]) record(current shown[T]) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hasShown {
		f.shown = current
	}
}

// startOrNone is its start, none while it has no value.
func (f *FollowedSource[T]) startOrNone() (NativeStart[T], bool) {
	start := f.impl.Start()
	if start.Kind == StartFailed && !start.HasLast {
		return NativeStart[T]{}, true
	}
	return start, false
}

// followOrNone is follow at its start, or at none while it has no value.
func (f *FollowedSource[T]) followOrNone() {
	start, none := f.startOrNone()
	if none {
		f.mu.Lock()
		defer f.mu.Unlock()
		if !f.hasShown {
			f.hasShown, f.shown = true, shown[T]{kind: shownNone}
		}
		return
	}
	f.follow(start.Kind, start.Value)
}

// follow is a follower comes: the first one records what followers have (a later one opened at the same current value).
func (f *FollowedSource[T]) follow(kind StartKind, value T) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hasShown {
		return
	}
	f.hasShown = true
	if kind == StartFailed {
		f.shown = shown[T]{kind: shownFailed}
		return
	}
	f.shown = shown[T]{kind: shownValue, value: value}
}

func (f *FollowedSource[T]) unfollow(watch *chord.NativeWatch[T]) {
	f.mu.Lock()
	for i, each := range f.watches {
		if each == watch {
			f.watches = append(append([]*chord.NativeWatch[T]{}, f.watches[:i]...), f.watches[i+1:]...)
			break
		}
	}
	f.mu.Unlock()
	f.settleFollowers()
}

// settleFollowers: no follower left: nothing is shown any more, so no old value is kept.
func (f *FollowedSource[T]) settleFollowers() {
	f.mu.Lock()
	if len(f.watches) > 0 || len(f.observers) > 0 {
		f.mu.Unlock()
		return
	}
	f.hasShown, f.shown = false, shown[T]{}
	f.mu.Unlock()
	f.impl.Unfollowed()
}
