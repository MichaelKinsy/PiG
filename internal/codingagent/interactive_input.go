package codingagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// inputLoop reads terminal input from source and dispatches to the editor or
// agent.
func (m *InteractiveMode) inputLoop(ctx context.Context, source io.Reader) error {
	readCh := make(chan inputChunk)
	errCh := make(chan error, 1)
	go m.pumpTerminalInput(ctx, source, readCh, errCh)

	dispatchInput := func(input inputChunk) error {
		defer input.ticket.settle()
		chunk := string(input.data)
		if chunk == "" {
			return nil
		}
		if os.Getenv("PIG_DEBUG_KEYS") != "" {
			debugLog("key %q -> %d", chunk, classifyKey(chunk))
		}
		if err := m.dispatchInputChunk(ctx, chunk, input.ticket); err != nil {
			return err
		}
		// Match upstream's immediate keyboard paint before asynchronous
		// autocomplete results request their throttled follow-up frame.
		m.tuiInst.Render()
		return nil
	}

	for {
		if err := m.inputLoopErr; err != nil {
			return err
		}
		if m.requestExit.Load() {
			m.stopInteractiveTui()
			if err := m.requestedExitError(); err != nil {
				return err
			}
			emitSessionShutdown(m.newRunner, "quit")
			m.printResumeHint()
			return nil
		}
		// Prioritize terminal input over agent/render events. During tool output or
		// streaming, eventCh/uiTaskCh can stay hot; without this pre-check a waiting
		// keystroke can sit behind repeated render work.
		switch kind, buf := priorityInput(readCh, nil); kind {
		case priorityInputClosed:
			readCh = nil
			continue
		case priorityInputRead:
			if err := dispatchInput(buf); err != nil {
				return err
			}
			continue
		case priorityInputFlush, priorityInputNone:
		}
		select {
		case <-ctx.Done():
			// Signal-triggered shutdown (SIGTERM cancels the root ctx).
			// Emit session_shutdown so extensions run cleanup before the
			// deferred terminal restore; in-flight ops are already aborting
			// because m.abortCtx derives from ctx. Mirrors upstream
			// shutdown({fromSignal}) which emits session_shutdown reason
			// "quit" (interactive-mode.ts:3290, agent-session-runtime.ts:380).
			//
			// ShutdownFromSignal normally emits this earlier, before the root
			// context is cancelled, because cancelling it kills the extension
			// subprocesses that would otherwise receive the event. This covers
			// cancellations that do not arrive through that path.
			m.ShutdownFromSignal()
			return nil
		case err := <-errCh:
			return err
		case fn := <-m.uiTaskCh:
			// A background worker posted a UI mutation (e.g. async autocomplete
			// results). Run it here so editor/component state is touched only on
			// this goroutine, single-threaded with keystroke handling.
			fn()
		case <-m.renderWakeCh:
			m.runScheduledRender()
		case <-m.extensionErrorWakeCh:
			m.showPendingExtensionErrors()
		case ev, ok := <-m.eventCh:
			// Agent live events (streaming deltas, tool exec, compaction). Handle
			// on this goroutine so the component tree is mutated + rendered
			// single-threaded with keystrokes and posted UI tasks: mirrors
			// upstream's single JS event loop. m.eventCh closes only at session
			// shutdown; nil-out so the disabled case stops selecting.
			if !ok {
				m.eventCh = nil
				continue
			}
			m.handleAgentEvent(ev)
		case buf, ok := <-readCh:
			if !ok {
				readCh = nil
				continue
			}
			if err := dispatchInput(buf); err != nil {
				return err
			}
		}
	}
}

// normalizeInputSequence applies upstream ProcessTerminal.forwardInputSequence's
// native Shift+Enter normalization to one complete StdinBuffer sequence.
var normalizeInputSequence = tui.NormalizeProcessInputSequence

