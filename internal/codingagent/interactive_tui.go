package codingagent

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

func (m *InteractiveMode) onTerminalResize() {
	m.postUITask(func() {
		m.applyEditorMaxVisible()
		// Pi's resize callback is requestRender(): the renderer repaints the
		// whole buffer only when the width or height changed, and it keeps
		// Termux keyboard height changes differential. SIGWINCH also arrives
		// without a geometry change (tmux window switches, terminal focus and
		// visibility changes), and a forced repaint there would replay the
		// entire transcript and snap the view to the top.
		m.tuiInst.RequestRender()
	})
}

// themedScrollbarTrackStyle and themedScrollbarThumbStyle style the fullscreen
// scrollbar with the live theme's scrollbarTrack and scrollbarThumb foregrounds
// (the theme resolver falls back to muted and text when a theme omits them).
// They read ActiveTheme() at call time: the layout invokes the styles on every
// paint, so a live theme change repaints the scrollbar on the next render.
// Mirrors upstream interactive-mode.ts scrollbarTrackStyle/scrollbarThumbStyle:
// (text) => theme.fg("scrollbarTrack"/"scrollbarThumb", text).
func themedScrollbarTrackStyle(text string) string {
	return tui.ActiveTheme().FgText("scrollbarTrack", text)
}

func themedScrollbarThumbStyle(text string) string {
	return tui.ActiveTheme().FgText("scrollbarThumb", text)
}

// buildChatViewport constructs the fullscreen transcript over the input dock.
// Run() builds it at startup; live tui-mode switching (switchTuiMode) rebuilds
// it as the second caller, which is why the construction lives here rather
// than inline in Run. Mirrors upstream init's createChatViewport call.
func (m *InteractiveMode) buildChatViewport() ChatViewport {
	m.ensureLoadedResourcesContainer()
	return CreateChatViewport(ChatViewportOptions{
		Document:            tui.NewContainer(m.headerContainer(), m.loadedResourcesContainer, m.chatContainer),
		PendingMessages:     m.pendingMessagesContainer,
		Status:              m.statusContainer,
		WidgetsAbove:        m.widgetContainer,
		Editor:              m.editorContainer,
		WidgetsBelow:        m.widgetContainerBelow,
		Footer:              tui.NewContainer(m.extFooter, m.statusLine),
		Scrollbar:           (&SettingsManager{merged: m.opts.Settings}).GetFullscreenScrollbar(),
		ScrollbarTrackStyle: themedScrollbarTrackStyle,
		ScrollbarThumbStyle: themedScrollbarThumbStyle,
	})
}

// ensureLoadedResourcesContainer creates the loaded-resources container for a
// mode assembled without Run.
func (m *InteractiveMode) ensureLoadedResourcesContainer() {
	if m.loadedResourcesContainer == nil {
		m.loadedResourcesContainer = tui.NewContainer()
	}
}

// mountInteractiveTui builds the mode-appropriate layout from the shared component tree and, when start is set, starts and paints the renderer. Fullscreen arranges the transcript scroll view over the dock and enters the alt screen; regular adds the flat layout to the main screen. The initial paint does not depend on a welcome header or input. Run and live tui-mode switching reuse the same containers across renderer mounts.
func (m *InteractiveMode) mountInteractiveTui(start bool) {
	m.ensureLoadedResourcesContainer()
	if m.widgetContainerBelow == nil {
		m.widgetContainerBelow = tui.NewContainer()
	}
	layoutChildren := []tui.Component{
		m.headerContainer(),
		m.loadedResourcesContainer,
		m.chatContainer,
		m.pendingMessagesContainer,
		m.statusContainer,
		m.widgetContainer,
		m.editorContainer,
		m.widgetContainerBelow,
		m.extFooter,
		m.statusLine,
	}
	m.layout = tui.NewContainer(layoutChildren...)
	if m.altScreen == nil {
		m.tuiInst.Add(m.layout)
		if !start {
			return
		}
		m.tuiInst.QueryCellSize()
		// Pi's TUI.start paints even when quietStartup omits the header.
		m.tuiInst.Render()
		return
	}
	// Fullscreen: build the transcript/dock layout root and enter the alt screen.
	// The flat m.layout is kept for the editor-slot swap helpers and the nil
	// guards, but is not the render root in this mode.
	viewport := m.buildChatViewport()
	m.transcriptScrollView = viewport.Transcript
	m.altScreen.SetLayoutRoot(viewport.Root)
	if start {
		m.altScreen.Start()
	}
}

