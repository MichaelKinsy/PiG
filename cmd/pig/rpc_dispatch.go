// Ports packages/coding-agent/src/modes/rpc/rpc-mode.ts
package main

import "sync"

// rpcResponseTurn serializes input callbacks and awaited continuations. A completion from any earlier request waits for the currently admitted input batch, not just its original batch. Continuations never wait for future input.
type rpcResponseTurn struct {
	mu        sync.Mutex
	execution sync.Mutex
	write     func(any)
	pending   []func()
	active    bool
}

func (t *rpcResponseTurn) begin() {
	t.execution.Lock()
	t.mu.Lock()
	t.active = true
	t.mu.Unlock()
}

func (t *rpcResponseTurn) complete(response any) { t.after(func() { t.write(response) }) }

func (t *rpcResponseTurn) after(continuation func()) { t.enqueue([]func(){continuation}) }

func (t *rpcResponseTurn) enqueue(continuations []func()) {
	t.mu.Lock()
	t.pending = append(t.pending, continuations...)
	// A busy executor owns the queued work. Do not block its producers: an input command can be joining the operation that just completed.
	if t.active || !t.execution.TryLock() {
		t.mu.Unlock()
		return
	}
	t.active = true
	t.mu.Unlock()
	t.end()
}

// end drains with execution held. Unlocking execution while mu is still held makes idle publication atomic with producer admission; no completion can be stranded between the empty check and unlock. Continuations can enqueue more work without recursively acquiring execution.
func (t *rpcResponseTurn) end() {
	t.mu.Lock()
	for {
		pending := t.pending
		t.pending = nil
		if len(pending) == 0 {
			t.active = false
			t.execution.Unlock()
			t.mu.Unlock()
			return
		}
		t.mu.Unlock()
		for _, continuation := range pending {
			continuation()
		}
		t.mu.Lock()
	}
}
