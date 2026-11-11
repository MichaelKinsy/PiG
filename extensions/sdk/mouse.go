package sdk

// Mouse event types, pi-tui's TuiMouseEventType.
const (
	MousePress   = "press"
	MouseRelease = "release"
	MouseMove    = "move"
	MouseDrag    = "drag"
	MouseClick   = "click"
	MouseWheel   = "wheel"
)

// Mouse buttons, pi-tui's TuiMouseButton.
const (
	MouseButtonLeft   = "left"
	MouseButtonMiddle = "middle"
	MouseButtonRight  = "right"
	MouseButtonNone   = "none"
)

// MouseEvent is pi-tui's TuiMouseEvent: one cell-based pointer event, with
// zero-based coordinates.
type MouseEvent struct {
	// Type is one of MousePress, MouseRelease, MouseMove, MouseDrag,
	// MouseClick or MouseWheel, and Button one of the MouseButton values
	// (MouseButtonNone for a wheel or a move without a button).
	Type   string `json:"type"`
	Button string `json:"button"`
	// X and Y are the cell relative to the component's first rendered cell.
	X int `json:"x"`
	Y int `json:"y"`
	// ScreenX and ScreenY are the cell on the terminal.
	ScreenX int `json:"screenX"`
	ScreenY int `json:"screenY"`
	// Width and Height are the component's bounds.
	Width  int  `json:"width"`
	Height int  `json:"height"`
	Shift  bool `json:"shift"`
	Alt    bool `json:"alt"`
	Ctrl   bool `json:"ctrl"`
	// WheelDelta is the lines a wheel event scrolls; negative scrolls up.
	WheelDelta int `json:"wheelDelta,omitempty"`
	// ClickCount is 1, 2 or 3 for consecutive clicks on one cell.
	ClickCount int `json:"clickCount,omitempty"`
}

// MouseHandler is implemented by a [RemoteComponent] or [ViewComponent] that
// takes the mouse, as an upstream component with handleMouse does. Pi routes
// the mouse to components only in fullscreen mode; regular mode leaves it to
// the terminal, and so does PiG.
//
// While the component's overlay shows, the host hands it every event inside
// its bounds: press, release, move, drag, click and wheel, in order. A left
// click arrives as press, release, then click. The host answers the terminal
// at once, so the component counts as handling every event (upstream
// handled: true), and text selection does not start over it. For a
// [ViewComponent], the view's components take an event first, as Pi's do (a
// select list row selects on press and fires select on click); HandleMouse
// receives only the events they leave.
//
// HandleMouse runs on the queue HandleInput runs on, and its result means
// what HandleInput's does.
type MouseHandler interface {
	HandleMouse(event MouseEvent) (RemoteComponentResult, error)
}

// ViewportInputCapturer is the optional capturesViewportInput of pi-tui's
// Component: a ui.custom, editor or tui.alt component that implements it
// keeps the alt-screen viewport's scroll keys when it returns true. The SDK
// reads it each time it sends a frame (ui.custom.render, ui.editor.render) and
// each time the host asks (tui.alt); a frame goes out when only this value
// changed. A component without the method reports false.
type ViewportInputCapturer interface {
	CapturesViewportInput() bool
}

// capturesViewportInput is a component's current CapturesViewportInput, false for a component without it.
func capturesViewportInput(component any) bool {
	c, ok := component.(ViewportInputCapturer)
	return ok && c.CapturesViewportInput()
}

const notifyUICustomMouse = "ui.custom.mouse"
