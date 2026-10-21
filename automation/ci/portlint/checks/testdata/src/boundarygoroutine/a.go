package boundarygoroutine

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type host struct {
	onEvent func(name string)
	onClose func()
	handler extension.Handler
	wg      sync.WaitGroup
}

// Bad: a field callback with an argument runs on a goroutine of its own.
func (h *host) badCallbackField() {
	go func() { // want `goroutine invokes the callback h.onEvent`
		h.onEvent("x")
	}()
}

// Bad: a func-typed parameter is invoked.
func badCallbackParam(report func(error)) {
	go func() { // want `goroutine invokes the callback report`
		report(nil)
	}()
}

// Bad: the go statement itself starts a callback.
func badGoCallback(cb func(int)) {
	go cb(1) // want `goroutine invokes the callback cb`
}

// Bad: a method of an extension interface.
func (h *host) badInterface() {
	go func() { // want `goroutine calls extension.Handler.Handle, a method of an extension or host interface`
		h.handler.Handle("x")
	}()
}

// Bad: the callback sits one static call below the goroutine body.
func (h *host) badIndirect() {
	go h.dispatch() // want `goroutine invokes the callback h.onEvent`
}

func (h *host) dispatch() { h.fire() }

func (h *host) fire() { h.onEvent("x") }

// Bad: a constructor starts work before its caller registered a consumer.
func newChannel() *host {
	h := &host{}
	go h.drain() // want `go statement in constructor newChannel starts work before the caller can register its consumer`
	return h
}

// NewHost is flagged whatever its goroutine reaches.
func NewHost() *host {
	h := &host{}
	go func() {}() // want `go statement in constructor NewHost`
	return h
}

func (h *host) drain() {}

// Good: a context cancel function is lifecycle, not a callback.
func goodCancel(cancel context.CancelFunc) {
	go func() { cancel() }()
}

// Good: a callback with no parameter is a close hook.
func (h *host) goodCloseHook() {
	go func() { h.onClose() }()
}

// Good: a local closure the goroutine declares is not a callback from outside.
func goodLocal() {
	go func() {
		step := func(n int) int { return n + 1 }
		step(1)
	}()
}

// Good: plain work with no callback.
func (h *host) goodPlain() {
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		h.drain()
	}()
}

// Good: a callback beyond three static calls is out of reach.
func (h *host) goodTooDeep() {
	go h.l1()
}

func (h *host) l1() { h.l2() }
func (h *host) l2() { h.l3() }
func (h *host) l3() { h.l4() }
func (h *host) l4() { h.onEvent("x") }

// Good: the marker names its class and the finding is dropped.
func (h *host) goodMarked() {
	//portlint:allow boundarygoroutine lifecycle: close notification, no callback order is observable
	go func() { h.onEvent("closed") }()
}

// Good: a method named newline is not a constructor.
func newlineWorker(h *host) {
	go h.drain()
}
