package surfaces

import "sync"

// outbox delivers a follower's changes one at a time, in order, with no lock held while client code runs: a callback may call the
// follower back (SetMode, SetDevice, Stop), whose own change is queued and delivered once the callback returns (Pi's single
// thread runs it the same way).
type outbox struct {
	mu      sync.Mutex
	queue   []func()
	running bool
}

// update reads the follower's state with read, which returns the change to deliver for it (nil: none), queues the change and
// delivers every queued change. Reading and queueing are one step under the outbox's lock, so changes are delivered in the order
// their state was read: a change read from older state is never delivered after one read from newer state, and the last
// change delivered is the follower's current one (Pi's single thread runs `update` whole, in call order). read runs with that
// lock held and no callback of the client runs under it: read must not call the client's code or update.
func (o *outbox) update(read func() func()) {
	o.enqueue(read)
	o.drain()
}

// run queues deliver and delivers every queued change: an event whose state is read when it is delivered (the connection loop's).
func (o *outbox) run(deliver func()) {
	o.update(func() func() { return deliver })
}

func (o *outbox) enqueue(read func() func()) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if deliver := read(); deliver != nil {
		o.queue = append(o.queue, deliver)
	}
}

// drain delivers every queued change, unless another caller is delivering: that one delivers them.
func (o *outbox) drain() {
	o.mu.Lock()
	if o.running {
		o.mu.Unlock()
		return
	}
	o.running = true
	// A delivery that panics leaves the outbox usable: the next change delivers again.
	delivering := true
	defer func() {
		if delivering {
			o.mu.Lock()
			o.running = false
			o.mu.Unlock()
		}
	}()
	for len(o.queue) > 0 {
		next := o.queue[0]
		o.queue[0] = nil
		o.queue = o.queue[1:]
		o.mu.Unlock()
		next()
		o.mu.Lock()
	}
	o.running = false
	delivering = false
	o.mu.Unlock()
}
