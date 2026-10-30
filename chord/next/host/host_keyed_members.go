package host

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/host/keyed-members.ts
//
// Keyed members across attempts (slice 6a §1.2, §1.4, §12): the keys of a local object's keyed members that references
// pin (`ref.member.at(key)`, `ref.member.watch(key)`), one member source per member and key, as state members have.
// While an attempt is live a key follows that attempt's keyed member (the object's own slot, or the provider's member
// it re-exposes, rule 22: one tracker at the end); while the object is failed it keeps its last value and gets no
// frames; when a new attempt goes live it follows that attempt's member of the same name, its key loaded before.

// pinnedKey is a key of a keyed member, pinned: its source, and the release (idempotent, called in a section of the
// host's loop).
type pinnedKey struct {
	source  state.DeriveSource
	release func()
}

// following is a pinned key on one attempt's keyed member; foreign when the member is another object's (re-exposed).
type following struct {
	pin     pinnedKey
	foreign bool
}

// entryKind is where one pinned key stands: its first pin in flight (loading: abandon rejects its users' waits),
// following an attempt's member (bound), frozen while its object is failed or activating again (both with what a new
// attempt prepared to follow, next), dropped (its last user left, or its object failed or ended while it loaded), or
// ended with its object (err, for a pin whose wait resolved just before).
type entryKind uint8

const (
	entryLoading entryKind = iota
	entryBound
	entryFrozen
	entryDropped
	entryEnded
)

type entryState struct {
	kind    entryKind
	abandon func(err error)
	source  *memberSource
	// following is what a bound key follows.
	following *following
	next      *following
	err       *data.ChordError
}

// keyEntry is one pinned key of one keyed member: its users, the wait of its first pin, and where it stands.
type keyEntry struct {
	member string
	key    string
	users  int
	ready  *keyedPromise[struct{}]
	state  entryState
}

// freezeScope is which keys a failed object freezes: foreign takes only the keys of re-exposed members (no admitted
// commit of this object changes them), all every key.
type freezeScope uint8

const (
	freezeForeign freezeScope = iota
	freezeAll
)

// keyedMembers is the pinned keys of one object's keyed members, across its attempts; every pin below them released
// exactly once. Its methods run in sections of the host's loop, except pin, which blocks.
type keyedMembers struct {
	batch *state.Batch
	// scheduler is the delivery scheduler of batch, for the watches of the member sources.
	scheduler *chord.DeliveryScheduler
	// loop is the host's: the resumptions after an await are its sections.
	loop sectionLoop
	// ctx is the object's context: a key's first load runs under it, shared by every user.
	ctx     context.Context
	entries keyedMap[*keyedMap[*keyEntry]]
}

// newKeyedMembers makes the pinned keys of an object whose batch, delivery scheduler (nil for the process-wide one) and
// loop are the host's, and whose context is ctx.
func newKeyedMembers(batch *state.Batch, scheduler *chord.DeliveryScheduler, loop sectionLoop, ctx context.Context) *keyedMembers {
	return &keyedMembers{batch: batch, scheduler: scheduler, loop: loop, ctx: ctx}
}

// empty is whether no key is pinned.
func (k *keyedMembers) empty() bool { return k.entries.len() == 0 }

// pinResult is what a pin settled with.
type pinResult struct {
	pin pinnedKey
	err error
}