// normalizeInputSequences normalizes freshly read sequences in place. Input
// retained across a startup prompt was normalized when it was read.
func normalizeInputSequences(sequences []string) []string {
	for i, sequence := range sequences {
		sequences[i] = normalizeInputSequence(sequence)
	}
	return sequences
}

// pumpTerminalInput owns the one StdinBuffer for the process input stream and
// routes only complete sequences. Upstream ProcessTerminal parses input before
// focus dispatch, so switching between the editor and a modal cannot split one
// terminal read differently or lose a partial escape sequence.
//
// Upstream's terminal-input listeners answer synchronously before anything else sees a chunk. A chunk routed to the main loop therefore holds all input after it until its listeners settle, including a subprocess listener whose verdict the main loop awaits without blocking. Ctrl+C follows the same ordering as every other key. While a verdict is pending the pump stops receiving raw input, leaving at most one read ahead in the reader worker.
func (m *InteractiveMode) pumpTerminalInput(ctx context.Context, source io.Reader, readCh chan<- inputChunk, errCh chan<- error) {
	rawCh := make(chan []byte)
	rawErrCh := make(chan error, 1)
	go func() {
		for {
			buf, err := tui.ReadInput(source)
			if err != nil {
				rawErrCh <- err
				return
			}
			select {
			case rawCh <- append([]byte(nil), buf...):
			case <-ctx.Done():
				return
			}
		}
	}()

	stdinBuf := newProcessStdinBuffer()
	var flush stdinFlushTimer
	var backlog inputBacklog
	route := func(chunks []string) {
		for _, chunk := range chunks {
			if chunk == "" || tui.HandleKeyboardProtocolNegotiationSequence(chunk) {
				continue
			}
			backlog.held = append(backlog.held, chunk)
		}
		for ctx.Err() == nil && backlog.waiting == nil {
			if len(backlog.held) == 0 {
				backlog.held = nil
				return
			}
			chunk := backlog.held[0]
			backlog.held[0] = ""
			backlog.held = backlog.held[1:]
			backlog.waiting = m.routeInputChunk(ctx, []byte(chunk), readCh)
		}
	}
	process := func(buf []byte) {
		route(normalizeInputSequences(stdinBuf.ProcessTerminalBytes(buf)))
		flush.sync(stdinBuf)
	}
	flushPending := func() {
		flush.stop()
		route(normalizeInputSequences(stdinBuf.Flush()))
	}

	// Startup dialogs (notably project trust) can finish in the middle of one
	// decoded terminal read. Deliver the type-ahead after the main editor takes
	// focus instead of letting terminal teardown discard it.
	route(takeStartupInput())

	defer close(readCh)
	var readErr error
	for {
		if backlog.waiting != nil {
			select {
			case <-ctx.Done():
				flush.stop()
				return
			case <-backlog.waiting.done:
				backlog.waiting = nil
				route(nil)
				continue
			}
		}
		if readErr != nil && len(backlog.held) == 0 {
			select {
			case errCh <- readErr:
			case <-ctx.Done():
			}
			return
		}
		switch kind, buf := priorityInput(rawCh, flush.C); kind {
		case priorityInputRead:
			process(buf)
			continue
		case priorityInputFlush:
			flushPending()
			continue
		case priorityInputClosed, priorityInputNone:
		}
		select {
		case <-ctx.Done():
			flush.stop()
			return
		case err := <-rawErrCh:
			// Everything read before the error is still delivered in order.
			rawErrCh = nil
			readErr = err
			flushPending()
		case buf := <-rawCh:
			process(buf)
		case <-flush.C:
			flushPending()
		}
	}
}

type priorityInputKind int

const (
	priorityInputNone priorityInputKind = iota
	priorityInputRead
	priorityInputClosed
	priorityInputFlush
)

