// Ports packages/coding-agent/src/modes/rpc/rpc-mode.ts
package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/jsloop"
	"github.com/MichaelKinsy/PiG/internal/stackprime"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// rpcPromise puts each awaited continuation on the input executor, including awaits of already fulfilled operations. Worker completion never executes a continuation inside another admitted callback.
type rpcPromise[T any] struct {
	turn    *rpcResponseTurn
	mu      sync.Mutex
	done    bool
	value   T
	err     error
	waiting []func(T, error)
}

func rpcResolved[T any](turn *rpcResponseTurn, value T, err error) *rpcPromise[T] {
	return &rpcPromise[T]{turn: turn, done: true, value: value, err: err}
}
func (p *rpcPromise[T]) then(fn func(T, error)) {
	p.mu.Lock()
	if !p.done {
		p.waiting = append(p.waiting, fn)
		p.mu.Unlock()
		return
	}
	value, err := p.value, p.err
	p.mu.Unlock()
	p.turn.Microtask(func() { fn(value, err) })
}
func (p *rpcPromise[T]) resolve(value T, err error) {
	p.mu.Lock()
	p.done, p.value, p.err = true, value, err
	waiting := p.waiting
	p.waiting = nil
	p.mu.Unlock()
	callbacks := make([]func(), len(waiting))
	for i, fn := range waiting {
		callbacks[i] = func() { fn(value, err) }
	}
	p.turn.Microtasks(callbacks...)
}

type rpcInputResult struct {
	text    string
	images  []ai.ImageContent
	handled bool
}
type rpcAdmission struct {
	ctx  context.Context
	turn *rpcResponseTurn
	// turns is the mode's model of Pi's loop (D189).
	turns         *jsloop.Turns
	session       *coding.Session
	runner        *inproc.Runner
	catalog       headlessCommandCatalog
	validateModel func() error
	write         func(any)
	tasks         rpcTaskGroup
	// shared, when set, is the task group every Session's admission of one RPC process uses, so a replacement does not fork the set of tasks that shutdown joins.
	shared *rpcTaskGroup
	runs   *sync.WaitGroup
	// commandDone runs after the extension command's synchronous prefix, before its prompt continuation reports success.
	commandDone func()
	// commandsInFlight, when set, counts the extension commands whose prompt response is not yet written.
	commandsInFlight *atomic.Int32
	// background is the gate that holds the deferred UI calls of a replacement an extension command requested until the last in-flight command answered.
	background *rpcBackgroundGate
	// commands joins in-flight extension commands for the shutdown flush; nil when no subprocess runtime can report a suspended command.
	commands *rpcCommandJoin
	// commandRuns holds the start of a run an extension command sent while a command's handler runs.
	commandRuns rpcCommandRuns
	// promptChecked is the end of the chain of prompt auto-compaction checks: the next prompt's check resolves after it, as Pi's awaits of one batch resume in call order.
	promptChecked <-chan struct{}
}

func (a *rpcAdmission) taskGroup() *rpcTaskGroup {
	if a.shared != nil {
		return a.shared
	}
	return &a.tasks
}

// commandTurn opens a Turns command for work that runs outside the executor. Pi runs the command's microtasks to the response without a timer callback between, so the extension runtimes hold theirs until the work's result settled and the loop drained what it queued: settled is called from the task that settles the result, and ends the command once that drain is over. abandoned ends it at once.
func (a *rpcAdmission) commandTurn() (settled, abandoned func()) {
	finish := a.turns.Command()
	return func() {
		idle := a.turn.Idle()
		//portlint:allow asyncorder the goroutine ends the command's timer hold once the executor drained; it writes nothing the client sees, so no output order depends on it
		go func() {
			<-idle
			finish()
		}()
	}, finish
}

func rpcAwaitWork[T any](a *rpcAdmission, slow bool, work func() (T, error)) *rpcPromise[T] {
	if !slow {
		value, err := work()
		return rpcResolved(a.turn, value, err)
	}
	p := &rpcPromise[T]{turn: a.turn}
	settled, abandoned := a.commandTurn()
	// The work ran outside the executor, so its result settles the promise from an event-loop task of its own, after the microtasks already queued (jsloop.Loop.Macrotask).
	if !a.taskGroup().Go(func() {
		value, err := work()
		a.turn.Macrotask(func() { p.resolve(value, err); settled() })
	}) {
		var zero T
		p.resolve(zero, context.Canceled)
		abandoned()
	}
	return p
}

