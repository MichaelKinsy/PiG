// Ports packages/coding-agent/src/modes/rpc/rpc-mode.ts
package main

import "sync"

// rpcResponseTurn serializes input callbacks and awaited continuations. A completion from any earlier request waits for the currently admitted input batch, not just its original batch. Continuations never wait for future input.
type rpcResponseTurn struct {
	mu          sync.Mutex
	execution   sync.Mutex
	write       func(any)
	pending     []func()
	active      bool
	idleWaiters []chan struct{}
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

// idle reports when the admitted input and its queued continuations drain. It does not join suspended commands, which may still be waiting for input.
func (t *rpcResponseTurn) idle() <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	done := make(chan struct{})
	if t.active {
		t.idleWaiters = append(t.idleWaiters, done)
	} else {
		close(done)
	}
	return done
}

// end drains with execution held. Unlocking execution while mu is still held makes idle publication atomic with producer admission; no completion can be stranded between the empty check and unlock. Continuations can enqueue more work without recursively acquiring execution.
func (t *rpcResponseTurn) end() {
	t.mu.Lock()
	for {
		pending := t.pending
		t.pending = nil
		if len(pending) == 0 {
			t.active = false
			for _, waiter := range t.idleWaiters {
				close(waiter)
			}
			t.idleWaiters = nil
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

// rpcCommandJoin counts extension command invocations whose prompt response is not yet written. Pi processes stdin's end one event-loop iteration after the line that started a command, and shutdown() then exits within microtasks and one tick (rpc-mode.ts:728-744, output-guard.ts:105-108). The flush therefore joins each command until it has answered or the host counts it suspended: its Node runtime reported its event-loop window closed unresponded, or its runtime has no microtask continuation and the command is still running at the checkpoint. A command that is suspended when the flush ends never answers.
type rpcCommandJoin struct {
	mu        sync.Mutex
	unwritten int
	suspended int
	closed    bool
	waiters   []chan struct{}
}

func (j *rpcCommandJoin) begin() {
	j.mu.Lock()
	j.unwritten++
	j.mu.Unlock()
}

func (j *rpcCommandJoin) end() {
	j.mu.Lock()
	j.unwritten--
	j.releaseLocked()
	j.mu.Unlock()
}

// setSuspended records how many in-flight commands their runtimes report suspended.
func (j *rpcCommandJoin) setSuspended(n int) {
	j.mu.Lock()
	j.suspended = n
	j.releaseLocked()
	j.mu.Unlock()
}

// releaseLocked wakes the waiters once every unwritten response belongs to a suspended command.
func (j *rpcCommandJoin) releaseLocked() {
	if j.unwritten > j.suspended {
		return
	}
	for _, w := range j.waiters {
		close(w)
	}
	j.waiters = nil
}

// idle is closed once every unwritten response belongs to a suspended command.
func (j *rpcCommandJoin) idle() <-chan struct{} {
	j.mu.Lock()
	defer j.mu.Unlock()
	done := make(chan struct{})
	if j.unwritten <= j.suspended {
		close(done)
	} else {
		j.waiters = append(j.waiters, done)
	}
	return done
}

// close ends the join: no command response is written after it.
func (j *rpcCommandJoin) close() {
	j.mu.Lock()
	j.closed = true
	j.mu.Unlock()
}

func (j *rpcCommandJoin) isClosed() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.closed
}
