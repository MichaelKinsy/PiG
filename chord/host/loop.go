package host

// The host's serialized executor. Upstream Chord runs on one JavaScript event loop: a synchronous section is atomic,
// and promise callbacks run later, in order, after the running section. A HostImpl gives its state the same
// discipline: host, object, reference and connection state is touched only by a task on the host's loop. A section runs
// with run (inline when the caller is already on the loop, so nested synchronous calls nest as upstream's do), a
// callback is queued with post, and an await is a Promise awaited off the loop.

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/internal/entered"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

type loop struct {
	// exec is held by the goroutine running the loop's tasks.
	exec  sync.Mutex
	owner atomic.Uint64

	mu        sync.Mutex
	queue     []func()
	scheduled bool

	// tasks counts owned background tasks; idle is signalled when it drops to zero.
	taskMu sync.Mutex
	tasks  int
	idle   chan struct{}

	// publications is the queue of the observer notifications of this loop's host (publish.go).
	publications publisher
}

// onLoop reports whether the caller is running a task of this loop.
func (loop *loop) onLoop() bool { return loop.owner.Load() == goroutineid.Current() }

// run runs task as a synchronous section of the loop and returns when it and the callbacks it queued have run. A
// goEntered task that the loop's owner awaits (enter) runs the section on the owner, as upstream runs a callback's
// synchronous part on its one thread; any other caller that has to wait for the loop has entered (goEntered).
func (loop *loop) run(task func()) {
	id := goroutineid.Current()
	if loop.owner.Load() == id {
		task()
		return
	}
	if !loop.exec.TryLock() {
		if lendSection(loop, id, task) {
			return
		}
		markEntered(id)
		loop.exec.Lock()
	}
	loop.owner.Store(id)
	defer func() {
		loop.owner.Store(0)
		loop.exec.Unlock()
	}()
	task()
	loop.drain()
}

// post queues task to run after the running section (a promise callback).
func (loop *loop) post(task func()) {
	loop.mu.Lock()
	loop.queue = append(loop.queue, task)
	start := !loop.scheduled
	loop.scheduled = true
	loop.mu.Unlock()
	if start {
		//portlint:allow golifetime the drain runs the queued sections and returns once the queue is empty; post starts the next one
		go func() {
			loop.exec.Lock()
			loop.owner.Store(goroutineid.Current())
			loop.drain()
			loop.owner.Store(0)
			loop.exec.Unlock()
		}()
	}
}

// drain runs queued tasks until the queue is empty. The caller holds exec.
func (loop *loop) drain() {
	for {
		loop.mu.Lock()
		if len(loop.queue) == 0 {
			loop.scheduled = false
			loop.mu.Unlock()
			return
		}
		task := loop.queue[0]
		loop.queue[0] = nil
		loop.queue = loop.queue[1:]
		loop.mu.Unlock()
		task()
	}
}

// goTask runs fn as an owned background task (an intentionally unawaited upstream promise): Close waits for it.
func (loop *loop) goTask(fn func()) {
	loop.taskMu.Lock()
	loop.tasks++
	loop.taskMu.Unlock()
	go func() {
		defer func() {
			loop.taskMu.Lock()
			loop.tasks--
			if loop.tasks == 0 && loop.idle != nil {
				close(loop.idle)
				loop.idle = nil
			}
			loop.taskMu.Unlock()
		}()
		fn()
	}()
}

// goEntered runs fn as an owned task and returns a channel that closes once fn has entered: it returned, or it waits.
// Upstream calls a function synchronously up to its first await, so the next of several started in upstream's order
// is started only after this one entered: work with no real wait completes, and queues its continuations, in start
// order, while work that really waits lets the next one run, as an await does. It may be waited on from the loop: a
// task that needs the loop waits for it and so has entered.
//
// A wait on any host (for its loop, a Promise, its tasks) marks the task entered at once. Any other wait (a channel,
// a timer, network I/O, a condition) is found from the goroutine's runtime state, polled after a short grace period
// with exponential backoff. A sync.Mutex or RWMutex wait is not a wait here: an upstream function cannot block on a
// lock, so a lock in Go code is part of its synchronous work.
func (loop *loop) goEntered(fn func()) <-chan struct{} {
	return loop.startEntering(fn, nil).entered
}

