//go:build !tinygo

package codemode

import "context"

// ServeRemote is the remote end of RemoteSandbox: it runs one execution's VM on the calling goroutine under an interrupt
// budget and forwards everything it reports through exchange, asking the client for the result of every tool call. It
// returns once the VM has stopped and the terminal message has been delivered, or the client asked to stop. It never
// fails: whatever goes wrong is reported to the client, or ends the execution when the client is gone.
//
// A Remote usually implements Execute as `ServeRemote(ctx, request, exchange, options)` with the limits of its host. The
// script's request is capped at those limits: see ServeOptions.
//
// Go mechanics, not divergences: ctx ends the execution (the client cancels the context of Remote.Execute once it
// finished), also while a script computes, where upstream's remote runs the VM to its budget.
//
// Ports packages/codemode/src/runtime/remote.ts (serveCodemodeRemote).
func ServeRemote(ctx context.Context, request RemoteRequest, exchange RemoteExchange, options ServeOptions) {
	serveRemote(ctx, request, exchange, options, func(budget int) vmLauncher {
		return inlineLauncher(wasmEngineLoader(options.Wasm, options.CacheDir), PreludeSource, budget)
	})
}

// close drops the jobs that are still queued and every later one.
func (l *serveLoop) close() {
	l.mu.Lock()
	l.closed = true
	l.jobs = nil
	l.mu.Unlock()
}

// serveRemote runs one execution's VM and forwards everything it reports through exchange. It returns once the VM has
// stopped and the terminal message has been delivered, or the client asked to stop, or ctx ended. launch starts the
// VM with the request's interrupt budget.
//
// Ports packages/codemode/src/runtime/remote.ts (serveCodemodeRemote).
func serveRemote(ctx context.Context, request RemoteRequest, exchange RemoteExchange, options ServeOptions, launch func(budget int) vmLauncher) {
	if ctx.Err() != nil {
		return
	}
	s := startRemoteServer(ctx, request, exchange, options, launch)
	defer s.loop.close()
	if s.channel != nil {
		// A context that ends interrupts a VM that is still computing.
		defer context.AfterFunc(ctx, s.stopChannel)()
	}
	for !s.resolved {
		if job := s.loop.next(); job != nil {
			job()
			continue
		}
		select {
		case <-s.loop.wake:
		case <-ctx.Done():
			// The client went away: nobody is left to receive a terminal message, as after a stop reply.
			s.resolve()
		}
	}
}
