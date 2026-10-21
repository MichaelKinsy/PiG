package jsloop

import (
	"sync"
	"sync/atomic"
)

// Lane keeps the calls of one destination in source order up to the point each issues its request. Pi runs the synchronous prefix of every call of `Promise.all(xs.map(f))` in source order, so f(a) issues its request before f(b). Go starts a goroutine per call and the scheduler may run the later one first, so the caller reserves each call's place in source order before it starts any goroutine, and a call issues its request only after every earlier place was released.
//
// The zero value is ready to use.
type Lane struct {
	mu   sync.Mutex
	tail *Place
}

// Place is one call's reservation in a Lane. A nil *Place is no reservation: Wait, Release and Pass do nothing.
//
// A place is one allocation and makes no channel unless a caller has to block on it: the next place's Wait finds an already released place with an atomic load. Each place holds the place it waits for only until it has waited or been released, so a long-lived lane keeps no chain of places.
type Place struct {
	// previous is the place this one waits for, nil once it waited, was released, or had none to wait for.
	previous atomic.Pointer[Place]
	released atomic.Bool
	mu       sync.Mutex
	// wake is made by the first caller that has to block on this place and closed by Release; guarded by mu.
	wake chan struct{}
}

// Reserve takes the next place in the lane. It must run in source order, before any of the calls runs.
func (l *Lane) Reserve() *Place {
	l.mu.Lock()
	p := After(l.tail)
	l.tail = p
	l.mu.Unlock()
	return p
}

// ReserveInto is Reserve for a caller that holds the place's storage, such as a field of a struct it allocates anyway: p must be the zero Place, and it is the new place.
func (l *Lane) ReserveInto(p *Place) *Place {
	l.mu.Lock()
	if l.tail != nil && !l.tail.released.Load() {
		p.previous.Store(l.tail)
	}
	l.tail = p
	l.mu.Unlock()
	return p
}

// After returns a place that waits for previous, which may be nil. It is Reserve for a caller that keeps the tail itself, for example one lane per key in a map it already guards: the call must run in source order, under the caller's own lock.
func After(previous *Place) *Place {
	p := new(Place)
	if previous != nil && !previous.released.Load() {
		p.previous.Store(previous)
	}
	return p
}

// Wait returns when every earlier place was released, provided each earlier call waited before it released (Wait then Release, or Pass). A place released without waiting hands on at once, and the place behind it waits for it, not for the places before it.
func (p *Place) Wait() {
	if p == nil {
		return
	}
	previous := p.previous.Load()
	if previous == nil {
		return
	}
	previous.await()
	p.previous.CompareAndSwap(previous, nil)
}

// await returns when p was released.
func (p *Place) await() {
	if p.released.Load() {
		return
	}
	p.mu.Lock()
	if p.released.Load() {
		p.mu.Unlock()
		return
	}
	if p.wake == nil {
		p.wake = make(chan struct{})
	}
	wake := p.wake
	p.mu.Unlock()
	<-wake
}

// Release hands the place on, so the next place's Wait can return. It does not wait for the earlier places, and it is idempotent. It must run however the call ends. A released place waits for nothing: its own Wait returns at once.
func (p *Place) Release() {
	if p == nil {
		return
	}
	p.mu.Lock()
	if !p.released.Load() {
		p.released.Store(true)
		if p.wake != nil {
			close(p.wake)
		}
	}
	p.mu.Unlock()
	p.previous.Store(nil)
}

// Pass waits for the earlier places and then hands this one on, so a call that ends without issuing its request cannot let a later call issue before an earlier one. It is idempotent.
func (p *Place) Pass() {
	p.Wait()
	p.Release()
}