// enter runs fn as a goEntered task and waits, on the loop, until it entered. Meanwhile the task's sections run on the
// loop's owner (the caller): upstream runs the callback synchronously on its one thread, host calls included, so its
// synchronous part ends before anything else on the loop runs, however long it computes.
func (loop *loop) enter(fn func()) {
	awaiter := &loopAwaiter{loop: loop, lend: make(chan func()), stopped: make(chan struct{})}
	task := loop.startEntering(fn, awaiter)
	//portlint:allow doubleclose awaiter is local to this call and stopped is closed once, by this defer
	defer close(awaiter.stopped)
	for {
		//portlint:allow golifetime the loop ends when the task entered; lend only carries the task's own sections, which it sends while not entered
		select {
		case <-task.entered:
			return
		case section := <-awaiter.lend:
			section()
		}
	}
}

// loopAwaiter is the loop owner awaiting a goEntered task (enter): it runs the task's sections until it stops.
type loopAwaiter struct {
	loop    *loop
	lend    chan func()
	stopped chan struct{}
}

// lendSection runs section on the owner of loop when goroutine id runs a task that owner awaits, and reports whether
// it did. A panic in section is raised again in the task.
func lendSection(loop *loop, id uint64, section func()) bool {
	task := entering.get(id)
	if task == nil || task.awaiter == nil || task.awaiter.loop != loop {
		return false
	}
	done := make(chan struct{})
	var panicked any
	lent := func() {
		defer func() {
			panicked = recover()
			close(done)
		}()
		section()
	}
	task.lending.Store(true)
	defer task.lending.Store(false)
	select {
	case task.awaiter.lend <- lent:
	case <-task.awaiter.stopped:
		return false
	}
	<-done
	if panicked != nil {
		panic(panicked)
	}
	return true
}

func (loop *loop) startEntering(fn func(), awaiter *loopAwaiter) *enteringTask {
	task := &enteringTask{entered: make(chan struct{}), awaiter: awaiter}
	ids := make(chan uint64, 1)
	loop.goTask(func() {
		id := goroutineid.Current()
		entering.add(id, task)
		ids <- id
		defer func() {
			entering.remove(id)
			task.enter()
		}()
		fn()
	})
	loop.goTask(func() { task.watch(<-ids) })
	return task
}

// enteringTask is one goEntered task's entered signal.
type enteringTask struct {
	once    sync.Once
	entered chan struct{}
	// awaiter is the loop owner awaiting the task (enter); nil for a task awaited elsewhere.
	awaiter *loopAwaiter
	// lending is set while the task hands a section to its awaiter: that wait is not one of the task's own.
	lending atomic.Bool
}

func (task *enteringTask) enter() { task.once.Do(func() { close(task.entered) }) }

// watch is goEntered's slow path: it waits until goroutine id is parked in a wait (internal/entered, the shared
// park check, polled with backoff) or the task entered by another path.
func (task *enteringTask) watch(id uint64) {
	// A task handing a section to the loop owner that awaits it (lendSection) waits for that owner, not for itself:
	// entered.Await alone would count that wait as entered and deadlock the lending handshake.
	entered.AwaitUnless(id, task.entered, task.lending.Load)
	task.enter()
}

// enteringTasks are the running goEntered tasks by goroutine id, so a host wait on any host marks its caller's task.
type enteringTasks struct {
	count atomic.Int32
	mu    sync.Mutex
	tasks map[uint64]*enteringTask
}

var entering = enteringTasks{tasks: map[uint64]*enteringTask{}}

func (tasks *enteringTasks) add(id uint64, task *enteringTask) {
	tasks.mu.Lock()
	tasks.tasks[id] = task
	tasks.mu.Unlock()
	tasks.count.Add(1)
}

func (tasks *enteringTasks) get(id uint64) *enteringTask {
	if tasks.count.Load() == 0 {
		return nil
	}
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	return tasks.tasks[id]
}

func (tasks *enteringTasks) remove(id uint64) {
	tasks.count.Add(-1)
	tasks.mu.Lock()
	delete(tasks.tasks, id)
	tasks.mu.Unlock()
}