// pin pins key (a stored key) of member, which the live attempt has as keyed: its member source once the key is loaded;
// ctx ends only this caller's wait. It fails when the object fails or ends while the first pin loads. It blocks, so it
// is called outside a section: its two sections (the lookup, and the resumption after the key loaded) enter the loop
// themselves, and callers of one key resume in the order they waited.
func (k *keyedMembers) pin(member, key string, keyed *keyedBinding, ctx context.Context) (pinnedKey, error) {
	result := make(chan pinResult, 1)
	k.loop.Run(func() {
		entry, found := k.find(member, key)
		if !found {
			entry = k.create(member, key, keyed)
		}
		entry.users++
		entry.ready.thenWithContext(ctx, func(_ struct{}, err error) {
			if err != nil {
				k.release(entry)
				result <- pinResult{err: err}
				return
			}
			current := entry.state
			// Loaded, but its object ended before this wait resumed: nothing to pin (the entry is gone).
			if current.kind == entryEnded {
				result <- pinResult{err: current.err}
				return
			}
			// Loaded, and its object neither failed nor ended since (a failure while loading rejects `ready` first).
			released := false
			release := func() {
				k.loop.Run(func() {
					if released {
						return
					}
					released = true
					k.release(entry)
				})
			}
			result <- pinResult{pin: pinnedKey{source: current.source, release: release}}
		})
	})
	settled := <-result
	return settled.pin, settled.err
}

// prepare loads every pinned key on members (a new attempt's, while it activates, before it goes live); nil when no key
// is pinned (Pi's undefined: nothing to await). The promise rejects when a load fails; rebind or discard then takes
// what it pinned. Its keys load one after the other, in the order they were pinned.
func (k *keyedMembers) prepare(members *contracts.NamedMap[*keyedBinding], ctx context.Context) *keyedPromise[struct{}] {
	if k.entries.len() == 0 {
		return nil
	}
	done := newKeyedPromise[struct{}](k.loop)
	outer := k.entries.iterate()
	var inner *keyedIterator[*keyEntry]
	nextEntry := func() *keyEntry {
		for {
			if inner != nil {
				if entry, ok := inner.next(); ok {
					return entry
				}
				inner.close()
				inner = nil
			}
			byKey, ok := outer.next()
			if !ok {
				return nil
			}
			inner = byKey.iterate()
		}
	}
	finish := func(err error) {
		if inner != nil {
			inner.close()
		}
		outer.close()
		if err != nil {
			done.reject(err)
		} else {
			done.resolve(struct{}{})
		}
	}
	var advance func()
	advance = func() {
		for entry := nextEntry(); entry != nil; entry = nextEntry() {
			keyed, found := members.Get(entry.member)
			if !found || (entry.state.kind != entryBound && entry.state.kind != entryFrozen) {
				continue
			}
			pinOn(k.loop, keyed, entry.key, ctx).then(func(pin pinnedKey, err error) {
				if err != nil {
					finish(err)
					return
				}
				next := &following{pin: pin, foreign: keyed.kind == keyedReexposed}
				current := entry.state
				// Dropped while it loaded: the new pin goes at once.
				if current.kind != entryBound && current.kind != entryFrozen {
					next.pin.release()
				} else {
					if current.next != nil {
						current.next.pin.release()
					}
					current.next = next
					entry.state = current
				}
				advance()
			})
			return
		}
		finish(nil)
	}
	advance()
	return done
}

// rebind is the new attempt went live, inside a batch: every prepared key follows its new member (one frame).
func (k *keyedMembers) rebind() {
	k.each(false, func(entry *keyEntry) {
		current := entry.state
		if (current.kind != entryBound && current.kind != entryFrozen) || current.next == nil {
			return
		}
		current.source.bind(current.next.pin.source)
		if current.kind == entryBound {
			current.following.pin.release()
		}
		entry.state = entryState{kind: entryBound, source: current.source, following: current.next}
	})
}

// discard is the new attempt did not go live: what prepare pinned is released.
func (k *keyedMembers) discard() {
	k.each(false, func(entry *keyEntry) {
		current := entry.state
		if (current.kind != entryBound && current.kind != entryFrozen) || current.next == nil {
			return
		}
		current.next.pin.release()
		current.next = nil
		entry.state = current
	})
}

