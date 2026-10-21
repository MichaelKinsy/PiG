package jsloop

import "sync"

// Lane keeps the calls of one destination in source order up to the point each issues its request. Pi runs the synchronous prefix of every call of `Promise.all(xs.map(f))` in source order, so f(a) issues its request before f(b). Go starts a goroutine per call and the scheduler may run the later one first, so the caller reserves each call's place in source order before it starts any goroutine, and a call issues its request only after every earlier place was released.
//
// The zero value is ready to use.
type Lane struct {
	mu   sync.Mutex
	tail chan struct{}
}

// Place is one call's reservation in a Lane. A nil *Place is no reservation: Wait, Release and Pass do nothing.
type Place struct {
	previous <-chan struct{}
	done     chan struct{}
	once     sync.Once
}

// Reserve takes the next place in the lane. It must run in source order, before any of the calls runs.
func (l *Lane) Reserve() *Place {
	p := &Place{done: make(chan struct{})}
	l.mu.Lock()
	p.previous = l.tail
	l.tail = p.done
	l.mu.Unlock()
	return p
}

// Wait returns when every earlier place was released.
func (p *Place) Wait() {
	if p == nil || p.previous == nil {
		return
	}
	<-p.previous
}

// Release hands the place on, so the next place's Wait can return. It does not wait for the earlier places, and it is idempotent. It must run however the call ends.
func (p *Place) Release() {
	if p == nil {
		return
	}
	p.once.Do(func() { close(p.done) })
}

// Pass waits for the earlier places and then hands this one on, so a call that ends without issuing its request cannot let a later call issue before an earlier one. It is idempotent.
func (p *Place) Pass() {
	p.Wait()
	p.Release()
}
