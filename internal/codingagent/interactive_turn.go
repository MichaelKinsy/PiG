package codingagent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/imageprocessing"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func (m *InteractiveMode) handleSubmit(ctx context.Context, prompt string) {
	m.handleSubmitWithImages(ctx, prompt, nil)
}

// isUserBashCommand requires a nonempty command after Pi's ! or !! prefix. Bare prefixes remain ordinary prompts.
func isUserBashCommand(text string) bool {
	return strings.HasPrefix(text, "!") && widthx.JSTrim(strings.TrimPrefix(text[1:], "!")) != ""
}

func (m *InteractiveMode) handleSubmitWithImages(ctx context.Context, prompt string, images []ai.ImageContent) {
	// Defensive drain for messages restored after a failed compaction-queue
	// delivery. Normal compaction completion flushes immediately, including the
	// WillRetry path that steers into the imminent retry turn.
	if !m.isCompacting && len(m.compactionQueue) > 0 {
		m.flushCompactionQueue(ctx, false)
	}

	// Queue model-bound inputs during compaction, but let local
	// slash commands and bash run immediately. Upstream's onSubmit handles its
	// builtin command if-chain (/model, /session, /fork, /tree, /new, ...) and
	// the !bash branch BEFORE the isCompacting gate, and inside the gate lets
	// extension commands through via isExtensionCommand; only plain prompts,
	// prompt templates, and skill commands (which expand into model input) are
	// queued (interactive-mode.ts:2530-2686). pig's registry holds those same
	// builtins plus extension-registered commands, so a prompt that resolves in
	// the registry is a local/UI dispatch that must run now, even mid-compaction.
	// Queueing it (as prior pig did) showed "/session" as a steering message
	// and stalled it until compaction finished.
	if m.isCompacting && !isUserBashCommand(prompt) && !m.resolvableSlashCommand(prompt) {
		m.compactionQueue = append(m.compactionQueue, compactionQueuedMessage{text: prompt, images: images, mode: compactionQueueSteer})
		m.statusLine.Flash("Queued message for after compaction", 2*time.Second)
		m.editor.SetText("")
		// Mirror upstream queueCompactionMessage: the queued message stays
		// visible in the pending container, not just a transient status.
		m.updatePendingMessagesDisplay()
		return
	}

	// `!cmd` and `!!cmd` bash-prefix interception. Runs
	// the command directly via the executor; output renders inline
	// as a `BashExecutionBlock` and persists to the session as a
	// `bash_execution` entry. `!!cmd` (double-bang) sets
	// excludeFromContext=true so the LLM doesn't see the entry on
	// its next turn. Mirrors upstream interactive-mode.ts:2505-2520.
	if isUserBashCommand(prompt) {
		exclude := strings.HasPrefix(prompt, "!!")
		var cmd string
		if exclude {
			cmd = widthx.JSTrim(prompt[2:])
		} else {
			cmd = widthx.JSTrim(prompt[1:])
		}
		// Upstream keeps the text and warns instead of starting a second
		// command, whose completion would mark the UI idle under the first.
		if m.bashCancel != nil {
			m.showWarning("A bash command is already running. Press Esc to cancel it first.")
			m.editor.SetText(prompt)
			return
		}
		m.handleBashCommand(ctx, cmd, exclude)
		return
	}

	// Registered commands (builtins, aliases, and extension commands) run
	// immediately, even while a run streams: upstream's onSubmit handles its
	// builtin chain first and prompt() executes extension commands before
	// anything is queued.
	if m.resolvableSlashCommand(prompt) {
		m.dispatchSlash(ctx, prompt)
		return
	}
	m.promptUserInput(ctx, prompt, images, false, extension.InputSourceUser, true)
}

