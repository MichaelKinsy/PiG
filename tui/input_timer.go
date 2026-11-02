package tui

// Ports packages/tui/src/terminal.ts (the escape and negotiation timers of ProcessTerminal's stdin buffer).

import (
	"os"
	"time"
)

func newProcessStdinBuffer() *StdinBuffer {
	return NewStdinBuffer(StdinBufferOptions{EscapeTimeout: nodeTimerDelay(ResolveEscapeTimeoutMs(os.Getenv))})
}

// inputTimer belongs to its input-loop owner; cancellation never dispatches a callback on another goroutine.
type inputTimer struct {
	timer *time.Timer
	C     <-chan time.Time
}

func (f *inputTimer) arm(deadline time.Time) {
	f.stop()
	if !deadline.IsZero() {
		f.timer = time.NewTimer(time.Until(deadline))
		f.C = f.timer.C
	}
}

func (f *inputTimer) stop() {
	if f.timer != nil {
		f.timer.Stop()
	}
	f.timer = nil
	f.C = nil
}

func stdinBufferDeadline(b *StdinBuffer) time.Time {
	if b.HasPendingFlush() {
		return time.Now().Add(b.FlushTimeout())
	}
	return time.Time{}
}
