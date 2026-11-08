package tui

import "sync"

// callQueue runs the surface's calls to the server one at a time, in the order they were made. Pi's handlers make their calls in order and the
// server answers a call once its effect is in place (a follow-up is queued, a run stopped), so Alt+Enter's send and the Alt+Up after it meet in that
// order: Alt+Up finds the follow-up queued. A call blocks, and one goroutine per call (what perform was) let the later call reach the server first.
// The goroutine that drains the queue lives while there is work.
type callQueue struct {
	mu      sync.Mutex
	pending []func()
	running bool
}

// push queues work behind what is queued; work runs on the queue's goroutine.
func (q *callQueue) push(work func()) {
	q.mu.Lock()
	q.pending = append(q.pending, work)
	start := !q.running
	q.running = true
	q.mu.Unlock()
	if start {
		go q.drain()
	}
}

func (q *callQueue) drain() {
	for {
		q.mu.Lock()
		if len(q.pending) == 0 {
			q.running = false
			q.mu.Unlock()
			return
		}
		work := q.pending[0]
		q.pending[0] = nil
		q.pending = q.pending[1:]
		q.mu.Unlock()
		work()
	}
}