// promptUserInput is upstream prompt() after command dispatch, and
// _queueUserInput for steer and follow-up. Extension input handlers see the
// raw text first, with the streaming behavior while a run is active; then
// skill commands and prompt templates expand (unless expand is false, as for
// an extension's sendUserMessage); then the text queues into the active run
// as a steering message, or as a follow-up with followUp, or starts a new
// turn when no run is active. An unresolved `/foo` is ordinary user text:
// upstream forwards it to the model rather than reporting an unknown command.
func (m *InteractiveMode) promptUserInput(ctx context.Context, text string, images []ai.ImageContent, followUp bool, source extension.InputSource, expand bool) {
	streaming := m.runStreaming()
	behavior := ""
	if streaming {
		behavior = "steer"
		if followUp {
			behavior = "followUp"
		}
	}
	text, images, handled, err := m.runInputHandlers(ctx, text, images, source, behavior)
	if err != nil {
		// Upstream prompt() rejects and the input is not sent; the error
		// is shown.
		m.showError(err.Error())
		return
	}
	if handled {
		return
	}
	if expand {
		// Upstream expansion order: _expandSkillCommand, then
		// expandPromptTemplate on its result.
		if expanded, ok := m.expandSkillCommand(text); ok {
			text = expanded
		}
		if expanded, ok := ExpandPromptTemplate(text, m.promptTemplates); ok {
			text = expanded
		}
	}
	if streaming && m.enqueueIfTurnActive(func() {
		if followUp {
			m.followUpMessageWithImages(text, images)
		} else {
			m.steerMessageWithImages(text, images)
		}
	}) {
		return
	}
	m.runPromptTurnWithImages(ctx, text, images)
}

// runInputHandlers runs the input handlers through the Session, or through
// the extension runner directly when the mode has no Session.
func (m *InteractiveMode) runInputHandlers(ctx context.Context, text string, images []ai.ImageContent, source extension.InputSource, behavior string) (string, []ai.ImageContent, bool, error) {
	if m.opts.SessionHandle != nil {
		return m.opts.SessionHandle.RunInputHandlers(ctx, text, images, source, behavior)
	}
	return RunInputHandlers(ctx, m.newRunner, text, images, source, behavior)
}

// runPromptTurnWithImages starts a turn for prompt, which input handlers and
// expansion have already processed. The user message renders from the agent's
// message_start event, as upstream interactive mode renders it: output that
// the caller, a compaction, or a before_agent_start handler produces before
// the run starts precedes it, and a prompt the run rejects is never shown.
func (m *InteractiveMode) runPromptTurnWithImages(ctx context.Context, prompt string, images []ai.ImageContent) {
	m.startTurn(ctx, prompt, images, true, func(runCtx context.Context) ([]agent.AgentMessage, error) {
		content := promptContent(prompt, images)
		autoResize := true
		if m.opts.SettingsManager != nil {
			autoResize = m.opts.SettingsManager.GetImageAutoResize()
		}
		content = NormalizePromptContent(content, autoResize, m.agent.Model(), imageprocessing.ProcessImage)
		// upstream: packages/coding-agent/src/core/agent-session.ts:_runAgentPrompt
		return m.agent.SendContent(runCtx, content)
	})
}

// runCustomMessageTurn starts a turn seeded with an extension's custom message,
// mirroring upstream sendCustomMessage's idle triggerTurn branch, which calls
// _runAgentPrompt with the message itself (agent-session.ts:1459).
//
// The message is its own turn seed, so there is no user text to echo: the
// message renders through the custom-message path before this runs.
func (m *InteractiveMode) runCustomMessageTurn(ctx context.Context, msg agent.AgentMessage) {
	m.runTurn(ctx, "", func(runCtx context.Context) ([]agent.AgentMessage, error) {
		// upstream: packages/coding-agent/src/core/agent-session.ts:_runAgentPrompt
		return m.agent.SendMessages(runCtx, []agent.AgentMessage{msg})
	})
}

// extensionIsIdle answers the extension-facing isIdle() from live run state.
//
// Upstream derives it on read (`get isIdle() { return !this._isAgentRunActive }`
// in agent-session.ts) and hands extensions that getter, so it cannot latch.
// m.isIdle is a UI mirror whose reset is queued through runOnMain, which drops
// the callback when runCtx finishes first; a dropped reset left every extension
// seeing a working agent for the life of the process. turnActive is cleared
// synchronously as the run goroutine unwinds, after continuations, retries and
// queued follow-ups have drained, which is the same window upstream measures.
//
// It also fixes the observed value at agent_settled. Upstream clears the flag
// before emitting that event; pig emits it after runOnMain has merely accepted
// the reset, so a cached read reported busy at the moment the run finished.
//
// The UI keeps m.isIdle: resolveOutcome reads it on every keystroke, and its
// main-loop ownership is what keeps that read race-free.
func (m *InteractiveMode) extensionIsIdle() bool {
	return !m.turnActive.Load()
}

// extensionSignal answers the extension-facing ctx.signal.
//
// upstream: agent-session.ts:3368 (`getSignal: () => this.agent.signal`)
func (m *InteractiveMode) extensionSignal() context.Context {
	current := m.extensionAgent.Load()
	if current == nil {
		return nil
	}
	return current.Signal()
}

