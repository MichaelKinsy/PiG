package codingagent

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
	"github.com/MichaelKinsy/PiG/tui"
)

// enterOwnerLoop records the calling goroutine as the owner loop and returns the function that restores the previous owner. Nested input loops run on the owner goroutine, so they leave the owner unchanged.
func (m *InteractiveMode) enterOwnerLoop() (leave func()) {
	previous := m.ownerGoroutine.Swap(goroutineid.Current())
	return func() { m.ownerGoroutine.Store(previous) }
}

// onOwnerLoop reports whether the caller is the goroutine running the owner loop. Upstream's single JS event loop makes that true for every caller, so its synchronous reads of UI state never queue; Go has several goroutines, so a caller on the loop must run owner work inline. It is false when no loop is running.
func (m *InteractiveMode) onOwnerLoop() bool {
	owner := m.ownerGoroutine.Load()
	return owner != 0 && owner == goroutineid.Current()
}

// ownerPost is a task the owner loop posted to its own full queue, with the context that may abandon it.
type ownerPost struct {
	// done is the posting context's Done channel; a post without a context has a nil channel, which never abandons it.
	done <-chan struct{}
	fn   func()
}

// postFromOwner queues fn from the owner loop without blocking it and without running fn inline. A full queue that only this goroutine drains cannot make room while it waits, and running fn now would run it ahead of the tasks already queued and inside the caller's current work. Upstream's event loop runs a posted callback after the current task and after every callback queued before it, so fn waits in ownerOverflow, behind earlier overflow, and one background forwarder hands it to the loop. A post whose ctx ends, or that is still waiting at shutdown, is abandoned as a blocked off-loop post would be.
func (m *InteractiveMode) postFromOwner(ctx context.Context, fn func()) {
	m.ownerOverflowMu.Lock()
	defer m.ownerOverflowMu.Unlock()
	if len(m.ownerOverflow) == 0 && m.postUITask(fn) {
		return
	}
	var done <-chan struct{}
	if ctx != nil {
		done = ctx.Done()
	}
	m.ownerOverflow = append(m.ownerOverflow, ownerPost{done: done, fn: fn})
	if len(m.ownerOverflow) == 1 {
		m.ownerOverflowActive.Store(true)
		m.ownerForwarder.Add(1)
		m.backgroundTasks.Go(func() {
			defer m.ownerForwarder.Done()
			m.forwardOwnerOverflow()
		})
	}
}

// forwardOwnerOverflow hands owner overflow to the loop in order and exits when none is left or the mode shuts down. It is the only forwarder: postFromOwner starts one when the overflow goes from empty to one task, and a forwarder leaves in the same critical section that removes the last task and clears ownerOverflowActive, so a post that finds the overflow empty always finds no forwarder either. A task stays in the overflow until the loop has accepted it, so a later post cannot pass it; the flag therefore clears just after the loop may already have run the last task.
func (m *InteractiveMode) forwardOwnerOverflow() {
	var shutdown <-chan struct{}
	if m.backgroundCtx != nil {
		shutdown = m.backgroundCtx.Done()
	} else if m.runCtx != nil {
		shutdown = m.runCtx.Done()
	}
	for {
		m.ownerOverflowMu.Lock()
		next := m.ownerOverflow[0]
		m.ownerOverflowMu.Unlock()
		stop := false
		select {
		case m.uiTaskCh <- next.fn:
		case <-next.done:
		case <-shutdown:
			stop = true
		}
		m.ownerOverflowMu.Lock()
		if stop {
			clear(m.ownerOverflow)
			m.ownerOverflow = nil
		} else {
			m.ownerOverflow[0] = ownerPost{}
			m.ownerOverflow = m.ownerOverflow[1:]
		}
		if len(m.ownerOverflow) == 0 {
			m.ownerOverflowActive.Store(false)
			m.ownerOverflowMu.Unlock()
			return
		}
		m.ownerOverflowMu.Unlock()
	}
}

