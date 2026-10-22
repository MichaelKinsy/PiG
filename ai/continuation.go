package ai

import (
	"context"
	"iter"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsloop"
	"github.com/MichaelKinsy/PiG/internal/stackprime"
)

type continuationExecutorKey struct{}
type continuationTurnKey struct{}

func withContinuationExecutor(ctx context.Context) (context.Context, *continuationExecutor) {
	if executor, ok := ctx.Value(continuationExecutorKey{}).(*continuationExecutor); ok {
		return ctx, executor
	}
	executor := &continuationExecutor{turns: jsloop.TurnsFrom(ctx)}
	return context.WithValue(ctx, continuationExecutorKey{}, executor), executor
}

// continuationExecutor orders promise reactions. A turn owns execution until it awaits or returns; posting a reaction never interrupts the current turn.
//
// Exactly one body runs at a time. A turn started by run is a coroutine: whichever goroutine serves the executor resumes it, so handing execution from one turn to another is a coroutine switch instead of a goroutine wake. A goroutine serves the executor only while it waits in run, runDirect or an await for its own turn, the times it would otherwise sleep; any other goroutine that posts a reaction drains plain reactions and wakes a serving goroutine for a coroutine turn.
type continuationExecutor struct {
	mu          sync.Mutex
	ready       continuationQueue
	ticks       continuationQueue
	tickPhase   bool
	external    continuationQueue
	watchers    []*continuationWatcher
	draining    bool
	active      *continuationTurn
	observation *StreamObservation
	// drainer is the turn whose home goroutine runs the current drain, which may then resume coroutine turns itself; nil when the drain runs on a goroutine that has other work.
	drainer *continuationTurn
	// drains counts ended drains; a drain whose count moved under it was ended in place by a turn it resumed (settle).
	drains uint64
	// frameless is the turn whose body holds the drain in progress, which has no loop on any stack: a body that its pause's drain resumed in place (settle) holds execution, and the goroutine that drives it goes on with that drain when the body next pauses or returns (advance), as the drain that would have resumed it would. Only that body's driver may do so: another turn's driver that takes the lock only after its own body switched back to it finds the drain here, but it is not its drain to run, and the body that holds it is running or settling beside it.
	frameless *continuationTurn
	// settling is set while a pausing turn runs the drain on its own stack (settle): its driver is awake and resumes the coroutines granted meanwhile.
	settling bool
	// drivers are the turns whose home goroutines sleep in serve, most recent last.
	drivers []*continuationTurn
	// arriving counts the turns whose goroutines will serve without a wake-up (arrive); a coroutine turn granted by a goroutine that cannot resume it waits for one of them instead of waking a sleeping one.
	arriving int
	// turns is told of each I/O completion, which is a turn of Pi's event loop: the timer callbacks an extension queued run then (jsloop.Turns). Nil when the run's owner holds none.
	turns *jsloop.Turns
	// trace observes scheduling boundaries in tests; production leaves it nil.
	trace *executorTrace
	// switchEveryPause makes every coroutine pause switch to its driver instead of settling on its own stack (settle); tests compare the two. Production leaves it false.
	switchEveryPause bool
}

// executorTrace reports the executor's macrotask boundaries and each pushed stream event.
type executorTrace struct {
	// external runs before each external completion, the analogue of one Node event-loop macrotask.
	external func()
	// idle runs when a drain ends with no reaction ready and no turn holding execution.
	idle func()
	// push runs before a stream event is queued.
	push func(*AssistantMessageEventStream, AssistantMessageEvent)
	// microtask runs before each ready reaction, the analogue of one Node microtask job.
	microtask func()
	// pause runs when a coroutine body switches to its driver, which a Pi program does not observe. The body has released execution, so other turns may run meanwhile.
	pause func()
	// afterReaction runs after each reaction a drain (settling false) or a pausing turn on its own stack (settling true) ran, with the lock released: the window in which another goroutine may take the executor's lock.
	afterReaction func(settling bool)
}

