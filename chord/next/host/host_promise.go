package host

import (
	"context"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/internal/parking"
)

// Go-only helpers (no Pi file): the Promise stored and awaited by several parties, and the live-iterator Map of
// `keyed-members.ts` that the host files share (see DESIGN-go-layout.md section 4).

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
	//portlint:allow doubleclose the p.settled check above returns before this line inside a loop section, so done closes once; a Promise settles once
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

// thenWithContext is `awaitWithContext(promise, context)` (context/index.ts:138-153): callback gets the promise's result, or
// the abort reason (`context.Cause(ctx)`) once ctx is aborted first. Cancellation ends only this waiter; the promise goes
// on. A context that cannot be aborted waits on the promise alone.
//
// Pi orders the two events by when they happen. Its abort listener runs inside `abort()` and rejects the wait there, at
// once; the settlement reaches the wait through a reaction, a microtask. So an abort inside a section wins over a
// settlement of the same section (whichever came first in it), whose reaction has not run, and a reaction that ran decides
// for the result. The listener here is chordctx.OnAbort, which runs inside the cancel function: when that is a section of
// this loop it decides the wait at once and queues the continuation, and an abort from another goroutine is queued as an
// event of its own, at the time it is observed (Pi: an external abort signal fires in a task of its own). A source
// OnAbort cannot listen to (a deadline, a context of another package) is observed by context.AfterFunc on a goroutine of
// its own, that enters the loop when it can.
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
	// fired is set by the first of the abort and the settlement to decide the wait; its owner is the loop's sections.
	fired := false
	var stopListening, stopAfter func() bool
	// end is the wait's end: it keeps no registration on ctx.
	end := func() {
		stopListening()
		if stopAfter != nil {
			stopAfter()
		}
	}
	inSection := onLoopOf(p.loop)
	var exact bool
	stopListening, exact = chordctx.OnAbort(ctx, func(cause error) {
		if inSection != nil && inSection() {
			// Inside the cancelling section: the wait is decided here, before the settlement's reaction (a microtask, queued
			// earlier or later) can run; the continuation is a microtask queued at the abort.
			if fired {
				return
			}
			fired = true
			p.loop.Queue(func() {
				end()
				var zero T
				callback(zero, cause)
			})
			return
		}
		// From another goroutine: an event of its own, queued now, in order with the settlements queued before and after.
		p.loop.Queue(func() {
			if fired {
				return
			}
			fired = true
			end()
			var zero T
			callback(zero, cause)
		})
	})
	if !exact {
		stopAfter = context.AfterFunc(ctx, func() {
			p.loop.Run(func() {
				if fired {
					return
				}
				fired = true
				end()
				var zero T
				callback(zero, context.Cause(ctx))
			})
		})
	}
	p.then(func(value T, err error) {
		if fired {
			return
		}
		fired = true
		end()
		callback(value, err)
	})
}

// await blocks until the promise settled: Pi's `await`. It is called outside a section. ctx is the waiter's: a handler that
// waits parks (its call's section goes on) before it blocks.
func (p *keyedPromise[T]) await(ctx context.Context) (T, error) {
	parking.Park(ctx)
	parkedOn(p.done)
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

// keys is the keys as they are now, in order.
func (m *keyedMap[V]) keys() []string {
	keys := make([]string, 0, len(m.index))
	for _, node := range m.nodes {
		if !node.removed {
			keys = append(keys, node.key)
		}
	}
	return keys
}
