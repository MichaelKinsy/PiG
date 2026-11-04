// Ports packages/durable/src/harness/util.ts.

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"

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
type Waiters[K comparable, T any] struct {
	mu   sync.Mutex
	keys []K
	sets map[K][]*Waiter[T]
}

// Add registers a wait for key, which the caller then blocks in through Wait; line is the Session whose publications resume it
// in order. A cancelled ctx fails it with the cancellation cause, at once when ctx is already done.
func (waiters *Waiters[K, T]) Add(ctx context.Context, line *session.SessionImpl, key K) *Waiter[T] {
	waiter := &Waiter[T]{done: make(chan struct{}), line: line, ctx: ctx}
	if ctx.Err() != nil {
		waiter.err = context.Cause(ctx)
		close(waiter.done)
		return waiter
	}
	waiters.mu.Lock()
	if waiters.sets == nil {
		waiters.sets = map[K][]*Waiter[T]{}
	}
	if _, exists := waiters.sets[key]; !exists {
		waiters.keys = append(waiters.keys, key)
	}
	waiters.sets[key] = append(waiters.sets[key], waiter)
	waiters.mu.Unlock()
	stop := context.AfterFunc(ctx, func() {
		waiters.mu.Lock()
		set := waiters.sets[key]
		index := -1
		for i, candidate := range set {
			if candidate == waiter {
				index = i
				break
			}
		}
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
		waiter.err = context.Cause(ctx)
		close(waiter.done)
	})
	go func() {
		<-waiter.done
		stop()
	}()
	return waiter
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

// Keys returns the keys with pending waits.
func (waiters *Waiters[K, T]) Keys() []K {
	waiters.mu.Lock()
	defer waiters.mu.Unlock()
	return append([]K(nil), waiters.keys...)
}

// Resolve settles every wait of key with value.
func (waiters *Waiters[K, T]) Resolve(key K, value T) {
	waiters.Settle(key, value)()
}

// Settle takes the waits of key out of the pending set and fixes their result to value, as resolving a promise does, and
// returns the function that wakes them. Until it is called the waits are settled but their goroutines still sleep: a
// RejectAll or a cancelled context in between no longer reaches them, so a close that seals before the wake cannot turn
// the result into an error.
func (waiters *Waiters[K, T]) Settle(key K, value T) (wake func()) {
	waiters.mu.Lock()
	set := waiters.sets[key]
	if set != nil {
		waiters.deleteKeyLocked(key)
	}
	waiters.mu.Unlock()
	for _, waiter := range set {
		waiter.value = value
	}
	return func() {
		for _, waiter := range set {
			close(waiter.done)
		}
	}
}

// RejectAll fails every pending wait with err.
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
		waiter.err = err
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
