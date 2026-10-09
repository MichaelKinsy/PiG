package extension

// RemoteMouseEvent is pi-tui's TuiMouseEvent as it crosses to an extension:
// one cell-based pointer event, zero-based, with X and Y relative to the
// receiving component's first rendered cell.
type RemoteMouseEvent struct {
	Type       string `json:"type"`
	Button     string `json:"button"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	ScreenX    int    `json:"screenX"`
	ScreenY    int    `json:"screenY"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Shift      bool   `json:"shift"`
	Alt        bool   `json:"alt"`
	Ctrl       bool   `json:"ctrl"`
	WheelDelta int    `json:"wheelDelta,omitempty"`
	ClickCount int    `json:"clickCount,omitempty"`
}

// ViewMouseResult is pi-tui's TuiMouseEventResult for an event the
// components of a view took: Handled is false when none did.
type ViewMouseResult struct {
	Handled, Capture, Focus bool
	// Render is the component's explicit render request, or nil for the
	// default of the event's type.
	Render *bool
}

// RemoteOverlayMouseHost is a [RemoteOverlayHost] whose component may take
// the mouse in fullscreen mode, as an upstream component with handleMouse
// does.
//
// pig-specific: no upstream equivalent; Pi calls handleMouse in-process.
type RemoteOverlayMouseHost interface {
	// OnMouse hands an event, local to the component's first cell, to the
	// components of the overlay's view, as Pi's would take it, and then,
	// when they leave it and the extension's component takes the mouse, to
	// the extension. The extension's answer cannot arrive in time, so a
	// component that takes the mouse handles every event inside its
	// bounds. Called on the owner loop.
	OnMouse(event RemoteMouseEvent) ViewMouseResult
}
