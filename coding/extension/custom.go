package extension

import "github.com/MichaelKinsy/PiG/tui"

// CustomFactory builds the component [UIContext.Custom] shows, for an extension that runs in the host process
// (upstream `ctx.ui.custom(factory)`). theme is the active theme and keybindings the merged keybinding table of the
// interactive mode (both concrete types of the tui package); done ends the call with a result and may run on any
// goroutine. The component renders and reads input on the interactive loop; it may also implement
// interface{ Dispose() }, which runs once after done.
// upstream: packages/coding-agent/src/core/extensions/types.ts:187 (custom)
type CustomFactory func(tui TUI, theme *tui.Theme, keybindings KeybindingsManager, done func(result any)) (DisposableComponent, error)

// OverlayOptionsSource is `OverlayOptions | (() => OverlayOptions)`, the overlayOptions member of [CustomOptions]: the options themselves
// or a function that returns them when the overlay is shown.
type OverlayOptionsSource interface {
	overlayOptionsSource()
}

// OverlayOptionsValue is overlayOptions given as an object.
type OverlayOptionsValue struct{ Options tui.OverlayOptions }

// OverlayOptionsFunc is overlayOptions given as a function, called each time the overlay is shown.
type OverlayOptionsFunc func() tui.OverlayOptions

func (OverlayOptionsValue) overlayOptionsSource() {}
func (OverlayOptionsFunc) overlayOptionsSource()  {}

// CustomBlocked is the `blocked` option of [UIContext.Custom]: what an inline component waits for. The terminal's program status
// shows pi as blocked on it while the component is visible. Kind is "question", "permission" or "auth" (a tui.ProgramStatusKind); Message
// is the line the terminal shows. The values are not validated, as Pi's runtime does not validate them.
// upstream: packages/coding-agent/src/core/extensions/types.ts `blocked?: { kind: NonNullable<ProgramStatus["kind"]>; message: string }`
type CustomBlocked struct {
	Kind    tui.ProgramStatusKind `json:"kind"`
	Message string                `json:"message"`
}

// CustomOptions is the `options` of an in-process [UIContext.Custom].
// upstream: packages/coding-agent/src/core/extensions/types.ts:187 `{ overlay?: boolean; overlayOptions?: OverlayOptions | (() => OverlayOptions); onHandle?: (handle: OverlayHandle) => void; blocked?: { kind; message } }`
type CustomOptions struct {
	// Overlay shows the component over the screen instead of in the editor slot.
	Overlay bool
	// OverlayOptions positions an overlay.
	OverlayOptions OverlayOptionsSource
	// OnHandle receives the overlay's handle as soon as the overlay is shown, to hide, focus or unfocus it.
	OnHandle func(handle *tui.OverlayHandle)
	// Blocked reports pi as blocked on user input (OSC 7501 program status) while an inline component is visible. An overlay ignores it.
	Blocked *CustomBlocked
}
