package kit

// MouseRegion is pi-tui's MouseRegion(child, onMouse): mouse handling added to
// a child without changing how it draws. The child gets each event first and
// the region's handler only the events it leaves. The handler is a closure
// upstream and crosses as a description: for each event whose type is in
// Types (every type when none is named) it returns the result below, and for
// the others no result (undefined). The host answers the terminal at once and
// reports each event the handler ran for as an [EventMouse] event of the node,
// in [Event.Mouse] with coordinates local to the region.
type MouseRegion struct {
	// ID names the node; it is required, unique within the view, and
	// [Event.Node] carries it.
	ID    string
	Child Node
	// Types are the event types the handler answers: "press", "release",
	// "click", "drag", "move" or "wheel".
	Types []string
	// Handled is the result's handled; nil takes true.
	Handled *bool
	// Capture and Focus are the result's capture and focus.
	Capture bool
	Focus   bool
	// Render is the result's render; nil takes upstream's per-type default.
	Render *bool
}

// NewMouseRegion returns a MouseRegion named id over child that handles every
// event.
func NewMouseRegion(id string, child Node) *MouseRegion { return &MouseRegion{ID: id, Child: child} }

func (*MouseRegion) isNode() {}

type mouseRegionFields struct {
	Region mouseRegionWire `json:"region"`
}

type mouseRegionWire struct {
	Types   []string `json:"types,omitempty"`
	Handled *bool    `json:"handled,omitempty"`
	Capture bool     `json:"capture,omitempty"`
	Focus   bool     `json:"focus,omitempty"`
	Render  *bool    `json:"render,omitempty"`
}

// WireNode implements [Wired].
func (m *MouseRegion) WireNode() WireForm {
	return WireForm{Kind: "mouse-region", ID: m.ID, Children: []Node{m.Child}, Fields: mouseRegionFields{Region: mouseRegionWire{
		Types: m.Types, Handled: m.Handled, Capture: m.Capture, Focus: m.Focus, Render: m.Render,
	}}}
}