// emitAgentSettledEvent awaits every settled handler before releasing actions
// that would start a replacement run.
func (m *InteractiveMode) emitAgentSettledEvent() {
	m.emitAgentSettledFor(m.newRunner)
}

func (m *InteractiveMode) emitAgentSettledFor(runner *inproc.Runner) {
	m.settledMu.Lock()
	m.emittingAgentSettled = true
	m.settledMu.Unlock()

	emitAgentSettled(runner)

	m.settledMu.Lock()
	m.emittingAgentSettled = false
	deferred := m.deferredSettledActions
	m.deferredSettledActions = nil
	m.settledMu.Unlock()
	for _, action := range deferred {
		action()
	}
}

// deferSettledAction queues action only while agent_settled handlers are being
// dispatched. It reports whether the caller must skip immediate execution.
func (m *InteractiveMode) deferSettledAction(action func()) bool {
	m.settledMu.Lock()
	defer m.settledMu.Unlock()
	if !m.emittingAgentSettled {
		return false
	}
	m.deferredSettledActions = append(m.deferredSettledActions, action)
	return true
}

// runTurn owns the turn lifecycle shared by every entry point: idle/turnActive
// bookkeeping, the run goroutine, error surfacing, and settle. start performs
// the low-level agent call that seeds the run, which differs per entry point.
// prompt is the user text for the before_agent_start event payload only; it is
// empty for turns not seeded by typed input.
func (m *InteractiveMode) runTurn(ctx context.Context, prompt string, start func(context.Context) ([]agent.AgentMessage, error)) {
	m.startTurn(ctx, prompt, nil, false, start)
}

// startTurn starts a run. userPrompt marks a turn seeded by a prompt, which
// upstream prompt() validates before compaction and before_agent_start; a
// rejected prompt shows its error and emits no run events.
func (m *InteractiveMode) startTurn(ctx context.Context, prompt string, images []ai.ImageContent, userPrompt bool, start func(context.Context) ([]agent.AgentMessage, error)) {
	m.isIdle = false
	// runGen identifies this run to its own UI cleanup, which runs later on
	// the owner loop: a cleanup that finds a newer run leaves that run's
	// busy state alone.
	m.runGen++
	gen := m.runGen
	m.queueMu.Lock()
	m.turnActive.Store(true)
	m.turnSettled = make(chan struct{})
	m.queueMu.Unlock()
	m.workStart = time.Now()
	m.statusLine.SetWorking(m.workingVisible)
	// The run's cancellation is Esc's abort context at the time the run starts;
	// the main loop replaces m.abortCtx after an abort, so the goroutine never
	// reads the field.
	runCtx := m.abortCtx
	session, runner := m.opts.SessionHandle, m.newRunner

	go func() {
		rejected := false
		defer func() {
			// Turn-end state (isIdle, workStart, statusLine, loaders) and the
			// final flush/render are read/rendered by the main input loop, so
			// apply them there instead of on this goroutine, which races
			// keystroke handling (resolveOutcome reads isIdle every keystroke).
			// runCtx so cleanup still applies after an aborted turn; it is
			// dropped only when the whole session is shutting down.
			m.runOnMain(m.runCtx, func() {
				if m.runGen == gen {
					m.isIdle = true
					m.workStart = time.Time{}
					m.statusLine.SetWorking(false)
					m.stopWorkingLoader() // belt-and-suspenders: ensure loader is removed on abort
				}
				// Any `!cmd` invocations queued during the
				// agent's turn now promote to chat. Mirrors upstream
				// `flushPendingBashComponents` called from agent_end.
				m.flushPendingBashBlocks()
				m.updatePendingMessagesDisplay() // clear stale queue indicators
				m.tuiInst.Render()
			})
			// Upstream prompt() throws before _runAgentPrompt, so a rejected
			// prompt has no agent_settled.
			if rejected {
				return
			}
			// Follow-up messages are now drained by the agent loop
			// itself (via followUpQueue). No explicit drain needed here.
			// The run has fully settled here (retries, recovery, compaction,
			// and queued input drained by runAgentPrompt and settleTurn),
			// mirroring upstream's _runAgentPrompt finally. Notifying the
			// cache warmer is upstream _emitAgentSettled's first step, ahead
			// of the extension event dispatch: interactive drives its own
			// agent_settled instead of going through
			// coding.Session.emitAgentSettled, so it calls OnAgentSettled
			// directly here. Awaiting every settled handler before releasing a
			// deferred action keeps a handler-started run from beginning
			// before this one settles. The run's prompt inputs end first, as
			// upstream clears _runSystemPromptOptions before agent_settled.
			if session != nil {
				session.OnAgentSettled()
			}
			m.emitAgentSettledFor(runner)
		}()
		// agent-session.ts:1673-1691 validates the model and its auth before
		// the compaction check and before_agent_start. interactive-mode.ts
		// shows the rejection with showError.
		if userPrompt {
			if err := m.validatePromptModelAuth(runCtx); err != nil {
				rejected = true
				_ = m.settleTurn(runCtx, err)
				if runCtx.Err() == nil {
					m.runOnMain(m.runCtx, func() { m.showError(err.Error()) })
				}
				return
			}
		}
		// Pre-prompt compaction check: before sending the new user message,
		// compact if the prior context already exceeds the threshold, including
		// after an aborted turn. Mirrors upstream prompt()'s
		// _checkCompaction(lastAssistant, false). A custom-message seed has no
		// such check upstream (sendCustomMessage calls _runAgentPrompt directly).
		if userPrompt {
			m.checkPromptCompaction(runCtx)
		}
		// Fire before_agent_start after the pre-prompt compaction check and off the
		// input loop. Upstream runs the async extension event after compaction and
		// before the provider request; Pig used to run it synchronously before
		// rendering the user's message, so a slow hook made Enter appear frozen.
		// The run's prompt and tool loadout come from the handlers' result.
		var err error
		if userPrompt {
			err = m.prepareRunPrompt(runCtx, runner, gen, prompt, images)
			rejected = err != nil
		}

		// The user prompt, assistant messages, and tool results are all persisted
		// incrementally by the OnMessagePersist hook (wired in coding.NewSession),
		// driven by the agent's message_end events. This mirrors upstream's
		// single message_end persistence site (agent-session.ts:511-525) and keeps
		// a mid-turn kill from losing the turn.
		if err == nil {
			_, err = m.runAgentPrompt(runCtx, start)
		}
		err = m.settleTurnWithPrompt(runCtx, err, func() { m.endRunPrompt(gen) })
		// Only the run's own cancellation (Esc or shutdown) is silent; any
		// other error, a deadline from elsewhere included, is shown.
		if err != nil && runCtx.Err() == nil {
			m.showTurnError(err)
		}
		m.runOnMain(m.runCtx, func() { m.tuiInst.Render() })
	}()
}

