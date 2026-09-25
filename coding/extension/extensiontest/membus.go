package extensiontest

import (
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// MemBus is an in-memory implementation of [extension.EventBus] suitable
// for unit tests. Goroutine-safe: handlers may publish from any goroutine.
type MemBus struct {
	mu        sync.Mutex
	subs      map[string][]*memSub
	nextSubID int
}

type memSub struct {
	id      int
	handler func(payload any)
}

// NewMemBus returns a fresh in-memory event bus.
func NewMemBus() *MemBus {
	return &MemBus{subs: map[string][]*memSub{}}
}

// Publish synchronously dispatches payload to every subscriber of topic.
// Handlers run in registration order. Panics in handlers propagate to the
// caller: tests can choose whether to recover.
func (b *MemBus) Publish(topic string, payload any) {
	b.mu.Lock()
	subs := append([]*memSub(nil), b.subs[topic]...)
	b.mu.Unlock()
	for _, s := range subs {
		s.handler(payload)
	}
}

// Subscribe registers handler for topic and returns a cancel function.
// Subscribing to the same topic multiple times produces independent
// subscriptions; cancelling one does not affect the others.
func (b *MemBus) Subscribe(topic string, handler func(payload any)) func() {
	b.mu.Lock()
	b.nextSubID++
	id := b.nextSubID
	b.subs[topic] = append(b.subs[topic], &memSub{id: id, handler: handler})
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		list := b.subs[topic]
		for i, s := range list {
			if s.id == id {
				b.subs[topic] = append(list[:i], list[i+1:]...)
				return
			}
		}
	}
}

// Compile-time assertion.
var _ extension.EventBus = (*MemBus)(nil)
