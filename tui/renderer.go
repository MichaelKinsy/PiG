package tui

// Renderer is the interactive-driver-facing rendering contract implemented by
// both the main-screen (TUI) and alternate-screen (TuiAltScreen) renderers. It
// is the Go equivalent of upstream's TUI interface (packages/tui/src/tui.ts),
// which both TuiMainScreen and TuiAltScreen implement; the driver holds one of
// these and does not care which renderer backs it.
//
// The interface is named Renderer rather than TUI (upstream's name) because pig
// reuses the identifier TUI for the concrete main-screen renderer struct (the
// layer-6 tui-main-screen naming reconciliation). This is a forced Go naming
// divergence, not a behavioral one.
type Renderer interface {
	// Render performs a differential render pass now.
	Render()
	// RequestRender coalesces a render on the shared 16ms frame throttle.
	RequestRender()
	// RequestImmediateRender coalesces input updates onto the next owner-loop turn without throttle delay.
	RequestImmediateRender()
	// CancelPendingRender invalidates a throttled frame already queued for owner-loop delivery.
	CancelPendingRender()
	// ForceFullRender marks the next frame as a full (non-differential) redraw.
	ForceFullRender()
	// RepaintAll forces an immediate full repaint.
	RepaintAll()
	// RenderSnapshot returns the rendered document lines at the given width.
	RenderSnapshot(width int) []string
	// Add mounts a component in the render tree.
	Add(component Component)
	// Invalidate marks the render tree dirty.
	Invalidate()
	// OpenOverlay pushes an overlay and returns its handle.
	OpenOverlay(component Component, opts OverlayOptions) *OverlayHandle
	// SetFocus records the non-overlay target restored after overlay teardown.
	SetFocus(component Component)
	// ActiveOverlay prepares visibility and eligible focus restoration at the input boundary.
	ActiveOverlay() Component
	// FocusedComponent returns the current keyboard focus target.
	FocusedComponent() Component
	// SetOverlayCommandDispatcher binds remote overlay commands to the owner loop.
	SetOverlayCommandDispatcher(dispatch func(func()))
	// HasOverlay reports whether any overlay is currently open.
	HasOverlay() bool
	// Width and Height report the current terminal geometry.
	Width() int
	Height() int
	// QueryTerminalBackgroundColor asks for the default background and returns its one-shot completion.
	QueryTerminalBackgroundColor(options TerminalColorQueryOptions) <-chan TerminalBackgroundColorResult
	// ConsumeOsc11BackgroundResponse intercepts replies before input listeners, including late replies after timeout.
	ConsumeOsc11BackgroundResponse(data string) bool
	// QueryCellSize asks an image-capable terminal for its cell size.
	QueryCellSize()
	// ConsumeCellSizeResponse applies and consumes a cell-size response.
	ConsumeCellSizeResponse(data string) bool
	// ShowCursor and HideCursor toggle the terminal cursor directly.
	ShowCursor()
	HideCursor()
	// SetShowHardwareCursor toggles hardware-cursor positioning.
	SetShowHardwareCursor(enabled bool)
	// SetClearOnShrink records the clear-on-shrink preference.
	SetClearOnShrink(enabled bool)
	// SetOnWidthChange registers a terminal-width-change callback.
	SetOnWidthChange(fn func(width int))

	// SetOnHeightChange registers a terminal-height-change callback.
	SetOnHeightChange(fn func(height int))
	// SetRenderDispatcher marshals timer-scheduled renders onto the driver loop.
	SetRenderDispatcher(dispatch func(render func()))
	// Start resumes rendering after Stop. The driver owns terminal input and raw mode.
	Start()
	// Stop tears down the renderer.
	Stop()
	// StopWithOptions tears down the renderer; PreserveScreen leaves the current
	// screen intact for a live renderer swap (no end-of-session output).
	StopWithOptions(options StopOptions)
}

var (
	_ Renderer = (*TUI)(nil)
	_ Renderer = (*TuiAltScreen)(nil)
)