// rpcResolvedChannel is the closed start of an rpcAwaitInOrder chain.
var rpcResolvedChannel = func() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}()

// rpcAwaitInOrder is rpcAwaitWork for slow work that Pi resumes in the order the awaits began: Pi handles every line of an input chunk before any microtask runs, and awaits of comparable work (`await session.setModel(model)`, `await runtime.newSession()`) resume in FIFO order. The work runs at once, concurrently with earlier work of the chain; work that must start after the previous one applied waits on previous itself. The result settles the promise from an event-loop task only after previous settled, so the tasks run in chain order. Work that fails with an rpcEarlyError settles at once instead: Pi raises that failure before the earlier work of the chain applies. It returns the channel the next work of the chain waits on. Shutdown of the admission releases the wait.
func rpcAwaitInOrder[T any](a *rpcAdmission, previous <-chan struct{}, work func() (T, error)) (*rpcPromise[T], <-chan struct{}) {
	resolved := make(chan struct{})
	p := &rpcPromise[T]{turn: a.turn}
	await := func() {
		select {
		case <-previous:
		case <-a.ctx.Done():
		}
	}
	settled, abandoned := a.commandTurn()
	if !a.taskGroup().Go(func() {
		value, err := work()
		if early, ok := errors.AsType[rpcEarlyError](err); ok {
			var zero T
			a.turn.Macrotask(func() { p.resolve(zero, early.err); settled() })
			await()
			close(resolved)
			return
		}
		await()
		a.turn.Macrotask(func() {
			p.resolve(value, err)
			close(resolved)
			settled()
		})
	}) {
		var zero T
		p.resolve(zero, context.Canceled)
		close(resolved)
		abandoned()
	}
	return p, resolved
}

// rpcEarlyError is a failure that rpcAwaitInOrder reports without waiting for the earlier work of its chain.
type rpcEarlyError struct{ err error }

func (e rpcEarlyError) Error() string { return e.err.Error() }
func (e rpcEarlyError) Unwrap() error { return e.err }

// rpcAwaitHandler preserves invocation order through the handler's first suspension without waiting for its Promise to settle. Subprocess runtimes acknowledge a blocked invocation on the existing request context. A handler that finishes without suspending admits later input only after its result is published, because Pi runs the continuation of a settled await before its next input event, stdin end included.
func rpcAwaitHandler[T any](a *rpcAdmission, work func(context.Context) (T, error)) *rpcPromise[T] {
	entered := make(chan struct{})
	ctx := invocation.WithSuspensionAcknowledgment(a.ctx, func() { close(entered) })
	p := &rpcPromise[T]{turn: a.turn}
	if !a.tasks.Go(func() {
		defer invocation.Acknowledge(ctx)
		value, err := work(ctx)
		p.resolve(value, err)
	}) {
		var zero T
		p.resolve(zero, context.Canceled)
	}
	select {
	case <-entered:
	case <-a.ctx.Done():
	}
	return p
}

