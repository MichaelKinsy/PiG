//go:build !tinygo

package codemode

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// engineLoader returns the compiled module for VMs with the given heap limit.
type engineLoader func(ctx context.Context, memoryLimit uint32) (*engine, error)

// launchWorker runs the VM on its own goroutine in this process: the counterpart of the worker thread of
// `CodemodeSandbox`. A fresh VM per run keeps termination simple: a runaway script, including one that only spins the
// job queue, is stopped through the interrupt flag and, when it does not reach a poll, by halting the instance. Stopping
// requests termination without waiting for exit, so a VM inside a call that does not poll the flag can outlive the
// script's result until the grace period halts it.
//
// Ports packages/codemode/src/runtime/host.ts (workerLauncher) and packages/codemode/src/runtime/worker.ts.
func (s *Sandbox) launchWorker(ctx context.Context, data VmStartData, events vmEvents) (vmChannel, error) {
	eng, err := s.engineFor(ctx, data.MemoryLimitBytes)
	if err != nil {
		return nil, fmt.Errorf("Failed to load QuickJS: %w", err)
	}
	var release func()
	if s.options.AcquireWorker != nil {
		if release, err = s.options.AcquireWorker(); err != nil {
			return nil, err
		}
	}
	w := &workerChannel{
		ctx: ctx, engine: eng, prelude: s.prelude, data: data, events: events, release: release,
		results: make(chan callResult), stopped: make(chan struct{}), exited: make(chan struct{}),
	}
	if s.options.OnWorkerStarted != nil {
		if err := s.options.OnWorkerStarted(w.exited); err != nil {
			// The VM goroutine has not started, so the worker exits here.
			w.exit()
			return nil, err
		}
	}
	events.ready()
	return w, nil
}

// workerChannel is the host's handle on a VM that runs on its own goroutine, which start begins. The goroutine owns the
// VM: stop only raises the interrupt flag and, after the grace period, halts the instance.
type workerChannel struct {
	ctx       context.Context
	engine    *engine
	prelude   string
	data      VmStartData
	events    vmEvents
	release   func()
	interrupt atomic.Bool
	guard     haltGuard
	results   chan callResult
	stopped   chan struct{}
	exited    chan struct{}
	once      sync.Once
}

//portlint:allow golifetime owned by stopped/exited: stop closes stopped and arms the halt guard, which bounds run; run closes exited on return
func (w *workerChannel) start() { go w.run() }

// exit releases the worker's admission and reports its exit. It runs once: when run returns, or in launchWorker when the
// worker is refused before run starts.
func (w *workerChannel) exit() {
	if w.release != nil {
		w.release()
	}
	//portlint:allow doubleclose exit runs once: from run, which start begins once per channel (execution.go:187, remote_serve.go:128 right after launch), or from launchWorker before it returns the channel
	close(w.exited)
}

func (w *workerChannel) run() {
	defer w.exit()
	defer w.guard.exited()
	session, err := startVM(startVMOptions{
		ctx: w.ctx, engine: w.engine, prelude: w.prelude, data: w.data,
		post:            w.events.message,
		shouldInterrupt: w.interrupt.Load,
		created:         w.guard.created,
	})
	if err != nil {
		w.events.failure(&Error{Kind: ErrorSandbox, Message: "Failed to start the VM: " + err.Error()})
		return
	}
	defer session.dispose()
	for {
		select {
		case r := <-w.results:
			session.deliver(r)
		case <-w.stopped:
			return
		}
	}
}

func (w *workerChannel) post(r callResult) {
	select {
	case w.results <- r:
	case <-w.stopped:
	}
}

// stop raises the interrupt flag and escalates to halting the instance if the script does not reach an interrupt check.
// It does not wait for the VM to exit; OnWorkerStarted's channel reports that.
func (w *workerChannel) stop() {
	w.once.Do(func() {
		w.interrupt.Store(true)
		w.guard.arm()
		close(w.stopped)
	})
}
