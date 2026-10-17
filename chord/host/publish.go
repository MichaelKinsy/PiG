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
// Pi runs on one event loop; Go callers may publish from several goroutines. The queue state is guarded, tasks run
// outside the guard, and the goroutine that is draining (Pi's one drain loop) delivers every task enqueued meanwhile,
// so enqueue order stays the delivery order for every goroutine.

import "sync"

var publications struct {
	mu       sync.Mutex
	queue    []func()
	held     int
	draining bool
}

// enqueue is publish.ts enqueue: it runs task after everything enqueued before it. Tasks must not panic (report
// instead).
func enqueue(task func()) {
	publications.mu.Lock()
	publications.queue = append(publications.queue, task)
	publications.mu.Unlock()
	drain()
}

// holdPublications is publish.ts holdPublications: it delivers nothing until fn returns, then everything it enqueued,
// in order.
func holdPublications[T any](fn func() T) T {
	publications.mu.Lock()
	publications.held++
	publications.mu.Unlock()
	defer func() {
		publications.mu.Lock()
		publications.held--
		publications.mu.Unlock()
		drain()
	}()
	return fn()
}

func drain() {
	publications.mu.Lock()
	if publications.draining || publications.held > 0 {
		publications.mu.Unlock()
		return
	}
	publications.draining = true
	defer func() {
		publications.mu.Lock()
		publications.draining = false
		publications.mu.Unlock()
	}()
	for {
		if len(publications.queue) == 0 {
			publications.mu.Unlock()
			return
		}
		task := publications.queue[0]
		publications.queue[0] = nil
		publications.queue = publications.queue[1:]
		publications.mu.Unlock()
		task()
		publications.mu.Lock()
	}
}
