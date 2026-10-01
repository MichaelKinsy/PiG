package ai

import (
	"context"
	"slices"
	"sync"
)

type continuationExecutorKey struct{}
type continuationTurnKey struct{}

func withContinuationExecutor(ctx context.Context) (context.Context, *continuationExecutor) {
	if executor, ok := ctx.Value(continuationExecutorKey{}).(*continuationExecutor); ok {
		return ctx, executor
	}
	executor := &continuationExecutor{}
	return context.WithValue(ctx, continuationExecutorKey{}, executor), executor
}

// continuationExecutor orders promise reactions. A turn owns execution until it awaits or returns; posting a reaction never interrupts the current turn.
type continuationExecutor struct {
	mu          sync.Mutex
	ready       []func()
	ticks       []func()
	tickPhase   bool
	external    []func()
	watchers    []*continuationWatcher
	draining    bool
	active      *continuationTurn
	observation *StreamObservation
	// trace observes scheduling boundaries in tests; production leaves it nil.
	trace *executorTrace
}

// executorTrace reports the executor's macrotask boundaries and each pushed stream event.
type executorTrace struct {
	// external runs before each external completion, the analogue of one Node event-loop macrotask.
	external func()
	// idle runs when no reaction is ready and no turn owns execution.
	idle func()
	// push runs before a stream event is queued.
	push func(*AssistantMessageEventStream, AssistantMessageEvent)
	// microtask runs before each ready reaction, the analogue of one Node microtask job.
	microtask func()
}

type continuationTurn struct {
	executor  *continuationExecutor
	permit    chan bool
	canceled  chan struct{}
	claimed   bool
	abandoned bool
	// producer is the observation of a provider response turn, set while that turn runs its body. A host callback the provider invokes (an onResponse hook reaching an extension) awaits through it.
	producer *StreamObservation
}

type continuationPromise[T any] struct {
	executor *continuationExecutor
	resolved bool
	value    T
	then     []func(T)
}

func (executor *continuationExecutor) post(reaction func()) {
	if drain := executor.postDeferred(reaction); drain != nil {
		drain()
	}
}

func (executor *continuationExecutor) postDeferred(reaction func()) func() {
	executor.mu.Lock()
	executor.ready = append(executor.ready, reaction)
	drain := executor.startDrainLocked()
	executor.mu.Unlock()
	if drain {
		return executor.drain
	}
	return nil
}

// postTickDeferred queues a process.nextTick callback: it runs after every ready reaction and before any I/O completion.
func (executor *continuationExecutor) postTickDeferred(reaction func()) func() {
	executor.mu.Lock()
	executor.ticks = append(executor.ticks, reaction)
	drain := executor.startDrainLocked()
	executor.mu.Unlock()
	if drain {
		return executor.drain
	}
	return nil
}

func (executor *continuationExecutor) postExternalDeferred(reaction func()) func() {
	executor.mu.Lock()
	executor.external = append(executor.external, reaction)
	drain := executor.startDrainLocked()
	executor.mu.Unlock()
	if drain {
		return executor.drain
	}
	return nil
}

func (executor *continuationExecutor) postExternal(reaction func()) {
	executor.mu.Lock()
	executor.external = append(executor.external, reaction)
	drain := executor.startDrainLocked()
	executor.mu.Unlock()
	if drain {
		executor.drain()
	}
}

func (executor *continuationExecutor) startDrainLocked() bool {
	if executor.draining || executor.active != nil || executor.idleLocked() {
		return false
	}
	executor.draining = true
	return true
}

func (executor *continuationExecutor) idleLocked() bool {
	return len(executor.ready) == 0 && len(executor.ticks) == 0 && len(executor.external) == 0
}

// popLocked takes the next reaction in Node's order: after an I/O completion its process.nextTick callbacks run first, then every ready promise reaction; microtasks run to exhaustion before the tick queue runs again, and I/O completions come last (lib/internal/process/task_queues.js processTicksAndRejections). external reports an I/O completion, which starts a new event-loop macrotask.
func (executor *continuationExecutor) popLocked() (reaction func(), kind reactionKind) {
	if executor.tickPhase {
		if len(executor.ticks) != 0 {
			return popReaction(&executor.ticks), reactionTick
		}
		executor.tickPhase = false
	}
	switch {
	case len(executor.ready) != 0:
		return popReaction(&executor.ready), reactionMicrotask
	case len(executor.ticks) != 0:
		executor.tickPhase = true
		return popReaction(&executor.ticks), reactionTick
	default:
		executor.tickPhase = true
		return popReaction(&executor.external), reactionExternal
	}
}

// reactionKind tells the trace which queue a reaction came from.
type reactionKind uint8

const (
	reactionMicrotask reactionKind = iota
	reactionTick
	reactionExternal
)

func popReaction(queue *[]func()) func() {
	reaction := (*queue)[0]
	(*queue)[0] = nil
	*queue = (*queue)[1:]
	if len(*queue) == 0 {
		*queue = nil
	}
	return reaction
}

// continuationWatcher runs fire as a ready reaction once its context is done.
type continuationWatcher struct {
	ctx  context.Context
	fire func()
}

// watch arms fire for ctx's cancellation. A JavaScript abort() takes effect in the job that calls it, and a Go cancel func has no hook, so the executor polls the armed contexts at every reaction boundary: the effect is queued as soon as the turn that canceled releases execution, ahead of any external completion. The returned func disarms the watcher.
func (executor *continuationExecutor) watch(ctx context.Context, fire func()) (disarm func()) {
	watcher := &continuationWatcher{ctx: ctx, fire: fire}
	executor.mu.Lock()
	executor.watchers = append(executor.watchers, watcher)
	executor.mu.Unlock()
	return func() {
		executor.mu.Lock()
		if index := slices.Index(executor.watchers, watcher); index >= 0 {
			executor.watchers = slices.Delete(executor.watchers, index, index+1)
		}
		executor.mu.Unlock()
	}
}

