package kit

// CancellableLoader is pi-tui's CancellableLoader(ui, spinnerColorFn,
// messageColorFn, message, indicator): a [Loader] that the focused
// tui.select.cancel key aborts. The host owns its abort state and animation;
// the abort reaches the extension as an [EventAbort] event of the node, which
// [View.Focus] must name to receive the key. Any other key goes to the
// extension, as the loader ignores it.
type CancellableLoader struct {
	// ID names the node; it is required, unique within the view, and
	// [Event.Node] carries it.
	ID string
	Loader
}

// NewCancellableLoader returns a CancellableLoader showing message.
func NewCancellableLoader(id, message string) *CancellableLoader {
	return &CancellableLoader{ID: id, Loader: Loader{Message: message}}
}

func (*CancellableLoader) isNode() {}

// WireNode implements [Wired].
func (c *CancellableLoader) WireNode() WireForm {
	return WireForm{Kind: "cancellable-loader", ID: c.ID, Fields: loaderFields(&c.Loader)}
}

// loaderWire is a loader's wire fields.
type loaderWire struct {
	Message      string         `json:"message"`
	SpinnerColor string         `json:"spinnerColor,omitempty"`
	MessageColor string         `json:"messageColor,omitempty"`
	Indicator    *indicatorWire `json:"indicator,omitempty"`
	Frame        *int           `json:"frame,omitempty"`
	Cancellable  *bool          `json:"cancellable,omitempty"`
}

type indicatorWire struct {
	Frames     *[]string `json:"frames,omitempty"`
	IntervalMs int       `json:"intervalMs,omitempty"`
}

func loaderFields(l *Loader) loaderWire {
	out := loaderWire{Message: l.Message, SpinnerColor: l.SpinnerColor, MessageColor: l.MessageColor, Frame: l.Frame}
	if ind := l.Indicator; ind != nil {
		out.Indicator = &indicatorWire{IntervalMs: ind.IntervalMs}
		if ind.Frames != nil {
			out.Indicator.Frames = &ind.Frames
		}
	}
	return out
}
