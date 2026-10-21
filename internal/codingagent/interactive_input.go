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
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// exitIfDeadTerminal bypasses terminal restoration on a disconnected terminal. A PTY master close can surface as EOF from the input reader; querying the terminal then reports the dead-device error without writing restore sequences.
// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (emergencyTerminalExit).
func (m *InteractiveMode) exitIfDeadTerminal(err error) {
	cause := err
	if errors.Is(err, io.EOF) {
		// The reader turns a read that fails with EIO on a hung-up terminal into EOF. When the output terminal is hung up
		// too it is gone, although it can still answer TIOCGWINSZ while the hang-up completes (strace on a CI-like loaded
		// host: read(0)=EIO, POLLHUP, ioctl(1,TIOCGWINSZ)=0): the size probe below saw a live terminal, the session kept
		// running without input, and the parity probe timed out (10-terminal-disconnect-exit). Pi exits 129 on that
		// terminal's EIO. Input that ended while output still goes to a live terminal keeps the session running (#165).
		// pig divergence (D186): Pi exits 0 at the hangup when its event loop holds nothing; PiG always exits 129 and records it in exit.log.
		if tui.OutputTerminalHungUp() {
			m.emergencyTerminalExit("terminal gone: " + cause.Error() + " (hung up)")
			return
		}
		// ENOTTY here only says stdout is not a terminal, as under a pipe or a test, not that the terminal went away.
		if _, _, err = term.GetSize(int(os.Stdout.Fd())); errors.Is(err, syscall.ENOTTY) {
			return
		}
	}
	if isDeadTerminalError(err) {
		m.emergencyTerminalExit("terminal gone: " + cause.Error() + " (" + err.Error() + ")")
	}
}

// emergencyTerminalExit exits without terminal restoration: the terminal is gone, and restore writes would fail again.
// It kills the tracked detached children first, as upstream does, so a running shell command does not outlive the exit
// in its own process group. Pi's extensions run inside the exiting process and die with it; pig's run as child
// processes in their own process groups, so it kills them too, or they would keep writing into the session's
// directories.
// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (emergencyTerminalExit).
func (m *InteractiveMode) emergencyTerminalExit(reason string) {
	// pig additive (D102): Pi exits here with no trace.
	RecordExit(m.opts.AgentDir, reason+" (exit 129)")
	tools.KillTrackedDetachedChildren()
	if m.opts.TerminateExtensionProcesses != nil {
		m.opts.TerminateExtensionProcesses()
	}
	EndSessionMarker()
	os.Exit(129)
}

// isDeadTerminalError reports whether err says the terminal is gone. EIO is a tty read or ioctl from an orphaned
// background process group, or a write after hangup. ENOTTY is a revoked tty (macOS) that stdin no longer reports as
// a terminal.
// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (isDeadTerminalError).
func isDeadTerminalError(err error) bool {
	return errors.Is(err, syscall.EIO) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ENOTCONN) || errors.Is(err, syscall.ENOTTY)
}

// inputLoop reads terminal input from source and dispatches to the editor or
// agent.
func (m *InteractiveMode) inputLoop(ctx context.Context, source io.Reader) error {
	return m.inputLoopUntil(ctx, source, nil)
}