// pollWatchersLocked queues the reaction of every armed watcher whose context is done, in arming order.
func (executor *continuationExecutor) pollWatchersLocked() {
	for index := 0; index < len(executor.watchers); {
		watcher := executor.watchers[index]
		if watcher.ctx.Err() == nil {
			index++
			continue
		}
		executor.ready = append(executor.ready, watcher.fire)
		executor.watchers = slices.Delete(executor.watchers, index, index+1)
	}
}

func (executor *continuationExecutor) drain() {
	for {
		executor.mu.Lock()
		executor.pollWatchersLocked()
		if executor.active != nil || executor.idleLocked() {
			executor.draining = false
			idle := executor.active == nil && executor.trace != nil && executor.trace.idle != nil
			executor.mu.Unlock()
			if idle {
				executor.trace.idle()
			}
			return
		}
		reaction, kind := executor.popLocked()
		trace := executor.trace
		executor.mu.Unlock()
		if trace != nil {
			if kind == reactionExternal && trace.external != nil {
				trace.external()
			}
			if kind == reactionMicrotask && trace.microtask != nil {
				trace.microtask()
			}
		}
		reaction()
	}
}

func (executor *continuationExecutor) newTurn() *continuationTurn {
	turn := executor.newDeferredTurn()
	turn.resume()
	return turn
}

// newDeferredTurn creates a turn whose owner grants it from its own promise reaction.
func (executor *continuationExecutor) newDeferredTurn() *continuationTurn {
	return &continuationTurn{executor: executor, permit: make(chan bool, 1), canceled: make(chan struct{})}
}

func (executor *continuationExecutor) run(body func(*continuationTurn)) {
	executor.newTurn().run(body)
}

func (turn *continuationTurn) run(body func(*continuationTurn)) {
	select {
	case acquired := <-turn.permit:
		if !acquired {
			return
		}
	case <-turn.canceled:
		return
	}
	turn.executor.mu.Lock()
	if turn.abandoned {
		turn.executor.mu.Unlock()
		return
	}
	turn.claimed = true
	turn.executor.mu.Unlock()
	defer turn.release()
	body(turn)
}

func (turn *continuationTurn) resume() {
	turn.executor.post(turn.grant)
}

func (turn *continuationTurn) grant() {
	turn.executor.mu.Lock()
	if turn.abandoned {
		turn.executor.mu.Unlock()
		turn.permit <- false
		return
	}
	if turn.executor.active != nil {
		turn.executor.mu.Unlock()
		panic("stream continuation resumed during another turn")
	}
	turn.executor.active = turn
	turn.executor.mu.Unlock()
	turn.permit <- true
}

func (turn *continuationTurn) abandon() bool {
	executor := turn.executor
	executor.mu.Lock()
	if turn.claimed {
		executor.mu.Unlock()
		return false
	}
	if !turn.abandoned {
		turn.abandoned = true
		close(turn.canceled)
	}
	if executor.active == turn {
		executor.active = nil
	}
	drain := executor.startDrainLocked()
	executor.mu.Unlock()
	if drain {
		executor.drain()
	}
	return true
}

func (turn *continuationTurn) release() {
	executor := turn.executor
	executor.mu.Lock()
	if executor.active != turn {
		executor.mu.Unlock()
		panic("stream continuation released without ownership")
	}
	executor.active = nil
	drain := executor.startDrainLocked()
	executor.mu.Unlock()
	if drain {
		executor.drain()
	}
}

func newContinuationPromise[T any](executor *continuationExecutor) *continuationPromise[T] {
	return &continuationPromise[T]{executor: executor}
}

func (promise *continuationPromise[T]) resolve(value T) {
	if drain := promise.resolveDeferred(value); drain != nil {
		drain()
	}
}

func (promise *continuationPromise[T]) resolveDeferred(value T) func() {
	executor := promise.executor
	executor.mu.Lock()
	if promise.resolved {
		executor.mu.Unlock()
		return nil
	}
	promise.resolved = true
	promise.value = value
	for _, reaction := range promise.then {
		executor.ready = append(executor.ready, func() { reaction(value) })
	}
	promise.then = nil
	drain := executor.startDrainLocked()
	executor.mu.Unlock()
	if drain {
		return executor.drain
	}
	return nil
}

func (promise *continuationPromise[T]) onResolved(reaction func(T)) {
	executor := promise.executor
	executor.mu.Lock()
	if !promise.resolved {
		promise.then = append(promise.then, reaction)
		executor.mu.Unlock()
		return
	}
	value := promise.value
	executor.ready = append(executor.ready, func() { reaction(value) })
	drain := executor.startDrainLocked()
	executor.mu.Unlock()
	if drain {
		executor.drain()
	}
}

func awaitContinuation[T any](turn *continuationTurn, promise *continuationPromise[T]) T {
	if turn.executor != promise.executor {
		panic("stream continuation awaited a different executor")
	}
	var result T
	promise.onResolved(func(value T) {
		result = value
		turn.grant()
	})
	// The executor's observation is the running turn's. Other turns replace it while this one waits, so a turn that owns the observation takes it back when it resumes; a host call it makes next (a result wait, a nested await) then releases the turn that runs it.
	turn.executor.mu.Lock()
	observation := turn.executor.observation
	if observation != nil && observation.turn != turn {
		observation = nil
	}
	turn.executor.mu.Unlock()
	turn.release()
	<-turn.permit
	if observation != nil {
		turn.executor.mu.Lock()
		turn.executor.observation = observation
		turn.executor.mu.Unlock()
	}
	return result
}
