package host

// Ports packages/chord/src/publish.ts: one FIFO queue for every observer notification (state events, shape pushes,
// registry tables, listener invocations).
//
// Problem: notifying observers synchronously from inside a write lets a re-entrant write overtake the write being
// published. Example: subscriber A reacts to n = 1 by writing n = 2; subscriber B then sees 2 before 1.
// Solution: a publication first adopts every revision it makes (cells and derivations), then enqueues its
// notifications. Notifications run in enqueue order, so every observer sees revisions in adoption order.
// holdPublications defers delivery until a multi-step change (a reload commit) is complete.
//
// Pi's queue is a module-level one on its single event loop, so a notification always runs on the thread that owns the
// state it reads, and one drain or hold covers every host of the process. Go hosts each have their own loop (loop.go)
// and run at once, so a queue shared by every loop would let the goroutine that drains it run another host's
// notifications outside that host's loop: two senders on one connection. Each loop therefore has its own queue, which
// a section of that loop delivers, and a hold covers the loop that makes the change. Only a source that belongs to no
// host (a cell written outside one) uses the shared queue.
//
// What one call stack does keeps Pi's order across queues. A goroutine inside a hold or a drain (a notification, or a
// commit) is on Pi's one thread there: what it enqueues for another loop, or holds on another loop, joins the queue it
// is delivering, behind everything enqueued before it, and runs in a section of its own loop when that queue's turn
// comes. Goroutines outside any hold or drain, and the goroutines of other hosts, are Pi's other turns: each delivers
// its own writes at once.

import (
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// publisher is one publication queue: the guard covers the queue state, tasks run outside it, and the goroutine that is
// draining (Pi's one drain loop) delivers every task enqueued meanwhile, so enqueue order stays the delivery order for
// every goroutine.
type publisher struct {
	mu       sync.Mutex
	queue    []func()
	held     int
	draining bool
}

// shared is the queue of the notifications that belong to no loop.
var shared publisher

// publisherOf is the queue of loop; a nil loop is the shared queue.
func publisherOf(loop *loop) *publisher {
	if loop == nil {
		return &shared
	}
	return &loop.publications
}

// delivering are the goroutines inside a hold or a drain, by goroutine ID, with the queue each one holds or drains.
var delivering struct {
	count  atomic.Int32
	queues sync.Map
}

// deliveringQueue is the queue the calling goroutine holds or drains, nil when it does neither.
func deliveringQueue() *publisher {
	if delivering.count.Load() == 0 {
		return nil
	}
	queue, _ := delivering.queues.Load(goroutineid.Current())
	current, _ := queue.(*publisher)
	return current
}

// enter makes queue the calling goroutine's queue and returns the goroutine's ID for leave; a goroutine that already
// holds or drains a queue keeps it and gets 0.
func (queue *publisher) enter() uint64 {
	id := goroutineid.Current()
	if _, held := delivering.queues.LoadOrStore(id, queue); held {
		return 0
	}
	delivering.count.Add(1)
	return id
}

func leave(id uint64) {
	if id != 0 {
		delivering.queues.Delete(id)
		delivering.count.Add(-1)
	}
}

// inSectionOf is task as a section of loop: a task that joined another queue still runs where its state is owned.
func inSectionOf(loop *loop, task func()) func() {
	if loop == nil {
		return task
	}
	return func() { loop.run(task) }
}

func (queue *publisher) push(task func()) {
	queue.mu.Lock()
	queue.queue = append(queue.queue, task)
	queue.mu.Unlock()
}

// enqueue is publish.ts enqueue: it runs task after everything enqueued before it, on loop. Tasks must not panic
// (report instead).
func enqueue(loop *loop, task func()) {
	queue := publisherOf(loop)
	if current := deliveringQueue(); current != nil && current != queue {
		// The queue this goroutine delivers delivers task too: Pi's one drain loop (or hold) covers it.
		current.push(inSectionOf(loop, task))
		return
	}
	queue.push(task)
	queue.deliver(loop)
}

// holdPublications is publish.ts holdPublications: loop delivers nothing until fn returns, then everything it
// enqueued, in order.
func holdPublications[T any](loop *loop, fn func() T) T {
	queue := publisherOf(loop)
	if current := deliveringQueue(); current != nil && current != queue {
		// Pi's one hold, or its drain loop that a hold does not stop, covers what fn enqueues already.
		return fn()
	}
	id := queue.enter()
	queue.mu.Lock()
	queue.held++
	queue.mu.Unlock()
	defer func() {
		queue.mu.Lock()
		queue.held--
		queue.mu.Unlock()
		leave(id)
		queue.deliver(loop)
	}()
	return fn()
}

// deliver drains the queue in a section of its loop: here when the caller is on the loop, else as a section the caller
// waits for. A running drain or a hold in progress delivers what is queued, so there is nothing to start then.
func (queue *publisher) deliver(loop *loop) {
	if loop == nil || loop.onLoop() {
		queue.drain()
		return
	}
	queue.mu.Lock()
	idle := queue.draining || queue.held > 0 || len(queue.queue) == 0
	queue.mu.Unlock()
	if !idle {
		loop.run(queue.drain)
	}
}

func (queue *publisher) drain() {
	queue.mu.Lock()
	if queue.draining || queue.held > 0 {
		queue.mu.Unlock()
		return
	}
	queue.draining = true
	id := queue.enter()
	defer func() {
		leave(id)
		queue.mu.Lock()
		queue.draining = false
		queue.mu.Unlock()
	}()
	for {
		if len(queue.queue) == 0 {
			queue.mu.Unlock()
			return
		}
		task := queue.queue[0]
		queue.queue[0] = nil
		queue.queue = queue.queue[1:]
		queue.mu.Unlock()
		task()
		queue.mu.Lock()
	}
}
