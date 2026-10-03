// Ports packages/coding-agent/src/modes/rpc/rpc-mode.ts
package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

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
	p.turn.after(func() { fn(value, err) })
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
	p.turn.enqueue(callbacks)
}

type rpcInputResult struct {
	text    string
	images  []ai.ImageContent
	handled bool
}
type rpcAdmission struct {
	ctx           context.Context
	turn          *rpcResponseTurn
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
	// commands joins in-flight extension commands for the shutdown flush; nil when no subprocess runtime can report a suspended command.
	commands *rpcCommandJoin
}

func (a *rpcAdmission) taskGroup() *rpcTaskGroup {
	if a.shared != nil {
		return a.shared
	}
	return &a.tasks
}

func rpcAwaitWork[T any](a *rpcAdmission, slow bool, work func() (T, error)) *rpcPromise[T] {
	if !slow {
		value, err := work()
		return rpcResolved(a.turn, value, err)
	}
	p := &rpcPromise[T]{turn: a.turn}
	if !a.taskGroup().Go(func() { value, err := work(); p.resolve(value, err) }) {
		var zero T
		p.resolve(zero, context.Canceled)
	}
	return p
}

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
	pending := rpcAwaitHandler(a, func(ctx context.Context) (struct{}, error) {
		a.catalog.executeCommand(ctx, name, args)
		return struct{}{}, nil
	})
	if a.commandDone != nil {
		a.commandDone()
	}
	pending.then(func(_ struct{}, _ error) {
		a.turn.after(func() {
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

func (a *rpcAdmission) prompt(id rpcRequestID, cmd RPCPromptCommand) {
	if strings.HasPrefix(cmd.Message, "/") {
		// An unresolved slash command still awaits _tryExecuteExtensionCommand(false).
		a.turn.after(func() { a.promptInput(id, cmd) })
		return
	}
	a.promptInput(id, cmd)
}

func (a *rpcAdmission) promptInput(id rpcRequestID, cmd RPCPromptCommand) {
	fail := func(err error) { a.turn.after(func() { a.write(rpcError(id, "prompt", err.Error())) }) }
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
			a.turn.after(func() { a.write(rpcDispositionSuccess(id, "prompt", coding.DispositionQueued)) })
			return
		}
		if err := a.validateModel(); err != nil {
			fail(err)
			return
		}
		prepare := func() {
			a.beforeAgentStart(id, coding.BuildUserContent(text, input.images), len(input.images) > 0, fail)
		}
		if slices.ContainsFunc(a.session.Messages(), func(m agent.AgentMessage) bool { return m.Assistant != nil }) {
			rpcAwaitWork(a, true, func() (struct{}, error) { return struct{}{}, a.session.CheckPromptCompaction(a.ctx) }).then(func(_ struct{}, err error) {
				if err != nil {
					fail(err)
				} else if a.ctx.Err() == nil {
					prepare()
				}
			})
		} else {
			prepare()
		}
	})
}
func (a *rpcAdmission) beforeAgentStart(id rpcRequestID, content []ai.UserContentBlock, hasImages bool, fail func(error)) {
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
		if a.ctx.Err() != nil {
			return
		}
		if err != nil {
			fail(err)
			return
		}
		rpcAwaitWork(a, hasImages, func() (struct{}, error) { prepared.NormalizeImages(); return struct{}{}, nil }).then(func(_ struct{}, err error) {
			if a.ctx.Err() != nil {
				return
			}
			if err != nil {
				fail(err)
				return
			}
			a.write(rpcDispositionSuccess(id, "prompt", coding.DispositionStarted))
			run, err := a.session.BeginPreparedPrompt(a.ctx, prepared)
			if err != nil {
				return
			} // Acceptance is authoritative; subsequent failures belong to the run.
			// _handleAgentEvent awaits _emitExtensionEvent, which awaits runner.emit. Dispatch the real first event at that boundary, then resume the loop separately.
			a.turn.after(func() {
				a.turn.after(func() {
					start := func() (struct{}, error) {
						err := run.Start()
						if flushErr := a.session.FlushEvents(a.ctx); err == nil {
							err = flushErr
						}
						return struct{}{}, err
					}
					hasStartHandler := a.runner != nil && a.runner.HasHandlers("agent_start")
					rpcAwaitWork(a, hasStartHandler, start).then(func(_ struct{}, _ error) {
						a.runs.Go(func() { _, _ = run.Run() })
					})
				})
			})
		})
	})
}
