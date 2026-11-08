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

// run queues deliver and, when no other caller is delivering, delivers every queued change.
func (o *outbox) run(deliver func()) {
	o.mu.Lock()
	o.queue = append(o.queue, deliver)
	if o.running {
		o.mu.Unlock()
		return
	}
	o.running = true
	for len(o.queue) > 0 {
		next := o.queue[0]
		o.queue[0] = nil
		o.queue = o.queue[1:]
		o.mu.Unlock()
		next()
		o.mu.Lock()
	}
	o.running = false
	o.mu.Unlock()
}