// checkPromptCompaction runs the Session's pre-prompt compaction check. A
// failure is shown, and the prompt is still sent, as upstream prompt() awaits
// _checkCompaction, which reports its own failures through compaction_end.
func (m *InteractiveMode) checkPromptCompaction(ctx context.Context) {
	if m.opts.SessionHandle == nil {
		return
	}
	if err := m.opts.SessionHandle.CheckPromptCompaction(ctx); err != nil && ctx.Err() == nil {
		m.runOnMain(m.runCtx, func() {
			m.appendChatBlock(tui.NewText("\033[31mError: " + err.Error() + "\033[0m"))
		})
	}
}

// runAgentPrompt runs start to settlement through the Session's run loop
// (upstream _runAgentPrompt): automatic retry, overflow and length recovery,
// threshold compaction, and continuation from queued input. Without a Session
// (a mode constructed directly in a unit test) there is no retry or
// compaction, and only queued input continues the run.
func (m *InteractiveMode) runAgentPrompt(ctx context.Context, start func(context.Context) ([]agent.AgentMessage, error)) ([]agent.AgentMessage, error) {
	if m.opts.SessionHandle != nil {
		return m.opts.SessionHandle.RunAgentPrompt(ctx, start)
	}
	messages, err := start(ctx)
	for err == nil && ctx.Err() == nil && m.agent.HasQueuedMessages() {
		// upstream: packages/coding-agent/src/core/agent-session.ts:_runAgentPrompt
		messages, err = m.agent.Continue(ctx)
	}
	return messages, err
}

// settleTurn ends the run like upstream's before-settle step: input queued
// after the run loop's last check starts another run instead of waiting in the
// queue. The check and the turnActive clear happen under queueMu, the lock
// every main-loop path holds while it decides between queueing into this run
// and starting a new one, so no message can land in a queue nothing drains.
// Upstream gets the same guarantee from its single-threaded event loop. It
// returns the error of the last run.
func (m *InteractiveMode) settleTurn(ctx context.Context, err error) error {
	return m.settleTurnWithPrompt(ctx, err, nil)
}

