package codingagent

import (
	"fmt"
	"os"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

// pig additive (D91): a Piglet frontend member can draw the interactive
// mode in place of Pi's ANSI renderer. Pi has no such member; with none
// fused, nothing here runs and the ANSI path is unchanged.

// openFrontend offers the run to the fused frontend after raw mode is on, as
// a terminal protocol handshake requires, and before the first paint. A
// frontend that takes the run replaces the renderer that has not painted yet.
func (m *InteractiveMode) openFrontend() {
	if m.opts.Frontend == nil {
		return
	}
	out := m.rendererOut
	if out == nil {
		out = os.Stdout
	}
	// The first fallback request names the reason; later ones, such as each
	// failed frame after it, change nothing.
	var leaving atomic.Bool
	leave := func(reason string) {
		if !leaving.Swap(true) {
			m.leaveFrontendAsync(reason)
		}
	}
	session, err := m.opts.Frontend.Open(frontend.Env{
		Out:      out,
		Columns:  m.tuiInst.Width(),
		Getenv:   os.Getenv,
		AppName:  AppName,
		Version:  m.opts.AppVersion,
		Fallback: leave,
	})
	if err != nil {
		m.frontendStartupWarning = fmt.Sprintf("Native rendering is unavailable: %v", err)
		return
	}
	if session == nil {
		return
	}
	onApplyError := func(err error) { leave(fmt.Sprintf("drawing failed: %v", err)) }
	var surface *tui.TuiSurface
	if m.rendererOut != nil {
		surface = tui.NewTuiSurfaceWithSize(session, m.tuiInst.Width(), m.tuiInst.Height(), onApplyError)
	} else {
		surface = tui.NewTuiSurface(session, onApplyError)
	}
	m.rendererMu.Lock()
	defer m.rendererMu.Unlock()
	m.surface = surface
	m.tuiInst = surface
	// The fullscreen renderer built for this run never starts.
	m.altScreen = nil
	m.applyBaseRendererWiring()
}

// leaveFrontendAsync schedules leaveFrontend on the owner loop. It never
// blocks the caller, which may be the owner loop itself, and the background
// task ends when the run does.
func (m *InteractiveMode) leaveFrontendAsync(reason string) {
	ctx := m.backgroundCtx
	m.backgroundTasks.Go(func() {
		_ = m.postToMain(ctx, func() { m.leaveFrontend(reason) })
	})
}

// leaveFrontend closes the frontend session and continues the run with the
// configured ANSI renderer, repainting the whole component tree. Open
// overlays belong to the outgoing renderer and are closed.
func (m *InteractiveMode) leaveFrontend(reason string) {
	if m.surface == nil || m.tuiTornDown {
		return
	}
	m.rendererMu.Lock()
	surface := m.surface
	surface.Stop()
	closeErr := surface.Session().Close()
	for surface.HasOverlay() {
		surface.HideOverlay()
	}
	m.surface = nil
	m.teardownCurrentTui()
	m.createInteractiveTui(m.runCtx)
	m.rewireRenderer()
	m.mountInteractiveTui(true)
	m.tuiInst.Invalidate()
	m.tuiInst.Render()
	m.rendererMu.Unlock()
	message := "Native rendering stopped: " + reason
	if closeErr != nil {
		message += fmt.Sprintf(" (closing it failed: %v)", closeErr)
	}
	m.showWarning(message)
}

// frontendInputReady tells the session that PiG's input loop is about to
// run, after startup has painted the first frame.
func (m *InteractiveMode) frontendInputReady() {
	if m.surface != nil {
		m.surface.Session().InputReady()
	}
}

// frontendInput offers terminal input to the frontend session.
func (m *InteractiveMode) frontendInput(data string) bool {
	return m.surface != nil && m.surface.HandleFrontendInput(data)
}

// closeFrontend stops drawing and closes the session while PiG still reads
// input, so the session's last replies are drained instead of reaching the
// shell. The error is reported after cooked mode returns.
func (m *InteractiveMode) closeFrontend() {
	if m.surface == nil {
		return
	}
	m.surface.Stop()
	m.frontendCloseErr = m.surface.Session().Close()
}

// applyBaseRendererWiring applies the wiring Run gives its renderer before
// the component tree exists.
func (m *InteractiveMode) applyBaseRendererWiring() {
	m.tuiInst.SetShowHardwareCursor(m.opts.Settings.GetShowHardwareCursor())
	m.tuiInst.SetClearOnShrink(m.opts.Settings.GetClearOnShrink())
	m.installRenderDispatcher()
	m.tuiInst.SetOverlayCommandDispatcher(func(command func()) {
		m.runOnMain(m.runCtx, command)
	})
}

// rewireRenderer applies the run's renderer-level wiring to a renderer that
// replaces another one mid-run, sourced from settings and host state so the
// current configuration carries across the swap.
func (m *InteractiveMode) rewireRenderer() {
	m.applyBaseRendererWiring()
	m.tuiInst.SetFocus(m.editor)
	if m.opts.SubprocessHost != nil {
		m.tuiInst.SetOnWidthChange(func(width int) { m.opts.SubprocessHost.NotifyWidth(width) })
	}
	// Registered unconditionally: the dialog chat cap depends on height even
	// when no subprocess extensions are loaded.
	m.tuiInst.SetOnHeightChange(m.onTerminalHeightChange)
}
