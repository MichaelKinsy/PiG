package host

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
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

// scopeBinding is the acquired a scopeHost returns: its release is counted.
type scopeBinding struct {
	mu        sync.Mutex
	reference any
	releases  int
}

func (b *scopeBinding) Release() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.releases++
}

func (b *scopeBinding) Reference() any { return b.reference }

func (b *scopeBinding) released() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.releases
}

// scopeAcquire is what a scopeHost's Acquire got.
type scopeAcquire struct {
	Type    *contracts.Contract
	params  any
	caller  caller
	ctx     context.Context
	options GetOptions
}

// scopeHost is a callHost and routineHost whose Acquire is the test's: `acquire` runs in place of `host.acquire`.
type scopeHost struct {
	loopOf      *scopeLoop
	incarnation string
	acquire     func(call scopeAcquire) (acquired, error)

	mu       sync.Mutex
	acquires []scopeAcquire
	reports  []HostError
}

var _ routineHost = (*scopeHost)(nil)

func newScopeHost(loop *scopeLoop) *scopeHost {
	return &scopeHost{loopOf: loop, incarnation: "host-incarnation"}
}

func (h *scopeHost) Incarnation() string { return h.incarnation }
func (h *scopeHost) loop() sectionLoop   { return h.loopOf }

func (h *scopeHost) Acquire(Type *contracts.Contract, params any, from caller, ctx context.Context, options GetOptions) (acquired, error) {
	call := scopeAcquire{Type: Type, params: params, caller: from, ctx: ctx, options: options}
	h.mu.Lock()
	h.acquires = append(h.acquires, call)
	acquire := h.acquire
	h.mu.Unlock()
	if acquire == nil {
		return &scopeBinding{reference: params}, nil
	}
	return acquire(call)
}

func (h *scopeHost) Report(err HostError) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reports = append(h.reports, err)
}

func (h *scopeHost) acquireCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.acquires)
}

func (h *scopeHost) reported() []HostError {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]HostError{}, h.reports...)
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

// scopeCounting is a context.Context with a Done channel whose AfterFunc registrations are counted
// (`getEventListeners(signal, "abort")`): context.AfterFunc calls its AfterFunc method when it has one.
type scopeCounting struct {
	context.Context
	mu     sync.Mutex
	active int
}

func newScopeCounting() (*scopeCounting, func(cause error)) {
	parent, cancel := chordctx.WithCancel(chordctx.Background())
	return &scopeCounting{Context: parent}, cancel
}

func (c *scopeCounting) AfterFunc(f func()) func() bool {
	c.mu.Lock()
	c.active++
	c.mu.Unlock()
	stop := context.AfterFunc(c.Context, func() {
		c.mu.Lock()
		c.active--
		c.mu.Unlock()
		f()
	})
	return func() bool {
		stopped := stop()
		if stopped {
			c.mu.Lock()
			c.active--
			c.mu.Unlock()
		}
		return stopped
	}
}

func (c *scopeCounting) listeners() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}
