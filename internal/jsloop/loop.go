package jsloop

import "sync"

// Loop serializes callbacks the way one JavaScript thread runs them, with Node's two queues. A microtask is the continuation of an await that settles without the event loop turning. A macrotask is the continuation of an await whose work ran elsewhere (Node delivers it from a later event-loop task), a timer, or a new input line. One callback runs at a time. Every queued microtask, including those a callback queues, runs before the next macrotask, so a goroutine that finishes early cannot overtake a microtask the running callback queues later.
//
// A producer that finds the loop idle runs the queued callbacks on its own goroutine; there is no drainer goroutine. A busy loop owns the queued work, and its producers never block, because the running callback can be joining the operation that just completed. A callback must not wait for anything the loop will later produce: work that has to wait runs on its own goroutine and re-enters with Macrotask when it is done.
//
// An input batch holds the loop between Begin and End: callbacks queued meanwhile run when End drains them.
//
// The zero value is ready to use.
type Loop struct {
	mu          sync.Mutex
	execution   sync.Mutex
	microtasks  []func()
	macrotasks  []func()
	active      bool
	idleWaiters []chan struct{}
	// observer is told when an input batch begins and ends (SetBatchObserver); batch is whether the loop is held by one.
	observer func(open bool)
	batch    bool
}

// SetBatchObserver registers fn, which runs with true once Begin holds the loop and with false once the loop drained and left the batch End ends. A callback queued by a producer, with no Begin, is no batch. D189: the batch is Pi's iteration that reads one stdin chunk, whose microtasks no timer callback interleaves with.
func (l *Loop) SetBatchObserver(fn func(open bool)) {
	l.mu.Lock()
	l.observer = fn
	l.mu.Unlock()
}

// Begin enters the loop for an input batch: it waits for the callback that is running, and holds the loop until End.
func (l *Loop) Begin() {
	l.execution.Lock()
	l.mu.Lock()
	l.active = true
	l.batch = true
	observer := l.observer
	l.mu.Unlock()
	if observer != nil {
		observer(true)
	}
}

// Microtask queues the continuation of an await that settles without the event loop turning.
func (l *Loop) Microtask(fn func()) { l.schedule(false, fn) }

// Microtasks queues fns, in order, as microtasks.
func (l *Loop) Microtasks(fns ...func()) { l.schedule(false, fns...) }

// Macrotask queues the continuation of work that completed outside the loop. It runs after every queued microtask, and after the microtasks that a callback it ran queues.
func (l *Loop) Macrotask(fn func()) { l.schedule(true, fn) }

// Hops runs fn after n microtasks; with n zero it runs it at once.
func (l *Loop) Hops(n int, fn func()) {
	if n == 0 {
		fn()
		return
	}
	l.Microtask(func() { l.Hops(n-1, fn) })
}

func (l *Loop) schedule(macrotask bool, fns ...func()) {
	l.mu.Lock()
	if macrotask {
		l.macrotasks = append(l.macrotasks, fns...)
	} else {
		l.microtasks = append(l.microtasks, fns...)
	}
	// A busy loop owns the queued work. Do not block its producers: an input command can be joining the operation that just completed.
	if l.active || !l.execution.TryLock() {
		l.mu.Unlock()
		return
	}
	l.active = true
	l.mu.Unlock()
	l.End()
}

// Queued reports how many microtasks and macrotasks wait to run. A test uses it to know that a goroutine delivered its completion.
func (l *Loop) Queued() (microtasks, macrotasks int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.microtasks), len(l.macrotasks)
}

// Idle reports when the admitted input and its queued callbacks drain. It does not join suspended commands, which may still be waiting for input.
func (l *Loop) Idle() <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	done := make(chan struct{})
	if l.active {
		l.idleWaiters = append(l.idleWaiters, done)
	} else {
		close(done)
	}
	return done
}

// End drains the queued callbacks with the loop held and then leaves it. Unlocking execution while mu is still held makes idle publication atomic with producer admission; no callback can be stranded between the empty check and the unlock. Callbacks can queue more work without recursively acquiring execution.
func (l *Loop) End() {
	l.mu.Lock()
	for {
		batch := l.microtasks
		l.microtasks = nil
		if len(batch) == 0 && len(l.macrotasks) > 0 {
			batch = []func(){l.macrotasks[0]}
			l.macrotasks[0] = nil
			l.macrotasks = l.macrotasks[1:]
		}
		if len(batch) == 0 {
			l.active = false
			for _, waiter := range l.idleWaiters {
				close(waiter)
			}
			l.idleWaiters = nil
			ended, observer := l.batch, l.observer
			l.batch = false
			l.execution.Unlock()
			l.mu.Unlock()
			if ended && observer != nil {
				observer(false)
			}
			return
		}
		l.mu.Unlock()
		for _, fn := range batch {
			fn()
		}
		l.mu.Lock()
	}
}