type continuationTurn struct {
	executor *continuationExecutor
	// The fields below up to unlessMet are guarded by executor.mu.
	state turnState
	// granted records a grant the granting drain could not resume itself: the body is not provided yet, runs a detached segment, is direct, or is a coroutine no serving goroutine resumed yet (needsDriver). The turn is the executor's active turn until its body consumes the grant.
	granted     bool
	needsDriver bool
	hasBody     bool
	// coroutine is set for a body started by run; a direct body (runDirect) runs on the goroutine that provided it.
	coroutine bool
	claimed   bool
	abandoned bool
	// parked is set while the turn's home goroutine sleeps in serve; wake rouses it.
	parked bool
	wake   chan struct{}
	// arriving is set from arrive until the body next suspends or returns.
	arriving bool
	// producer is the observation of a provider response turn, set while that turn runs its body. A host callback the provider invokes (an onResponse hook reaching an extension) awaits through it.
	producer *StreamObservation
	// owed counts the awaits of an already resolved promise a suspended body still has to make before it resumes: its grant makes them (awaitResolvedThen).
	owed int
	// unless is the check a suspended body runs after its owed awaits, which its grant runs at the body's place (checkLocked); unlessMet is its answer.
	unless    func() bool
	unlessMet bool

	// deferred counts the awaits of an already resolved promise the body owes (deferResolved); only the body reads and writes it.
	deferred int

	// The coroutine of a turn started by run. next and stop are called by one goroutine at a time under the state machine; yield only by the body.
	next  func() (turnStep, bool)
	stop  func()
	yield func(turnStep) bool
	// driver is the turn whose home goroutine resumed the body last (advance): written before each resume, read only by the body.
	driver *continuationTurn
	// panicked is a panic the coroutine body raised, rethrown on the turn's home goroutine.
	panicked any
}

// turnState is where a turn's body is.
type turnState uint8

const (
	// turnWaiting: the body has not started, or it is suspended until a grant resumes it.
	turnWaiting turnState = iota
	// turnRunning: a goroutine runs the body, holding execution or in a detached segment.
	turnRunning
	// turnDetached: the coroutine body released execution to keep running outside the executor, and its home goroutine has to run that segment.
	turnDetached
	// turnDone: the body returned.
	turnDone
)

// turnStep is what a coroutine body hands its driver when it stops running.
type turnStep uint8

const (
	// stepSuspend: the body awaits; a grant resumes it.
	stepSuspend turnStep = iota
	// stepDetach: the body released execution and continues outside the executor.
	stepDetach
)

// turnStopped unwinds a suspended coroutine body that is stopped instead of resumed.
type turnStopped struct{}

type continuationPromise[T any] struct {
	executor *continuationExecutor
	resolved bool
	value    T
	then     []continuationThen[T]
	// firstThen holds the first reaction, so a promise with one reaction allocates no list.
	firstThen [1]continuationThen[T]
}

// continuationThen is one reaction to a promise: reaction receives the value, or, when grant is set, the promise's resolution resumes that turn.
type continuationThen[T any] struct {
	reaction func(T)
	grant    *continuationTurn
}

// continuationReactor runs a promise reaction with the promise's value.
type continuationReactor interface {
	react(reaction any)
}

// continuationJob is one queued reaction: fn, a turn to resume, an observation whose turn takes execution back, or a promise reaction (a func(T) of promise) that receives the promise's value. A job is a value, so queuing one of the latter three allocates no closure.
type continuationJob struct {
	fn       func()
	grant    *continuationTurn
	acquire  *StreamObservation
	promise  continuationReactor
	reaction any
}

func (job continuationJob) run() {
	switch {
	case job.fn != nil:
		job.fn()
	case job.grant != nil:
		job.grant.grant()
	case job.acquire != nil:
		job.acquire.acquire()
	default:
		job.promise.react(job.reaction)
	}
}

// continuationQueue is a FIFO of jobs that reuses its storage once drained, so a queue that empties and refills does not allocate.
type continuationQueue struct {
	items []continuationJob
	head  int
}

func (queue *continuationQueue) push(job continuationJob) {
	if queue.head > 0 && len(queue.items) == cap(queue.items) {
		kept := copy(queue.items, queue.items[queue.head:])
		clear(queue.items[kept:])
		queue.items, queue.head = queue.items[:kept], 0
	}
	queue.items = append(queue.items, job)
}

func (queue *continuationQueue) len() int { return len(queue.items) - queue.head }

func (queue *continuationQueue) pop() continuationJob {
	job := queue.items[queue.head]
	queue.items[queue.head] = continuationJob{}
	queue.head++
	if queue.head == len(queue.items) {
		queue.items, queue.head = queue.items[:0], 0
	}
	return job
}

func (queue *continuationQueue) peek() continuationJob { return queue.items[queue.head] }

func (executor *continuationExecutor) post(reaction func()) {
	if drain := executor.postDeferred(reaction); drain != nil {
		drain()
	}
}

func (executor *continuationExecutor) postDeferred(reaction func()) func() {
	return executor.queueDeferred(&executor.ready, continuationJob{fn: reaction})
}

// postTickDeferred queues a process.nextTick callback: it runs after every ready reaction and before any I/O completion.
func (executor *continuationExecutor) postTickDeferred(reaction func()) func() {
	return executor.queueDeferred(&executor.ticks, continuationJob{fn: reaction})
}

// queueDeferred queues job on queue, one of the executor's, and returns the drain the caller runs once it released its own locks, or nil.
func (executor *continuationExecutor) queueDeferred(queue *continuationQueue, job continuationJob) func() {
	executor.mu.Lock()
	queue.push(job)
	drain := executor.startDrainLocked()
	executor.mu.Unlock()
	if drain {
		return executor.drain
	}
	return nil
}

