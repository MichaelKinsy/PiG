package runtime

import (
	"context"
	"sync"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
)

// LoadGate is the worker's "every plugin is loaded" gate (the Host.Load of the worker entry returned: each plugin's
// extensions, codemode's tool among them, are contributed).
//
// Why it exists. Pi's worker is one JS event loop: the HarnessHost object goes live, and the continuations that make the
// objects behind it live (codemode contributing its tool) run before the loop dispatches the next IPC request. The server
// routes a `send` as soon as the worker has started, before it is ready, and a send's Submit resumes scheduling, whose
// reserve pass snapshots the registry to plan the tools of the first request. In Go the request is served on its own
// goroutine, so it can land between HarnessHost going live and codemode contributing: the snapshot lacks codemode and the
// model's first codemode call is unoffered ("Tool codemode is not available"). The gate restores Pi's order without changing
// when Submit resumes or when the scheduler snapshots.
//
// Async contract.
//   - Who waits: request-time users of the Harness (the chat methods, Chat.send and its routers: HarnessParts.AwaitLoaded).
//     Liveness-time users do NOT wait: a facet's own Durable() (codemode's) runs while Load is still running, and the gate
//     opens only after Load, so waiting there would deadlock the load.
//   - Ordering: Finish is called once, right after worker.Load returns (before the Harness is resumed and before the worker
//     reports ready); every waiter returns after that.
//   - Cancel: a waiter whose ctx is cancelled returns the cancellation cause to its caller; the gate stays closed for the
//     others. The wait parks the handler first (chordctx.Suspend): Load needs the host's loop.
//   - Error: a failed Load finishes the gate with that error, which every waiter returns (not a hang).
//
// A nil *LoadGate is open (test hosts without a worker entry).
type LoadGate struct {
	once sync.Once
	done chan struct{}
	err  error

	blockOnce sync.Once
	blocked   chan struct{}
}

// NewLoadGate is a closed gate: Finish opens it.
func NewLoadGate() *LoadGate {
	return &LoadGate{done: make(chan struct{}), blocked: make(chan struct{})}
}

// Finish opens the gate with the outcome of the worker's Load (nil: loaded). Only the first call counts.
func (g *LoadGate) Finish(err error) {
	if g == nil {
		return
	}
	g.once.Do(func() {
		g.err = err
		close(g.done)
	})
}

// Blocked is closed once a request has started to wait for the gate (a nil gate: never): what a test that must act while
// a request waits, and not before, observes.
func (g *LoadGate) Blocked() <-chan struct{} {
	if g == nil {
		return nil
	}
	return g.blocked
}

// Wait blocks until Finish: its error, or ctx's cancellation cause.
func (g *LoadGate) Wait(ctx context.Context) error {
	if g == nil {
		return nil
	}
	select {
	case <-g.done:
		return g.err
	default:
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	g.blockOnce.Do(func() { close(g.blocked) })
	chordctx.Suspend(ctx)
	select {
	case <-g.done:
		return g.err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// AwaitLoaded waits until the worker has loaded every plugin (see LoadGate). Request-time users call it before they touch
// the Harness; the Durable of a facet that is itself loading does not.
func (p *HarnessParts) AwaitLoaded(ctx context.Context) error { return p.Loaded.Wait(ctx) }