// runOnOwner runs fn on the owner loop. A caller already on the loop runs fn inline, because queueing a task and waiting for its result from the loop itself waits for work only that goroutine can run. Other callers queue fn as runOnMain does and do not wait for it.
func (m *InteractiveMode) runOnOwner(ctx context.Context, fn func()) {
	if m.onOwnerLoop() {
		fn()
		return
	}
	m.runOnMain(ctx, fn)
}

// OnOwnerLoop reports whether the caller is the goroutine running the owner loop. The extension Runner asks before it waits, on the caller's goroutine, for a handler that runs on another goroutine and may need the loop (inproc Runner.onMcpServersChange).
func (c *ExtUIContext) OnOwnerLoop() bool { return c.m.onOwnerLoop() }

// writeBetweenFrames runs a terminal write so that it is before the bytes of a frame or after them. The owner loop renders the frames, so a write made on it
// is ordered where it is made, as Pi's terminal.write calls are on its one event loop. A write another goroutine makes (an extension's host call) is queued
// for the loop in order and not waited for. While no loop runs, or once the exit's stop began (the queue is closed), it runs where it is made, when there is
// no frame to meet: the terminal's stop transaction then orders it against the stop (writeOutsideStop).
func (m *InteractiveMode) writeBetweenFrames(write func()) {
	if m.ownerGoroutine.Load() == 0 || m.onOwnerLoop() {
		write()
		return
	}
	queued, wake := m.terminalWrites.enqueue(write)
	if !queued {
		write()
		return
	}
	if wake {
		if err := m.postToMain(m.runCtx, m.terminalWrites.run); err != nil {
			// The loop will not take it: the queue is run here, in order, so nothing is lost.
			m.terminalWrites.run()
		}
	}
}

// terminalWriteQueue holds, in the order they were made, the terminal writes that goroutines other than the owner loop made while it ran (writeBetweenFrames).
// The loop runs them between frames; the exit's stop (flushAndClose) runs the ones the loop never took before its own bytes, as Pi's loop ran every callback
// that preceded stop(), and closes the queue.
type terminalWriteQueue struct {
	// mu guards writes, pending and closed. It is never held across a write.
	mu     sync.Mutex
	writes []func()
	// pending is whether a run is posted to the loop or running: the next write needs no second one.
	pending bool
	closed  bool
	// runMu holds the writes of one run together, so that two runs (the loop's and the stop's) write in the queue's order.
	runMu sync.Mutex
}

// enqueue appends write unless the queue is closed. wake reports that the caller must have a run posted: none is pending.
func (q *terminalWriteQueue) enqueue(write func()) (queued, wake bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false, false
	}
	q.writes = append(q.writes, write)
	wake = !q.pending
	q.pending = true
	return true, wake
}

// run writes what is queued, including what is queued while it runs.
func (q *terminalWriteQueue) run() {
	q.runMu.Lock()
	defer q.runMu.Unlock()
	for {
		q.mu.Lock()
		batch := q.writes
		q.writes = nil
		if len(batch) == 0 {
			q.pending = false
			q.mu.Unlock()
			return
		}
		q.mu.Unlock()
		for _, write := range batch {
			write()
		}
	}
}

// flushAndClose writes what is queued, and what is queued while it writes, then closes the queue in the same critical section that finds it empty: a write
// is either in the queue and written here, in order, or made after the close and left to the caller.
func (q *terminalWriteQueue) flushAndClose() {
	q.runMu.Lock()
	defer q.runMu.Unlock()
	for {
		q.mu.Lock()
		batch := q.writes
		q.writes = nil
		if len(batch) == 0 {
			q.closed = true
			q.mu.Unlock()
			return
		}
		q.mu.Unlock()
		for _, write := range batch {
			write()
		}
	}
}