func (executor *continuationExecutor) postExternal(reaction func()) {
	executor.mu.Lock()
	executor.external.push(continuationJob{fn: reaction})
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
	return executor.ready.len() == 0 && executor.ticks.len() == 0 && executor.external.len() == 0
}

// popLocked takes the next reaction in Node's order: after an I/O completion its process.nextTick callbacks run first, then every ready promise reaction; microtasks run to exhaustion before the tick queue runs again, and I/O completions come last (lib/internal/process/task_queues.js processTicksAndRejections). external reports an I/O completion, which starts a new event-loop macrotask.
func (executor *continuationExecutor) popLocked() (reaction continuationJob, kind reactionKind) {
	if executor.tickPhase {
		if executor.ticks.len() != 0 {
			return executor.ticks.pop(), reactionTick
		}
		executor.tickPhase = false
	}
	switch {
	case executor.ready.len() != 0:
		return executor.ready.pop(), reactionMicrotask
	case executor.ticks.len() != 0:
		executor.tickPhase = true
		return executor.ticks.pop(), reactionTick
	default:
		executor.tickPhase = true
		return executor.external.pop(), reactionExternal
	}
}

// peekLocked is the reaction popLocked would take next, without taking it. Some queue must hold one.
func (executor *continuationExecutor) peekLocked() continuationJob {
	switch {
	case executor.tickPhase && executor.ticks.len() != 0, executor.ready.len() == 0 && executor.ticks.len() != 0:
		return executor.ticks.peek()
	case executor.ready.len() != 0:
		return executor.ready.peek()
	default:
		return executor.external.peek()
	}
}

// reactionKind tells the trace which queue a reaction came from.
type reactionKind uint8

const (
	reactionMicrotask reactionKind = iota
	reactionTick
	reactionExternal
)

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
		executor.ready.push(continuationJob{fn: watcher.fire})
		executor.watchers = slices.Delete(executor.watchers, index, index+1)
	}
}

// drain runs reactions until execution is owned or nothing is ready. A grant reaction it runs resumes a coroutine turn in place when the drain runs on a serving goroutine (executor.drainer). A turn it resumes may end it from the turn's own stack (settle): the drain then returns without touching the executor.
func (executor *continuationExecutor) drain() {
	executor.mu.Lock()
	executor.drainLocked()
}

// drainLocked is drain, called with the lock held; it returns with the lock released.
func (executor *continuationExecutor) drainLocked() {
	drains := executor.drains
	for {
		if executor.drains != drains {
			executor.mu.Unlock()
			return
		}
		executor.pollWatchersLocked()
		if executor.active != nil || executor.idleLocked() {
			idle := executor.endDrainLocked()
			executor.mu.Unlock()
			if idle != nil {
				idle()
			}
			return
		}
		reaction, kind := executor.popLocked()
		trace := executor.trace
		executor.mu.Unlock()
		executor.traceReaction(trace, kind)
		reaction.run()
		if trace != nil && trace.afterReaction != nil {
			trace.afterReaction(false)
		}
		executor.mu.Lock()
	}
}

// endDrainLocked records that the running drain ended, and returns the trace's idle hook to run once the lock is released when it ended with nothing ready and no turn holding execution.
func (executor *continuationExecutor) endDrainLocked() (idle func()) {
	executor.draining = false
	executor.drainer = nil
	executor.frameless = nil
	executor.drains++
	if executor.active == nil && executor.idleLocked() && executor.trace != nil {
		return executor.trace.idle
	}
	return nil
}

// traceReaction reports a reaction of kind to the trace before it runs.
func (executor *continuationExecutor) traceReaction(trace *executorTrace, kind reactionKind) {
	if kind == reactionExternal {
		executor.turns.Turn()
	}
	if trace == nil {
		return
	}
	if kind == reactionExternal && trace.external != nil {
		trace.external()
	}
	if kind == reactionMicrotask && trace.microtask != nil {
		trace.microtask()
	}
}

func (executor *continuationExecutor) newTurn() *continuationTurn {
	turn := executor.newDeferredTurn()
	turn.resume()
	return turn
}

// newDeferredTurn creates a turn whose owner grants it from its own promise reaction.
func (executor *continuationExecutor) newDeferredTurn() *continuationTurn {
	return &continuationTurn{executor: executor}
}

func (executor *continuationExecutor) run(body func(*continuationTurn)) {
	executor.newTurn().run(body)
}