// switchTuiMode swaps the renderer on the owner loop while preserving the component tree and main-screen render state. The current mode is a no-op; an active overlay refuses a change. Renderer references resolve dynamically under rendererMu, and only a successful swap changes the run's mode. Without startRenderer the new renderer is mounted but neither started nor painted, as the exit path needs.
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:switchTuiMode
func (m *InteractiveMode) switchTuiMode(mode string, restoreProgress, startRenderer bool) bool {
	current := "regular"
	if m.altScreen != nil {
		current = "fullscreen"
	}
	if mode == current {
		return true
	}
	if m.tuiInst.HasOverlay() {
		return false
	}

	// Hold the renderer write lock across the whole swap so a concurrent
	// invalidation from a subprocess/extension goroutine (which takes RLock via
	// renderNow/requestRender/invalidate) cannot land on a renderer after its
	// preserve-screen stop: it either completes on the old renderer before the
	// stop or waits and runs on the new one. The lock also serializes the
	// component-tree rebuild below against concurrent renders. Non-reentrant: see
	// the rendererMu invariant: nothing called under this lock may take RLock.
	m.rendererMu.Lock()
	defer m.rendererMu.Unlock()

	// Capture main-screen render state when leaving regular and persist it on m
	// so the return leg (fullscreen->regular) can restore it. Local-only state
	// would be lost when this call returns. Mirrors upstream
	// InteractiveMode.mainScreenRenderState.
	if prev, ok := m.tuiInst.(*tui.TUI); ok {
		state := prev.CaptureRenderState()
		m.mainScreenRenderState = &state
	}

	// Tear down the outgoing renderer: cancel its owner-scoped tick context,
	// dispose the fullscreen transcript timer (owned here, not by the renderer),
	// then stop it with PreserveScreen so the swap emits no end-of-session output
	// (no regular cursor-park newline; the alt screen leaves without dumping the
	// transcript into scrollback). m.tuiInst and m.altScreen are the same object
	// in fullscreen.
	m.teardownCurrentTui()
	if current == "fullscreen" && m.transcriptScrollView != nil {
		m.transcriptScrollView.Dispose()
		m.transcriptScrollView = nil
	}
	if m.themeState.autoSyncEnabled.Load() {
		m.writeThemeNotifications(false)
	}
	m.tuiInst.StopWithOptions(tui.StopOptions{PreserveScreen: true})
	m.altScreen = nil

	// Build the incoming renderer from the run's mode, independently of the settings snapshot that reload and unrelated settings changes replace.
	m.opts.TuiMode = mode
	m.createInteractiveTui(m.runCtx)

	// Restore persisted main-screen render state when entering regular.
	if next, ok := m.tuiInst.(*tui.TUI); ok && m.mainScreenRenderState != nil {
		next.RestoreRenderState(*m.mainScreenRenderState)
	}

	// Re-apply Run's renderer-level wiring to the new renderer, sourced from
	// settings/host state so the current configuration carries across the swap.
	// Inlined rather than shared because Run interleaves this with one-time setup
	// (the initial extension width kick) a live swap must not repeat.
	m.tuiInst.SetShowHardwareCursor(m.opts.Settings.GetShowHardwareCursor())
	m.tuiInst.SetClearOnShrink(m.opts.Settings.GetClearOnShrink())
	m.installRenderDispatcher()
	m.tuiInst.SetOverlayCommandDispatcher(func(command func()) {
		m.runOnMain(m.runCtx, command)
	})
	m.tuiInst.SetFocus(m.editor)
	if m.opts.SubprocessHost != nil {
		m.tuiInst.SetOnWidthChange(func(width int) { m.opts.SubprocessHost.NotifyWidth(width) })
	}
	// Registered unconditionally: the dialog chat cap depends on height even
	// when no subprocess extensions are loaded.
	m.tuiInst.SetOnHeightChange(m.onTerminalHeightChange)

	// Remount the complete shared component tree into the new renderer, mirroring
	// upstream remounting every previous child.
	m.mountInteractiveTui(startRenderer)
	if !startRenderer {
		m.tuiInst.Invalidate()
		return true
	}
	if m.themeState.autoSyncEnabled.Load() {
		m.writeThemeNotifications(true)
	}
	m.tuiInst.Invalidate()

	// Restore terminal progress if a turn is in flight and progress is enabled
	// (mirrors upstream restoreProgress + session.isStreaming/isCompacting).
	if restoreProgress && m.opts.Settings.GetShowTerminalProgress() &&
		((m.agent != nil && m.agent.IsStreaming()) || m.isCompacting) {
		setTerminalProgress(true)
	}
	m.tuiInst.Render()
	return true
}