// markEntered marks goroutine id's goEntered task, if it runs one, as entered: it is about to wait on a host.
func markEntered(id uint64) {
	if entering.count.Load() == 0 {
		return
	}
	entering.mu.Lock()
	task := entering.tasks[id]
	entering.mu.Unlock()
	if task != nil {
		task.enter()
	}
}

// waitTasks blocks until no owned background task is running.
func (loop *loop) waitTasks() {
	loop.taskMu.Lock()
	if loop.tasks == 0 {
		loop.taskMu.Unlock()
		return
	}
	if loop.idle == nil {
		loop.idle = make(chan struct{})
	}
	idle := loop.idle
	loop.taskMu.Unlock()
	markEntered(goroutineid.Current())
	<-idle
}

// nodeTimeoutMax is Node's TIMEOUT_MAX (lib/internal/timers.js), the longest delay a timer honors: 2^31-1 ms.
const nodeTimeoutMax = (1<<31 - 1) * time.Millisecond

// nodeDelay is the wait Node's setTimeout and setInterval give a delay of d, as upstream's timers are Node timers:
// lib/internal/timers.js Timeout runs a delay outside [1 ms, TIMEOUT_MAX] after 1 ms (`if (!(after >= 1 && after <=
// TIMEOUT_MAX)) after = 1`), and insert truncates the rest to whole milliseconds (`msecs = MathTrunc(msecs)`). Every
// timer the host ports from upstream waits nodeDelay of its delay.
func nodeDelay(d time.Duration) time.Duration {
	if d < time.Millisecond || d > nodeTimeoutMax {
		return time.Millisecond
	}
	return d.Truncate(time.Millisecond)
}

// after posts fn once d has elapsed (setTimeout(fn, d)); stop cancels it if it has not fired. clearTimeout frees what the
// callback captured at once; a stopped Go timer does not: it stays in its P's timer heap, still holding its callback,
// until the runtime removes it (a zombie is purged when it reaches the head or outnumbers a quarter of the heap), which
// can be the timer's whole delay. So the timer holds only a slot that stop empties: a stopped timer retains the callback's
// captures (an object whose ready timeout is a minute away) for no time at all.
func (loop *loop) after(d time.Duration, fn func()) (stop func()) {
	var pending atomic.Pointer[func()]
	pending.Store(&fn)
	timer := time.AfterFunc(nodeDelay(d), func() {
		if due := pending.Swap(nil); due != nil {
			loop.post(*due)
		}
	})
	return func() {
		timer.Stop()
		pending.Store(nil)
	}
}

// Promise is a value or error settled once, on a loop. Callbacks registered with then run on the loop, in
// registration order, after the settling section, as upstream's promise reactions do.
type Promise[T any] struct {
	loop      *loop
	mu        sync.Mutex
	done      chan struct{}
	settled   bool
	value     T
	err       error
	reactions []func(T, error)
}

func newPromise[T any](loop *loop) (promise *Promise[T], resolve func(T), reject func(error)) {
	promise = &Promise[T]{loop: loop, done: make(chan struct{})}
	return promise, func(value T) { promise.settle(value, nil) }, func(err error) {
		var zero T
		promise.settle(zero, err)
	}
}

func resolvedPromise[T any](loop *loop, value T) *Promise[T] {
	promise, resolve, _ := newPromise[T](loop)
	resolve(value)
	return promise
}

func rejectedPromise[T any](loop *loop, err error) *Promise[T] {
	promise, _, reject := newPromise[T](loop)
	reject(err)
	return promise
}

func (promise *Promise[T]) settle(value T, err error) {
	promise.mu.Lock()
	if promise.settled {
		promise.mu.Unlock()
		return
	}
	promise.settled, promise.value, promise.err = true, value, err
	reactions := promise.reactions
	promise.reactions = nil
	close(promise.done)
	promise.mu.Unlock()
	for _, reaction := range reactions {
		promise.loop.post(func() { reaction(value, err) })
	}
}

// Settled reports whether the promise has settled.
func (promise *Promise[T]) Settled() bool {
	promise.mu.Lock()
	defer promise.mu.Unlock()
	return promise.settled
}

