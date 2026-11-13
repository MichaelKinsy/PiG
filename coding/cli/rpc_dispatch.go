// Ports packages/coding-agent/src/modes/rpc/rpc-mode.ts
package cli

import (
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsloop"
)

// rpcResponseTurn is the jsloop.Loop that serializes RPC input callbacks and awaited continuations, with the writer a completed response goes to. A completion from any earlier request waits for the currently admitted input batch, not just its original batch. Continuations never wait for future input.
type rpcResponseTurn struct {
	jsloop.Loop
	write func(any)
}

func (t *rpcResponseTurn) complete(response any) { t.Microtask(func() { t.write(response) }) }

// deliverRPCDialogAnswer hands a dialog's answer to the handler that awaits it. Pi resolves the dialog's promise on the answer's line and the handler goes on in a microtask (rpc-mode.ts:784-797), so behind every line of the stdin chunk: the reader emits the lines of a chunk in one synchronous loop (jsonl.ts:29-40, rpc-mode.ts:832-834), and a prompt of the chunk that names an extension command has run its handler to the handler's first await by then, on the runner that was current (agent-session.ts:2171-2176, 2279). One that awaits nothing answers ahead of the handler that asked: Pi 1.5.2 writes `notify ping`, `response c`, and then the notifications of the handler that went on.
//
// While a reload runs, the handler that asked is a handler of the reload's chain, and what follows its return is the rest of the reload: the old runner's invalidation after session_shutdown, the reloading command's response after session_start (agent-session.ts:3825-3826, 3857-3861). So an answer read during a reload is delivered as the loop's next task, behind the lines of its batch and the microtasks they queued. Delivered at once, the reload went on in another process while the batch was still being dispatched: a prompt of the chunk ran on a generation the reload had just invalidated, or answered behind the reloading command.
//
// Outside a reload the answer is delivered at once, so the handler's continuation can precede the responses of its chunk, which Pi writes first.
func deliverRPCDialogAnswer(turn *rpcResponseTurn, reloading bool, deliver func()) {
	if !reloading {
		deliver()
		return
	}
	turn.Macrotask(deliver)
}

// rpcCommandJoin counts extension command invocations whose prompt response is not yet written. Pi processes stdin's end one event-loop iteration after the line that started a command, and shutdown() then exits within microtasks and one tick (rpc-mode.ts:728-744, output-guard.ts:105-108). The flush therefore joins each command until it has answered or the host counts it suspended: its Node runtime reported its event-loop window closed unresponded, or its runtime has no microtask continuation and the command is still running at the checkpoint. A command that is suspended when the flush ends never answers.
type rpcCommandJoin struct {
	mu        sync.Mutex
	unwritten int
	suspended int
	closed    bool
	waiters   []chan struct{}
	// windowSuspended counts the commands Pi would have left unanswered when it read stdin's end (Host.SetCommandWindowHandler); windowWaiters wait for unwritten to reach it.
	windowSuspended int
	windowWaiters   []chan struct{}
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
	j.releaseWindowLocked()
	j.mu.Unlock()
}

// setSuspended records how many in-flight commands their runtimes report suspended.
func (j *rpcCommandJoin) setSuspended(n int) {
	j.mu.Lock()
	j.suspended = n
	j.releaseLocked()
	j.mu.Unlock()
}

// setWindowSuspended records how many in-flight commands would be unanswered when stdin's end is read.
func (j *rpcCommandJoin) setWindowSuspended(n int) {
	j.mu.Lock()
	j.windowSuspended = n
	j.releaseWindowLocked()
	j.mu.Unlock()
}

func (j *rpcCommandJoin) releaseWindowLocked() {
	if j.unwritten > j.windowSuspended && !j.closed {
		return
	}
	for _, w := range j.windowWaiters {
		close(w)
	}
	j.windowWaiters = nil
}

// windowIdle is closed once every unwritten response belongs to a command that Pi would have left unanswered when it read stdin's end: Pi answers the others before it reads it, so their responses precede the shutdown.
func (j *rpcCommandJoin) windowIdle() <-chan struct{} {
	j.mu.Lock()
	defer j.mu.Unlock()
	done := make(chan struct{})
	if j.closed || j.unwritten <= j.windowSuspended {
		close(done)
	} else {
		j.windowWaiters = append(j.windowWaiters, done)
	}
	return done
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

// idle is closed once every unwritten response belongs to a suspended command, or the join closed.
func (j *rpcCommandJoin) idle() <-chan struct{} {
	j.mu.Lock()
	defer j.mu.Unlock()
	done := make(chan struct{})
	if j.closed || j.unwritten <= j.suspended {
		close(done)
	} else {
		j.waiters = append(j.waiters, done)
	}
	return done
}

// close ends the join: no command response is written after it, so nothing is left to wait for.
func (j *rpcCommandJoin) close() {
	j.mu.Lock()
	j.closed = true
	for _, w := range j.waiters {
		close(w)
	}
	j.waiters = nil
	j.releaseWindowLocked()
	j.mu.Unlock()
}

func (j *rpcCommandJoin) isClosed() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.closed
}