// priorityInput returns the ready input-loop work that runs before agent and
// render events, without blocking. Waiting terminal input wins over an expired
// flush timeout: the continuation of a split escape sequence must join its
// prefix, not find the prefix already flushed as a key. Upstream gets the same
// order because StdinBuffer.process clears its timeout before anything else.
func priorityInput[T any](readCh <-chan T, flushC <-chan time.Time) (priorityInputKind, T) {
	var none T
	select {
	case buf, ok := <-readCh:
		if !ok {
			return priorityInputClosed, none
		}
		return priorityInputRead, buf
	default:
	}
	select {
	case <-flushC:
		return priorityInputFlush, none
	default:
	}
	return priorityInputNone, none
}

// dispatchKey processes a single keystroke (post-splitting) that no input pump
// waits on.
func (m *InteractiveMode) dispatchKey(ctx context.Context, data string) error {
	return m.dispatchInputChunk(ctx, data, nil)
}

// dispatchInputChunk processes one keystroke routed with ticket. When a
// subprocess terminal-input listener must answer first, it returns before the
// keystroke is handled, and handling resumes on the main loop with the verdict.
func (m *InteractiveMode) dispatchInputChunk(ctx context.Context, data string, ticket *inputTicket) error {
	// In fullscreen mode, viewport input (mouse wheel/click, focus events) is
	// handled by the alt-screen renderer and must not reach the editor. Mirrors
	// upstream's addInputListener(handleViewportInput) on the alt-screen; pig is
	// driver-owned, so the driver routes it explicitly.
	if m.altScreen != nil {
		consumed := m.altScreen.HandleViewportInput(data)
		m.syncEditorFocusWithSearch()
		if consumed {
			return nil
		}
	}
	if m.extensionDialog != nil {
		// The dialog is the focused component, so releases are dropped unless
		// it opts in. The editor's equivalent check sits below this branch and
		// never runs while a dialog is open, which is what made every arrow
		// press move a selector cursor two rows.
		if m.tuiInst != nil && m.tuiInst.ConsumeCellSizeResponse(data) {
			return nil
		}
		if !tui.ShouldDeliverKey(m.extensionDialog.component, data) {
			return nil
		}
		m.extensionDialog.handle(data)
		return nil
	}

	// Notify extension terminal-input listeners first. If any consume
	// the input, skip normal dispatch. Mirrors upstream
	// ui.addInputListener (interactive-mode.ts:1875).
	return m.passTerminalInput(ctx, data, ticket, m.handleKey)
}