// run runs body as a coroutine turn and returns when it has returned, or at once when the turn was abandoned before it ran. Any serving goroutine resumes the body at its grants; the calling goroutine serves the executor meanwhile and runs the body's detached segments. A panic in body is rethrown here.
func (turn *continuationTurn) run(body func(*continuationTurn)) {
	executor := turn.executor
	next, stop := iter.Pull(func(yield func(turnStep) bool) {
		turn.yield = yield
		stackprime.Prime()
		defer func() {
			if recovered := recover(); recovered != nil {
				if _, stopped := recovered.(turnStopped); !stopped {
					turn.panicked = recovered
				}
			}
		}()
		body(turn)
		turn.settleDeferred()
	})
	executor.mu.Lock()
	if turn.abandoned {
		executor.mu.Unlock()
		stop()
		return
	}
	turn.next, turn.stop, turn.hasBody, turn.coroutine = next, stop, true, true
	if turn.granted {
		turn.granted, turn.needsDriver = false, true
	}
	executor.mu.Unlock()
	for {
		executor.serve(turn)
		executor.mu.Lock()
		state := turn.state
		executor.mu.Unlock()
		switch state {
		case turnDone:
			// The body's return released execution; drain what it left, as a goroutine with work of its own.
			executor.mu.Lock()
			drain := executor.startDrainLocked()
			executor.mu.Unlock()
			if drain {
				executor.drain()
			}
			if turn.panicked != nil {
				panic(turn.panicked)
			}
			return
		case turnRunning:
			// serve handed back the body's detached segment.
			executor.advance(turn, turn)
		default:
			// Abandoned before the body started.
			stop()
			return
		}
	}
}

// runDirect runs body on the calling goroutine once the turn is granted, for a caller that runs one short callback after another and must not pay a coroutine per callback. Its awaits serve the executor until the turn is granted again.
func (turn *continuationTurn) runDirect(body func(*continuationTurn)) {
	executor := turn.executor
	executor.mu.Lock()
	if turn.abandoned {
		executor.mu.Unlock()
		return
	}
	turn.hasBody = true
	if !turn.granted {
		executor.mu.Unlock()
		executor.serve(turn)
		executor.mu.Lock()
		if turn.abandoned {
			executor.mu.Unlock()
			return
		}
	}
	turn.granted, turn.claimed, turn.state = false, true, turnRunning
	executor.mu.Unlock()
	defer turn.finishDirect()
	body(turn)
	turn.settleDeferred()
}

func (turn *continuationTurn) finishDirect() {
	executor := turn.executor
	executor.mu.Lock()
	if executor.active != turn {
		executor.mu.Unlock()
		panic("stream continuation released without ownership")
	}
	executor.active = nil
	turn.state = turnDone
	executor.departLocked(turn)
	drain := executor.startDrainLocked()
	executor.mu.Unlock()
	if drain {
		executor.drain()
	}
}

// serve lends the calling goroutine, the home of self, to the executor until self needs it: a direct self holds execution again, a coroutine self has a detached segment to run or has returned, or self was abandoned before it ran. Meanwhile the goroutine drains reactions and resumes coroutine turns, so execution passes between turns without waking another goroutine, and it sleeps when there is nothing to run.
func (executor *continuationExecutor) serve(self *continuationTurn) {
	executor.mu.Lock()
	for {
		switch {
		case self.state == turnDone, self.abandoned && !self.claimed, !self.coroutine && self.granted:
			executor.mu.Unlock()
			return
		case executor.active == nil && !executor.draining && !executor.idleLocked():
			executor.draining, executor.drainer = true, self
			executor.mu.Unlock()
			executor.drain()
		case executor.active != nil && executor.active.needsDriver:
			turn := executor.active
			turn.needsDriver, turn.granted, turn.claimed, turn.state = false, false, true, turnRunning
			executor.mu.Unlock()
			executor.advance(turn, self)
		case self.state == turnDetached:
			// The executor ran what the release let run; now the segment.
			self.state = turnRunning
			executor.mu.Unlock()
			return
		default:
			if self.wake == nil {
				self.wake = make(chan struct{}, 1)
			}
			executor.departLocked(self)
			self.parked = true
			executor.drivers = append(executor.drivers, self)
			executor.mu.Unlock()
			<-self.wake
		}
		executor.mu.Lock()
	}
}

// advance resumes a coroutine turn's body on the calling goroutine, the home of self, until the body suspends, detaches or returns. turn holds execution unless it resumes a detached segment.
func (executor *continuationExecutor) advance(turn, self *continuationTurn) {
	turn.driver = self
	for {
		step, more := turn.next()
		executor.mu.Lock()
		if more && step == stepSuspend && turn.granted {
			// A detached segment suspended after its grant already ran: it holds execution and goes on.
			turn.granted = false
			executor.departLocked(turn)
			executor.mu.Unlock()
			continue
		}
		if executor.active == turn {
			executor.active = nil
		}
		turn.granted = false
		if !more || step == stepSuspend {
			executor.departLocked(turn)
		}
		switch {
		case !more:
			turn.state = turnDone
		case step == stepSuspend:
			turn.state = turnWaiting
		default:
			turn.state = turnDetached
		}
		if turn != self && turn.state != turnWaiting {
			executor.wakeLocked(turn)
		}
		if executor.frameless == turn {
			// The body ran, in place, the drain that resumed it (settle); that drain would have run on this goroutine, under the body, and goes on now.
			executor.frameless = nil
			executor.drainLocked()
			return
		}
		executor.mu.Unlock()
		return
	}
}

