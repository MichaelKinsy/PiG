package extensiontest

import (
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// MemBus is an in-memory implementation of [extension.EventBus] suitable
// for unit tests. Goroutine-safe: handlers may emit from any goroutine.
type MemBus struct {
	mu        sync.Mutex
	subs      map[string][]*memSub
	nextSubID int
}

type memSub struct {
	id      int
	handler func(data any)
}

// NewMemBus returns a fresh in-memory event bus.
func NewMemBus() *MemBus {
	return &MemBus{subs: map[string][]*memSub{}}
}

// Emit synchronously dispatches data to every listener of channel.
// Handlers run in registration order. Panics in handlers propagate to the
// caller: tests can choose whether to recover.
func (b *MemBus) Emit(channel string, data any) {
	b.mu.Lock()
	subs := append([]*memSub(nil), b.subs[channel]...)
	b.mu.Unlock()
	for _, s := range subs {
		s.handler(data)
	}
}

// On registers handler for channel and returns an unsubscribe function.
// Registering on the same channel multiple times produces independent
// listeners; removing one does not affect the others.
func (b *MemBus) On(channel string, handler func(data any)) func() {
	b.mu.Lock()
	b.nextSubID++
	id := b.nextSubID
	b.subs[channel] = append(b.subs[channel], &memSub{id: id, handler: handler})
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		list := b.subs[channel]
		for i, s := range list {
			if s.id == id {
				b.subs[channel] = append(list[:i], list[i+1:]...)
				return
			}
		}
	}
}

// Compile-time assertion.
var _ extension.EventBus = (*MemBus)(nil)