// handleKey handles a keystroke the terminal-input listeners passed on.
func (m *InteractiveMode) handleKey(ctx context.Context, data string) error {
	// Consume the terminal's reply to the startup cell-size query so it never
	// reaches the editor. Mirrors upstream tui.ts handleTerminalInput, which
	// checks consumeCellSizeResponse after the input listeners and before the
	// focused component.
	if m.tuiInst != nil && m.tuiInst.ConsumeCellSizeResponse(data) {
		return nil
	}

	// While fullscreen transcript search has focus it is upstream's focused
	// component, so the remaining keys edit its query instead of the editor.
	if m.altScreen != nil && m.altScreen.HandleFocusedSearchInput(data) {
		return nil
	}

	// Generic overlays and mouse-focused nested controls use the renderer's
	// focus target. The application editor keeps the driver-owned action routing
	// below; every other focused component receives input directly, as TuiBase
	// does upstream.
	if m.tuiInst != nil {
		focused := m.tuiInst.FocusedComponent()
		if focused != nil && focused != m.editor {
			if tui.ShouldDeliverKey(focused, data) {
				if input, ok := focused.(tui.InputHandler); ok {
					input.HandleInput(data)
					m.tuiInst.RequestRender()
				}
			}
			return nil
		}
	}

	// Drop Kitty key-release events before the editor / keybinding dispatch.
	// The alt-screen viewport handler (above) and extension terminal-input
	// listeners see raw input, but the focused editor must not act on a release
	// or every keystroke fires twice under the Kitty keyboard protocol
	// (extendedKeyInit pushes \x1b[>7u, whose flag 2 reports event types).
	// Mirrors upstream tui.ts:887 (isKeyRelease(data) &&
	// !focusedComponent.wantsKeyRelease).
	if !tui.ShouldDeliverKey(m.editor, data) {
		return nil
	}

	action := classifyKeyWithBindings(data, m.keybindings)
	// Upstream CustomEditor gates app.exit on getText().length === 0. Spaces
	// and newlines are editor content: Ctrl+D must fall through to the editor's
	// delete-char-forward binding rather than exit.
	editorEmpty := m.editor.Text() == ""
	// Pi temporarily replaces the default editor's Escape callback while
	// compaction runs. Compaction itself may be active while the agent is idle,
	// so routing through the normal idle/working outcome table makes Escape a
	// no-op. Preserve the same priority explicitly before that table.
	if action == actionInterrupt && m.isCompacting && m.branchSummaryCancel == nil && m.opts.SessionHandle != nil {
		m.opts.SessionHandle.AbortCompaction()
		return nil
	}

	// A background OAuth login (github-copilot / anthropic device/PKCE flow)
	// polls while the main loop stays live and shows a "Ctrl+C to cancel" hint.
	// Esc or Ctrl+C aborts that polling window, matching the hint and upstream's
	// login-dialog abort. This takes priority over the idle clear-editor /
	// double-Esc handling; cancelActiveLogin is a no-op when no login is active.
	if action == actionInterrupt || action == actionClearEditor {
		if m.cancelActiveLogin() {
			m.tuiInst.Render()
			return nil
		}
	}

	// When the slash-autocomplete popup is open, intercept
	// Esc (dismiss) and Enter (accept + maybe submit) before the
	// idle/working state machine sees them. Tab and arrows are
	// dispatched into the editor as normal `actionInsert`s and the
	// editor's HandleInput honors the popup-open guard.
	if m.editor.AutocompleteOpen() {
		switch action {
		case actionInterrupt:
			m.editor.AutocompleteCancel()
			m.tuiInst.Render()
			return nil
		case actionSubmit:
			submit := m.editor.AutocompleteAccept()
			if submit {
				text := strings.TrimSpace(m.editor.Text())
				if text != "" {
					m.editor.Clear()
					m.handleSubmit(ctx, text)
				}
			}
			// An accepted argument completion stays in the editor
			// (upstream editor.ts tui.select.confirm returns after
			// applying a completion whose prefix does not start with "/").
			m.tuiInst.Render()
			return nil
		}
	}

	if action == actionInterrupt && m.branchSummaryCancel != nil {
		m.branchSummaryCancel()
		m.opts.SessionHandle.AbortBranchSummary()
		return nil
	}

	switch resolveOutcome(action, m.isIdle, editorEmpty) {
	case outcomeExit:
		m.requestShutdown()
		return nil

	case outcomeAbort:
		// Esc while working. During an automatic-retry countdown upstream
		// swaps in an Escape handler that only cancels the retry delay, so
		// the run itself settles with the failed attempt.
		if m.retryCountdownStop != nil && m.opts.SessionHandle != nil {
			m.opts.SessionHandle.AbortRetry()
			return nil
		}
		// Otherwise upstream's onEscape calls
		// restoreQueuedMessagesToEditor({ abort: true }): queued steering
		// and follow-up messages go back to the editor before the abort, so
		// they cannot leak into the next, unrelated prompt.
		m.restoreQueuedMessagesToEditor(true)
		// Freeze any tool still mid-execution right now, at abort time,
		// instead of waiting for agent_end. A hung tool (e.g. an ssh that
		// ignores the cancelled context) does not return promptly, so its
		// component would stay ToolStateRunning and keep recomputing the
		// live "Elapsed X.Xs" footer on every keystroke and agent chunk -
		// and once it has scrolled above the viewport that forces a full
		// clearing repaint each time (the flicker users saw after pressing
		// Esc). Mirrors upstream, where an aborted tool result is isError
		// so bash.ts renderResult freezes endedAt and clears its interval.
		m.finalizeRunningTools()
		// Upstream does not render a separate abort banner: the
		// assistant message block's SetTerminalError("aborted", ...)
		// handles the visual feedback ("Operation aborted" in error
		// color). Removed the Pig-specific "⚠  aborted by user" text
		// block to match upstream.
		m.tuiInst.Render()

	case outcomeClearEditor:
		// Mirror upstream handleCtrlC (interactive-mode.ts:3262): a second
		// Ctrl+C within 500ms exits; otherwise clear the editor and arm the
		// exit timer. Ctrl+C never aborts a running turn: abort is Esc.
		now := time.Now()
		if ctrlCExits(m.lastSigintTime, now) {
			m.lastSigintTime = time.Time{}
			m.requestShutdown()
			return nil
		}
		m.lastSigintTime = now
		// Upstream clearEditor() just clears the text and re-renders; it
		// shows no "cleared" status (interactive-mode.ts:3629). Pig used
		// to flash "cleared" here, which diverged.
		m.editor.Clear()
		m.tuiInst.Render()

	case outcomeToggleTools:
		m.toggleAllTools()
		m.tuiInst.Render()

	case outcomeExternalEditor:
		m.openExternalEditor(ctx)
		m.tuiInst.Render()

	case outcomePasteImage:
		m.handleClipboardImagePaste()
		m.tuiInst.Render()

	case outcomeCopyMessage:
		// app.message.copy copies the active fullscreen selection before
		// falling back to the last assistant message, as upstream's
		// handleCopyCommand({ flashConfirmation: true, preferSelection: true }).
		m.handleCopyCommand(true, true)
		return nil

	case outcomeModelPicker:
		m.handleModelPicker()
		m.tuiInst.Render()

	case outcomeSuspend:
		m.handleSuspend()

	case outcomeCycleThinking:
		m.cycleThinkingLevel()
		m.tuiInst.Render()
		return nil

	case outcomeToggleThinking:
		m.toggleThinkingVisibility()
		m.tuiInst.Render()
		return nil

	case outcomeCycleModelForward:
		m.cycleModel(true)
		m.tuiInst.Render()
		return nil

	case outcomeCycleModelBackward:
		m.cycleModel(false)
		m.tuiInst.Render()
		return nil

	case outcomeSessionNew:
		// app.session.new: mirrors upstream onAction("app.session.new")
		m.dispatchSlash(ctx, "/new")
		return nil

	case outcomeSessionTree:
		// app.session.tree: mirrors upstream onAction("app.session.tree")
		m.dispatchSlash(ctx, "/tree")
		return nil

	case outcomeSessionFork:
		// app.session.fork: mirrors upstream onAction("app.session.fork")
		m.dispatchSlash(ctx, "/fork")
		return nil

	case outcomeSessionResume:
		// app.session.resume: mirrors upstream onAction("app.session.resume")
		m.dispatchSlash(ctx, "/resume")
		return nil

	case outcomeBracketedPaste:
		// Pass the full bracketed paste to the editor, which handles
		// buffering, normalization, and large-paste marker collapse internally.
		// Mirrors upstream: the editor's HandleInput detects \x1b[200~ and
		// routes through handlePasteFlush which inserts markers for pastes
		// >10 lines or >1000 chars.
		m.editor.HandleInput(data)
		m.tuiInst.Render()
		return nil

	case outcomeSubmit:
		text := strings.TrimSpace(m.editor.GetExpandedText())
		if text == "" {
			return nil
		}
		m.editor.AddToHistory(text)
		m.editor.Clear()
		// handleSubmit is upstream's onSubmit: commands and `!` bash run
		// immediately, input during compaction queues for after it, and
		// anything else goes through prompt() with streamingBehavior "steer"
		// while a run is active.
		m.handleSubmit(ctx, text)

	case outcomeNewline:
		m.editor.HandleInput("\n")

	case outcomeFollowUp:
		// Alt+Enter: if idle, act as regular submit; if working, enqueue
		// as follow-up (delivered after the agent has no more tool calls
		// or steering messages).
		// upstream: interactive-mode.ts:3258-3270
		text := m.editor.GetExpandedText()
		if text == "" {
			break
		}
		m.editor.AddToHistory(text)
		m.editor.SetText("")
		m.editor.ClearPastes()
		// Mirrors upstream handleFollowUp: during compaction only extension
		// commands run and anything else queues for after it; while a run is
		// active, prompt(text, { streamingBehavior: "followUp" }) runs an
		// extension command or queues the text as a follow-up; otherwise
		// Alt+Enter acts like Enter.
		trimmed := strings.TrimSpace(text)
		switch {
		case m.isExtensionCommand(trimmed) && (m.isCompacting || m.runStreaming()):
			m.dispatchSlash(ctx, trimmed)
		case m.isCompacting:
			m.compactionQueue = append(m.compactionQueue, compactionQueuedMessage{text: trimmed, mode: compactionQueueFollowUp})
			m.statusLine.Flash("Queued message for after compaction", 2*time.Second)
			m.updatePendingMessagesDisplay()
		case m.runStreaming():
			m.promptUserInput(ctx, trimmed, nil, true, extension.InputSourceUser, true)
		default:
			m.handleSubmit(ctx, trimmed)
		}
		m.tuiInst.Render()

	case outcomeDequeue:
		// Alt+Up: restore all queued messages to the editor. This must
		// include messages typed during an in-flight compaction, which
		// live in m.compactionQueue rather than the agent's steering/
		// follow-up queues. updatePendingMessagesDisplay already merges
		// them ("Steering:"/"Follow-up:" lines with the Alt+Up hint), so
		// dequeue must clear the same set or the hint restores nothing.
		// Mirrors upstream restoreQueuedMessagesToEditor via clearAllQueues,
		// which combines the session queue with compactionQueuedMessages
		// (interactive-mode.ts:3564-3572, 3794-3807).
		n := m.restoreQueuedMessagesToEditor(false)
		switch n {
		case 0:
			m.statusLine.Flash("No queued messages to restore", 3*time.Second)
		case 1:
			m.statusLine.Flash("Restored 1 queued message to editor", 3*time.Second)
		default:
			m.statusLine.Flash(fmt.Sprintf("Restored %d queued messages to editor", n), 3*time.Second)
		}
		m.tuiInst.Render()

	case outcomeNop:
		// Esc while in bash mode: clear editor and exit bash mode.
		// Mirrors upstream onEscape → isBashMode branch
		// (interactive-mode.ts:2295-2298).
		if action == actionInterrupt && m.editor.IsBashMode() {
			m.editor.SetText("")
			m.tuiInst.Render()
			return nil
		}
		// Idle Esc with empty editor arms / fires the
		// double-Esc shortcut (mirrors upstream
		// interactive-mode.ts:2300-2316). Default action is "tree";
		// reads from doubleEscapeAction setting ("fork"/"tree"/"none").
		// 500ms window matches upstream `now - this.lastEscapeTime < 500`.
		if action == actionInterrupt && editorEmpty {
			dblAction := "tree"
			if m.opts.SettingsManager != nil {
				dblAction = m.opts.SettingsManager.GetDoubleEscapeAction()
			}
			if dblAction != "none" {
				now := time.Now()
				if !m.lastEscapeTime.IsZero() && now.Sub(m.lastEscapeTime) < 500*time.Millisecond {
					m.lastEscapeTime = time.Time{}
					switch dblAction {
					case "fork":
						m.dispatchSlash(ctx, "/fork")
					default: // "tree"
						m.dispatchSlash(ctx, "/tree")
					}
					return nil
				}
				m.lastEscapeTime = now
			}
		}
		// Intentional no-op (idle Ctrl+C empty, working Ctrl+G/V/L/Submit).
		return nil

	default:
		m.editor.HandleInput(data)
	}
	m.tuiInst.RequestRender()
	return nil
}