// wakeLocked rouses turn's home goroutine if it sleeps in serve.
func (executor *continuationExecutor) wakeLocked(turn *continuationTurn) {
	if !turn.parked {
		return
	}
	if index := slices.Index(executor.drivers, turn); index >= 0 {
		executor.drivers = slices.Delete(executor.drivers, index, index+1)
	}
	turn.parked = false
	turn.wake <- struct{}{}
}

// wakeDriverLocked rouses the goroutine that most recently began to sleep in serve, to resume the granted coroutine turn.
func (executor *continuationExecutor) wakeDriverLocked() {
	last := len(executor.drivers) - 1
	if last < 0 {
		// Every serving goroutine is awake and checks for a coroutine to resume before it sleeps.
		return
	}
	turn := executor.drivers[last]
	executor.drivers[last] = nil
	executor.drivers = executor.drivers[:last]
	turn.parked = false
	turn.wake <- struct{}{}
}

// arrive records that turn's body, running a detached segment whose wait has ended, is about to suspend: its goroutine will serve the executor without a wake-up, so a coroutine turn granted meanwhile waits for it instead of waking a sleeping goroutine. A turn already suspended is not arriving.
func (turn *continuationTurn) arrive() {
	executor := turn.executor
	executor.mu.Lock()
	if !turn.arriving && turn.state != turnWaiting && turn.state != turnDone {
		turn.arriving = true
		executor.arriving++
	}
	executor.mu.Unlock()
}

// departLocked clears turn's arrive once its body suspends, returns or its goroutine serves.
func (executor *continuationExecutor) departLocked(turn *continuationTurn) {
	if turn.arriving {
		turn.arriving = false
		executor.arriving--
	}
}

func (turn *continuationTurn) resume() {
	executor := turn.executor
	executor.mu.Lock()
	executor.ready.push(continuationJob{grant: turn})
	drain := executor.startDrainLocked()
	executor.mu.Unlock()
	if drain {
		executor.drain()
	}
}

// grant gives turn execution. It runs as a reaction in a drain: a serving drain resumes a suspended coroutine body in place; otherwise the turn's grant waits for its body, its home goroutine, or a serving goroutine.
func (turn *continuationTurn) grant() {
	executor := turn.executor
	executor.mu.Lock()
	if turn.abandoned || turn.state == turnDone {
		executor.mu.Unlock()
		return
	}
	if executor.active != nil {
		executor.mu.Unlock()
		panic("stream continuation resumed during another turn")
	}
	for turn.owed > 0 || turn.unless != nil {
		// The body owes awaits of an already resolved promise (awaitResolvedThen) and runs no code between them but, after the last, the check it left: this resumption is the next of those awaits, behind every queued reaction or, with none queued, at once, as awaitResolved makes it.
		if turn.owed == 0 && !turn.checkLocked() {
			break
		}
		turn.owed--
		if executor.ready.len() != 0 || executor.ticks.len() != 0 {
			executor.ready.push(continuationJob{grant: turn})
			executor.mu.Unlock()
			return
		}
		executor.active = turn
		trace := executor.resumeNextLocked()
		executor.mu.Unlock()
		if trace != nil && trace.microtask != nil {
			trace.microtask()
		}
		executor.mu.Lock()
		executor.active = nil
	}
	executor.active = turn
	waiting := turn.hasBody && turn.state == turnWaiting
	switch self := executor.drainer; {
	case waiting && turn.coroutine && self != nil && self.state != turnDone:
		turn.state, turn.claimed = turnRunning, true
		executor.mu.Unlock()
		executor.advance(turn, self)
		return
	case waiting && turn.coroutine:
		turn.granted, turn.needsDriver = true, true
		if executor.arriving == 0 && !executor.settling {
			executor.wakeDriverLocked()
		}
	case waiting:
		turn.granted = true
		if turn != self {
			executor.wakeLocked(turn)
		}
	default:
		turn.granted = true
	}
	executor.mu.Unlock()
}

// checkLocked runs, at the body's place, the check the body left to run after its owed awaits (awaitResolvedThen), with the lock released and execution held as the body would hold it. It reports whether the body makes one more await, which it does unless the check holds.
func (turn *continuationTurn) checkLocked() (more bool) {
	executor := turn.executor
	check := turn.unless
	turn.unless = nil
	executor.active = turn
	executor.mu.Unlock()
	met := check()
	executor.mu.Lock()
	executor.active = nil
	turn.unlessMet = met
	if !met {
		turn.owed = 1
	}
	return !met
}

