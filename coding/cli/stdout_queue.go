package cli

// Ports packages/coding-agent/src/core/output-guard.ts

import (
	"context"
	"io"
	"sync"
)

// pig additive (D112): maxRetainedStdoutBatch bounds the buffer the writer keeps between batches (golang/go#23199).
const maxRetainedStdoutBatch = 256 << 10

// stdoutQueue is output-guard.ts's raw stdout tail: writeRawStdout appends a serialized chunk and returns at once, one writer drains the chunks in order, and waitForRawStdoutBackpressure resolves when the tail is empty. A Session listener therefore never blocks on a slow or stopped stdout reader. A failed write stops output and reports once, as the tail's catch calls process.exit(1).
//
// The chunks pending when the writer takes its turn are one write of their concatenation, the byte stream that one write per chunk produces. The writer is one goroutine that lives from the first chunk until Close, not one per burst of chunks.
type stdoutQueue struct {
	w       io.Writer
	onError func(error)

	mu sync.Mutex
	// wake parks the writer while no chunk is pending.
	wake *sync.Cond
	// pending holds the concatenated chunks the writer has not taken; spare is the writer's previous batch, kept for the next one.
	pending, spare []byte
	// running is set while the writer goroutine exists; writing while it holds a batch outside the lock; parked while it waits on wake.
	running, writing, parked bool
	// closing asks the writer to exit once nothing is pending.
	closing bool
	failed  bool
	// changed is closed when the tail next goes idle; it exists only while a Wait is blocked.
	changed chan struct{}
}

func newStdoutQueue(w io.Writer, onError func(error)) *stdoutQueue {
	q := &stdoutQueue{w: w, onError: onError}
	q.wake = sync.NewCond(&q.mu)
	return q
}

// Write enqueues a copy of p. It never blocks on the underlying writer.
func (q *stdoutQueue) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.failed {
		return len(p), nil
	}
	if q.pending == nil && q.spare != nil {
		q.pending, q.spare = q.spare, nil
	}
	q.pending = append(q.pending, p...)
	q.closing = false
	if !q.running {
		q.running = true
		go q.drain()
	} else if q.parked {
		q.wake.Signal()
	}
	return len(p), nil
}

// drain is the writer goroutine: it writes everything pending in one call, and parks when nothing is. It returns after Close once the tail is empty.
func (q *stdoutQueue) drain() {
	q.mu.Lock()
	for {
		for len(q.pending) == 0 && !q.closing {
			q.parked = true
			q.wake.Wait()
			q.parked = false
		}
		if len(q.pending) == 0 {
			q.running = false
			q.mu.Unlock()
			return
		}
		batch := q.pending
		q.pending = nil
		if q.failed {
			q.spare = batch[:0]
			continue
		}
		q.writing = true
		q.mu.Unlock()
		_, err := q.w.Write(batch)
		if err != nil {
			q.mu.Lock()
			q.failed = true
			q.mu.Unlock()
			if q.onError != nil {
				q.onError(err)
			}
		}
		q.mu.Lock()
		if err != nil {
			q.pending = nil
		}
		if cap(batch) <= maxRetainedStdoutBatch {
			q.spare = batch[:0]
		}
		if len(q.pending) == 0 {
			q.writing = false
			q.signalLocked()
		}
	}
}

func (q *stdoutQueue) signalLocked() {
	if q.changed != nil {
		close(q.changed)
		q.changed = nil
	}
}

func (q *stdoutQueue) idleLocked() bool { return len(q.pending) == 0 && !q.writing }

// Close lets the writer goroutine exit once every chunk enqueued so far is written. It does not wait: a writer blocked on a stopped reader exits when its write returns. A later Write starts a new writer.
func (q *stdoutQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.running {
		return
	}
	q.closing = true
	if q.parked {
		q.wake.Signal()
	}
}

// Wait returns when every chunk enqueued so far has been written or output has failed, or when ctx ends. It reports whether the tail drained.
func (q *stdoutQueue) Wait(ctx context.Context) bool {
	for {
		q.mu.Lock()
		if q.idleLocked() {
			q.mu.Unlock()
			return true
		}
		if q.changed == nil {
			q.changed = make(chan struct{})
		}
		changed := q.changed
		q.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return false
		}
	}
}
