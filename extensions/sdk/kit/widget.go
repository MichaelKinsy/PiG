package kit

// Wired is a widget node: a node whose wire form is a kind, an optional ID,
// children and fields beyond the base set (docs/plan/extension-component-kit.md
// §2.2). The encoder in internal/kitwire writes it without a case of its own.
type Wired interface {
	Node
	// WireNode returns the node's wire form.
	WireNode() WireForm
}

// WireForm is a widget node as the host's ViewNode carries it: Fields is a
// JSON-encodable struct whose members are the node's wire fields.
type WireForm struct {
	Kind     string
	ID       string
	Children []Node
	Fields   any
}

// Cursor is an input's or an editor's cursor: a logical line and a UTF-16
// column (Line 0 for an input).
type Cursor struct {
	Line int `json:"line"`
	Col  int `json:"col"`
}

// Mouse is pi-tui's TuiMouseEvent as a mouse-region took it: X and Y are
// local to the region's first cell, ScreenX and ScreenY on the terminal.
type Mouse struct {
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
