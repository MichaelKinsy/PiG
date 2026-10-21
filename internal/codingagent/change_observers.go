package codingagent

import (
	"slices"
	"sync"
	"sync/atomic"
)

// changeObserver is one registered ChangeObservers callback.
type changeObserver struct {
	id      uint64
	observe func()
}

// ChangeObservers is a set of callbacks that run after a change to the state their owner holds. A registration replaces the list and never edits it, so Notify reads it without a lock and without allocating, from any goroutine and while the owner holds its own locks.
type ChangeObservers struct {
	mu   sync.Mutex
	next uint64
	list atomic.Pointer[[]changeObserver]
}

// Add registers observe and returns the function that removes it, which can be called repeatedly.
func (o *ChangeObservers) Add(observe func()) func() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.next++
	id := o.next
	var current []changeObserver
	if list := o.list.Load(); list != nil {
		current = *list
	}
	next := append(slices.Clone(current), changeObserver{id: id, observe: observe})
	o.list.Store(&next)
	return func() {
		o.mu.Lock()
		defer o.mu.Unlock()
		var current []changeObserver
		if list := o.list.Load(); list != nil {
			current = *list
		}
		next := slices.DeleteFunc(slices.Clone(current), func(observer changeObserver) bool { return observer.id == id })
		o.list.Store(&next)
	}
}

// Notify runs the observers registered now, in registration order, on the caller's goroutine.
func (o *ChangeObservers) Notify() {
	list := o.list.Load()
	if list == nil {
		return
	}
	for _, observer := range *list {
		observer.observe()
	}
}
