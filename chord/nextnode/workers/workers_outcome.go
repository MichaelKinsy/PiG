package workers

import (
	"context"
	"sync"
	"sync/atomic"
)

// outcome is the result of a piece of work that several parties wait for (a child's `started` and `ready`, an address's
// start, a reload's reply): settled once, by value or by error, the first settle winning. It is Go's form of a stored
// Promise: a closed channel, with the fields written before the close. It ports no Pi file.
type outcome[T any] struct {
	done    chan struct{}
	settled atomic.Bool
	value   T
	err     error
}

func newOutcome[T any]() *outcome[T] { return &outcome[T]{done: make(chan struct{})} }

// resolve settles the outcome with value; it reports whether it did (false when it was settled already).
func (o *outcome[T]) resolve(value T) bool { return o.settle(value, nil) }

// reject settles the outcome with err; it reports whether it did.
func (o *outcome[T]) reject(err error) bool {
	var zero T
	return o.settle(zero, err)
}

func (o *outcome[T]) settle(value T, err error) bool {
	if !o.settled.CompareAndSwap(false, true) {
		return false
	}
	o.value, o.err = value, err
	close(o.done)
	return true
}

// wait blocks until the outcome is settled.
func (o *outcome[T]) wait() (T, error) {
	<-o.done
	return o.value, o.err
}

// await is `awaitWithContext(promise, ctx)`: the outcome, or ctx's cause when it aborts first (the work goes on).
func (o *outcome[T]) await(ctx context.Context) (T, error) {
	if ctx.Err() != nil {
		var zero T
		return zero, context.Cause(ctx)
	}
	select {
	case <-o.done:
		return o.value, o.err
	case <-ctx.Done():
		var zero T
		return zero, context.Cause(ctx)
	}
}

// isSettled reports whether the outcome is settled.
func (o *outcome[T]) isSettled() bool {
	select {
	case <-o.done:
		return true
	default:
		return false
	}
}

// taskGroup is the goroutines a component owns: Close joins them. Unlike a sync.WaitGroup it takes a task while another
// goroutine waits, as a start queued behind Close's does.
type taskGroup struct {
	mu      sync.Mutex
	running int
	// idle is closed when the running count reaches zero, and replaced by the next task.
	idle chan struct{}
}

func (g *taskGroup) add() {
	g.mu.Lock()
	if g.running == 0 {
		g.idle = make(chan struct{})
	}
	g.running++
	g.mu.Unlock()
}

func (g *taskGroup) done() {
	g.mu.Lock()
	g.running--
	if g.running == 0 {
		close(g.idle)
	}
	g.mu.Unlock()
}

// wait blocks until no task runs, or until ctx ends: it returns ctx's cause then (the tasks go on).
func (g *taskGroup) wait(ctx context.Context) error {
	for {
		g.mu.Lock()
		idle := g.idle
		running := g.running
		g.mu.Unlock()
		if running == 0 {
			return nil
		}
		select {
		case <-idle:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}