// abandon cancels a turn whose body has not started, so its place in the queue no longer holds execution. It returns false once the body runs.
func (turn *continuationTurn) abandon() bool {
	executor := turn.executor
	executor.mu.Lock()
	if turn.claimed {
		executor.mu.Unlock()
		return false
	}
	if !turn.abandoned {
		turn.abandoned = true
		executor.wakeLocked(turn)
	}
	if executor.active == turn {
		executor.active = nil
		turn.granted, turn.needsDriver = false, false
	}
	drain := executor.startDrainLocked()
	executor.mu.Unlock()
	if drain {
		executor.drain()
	}
	return true
}

// suspend gives up execution, if the turn holds it, and returns once a grant the body registered gives it back. A coroutine body first runs, on its own stack, what its driver would run before that grant (settle), and switches only when something it may not run comes first.
func (turn *continuationTurn) suspend() {
	if turn.deferred != 0 {
		// Its grant is already registered: the owed awaits would come after it.
		panic("stream continuation suspended owing deferred awaits")
	}
	if turn.coroutine {
		if turn.executor.switchEveryPause || !turn.settle(stepSuspend) {
			turn.pause(stepSuspend)
		}
		return
	}
	executor := turn.executor
	executor.mu.Lock()
	executor.departLocked(turn)
	if turn.granted {
		turn.granted = false
		executor.mu.Unlock()
		return
	}
	if executor.active == turn {
		executor.active = nil
	}
	turn.state = turnWaiting
	executor.mu.Unlock()
	executor.serve(turn)
	executor.mu.Lock()
	turn.granted, turn.state = false, turnRunning
	executor.mu.Unlock()
}

// detach gives up execution while the body goes on outside the executor; the body later suspends to take execution back. What the release lets run runs first, as a JavaScript await's caller continues only after the microtasks it queued.
func (turn *continuationTurn) detach() {
	turn.settleDeferred()
	if turn.coroutine {
		if turn.executor.switchEveryPause || !turn.settle(stepDetach) {
			turn.pause(stepDetach)
		}
		return
	}
	executor := turn.executor
	executor.mu.Lock()
	if executor.active == turn {
		executor.active = nil
	}
	turn.granted = false
	drain := executor.startDrainLocked()
	if drain {
		executor.drainer = turn
	}
	executor.mu.Unlock()
	if drain {
		executor.drain()
	}
}

// pause switches the coroutine body to its driver.
func (turn *continuationTurn) pause(step turnStep) {
	if trace := turn.executor.trace; trace != nil && trace.pause != nil {
		trace.pause()
	}
	if !turn.yield(step) {
		panic(turnStopped{})
	}
}

// settle is the drain that a pause of turn would start on its driver, run on turn's own stack for as long as the next reaction is one that stack may run (settlesInPlaceLocked): in the drain's order, with the drain's watcher polls, tick phases and trace. It reports whether the body goes on without a coroutine switch: after a suspend, one of those reactions granted turn; after a detach, the drain ended, and the home goroutine, which drives the body, would now hand it its segment. Otherwise the queue is as the drain would leave it before its next reaction, and the pause hands that reaction to the driver.
func (turn *continuationTurn) settle(step turnStep) bool {
	executor := turn.executor
	executor.mu.Lock()
	if step == stepSuspend && turn.granted {
		// A detached segment whose grant already ran holds execution and goes on, as advance would make it after the switch.
		turn.granted = false
		executor.departLocked(turn)
		executor.mu.Unlock()
		return true
	}
	holds := executor.active == turn
	if step == stepDetach && (!holds || turn.driver != turn) || !holds && (executor.active != nil || executor.draining) || holds && executor.draining && executor.drainer != turn.driver {
		// A segment runs on the home goroutine, and a drain another turn or goroutine owns is not this stack's to run. A holding turn owns the drain only when the one that resumed it runs, or ran, on this body's driver (drainer is that turn: in place, or frameless). Any other drain is live on another stack, typically the one whose grant a serving goroutine claimed, which has not yet noticed it granted: this body must neither run reactions under that flag nor write it back after the drain ended, which would leave the executor draining with no loop, so the pause hands the queue to that drain or to the driver.
		executor.mu.Unlock()
		return false
	}
	// Turn releases execution. When its driver resumed it from a drain (draining), that drain is suspended under it and goes on after the pause; otherwise the driver starts one, and that drain then resumes turn and goes on under it. No body runs on this stack meanwhile: a coroutine a reaction grants waits for the driver, which is awake (settling), so it runs where the drain would have run it.
	draining, drainer := executor.draining, executor.drainer
	if holds {
		executor.active = nil
	}
	executor.draining, executor.drainer, executor.settling = true, nil, true
	ran := false
	for {
		executor.pollWatchersLocked()
		if executor.active != nil || executor.idleLocked() {
			break
		}
		if !turn.settlesInPlaceLocked(executor.peekLocked()) {
			break
		}
		reaction, kind := executor.popLocked()
		trace := executor.trace
		executor.mu.Unlock()
		executor.traceReaction(trace, kind)
		reaction.run()
		if trace != nil && trace.afterReaction != nil {
			trace.afterReaction(true)
		}
		ran = true
		executor.mu.Lock()
		if executor.active == turn && turn.granted {
			turn.granted = false
			executor.settling = false
			if draining {
				executor.drainer = drainer
			} else {
				// The drain the driver would have started resumed turn: it goes on when the body next pauses or returns.
				executor.drainer, executor.frameless = turn.driver, turn
			}
			executor.departLocked(turn)
			executor.mu.Unlock()
			return true
		}
	}
	executor.settling = false
	ended := executor.active == nil && executor.idleLocked() || executor.active != nil && !executor.active.needsDriver
	var idle func()
	switch {
	case step == stepDetach && ended:
		// The drain ended, and the home goroutine has nothing to resume before it hands the body its segment. A drain that would not have started reports nothing.
		idle = executor.endDrainLocked()
		if !draining && !ran {
			idle = nil
		}
	case !draining && ran && (ended || executor.active != nil):
		// The drain the driver would have started ran here, and ended.
		idle = executor.endDrainLocked()
	default:
		// The suspended drain, or the one the driver starts, goes on from here.
		executor.draining, executor.drainer = draining, drainer
	}
	executor.mu.Unlock()
	if idle != nil {
		idle()
	}
	return step == stepDetach && ended
}

