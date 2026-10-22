package sdk

import (
	"fmt"
	"os"
	"runtime/debug"
	"slices"
	"sync"
)

// stateFeed delivers each change of a value the host replicates (the theme, the tool-expansion state) to the handlers an extension subscribed. Upstream Pi calls a widget component's invalidate() on such a change, and the component reads the new value at its next render; a subprocess extension has no component to invalidate, so it subscribes.
//
// Handlers run on a worker of their own, one delivery at a time in the order the changes arrived, never on the message loop or the goroutine that reads the host's replies, so a handler can make a blocking host call. A handler that panics is recovered and does not stop the others. An unsubscribe changes only later deliveries: a delivery already queued still reaches the handlers subscribed when the change arrived.
type stateFeed[T any] struct {
	what string

	mu      sync.Mutex
	nextID  uint64
	subs    []feedSub[T]
	queue   []feedDelivery[T]
	running bool
	stopped bool
}

type feedSub[T any] struct {
	id      uint64
	handler func(Context, T)
}

type feedDelivery[T any] struct {
	value T
	subs  []feedSub[T]
}

// subscribe adds handler and returns its idempotent unsubscribe.
func (f *stateFeed[T]) subscribe(handler func(Context, T)) func() {
	f.mu.Lock()
	f.nextID++
	id := f.nextID
	f.subs = append(f.subs, feedSub[T]{id: id, handler: handler})
	f.mu.Unlock()
	return sync.OnceFunc(func() {
		f.mu.Lock()
		f.subs = slices.DeleteFunc(f.subs, func(s feedSub[T]) bool { return s.id == id })
		f.mu.Unlock()
	})
}

// publish queues value for the handlers subscribed now.
func (f *stateFeed[T]) publish(e *Extension, value T) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped || len(f.subs) == 0 {
		return
	}
	f.queue = append(f.queue, feedDelivery[T]{value: value, subs: slices.Clone(f.subs)})
	if f.running {
		return
	}
	f.running = true
	e.requestWG.Go(func() { f.run(e) })
}

func (f *stateFeed[T]) run(e *Extension) {
	ctx := Context{ext: e}
	for {
		f.mu.Lock()
		if len(f.queue) == 0 || f.stopped {
			f.queue = nil
			f.running = false
			f.mu.Unlock()
			return
		}
		delivery := f.queue[0]
		f.queue = f.queue[1:]
		f.mu.Unlock()
		for _, sub := range delivery.subs {
			f.call(ctx, sub.handler, delivery.value)
		}
	}
}

func (f *stateFeed[T]) call(ctx Context, handler func(Context, T), value T) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "extension: recovered from panic in a %s change handler: %v\n%s\n", f.what, r, debug.Stack())
		}
	}()
	handler(ctx, value)
}

// stop drops the queued deliveries; the running handler finishes, and stopRequests waits for it.
func (f *stateFeed[T]) stop() {
	f.mu.Lock()
	f.stopped = true
	f.queue = nil
	f.mu.Unlock()
}