func (m *InteractiveMode) settleTurnWithPrompt(ctx context.Context, err error, finishPrompt func()) error {
	for {
		m.queueMu.Lock()
		if err != nil || ctx.Err() != nil || !m.agent.HasQueuedMessages() {
			// Clear the outgoing prompt before publishing idle or releasing replacement waiters.
			if finishPrompt != nil {
				finishPrompt()
			}
			m.turnActive.Store(false)
			if m.turnSettled != nil {
				close(m.turnSettled)
				m.turnSettled = nil
			}
			m.queueMu.Unlock()
			return err
		}
		m.queueMu.Unlock()
		_, err = m.runAgentPrompt(ctx, m.agent.Continue)
	}
}

// submitInitialMessages sends each message as its own prompt after the
// previous run settles, as upstream awaits session.prompt for every entry of
// initialMessages before the interactive loop. It runs off the owner loop and
// stops when ctx ends.
func (m *InteractiveMode) submitInitialMessages(ctx context.Context, messages []string) {
	for _, message := range messages {
		if m.waitForIdle(ctx) != nil {
			return
		}
		submitted := make(chan struct{})
		if m.postToMain(ctx, func() {
			defer close(submitted)
			m.handleSubmit(ctx, message)
		}) != nil {
			return
		}
		select {
		case <-submitted:
		case <-ctx.Done():
			return
		}
	}
}

// waitForIdle blocks until no run is active, like upstream
// AgentSession.waitForIdle, which resolves when the run settles rather than
// polling. It returns early only when ctx ends.
func (m *InteractiveMode) waitForIdle(ctx context.Context) error {
	m.queueMu.Lock()
	settled := m.turnSettled
	session := m.opts.SessionHandle
	m.queueMu.Unlock()
	return waitForSessionIdle(ctx, settled, session)
}