// expandSkillCommand checks if prompt starts with "/skill:name" and if so,
// expands it to the skill's XML block. Returns (expanded, true) on match.
// Delegates to ExpandSkillCommand with the session's loaded skills.
// Mirrors upstream _expandSkillCommand (agent-session.ts:1124-1151).
func (m *InteractiveMode) expandSkillCommand(prompt string) (string, bool) {
	if len(m.opts.Skills) == 0 {
		return "", false
	}
	return ExpandSkillCommand(prompt, m.opts.Skills)
}

// syncExtensionSlashCommands makes the slash registry's extension commands
// exactly the inproc runner's commands, so Resolve sees them. Like upstream,
// which rebuilds commands from the loaded extensions, a command of an
// extension that is no longer loaded is removed. Idempotent.
func (m *InteractiveMode) syncExtensionSlashCommands() {
	if m.newRunner == nil {
		m.slashRegistry.ReplaceDynamic(nil)
		return
	}
	commands := m.newRunner.Commands()
	dynamic := make([]SlashCommand, 0, len(commands))
	for _, rc := range commands {
		handler := rc.Handler
		nr := m.newRunner
		cmdName := strings.TrimPrefix(rc.InvocationName, "/")
		dynamic = append(dynamic, SlashCommand{
			Name:        cmdName,
			Description: rc.Description,
			Handler: func(_ *ExtensionContext, args string) error {
				if handler == nil {
					return nil
				}
				cmdCtx := nr.CreateCommandContext()
				baseCtx := m.runCtx
				if baseCtx == nil {
					baseCtx = context.Background()
				}
				newCtx := extension.WithContext(baseCtx, cmdCtx.Context)
				newCtx = extension.WithCommandContext(newCtx, cmdCtx)
				go func() {
					if err := handler(newCtx, args); err != nil {
						m.runOnMain(baseCtx, func() {
							m.appendChatBlock(tui.NewText("\033[31mError: " + err.Error() + "\033[0m"))
							m.tuiInst.Render()
						})
					}
				}()
				return nil
			},
		})
	}
	m.slashRegistry.ReplaceDynamic(dynamic)
}