// freeze is the object failed live (err): the keys keep their values and release what they follow; foreign takes only the
// keys of re-exposed members (no admitted commit of this object changes them), all every key. A first pin still loading
// is given up at once (its users' waits reject with err; the pin, when it lands, is released).
func (k *keyedMembers) freeze(which freezeScope, err error) {
	k.each(true, func(entry *keyEntry) {
		current := entry.state
		if current.kind == entryLoading {
			k.abandon(entry, current, err)
			return
		}
		if current.kind != entryBound || (which == freezeForeign && !current.following.foreign) {
			return
		}
		current.source.freeze()
		current.following.pin.release()
		entry.state = entryState{kind: entryFrozen, source: current.source, next: current.next}
	})
}

// end is the object ended, inside a batch: every key ends with err and releases what it pins.
func (k *keyedMembers) end(err *data.ChordError) {
	k.each(false, func(entry *keyEntry) {
		current := entry.state
		switch current.kind {
		case entryLoading:
			current.abandon(err)
		case entryBound, entryFrozen:
			current.source.objectEnded(err)
		}
		k.releaseAll(entry)
		entry.state = entryState{kind: entryEnded, err: err}
	})
	k.entries.clear()
}

// each visits every entry in the order they were created, member by member, as a `for of` over the Map of Maps does: an
// entry added meanwhile is visited, one removed before its turn is not. snapshotKeys takes the keys of one member as they
// are when its turn comes (`[...byKey.values()]`).
func (k *keyedMembers) each(snapshotKeys bool, visit func(entry *keyEntry)) {
	outer := k.entries.iterate()
	defer outer.close()
	for {
		byKey, ok := outer.next()
		if !ok {
			return
		}
		if snapshotKeys {
			for _, entry := range byKey.values() {
				visit(entry)
			}
			continue
		}
		inner := byKey.iterate()
		for {
			entry, ok := inner.next()
			if !ok {
				break
			}
			visit(entry)
		}
		inner.close()
	}
}

func (k *keyedMembers) find(member, key string) (*keyEntry, bool) {
	byKey, found := k.entries.get(member)
	if !found {
		return nil, false
	}
	return byKey.get(key)
}

// create is a new entry, its first pin started under the object's context.
func (k *keyedMembers) create(member, key string, keyed *keyedBinding) *keyEntry {
	// Each user waits on ready; a rejection nobody waits for is not unhandled.
	ready := newKeyedPromise[struct{}](k.loop)
	entry := &keyEntry{
		member: member,
		key:    key,
		ready:  ready,
		state:  entryState{kind: entryLoading, abandon: ready.reject},
	}
	pinOn(k.loop, keyed, key, k.ctx).then(func(pin pinnedKey, err error) {
		if err != nil {
			if entry.state.kind == entryLoading {
				k.abandon(entry, entry.state, err)
			}
			return
		}
		// Given up meanwhile (its object failed or ended, or every user left): the late pin goes, once.
		if entry.state.kind != entryLoading {
			pin.release()
			return
		}
		entry.state = entryState{
			kind:      entryBound,
			source:    newMemberSource(pin.source, k.batch, k.scheduler),
			following: &following{pin: pin, foreign: keyed.kind == keyedReexposed},
		}
		ready.resolve(struct{}{})
	})
	byKey, found := k.entries.get(member)
	if !found {
		byKey = &keyedMap[*keyEntry]{}
		k.entries.set(member, byKey)
	}
	byKey.set(key, entry)
	return entry
}

// abandon gives up entry's first pin with err: dropped and forgotten, its users' waits rejected.
func (k *keyedMembers) abandon(entry *keyEntry, current entryState, err error) {
	entry.state = entryState{kind: entryDropped}
	k.forget(entry)
	current.abandon(err)
}

// release is one user of entry left; the last drops it.
func (k *keyedMembers) release(entry *keyEntry) {
	entry.users--
	if entry.users > 0 {
		return
	}
	k.forget(entry)
	k.releaseAll(entry)
}

// releaseAll drops entry: what it follows and will follow is released; its source keeps its value and follows nothing.
func (k *keyedMembers) releaseAll(entry *keyEntry) {
	current := entry.state
	entry.state = entryState{kind: entryDropped}
	if current.kind != entryBound && current.kind != entryFrozen {
		return
	}
	current.source.freeze()
	if current.kind == entryBound {
		current.following.pin.release()
	}
	if current.next != nil {
		current.next.pin.release()
	}
}

