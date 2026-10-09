package extension

import (
	"fmt"
	"os"
	"slices"
	"sync"
)

// EventBus is the extension-to-extension event bus. Mirrors upstream's
// core/event-bus.ts EventBus, exposed on ExtensionAPI as the property
// `events: EventBus`, with upstream's method names.
//
// Go exposes upstream's `events` property through [API.Events].
//
// upstream: event-bus.ts:3-6
type EventBus interface {
	// Emit broadcasts data on channel to every current listener. Synchronous:
	// handlers run before Emit returns. A handler's failure does not reach the
	// emitter or the other listeners; the host reports it as
	// `Event handler error (<channel>):` (event-bus.ts:19-23).
	Emit(channel string, data any)

	// On registers handler for channel and returns an idempotent function that
	// removes it. Handlers are dispatched in registration order.
	On(channel string, handler func(data any)) (unsubscribe func())
}

// EventBusController is an [EventBus] its owner can also clear.
//
// upstream: event-bus.ts:8-10
type EventBusController interface {
	EventBus

	// Clear removes every listener of every channel (EventEmitter.removeAllListeners).
	Clear()
}

// CreateEventBus returns an in-memory [EventBusController]. Emit snapshots the channel's listeners, so a handler that subscribes or unsubscribes during an emit does not change that emit's recipients. A handler that panics is reported on stderr as `Event handler error (<channel>): <value>` and neither reaches the emitter nor stops the remaining listeners.
//
// upstream: event-bus.ts:12-33 (createEventBus)
func CreateEventBus() EventBusController {
	return &eventBus{listeners: map[string][]*eventBusListener{}}
}

type eventBusListener struct {
	handler func(data any)
}

type eventBus struct {
	mu        sync.Mutex
	listeners map[string][]*eventBusListener
}

func (b *eventBus) Emit(channel string, data any) {
	b.mu.Lock()
	snapshot := slices.Clone(b.listeners[channel])
	b.mu.Unlock()
	for _, listener := range snapshot {
		listener.handler(data)
	}
}

func (b *eventBus) On(channel string, handler func(data any)) func() {
	listener := &eventBusListener{handler: func(data any) {
		defer func() {
			if recovered := recover(); recovered != nil {
				fmt.Fprintf(os.Stderr, "Event handler error (%s): %v\n", channel, recovered)
			}
		}()
		handler(data)
	}}
	b.mu.Lock()
	b.listeners[channel] = append(b.listeners[channel], listener)
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		list := b.listeners[channel]
		if i := slices.Index(list, listener); i >= 0 {
			// A fresh slice keeps an in-flight Emit's snapshot independent of this removal.
			b.listeners[channel] = slices.Delete(slices.Clone(list), i, i+1)
		}
	}
}

func (b *eventBus) Clear() {
	b.mu.Lock()
	b.listeners = map[string][]*eventBusListener{}
	b.mu.Unlock()
}
