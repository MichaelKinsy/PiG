package link

import (
	"sync"
)

// Ports packages/chord/src/link/link.ts. Links (§3): an ordered, bidirectional channel of messages between two
// hosts, and the in-memory pair.

// Link is an ordered, bidirectional channel of `[]byte` messages between two hosts. Adapters only move bytes.
type Link interface {
	// Send sends one message; a no-op once closed.
	Send(message []byte)
	// OnMessage sets the one listener for incoming messages; messages that arrived earlier are delivered to it first.
	// On a closed link it is not kept.
	OnMessage(listener func(message []byte))
	// Close closes both ends; messages not delivered yet are dropped.
	Close()
	// OnClose sets the one listener for the close of either end; it runs at once when the link is closed already.
	OnClose(listener func())
}

// LinkEnd is what every link end shares: the one message listener (messages that arrived before it are queued for
// it), the one close listener, and closing once. A transport moves the bytes: it calls deliver with what arrived,
// ended when the transport closed and closedHere when this side closes it, and implements Send and Close. The state is
// guarded because a transport's events arrive on its own goroutines; listeners run outside the guard.
type LinkEnd struct {
	mu       sync.Mutex
	closed   bool
	listener func(message []byte)
	// queued is the messages that arrived before the listener was set, or while they were handed to it; draining is true
	// while OnMessage hands them over; ending, that the transport ended during that hand-over: what it delivered before
	// its end is still handed over, then the hand-over closes this end, and nothing that arrives after its end is taken.
	queued        [][]byte
	draining      bool
	ending        bool
	closeListener func()
}

// Closed reports whether this end is closed.
func (end *LinkEnd) Closed() bool {
	end.mu.Lock()
	defer end.mu.Unlock()
	return end.closed
}

// OnMessage sets the listener and hands it what was queued. A message that arrives while the queue is handed over is queued
// behind it, so the listener sees them in arrival order; the goroutine that set the listener hands them over. A transport
// that ends meanwhile (ended, on its own goroutine) ends after what it delivered before: the hand-over closes this end
// once the queue is handed over, as Pi's one thread finishes the hand-over before it hears of the close. The close is
// then told when the listener returned, as it is when the transport itself is inside the listener (deliver): a listener
// that never returns holds a transport's end either way, and never this side's close (closedHere).
func (end *LinkEnd) OnMessage(listener func(message []byte)) {
	end.mu.Lock()
	// A closed end delivers nothing: keeping the listener would keep its owner.
	if end.closed {
		end.mu.Unlock()
		return
	}
	end.listener = listener
	if end.draining {
		// The hand-over under way continues with the new listener.
		end.mu.Unlock()
		return
	}
	end.draining = true
	handedOver := false
	defer func() {
		if !handedOver {
			end.handOverLeft()
		}
	}()
	for {
		// A listener may close the link: what is still queued then is dropped (closedHere cleared it).
		if end.closed || end.listener == nil || len(end.queued) == 0 {
			if len(end.queued) == 0 {
				// Handed over: the array goes too. What is left of the queue is empty and still points into it, and an end
				// whose listener is set does not queue again, so nothing would replace it before the close.
				end.queued = nil
			}
			end.draining = false
			var closeListener func()
			if end.ending {
				// The transport ended meanwhile: what it delivered before is handed over, and now this end closes.
				closeListener = end.closeLocked()
			}
			end.mu.Unlock()
			handedOver = true
			if closeListener != nil {
				closeListener()
			}
			return
		}
		message, current := end.queued[0], end.listener
		// The slot is cleared as its message goes to the listener: the array would keep it for as long as the queue lives.
		end.queued[0] = nil
		end.queued = end.queued[1:]
		end.mu.Unlock()
		current(message)
		end.mu.Lock()
	}
}

// handOverLeft ends a hand-over that its listener left by a panic: what it had not handed over goes with it, as Pi's loop
// over the queue ends at a throw, the listener stays for what arrives later, and a transport's end that waited for the
// hand-over closes this end.
func (end *LinkEnd) handOverLeft() {
	end.mu.Lock()
	end.draining = false
	end.queued = nil
	var closeListener func()
	if end.ending {
		closeListener = end.closeLocked()
	}
	end.mu.Unlock()
	if closeListener != nil {
		closeListener()
	}
}

// OnClose sets the close listener; it runs at once on a closed end.
func (end *LinkEnd) OnClose(listener func()) {
	end.mu.Lock()
	if end.closed {
		end.mu.Unlock()
		listener()
		return
	}
	end.closeListener = listener
	end.mu.Unlock()
}

// deliver handles a message that arrived; dropped once closed, or once the transport ended.
func (end *LinkEnd) deliver(message []byte) {
	end.mu.Lock()
	if end.closed || end.ending {
		end.mu.Unlock()
		return
	}
	listener := end.listener
	if listener == nil || end.draining {
		end.queued = append(end.queued, message)
		end.mu.Unlock()
		return
	}
	end.mu.Unlock()
	listener(message)
}

// ended closes this end (once) for its transport, which ended: nothing is delivered any more, and nothing the listeners
// reach is kept. While OnMessage hands the queue over on another goroutine, what the transport delivered before its end
// is the listener's first: ended returns, and the hand-over closes this end after it (OnMessage).
func (end *LinkEnd) ended() {
	end.mu.Lock()
	if end.closed || end.ending {
		end.mu.Unlock()
		return
	}
	if end.draining {
		end.ending = true
		end.mu.Unlock()
		return
	}
	listener := end.closeLocked()
	end.mu.Unlock()
	if listener != nil {
		listener()
	}
}

