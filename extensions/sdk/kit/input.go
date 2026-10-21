package kit

// Input is pi-tui's Input({prompt, placeholder, placeholderStyle}): a
// one-line text field. The host owns its value and cursor and runs Input's own
// key and mouse handling once [View.Focus] names it, as the focused component
// of Pi's TUI takes every key. The extension receives Input's callbacks as
// [EventSubmit] (onSubmit) and [EventEscape] (onEscape) events of the node,
// and an [EventChange] with the value and [Event.Cursor] after each key or
// press that changed either, since Pi's author reads them with getValue.
type Input struct {
	// ID names the node; it is required, unique within the view, and
	// [Event.Node] carries it.
	ID string
	// Value is setValue's argument, applied when it differs from the value
	// the previous frame sent; the cursor stays where Input left it, as
	// setValue leaves it.
	Value string
	// Prompt is the options' prompt; nil takes upstream's "> ".
	Prompt *string
	// Placeholder is the text drawn while the value is empty.
	Placeholder string
	// PlaceholderColor is a theme foreground token for placeholderStyle;
	// empty leaves the placeholder unstyled.
	PlaceholderColor string
}

// NewInput returns an empty Input named id with upstream's defaults.
func NewInput(id string) *Input { return &Input{ID: id} }

func (*Input) isNode() {}

type inputFields struct {
	Value            string  `json:"value,omitempty"`
	Prompt           *string `json:"prompt,omitempty"`
	Placeholder      string  `json:"placeholder,omitempty"`
	PlaceholderColor string  `json:"placeholderColor,omitempty"`
}

// WireNode implements [Wired].
func (i *Input) WireNode() WireForm {
	return WireForm{Kind: "input", ID: i.ID, Fields: inputFields{Value: i.Value, Prompt: i.Prompt, Placeholder: i.Placeholder, PlaceholderColor: i.PlaceholderColor}}
}
