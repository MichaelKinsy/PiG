package codingagent

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
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
