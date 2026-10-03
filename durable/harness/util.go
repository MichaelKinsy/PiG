// Ports packages/durable/src/harness/util.ts.

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/MichaelKinsy/PiG/durable"
)

// Waiter is one pending wait registered with Waiters. It settles once: through Resolve, RejectAll, or cancellation of the context it was added with.
type Waiter[T any] struct {
	done  chan struct{}
	value T
	err   error
}

// Wait blocks until the waiter settles.
func (waiter *Waiter[T]) Wait() (T, error) {
	<-waiter.done
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

// Add registers a wait for key. A cancelled ctx fails it with the cancellation cause, at once when ctx is already done.
func (waiters *Waiters[K, T]) Add(ctx context.Context, key K) *Waiter[T] {
	waiter := &Waiter[T]{done: make(chan struct{})}
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
	waiters.mu.Lock()
	set := waiters.sets[key]
	if set != nil {
		waiters.deleteKeyLocked(key)
	}
	waiters.mu.Unlock()
	for _, waiter := range set {
		waiter.value = value
		close(waiter.done)
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

func closedError() error { return ErrClosed }

// jsonQuote is JSON.stringify of a string.
func jsonQuote(text string) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(text)
	return string(bytes.TrimSuffix(buffer.Bytes(), []byte("\n")))
}