func (a *rpcAdmission) input(text string, images []ai.ImageContent, behavior string) *rpcPromise[rpcInputResult] {
	hasInput := a.runner != nil && a.runner.HasHandlers("input")
	if !a.session.IsStreaming() {
		behavior = ""
	}
	work := func(ctx context.Context) (rpcInputResult, error) {
		text, images, handled, err := codingagent.RunInputHandlers(ctx, a.runner, text, images, extension.InputSourceRPC, behavior)
		return rpcInputResult{text, images, handled}, err
	}
	if !hasInput {
		value, err := work(a.ctx)
		return rpcResolved(a.turn, value, err)
	}
	emitted := rpcAwaitHandler(a, work)
	// _runInputHandlers awaits emitInput before returning its processed input.
	wrapped := &rpcPromise[rpcInputResult]{turn: a.turn}
	emitted.then(wrapped.resolve)
	return wrapped
}
func (a *rpcAdmission) queue(id rpcRequestID, command, text string, images []ai.ImageContent) {
	queued := &rpcPromise[coding.QueuedInputDisposition]{turn: a.turn}
	if name, _, ok := a.catalog.extensionCommand(text); ok {
		queued.resolve("", fmt.Errorf("Extension command %q cannot be queued. Use prompt() or execute the command when not streaming.", "/"+strings.TrimPrefix(name, "/")))
	} else {
		behavior := "steer"
		if command == "follow_up" {
			behavior = "followUp"
		}
		a.input(text, images, behavior).then(func(input rpcInputResult, err error) {
			if a.ctx.Err() != nil {
				return
			}
			if err != nil {
				queued.resolve("", err)
				return
			}
			// A handler that consumed the input reports "handled", even when it queued another message on its own (agent-session.ts:2076-2078).
			if input.handled {
				queued.resolve(coding.DispositionHandled, nil)
				return
			}
			if command == "steer" {
				a.session.QueueSteer(a.catalog.expandPrompt(input.text), input.images)
			} else {
				a.session.QueueFollowUp(a.catalog.expandPrompt(input.text), input.images)
			}
			err = a.session.FlushEvents(a.ctx)
			// _queueUserInput awaits the resolved _queueSteer/_queueFollowUp call.
			rpcResolved(a.turn, coding.DispositionQueued, err).then(queued.resolve)
		})
	}
	// steer/followUp await _queueUserInput; handleCommand then awaits that wrapper.
	wrapped := &rpcPromise[coding.QueuedInputDisposition]{turn: a.turn}
	queued.then(wrapped.resolve)
	wrapped.then(func(disposition coding.QueuedInputDisposition, err error) {
		if err != nil {
			a.turn.complete(rpcError(id, command, err.Error()))
		} else {
			a.turn.complete(rpcDispositionSuccess(id, command, disposition))
		}
	})
}
func (a *rpcAdmission) command(id rpcRequestID, name, args string) {
	if a.commands != nil {
		a.commands.begin()
	}
	if a.commandsInFlight != nil {
		a.commandsInFlight.Add(1)
	}
	flight := &rpcCommandFlight{runs: &a.commandRuns}
	pending := rpcAwaitHandler(a, func(ctx context.Context) (struct{}, error) {
		a.catalog.executeCommand(invocation.WithRunPhase(ctx, flight), name, args)
		return struct{}{}, nil
	})
	if a.commandDone != nil {
		a.commandDone()
	}
	pending.then(func(_ struct{}, _ error) {
		a.turn.Microtask(func() {
			// The answer releases a run the handler sent; it is deferred first, so it follows the response.
			defer a.commandRuns.answered(flight, a)
			if a.commandsInFlight != nil {
				defer func() {
					if a.commandsInFlight.Add(-1) == 0 && a.background != nil {
						a.background.endCommandHolds()
					}
				}()
			}
			if a.commands != nil {
				if a.commands.isClosed() {
					return
				}
				defer a.commands.end()
			}
			a.write(rpcDispositionSuccess(id, "prompt", coding.DispositionHandled))
		})
	})
}

// rpcCommandFlight is an extension command whose handler a subprocess runtime runs: the transport reports when the handler
// runs again after a send and when it next awaits (invocation.RunPhase). Its fields are guarded by rpcCommandRuns.mu.
type rpcCommandFlight struct {
	runs     *rpcCommandRuns
	running  bool
	answered bool
}

func (f *rpcCommandFlight) Resumed()   { f.runs.set(f, true, nil) }
func (f *rpcCommandFlight) Suspended() { f.runs.set(f, false, nil) }

// rpcCommandRuns holds the start of a run an extension command sent while a command's handler runs. Pi runs a command's handler
// on its one event loop: pi.sendMessage and pi.sendUserMessage return inside the handler, which goes on until it awaits or
// returns (agent-session.ts:2129 `await command.handler(args, ctx)`), the prompt's response is written two microtasks after it
// returns (agent-session.ts:1978-1981, rpc-mode.ts:403-406), and the run emits agent_start only after the awaits of its
// preparation (agent-session.ts:2293 sendCustomMessage, PR #10739 _startRun). So the run starts after the response of a
// command that returns, and while a command that awaits is suspended.
type rpcCommandRuns struct {
	mu      sync.Mutex
	running int
	waiters []func()
}