// createInteractiveTui constructs the interactive renderer for this Run and
// returns a cleanup that MUST run on every Run return. Fullscreen mode uses the
// alternate-screen renderer and installs the selection auto-scroll tick seam;
// regular mode uses the main-screen renderer and needs no cleanup.
//
// The auto-scroll tick is an owned state-machine tick, not a cosmetic render: it
// must reach the owner loop while the loop is alive, or the fired one-shot timer
// wedges (never re-arms). It binds a context whose lifetime is exactly THIS Run
// (the owner loop), not the turn-scoped m.abortCtx, which is canceled and
// replaced on every abort and new turn: reading m.abortCtx from the timer
// goroutine both races the owner-loop writes and can drop a tick against a
// canceled turn context. The returned cleanup cancels uiCtx on every Run return
// (including input-read errors), so a tick goroutine blocked on a saturated
// uiTaskCh unblocks at shutdown instead of outliving the session.
//
// interactiveTuiHandle is createInteractiveTui's result. cleanup tears down the
// renderer's owner-scoped background work (the fullscreen auto-scroll tick
// context) and is also stored on InteractiveMode as the current renderer's
// cleanup. tickDispatch is the owner-loop tick seam bound onto a fullscreen
// renderer (nil in regular mode); it is the private construction result an
// in-package test drives to verify the production owner-loop context binding.
type interactiveTuiHandle struct {
	cleanup      func()
	tickDispatch func(func())
}

// effectiveOpenURL returns the injected OSC 8 opener, defaulting to openBrowser.
func (m *InteractiveMode) effectiveOpenURL() func(url string) error {
	if m.openURL != nil {
		return m.openURL
	}
	return openBrowser
}

func (m *InteractiveMode) effectiveCopyClipboard() func(text string) error {
	if m.copyClipboard != nil {
		return m.copyClipboard
	}
	return copyToClipboard
}

// renderNow paints the current renderer race-safely: it holds rendererMu.RLock
// across the call so a concurrent switchTuiMode (which holds the write lock
// across stop+swap) cannot let this land on a renderer after its preserve-screen
// stop. Used by the dynamic invalidation closures that can fire off the owner
// loop (subprocess/extension goroutines).
func (m *InteractiveMode) renderNow() {
	defer func() {
		if value := recover(); value != nil {
			m.forwardRenderCrash(value)
		}
	}()
	m.rendererMu.RLock()
	defer m.rendererMu.RUnlock()
	m.tuiInst.Render()
}

// requestRender coalesces a render on the current renderer, race-safe like
// renderNow.
func (m *InteractiveMode) requestRender() {
	m.rendererMu.RLock()
	defer m.rendererMu.RUnlock()
	m.tuiInst.RequestRender()
}

// teardownCurrentTui runs the current renderer's cleanup (owner-loop tick context
// cancel). Idempotent and nil-safe; reads the field at call time so a renderer
// swap's replacement cleanup is the one that runs at teardown.
func (m *InteractiveMode) teardownCurrentTui() {
	if m.currentTuiCleanup != nil {
		m.currentTuiCleanup()
	}
}