// settlesInPlaceLocked reports whether turn's stack may run job, which a drain of turn's pause would run next: turn's own grant or acquire, a callback, a promise reaction, an I/O completion, or a grant that resumes no other body (one more deferred await, or a deferred check, of a turn that then queues itself again behind what is still queued, or a turn whose body is not waiting). The driver runs another turn's acquire and a grant that resumes or wakes another body: that body runs next either way, and its driver hands it a coroutine or a goroutine.
func (turn *continuationTurn) settlesInPlaceLocked(job continuationJob) bool {
	switch other := job.grant; {
	case other == turn, job.acquire != nil && job.acquire.turn == turn:
		return true
	case job.acquire != nil:
		return false
	case other == nil:
		return true
	case other.owed > 0 || other.unless != nil:
		executor := turn.executor
		return executor.ready.len()+executor.ticks.len() > 1
	default:
		return other.abandoned || other.state == turnDone || !other.hasBody || other.state != turnWaiting
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
	for _, then := range promise.then {
		executor.ready.push(promise.job(then))
	}
	clear(promise.then)
	promise.then = nil
	drain := executor.startDrainLocked()
	executor.mu.Unlock()
	if drain {
		return executor.drain
	}
	return nil
}

func (promise *continuationPromise[T]) onResolved(reaction func(T)) {
	promise.addThen(continuationThen[T]{reaction: reaction})
}

// onResolvedGrant resumes turn as the promise's reaction.
func (promise *continuationPromise[T]) onResolvedGrant(turn *continuationTurn) {
	promise.addThen(continuationThen[T]{grant: turn})
}

func (promise *continuationPromise[T]) addThen(then continuationThen[T]) {
	executor := promise.executor
	executor.mu.Lock()
	if !promise.resolved {
		if promise.then == nil {
			promise.then = promise.firstThen[:0]
		}
		promise.then = append(promise.then, then)
		executor.mu.Unlock()
		return
	}
	executor.ready.push(promise.job(then))
	drain := executor.startDrainLocked()
	executor.mu.Unlock()
	if drain {
		executor.drain()
	}
}

// job is the queued form of a reaction to the resolved promise.
func (promise *continuationPromise[T]) job(then continuationThen[T]) continuationJob {
	if then.grant != nil {
		return continuationJob{grant: then.grant}
	}
	return continuationJob{promise: promise, reaction: then.reaction}
}

// react runs reaction, a func(T), with the value the promise resolved with. The value is set before any job of the promise is queued and never changes.
func (promise *continuationPromise[T]) react(reaction any) {
	reaction.(func(T))(promise.value)
}

func awaitContinuation[T any](turn *continuationTurn, promise *continuationPromise[T]) T {
	if turn.executor != promise.executor {
		panic("stream continuation awaited a different executor")
	}
	turn.settleDeferred()
	promise.onResolvedGrant(turn)
	turn.park()
	// The resolution happened before the reaction that resumed this turn, and a resolved promise's value never changes.
	return promise.value
}

// awaitSettled is `await` of an already settled value: one reaction resumes the turn, as onResolvedGrant on a resolved promise queues it.
func awaitSettled(turn *continuationTurn) {
	turn.settleDeferred()
	executor := turn.executor
	executor.mu.Lock()
	executor.ready.push(continuationJob{grant: turn})
	drain := executor.startDrainLocked()
	executor.mu.Unlock()
	if drain {
		executor.drain()
	}
	turn.park()
}

// park releases execution until a reaction already queued or registered grants the turn again.
func (turn *continuationTurn) park() {
	// The executor's observation is the running turn's. Other turns replace it while this one waits, so a turn that owns the observation takes it back when it resumes; a host call it makes next (a result wait, a nested await) then releases the turn that runs it.
	turn.executor.mu.Lock()
	observation := turn.executor.observation
	if observation != nil && observation.turn != turn {
		observation = nil
	}
	turn.executor.mu.Unlock()
	turn.suspend()
	if observation != nil {
		turn.executor.mu.Lock()
		turn.executor.observation = observation
		turn.executor.mu.Unlock()
	}
}

// awaitResolved is an await of an already resolved promise by turn: the turn resumes from a reaction queued behind every ready one. The awaits the body deferred are made first.
func (turn *continuationTurn) awaitResolved() {
	turn.awaitResolvedThen(1, nil)
}

// deferResolved is awaitResolved for a body that holds execution and whose code up to its next await or settleDeferred reads and writes nothing another turn or a host can observe: no published state, no promise, no queue, no I/O. The await is owed instead of made. The next await makes the owed ones first, all in a row (awaitResolvedThen), so every other reaction runs at the place and with the scheduling effects it would have had; only the body's own unobservable code ran before them.
func (turn *continuationTurn) deferResolved() {
	executor := turn.executor
	executor.mu.Lock()
	holds := executor.active == turn
	executor.mu.Unlock()
	if !holds {
		// Other turns run while the body does, so the await must take its place now.
		turn.awaitResolved()
		return
	}
	turn.deferred++
}

// settleDeferred makes the awaits the body owes (deferResolved) before it does what another turn or a host can observe. Every await settles them first; a body that suspends any other way owes none.
func (turn *continuationTurn) settleDeferred() {
	if turn.deferred != 0 {
		turn.awaitResolvedThen(0, nil)
	}
}

// awaitResolvedUnless makes the awaits the body owes (deferResolved), then runs check where the body stands after them, and unless check reports true makes one more await in the same row, for a body that reads state another turn can change before its next await. It reports check's answer.
func (turn *continuationTurn) awaitResolvedUnless(check func() bool) bool {
	return turn.awaitResolvedThen(0, check)
}

// awaitResolvedThen makes count awaits of an already resolved promise in a row, after the ones the body owes (deferResolved); then, if check is set, it runs check in the body's place and, unless check reports true, makes one more. Each await is awaitResolved: a turn that holds execution, with no ready reaction and no nextTick callback queued, would resume at once, since the drain its release starts polls the watchers and then pops the turn's own grant, so it takes those scheduling effects (the watchers' poll, the end of a tick phase, the trace's microtask) and goes on without suspending. Otherwise the turn queues its grant behind every ready reaction and parks once: the grant makes the awaits left, and runs check, as the executor reaches it, each await one more trip through the queue, and resumes the body after the last (grant). A turn that owns the executor's observation takes it back at each resume (park), so it parks for each await. It reports check's answer.
func (turn *continuationTurn) awaitResolvedThen(count int, check func() bool) (met bool) {
	count += turn.deferred
	turn.deferred = 0
	executor := turn.executor
	executor.mu.Lock()
	for {
		if count == 0 {
			if check == nil {
				break
			}
			executor.mu.Unlock()
			met = check()
			check = nil
			executor.mu.Lock()
			if met {
				break
			}
			count = 1
		}
		count--
		if executor.active != turn {
			executor.mu.Unlock()
			resolved := newContinuationPromise[struct{}](executor)
			resolved.resolve(struct{}{})
			awaitContinuation(turn, resolved)
			executor.mu.Lock()
			continue
		}
		if executor.ready.len() != 0 || executor.ticks.len() != 0 {
			handed := false
			if observation := executor.observation; observation == nil || observation.turn != turn {
				turn.owed, turn.unless, handed = count, check, check != nil
				count, check = 0, nil
			}
			executor.ready.push(continuationJob{grant: turn})
			executor.mu.Unlock()
			turn.park()
			executor.mu.Lock()
			if handed {
				met = turn.unlessMet
				break
			}
			continue
		}
		trace := executor.resumeNextLocked()
		executor.mu.Unlock()
		if trace != nil && trace.microtask != nil {
			trace.microtask()
		}
		executor.mu.Lock()
	}
	executor.mu.Unlock()
	return met
}

// resumeNextLocked takes the scheduling effects of a drain that pops the active turn's grant at once: the watchers' poll and the end of a tick phase. It returns the trace, whose microtask the caller reports.
func (executor *continuationExecutor) resumeNextLocked() *executorTrace {
	executor.pollWatchersLocked()
	executor.tickPhase = false
	return executor.trace
}
