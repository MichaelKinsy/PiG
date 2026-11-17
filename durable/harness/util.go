// Ports packages/durable/src/harness/util.ts.

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// Waiter is one pending wait registered with Waiters. It settles once: through Resolve, RejectAll, or cancellation of the context it was added with.
type Waiter[T any] struct {
	done  chan struct{}
	value T
	err   error
	line  *session.SessionImpl
	ctx   context.Context
	// sampled is set when ctx can also be aborted by a context chordctx.OnAbort cannot listen to: the abort of such a context
	// reaches the wait on a goroutine of its own, so Settle, RejectAll and Keys sample ctx.Err(). Set before the wait is in the set.
	sampled bool
	// added is set once the wait is in the set; aborted is the cause of an abort that came before that. Both are guarded by the
	// mu of the Waiters.
	added   bool
	aborted error
	// parked is the wait registered with the Session before it blocks, so the publication that settles it waits for the
	// goroutine to run again (Session.Park); nil until Park, or when the waiter was settled at once.
	parked *session.Parked
}

// Park registers the wait with the Session. Wait does it when it was not done; a caller that must be registered before it
// releases a lock the settle needs calls it first. It must not be called inside a Session line job: that job's outcome is
// delivered after the publication that waits for the registered wait to resume.
func (waiter *Waiter[T]) Park() {
	if waiter.parked == nil {
		waiter.parked = waiter.line.Park(waiter.ctx, waiter.done)
	}
}

// Wait blocks until the waiter settles, then acknowledges the wait to the Session: the goroutine runs again.
func (waiter *Waiter[T]) Wait() (T, error) {
	waiter.Park()
	<-waiter.done
	if waiter.err != nil {
		// Rejected or cancelled, not woken by a publication: no continuation of it is ordered.
		waiter.parked.Abandon()
	} else {
		waiter.parked.Resume()
	}
	return waiter.value, waiter.err
}

// Done is closed once the waiter settles.
func (waiter *Waiter[T]) Done() <-chan struct{} { return waiter.done }

// Waiters holds pending waits by key. Keys keep their first-registration order.
//
// Lock order: the abort listener of a wait (abort) runs inside the cancel function of the context that aborts, on the goroutine
// that cancels, with every lock that goroutine holds: the scheduler's mutex when a Join or the end of an invocation cancels,
// whatever the Session holds while it runs the close listeners when seal does. The listener takes only mu, and mu is a leaf:
// nothing is called and no other lock is taken while it is held (a waiter's error is written and its channel closed after it
// is released), and chord/context's own locks are leaves in the same way. So the order is always: a canceller's locks, then
// mu, and never the other way.
type Waiters[K comparable, T any] struct {
	mu   sync.Mutex
	keys []K
	sets map[K][]*Waiter[T]
}

// Add registers a wait for key, which the caller then blocks in through Wait; line is the Session whose publications resume it
// in order. A cancelled ctx fails it with the cancellation cause, at once when ctx is already done.
//
// The wait listens for the abort of ctx with chordctx.OnAbort, which runs inside the cancel function that aborts ctx, as Pi's
// listener runs inside abort(): when that cancel returns the wait is out of the set and rejected with the cause. For a ctx
// that can also be aborted by a context OnAbort cannot listen to it also registers context.AfterFunc, and Settle, RejectAll
// and Keys sample ctx.Err() for it (see OnAbort).
func (waiters *Waiters[K, T]) Add(ctx context.Context, line *session.SessionImpl, key K) *Waiter[T] {
	waiter := &Waiter[T]{done: make(chan struct{}), line: line, ctx: ctx}
	if ctx.Err() != nil {
		waiter.err = context.Cause(ctx)
		close(waiter.done)
		return waiter
	}
	// Registered before the wait is in the set, outside waiters.mu: the listener runs at once when ctx was aborted since the
	// check above, and takes waiters.mu. An abort that finds the wait not in the set yet leaves its cause on the wait.
	stopListening, exact := chordctx.OnAbort(ctx, func(cause error) { waiters.abort(key, waiter, cause) })
	var stopAfter func() bool
	if !exact {
		waiter.sampled = true
		stopAfter = context.AfterFunc(ctx, func() { waiters.abort(key, waiter, context.Cause(ctx)) })
	}
	waiters.mu.Lock()
	if waiter.aborted != nil {
		waiters.mu.Unlock()
		waiter.err = waiter.aborted
		close(waiter.done)
		stopListening()
		if stopAfter != nil {
			stopAfter()
		}
		return waiter
	}
	if waiters.sets == nil {
		waiters.sets = map[K][]*Waiter[T]{}
	}
	if _, exists := waiters.sets[key]; !exists {
		waiters.keys = append(waiters.keys, key)
	}
	waiters.sets[key] = append(waiters.sets[key], waiter)
	waiter.added = true
	waiters.mu.Unlock()
	go func() {
		<-waiter.done
		stopListening()
		if stopAfter != nil {
			stopAfter()
		}
	}()
	return waiter
}

// abort is the abort of a wait's context: it takes the wait out of the set and rejects it with the cause, unless a settle or a
// RejectAll took it first. It runs inside a cancel function and takes only waiters.mu, which is a leaf: nothing is called
// while it is held. A wait that is not in the set yet (Add registers the listener first) keeps the cause for Add to reject with.
func (waiters *Waiters[K, T]) abort(key K, waiter *Waiter[T], cause error) {
	waiters.mu.Lock()
	if !waiter.added {
		if waiter.aborted == nil {
			waiter.aborted = cause
		}
		waiters.mu.Unlock()
		return
	}
	set := waiters.sets[key]
	index := slices.Index(set, waiter)
	if index < 0 {
		waiters.mu.Unlock()
		return
	}
	set = append(set[:index:index], set[index+1:]...)
	if len(set) == 0 {
		waiters.deleteKeyLocked(key)
	} else {
		waiters.sets[key] = set
	}
	waiters.mu.Unlock()
	waiter.err = cause
	close(waiter.done)
}

