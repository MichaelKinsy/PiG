package codingagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// exitIfDeadTerminal bypasses terminal restoration on a disconnected terminal. A PTY master close can surface as EOF from the input reader; querying the terminal then reports the dead-device error without writing restore sequences.
// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (emergencyTerminalExit).
//
// Pi's extensions run inside the exiting process and die with it; pig's run as child processes in their own process groups, so they are killed here or they would outlive the exit and keep writing into the session's directories.
func (m *InteractiveMode) exitIfDeadTerminal(err error) {
	if errors.Is(err, io.EOF) {
		_, _, err = term.GetSize(int(os.Stdout.Fd()))
	}
	if errors.Is(err, syscall.EIO) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ENOTCONN) {
		if m.opts.TerminateExtensionProcesses != nil {
			m.opts.TerminateExtensionProcesses()
		}
		os.Exit(129)
	}
}

// inputLoop reads terminal input from source and dispatches to the editor or
// agent.
func (m *InteractiveMode) inputLoop(ctx context.Context, source io.Reader) error {
	return m.inputLoopUntil(ctx, source, nil)
}

func (m *InteractiveMode) inputLoopUntil(ctx context.Context, source io.Reader, until <-chan struct{}) (resultErr error) {
	m.startTerminalInput(ctx, source)
	if until == nil {
		defer func() {
			if err := m.stopTerminalInput(); err != nil {
				resultErr = errors.Join(resultErr, err)
			}
		}()
	}
	readCh, errCh := m.inputReadCh, m.inputErrCh
	var userInput <-chan string
	if until == nil {
		defer func() {
			m.onInputCallback = nil
			m.pendingUserInputs = nil
		}()
	}

	// dispatchInput handles one routed sequence and reports whether to await its terminal-read completion before painting.
	dispatchInput := func(input inputChunk) (bool, error) {
		defer input.ticket.settle()
		if chunk := string(input.data); chunk != "" {
			if os.Getenv("PIG_DEBUG_KEYS") != "" {
				debugLog("key %q -> %d", chunk, classifyKeyWithBindings(chunk, m.keybindings))
			}
			if err := m.dispatchInputChunk(ctx, chunk, input.ticket); err != nil {
				return false, err
			}
		}
		input.ticket.settle()
		// A chunk still waiting on a remote listener's verdict holds the
		// pump, and a modal takes the next sequence itself, so neither
		// continues the read here.
		if input.readDone == nil || !input.ticket.settled() || m.inputLoopErr != nil || m.requestExit.Load() {
			return false, nil
		}
		modalCh, _, _ := m.modalRouteWatch()
		return modalCh == nil, nil
	}

	// dispatchRead dispatches input and the rest of its terminal read, then
	// paints once. Upstream's StdinBuffer emits every sequence of one read
	// synchronously, so no posted UI task (an async autocomplete result),
	// agent event or paint runs between them, and requestImmediateRender
	// coalesces the paint to one frame after the read. Handling each sequence
	// as its own loop turn painted every prefix of a typed "/compact" with the
	// popup its stale suggestions opened, which grew the transcript and left
	// the main screen scrolled further than upstream.
	dispatchRead := func(input inputChunk) (err error) {
		dispatched := false
		// Match upstream's immediate keyboard paint before asynchronous
		// autocomplete results request their throttled follow-up frame.
		defer func() {
			if dispatched && err == nil {
				m.tuiInst.Render()
			}
		}()
		for {
			dispatched = dispatched || len(input.data) > 0
			continues, dispatchErr := dispatchInput(input)
			if dispatchErr != nil || !continues {
				return dispatchErr
			}
			select {
			case <-input.readDone:
				return nil
			default:
			}
			_, _, changed := m.modalRouteWatch()
			select {
			case <-input.readDone:
				return nil
			case <-ctx.Done():
				return nil
			case readErr := <-errCh:
				// Paint what the read delivered before the loop ends.
				m.tuiInst.Render()
				return readErr
			case <-changed:
				// A modal armed off the loop and takes the rest of the read.
				return nil
			case next, ok := <-readCh:
				if !ok {
					readCh = nil
					return nil
				}
				input = next
			}
		}
	}

	for {
		if ctx.Err() != nil {
			m.ShutdownFromSignal()
			return nil
		}
		select {
		case <-until:
			return nil
		default:
		}
		if err := m.inputLoopErr; err != nil {
			return err
		}
		if m.requestExit.Load() {
			return m.finishInteractiveShutdown()
		}
		if until == nil && userInput == nil && m.initialMessagesDone == nil && !m.hasActiveAgentTurn() {
			userInput = m.getUserInput()
		}
		// Pi resumes the resolved getUserInput promise after this read's synchronous sequences, before another terminal-read event.
		select {
		case text := <-userInput:
			userInput = nil
			m.handleSubmit(ctx, text)
			continue
		default:
		}
		// Prioritize terminal input over agent/render events. During tool output or
		// streaming, eventCh/uiTaskCh can stay hot; without this pre-check a waiting
		// keystroke can sit behind repeated render work.
		switch kind, buf := priorityInput(readCh, nil); kind {
		case priorityInputClosed:
			readCh = nil
			continue
		case priorityInputRead:
			if err := dispatchRead(buf); err != nil {
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
		case <-m.initialMessagesDone:
			m.initialMessagesDone = nil
		case text := <-userInput:
			userInput = nil
			m.handleSubmit(ctx, text)
		case err := <-errCh:
			return err
		case <-until:
			return nil
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
			if err := dispatchRead(buf); err != nil {
				return err
			}
		}
	}
}

func (m *InteractiveMode) finishInteractiveShutdown() error {
	m.shutdownMu.Lock()
	defer m.shutdownMu.Unlock()
	fromSignal := m.signalShutdownDone.Swap(true)
	inputErr := m.stopTerminalInput()
	m.remoteEditorEvents.drain()
	m.stopInteractiveTui()
	if err := m.requestedExitError(); err != nil {
		if inputErr != nil {
			return errors.Join(inputErr, err)
		}
		return err
	}
	if !fromSignal {
		m.emitQuitShutdown()
		m.printResumeHint()
	}
	return inputErr
}

type terminalInputOwner struct {
	cancel context.CancelFunc
	done   chan struct{}
	reader *interactiveTerminalReader
	err    error
}

// startTerminalInput starts one owned decoder shared by theme detection and the interactive loop. Temporary UI loops keep that owner; final shutdown cancels and joins it before terminal teardown.
func (m *InteractiveMode) startTerminalInput(ctx context.Context, source io.Reader) {
	if m.inputReadCh != nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	owner := &terminalInputOwner{cancel: cancel, done: make(chan struct{})}
	if file, ok := source.(*os.File); ok {
		owner.reader = newInteractiveTerminalReader(ctx, file)
		m.inputReader = owner.reader
	}
	m.inputOwner = owner
	m.inputReadCh = make(chan inputChunk)
	m.inputErrCh = make(chan error, 1)
	go func() {
		defer close(owner.done)
		owner.err = m.pumpTerminalInput(ctx, source, m.inputReadCh, m.inputErrCh)
	}()
}

func (m *InteractiveMode) stopTerminalInput() error {
	owner := m.inputOwner
	if owner == nil {
		return nil
	}
	owner.cancel()
	if owner.reader != nil {
		owner.reader.pause()
	}
	<-owner.done
	m.inputOwner = nil
	return owner.err
}

// normalizeInputSequence applies upstream ProcessTerminal.forwardInputSequence's
// native Shift+Enter normalization to one complete StdinBuffer sequence.
var normalizeInputSequence = tui.NormalizeProcessInputSequence

// pumpTerminalInput owns the one StdinBuffer for the process input stream and
// routes only complete sequences. Upstream ProcessTerminal parses input before
// focus dispatch, so switching between the editor and a modal cannot split one
// terminal read differently or lose a partial escape sequence.
//
// Upstream's terminal-input listeners answer synchronously before anything else sees a chunk. A chunk routed to the main loop therefore holds all input after it until its listeners settle, including a subprocess listener whose verdict the main loop awaits without blocking. Ctrl+C follows the same ordering as every other key. While a verdict is pending the pump stops receiving raw input, leaving at most one read ahead in the reader worker.
func (m *InteractiveMode) pumpTerminalInput(ctx context.Context, source io.Reader, readCh chan<- inputChunk, errCh chan<- error) (cleanupErr error) {
	defer close(readCh)
	rawCh, rawErrCh, closeRaw := m.rawInputChannels(ctx, source)
	defer func() { cleanupErr = closeRaw() }()

	var input *tui.TerminalInput
	var readDone chan struct{}
	deliver := func(sequence string) {
		ticket := m.routeInputSequence(ctx, []byte(sequence), readDone, readCh)
		if ticket != nil {
			select {
			case <-ctx.Done():
				input.Close()
			case <-ticket.done:
			}
		}
	}
	input = tui.NewTerminalInput(func(sequence string) { deliver(normalizeInputSequence(sequence)) })
	defer input.Close()
	batch := func(run func()) {
		readDone = make(chan struct{})
		defer close(readDone)
		run()
	}
	process := func(buf []byte) { batch(func() { input.Process(buf) }) }
	flush := func() { batch(input.Flush) }
	// Startup type-ahead was already framed and normalized by its terminal owner.
	batch(func() {
		for _, sequence := range takeStartupInput() {
			deliver(sequence)
		}
	})
	for {
		switch kind, buf := priorityInput(rawCh, input.C); kind {
		case priorityInputRead:
			process(buf)
			continue
		case priorityInputFlush:
			flush()
			continue
		case priorityInputClosed, priorityInputNone:
		}
		select {
		case <-ctx.Done():
			return
		case err := <-rawErrCh:
			m.exitIfDeadTerminal(err)
			batch(input.FlushPending)
			select {
			case errCh <- err:
			case <-ctx.Done():
			}
			return
		case buf := <-rawCh:
			process(buf)
		case <-input.C:
			flush()
		}
	}
}

// rawInputChannels retains the existing byte-read contract and joins its worker. Files remain open; cancellation closes other ReadClosers. Non-closable readers must finish each Read without waiting for external input.
func (m *InteractiveMode) rawInputChannels(ctx context.Context, source io.Reader) (<-chan []byte, <-chan error, func() error) {
	if m.inputReader != nil {
		return m.inputReader.data, m.inputReader.errors, func() error { return nil }
	}
	if file, ok := source.(*os.File); ok {
		reader := newInteractiveTerminalReader(ctx, file)
		return reader.data, reader.errors, func() error { reader.pause(); return nil }
	}
	ctx, cancel := context.WithCancel(ctx)
	data, readErrors := make(chan []byte), make(chan error, 1)
	done := make(chan struct{})
	var closeErr error
	var stopClose func() bool
	closeDone := make(chan struct{})
	if closer, ok := source.(io.ReadCloser); ok {
		stopClose = context.AfterFunc(ctx, func() {
			closeErr = closer.Close()
			close(closeDone)
		})
	}
	go func() {
		defer close(done)
		disarmClose := func() {
			if stopClose != nil {
				stop := stopClose
				stopClose = nil
				if !stop() {
					<-closeDone
				}
			}
		}
		defer disarmClose()
		for ctx.Err() == nil {
			buf, err := tui.ReadInputChunk(source)
			if len(buf) != 0 {
				select {
				case data <- buf:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				disarmClose()
				readErrors <- err
				return
			}
		}
	}()
	return data, readErrors, func() error { cancel(); <-done; return closeErr }
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
	if m.consumeTerminalThemeInput(data) {
		return nil
	}
	if pending := m.autocompletePending; pending != nil {
		ticket.await()
		m.backgroundTasks.Go(func() {
			select {
			case <-pending:
			case <-ctx.Done():
				ticket.resume()
				ticket.settle()
				return
			}
			if err := m.postToMain(ctx, func() {
				ticket.resume()
				defer ticket.settle()
				m.failInputLoop(m.dispatchInputChunk(ctx, data, ticket))
			}); err != nil {
				ticket.resume()
				ticket.settle()
			}
		})
		return nil
	}
	if m.externalEditorActive {
		ticket.await()
		m.externalEditorInput = func() {
			ticket.resume()
			defer ticket.settle()
			m.failInputLoop(m.dispatchInputChunk(ctx, data, ticket))
		}
		return nil
	}
	// In fullscreen mode, viewport input (mouse wheel/click, focus events) is
	// handled by the alt-screen renderer and must not reach the editor. Mirrors
	// upstream's addInputListener(handleViewportInput) on the alt-screen; pig is
	// driver-owned, so the driver routes it explicitly.
	if m.altScreen != nil {
		consumed := m.altScreen.HandleViewportInput(data)
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
	return m.passTerminalInput(ctx, data, ticket, func(ctx context.Context, data string) error {
		previous := m.currentInputTicket
		m.currentInputTicket = ticket
		defer func() { m.currentInputTicket = previous }()
		return m.handleKey(ctx, data)
	})
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

	// Refresh overlay visibility and eligible focus restoration before choosing the input target. An active replacement keeps input until it changes focus. The application editor uses the driver-owned action routing below; other focused components receive input directly.
	if m.tuiInst != nil {
		focused := m.tuiInst.ActiveOverlay()
		if focused == nil {
			focused = m.tuiInst.FocusedComponent()
		}
		if focused != nil && focused != m.editor {
			if tui.ShouldDeliverKey(focused, data) {
				if input, ok := focused.(tui.InputHandler); ok {
					input.HandleInput(data)
					m.tuiInst.RequestImmediateRender()
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

	// An extension's editor component (ctx.ui.setEditorComponent) is Pi's
	// focused editor: every key goes to its handleInput, and Pi's CustomEditor
	// there runs the app actions through the handlers the host bound to it
	// (remote_editor.go).
	if m.editor.IsRemote() {
		m.editor.HandleInput(data)
		return nil
	}

	return m.handleEditorAction(ctx, classifyKeyWithBindings(data, m.keybindings), data)
}

// handleEditorAction runs what Pi's editor handlers do for one classified
// key: the app actions bound on the default editor (onEscape, onCtrlD,
// onAction, onPasteImage), submit, and editing. data is the key, delivered to
// the editor for the editing actions.
func (m *InteractiveMode) handleEditorAction(ctx context.Context, action keyAction, data string) error {
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
			m.editor.AcceptAutocomplete(func(submit bool) {
				if submit {
					text := widthx.JSTrim(m.editor.Text())
					if text != "" {
						m.editor.Clear()
						if m.editor.OnSubmit != nil {
							m.editor.OnSubmit(text)
						} else {
							m.handleSubmit(ctx, text)
						}
					}
				}
			})
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
		return m.handleSuspend()

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
		text := widthx.JSTrim(m.editor.GetExpandedText())
		if text == "" {
			return nil
		}
		if m.editor.OnSubmit != nil {
			m.editor.Clear()
			m.editor.OnSubmit(text)
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
		text := widthx.JSTrim(m.editor.GetExpandedText())
		if text == "" {
			break
		}
		if !m.isCompacting && !m.runStreaming() && m.editor.OnSubmit != nil {
			m.editor.Clear()
			m.editor.OnSubmit(text)
			m.tuiInst.Render()
			return nil
		}
		m.editor.AddToHistory(text)
		m.editor.SetText("")
		m.editor.ClearPastes()
		// Mirrors upstream handleFollowUp: during compaction only extension
		// commands run and anything else queues for after it; while a run is
		// active, prompt(text, { streamingBehavior: "followUp" }) runs an
		// extension command or queues the text as a follow-up; otherwise
		// Alt+Enter acts like Enter.
		trimmed := widthx.JSTrim(text)
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
	expanded, ok, err := ExpandSkillCommand(prompt, m.opts.Skills)
	if err != nil && m.newRunner != nil {
		m.newRunner.EmitError(err)
	}
	return expanded, ok
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
		nr := m.newRunner
		cmdName := strings.TrimPrefix(rc.InvocationName, "/")
		runsLlama := m.opts.Llama != nil && IsLlamaCommand(rc)
		dynamic = append(dynamic, SlashCommand{
			Name:        cmdName,
			Description: rc.Description,
			Handler: func(_ *ExtensionContext, args string) error {
				baseCtx := m.runCtx
				if baseCtx == nil {
					baseCtx = context.Background()
				}
				// The built-in llama.cpp extension's /llama runs the llama host with this mode's command context.
				if runsLlama {
					return m.runLlamaCommand(baseCtx)
				}
				// Use the runner's command context and error channel, as Session command dispatch does. IPC stays off the input loop.
				go nr.ExecuteCommand(baseCtx, cmdName, args)
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
		if current := m.editor.Text(); widthx.JSTrim(current) != "" {
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

// abortRun requests the Session's owned abort without cancelling its caller lifetime. Raw-agent modes cancel and renew their local run context.
func (m *InteractiveMode) abortRun(ctx context.Context) {
	if session, ok := m.opts.SessionHandle.(interface{ RequestAbort() }); ok {
		session.RequestAbort()
		return
	}
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
	session := m.opts.SessionHandle
	m.queueMu.Unlock()
	if settled != nil || session != nil && !session.IsIdle() {
		m.abortRun(m.runCtx)
	}
	if err := m.settleUserBash(); err != nil {
		return err
	}
	if settled == nil && (session == nil || session.IsIdle()) {
		return nil
	}
	owner := m.runCtx
	if owner == nil {
		owner = context.Background()
	}
	ctx, cancel := context.WithCancel(owner)
	var workers sync.WaitGroup
	idle := make(chan error, 1)
	workers.Go(func() { idle <- waitForSessionIdle(ctx, settled, session) })
	defer func() { cancel(); workers.Wait() }()
	for {
		select {
		case err := <-idle:
			if ctx.Err() != nil {
				return errors.New("interactive mode shut down before the active run settled")
			}
			return err
		case <-ctx.Done():
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
