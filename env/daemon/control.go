package daemon

import (
	"context"
	"sync"
	"sync/atomic"
)

// control is the cancellation of one request, registered before the request runs so a cancel that arrives first is
// not lost.
type control struct {
	aborted atomic.Bool
	killed  atomic.Bool

	mu     sync.Mutex
	cancel context.CancelFunc
	kill   chan struct{}
}

func newControl() *control { return &control{kill: make(chan struct{})} }

// cancelRequest aborts the request; with kill it only kills its command, which then settles with the killed process's
// status, as cleanup() does.
func (c *control) cancelRequest(kill bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if kill {
		if !c.killed.Swap(true) {
			close(c.kill)
		}
		return
	}
	c.aborted.Store(true)
	if c.cancel != nil {
		c.cancel()
	}
}

// attach connects the running request's context, which a cancel that came first has already aborted.
func (c *control) attach(cancel context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancel = cancel
	if c.aborted.Load() {
		cancel()
	}
}
