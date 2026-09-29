package main

// Ports packages/coding-agent/src/core/output-guard.ts

import (
	"context"
	"io"
	"sync"
)

// stdoutQueue is output-guard.ts's raw stdout tail: writeRawStdout appends a serialized chunk and returns at once, one writer drains the chunks in order, and waitForRawStdoutBackpressure resolves when the tail is empty. A Session listener therefore never blocks on a slow or stopped stdout reader. A failed write stops output and reports once, as the tail's catch calls process.exit(1).
type stdoutQueue struct {
	w       io.Writer
	onError func(error)

	mu      sync.Mutex
	changed chan struct{}
	pending [][]byte
	writing bool
	failed  bool
}

func newStdoutQueue(w io.Writer, onError func(error)) *stdoutQueue {
	return &stdoutQueue{w: w, onError: onError, changed: make(chan struct{})}
}

// Write enqueues a copy of p. It never blocks on the underlying writer.
func (q *stdoutQueue) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	chunk := append([]byte(nil), p...)
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.failed {
		return len(p), nil
	}
	q.pending = append(q.pending, chunk)
	if !q.writing {
		q.writing = true
		go q.drain()
	}
	return len(p), nil
}

func (q *stdoutQueue) drain() {
	for {
		q.mu.Lock()
		if len(q.pending) == 0 || q.failed {
			q.pending = nil
			q.writing = false
			q.signalLocked()
			q.mu.Unlock()
			return
		}
		chunk := q.pending[0]
		q.pending[0] = nil
		q.pending = q.pending[1:]
		q.mu.Unlock()
		if _, err := q.w.Write(chunk); err != nil {
			q.mu.Lock()
			q.failed = true
			q.mu.Unlock()
			if q.onError != nil {
				q.onError(err)
			}
		}
	}
}

func (q *stdoutQueue) signalLocked() {
	close(q.changed)
	q.changed = make(chan struct{})
}

// Wait returns when every chunk enqueued so far has been written or output has failed, or when ctx ends. It reports whether the tail drained.
func (q *stdoutQueue) Wait(ctx context.Context) bool {
	for {
		q.mu.Lock()
		idle := !q.writing
		changed := q.changed
		q.mu.Unlock()
		if idle {
			return true
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return false
		}
	}
}