// then queues fn on the loop once the promise settles.
func (promise *Promise[T]) then(fn func(T, error)) {
	promise.mu.Lock()
	if !promise.settled {
		promise.reactions = append(promise.reactions, fn)
		promise.mu.Unlock()
		return
	}
	value, err := promise.value, promise.err
	promise.mu.Unlock()
	promise.loop.post(func() { fn(value, err) })
}

// Await blocks until the promise settles or ctx ends. It must not be called on the loop, which settles it.
func (promise *Promise[T]) Await(ctx context.Context) (T, error) {
	id := goroutineid.Current()
	if promise.loop.owner.Load() == id {
		panic("chord: a promise cannot be awaited on its host's loop")
	}
	select {
	case <-promise.done:
		return promise.value, promise.err
	default:
	}
	markEntered(id)
	select {
	case <-promise.done:
		return promise.value, promise.err
	case <-ctx.Done():
		var zero T
		return zero, context.Cause(ctx)
	}
}

// promiseAll is Promise.all over void promises: it resolves when every promise resolved, and rejects with the first
// rejection in settling order.
func promiseAll(loop *loop, promises []*Promise[struct{}]) *Promise[struct{}] {
	all, resolve, reject := newPromise[struct{}](loop)
	if len(promises) == 0 {
		resolve(struct{}{})
		return all
	}
	remaining := len(promises)
	for _, promise := range promises {
		promise.then(func(_ struct{}, err error) {
			if err != nil {
				reject(err)
				return
			}
			remaining--
			if remaining == 0 {
				resolve(struct{}{})
			}
		})
	}
	return all
}

// settledResult is one Promise.allSettled outcome.
type settledResult[T any] struct {
	value T
	err   error
}

// promiseAllSettled is Promise.allSettled: results in input order once every promise settled.
func promiseAllSettled[T any](loop *loop, promises []*Promise[T]) *Promise[[]settledResult[T]] {
	all, resolve, _ := newPromise[[]settledResult[T]](loop)
	results := make([]settledResult[T], len(promises))
	if len(promises) == 0 {
		resolve(results)
		return all
	}
	remaining := len(promises)
	for index, promise := range promises {
		promise.then(func(value T, err error) {
			results[index] = settledResult[T]{value: value, err: err}
			remaining--
			if remaining == 0 {
				resolve(results)
			}
		})
	}
	return all
}

// orderedMap is a JavaScript Map: lookups by key, iteration in insertion order.
type orderedMap[K comparable, V any] struct {
	keys   []K
	values map[K]V
}

func newOrderedMap[K comparable, V any]() *orderedMap[K, V] {
	return &orderedMap[K, V]{values: map[K]V{}}
}

func (entries *orderedMap[K, V]) Get(key K) (V, bool) {
	value, ok := entries.values[key]
	return value, ok
}

func (entries *orderedMap[K, V]) Has(key K) bool {
	_, ok := entries.values[key]
	return ok
}

func (entries *orderedMap[K, V]) Set(key K, value V) {
	if _, ok := entries.values[key]; !ok {
		entries.keys = append(entries.keys, key)
	}
	entries.values[key] = value
}

func (entries *orderedMap[K, V]) Delete(key K) bool {
	if _, ok := entries.values[key]; !ok {
		return false
	}
	delete(entries.values, key)
	for index, existing := range entries.keys {
		if existing == key {
			entries.keys = append(entries.keys[:index:index], entries.keys[index+1:]...)
			break
		}
	}
	return true
}

func (entries *orderedMap[K, V]) Len() int { return len(entries.keys) }

func (entries *orderedMap[K, V]) Clear() {
	entries.keys = nil
	clear(entries.values)
}

// Keys is a snapshot of the keys in insertion order.
func (entries *orderedMap[K, V]) Keys() []K { return append([]K(nil), entries.keys...) }

// Values is a snapshot of the values in insertion order.
func (entries *orderedMap[K, V]) Values() []V {
	values := make([]V, len(entries.keys))
	for index, key := range entries.keys {
		values[index] = entries.values[key]
	}
	return values
}
