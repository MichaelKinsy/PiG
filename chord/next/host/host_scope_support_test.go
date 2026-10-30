package host

import (
	"errors"
	"sync"
	"testing"
	"time"

)

// Test support of host_ctx, host_root, host_routines and host_route tests: a loop with vitest's fake timers and the real
// Loop's sections, and a fake callHost.

// scopeLoop is a sectionLoop whose clock and timers are fakeLoop's (`fakeTimers()`: Date.now() and setTimeout from 0, moved
// by Advance) and whose sections, microtasks and tasks are a real Loop's: a timer's job runs as a section, a microtask runs
// at the end of the section that queued it, as in Pi.
type scopeLoop struct {
	*fakeLoop
	real *Loop
}

var _ sectionLoop = (*scopeLoop)(nil)

func newScopeLoop() *scopeLoop { return &scopeLoop{fakeLoop: newFakeLoop(), real: NewLoop()} }

func (l *scopeLoop) Run(work func())  { l.real.Run(work) }
func (l *scopeLoop) Go(fn func())     { l.real.Go(fn) }
func (l *scopeLoop) Queue(job func()) { l.real.Queue(job) }
func (l *scopeLoop) barrier()         { l.real.Run(func() {}) }
func (l *scopeLoop) AfterFunc(d time.Duration, job func()) Timer {
	return l.fakeLoop.AfterFunc(d, func() { l.real.Run(job) })
}

// scopeEventually waits for cond, which a goroutine of the code under test makes true. It polls: the code under test hands
// off between goroutines (a watch's delivery, a task waiting for a close), and cond is the observable of that hand-off,
// never a duration the test asserts.
func scopeEventually(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("never happened: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// scopeReason is an abort reason: `new Error(text)` as an abort(reason) argument.
func scopeReason(text string) error { return errors.New(text) }

// scopeCounting is an abort signal (`new AbortController().signal`) whose listeners are counted
// (`getEventListeners(signal, "abort")`). It is its own context.Context, not one derived from the standard library's:
// context.AfterFunc calls the AfterFunc method of a context of another implementation, which is how a listener is
// counted, and Cause of such a context is its Err, which is the reason it was aborted with here.
type scopeCounting struct {
	mu        sync.Mutex
	done      chan struct{}
	reason    error
	listeners map[*func()]struct{}
}

func newScopeCounting() (*scopeCounting, func(cause error)) {
	signal := &scopeCounting{done: make(chan struct{}), listeners: map[*func()]struct{}{}}
	return signal, signal.abort
}

func (c *scopeCounting) abort(reason error) {
	c.mu.Lock()
	if c.reason != nil {
		c.mu.Unlock()
		return
	}
	c.reason = reason
	close(c.done)
	fired := c.listeners
	c.listeners = map[*func()]struct{}{}
	c.mu.Unlock()
	for listener := range fired {
		go (*listener)()
	}
}

func (c *scopeCounting) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *scopeCounting) Done() <-chan struct{}       { return c.done }
func (c *scopeCounting) Value(any) any               { return nil }
func (c *scopeCounting) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reason
}

// AfterFunc adds a listener: it runs once on a goroutine of its own when the signal is aborted, unless stopped first.
func (c *scopeCounting) AfterFunc(f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reason != nil {
		go f()
		return func() bool { return false }
	}
	listener := &f
	c.listeners[listener] = struct{}{}
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		_, pending := c.listeners[listener]
		delete(c.listeners, listener)
		return pending
	}
}

// count is `getEventListeners(signal, "abort").length`.
func (c *scopeCounting) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.listeners)
}
