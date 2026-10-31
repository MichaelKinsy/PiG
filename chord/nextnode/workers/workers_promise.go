package workers

import (
	"context"
	"sync"
)

// promise is a Promise stored and awaited by several parties (a child's `started` and `ready`, an address's start, a
// reload's reply): settled once, by value or by error; the first settle wins. Reactions run on the settling goroutine
// before the waiters resume, in the order they were added: Pi runs the reactions registered on a Promise in registration
// order before the continuations of those awaiting it later. It ports no Pi file.
type promise[T any] struct {
	done chan struct{}

	mu        sync.Mutex
	settled   bool
	value     T
	err       error
	reactions []func()
}

func newPromise[T any]() *promise[T] { return &promise[T]{done: make(chan struct{})} }

// resolve settles the promise with value; it reports whether it did (false when it was settled already).
func (p *promise[T]) resolve(value T) bool { return p.settle(value, nil) }

// reject settles the promise with err; it reports whether it did.
func (p *promise[T]) reject(err error) bool {
	var zero T
	return p.settle(zero, err)
}

func (p *promise[T]) settle(value T, err error) bool {
	p.mu.Lock()
	if p.settled {
		p.mu.Unlock()
		return false
	}
	p.settled = true
	p.value, p.err = value, err
	reactions := p.reactions
	p.reactions = nil
	p.mu.Unlock()
	for _, reaction := range reactions {
		reaction()
	}
	close(p.done)
	return true
}

// then adds a reaction that runs when the promise settles (at once when it has).
func (p *promise[T]) then(reaction func()) {
	p.mu.Lock()
	if !p.settled {
		p.reactions = append(p.reactions, reaction)
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	reaction()
}

// wait blocks until the promise settles.
func (p *promise[T]) wait() (T, error) {
	<-p.done
	return p.value, p.err
}

// await is `awaitWithContext(promise, ctx)`: the outcome, or ctx's cause when it aborts first (the work goes on).
func (p *promise[T]) await(ctx context.Context) (T, error) {
	if ctx.Err() != nil {
		var zero T
		return zero, context.Cause(ctx)
	}
	select {
	case <-p.done:
		return p.value, p.err
	case <-ctx.Done():
		var zero T
		return zero, context.Cause(ctx)
	}
}

// isSettled reports whether the promise has settled.
func (p *promise[T]) isSettled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.settled
}

// taskGroup is the goroutines a component owns: Close joins them. Unlike a sync.WaitGroup it takes a task while another
// goroutine waits, as a start queued behind Close's does.
type taskGroup struct {
	mu      sync.Mutex
	running int
	idle    *sync.Cond
}

func (g *taskGroup) add() {
	g.mu.Lock()
	g.running++
	g.mu.Unlock()
}

func (g *taskGroup) done() {
	g.mu.Lock()
	g.running--
	if g.running == 0 && g.idle != nil {
		g.idle.Broadcast()
	}
	g.mu.Unlock()
}

// wait blocks until no task runs.
func (g *taskGroup) wait() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.idle == nil {
		g.idle = sync.NewCond(&g.mu)
	}
	for g.running > 0 {
		g.idle.Wait()
	}
}
