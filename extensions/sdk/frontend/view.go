// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package frontend

import "time"

// View is the component structure of an extension surface's lines (D107):
// what Pi's tui components drew them, as [Lines.View], [Overlay.View] and
// [ToolCard.ResultView] report it. The lines stay authoritative: a session
// that does not draw a view, or one of its node kinds, draws the lines, and
// a session that draws a node natively shows the same information.
type View struct {
	// Root is the component tree. Its Rows are all of the node's lines.
	Root ViewNode
	// Theme holds the extension's overrides of theme tokens for this
	// surface: Pi's token name to "#rrggbb". The session's own theme holds
	// every other token.
	Theme map[string]string
	// Focus is the ID of the [ViewKindSelectList] or [ViewKindSettingsList]
	// that receives keys, or "". [Env.Act] reaches it through
	// [Action.ViewNode].
	Focus string
	// Seq is PiG's revision of the producer's view: two views of one node
	// with the same Seq and root Width are the same view.
	Seq uint64
}

// ViewKind is a closed set of component kinds, each Pi's component of the
// same name, plus [ViewKindLines] for rows a component of its own drew.
type ViewKind string

const (
	ViewKindContainer     ViewKind = "container"
	ViewKindBox           ViewKind = "box"
	ViewKindText          ViewKind = "text"
	ViewKindTruncatedText ViewKind = "truncated-text"
	ViewKindMarkdown      ViewKind = "markdown"
	ViewKindSpacer        ViewKind = "spacer"
	ViewKindDynamicBorder ViewKind = "dynamic-border"
	ViewKindSelectList    ViewKind = "select-list"
	ViewKindSettingsList  ViewKind = "settings-list"
	ViewKindImage         ViewKind = "image"
	ViewKindLoader        ViewKind = "loader"
	ViewKindHStack        ViewKind = "hstack"
	ViewKindVStack        ViewKind = "vstack"
	ViewKindLines         ViewKind = "lines"
	// The conversation kinds are Pi's conversation components: a user
	// message, an assistant message with its thinking, a tool call's card,
	// a `!` command, and a diff Pi's renderDiff colors.
	ViewKindUserMessage      ViewKind = "user-message"
	ViewKindAssistantMessage ViewKind = "assistant-message"
	ViewKindToolExecution    ViewKind = "tool-execution"
	ViewKindBashExecution    ViewKind = "bash-execution"
	ViewKindDiff             ViewKind = "diff"
)

// ViewNode is one component and the lines it drew.
//
// Layout: Rows is the count of lines the node drew and Width the cells it
// was laid out at. The children of a container and of a vstack follow one
// another (a vstack puts Gap empty rows between them); a box's children
// follow one another inside PaddingY rows above and below and PaddingX
// cells on each side; the children of an hstack sit side by side, each
// Width cells wide and Gap cells apart, from the first row, each with its
// own Rows. A stack's Align offsets are not reported.
type ViewNode struct {
	Kind ViewKind
	// ID is the extension's name for the node; always set on lists.
	ID          string
	Rows, Width int
	Children    []ViewNode
	// Stack is the StackEntry options of a child of an hstack or vstack,
	// nil elsewhere.
	Stack *ViewStack
	// Theme holds token overrides for this subtree, where a view combines
	// surfaces with overrides of their own (the dock's widgets); nil
	// elsewhere, where [View.Theme] applies.
	Theme map[string]string

	// Text is the text of text, truncated-text, markdown and
	// user-message, and a loader's message, as the extension gave it (ANSI
	// included).
	Text               string
	PaddingX, PaddingY int
	// Bg is the background token of a box or text, or "".
	Bg string
	// Color is a foreground token: a dynamic border's color, an image's
	// fallback color, a loader's spinner color.
	Color string
	// MessageColor is a loader's message token.
	MessageColor string
	// TextStyle is a markdown node's default text style, or nil.
	TextStyle *ViewTextStyle

	// Items are a list's shown items in order: a select list's filtered
	// items, a settings list's filtered settings. Selected indexes them, or
	// is -1. MaxVisible is the rows the list shows at once.
	Items      []ViewItem
	Selected   int
	MaxVisible int
	// Query is a select list's filter or a settings list's search text;
	// Searchable reports a settings list that takes typed search text.
	Query      string
	Searchable bool
	// Submenu is the open submenu of a settings list, which draws in the
	// list's place, or nil.
	Submenu *ViewNode

	// Image is an image node's image, or the image a lines node's rows
	// depict; nil otherwise.
	Image *ViewImage
	// Progress is the position a lines node's rows show, or nil.
	Progress *ViewProgress
	// List says a lines node's rows are a list, one item per row, or nil.
	List *ViewList

	// Frames, Frame and Interval are a loader's indicator: Frames nil when
	// it shows none, Frame the one it shows now.
	Frames   []string
	Frame    int
	Interval time.Duration

	// Gap and Align are a stack's options ("stretch", "start", "center" or
	// "end").
	Gap   int
	Align string

	// Lines are a lines node's rows.
	Lines []string

	// Transcript are the nodes PiG's main transcript reports for the same
	// component, so a session draws a conversation node as it draws the
	// transcript: a user message's [MarkdownText], an assistant message's
	// [MarkdownText] and [Thinking] parts and its error line as [Lines], a
	// tool call's [ToolCard]. A bash-execution node has none: the
	// transcript reports a `!` command as lines. They are shared with PiG
	// and must not be modified.
	Transcript []Node
	// Diff is a diff node's diff, Path its file path or "".
	Diff *Diff
}

// ViewTextStyle is a markdown node's default text style: foreground and
// background tokens and attributes.
type ViewTextStyle struct {
	Color, BgColor                         string
	Bold, Italic, Strikethrough, Underline bool
}

// ViewItem is a select list item (Value, Label, Description) or a setting
// (ID, Label, Description, CurrentValue, Values, Submenu).
type ViewItem struct {
	Value        string
	ID           string
	Label        string
	Description  string
	CurrentValue string
	// Values are the values the confirm key cycles through.
	Values []string
	// Submenu reports a setting that opens a submenu.
	Submenu bool
}

// ViewImage is an image's bytes. Data is shared with PiG and must not be
// modified.
type ViewImage struct {
	// Ref is the lowercase hex SHA-256 of Data.
	Ref      string
	MimeType string
	Data     []byte
	Filename string
	// MaxWidthCells and MaxHeightCells bound the image, or are 0.
	MaxWidthCells, MaxHeightCells int
}

// ViewProgress is a position in a range.
type ViewProgress struct {
	Value, Max float64
}

// ViewList is a list a lines node's rows show: Items[i] is row i. Selected
// is the highlighted item, or -1. A session that draws it natively shows
// each item's label, detail and columns and the selection.
type ViewList struct {
	Items    []ViewListItem
	Selected int
}

// ViewListItem is one row of a [ViewList]: its primary text, a secondary
// text or "", and the further cells of a table row.
type ViewListItem struct {
	Label, Detail string
	Columns       []string
}

// ViewStack is a stack child's StackEntry options with upstream's defaults
// applied: Basis -1 is "auto" (the child's own size), MaxSize -1 is no
// maximum, Grow defaults to 0 and Shrink to 1.
type ViewStack struct {
	Basis, Grow, Shrink, MinSize, MaxSize int
}