// waitForSessionIdle joins the captured outgoing Session even if a UI task rebinds the mode while its turn cleanup is pending.
func waitForSessionIdle(ctx context.Context, settled <-chan struct{}, session InteractiveSessionHandle) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if settled != nil {
		select {
		case <-settled:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if session != nil {
		return session.WaitForIdle(ctx)
	}
	return nil
}

// showTurnError surfaces a failed run on the main loop.
func (m *InteractiveMode) showTurnError(err error) {
	// Upstream shows the error text; it never starts a login from it.
	errStr := err.Error()
	switch {
	case errors.Is(err, agent.ErrNoModelSelected):
		// No model / not logged in. Mirror upstream prompt(), which
		// throws formatNoModelSelectedMessage() with login + /model
		// guidance instead of a bare error.
		m.runOnMain(m.runCtx, func() {
			m.appendChatBlock(tui.NewText("\033[33m" + FormatNoModelSelectedMessage() + "\033[0m"))
			m.tuiInst.Render()
		})
	default:
		m.runOnMain(m.runCtx, func() {
			m.appendChatBlock(tui.NewText("\033[31mError: " + errStr + "\033[0m"))
		})
	}
}

// handleBashCommand owns the awaited user_bash dispatch, execution and persistence off the input loop. UI mutations return to the owner loop; hook failure never falls back to local execution.
// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts.
func (m *InteractiveMode) handleBashCommand(ctx context.Context, command string, excludeFromContext bool) {
	runner, session := m.newRunner, m.currentSession()
	cwd, settings, agentDir := m.opts.CWD, m.opts.Settings, m.opts.AgentDir
	m.startUserBashTask(ctx, func(task *userBashTask) {
		eventResult, err := emitUserBash(task.ctx, runner, command, cwd, excludeFromContext)
		if err != nil || task.ctx.Err() != nil {
			return
		}
		var override *BashResult
		var operations extension.BashOperations
		if eventResult != nil {
			operations = eventResult.Operations
			if eventResult.Result != nil {
				result := userBashResultOverride(eventResult.Result)
				override = &result
			}
		}
		var block *tui.BashExecutionBlock
		var deferred bool
		if !m.awaitUserBashMain(task.owner, func() {
			if task.ctx.Err() != nil {
				return
			}
			// Upstream reads streaming state after the hook has completed.
			deferred = m.runStreaming()
			block = tui.NewBashExecutionBlock(command, excludeFromContext)
			m.toolMu.Lock()
			m.bashOrder = append(m.bashOrder, block)
			if m.toolsExpanded {
				block.SetExpanded(true)
			}
			m.toolMu.Unlock()
			if deferred {
				m.pendingBashBlocksMu.Lock()
				m.pendingBashBlocks = append(m.pendingBashBlocks, block)
				m.pendingBashBlocksMu.Unlock()
			} else {
				m.appendToChat(block)
			}
			if override != nil {
				block.AppendOutput(override.Output)
			} else {
				task.running = true
				m.bashCancel = m.cancelRunningUserBash
				if !deferred {
					m.isIdle = false
				}
			}
			m.tuiInst.Render()
		}) || block == nil {
			return
		}
		if override != nil {
			m.finishUserBash(task.owner, session, block, command, excludeFromContext, *override, deferred)
			return
		}
		resolvedCommand := command
		if prefix := settings.GetCommandPrefix(); prefix != "" {
			resolvedCommand = prefix + "\n" + command
		}
		if operations == nil {
			operations = tools.NewLocalBashOperations(settings, filepath.Join(agentDir, "bin"))
		}
		res, err := tools.ExecuteBashWithOperations(task.ctx, resolvedCommand, cwd, operations, BashExecOptions{
			OnChunk: func(chunk string) {
				m.awaitUserBashMain(task.owner, func() {
					block.AppendOutput(chunk)
					m.tuiInst.Render()
				})
			},
		})
		if err != nil {
			m.awaitUserBashMain(task.owner, func() {
				block.SetComplete(nil, false, false)
				if deferred {
					m.flushPendingBashBlocks()
				}
				m.showError("Bash command failed: " + err.Error())
			})
			return
		}
		m.finishUserBash(task.owner, session, block, command, excludeFromContext, res, deferred)
	})
}

func userBashResultOverride(override any) BashResult {
	var result struct {
		Output         string  `json:"output"`
		ExitCode       *int    `json:"exitCode"`
		Cancelled      bool    `json:"cancelled"`
		Truncated      bool    `json:"truncated"`
		FullOutputPath *string `json:"fullOutputPath"`
	}
	if encoded, err := json.Marshal(override); err == nil {
		_ = json.Unmarshal(encoded, &result)
	}
	res := BashResult{Output: result.Output, ExitCode: result.ExitCode, Cancelled: result.Cancelled, Truncated: result.Truncated}
	if result.FullOutputPath != nil {
		res.FullOutputPath = *result.FullOutputPath
	}
	return res
}

// finishUserBash persists to the captured Session, then completes the block on its owner loop.
func (m *InteractiveMode) finishUserBash(ctx context.Context, session *Session, block *tui.BashExecutionBlock, command string, excludeFromContext bool, res BashResult, deferred bool) {
	var persistWarn string
	if session != nil {
		if _, perr := session.AppendBashExecution(BashExecutionMessage{
			Role: "bashExecution", Command: command, Output: res.Output,
			ExitCode: res.ExitCode, Cancelled: res.Cancelled, Truncated: res.Truncated,
			FullOutputPath: res.FullOutputPath, ExcludeFromContext: excludeFromContext,
			Timestamp: time.Now().UnixMilli(),
		}); perr != nil {
			persistWarn = perr.Error()
		}
	}
	// Apply the terminal block state on the main loop, after every OnChunk
	// post, so the block's output, completion, promotion, and render are
	// single-threaded with keystrokes.
	m.awaitUserBashMain(ctx, func() {
		block.SetCompleteWithOutput(res.ExitCode, res.Cancelled, res.Truncated, res.Output, res.FullOutputPath)
		// A deferred block is appended after the current chat content, which
		// preserves the order observed by the user.
		if deferred {
			m.flushPendingBashBlocks()
		}
		if persistWarn != "" {
			m.appendToChat(tui.NewText("\033[33mbash session persist warning: " + persistWarn + "\033[0m"))
		}
		m.tuiInst.Render()
	})
}

// flushPendingBashBlocks promotes deferred bash components from the
// pending buffer to the chat container. Called when a bash goroutine
// finishes (and again from agent_end via runner so any blocks queued
// during streaming surface promptly). Idempotent: safe to call when
// there's nothing pending.
func (m *InteractiveMode) flushPendingBashBlocks() {
	m.pendingBashBlocksMu.Lock()
	pending := m.pendingBashBlocks
	m.pendingBashBlocks = nil
	m.pendingBashBlocksMu.Unlock()
	for _, b := range pending {
		m.appendToChat(b)
	}
	if len(pending) > 0 {
		m.tuiInst.Render()
	}
}

// dispatchSlash routes a `/cmd …` line through the registry. Builtins run on
// the input loop; extension command wrappers start their awaited handler off-loop
// so Promise-equivalent UI calls can post mutations back to that loop.