// set records whether f's handler runs; when no handler runs any longer, the held starts proceed in the order they were held, from a
// task of their own (the transport reports from its read loop, which a start must not occupy).
func (r *rpcCommandRuns) set(f *rpcCommandFlight, running bool, a *rpcAdmission) {
	r.mu.Lock()
	if f.answered || f.running == running {
		r.mu.Unlock()
		return
	}
	f.running = running
	if running {
		r.running++
		r.mu.Unlock()
		return
	}
	r.running--
	r.releaseLocked(a)
}

// answered retires f once its response is written.
func (r *rpcCommandRuns) answered(f *rpcCommandFlight, a *rpcAdmission) {
	r.mu.Lock()
	if f.answered {
		r.mu.Unlock()
		return
	}
	f.answered = true
	if !f.running {
		r.mu.Unlock()
		return
	}
	f.running = false
	r.running--
	r.releaseLocked(a)
}

// releaseLocked unlocks r and runs the held starts once no handler runs, in the order they were held. With a nil admission (a
// transport report) they run on one goroutine; the executor's answer runs them in place, behind the response it wrote.
func (r *rpcCommandRuns) releaseLocked(a *rpcAdmission) {
	var waiters []func()
	if r.running == 0 {
		waiters, r.waiters = r.waiters, nil
	}
	r.mu.Unlock()
	if len(waiters) == 0 {
		return
	}
	run := func() {
		for _, waiter := range waiters {
			waiter()
		}
	}
	if a != nil {
		run()
		return
	}
	//portlint:allow asyncorder the held starts run in order on this one goroutine and each queues its start on the executor (extensionRunStage), which is the ordering point
	go run() //portlint:allow golifetime each held start only queues work on the executor, so the goroutine ends once they are queued
}

// whenNoneRuns runs fn at once if no command handler runs, and otherwise once none does.
func (r *rpcCommandRuns) whenNoneRuns(fn func()) {
	r.mu.Lock()
	if r.running == 0 {
		r.mu.Unlock()
		fn()
		return
	}
	r.waiters = append(r.waiters, fn)
	r.mu.Unlock()
}

// extensionRunStageHops is the number of microtasks between the end of an extension run's preparation and its start. The response of the command whose handler sent the run is written two microtasks after the handler's result, so the run starts behind it; Pi's agent_start follows the run's preparation by more awaits still (agent-session.ts:2090-2112 prompt, 1822-1832 _runAgentPrompt; PR #10739 _startRun).
const extensionRunStageHops = 2

// extensionRunStage stages a run that an extension sends with sendUserMessage or sendMessage while a command is in flight. Pi runs the send's synchronous part inside the handler, ahead of the microtasks of the command's response: the input handlers, the compaction check and before_agent_start start before the response is written, and agent_start follows it (agent-session.ts:2116-2140, 2293-2330, 2365-2394; rpc-mode.ts:394-413). The preparation is queued on the executor, where it precedes the command's continuation, and holds it until the preparation suspends or ends. The start then waits while a command's handler runs (rpcCommandRuns): a subprocess handler that waits for the send's answer goes on after it, as Pi's handler goes on after the send returns, until it awaits or answers. The start follows after extensionRunStageHops more microtasks.
func (a *rpcAdmission) extensionRunStage() *coding.ExtensionRunStage {
	return &coding.ExtensionRunStage{
		Prepare: func(ctx context.Context, prepare func(context.Context)) {
			a.turn.Microtask(func() { a.awaitPreparation(ctx, prepare) })
		},
		Start: func(start func()) {
			a.commandRuns.whenNoneRuns(func() { a.turn.Hops(extensionRunStageHops, start) })
		},
	}
}

// awaitPreparation runs prepare as rpcAwaitHandler runs a handler: the executor waits for its first suspension or its end. A preparation that cannot start a task at shutdown runs on the executor.
func (a *rpcAdmission) awaitPreparation(ctx context.Context, prepare func(context.Context)) {
	entered := make(chan struct{})
	ctx = invocation.WithSuspensionAcknowledgment(ctx, func() { close(entered) })
	work := func() {
		defer invocation.Acknowledge(ctx)
		prepare(ctx)
	}
	if !a.taskGroup().Go(work) {
		work()
	}
	select {
	case <-entered:
	case <-a.ctx.Done():
	}
}