// closedHere closes this end (once) for this side, which closed the link: at once, also from a listener or beside one
// during a hand-over, and what is still queued is dropped, as in Pi (`if (this.#closed) return` in onMessage's loop).
func (end *LinkEnd) closedHere() {
	end.mu.Lock()
	if end.closed {
		end.mu.Unlock()
		return
	}
	listener := end.closeLocked()
	end.mu.Unlock()
	if listener != nil {
		listener()
	}
}

// closeLocked closes this end under its guard and returns the close listener, for the caller to run outside it.
func (end *LinkEnd) closeLocked() func() {
	end.closed, end.ending = true, false
	listener := end.closeListener
	end.listener = nil
	end.closeListener = nil
	end.queued = nil
	return listener
}

// memoryQueue is the pair's microtask queue: jobs run in order, one at a time, on a goroutine started when the queue
// was idle and gone when it drained (queueMicrotask's Go form: an owned task, nothing left running).
type memoryQueue struct {
	// post and flush, when set, are another microtask queue the pair runs on (MemoryLinkPairOn): deliveries and closes are
	// its jobs, in its order, and flush waits until it ran what was queued.
	post_ func(job func())
	flush func()

	mu      sync.Mutex
	idle    *sync.Cond
	jobs    []func()
	running bool
}

func newMemoryQueue() *memoryQueue {
	queue := &memoryQueue{}
	queue.idle = sync.NewCond(&queue.mu)
	return queue
}

func (queue *memoryQueue) post(job func()) {
	if queue.post_ != nil {
		queue.post_(job)
		return
	}
	queue.mu.Lock()
	queue.jobs = append(queue.jobs, job)
	start := !queue.running
	queue.running = true
	queue.mu.Unlock()
	if start {
		//portlint:allow golifetime memoryQueue owns this goroutine: running stays true until drain finds jobs empty, broadcasts idle and returns; the queue's flush waits on that Cond (queueMicrotask in link.ts:92 has no handle either)
		go queue.drain()
	}
}

func (queue *memoryQueue) drain() {
	for {
		queue.mu.Lock()
		if len(queue.jobs) == 0 {
			queue.running = false
			queue.idle.Broadcast()
			queue.mu.Unlock()
			return
		}
		job := queue.jobs[0]
		queue.jobs[0] = nil
		queue.jobs = queue.jobs[1:]
		queue.mu.Unlock()
		job()
	}
}

func (queue *memoryQueue) wait() {
	if queue.flush != nil {
		queue.flush()
		return
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	for queue.running || len(queue.jobs) > 0 {
		queue.idle.Wait()
	}
}

// MemoryLinkPair returns two ends of one link: what one sends, the other receives asynchronously (a microtask later,
// on the pair's queue), in order.
func MemoryLinkPair() (*MemoryLink, *MemoryLink) {
	queue := newMemoryQueue()
	a, b := &MemoryLink{queue: queue}, &MemoryLink{queue: queue}
	a.peer, b.peer = b, a
	return a, b
}

// MemoryLinkPairOn is MemoryLinkPair on a microtask queue of the caller's: what one end sends is a job of post, delivered
// in post's order with its other jobs (Pi's memory link queues its deliveries on the one global microtask queue the hosts
// queue theirs on, so a host's microtask queued while it handles a message runs after the deliveries queued before it).
// flush blocks until post's queue ran what was queued: Flushed calls it.
func MemoryLinkPairOn(post func(job func()), flush func()) (*MemoryLink, *MemoryLink) {
	queue := newMemoryQueue()
	queue.post_, queue.flush = post, flush
	a, b := &MemoryLink{queue: queue}, &MemoryLink{queue: queue}
	a.peer, b.peer = b, a
	return a, b
}

// MemoryLink is one end of a memory link. Closing it closes this end at once and the peer a microtask later, as a
// transport would.
type MemoryLink struct {
	LinkEnd
	queue *memoryQueue
	// peer is the other end while this one is open.
	peerMu sync.Mutex
	peer   *MemoryLink
}

func (end *MemoryLink) peerEnd() *MemoryLink {
	end.peerMu.Lock()
	defer end.peerMu.Unlock()
	return end.peer
}

// Send sends a message to the peer, delivered a microtask later.
func (end *MemoryLink) Send(message []byte) {
	if end.Closed() {
		return
	}
	peer := end.peerEnd()
	if peer == nil {
		return
	}
	end.queue.post(func() { peer.deliver(message) })
}

// Close closes this end at once and the peer a microtask later.
func (end *MemoryLink) Close() {
	if end.Closed() {
		return
	}
	peer := end.unpeer()
	end.closedHere()
	if peer != nil {
		end.queue.post(peer.end)
	}
}

// end closes this end for its peer, which closed: after the messages the peer sent before.
func (end *MemoryLink) end() {
	end.unpeer()
	end.ended()
}

// unpeer forgets the peer and returns it; nil once forgotten.
func (end *MemoryLink) unpeer() *MemoryLink {
	end.peerMu.Lock()
	defer end.peerMu.Unlock()
	peer := end.peer
	end.peer = nil
	return peer
}

// Flushed blocks until the pair's queued deliveries and closes ran: the Go form of waiting for a macrotask after
// queueMicrotask, which tests use to let messages in flight arrive.
func (end *MemoryLink) Flushed() { end.queue.wait() }
