package kit

// Editor is pi-tui's Editor(tui, theme, {paddingX, autocompleteMaxVisible})
// with the setters an author calls on it: a multi-line text editor between two
// borders. The host owns the text and the cursor and runs Editor's own key and
// mouse handling once [View.Focus] names it. The extension receives
// onSubmit as an [EventSubmit] event of the node (the text in [Event.Value])
// and an [EventChange] with the text and [Event.Cursor] after each key or
// press that changed either. The editor shows max(5, floor(terminal rows *
// 0.3)) lines, the terminal's rows being the host's.
type Editor struct {
	// ID names the node; it is required, unique within the view, and
	// [Event.Node] carries it.
	ID string
	// Value is setText's argument, applied when it differs from the value
	// the previous frame sent.
	Value string
	// PaddingX is the horizontal padding (the options' paddingX).
	PaddingX int
	// AutocompleteMaxVisible is the autocomplete row limit (the options'
	// autocompleteMaxVisible, clamped to 3..20); 0 takes upstream's 5.
	AutocompleteMaxVisible int
	// BorderColor is a theme foreground token for borderColor; empty takes
	// upstream's "borderMuted".
	BorderColor string
	// DisableSubmit is upstream's disableSubmit field.
	DisableSubmit bool
	// History is the addToHistory calls, oldest first. A frame that extends
	// the history the previous frame sent adds the new entries; another
	// history is another editor.
	History []string
	// Autocomplete is the provider setAutocompleteProvider received; nil
	// leaves the editor without one.
	Autocomplete *Autocomplete
}

// NewEditor returns an empty Editor named id with upstream's defaults.
func NewEditor(id string) *Editor { return &Editor{ID: id} }

func (*Editor) isNode() {}

// SlashCommand is pi-tui's SlashCommand without getArgumentCompletions, which
// is a closure and cannot cross.
type SlashCommand struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	ArgumentHint string `json:"argumentHint,omitempty"`
}

// Autocomplete is pi-tui's CombinedAutocompleteProvider(commands, basePath): the
// slash commands and the directory its path completion starts from (the host's
// working directory when empty). The host runs its getSuggestions,
// applyCompletion and shouldTriggerFileCompletion as the editor asks.
type Autocomplete struct {
	Commands []SlashCommand `json:"commands,omitempty"`
	BasePath string         `json:"basePath,omitempty"`
}

type editorFields struct {
	Value                  string        `json:"value,omitempty"`
	PaddingX               *int          `json:"paddingX,omitempty"`
	AutocompleteMaxVisible *int          `json:"autocompleteMaxVisible,omitempty"`
	BorderColor            string        `json:"borderColor,omitempty"`
	DisableSubmit          bool          `json:"disableSubmit,omitempty"`
	History                []string      `json:"history,omitempty"`
	Autocomplete           *Autocomplete `json:"autocomplete,omitempty"`
}

// WireNode implements [Wired].
func (e *Editor) WireNode() WireForm {
	fields := editorFields{Value: e.Value, BorderColor: e.BorderColor, DisableSubmit: e.DisableSubmit, History: e.History, Autocomplete: e.Autocomplete}
	if e.PaddingX != 0 {
		fields.PaddingX = &e.PaddingX
	}
	if e.AutocompleteMaxVisible != 0 {
		fields.AutocompleteMaxVisible = &e.AutocompleteMaxVisible
	}
	return WireForm{Kind: "editor", ID: e.ID, Fields: fields}
}