func (a *rpcAdmission) prompt(id rpcRequestID, cmd RPCPromptCommand) {
	if strings.HasPrefix(cmd.Message, "/") {
		// An unresolved slash command still awaits _tryExecuteExtensionCommand(false).
		a.turn.Microtask(func() { a.promptInput(id, cmd) })
		return
	}
	a.promptInput(id, cmd)
}

func (a *rpcAdmission) promptInput(id rpcRequestID, cmd RPCPromptCommand) {
	fail := func(err error) { a.turn.Microtask(func() { a.write(rpcError(id, "prompt", err.Error())) }) }
	if a.session.IsCompacting() {
		fail(fmt.Errorf("Cannot submit a prompt while compaction is in progress. Wait for compaction to finish and retry."))
		return
	}
	a.input(cmd.Message, rpcImages(cmd.Images), cmd.StreamingBehavior).then(func(input rpcInputResult, err error) {
		if a.ctx.Err() != nil {
			return
		}
		if err != nil {
			fail(err)
			return
		}
		if input.handled {
			a.write(rpcDispositionSuccess(id, "prompt", coding.DispositionHandled))
			return
		}
		text := a.catalog.expandPrompt(input.text)
		if a.session.IsStreaming() {
			if cmd.StreamingBehavior == "" {
				fail(fmt.Errorf("Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message."))
				return
			}
			if cmd.StreamingBehavior == "followUp" {
				a.session.QueueFollowUp(text, input.images)
			} else {
				a.session.QueueSteer(text, input.images)
			}
			if err := a.session.FlushEvents(a.ctx); err != nil {
				fail(err)
				return
			}
			a.turn.Microtask(func() { a.write(rpcDispositionSuccess(id, "prompt", coding.DispositionQueued)) })
			return
		}
		if err := a.validateModel(); err != nil {
			fail(err)
			return
		}
		// upstream: agent-session.ts:2133 prompt() awaits _startRun, which marks the Session busy before its compaction check and
		// before_agent_start (:1832-1837), so input that arrives meanwhile queues into this run or is rejected.
		start, err := a.session.BeginRunStart()
		if err != nil {
			fail(err)
			return
		}
		failStarted := func(err error) {
			start.Abandon()
			fail(err)
		}
		prepare := func() {
			a.beforeAgentStart(id, start, coding.BuildUserContent(text, input.images), len(input.images) > 0, failStarted)
		}
		if slices.ContainsFunc(a.session.Messages(), func(m agent.AgentMessage) bool { return m.Assistant != nil }) {
			checked := a.promptCompactionCheck()
			checked.then(func(_ struct{}, err error) {
				switch {
				case err != nil && !a.session.AgentRunAbortRequested():
					failStarted(err)
				case a.ctx.Err() == nil:
					prepare()
				default:
					start.Abandon()
				}
			})
		} else {
			prepare()
		}
	})
}

// promptCompactionCheck is prompt()'s compaction check. Pi's check resumes without I/O unless it compacts, so only a due compaction leaves the loop, and the
// checks of one batch resolve in call order: a check that follows a compaction still running decides behind it, off the loop, because the decision
// waits for the Session lock that the compaction holds.
func (a *rpcAdmission) promptCompactionCheck() *rpcPromise[struct{}] {
	if a.promptChecked == nil {
		a.promptChecked = rpcResolvedChannel
	}
	offLoop := func() *rpcPromise[struct{}] {
		var checked *rpcPromise[struct{}]
		checked, a.promptChecked = rpcAwaitInOrder(a, a.promptChecked, func() (struct{}, error) { return struct{}{}, a.session.CheckPromptCompaction(a.ctx) })
		return checked
	}
	select {
	case <-a.promptChecked:
		due, ok, err := a.session.BeginPromptCompactionCheck(a.ctx)
		if !ok {
			return offLoop()
		}
		if err != nil {
			return rpcResolved(a.turn, struct{}{}, err)
		}
		if due == nil {
			return rpcResolved(a.turn, struct{}{}, nil)
		}
		var checked *rpcPromise[struct{}]
		checked, a.promptChecked = rpcAwaitInOrder(a, a.promptChecked, func() (struct{}, error) { return struct{}{}, due(a.ctx) })
		return checked
	default:
		return offLoop()
	}
}