func (m *InteractiveMode) createInteractiveTui(ctx context.Context) interactiveTuiHandle {
	uiCtx, cancelUI := context.WithCancel(ctx)
	reads := &sync.WaitGroup{}
	m.clipboardCtx, m.clipboardReads = uiCtx, reads
	if m.opts.TuiMode != "fullscreen" {
		m.tuiInst = CreateInteractiveTui(InteractiveTuiOptions{TuiMode: "regular", Output: m.rendererOut, LogDirectory: m.opts.AgentDir})
		m.altScreen = nil
		m.currentTuiCleanup = func() { cancelUI(); reads.Wait() }
		return interactiveTuiHandle{cleanup: m.currentTuiCleanup}
	}
	settings := &SettingsManager{merged: m.opts.Settings}
	copyOnSelect, wheelScrollLines := settings.GetFullscreenCopyOnSelect(), tuiWheelScrollLines(settings.GetFullscreenWheelScrollLines())
	m.tuiInst = CreateInteractiveTui(InteractiveTuiOptions{
		TuiMode: "fullscreen", Output: m.rendererOut, LogDirectory: m.opts.AgentDir,
		FullscreenCopyOnSelect: &copyOnSelect, FullscreenWheelScrollLines: &wheelScrollLines, CopySelection: m.effectiveCopyClipboard(),
		OpenURL: func(url string) error { return m.effectiveOpenURL()(url) }, OnRightClickPaste: m.handleRightClickPaste,
	})
	m.altScreen = m.tuiInst.(*tui.TuiAltScreen)
	dispatch := m.autoScrollTickDispatcher(uiCtx)
	m.altScreen.SetTickDispatcher(dispatch)
	m.currentTuiCleanup = func() { cancelUI(); reads.Wait() }
	return interactiveTuiHandle{cleanup: m.currentTuiCleanup, tickDispatch: dispatch}
}

// autoScrollTickDispatcher returns the owner-loop dispatcher for the selection
// auto-scroll tick, bound to a STABLE owner-loop-scoped context (uiCtx), not the
// turn-scoped m.abortCtx. Delivery blocks on the owner loop's uiTaskCh while
// uiCtx is live and unblocks when uiCtx is canceled (Run return), so the tick is
// serialized with rendering as upstream's single event loop does and no dispatch
// goroutine outlives the owner loop. Extracted so the context binding is testable
// without driving Run().
func (m *InteractiveMode) autoScrollTickDispatcher(uiCtx context.Context) func(func()) {
	return func(fn func()) {
		m.runOnMain(uiCtx, fn)
	}
}

// requestShutdown asks the input loop to exit from any goroutine. Extension
// shutdown callbacks (the subprocess host "shutdown" action and the in-process
// runner Shutdown) run off the owner loop, so they must not stop the renderer or
// dispose views directly: that would race the loop and the deferred teardown
// over renderer state. Instead this sets the atomic exit flag and wakes the loop
// with a non-blocking post; the loop then runs stopInteractiveTui on its own
// goroutine at the top of the next iteration. The wake may be dropped under
// saturation without losing the exit, because the loop re-reads requestExit at
// the top of every iteration and on every event. Safe on-loop too (the flag is
// simply observed on the next pass).
func (m *InteractiveMode) requestShutdown() {
	m.requestExit.Store(true)
	m.postUITask(func() {})
}