// resolvableSlashCommand reports whether prompt is a slash command that
// resolves to a registered builtin or extension command. These are local/UI
// dispatches that run immediately even during compaction, mirroring upstream
// where the builtin command if-chain and the isExtensionCommand branch both
// precede the isCompacting queue. Prompt templates, skill commands, and
// unresolved slashes are not resolvable here: they expand into model prompts
// and stay queued during compaction.
func (m *InteractiveMode) resolvableSlashCommand(prompt string) bool {
	if !strings.HasPrefix(prompt, "/") {
		return false
	}
	name, _ := parseSlashLine(prompt)
	m.syncExtensionSlashCommands()
	_, ok := m.slashRegistry.Resolve(name)
	return ok
}

// restoreQueuedMessagesToEditor moves every queued message (the agent's
// steering and follow-up queues plus messages queued during compaction) into
// the editor ahead of its current text, and reports how many it restored.
// With abort it then aborts the active run. Mirrors upstream
// restoreQueuedMessagesToEditor, whose clearAllQueues combines the session
// queue with compactionQueuedMessages.
func (m *InteractiveMode) restoreQueuedMessagesToEditor(abort bool) int {
	var steering, followUps []agent.AgentMessage
	if m.agent != nil {
		steering, followUps = m.agent.PendingMessages()
	}
	steeringTexts, followUpTexts := collectQueuedTexts(steering, followUps, m.compactionQueue)
	texts := slices.Concat(steeringTexts, followUpTexts)
	if len(texts) > 0 {
		if m.agent != nil {
			m.agent.ClearAllQueues()
		}
		m.compactionQueue = nil
		combined := strings.Join(texts, "\n\n")
		if current := m.editor.Text(); strings.TrimSpace(current) != "" {
			combined += "\n\n" + current
		}
		m.editor.SetText(combined)
	}
	m.updatePendingMessagesDisplay()
	if abort {
		m.abortRun(m.runCtx)
	}
	return len(texts)
}

