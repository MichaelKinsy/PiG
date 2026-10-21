package subprocess

import "github.com/MichaelKinsy/PiG/coding/extension"

// View node kinds of the pi-tui widgets and coding-agent messages the kit
// carries beyond the first set (D107, docs/plan/extension-component-kit.md
// §2.2). Each mirrors the upstream component of the same name; the host
// renders it with its Go port.
const (
	ViewKindScrollView        = "scroll-view"
	ViewKindMouseRegion       = "mouse-region"
	ViewKindInput             = "input"
	ViewKindEditor            = "editor"
	ViewKindCancellableLoader = "cancellable-loader"
	ViewKindBorderedLoader    = "bordered-loader"
	ViewKindBranchSummary     = "branch-summary"
	ViewKindCompactionSummary = "compaction-summary"
	ViewKindCustomMessage     = "custom-message"
	ViewKindSkillInvocation   = "skill-invocation"
)

// View event types of the widget kinds: Input's and Editor's onSubmit,
// Input's onEscape, CancellableLoader's onAbort, the value an Input or an
// Editor holds after a key (onChange), and the mouse event a MouseRegion
// took.
const (
	ViewEventSubmit = "submit"
	ViewEventEscape = "escape"
	ViewEventAbort  = "abort"
	ViewEventMouse  = "mouse"
)

// ViewWidgetFields are the ViewNode fields of the widget kinds; an omitted
// field takes the upstream default.
type ViewWidgetFields struct {
	// Summary is the markdown of a branch-summary and a compaction-summary;
	// TokensBefore a compaction-summary's context size before compaction.
	Summary      string `json:"summary,omitempty"`
	TokensBefore *int   `json:"tokensBefore,omitempty"`
	// Skill is a skill-invocation's skill block.
	Skill *ViewSkill `json:"skill,omitempty"`
	// CustomType is a custom-message's customType; its content is Text.
	CustomType string `json:"customType,omitempty"`
	// Cancellable is a bordered-loader's `{cancellable}` option (default true).
	Cancellable *bool `json:"cancellable,omitempty"`

	// Value is an input's and an editor's text. Prompt, Placeholder and
	// PlaceholderColor (a foreground token for placeholderStyle) are an
	// input's InputOptions.
	Value            string  `json:"value,omitempty"`
	Prompt           *string `json:"prompt,omitempty"`
	Placeholder      string  `json:"placeholder,omitempty"`
	PlaceholderColor string  `json:"placeholderColor,omitempty"`
	// Cursor and Focused are the state of an input or an editor whose
	// component the sender owns (Node's): the cursor it holds and its
	// Focusable flag, applied each frame when they differ from the frame
	// before. A host-owned widget (the Go, Rust and Python SDKs') keeps its
	// own cursor and takes its focus from the view's Focus, so those SDKs
	// send neither.
	Cursor  *ViewCursor `json:"cursor,omitempty"`
	Focused bool        `json:"focused,omitempty"`

	// AutocompleteMaxVisible, BorderColor (a foreground token for
	// borderColor, default "borderMuted"), DisableSubmit, History (the
	// addToHistory calls, oldest first) and Autocomplete (the
	// CombinedAutocompleteProvider handed to setAutocompleteProvider) are an
	// editor's.
	AutocompleteMaxVisible *int   `json:"autocompleteMaxVisible,omitempty"`
	BorderColor            string `json:"borderColor,omitempty"`
	DisableSubmit          bool   `json:"disableSubmit,omitempty"`
	//portlint:allow emptydrop the D107 view wire is PiG-owned with no Pi counterpart, and an absent and an empty history mean the same: none
	History      []string          `json:"history,omitempty"`
	Autocomplete *ViewAutocomplete `json:"autocomplete,omitempty"`

	// Follow ("none" or "end"), Primary, Overscroll ("chain" or "contain"),
	// Scrollbar ("hidden", "auto" or "always"), ScrollbarTrackColor and
	// ScrollbarThumbColor (foreground tokens for the style closures),
	// ScrollbarHideDelayMs and ScrollTop (state) are a scroll-view's
	// ScrollViewOptions.
	Follow               string `json:"follow,omitempty"`
	Primary              bool   `json:"primary,omitempty"`
	Overscroll           string `json:"overscroll,omitempty"`
	Scrollbar            string `json:"scrollbar,omitempty"`
	ScrollbarTrackColor  string `json:"scrollbarTrackColor,omitempty"`
	ScrollbarThumbColor  string `json:"scrollbarThumbColor,omitempty"`
	ScrollbarHideDelayMs *int   `json:"scrollbarHideDelayMs,omitempty"`
	ScrollTop            *int   `json:"scrollTop,omitempty"`

	// Region is a mouse-region's handler, which cannot cross as a closure.
	Region *ViewMouseRegion `json:"region,omitempty"`
}

// ViewSkill is the part of a ParsedSkillBlock a skill-invocation draws.
type ViewSkill struct {
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
}

// ViewAutocomplete is a CombinedAutocompleteProvider(commands, basePath): the
// slash commands (without argument completion closures) and the directory
// its path completion starts from.
type ViewAutocomplete struct {
	//portlint:allow emptydrop the D107 view wire is PiG-owned with no Pi counterpart, and an absent and an empty command list mean the same: none
	Commands []ViewSlashCommand `json:"commands,omitempty"`
	BasePath string             `json:"basePath,omitempty"`
}

// ViewSlashCommand is a SlashCommand without getArgumentCompletions.
type ViewSlashCommand struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	ArgumentHint string `json:"argumentHint,omitempty"`
}

// ViewMouseRegion is the handler of a mouse-region: onMouse returns this
// result for each event whose type is in Types (every type when none is
// named), and no result (undefined) for the others. The host answers the
// terminal at once with the result and reports each handled event to the
// extension as a "mouse" view event. Handled defaults to true.
type ViewMouseRegion struct {
	//portlint:allow emptydrop the D107 view wire is PiG-owned with no Pi counterpart, and an absent and an empty type list mean the same: every type
	Types   []string `json:"types,omitempty"`
	Handled *bool    `json:"handled,omitempty"`
	Capture bool     `json:"capture,omitempty"`
	Focus   bool     `json:"focus,omitempty"`
	Render  *bool    `json:"render,omitempty"`
}

// ViewCursor is an editor's cursor: a logical line and a UTF-16 column; an
// input's is its Col on line 0.
type ViewCursor struct {
	Line int `json:"line"`
	Col  int `json:"col"`
}

// ViewEventExtras are the ViewEventPayload fields of the widget events.
type ViewEventExtras struct {
	// Cursor is the cursor after a change, submit or escape of an input or editor.
	Cursor *ViewCursor `json:"cursor,omitempty"`
	// Mouse is the event a mouse-region took.
	Mouse *extension.RemoteMouseEvent `json:"mouse,omitempty"`
}
