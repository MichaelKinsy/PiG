package tui

import "context"

// TUI is the interactive-driver-facing rendering contract implemented by both the main-screen ([TuiMainScreen]) and
// alternate-screen ([TuiAltScreen]) renderers (packages/tui/src/tui.ts TUI); the driver holds one of these and does not
// care which renderer backs it.
type TUI interface {
	// The Container and state members tui.ts:454-476 declares on TUI.
	AddChild(component Component)
	RemoveChild(component Component)
	Children() []Component
	Clear()
	HideOverlay()
	GetShowHardwareCursor() bool
	GetClearOnShrink() bool
	HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult
	Mode() TuiMode
	// OnDebug is the callback SetOnDebug stored, nil when none is set (tui.ts:457 onDebug).
	OnDebug() func()
	// FullRedraws counts full repaints (tui.ts:553 fullRedraws).
	FullRedraws() int
	// RenderNow renders at once and cancels a pending render; force makes the frame a full repaint (tui.ts:982 renderNow).
	RenderNow(force ...bool)
	Terminal() Terminal
	// Render performs a differential render pass now.
	Render()
	// RequestRender coalesces a render on the shared 16ms frame throttle; force (upstream requestRender(force = false)) makes the frame a full
	// repaint that renders on the next owner-loop turn.
	RequestRender(force ...bool)
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
	// ShowOverlay pushes an overlay and returns its handle.
	ShowOverlay(component Component, opts OverlayOptions) *OverlayHandle
	// SetFocus records the non-overlay target restored after overlay teardown.
	SetFocus(component Component)
	// ActiveOverlay prepares visibility and eligible focus restoration at the input boundary.
	ActiveOverlay() Component
	// FocusedComponent returns the current keyboard focus target.
	GetFocusedComponent() Component
	// SetOverlayCommandDispatcher binds remote overlay commands to the owner loop.
	SetOverlayCommandDispatcher(dispatch func(func()))
	// HasOverlay reports whether any overlay is currently open.
	HasOverlay() bool
	// Width and Height report the current terminal geometry.
	Width() int
	Height() int
	// QueryTerminalColors asks for the default colors and ANSI palette and returns its one-shot completion.
	QueryTerminalColors(options TerminalColorQueryOptions) <-chan TerminalColorsResult
	// ConsumeTerminalColorResponse intercepts color and DA1 replies before input listeners, including late replies after timeout.
	ConsumeTerminalColorResponse(data string) bool
	// AddInputListener registers a raw-input listener that runs before the focused component and returns its remover; RemoveInputListener removes it by pointer.
	AddInputListener(listener *TuiInputListener) func()
	RemoveInputListener(listener *TuiInputListener)
	// RunInputListeners passes input through the listeners and reports whether they consumed it.
	RunInputListeners(data string) (string, bool)
	// OnTerminalColorSchemeChange registers a light/dark report listener and returns its remover.
	OnTerminalColorSchemeChange(listener func(scheme TerminalColorScheme)) func()
	// SetTerminalColorSchemeNotifications turns light/dark reports on or off across Start and Stop.
	SetTerminalColorSchemeNotifications(enabled bool)
	// ConsumeTerminalColorSchemeReport notifies the listeners of a light/dark report and reports whether data was one.
	ConsumeTerminalColorSchemeReport(data string) bool
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
	// HandleTerminalInput dispatches one chunk of terminal input as tui.ts:1044 handleTerminalInput does; DispatchFocusedInput is its last stage, for a driver that ran the earlier ones.
	HandleTerminalInput(data string)
	DispatchFocusedInput(data string)
	// SetOwnerDispatcher installs the seam PostToOwner uses; PostToOwner queues fn on the loop that owns component-tree mutation and returns once the loop accepted it or ctx ended.
	SetOwnerDispatcher(dispatch func(ctx context.Context, fn func()) error)
	PostToOwner(ctx context.Context, fn func()) error
	// SetOnDebug sets the global debug-key callback; ConsumeDebugKey runs it for Shift+Ctrl+D and reports whether data was consumed.
	SetOnDebug(onDebug func())
	ConsumeDebugKey(data string) bool
	// Start resumes rendering after Stop. The driver owns terminal input and raw mode.
	Start()
	// Stop tears down the renderer.
	Stop()
	// StopWithOptions tears down the renderer; PreserveScreen leaves the current
	// screen intact for a live renderer swap (no end-of-session output).
	StopWithOptions(options StopOptions)
}

var (
	_ TUI = (*TuiMainScreen)(nil)
	_ TUI = (*TuiAltScreen)(nil)
)