func (a *rpcAdmission) beforeAgentStart(id rpcRequestID, start *coding.RunStart, content []ai.UserContentBlock, hasImages bool, fail func(error)) {
	hasHandlers := a.runner != nil && a.runner.HasHandlers("before_agent_start")
	var preparation *rpcPromise[*coding.PreparedPrompt]
	if hasHandlers {
		preparation = rpcAwaitHandler(a, func(ctx context.Context) (*coding.PreparedPrompt, error) {
			return a.session.PreparePrompt(ctx, content)
		})
	} else {
		value, err := a.session.PreparePrompt(a.ctx, content)
		preparation = rpcResolved(a.turn, value, err)
	}
	preparation.then(func(prepared *coding.PreparedPrompt, err error) {
		// upstream: packages/coding-agent/src/core/agent-session.ts:_startRun awaits _prepareRun, an async function that
		// returns the starting messages, so the run starts (and "started" is written) one microtask after the
		// preparation settles; Pi 1.1.0 prepared inline in prompt().
		a.turn.Hops(a.beforeAgentStartHandlerHops()+1, func() {
			a.startPreparedPrompt(id, start, prepared, err, hasImages, fail)
		})
	})
}

// beforeAgentStartHandlerHops is the number of microtasks Pi's built-in before_agent_start handlers add to emitBeforeAgentStart. builtin:mcp's handler is an async function that awaits waitForDirectServers once (extensions/mcp/index.ts:1175-1181), so `await handler(event, ctx)` resumes two microtasks after it starts; its Go port returns synchronously.
func (a *rpcAdmission) beforeAgentStartHandlerHops() int {
	if a.runner == nil || !slices.Contains(a.runner.ExtensionPaths(), codingagent.BuiltinPathPrefix+"mcp") {
		return 0
	}
	return 2
}

func (a *rpcAdmission) startPreparedPrompt(id rpcRequestID, start *coding.RunStart, prepared *coding.PreparedPrompt, err error, hasImages bool, fail func(error)) {
	if a.ctx.Err() != nil {
		start.Abandon()
		return
	}
	if err != nil {
		fail(err)
		return
	}
	rpcAwaitWork(a, hasImages, func() (struct{}, error) { prepared.NormalizeImages(); return struct{}{}, nil }).then(func(_ struct{}, err error) {
		if a.ctx.Err() != nil {
			start.Abandon()
			return
		}
		if err != nil {
			fail(err)
			return
		}
		// upstream: agent-session.ts:1845-1849: the messages held while the run started are appended, and their events written, before
		// preflightResult("started").
		if flushed, err := start.FlushHeldMessages(); err != nil {
			fail(err)
			return
		} else if flushed {
			if err := a.session.FlushEvents(a.ctx); err != nil {
				fail(err)
				return
			}
		}
		a.write(rpcDispositionSuccess(id, "prompt", coding.DispositionStarted))
		run, err := a.session.BeginPreparedPrompt(a.ctx, start, prepared)
		if err != nil {
			return
		} // Acceptance is authoritative; subsequent failures belong to the run.
		// _handleAgentEvent awaits _emitExtensionEvent, which awaits runner.emit. Dispatch the real first event at that boundary, then resume the loop separately.
		a.turn.Microtask(func() {
			a.turn.Microtask(func() {
				start := func() (struct{}, error) {
					err := run.Start()
					if flushErr := a.session.FlushEvents(a.ctx); err == nil {
						err = flushErr
					}
					return struct{}{}, err
				}
				hasStartHandler := a.runner != nil && a.runner.HasHandlers("agent_start")
				rpcAwaitWork(a, hasStartHandler, start).then(func(_ struct{}, _ error) {
					a.runs.Go(func() { stackprime.Prime(); _, _ = run.Run() })
				})
			})
		})
	})
}
