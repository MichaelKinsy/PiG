package kit

// BorderedLoader is coding-agent's BorderedLoader(tui, theme, message,
// {cancellable}): a loader between two borders with, when cancellable (the
// default), a cancel key hint that the focused tui.select.cancel key aborts
// ([EventAbort]). It draws with the view's theme, the one Pi hands the factory
// that builds it. The host owns its abort state and animation.
type BorderedLoader struct {
	// ID names the node; it is required, unique within the view, and
	// [Event.Node] carries it.
	ID      string
	Message string
	// Cancellable is upstream's `{cancellable}` option; NewBorderedLoader
	// starts with its default, true.
	Cancellable bool
	// Frame is the spinner's upstream currentFrame; nil starts at 0.
	Frame *int
}

// NewBorderedLoader returns a cancellable BorderedLoader showing message.
func NewBorderedLoader(id, message string) *BorderedLoader {
	return &BorderedLoader{ID: id, Message: message, Cancellable: true}
}

func (*BorderedLoader) isNode() {}

// WireNode implements [Wired].
func (b *BorderedLoader) WireNode() WireForm {
	fields := loaderWire{Message: b.Message, Frame: b.Frame}
	if !b.Cancellable {
		off := false
		fields.Cancellable = &off
	}
	return WireForm{Kind: "bordered-loader", ID: b.ID, Fields: fields}
}