func (k *keyedMembers) forget(entry *keyEntry) {
	byKey, found := k.entries.get(entry.member)
	if !found {
		return
	}
	if current, found := byKey.get(entry.key); !found || current != entry {
		return
	}
	byKey.delete(entry.key)
	if byKey.len() == 0 {
		k.entries.delete(entry.member)
	}
}

// pinOn pins key on keyed: on the object's own slot it is pinned and loaded; on a re-exposed member it is pinned on the
// provider's member (whose own keys follow its attempts). The promise settles in a section of the loop; the slot's
// Pin runs in the caller's section, as the synchronous start of Pi's async function does.
func pinOn(loop sectionLoop, keyed *keyedBinding, key string, ctx context.Context) *keyedPromise[pinnedKey] {
	if keyed.kind == keyedReexposed {
		promise := newKeyedPromise[pinnedKey](loop)
		loop.Go(func() {
			pin, err := keyed.binding.pinKey(keyed.member, key, ctx)
			loop.Run(func() { promise.settle(pin, err) })
		})
		return promise
	}
	return pinned(loop, keyed.slot, key, ctx)
}

// pinned pins key on slot and loads it when it is not in memory; a failed load releases the pin.
func pinned(loop sectionLoop, slot *documents.KeyedSlot, key string, ctx context.Context) *keyedPromise[pinnedKey] {
	promise := newKeyedPromise[pinnedKey](loop)
	release, err := slot.Pin(key, ctx)
	if err != nil {
		promise.reject(err)
		return promise
	}
	loop.Go(func() {
		err := slot.Ensure(key, ctx)
		loop.Run(func() {
			if err != nil {
				release()
				promise.reject(err)
				return
			}
			source := slot.Source(key)
			// The slot dropped its sources while the key loaded (its object ended): no key is pinned any more, as Pi's
			// `ensure` would find it ended.
			if source == nil {
				release()
				promise.reject(slot.Owner().Released())
				return
			}
			promise.resolve(pinnedKey{source: source, release: release})
		})
	})
	return promise
}

// keyedPromise is the Go form of a Promise that several parties wait on (the `ready` of an entry, a pin in flight):
// settled once, in a section of the host's loop. Callbacks registered with then run as microtasks of that loop in the
// order they were registered, as Promise reactions do; await blocks outside a section until it settled.
type keyedPromise[T any] struct {
	loop      sectionLoop
	done      chan struct{}
	settled   bool
	value     T
	err       error
	callbacks []func(value T, err error)
}

func newKeyedPromise[T any](loop sectionLoop) *keyedPromise[T] {
	return &keyedPromise[T]{loop: loop, done: make(chan struct{})}
}

// settle settles the promise unless it is settled: its value, or its error.
func (p *keyedPromise[T]) settle(value T, err error) {
	if p.settled {
		return
	}
	p.settled, p.value, p.err = true, value, err
	close(p.done)
	callbacks := p.callbacks
	p.callbacks = nil
	for _, callback := range callbacks {
		p.loop.Queue(func() { callback(value, err) })
	}
}

func (p *keyedPromise[T]) resolve(value T) { p.settle(value, nil) }

func (p *keyedPromise[T]) reject(err error) {
	var zero T
	p.settle(zero, err)
}

// then runs callback as a microtask once the promise settled.
func (p *keyedPromise[T]) then(callback func(value T, err error)) {
	if !p.settled {
		p.callbacks = append(p.callbacks, callback)
		return
	}
	value, err := p.value, p.err
	p.loop.Queue(func() { callback(value, err) })
}