// rememberDrawTerminal keeps the terminal the renderer draws on and its stop gate, for the writers that are not the renderer's: a renderer swap keeps the terminal, and
// a goroutine other than the owner loop must not read m.tuiInst, which the exit's transcript switch assigns. A terminal that has no gate leaves it unset.
func (m *InteractiveMode) rememberDrawTerminal() {
	if m.tuiInst == nil {
		return
	}
	terminal := m.tuiInst.Terminal()
	m.drawTerminal.Store(&terminal)
	if gate, ok := terminal.(tui.StopGate); ok {
		m.terminalGate.Store(&gate)
	}
}

// writeOutsideStop runs a terminal write of a goroutine other than the one that stops the terminal where the terminal's stop transaction orders it: now,
// or after the stop that holds the terminal (dropped when that stop is a signal's). Without a gate it runs where it is made.
func (m *InteractiveMode) writeOutsideStop(write func()) {
	if gate := m.terminalGate.Load(); gate != nil {
		(*gate).WriteOutsideStop(write)
		return
	}
	write()
}

// beginTerminalStop takes the terminal for a stop; nil when the renderer's terminal has no gate. The goroutine that holds the transaction writes the
// stop's bytes and nothing else: it never calls a write that waits behind a stop (the terminal's SetTitle, SetProgress).
func (m *InteractiveMode) beginTerminalStop() *tui.StopTransaction {
	gate := m.terminalGate.Load()
	if gate == nil {
		return nil
	}
	return (*gate).BeginStop()
}

// endTerminalStop ends the transaction beginTerminalStop returned. seal drops the writes of other goroutines that wait behind it and every later one.
func (m *InteractiveMode) endTerminalStop(tx *tui.StopTransaction, seal bool) {
	if tx != nil {
		tx.End(seal)
	}
}

// holdTerminalStop takes the terminal for a stop that the run goes on after (the external editor's, a suspend's): the returned call ends it, and the writes
// that waited behind it run.
func (m *InteractiveMode) holdTerminalStop() (end func()) {
	tx := m.beginTerminalStop()
	return func() { m.endTerminalStop(tx, false) }
}

// stopTerminalProgress turns the terminal progress off as the first write of the exit's stop (interactive-mode.ts:7285-7287). It writes at once: the stop holds the
// terminal, and the terminal's own SetProgress would queue the clear behind it, after the stop's bytes. A terminal with no gate has no transaction: the clear is
// setTerminalProgress's.
func (m *InteractiveMode) stopTerminalProgress(tx *tui.StopTransaction) {
	if tx == nil {
		m.setTerminalProgress(false)
		return
	}
	tx.SetProgress(false)
}

// exitEndsAtStop reports whether the process ends right after the exit's stop writes its last byte: a signal's exit does (Pi's process.exit(0),
// interactive-mode.ts:4493-4494), a quit by keys goes on to its dispose and the resume hint (:4506-4513).
func (m *InteractiveMode) exitEndsAtStop() bool {
	return !m.quitByKeys && m.signalShutdownDone.Load()
}

// loopLife records whether the owner loop of one Run still runs, and lets a caller that waits for the loop to take its task learn that the loop ended. The zero value is a loop that has not ended.
type loopLife struct {
	mu   sync.Mutex
	done bool
	// ch is closed once the loop ended; it is made on first use, so a mode that never runs allocates none.
	ch chan struct{}
}

// begin starts a new life: a Run entered again on the same mode after Stop gets a channel of its own.
func (l *loopLife) begin() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.done {
		l.done = false
		l.ch = nil
	}
}

// end marks the loop ended and releases every waiter; it does nothing when the loop already ended.
func (l *loopLife) end() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.done {
		return
	}
	l.done = true
	if l.ch == nil {
		l.ch = make(chan struct{})
	}
	close(l.ch)
}

// ended reports whether the loop ended.
func (l *loopLife) ended() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.done
}

// endedCh is closed when the loop ends. A channel that was asked for before begin restarted the life belongs to the earlier life.
func (l *loopLife) endedCh() <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ch == nil {
		l.ch = make(chan struct{})
		if l.done {
			close(l.ch)
		}
	}
	return l.ch
}
