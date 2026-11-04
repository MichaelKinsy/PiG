package nexttest

import (
	"context"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
)

// Ports packages/chord/test/next/support.ts (framesOf, changesOf, valuesOf, flushed).

// Collector gathers what a watch's listener, which runs on a delivery goroutine, was called with. Items is safe to call
// while the listener still runs; the TypeScript arrays the helpers return are read by the one event loop, the Go ones
// under a mutex.
type Collector[E any] struct {
	mu    sync.Mutex
	items []E
}

// Append adds item and returns how many items were collected including it.
func (collector *Collector[E]) Append(item E) int {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	collector.items = append(collector.items, item)
	return len(collector.items)
}

// Items returns a copy of what was collected so far, in order; never nil.
func (collector *Collector[E]) Items() []E {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	return append([]E{}, collector.items...)
}

// Len is the number of items collected so far.
func (collector *Collector[E]) Len() int {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	return len(collector.items)
}

// FramesOf starts watch and collects its value frames as Frame{Value, Ops}; failed calls are not collected.
func FramesOf[T any](watch chord.WatchHandle[T]) *Collector[Frame[T]] {
	frames := &Collector[Frame[T]]{}
	watch.Start(func(_ context.Context, call chord.WatchCall[T]) error {
		if call.Kind == chord.WatchCallValue {
			frames.Append(Frame[T]{Value: call.Value, Ops: call.Ops})
		}
		return nil
	})
	return frames
}

// ChangesOf starts watch and collects every call it gets: values (the Retired flag included) and failures.
func ChangesOf[T any](watch chord.WatchHandle[T]) *Collector[chord.WatchCall[T]] {
	calls := &Collector[chord.WatchCall[T]]{}
	watch.Start(func(_ context.Context, call chord.WatchCall[T]) error {
		calls.Append(call)
		return nil
	})
	return calls
}

// FailedValue is a failed call of ValuesOf: upstream's `{ failed: message }`.
type FailedValue struct{ Failed string }

// ValuesOf returns the values of calls, a failure as FailedValue{error message}, for short expectations. The elements
// are T or FailedValue.
func ValuesOf[T any](calls []chord.WatchCall[T]) []any {
	values := make([]any, len(calls))
	for i, call := range calls {
		if call.Kind == chord.WatchCallValue {
			values[i] = call.Value
		} else {
			values[i] = FailedValue{Failed: call.Error.Error()}
		}
	}
	return values
}

// Flushed waits until every pending delivery callback has run; upstream waits one setImmediate turn after the promise
// callbacks, here a Delivered pause on top of one extra millisecond for deliveries queued by those callbacks.
func Flushed() {
	Delivered()
	time.Sleep(time.Millisecond)
}