// thenWithContext is `awaitWithContext(promise, context)`: callback gets the promise's result, or the abort reason
// (`context.Cause(ctx)`) once ctx is aborted first. Cancellation ends only this waiter; the promise goes on. A context
// that cannot be aborted waits on the promise alone.
func (p *keyedPromise[T]) thenWithContext(ctx context.Context, callback func(value T, err error)) {
	if ctx.Done() == nil {
		p.then(callback)
		return
	}
	if ctx.Err() != nil {
		cause := context.Cause(ctx)
		p.loop.Queue(func() {
			var zero T
			callback(zero, cause)
		})
		return
	}
	fired := false
	stop := context.AfterFunc(ctx, func() {
		p.loop.Run(func() {
			if fired {
				return
			}
			fired = true
			var zero T
			callback(zero, context.Cause(ctx))
		})
	})
	p.then(func(value T, err error) {
		if fired {
			return
		}
		fired = true
		stop()
		callback(value, err)
	})
}

// await blocks until the promise settled: Pi's `await`. It is called outside a section.
func (p *keyedPromise[T]) await() (T, error) {
	<-p.done
	return p.value, p.err
}

// keyedNode is one entry of a keyedMap; removed when deleted, so a running iterator steps over it.
type keyedNode[V any] struct {
	key     string
	value   V
	removed bool
}

// keyedMap is the Go form of a JavaScript Map keyed by string, whose iterators are live: an entry added while one
// iterates is visited, one deleted before its turn is not. Entries keep their insertion order. The zero value is empty.
type keyedMap[V any] struct {
	nodes     []*keyedNode[V]
	index     map[string]*keyedNode[V]
	iterators int
}

func (m *keyedMap[V]) len() int { return len(m.index) }

func (m *keyedMap[V]) get(key string) (V, bool) {
	node, found := m.index[key]
	if !found {
		var zero V
		return zero, false
	}
	return node.value, true
}

// set sets key to value; a new key goes last, an existing one keeps its place.
func (m *keyedMap[V]) set(key string, value V) {
	if node, found := m.index[key]; found {
		node.value = value
		return
	}
	if m.index == nil {
		m.index = map[string]*keyedNode[V]{}
	}
	node := &keyedNode[V]{key: key, value: value}
	m.nodes = append(m.nodes, node)
	m.index[key] = node
}

func (m *keyedMap[V]) delete(key string) {
	node, found := m.index[key]
	if !found {
		return
	}
	node.removed = true
	delete(m.index, key)
	m.compact()
}

// clear removes every entry; an iterator running goes on with what is added after.
func (m *keyedMap[V]) clear() {
	for _, node := range m.nodes {
		node.removed = true
	}
	clear(m.index)
	m.compact()
}

// values is the values as they are now, in order.
func (m *keyedMap[V]) values() []V {
	values := make([]V, 0, len(m.index))
	for _, node := range m.nodes {
		if !node.removed {
			values = append(values, node.value)
		}
	}
	return values
}

// compact drops the removed entries when no iterator is running and they are most of the list.
func (m *keyedMap[V]) compact() {
	if m.iterators > 0 || len(m.nodes) < 2*len(m.index)+16 {
		return
	}
	live := make([]*keyedNode[V], 0, len(m.index))
	for _, node := range m.nodes {
		if !node.removed {
			live = append(live, node)
		}
	}
	m.nodes = live
}

// iterate starts a live iterator; close it when it is dropped before the end.
func (m *keyedMap[V]) iterate() *keyedIterator[V] {
	m.iterators++
	return &keyedIterator[V]{m: m}
}

// keyedIterator is a live iterator of a keyedMap.
type keyedIterator[V any] struct {
	m      *keyedMap[V]
	at     int
	closed bool
}

// next is the next value that is still in the map; false at the end, which closes the iterator.
func (it *keyedIterator[V]) next() (V, bool) {
	for !it.closed && it.at < len(it.m.nodes) {
		node := it.m.nodes[it.at]
		it.at++
		if !node.removed {
			return node.value, true
		}
	}
	it.close()
	var zero V
	return zero, false
}

func (it *keyedIterator[V]) close() {
	if it.closed {
		return
	}
	it.closed = true
	it.m.iterators--
	it.m.compact()
}
