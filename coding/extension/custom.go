package extension

// CustomHost is the terminal a component built by a [CustomFactory] draws on (upstream's `tui` argument).
type CustomHost interface {
	// RequestRender asks the terminal to draw again. It is safe from any goroutine.
	RequestRender()
}

// CustomFactory builds the component [UIContext.Custom] shows, for an extension that runs in the host process
// (upstream `ctx.ui.custom(factory)`). theme is the active theme and keybindings the merged keybinding table of the
// interactive mode (both concrete types of the tui package); done ends the call with a result and may run on any
// goroutine. The component renders and reads input on the interactive loop; it may also implement
// interface{ Dispose() }, which runs once after done.
// upstream: packages/coding-agent/src/core/extensions/types.ts:187 (custom)
type CustomFactory func(host CustomHost, theme Theme, keybindings KeybindingsManager, done func(result any)) (Component, error)

// CustomOptions is the `options` of an in-process [UIContext.Custom].
// upstream: packages/coding-agent/src/core/extensions/types.ts:187 (overlay, overlayOptions)
type CustomOptions struct {
	// Overlay shows the component over the screen instead of in the editor slot.
	Overlay bool
	// Layout positions an overlay.
	Layout *OverlayLayout
}