// stopInteractiveTui drains input, stops the renderer while output is raw, then restores cooked mode and disposes the transcript view exactly once. Signal cleanup precedes it; dead-terminal emergencies skip these writes.
func (m *InteractiveMode) stopInteractiveTui() {
	if m.tuiTornDown {
		return
	}
	m.tuiTornDown = true
	m.tuiStopped.Store(true)
	m.disposeTheme()
	if m.inputReader != nil {
		m.inputReader.pause()
	}
	if m.rawDrain != nil {
		m.rawDrain()
		m.rawDrain = nil
	} else if m.rawRestore != nil && m.inputReader != nil {
		// upstream: packages/tui/src/terminal.ts:drainInput
		_ = m.inputReader.terminal.DrainInput(time.Second, 50*time.Millisecond)
	}
	// A fullscreen exit that prints the transcript switches to the main-screen renderer without starting it, renders there, and stops it as a regular run stops: parked below the last line, then CRLF. The resume hint keeps the alternate screen's own preserve-screen exit.
	// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:stopInteractiveTui
	if m.altScreen != nil && (&SettingsManager{merged: m.opts.Settings}).GetFullscreenExitOutput() == "transcript" {
		for m.altScreen.HasOverlay() {
			m.altScreen.HideOverlay()
		}
		m.switchTuiMode("regular", false, false)
		m.renderNow()
	}
	if m.altScreen != nil {
		m.altScreen.StopWithOptions(tui.StopOptions{PreserveScreen: true})
	} else if m.tuiInst != nil {
		m.tuiInst.Stop()
	}
	// The resume hint and extension shutdown run only after cooked output is
	// restored; a bare LF in raw mode leaves the next writer mid-line.
	if m.rawRestore != nil {
		m.rawRestore()
		m.rawRestore = nil
	}
	// The fullscreen transcript view is owned here, not by the renderer's
	// implicitScrollView, so its scrollbar-hide timer must be disposed by the
	// owner or it can outlive the session and request a render after stop.
	if m.transcriptScrollView != nil {
		m.transcriptScrollView.Dispose()
	}
}

// ShutdownFromSignal emits session_shutdown before requesting owner-loop teardown. Concurrent callers join ongoing cleanup, and repeated calls do not emit again.
//
// Upstream's signal-triggered shutdown emits extension cleanup BEFORE touching
// the terminal, because teardown such as removing sockets does not write to the
// tty and must not be skipped if a later terminal-restore write fails
// (interactive-mode.ts shutdown({fromSignal: true})).
//
// Ordering matters in pig for a second reason: extension subprocesses are
// spawned with the root context, so cancelling it kills them. If SIGTERM
// cancelled the context first, the shutdown event would be delivered to
// processes that no longer exist and extensions would silently never clean up.
// The caller therefore invokes this before cancelling.
func (m *InteractiveMode) ShutdownFromSignal() {
	if m == nil {
		return
	}
	m.shutdownMu.Lock()
	defer m.shutdownMu.Unlock()
	if !m.signalShutdownDone.CompareAndSwap(false, true) {
		return
	}
	m.emitQuitShutdown()
	m.requestShutdown()
}

// handleInterruptSignal terminates the session on SIGINT, matching upstream,
// after giving extensions their shutdown event and returning the terminal to
// cooked mode.
//
// The temporary suspend listener is checked at dispatch time. A SIGINT queued while stopped can terminate the process if SIGCONT removes that listener before SIGINT is dispatched, matching Pi's listener lifecycle.
//
// Exits rather than unwinding Run because the input loop may be blocked in a
// read. Only signal-safe protocol disable and termios restoration run here; normal teardown also drains stdin, which the render loop owns.
func (m *InteractiveMode) handleInterruptSignal() {
	if m.suspended.Load() {
		return
	}
	// pig additive (D102): Pi dies on SIGINT with no trace.
	RecordExit(m.opts.AgentDir, "received SIGINT (exit 130)")
	// pig divergence (D51): restore terminal state before exit 130.
	done := make(chan struct{})
	go func() {
		m.ShutdownFromSignal()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
	tui.RestoreTerminalFromSignal()
	EndSessionMarker()
	os.Exit(130)
}

// `/model` empty-args) and applies the chosen model.
// Equivalent to /model with no args; reuses the SlashContext callbacks
// so behavior stays consistent between keybinding and slash entry.

// requestExtensionShutdown ends the session at an extension's request. The user did not ask for it, so it leaves a line
// in exit.log; an extension's timer can end an idle session this way.
// pig additive (D102): Pi leaves no trace.
func (m *InteractiveMode) requestExtensionShutdown() {
	RecordExit(m.opts.AgentDir, "extension requested shutdown (ctx.shutdown())")
	m.requestShutdown()
}
