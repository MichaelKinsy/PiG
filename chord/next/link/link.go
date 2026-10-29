package link

import (
	"sync"
)

// Ports packages/chord/src/next/link/link.ts. Links (§3): an ordered, bidirectional channel of messages between two
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
// it), the one close listener, and closing once. A transport moves the bytes: it calls deliver with what arrived and
// ended when the transport closed, and implements Send and Close. The state is guarded because a transport's events
// arrive on its own goroutines; listeners run outside the guard.
type LinkEnd struct {
	mu       sync.Mutex
	closed   bool
	listener func(message []byte)
	// queued is the messages that arrived before the listener was set.
	queued        [][]byte
	closeListener func()
}

// Closed reports whether this end is closed.
func (end *LinkEnd) Closed() bool {
	end.mu.Lock()
	defer end.mu.Unlock()
	return end.closed
}

// OnMessage sets the listener and hands it what was queued.
func (end *LinkEnd) OnMessage(listener func(message []byte)) {
	end.mu.Lock()
	// A closed end delivers nothing: keeping the listener would keep its owner.
	if end.closed {
		end.mu.Unlock()
		return
	}
	end.listener = listener
	queued := end.queued
	end.queued = nil
	end.mu.Unlock()
	for _, message := range queued {
		// A listener may close the link: what is still queued then is dropped.
		if end.Closed() {
			return
		}
		listener(message)
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

// deliver handles a message that arrived; dropped once closed.
func (end *LinkEnd) deliver(message []byte) {
	end.mu.Lock()
	if end.closed {
		end.mu.Unlock()
		return
	}
	listener := end.listener
	if listener == nil {
		end.queued = append(end.queued, message)
	}
	end.mu.Unlock()
	if listener != nil {
		listener(message)
	}
}

// ended closes this end (once): nothing is delivered any more, and nothing the listeners reach is kept.
func (end *LinkEnd) ended() {
	end.mu.Lock()
	if end.closed {
		end.mu.Unlock()
		return
	}
	end.closed = true
	listener := end.closeListener
	end.listener = nil
	end.closeListener = nil
	end.queued = nil
	end.mu.Unlock()
	if listener != nil {
		listener()
	}
}

// memoryQueue is the pair's microtask queue: jobs run in order, one at a time, on a goroutine started when the queue
// was idle and gone when it drained (queueMicrotask's Go form: an owned task, nothing left running).
type memoryQueue struct {
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
	queue.mu.Lock()
	queue.jobs = append(queue.jobs, job)
	start := !queue.running
	queue.running = true
	queue.mu.Unlock()
	if start {
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
	peer := end.peerEnd()
	end.end()
	if peer != nil {
		end.queue.post(peer.end)
	}
}

func (end *MemoryLink) end() {
	end.peerMu.Lock()
	end.peer = nil
	end.peerMu.Unlock()
	end.ended()
}

// Flushed blocks until the pair's queued deliveries and closes ran: the Go form of waiting for a macrotask after
// queueMicrotask, which tests use to let messages in flight arrive.
func (end *MemoryLink) Flushed() { end.queue.wait() }