func (waiters *Waiters[K, T]) deleteKeyLocked(key K) {
	delete(waiters.sets, key)
	for i, candidate := range waiters.keys {
		if candidate == key {
			waiters.keys = append(waiters.keys[:i:i], waiters.keys[i+1:]...)
			break
		}
	}
}

// Keys returns the keys with pending waits. A key whose waits were all aborted is not one: Pi's abort listener took them out at
// the abort (util.ts Waiters.add). The waits of a context OnAbort could not listen to are tested with an ctx.Err() sample, as
// Settle tests them: their listener may not have run yet.
func (waiters *Waiters[K, T]) Keys() []K {
	waiters.mu.Lock()
	keys := append([]K(nil), waiters.keys...)
	var doubtful map[int][]*Waiter[T]
	for i, key := range keys {
		set := waiters.sets[key]
		if slices.ContainsFunc(set, func(waiter *Waiter[T]) bool { return !waiter.sampled }) {
			continue
		}
		if doubtful == nil {
			doubtful = map[int][]*Waiter[T]{}
		}
		doubtful[i] = append([]*Waiter[T](nil), set...)
	}
	waiters.mu.Unlock()
	if doubtful == nil {
		return keys
	}
	live := keys[:0]
	for i, key := range keys {
		if set, sampled := doubtful[i]; sampled && !slices.ContainsFunc(set, func(waiter *Waiter[T]) bool { return waiter.ctx.Err() == nil }) {
			continue
		}
		live = append(live, key)
	}
	return live
}

// Resolve settles every wait of key with value.
func (waiters *Waiters[K, T]) Resolve(key K, value T) {
	waiters.Settle(key, value)()
}

// Settle takes the waits of key out of the pending set and fixes their result to value, as resolving a promise does, and
// returns the function that wakes them. Until it is called the waits are settled but their goroutines still sleep: a
// RejectAll or a cancelled context in between no longer reaches them, so a close that seals before the wake cannot turn
// the result into an error. Pi runs the abort listener at the abort itself (util.ts Waiters.add), so a wait whose context
// was aborted is gone from the set and rejected: here chordctx.OnAbort is that listener for every abort a cancel function of
// chord/context makes, and it has run when that cancel returns, so such a wait is never in the set. A wait whose context
// OnAbort could not listen to (an embedder's own, a deadline) is cancelled on a goroutine of its own, which may not have run
// yet: that wait fails with the cancellation cause, not with value, by the ctx.Err() sample (the rule OnAbort states).
func (waiters *Waiters[K, T]) Settle(key K, value T) (wake func()) {
	waiters.mu.Lock()
	set := waiters.sets[key]
	if set != nil {
		waiters.deleteKeyLocked(key)
	}
	waiters.mu.Unlock()
	for _, waiter := range set {
		if waiter.sampled && waiter.ctx.Err() != nil {
			waiter.err = context.Cause(waiter.ctx)
			continue
		}
		waiter.value = value
	}
	return func() {
		for _, waiter := range set {
			close(waiter.done)
		}
	}
}

// RejectAll fails every pending wait with err, except a wait whose context was aborted already: Pi's abort listener
// rejected it at the abort, and the first rejection wins. A wait whose context OnAbort could not listen to fails with its
// cancellation cause when its context is cancelled, as Settle decides.
func (waiters *Waiters[K, T]) RejectAll(err error) {
	waiters.mu.Lock()
	var all []*Waiter[T]
	for _, key := range waiters.keys {
		all = append(all, waiters.sets[key]...)
	}
	waiters.keys = nil
	waiters.sets = nil
	waiters.mu.Unlock()
	for _, waiter := range all {
		if waiter.sampled && waiter.ctx.Err() != nil {
			waiter.err = context.Cause(waiter.ctx)
		} else {
			waiter.err = err
		}
		close(waiter.done)
	}
}

// ScanAll returns every item of a paginated scan, in page order.
func ScanAll[T any](scan func(cursor durable.Cursor) (durable.Page[T, durable.Cursor], error)) ([]T, error) {
	var items []T
	var cursor durable.Cursor
	for {
		page, err := scan(cursor)
		if err != nil {
			return nil, err
		}
		items = append(items, page.Items...)
		if page.Next == nil {
			return items, nil
		}
		cursor = *page.Next
	}
}

// ErrClosed is the error of an operation on a closed Harness.
var ErrClosed = errors.New("Harness is closed")

// failureSource is a Session that can say it failed.
type failureSource interface{ Failure() error }

// closedError is what a closed Harness rejects with: SessionFailed with the cause when it closed because the Session failed
// (util.ts closedError).
func closedError(session failureSource) error {
	if failure := session.Failure(); failure != nil {
		return durable.NewSessionFailed(failure)
	}
	return ErrClosed
}

// jsonQuote is JSON.stringify of a string.
func jsonQuote(text string) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(text)
	return string(bytes.TrimSuffix(buffer.Bytes(), []byte("\n")))
}

// conversationEntries is the scan of conversation conversationId's history that query asks for, without fields an
// untyped caller added: only the query's bounds and order are kept.
func conversationEntries(conversationId durable.ConversationId, query durable.EntryQuery) durable.EntryQuery {
	return durable.EntryQuery{ConversationId: conversationId, MinEntryId: query.MinEntryId, MaxEntryId: query.MaxEntryId, Order: query.Order}
}