func (m *InteractiveMode) inputLoopUntil(ctx context.Context, source io.Reader, until <-chan struct{}) (resultErr error) {
	defer m.enterOwnerLoop()()
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

	dispatchRead := func(input inputChunk) error {
		return m.dispatchTerminalRead(ctx, input, &readCh, errCh)
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

// dispatchRoutedInput handles one routed sequence. To await the rest of its terminal read before painting, it returns the
// route-change signal of the route snapshot that found no modal: a modal that arms off the loop after that snapshot takes
// the rest of the read, and the signal ends the wait. A nil signal ends the read here.
func (m *InteractiveMode) dispatchRoutedInput(ctx context.Context, input inputChunk) (<-chan struct{}, error) {
	defer input.ticket.settle()
	if chunk := string(input.data); chunk != "" {
		if os.Getenv("PIG_DEBUG_KEYS") != "" {
			debugLog("key %s -> %d", redactKeyChunk(chunk), classifyKeyWithBindings(chunk, m.keybindings))
		}
		if err := m.dispatchInputChunk(ctx, chunk, input.ticket); err != nil {
			return nil, err
		}
	}
	input.ticket.settle()
	// A chunk still waiting on a remote listener's verdict holds the
	// router, and a modal takes the next sequence itself, so neither
	// continues the read here.
	if !input.more || !input.ticket.settled() || m.inputLoopErr != nil || m.requestExit.Load() {
		return nil, nil
	}
	if modalCh, _, changed := m.modalRouteWatch(); modalCh == nil {
		return changed, nil
	}
	return nil, nil
}

// dispatchTerminalRead dispatches input and the rest of its terminal read from
// *readCh, then paints once; a closed *readCh becomes nil. Upstream's
// StdinBuffer emits every sequence of one read synchronously, so no posted UI
// task (an async autocomplete result), agent event or paint runs between them,
// and requestImmediateRender coalesces the paint to one frame after the read.
// Handling each sequence as its own loop turn painted every prefix of a typed
// "/compact" with the popup its stale suggestions opened, which grew the
// transcript and left the main screen scrolled further than upstream.
func (m *InteractiveMode) dispatchTerminalRead(ctx context.Context, input inputChunk, readCh *chan inputChunk, errCh <-chan error) (err error) {
	dispatched := false
	// Match upstream's immediate keyboard paint before asynchronous
	// autocomplete results request their throttled follow-up frame.
	defer func() {
		if dispatched && err == nil {
			m.tuiInst.Render()
		}
	}()
	for {
		m.frontendTookInput = false
		changed, dispatchErr := m.dispatchRoutedInput(ctx, input)
		// pig additive (D91): a sequence a frontend session took, such as
		// a protocol acknowledgement, paints nothing; the actions it asks
		// for request their own frames. Painting after each one would
		// render once per acknowledged frame, and an animation then runs
		// at the frontend's frame rate instead of its own.
		dispatched = dispatched || (len(input.data) > 0 && !m.frontendTookInput)
		if dispatchErr != nil || changed == nil {
			return dispatchErr
		}
		select {
		case <-ctx.Done():
			return nil
		case readErr := <-errCh:
			// Paint what the read delivered before the loop ends.
			m.tuiInst.Render()
			return readErr
		case <-changed:
			// A modal armed off the loop and takes the rest of the read.
			return nil
		case next, ok := <-*readCh:
			if !ok {
				*readCh = nil
				return nil
			}
			input = next
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
	// reader reads a terminal source and routes on its own goroutine; done
	// closes when the input pump of any other source ends.
	reader *interactiveTerminalReader
	router *terminalInputRouter
	done   chan struct{}
	err    error
}

// startTerminalInput starts one owned decoder shared by theme detection and the interactive loop. Temporary UI loops keep that owner; final shutdown cancels and joins it before terminal teardown. A terminal is framed and routed on the goroutine that reads it; another source (a test's reader) on an input pump fed by a read worker.
func (m *InteractiveMode) startTerminalInput(ctx context.Context, source io.Reader) {
	if m.inputReadCh != nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	owner := &terminalInputOwner{cancel: cancel}
	m.inputOwner = owner
	m.inputReadCh = make(chan inputChunk)
	m.inputErrCh = make(chan error, 1)
	if file, ok := source.(*os.File); ok {
		owner.router = m.newTerminalInputRouter(ctx, m.inputReadCh, m.inputErrCh, true)
		owner.reader = newInteractiveTerminalReader(ctx, file, owner.router)
		m.inputReader = owner.reader
		return
	}
	owner.done = make(chan struct{})
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
		owner.reader.stop()
		owner.router.end()
	} else {
		<-owner.done
	}
	m.inputOwner = nil
	return owner.err
}

// normalizeInputSequence applies upstream ProcessTerminal.forwardInputSequence's
// native Shift+Enter normalization to one complete StdinBuffer sequence.
var normalizeInputSequence = tui.NormalizeProcessInputSequence

// terminalInputRouter owns the one StdinBuffer for the process input stream and
// routes only complete sequences, on the goroutine that reads the input.
// Upstream ProcessTerminal parses input before focus dispatch, so switching
// between the editor and a modal cannot split one terminal read differently or
// lose a partial escape sequence.
//
// Upstream's terminal-input listeners answer synchronously before anything else sees a chunk. A chunk routed to the main loop therefore holds all input after it until its listeners settle, including a subprocess listener whose verdict the main loop awaits without blocking. Ctrl+C follows the same ordering as every other key. The router waits for that settlement only when it has more to route: the last chunk of a read goes out at once, its listeners settle while the reader waits for the next read, and the router takes their token before it frames that read. A key typed on its own thus reaches the main loop in one hand-off, and while a verdict is pending the reader holds at most one read ahead.
type terminalInputRouter struct {
	m      *InteractiveMode
	ctx    context.Context
	readCh chan<- inputChunk
	errCh  chan<- error
	// terminal: the source is the process terminal, whose end leaves the session running.
	terminal bool
	input    *tui.TerminalInput
	// settled takes the token of every ticket the router routes (inputTicket.done).
	settled chan struct{}
	// waiting: the token of the chunk routed last is outstanding.
	waiting bool
	// held: sequences the decoder forwarded that are not routed yet.
	held []string
	// split: the current read routed a chunk with more set, so its end must reach the main loop.
	split     bool
	started   bool
	closeOnce sync.Once
}

func (m *InteractiveMode) newTerminalInputRouter(ctx context.Context, readCh chan<- inputChunk, errCh chan<- error, terminal bool) *terminalInputRouter {
	r := &terminalInputRouter{m: m, ctx: ctx, readCh: readCh, errCh: errCh, terminal: terminal, settled: make(chan struct{}, 1)}
	r.input = tui.NewTerminalInput(func(sequence string) { r.held = append(r.held, normalizeInputSequence(sequence)) })
	r.input.SetSequenceBarrier(r.routeHeld)
	return r
}

// begin routes the startup type-ahead once; its terminal owner already framed and normalized it.
func (r *terminalInputRouter) begin() {
	if r.started {
		return
	}
	r.started = true
	r.held = append(r.held, takeStartupInput()...)
	r.finish()
}

// process frames one terminal read and routes its sequences.
func (r *terminalInputRouter) process(data []byte) {
	if r.settle() {
		r.input.Process(data)
		r.finish()
	}
}

// flush dispatches the decoder's expired framing and negotiation deadlines.
func (r *terminalInputRouter) flush() {
	if r.settle() {
		r.input.Flush()
		r.finish()
	}
}

// deadline reports when flush must run if no input arrives first.
func (r *terminalInputRouter) deadline() time.Time {
	return r.input.NextDeadline()
}

// fail ends the input at a read error: the decoder's held input first, then the error once its listeners settled.
func (r *terminalInputRouter) fail(err error) {
	defer r.end()
	r.m.exitIfDeadTerminal(err)
	if !r.settle() {
		return
	}
	r.input.FlushPending()
	r.finish()
	// Ports packages/tui/src/terminal.ts: process.stdin gets a "data" listener and no "end" listener, so a terminal
	// whose input ends while it lives leaves the session running without input.
	if r.terminal && errors.Is(err, io.EOF) {
		debugLog("terminal input ended (EOF); the session keeps running without input")
		return
	}
	if !r.settle() {
		return
	}
	select {
	case r.errCh <- err:
	case <-r.ctx.Done():
	}
}

// end closes the main loop's input channel once.
func (r *terminalInputRouter) end() {
	r.closeOnce.Do(func() { close(r.readCh) })
}

// settle waits for the listeners of the chunk routed last and reports whether input continues. Cancellation discards
// the decoder's rest.
func (r *terminalInputRouter) settle() bool {
	if r.waiting {
		select {
		case <-r.settled:
			r.waiting = false
		case <-r.ctx.Done():
		}
	}
	if r.ctx.Err() != nil {
		r.input.Close()
		r.held = r.held[:0]
		return false
	}
	return true
}

// route sends one sequence after the previous chunk settled and reports whether input continues.
func (r *terminalInputRouter) route(sequence string, more bool) bool {
	if !r.settle() {
		return false
	}
	if r.m.routeInputSequence(r.ctx, []byte(sequence), more, r.settled, r.readCh) != nil {
		r.waiting = true
		r.split = r.split || more
	}
	return true
}

// routeHeld is the decoder's sequence barrier: what the decoder forwarded reaches its listeners, which settle, before the
// decoder interprets a later sequence of the read.
func (r *terminalInputRouter) routeHeld() {
	for _, sequence := range r.held {
		if !r.route(sequence, true) {
			return
		}
	}
	r.held = r.held[:0]
	r.settle()
}

// finish routes the rest of a decoder step: its last chunk without waiting for the listeners, or, when the read ended in
// protocol input after a chunk that announced more, the read's end.
func (r *terminalInputRouter) finish() {
	last := len(r.held) - 1
	for i, sequence := range r.held {
		if !r.route(sequence, i < last) {
			return
		}
	}
	r.held = r.held[:0]
	if last < 0 && r.split && r.settle() {
		r.m.routeReadEnd(r.ctx, r.readCh)
	}
	r.split = false
}

// pumpTerminalInput routes a source other than the process terminal, such as a test's reader: a read worker feeds raw
// reads to the router on this goroutine, which receives no read while the last chunk's listeners are pending, leaving at
// most one read ahead in the worker.
func (m *InteractiveMode) pumpTerminalInput(ctx context.Context, source io.Reader, readCh chan<- inputChunk, errCh chan<- error) (cleanupErr error) {
	r := m.newTerminalInputRouter(ctx, readCh, errCh, false)
	defer r.end()
	rawCh, rawErrCh, closeRaw := m.rawInputChannels(ctx, source)
	defer func() { cleanupErr = closeRaw() }()
	defer r.input.Close()
	r.begin()
	for r.settle() {
		switch kind, buf := priorityInput(rawCh, r.input.C); kind {
		case priorityInputRead:
			r.process(buf)
			continue
		case priorityInputFlush:
			r.flush()
			continue
		case priorityInputClosed, priorityInputNone:
		}
		select {
		case <-ctx.Done():
			return
		case err := <-rawErrCh:
			r.fail(err)
			return
		case buf := <-rawCh:
			r.process(buf)
		case <-r.input.C:
			r.flush()
		}
	}
	return
}

// rawInputChannels retains the existing byte-read contract and joins its worker. Cancellation closes a ReadCloser. Non-closable readers must finish each Read without waiting for external input.
func (*InteractiveMode) rawInputChannels(ctx context.Context, source io.Reader) (<-chan []byte, <-chan error, func() error) {
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
//
//perf:hot
func (m *InteractiveMode) dispatchKey(ctx context.Context, data string) error {
	return m.dispatchInputChunk(ctx, data, nil)
}

// dispatchInputChunk processes one keystroke routed with ticket. When a
// subprocess terminal-input listener must answer first, it returns before the
// keystroke is handled, and handling resumes on the main loop with the verdict.
// pig divergence (D105): the driver owns the terminal reader; extension input listeners run asynchronously around the renderer's listener chain.
func (m *InteractiveMode) dispatchInputChunk(ctx context.Context, data string, ticket *inputTicket) error {
	// pig additive (D91): frontend protocol replies and events never reach
	// key handling.
	if m.frontendInput(data) {
		return nil
	}
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
	// upstream's addInputListener(handleViewportInput) on the alt-screen, which
	// the alt-screen renderer registers; the driver runs the renderer's listeners.
	if m.tuiInst != nil {
		current, done := m.tuiInst.RunInputListeners(data)
		if done {
			return nil
		}
		data = current
	}
	if m.extensionDialog != nil {
		// The dialog is the focused component, so releases are dropped unless
		// it opts in. The editor's equivalent check sits below this branch and
		// never runs while a dialog is open, which is what made every arrow
		// press move a selector cursor two rows.
		if m.tuiInst != nil && m.tuiInst.ConsumeCellSizeResponse(data) {
			return nil
		}
		if m.tuiInst != nil && m.tuiInst.ConsumeDebugKey(data) {
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
// Ports packages/tui/src/tui.ts (handleTerminalInput: driver-owned input routing).
//
//perf:hot
func (m *InteractiveMode) handleKey(ctx context.Context, data string) error {
	// Consume the terminal's reply to the startup cell-size query so it never
	// reaches the editor. Mirrors upstream tui.ts handleTerminalInput, which
	// checks consumeCellSizeResponse after the input listeners and before the
	// focused component.
	if m.tuiInst != nil && m.tuiInst.ConsumeCellSizeResponse(data) {
		return nil
	}
	// The global debug key (Shift+Ctrl+D) runs the TUI's onDebug whatever has focus, after the cell-size reply (tui.ts:1076).
	if m.tuiInst != nil && m.tuiInst.ConsumeDebugKey(data) {
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
			focused = m.tuiInst.GetFocusedComponent()
		}
		if focused != nil && focused != m.editor {
			m.tuiInst.DispatchFocusedInput(data)
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

	return m.handleCustomEditorKey(ctx, data)
}

// handleCustomEditorKey gives one key to the default editor, Pi's CustomEditor.handleInput, and reports the error an app action returned.
func (m *InteractiveMode) handleCustomEditorKey(ctx context.Context, data string) error {
	editor := m.defaultCustomEditor()
	previousCtx, previousErr := m.inputCtx, m.inputErr
	m.inputCtx, m.inputErr = ctx, nil
	defer func() { m.inputCtx, m.inputErr = previousCtx, previousErr }()
	editor.HandleInput(data)
	return m.inputErr
}

// defaultCustomEditor returns the CustomEditor over the current default editor, registering the app handlers Pi's setupKeyHandlers binds the
// first time and again whenever the editor changes.
func (m *InteractiveMode) defaultCustomEditor() *CustomEditor {
	if m.customEditor != nil && m.customEditor.Editor == m.editor && m.customEditor.keybindings == m.keybindings {
		return m.customEditor
	}
	// interactive-mode.ts:645-651 builds the editor from the settings' editorPaddingX and autocompleteMaxVisible.
	paddingX, autocompleteMaxVisible := m.opts.Settings.GetEditorPaddingX(), m.opts.Settings.GetAutocompleteMaxVisible()
	options := CustomEditorOptions{PaddingX: &paddingX, AutocompleteMaxVisible: &autocompleteMaxVisible, EmbedWorkingStatus: true}
	superInput := func(data string) {
		m.runEditorAction(classifyKeyWithBindings(data, m.keybindings), data)
	}
	if m.editor == nil {
		// interactive-mode.ts:645 `new CustomEditor(this.ui, getEditorTheme(), this.keybindings, {...})`: the default editor is the CustomEditor's own.
		m.customEditor = NewCustomEditor(m.tuiInst, tui.EditorTheme{}, m.keybindings, options)
		m.customEditor.superInput = superInput
		m.editor = m.customEditor.Editor
	} else {
		m.customEditor = wrapCustomEditor(m.editor, m.keybindings, superInput, options)
	}
	m.setupKeyHandlers(m.customEditor)
	return m.customEditor
}

// runEditorAction runs the editor handling for one classified key under the key's context and keeps the first error for handleCustomEditorKey.
func (m *InteractiveMode) runEditorAction(action keyAction, data string) {
	if err := m.handleEditorAction(m.inputCtx, action, data); err != nil && m.inputErr == nil {
		m.inputErr = err
	}
}

// setupKeyHandlers binds the app actions on the default editor in the order interactive-mode.ts setupKeyHandlers registers them.
func (m *InteractiveMode) setupKeyHandlers(editor *CustomEditor) {
	editor.OnEscape = func() { m.runEditorAction(actionInterrupt, "") }
	editor.OnCtrlD = func() { m.runEditorAction(actionExit, "") }
	editor.OnPasteImage = func() { m.runEditorAction(actionPasteImage, "") }
	editor.OnExtensionShortcut = func(data string) bool {
		m.terminalInputMu.Lock()
		listener := m.extensionShortcutListener
		m.terminalInputMu.Unlock()
		return listener != nil && listener(data)
	}
	for _, action := range []AppKeybinding{
		AppClear, AppSuspend, AppThinkingCycle, AppModelCycleForward, AppModelCycleBackward, AppModelSelect,
		AppToolsExpand, AppThinkingToggle, AppEditorExternal, AppMessageCopy, AppMessageFollowUp, AppMessageDequeue,
		AppSessionNew, AppSessionTree, AppSessionFork, AppSessionResume,
	} {
		key, _ := appActionKey(action)
		editor.OnAction(action, func() { m.runEditorAction(key, "") })
	}
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

	// Pi temporarily replaces the default editor's Escape callback while
	// compaction runs (interactive-mode.ts:3678). Compaction itself may be active
	// while the agent is idle, so routing through the normal idle/working outcome
	// table makes Escape a no-op. Preserve the same priority explicitly before
	// that table. It comes after the autocomplete intercept: CustomEditor calls
	// onEscape only while no autocomplete is showing (custom-editor.ts:106-113),
	// so Escape over an open autocomplete closes it and compaction continues.
	if action == actionInterrupt && m.isCompacting && m.branchSummaryCancel == nil && m.opts.SessionHandle != nil {
		m.opts.SessionHandle.AbortCompaction()
		return nil
	}

	if action == actionInterrupt && m.branchSummaryCancel != nil {
		m.branchSummaryCancel()
		m.opts.SessionHandle.AbortBranchSummary()
		return nil
	}

	// Upstream onEscape aborts the running `!` command (session.abortBash) when no agent run streams, and asks nothing
	// else to abort. A deferred command can outlive the run that was streaming when it started.
	if action == actionInterrupt && m.bashCancel != nil && m.retryCountdownStop == nil && !m.runStreaming() {
		m.bashCancel()
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
		m.ClearEditor()

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
		m.editor.Clear()
		if m.submit(ctx, text) {
			return nil
		}

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
			m.showStatus("Queued message for after compaction")
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
			m.showStatus("No queued messages to restore")
		case 1:
			m.showStatus("Restored 1 queued message to editor")
		default:
			m.showStatus(fmt.Sprintf("Restored %d queued messages to editor", n))
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
		// Idle Esc with an editor that is empty after JavaScript trim() arms / fires the
		// double-Esc shortcut (mirrors upstream onEscape
		// `!this.editor.getText().trim()`). Default action is "tree";
		// reads from doubleEscapeAction setting ("fork"/"tree"/"none").
		// 500ms window matches upstream `now - this.lastEscapeTime < 500`.
		if action == actionInterrupt && widthx.JSTrim(m.editor.Text()) == "" {
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

// submit runs what Enter does with the editor's trimmed text once the editor
// is cleared: the installed onSubmit handler, or, without one, history and
// handleSubmit, which is upstream's onSubmit: commands and `!` bash run
// immediately, input during compaction queues for after it, and anything else
// goes through prompt() with streamingBehavior "steer" while a run is active.
// It reports whether the handler took the text.
func (m *InteractiveMode) submit(ctx context.Context, text string) bool {
	if m.editor.OnSubmit != nil {
		m.editor.OnSubmit(text)
		return true
	}
	m.editor.AddToHistory(text)
	m.handleSubmit(ctx, text)
	return false
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
		dynamic = append(dynamic, SlashCommand{
			Name:        cmdName,
			Description: rc.Description,
			Handler: func(_ *ExtensionContext, args string) error {
				baseCtx := m.runCtx
				if baseCtx == nil {
					baseCtx = context.Background()
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
	m.syncExtensionSlashCommands()
	_, ok := m.slashRegistry.Match(prompt)
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
			// pig divergence (D135): queued extension custom messages are not restored to the editor and stay queued.
			m.agent.ClearQueuedUserMessages()
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
	// interactive-mode.ts isExtensionCommand: the name is the text before the first space.
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