// abortRun cancels the active run, its retry delay and compaction included,
// and arms a fresh abort context for the next run.
func (m *InteractiveMode) abortRun(ctx context.Context) {
	m.abortFn()
	if ctx == nil {
		ctx = context.Background()
	}
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
}

// settleActiveRun aborts the active run and waits until it settles, as
// upstream's session replacement (teardownCurrent) awaits session.abort()
// before it touches the session, so the aborted turn persists to the
// outgoing session rather than the one replacing it. It runs on the owner
// loop, so while it waits it keeps handling Session events and posted UI
// tasks, which the settling run needs. It fails only when the mode shuts
// down first; the caller must then not replace the session.
func (m *InteractiveMode) settleActiveRun() error {
	m.queueMu.Lock()
	settled := m.turnSettled
	m.queueMu.Unlock()
	if settled == nil {
		return nil
	}
	m.abortRun(m.runCtx)
	done := context.Background().Done()
	if m.runCtx != nil {
		done = m.runCtx.Done()
	}
	for {
		select {
		case <-settled:
			return nil
		case <-done:
			return errors.New("interactive mode shut down before the active run settled")
		case ev, ok := <-m.eventCh:
			if !ok {
				m.eventCh = nil
				continue
			}
			m.handleAgentEvent(ev)
		case fn := <-m.uiTaskCh:
			fn()
		case <-m.renderWakeCh:
			m.runScheduledRender()
		}
	}
}

