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
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

func (m *InteractiveMode) handleSubmit(ctx context.Context, prompt string) {
	m.handleSubmitWithImages(ctx, prompt, nil)
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
	if m.isCompacting && !strings.HasPrefix(prompt, "!") && !m.resolvableSlashCommand(prompt) {
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
	if strings.HasPrefix(prompt, "!") {
		exclude := strings.HasPrefix(prompt, "!!")
		var cmd string
		if exclude {
			cmd = strings.TrimSpace(prompt[2:])
		} else {
			cmd = strings.TrimSpace(prompt[1:])
		}
		if cmd == "" {
			return // bare `!`: ignore (matches upstream)
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

// runPromptTurnWithImages renders the user message and starts a turn for
// prompt, which input handlers and expansion have already processed.
func (m *InteractiveMode) runPromptTurnWithImages(ctx context.Context, prompt string, images []ai.ImageContent) {
	// Show user message in chat. Rendered as a styled
	// box (UserMessageBlock) instead of the prior markdown blockquote
	// (`> **You:** ...`). The blockquote rendering was visually
	// ambiguous: any LLM output containing literal `> ` lines (e.g.
	// when reciting documentation that quotes things) rendered with
	// the same `│ ` bar as the user's own messages, making it look
	// like the LLM was speaking as the user. The styled-box approach
	// uses raw ANSI bg paint, which markdown output cannot mimic by
	// construction. Mirrors upstream `UserMessageComponent`
	// Spacer before the user block. Mirrors upstream addMessageToChat using
	// the chat container child count instead of a separate first-message flag.
	// The AssistantMessageBlock also adds a leading spacer when it has
	// visible content, so the assistant text gets its own separation.
	if !m.chatContainer.IsEmpty() {
		m.appendToChat(tui.NewSpacer(1))
	}
	m.appendToChat(m.newUserMessageBlock(prompt))
	m.skipNextUserMessageText = prompt
	m.tuiInst.Render()

	m.runTurnWithImages(ctx, prompt, images, func(runCtx context.Context) ([]agent.AgentMessage, error) {
		content := promptContent(prompt, images)
		autoResize := true
		if m.opts.SettingsManager != nil {
			autoResize = m.opts.SettingsManager.GetImageAutoResize()
		}
		content = NormalizePromptContent(content, autoResize, m.agent.Model())
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

// emitAgentSettledEvent awaits every settled handler before releasing actions
// that would start a replacement run.
func (m *InteractiveMode) emitAgentSettledEvent() {
	m.settledMu.Lock()
	m.emittingAgentSettled = true
	m.settledMu.Unlock()

	emitAgentSettled(m.newRunner)

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
	m.runTurnWithImages(ctx, prompt, nil, start)
}

func (m *InteractiveMode) runTurnWithImages(ctx context.Context, prompt string, images []ai.ImageContent, start func(context.Context) ([]agent.AgentMessage, error)) {
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

	go func() {
		defer func() {
			// Turn-end state (isIdle, workStart, statusLine, loaders) and the
			// final flush/render are read/rendered by the main input loop, so
			// apply them there instead of on this goroutine, which races
			// keystroke handling (resolveOutcome reads isIdle every keystroke).
			// runCtx so cleanup still applies after an aborted turn; it is
			// dropped only when the whole session is shutting down.
			m.runOnMain(m.runCtx, func() {
				if m.runGen == gen {
					m.clearTurnSystemPrompt()
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
			// before this one settles.
			if m.opts.SessionHandle != nil {
				m.opts.SessionHandle.OnAgentSettled()
			}
			m.emitAgentSettledEvent()
		}()
		// Pre-prompt compaction check: before sending the new user message,
		// compact if the prior context already exceeds the threshold, including
		// after an aborted turn. Mirrors upstream prompt()'s
		// _checkCompaction(lastAssistant, false). A custom-message seed has no
		// such check upstream (sendCustomMessage calls _runAgentPrompt directly).
		if prompt != "" {
			m.checkPromptCompaction(runCtx)
		}
		// Fire before_agent_start after the pre-prompt compaction check and off the
		// input loop. Upstream runs the async extension event after compaction and
		// before the provider request; Pig used to run it synchronously before
		// rendering the user's message, so a slow hook made Enter appear frozen.
		turnSystemPrompt := m.currentSystemPrompt()
		spOpts := m.opts.SystemPromptOptions
		// Mirror upstream: event.systemPromptOptions.cwd always reflects the
		// current session cwd (agent-session.ts:_rebuildSystemPrompt:916).
		if spOpts.Cwd == "" {
			spOpts.Cwd = m.opts.CWD
		}
		if combined := emitBeforeAgentStartWithImages(ctx, m.newRunner, prompt, images, turnSystemPrompt, spOpts); combined != nil {
			if combined.SystemPrompt != nil {
				turnSystemPrompt = *combined.SystemPrompt
			}
			// The Session adds the returned messages to this prompt.
			if session, ok := m.opts.SessionHandle.(interface {
				QueueAgentStartMessages([]extension.CustomMessageRef)
			}); ok {
				session.QueueAgentStartMessages(combined.Messages)
			}
		}
		m.setTurnSystemPrompt(turnSystemPrompt)
		m.agent.SetSystemPrompt(turnSystemPrompt)

		// The user prompt, assistant messages, and tool results are all persisted
		// incrementally by the OnMessagePersist hook (wired in coding.NewSession),
		// driven by the agent's message_end events. This mirrors upstream's
		// single message_end persistence site (agent-session.ts:511-525) and keeps
		// a mid-turn kill from losing the turn.
		_, err := m.runAgentPrompt(runCtx, start)
		err = m.settleTurn(runCtx, err)
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
	for {
		m.queueMu.Lock()
		if err != nil || ctx.Err() != nil || !m.agent.HasQueuedMessages() {
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
	m.queueMu.Unlock()
	if settled == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-settled:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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

// handleBashCommand runs a `!cmd` (or `!!cmd` excluded) directly via
// the bash executor.
//
// Mirrors upstream `interactive-mode.ts:5037-5126::handleBashCommand`.
// Three behaviors:
//
//  1. While agent is streaming: queue the bash component to a pending
//     buffer; flush after agent_end so message ordering is preserved.
//     Mirrors upstream `pendingBashComponents` flush.
//  2. While agent is idle: render inline immediately, run the command,
//     persist the result as a `bash_execution` session entry.
//  3. Esc cancels the running command (parallels LLM abort).
//
// Extension hook: emits `user_bash` before execution; a handler may return
// a full result (shown and recorded without running the command) or
// operations the command runs through.
func (m *InteractiveMode) handleBashCommand(ctx context.Context, command string, excludeFromContext bool) {
	// Pending-while-streaming queue: when agent is mid-turn, defer
	// the bash component into pendingBashBlocks; the goroutine that
	// runs the command publishes its block to chat after the agent's
	// turn finishes (handled by flushPendingBashBlocks below).
	deferred := !m.isIdle

	// Emit user_bash first. A failed handler was already reported; like
	// upstream, do not fall back to local execution.
	eventResult, err := emitUserBash(m.newRunner, command, m.opts.CWD, excludeFromContext)
	if err != nil {
		return
	}

	block := tui.NewBashExecutionBlock(command, excludeFromContext)

	// Apply the current global expansion state to blocks created after Ctrl+O.
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
		m.tuiInst.Render()
	}

	// An extension's full result is shown and recorded without running the
	// command (upstream handleBashCommand's eventResult.result branch).
	if eventResult != nil && eventResult.Result != nil {
		m.showUserBashResultOverride(block, command, excludeFromContext, eventResult.Result, deferred)
		return
	}
	var operations extension.BashOperations
	if eventResult != nil {
		operations = eventResult.Operations
	}

	// Mark bash as running so Esc can cancel it. resolveOutcome maps
	// Esc while busy to outcomeAbort. The bash context derives from m.abortCtx,
	// so cancellation reaps the process group.
	if !deferred {
		m.isIdle = false
	}
	bashCtx, bashCancel := context.WithCancel(m.abortCtx)
	prev := m.bashCancel
	m.bashCancel = bashCancel

	go func() {
		defer func() {
			// The process context is cancelled off-main (it only reaps the
			// process group). The shared fields it restores are touched on the
			// main loop, after the final render posted below (uiTaskCh is FIFO).
			bashCancel()
			m.runOnMain(m.runCtx, func() {
				m.bashCancel = prev
				if !deferred {
					m.isIdle = true
				}
			})
		}()

		// Upstream executeBash applies the shell command prefix and runs
		// through the extension's operations or local bash; the block and the
		// session record keep the command as typed.
		resolvedCommand := command
		if prefix := m.opts.Settings.GetCommandPrefix(); prefix != "" {
			resolvedCommand = prefix + "\n" + command
		}
		if operations == nil {
			operations = tools.NewLocalBashOperations(m.opts.Settings, filepath.Join(m.opts.AgentDir, "bin"))
		}
		res, err := tools.ExecuteBashWithOperations(bashCtx, resolvedCommand, m.opts.CWD, operations, BashExecOptions{
			// Streamed output must not be lost, so post reliably (backpressure)
			// onto the main loop rather than mutating the block + rendering from
			// this goroutine, which races keystroke handling. runCtx (not bashCtx)
			// so a cancelled bash still delivers output already produced.
			OnChunk: func(chunk string) {
				m.runOnMain(m.runCtx, func() {
					block.AppendOutput(chunk)
					m.tuiInst.Render()
				})
			},
		})
		if err != nil {
			// Upstream: the block completes without an exit code, the failure
			// is shown, and nothing is recorded.
			m.runOnMain(m.runCtx, func() {
				block.SetComplete(nil, false, false)
				if deferred {
					m.flushPendingBashBlocks()
				}
				m.showError("Bash command failed: " + err.Error())
			})
			return
		}
		m.finishUserBash(block, command, excludeFromContext, res, deferred)
	}()
}

// showUserBashResultOverride shows and records a user_bash handler's result
// in place of running the command.
func (m *InteractiveMode) showUserBashResultOverride(block *tui.BashExecutionBlock, command string, excludeFromContext bool, override any, deferred bool) {
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
	if result.Output != "" {
		block.AppendOutput(result.Output)
	}
	res := BashResult{Output: result.Output, ExitCode: result.ExitCode, Cancelled: result.Cancelled, Truncated: result.Truncated}
	if result.FullOutputPath != nil {
		res.FullOutputPath = *result.FullOutputPath
	}
	go m.finishUserBash(block, command, excludeFromContext, res, deferred)
}

// finishUserBash records a user bash result in the session and completes its
// block on the main loop.
func (m *InteractiveMode) finishUserBash(block *tui.BashExecutionBlock, command string, excludeFromContext bool, res BashResult, deferred bool) {
	// Persist to the session off the main loop (I/O); capture only a
	// warning to surface on the loop.
	var persistWarn string
	if m.currentSession() != nil {
		if _, perr := m.currentSession().AppendBashExecution(
			command, res.Output, res.ExitCode, res.Cancelled,
			res.Truncated, res.FullOutputPath, excludeFromContext,
		); perr != nil {
			persistWarn = perr.Error()
		}
	}
	// Apply the terminal block state on the main loop, after every OnChunk
	// post, so the block's output, completion, promotion, and render are
	// single-threaded with keystrokes.
	m.runOnMain(m.runCtx, func() {
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
