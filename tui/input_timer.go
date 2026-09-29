package tui

import (
	"os"
	"time"
)

func newProcessStdinBuffer() *StdinBuffer {
	return NewStdinBuffer(StdinBufferOptions{EscapeTimeout: time.Duration(ResolveEscapeTimeoutMs(os.Getenv) * float64(time.Millisecond))})
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
