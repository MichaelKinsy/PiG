// Package changeobservers is the list of callbacks an owner of replicated state runs after the state changes, so the extension host can push it to the runtimes that replicate it.
package changeobservers

import (
	"slices"
	"sync"
	"sync/atomic"
)

// observer is one registered Observers callback.
type observer struct {
	id      uint64
	observe func()
}

// Observers is a set of callbacks that run after a change to the state their owner holds. A registration replaces the list and never edits it, so Notify reads it without a lock and without allocating, from any goroutine and while the owner holds its own locks.
type Observers struct {
	mu   sync.Mutex
	next uint64
	list atomic.Pointer[[]observer]
}

// Add registers observe and returns the function that removes it, which can be called repeatedly.
func (o *Observers) Add(observe func()) func() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.next++
	id := o.next
	var current []observer
	if list := o.list.Load(); list != nil {
		current = *list
	}
	next := append(slices.Clone(current), observer{id: id, observe: observe})
	o.list.Store(&next)
	return func() {
		o.mu.Lock()
		defer o.mu.Unlock()
		var current []observer
		if list := o.list.Load(); list != nil {
			current = *list
		}
		next := slices.DeleteFunc(slices.Clone(current), func(registered observer) bool { return registered.id == id })
		o.list.Store(&next)
	}
}

// Notify runs the observers registered now, in registration order, on the caller's goroutine.
func (o *Observers) Notify() {
	list := o.list.Load()
	if list == nil {
		return
	}
	for _, registered := range *list {
		registered.observe()
	}
}