// runStreaming reports whether a run is active (upstream isStreaming).
func (m *InteractiveMode) runStreaming() bool {
	return m.turnActive.Load() || (m.agent != nil && m.agent.IsStreaming())
}

// isExtensionCommand reports whether text invokes an extension-registered
// command. Mirrors upstream InteractiveMode.isExtensionCommand.
func (m *InteractiveMode) isExtensionCommand(text string) bool {
	if !strings.HasPrefix(text, "/") || m.newRunner == nil {
		return false
	}
	name, _ := parseSlashLine(text)
	for _, command := range m.newRunner.Commands() {
		if strings.TrimPrefix(command.InvocationName, "/") == name {
			return true
		}
	}
	return false
}

func (m *InteractiveMode) hasActiveAgentTurn() bool {
	// A turn is active from the synchronous commit in runPromptTurn until the
	// run goroutine returns, not merely while the provider streams tokens.
	// Upstream derives both isStreaming and isIdle from one _isAgentRunActive
	// flag set synchronously in prompt() (agent-session.ts:874). pig's
	// Agent.streaming flips true only once the goroutine reaches runLoop, so a
	// submit in the window before that (goroutine dispatch, before_agent_start
	// hook, pre-prompt auto-compaction) saw IsStreaming()==false and started a
	// second concurrent turn: persisting back-to-back assistant messages that
	// break tool_use/tool_result pairing ("tool_use_id ... has no corresponding
	// tool_use"). turnActive marks exactly the goroutine's lifetime, so it stays
	// true across that window yet reads false once the turn ends (unlike the
	// runOnMain-reset isIdle, which can be stale-false: see
	// TestPendingDisplay_EnterAfterAgentStoppedStartsNewTurn).
	if m.turnActive.Load() || m.isCompacting {
		return true
	}
	if m.agent == nil {
		return false
	}
	return m.agent.IsStreaming()
}

// handleSubmit fires before_agent_start, then starts the agent in a goroutine.

// syncEditorFocusWithSearch mirrors upstream setFocus toggling the editor's
// Focusable flag: while fullscreen transcript search holds focus the editor
// emits no hardware-cursor marker, so the cursor lands in the search box.
func (m *InteractiveMode) syncEditorFocusWithSearch() {
	if m.editor != nil && m.altScreen != nil {
		m.editor.Focused = !m.altScreen.IsSearchFocused()
	}
}
