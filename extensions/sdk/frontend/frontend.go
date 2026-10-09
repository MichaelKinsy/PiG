// SPDX-License-Identifier: MIT

// Package frontend is the contract between PiG's interactive mode and a
// Piglet frontend member that draws it in place of the ANSI renderer (D91).
//
// A frontend member is a Go package compiled into a Piglet Binary. It is not
// part of the extension API: it has no wire protocol and no subprocess
// realization, because it owns the terminal's output and reads terminal input
// on the interactive owner loop.
//
// PiG keeps building Pi's component tree. Each frame, it reports what changed
// as retained-tree ops keyed by stable ids: transcript entries in [RegionMain],
// the input dock in [RegionDock] and overlays in [RegionOverlay]. Tool calls
// arrive as [ToolCard] nodes, messages as [MarkdownText] and [Thinking]
// nodes, the input editor as an [Editor] node, a selector or dialog in its
// place as a [Selector] or [Settings] node, the working indicator as a
// [Working] node, the footer as a [Footer] node and an overlay as an
// [Overlay] node; every other component arrives as [Lines], the ANSI lines
// the component rendered. A session acts on the editor through [Env]'s Edit,
// Undo and Send, on a selector through its Act, and on a node's Click area
// through its Click. A [ScreenSession] also shows fullscreen overlays.
package frontend

import (
	"io"
	"time"
)

// Frontend opens a frontend session for one interactive run.
type Frontend interface {
	// Open runs before the first paint, before PiG reads terminal input. A nil
	// Session with a nil error means the frontend does not apply here, and PiG
	// paints with its ANSI renderer.
	Open(env Env) (Session, error)
}

// Env describes the terminal a session draws on.
type Env struct {
	// Out writes to the terminal, from Open, the Session methods or the
	// session's own goroutines, until Close returns. Each Write reaches the
	// terminal whole, never interleaved with another Write or with PiG's own
	// terminal writes (see Session.HandleInput), so a message the terminal
	// must read whole goes out in one Write.
	Out io.Writer
	// Columns is the terminal width in cells.
	Columns int
	// Getenv reads the process environment.
	Getenv func(key string) string
	// AppName and Version identify the running application.
	AppName, Version string
	// Fallback asks PiG to stop the session and continue with its ANSI
	// renderer. It may be called from any goroutine; PiG closes the session
	// on its owner loop and repaints the whole tree.
	Fallback func(reason string)

	// Edit, Undo and Send act on the input editor that the dock reports as
	// an [Editor] node. Like Fallback, they may be called from any goroutine
	// and never block. PiG runs them on its owner loop in the order they
	// were called; one called from Session.HandleInput runs before PiG
	// handles the next input sequence. After Close they do nothing.

	// Edit applies one native edit to the editor's text, unless the text has
	// changed since the edit was computed: PiG ignores an edit whose Len is
	// not the length of the editor's current text.
	Edit func(edit Edit)
	// Undo reverts the editor's last change through its own history, where
	// each Edit is one step. Nothing happens when there is none.
	Undo func()
	// Send submits text through the path Enter takes, as if it were typed
	// into an empty editor: a slash command runs, multi-line text stays one
	// prompt, and while the agent works the prompt steers it. The editor's
	// draft stays unless that path sets the editor itself, as queueing a
	// prompt during compaction clears it. A session sends only while the
	// editor reports Sendable.
	Send func(text string)

	// Act acts on the [Selector] or [Settings] node with id Action.Node as
	// the keys it stands for. Like Edit, it may be called from any goroutine
	// and never blocks, and after Close it does nothing. PiG feeds the keys
	// through its terminal input path, after the input it has already read,
	// to whatever has the keys, so the selector handles them exactly as
	// typed keys: the same results, side effects and live previews. It works
	// a step at a time against the node as it is at that moment, and stops
	// as soon as the node is no longer shown, so an action that closes the
	// selector or opens another one goes no further. An action naming a
	// node, item, tab or value that is not shown does nothing. Keys typed
	// meanwhile still reach the selector.
	Act func(action Action)

	// Click clicks a cell of a node's Click area as a terminal click would:
	// PiG hands the click to the component that drew the node, which acts
	// as it does on a click there, such as the header's logo playing its
	// animation or a fullscreen overlay closing. Like Act, it may be called
	// from any goroutine and never blocks, and after Close it does nothing.
	// A click on a node that no longer shows, or outside its Click area,
	// does nothing.
	//
	// pig additive (D91): a click on an extension's view, an overlay's or
	// a dock node's, reaches the extension's component as the terminal's
	// left click at that cell would in Pi's fullscreen mode: a press, then
	// the release and the click to the component that took the press, or,
	// when none took it, the release to the component under the pointer and
	// the click when it leaves that too. The component receives the
	// coordinates it would in a terminal, and the clickCount Pi counts
	// (see [Click.Count]). While the run is not in fullscreen mode, where a
	// terminal reports no mouse, it does nothing.
	Click func(click Click)
}

// ActionKind is what an [Action] does.
type ActionKind string

const (
	// Highlight moves the selection to Item with the selection keys (up and
	// down), as Selected reports it afterwards. It does not choose it.
	Highlight ActionKind = "highlight"
	// Choose highlights Item, or keeps the selection when Item is "", and
	// then presses the confirm key (Enter): a selector picks it, a list that
	// toggles entries toggles it, a setting cycles to its next value or
	// opens its submenu, and a [Selector] asking Confirm answers yes.
	Choose ActionKind = "choose"
	// Dismiss presses the cancel key (Escape). Most selectors close; a
	// selector with a filter of its own may clear it first, and a Confirm
	// question is answered no.
	Dismiss ActionKind = "dismiss"
	// Filter makes Value the search text: it erases the end of the query
	// that differs (Backspace) and types the rest. Only for a searchable
	// node.
	Filter ActionKind = "filter"
	// SwitchTab presses the tab key until Item, the ID of one of the
	// [Selector]'s Tabs, shows.
	SwitchTab ActionKind = "tab"
	// SetValue highlights the [Setting] Item and presses the confirm key
	// until it shows Value, one of its Values. A setting with a submenu, or
	// that opens one, has no value to set: Choose opens it instead.
	SetValue ActionKind = "set"
)

// Action is a request on a selector node, performed as keys.
type Action struct {
	// Node is the id of the [Selector] or [Settings] node, or of the node
	// whose [View] holds ViewNode.
	Node string
	// ViewNode is the ID of the focused list of Node's [View] (D107), or "".
	// Item is then the select list item's Value or the setting's ID.
	ViewNode string
	Kind     ActionKind
	// Item is a [SelectorItem], [Setting] or [SelectorTab] ID.
	Item string
	// Value is Filter's query or SetValue's value.
	Value string
}

// Edit replaces the UTF-16 code units [From, To) of the editor's text with
// Text, as one undo step, then puts the cursor at Cursor, a UTF-16 offset
// into the result. From == To with an empty Text only moves the cursor.
type Edit struct {
	From, To int
	Text     string
	Cursor   int
	// Len is the length in UTF-16 code units of the text the edit was
	// computed against.
	Len int
}

// Click is a click on cell Row, Column of the lines of node Node, counted
// from 0. A session that does not know which cell was clicked names the
// first cell of the node's Click area.
type Click struct {
	Node        string
	Row, Column int
	// ViewPath names a node of Node's [View] (D107) by the index of each
	// child from the root, the root itself when empty but not nil. Row
	// and Column then count from that view node's first cell, within its
	// Rows and Width: a session that draws the node natively names its
	// first cell, a lines node's [ViewList] row i as Row i, or a cell of a
	// [ViewProgress] in proportion. Nil: Row and Column count in Node's
	// lines.
	ViewPath []int
	// Item names an item of the select-list (its Value) or settings-list
	// (its ID) at ViewPath; PiG clicks the row it shows on, after wheeling
	// the list until the item is in view, as a terminal user would, and
	// Row is unused.
	Item string
	// Count is the click's place in a double or triple click as the
	// session saw it, such as 2 for the activate a native list sends after
	// a double click's second click, or 0. PiG counts a click on an
	// extension's view as Pi counts a terminal's: the click on the cell
	// and component of the one before it, within Pi's double-click
	// interval, is its second, then its third. A click with Count reaches
	// the component with that clickCount unless the click PiG counted
	// last at that cell already reached it: that was this click.
	Count int
}

// Session draws one interactive run. PiG calls every method on its owner
// loop, never concurrently; a method must not block on terminal input.
//
// A session is the terminal PiG's interactive mode reports its program
// status to (OSC 7501). A session that implements [ProgramStatusSession]
// receives every report through ProgramStatus, on the owner loop, in the
// order the status changed, never while suspended or after Close; one
// that does not receives none. Neither way does PiG write a status report
// or the OSC 7501 support query to the terminal while a session draws.
type Session interface {
	// InputReady reports that PiG's input loop is running: from now on,
	// answers to what the session wrote in Open reach HandleInput without a
	// startup delay. A session that waits for an answer starts its timeout
	// here. PiG calls it once, after the first Apply.
	InputReady()
	// Columns reports how many terminal columns the session lays out in the
	// main and dock regions. PiG renders each region's lines at that width,
	// capped at the terminal's; zero or less means the terminal's width. PiG
	// reads it for every frame and after HandleInput takes a sequence, so a
	// change from a resize event repaints.
	Columns() (main, dock int)
	// Apply draws one frame.
	Apply(frame Frame) error
	// HandleInput is offered every terminal input sequence before key
	// handling. It reports whether the sequence belonged to the frontend.
	// PiG reads the terminal's colors and appearance while a session draws,
	// as it does with its ANSI renderer: it writes the terminal color query
	// (OSC 10, OSC 11 and OSC 4 for ANSI colors 0-15, then a DA1 request)
	// to Env.Out, and asks for light/dark reports (CSI ? 2031 h) while its
	// theme follows the terminal. Their replies, the DA1 reply included, and
	// the reports (CSI ? 997 ; 1 n or 2 n) are PiG's: a session reports them
	// as not its own.
	HandleInput(data string) bool
	// Suspend reports that PiG lends the terminal to another program, such
	// as an external editor or, after a job-control stop, the shell, and
	// stops drawing until Resume. PiG calls it while the terminal is still
	// raw and before it stops reading input. Until Resume, PiG calls no
	// other method but Close and offers no input, and the session writes
	// nothing to the terminal, which the other program owns. Input PiG has
	// read waits for Resume, but what the terminal sends after Suspend
	// returns reaches the other program, so a session that hears from the
	// terminal asks it to stop before Suspend returns.
	Suspend()
	// Resume reports that PiG has the terminal back in raw mode and reads
	// input again. Frames continue from the last one before Suspend: the
	// next Apply carries only what changed meanwhile.
	Resume()
	// Close ends the session. PiG discards the terminal input that arrives
	// after Close, so answers still in flight never reach the shell.
	Close() error
}

// ScreenSession is a [Session] that can show a fullscreen [Overlay] over
// its whole view. PiG then plays the animations its fullscreen mode plays
// over the screen, such as the easter eggs of the header's logo and
// /pigsayhi, as fullscreen overlays; for any other session they do what
// they do outside fullscreen mode.
type ScreenSession interface {
	Session
	// Screen reports the screen a fullscreen overlay covers now, and false
	// while the session cannot show one. PiG calls it on its owner loop,
	// on every frame of an animation.
	Screen() (Screen, bool)
}

// ProgramStatusSession is a [Session] that shows what PiG is doing: Pi's
// program status (OSC 7501, Program Status Protocol), which PiG otherwise
// reports to a terminal that answers its support query. The session is the
// terminal here, so PiG sends no query and reports every change.
type ProgramStatusSession interface {
	Session
	// ProgramStatus receives one status report as Pi writes it to a
	// terminal: ESC ] 7501 ; state=S[:app=A][:kind=K][:msg=M] ESC \, where
	// S is idle, working, blocked, done or error, or clear when the status
	// is removed. A blocked report's kind is permission, question or auth,
	// and msg is a base64 line of text, such as a dialog's title or the
	// first line of an error.
	//
	// PiG calls it on its owner loop, the loop that also handles terminal
	// input and agent events, each time the status changes: an agent run
	// or compaction starts (working), a dialog or login waits for the user
	// (blocked) or closes, and a run settles (done, error or idle). Calls
	// arrive in the order the status changed, and the same report never
	// twice in a row. Before Suspend and before Close, PiG reports clear
	// if a status shows; after Resume, it reports the latest status again.
	// It never calls it while suspended or after Close. ProgramStatus must
	// return without blocking, as a frame's Apply does: a session that
	// writes to the terminal writes the report as it writes a frame.
	ProgramStatus(report string)
}

// Screen is the view a fullscreen [Overlay] covers.
type Screen struct {
	// Columns and Rows are its size in cells. Zero or less counts from the
	// terminal's size: Rows -2 is two rows fewer than the terminal has.
	Columns, Rows int
	// MainRow and MainColumn are the cell where the session shows the first
	// line of the main region while it shows the region from its start,
	// and DockColumn the column where it shows the dock's lines, at the
	// bottom. PiG draws the screen as the session shows it from them, for
	// an animation that starts from the screen, as the logo's dissolves it.
	MainRow, MainColumn, DockColumn int
	// ShowsDock reports that the session shows the dock itself under a
	// fullscreen overlay, as it shows it outside one: Rows leaves the dock
	// out, PiG draws the screen without the dock's lines, and the dock
	// keeps its nodes while the overlay shows. The overlay takes the keys,
	// so the [Editor] reports Sendable false meanwhile.
	ShowsDock bool
	// Hidden reports that the view cannot be seen now, such as while
	// another tab shows: PiG pauses an animation until it shows again.
	Hidden bool
	// ReduceMotion reports that the user asked for reduced motion: PiG
	// starts no fullscreen animation and does what it does outside
	// fullscreen mode.
	ReduceMotion bool
}

// Frame is the change since the previous frame. The first frame inserts the
// whole tree.
type Frame struct {
	Ops []Op
	// SessionFile is the file the session persists to, as Pi's
	// sessionManager.getSessionFile reports it, or "" for a session that is
	// not persisted (--no-session). The file need not exist yet. The first
	// frame carries it, and later frames only when it changed, such as after
	// /new, /resume, /fork or /clone; nil means it is unchanged. A frame may
	// carry it and no ops.
	SessionFile *string
	// Theme is the theme PiG draws with. The first frame carries it, and
	// later frames only when its colors changed, such as after a theme
	// switch in /settings, when the terminal switches between light and dark
	// under a theme that follows it (a light/dark pair or the system
	// theme), or when the terminal's colors change those of the theme; nil
	// means it is unchanged. A frame may carry it and no ops.
	Theme *Theme
	// Mark is the application's mark, for a frontend that shows a brand of
	// its own: PiG's is the pig head of its active sprite, the pixels its
	// startup header draws. The first frame carries it, and later frames only
	// when it changed, such as after /sprite picks another sprite, an
	// extension registers or unregisters the sprite PiG shows, or a theme
	// switch changes the colors PiG draws it in; nil means it is unchanged. A
	// frame may carry it and no ops. A zero Mark is no mark.
	Mark *Mark
}

// Mark is pixel art: a grid of Width by Height pixels, of which Pixels are
// opaque and the rest transparent.
type Mark struct {
	Width, Height int
	// Pixels are the opaque pixels in row-major order.
	Pixels []MarkPixel
}

// MarkPixel is one opaque pixel of a [Mark]: its column X and row Y,
// counted from 0 at the top left, and its color as "#rrggbb", as PiG draws
// it in the color mode of its theme: in truecolor the pixel's own color, in
// 256-color mode the xterm color of the palette index PiG picks for it.
type MarkPixel struct {
	X, Y  int
	Color string
}

// Theme is the palette of the theme PiG draws with, for a frontend that
// draws in colors of its own.
type Theme struct {
	// Name is the theme's name, such as "dark", "light" or a custom theme's.
	Name string
	// Dark reports that the theme is designed for a dark background.
	Dark bool
	// Colors maps each color token the theme sets to "#rrggbb", under Pi's
	// token names (accent, toolSuccessBg, mdHeading, syntaxKeyword, ...),
	// its export colors (pageBg, cardBg, infoBg) included. A token PiG draws
	// faint is mixed toward the terminal's background, as PiG resolves it
	// for export. A token the theme leaves at the terminal's default color,
	// or sets to one of the terminal's sixteen ANSI colors, is absent: it
	// follows the terminal's own palette.
	Colors map[string]string
}

// Region is where a node sits.
type Region string

const (
	// RegionMain is the transcript: header, loaded resources, messages and
	// tools.
	RegionMain Region = "main"
	// RegionDock is the input area under the transcript: pending messages,
	// status, widgets, the editor and the footer.
	RegionDock Region = "dock"
	// RegionOverlay holds the overlays PiG draws over the layout, such as an
	// extension's custom component shown as an overlay, each an [Overlay]
	// node, from the bottom to the top one.
	RegionOverlay Region = "overlay"
)

// OpKind is the kind of change an op makes.
type OpKind string

const (
	// Insert adds Node with id ID at Index in Region.
	Insert OpKind = "insert"
	// Update replaces the node with id ID. Index is its current position.
	Update OpKind = "update"
	// Remove deletes the node with id ID.
	Remove OpKind = "remove"
)

// Op is one change to the retained tree. Ops apply in order; Index counts the
// region's nodes after every earlier op of the frame.
type Op struct {
	Kind   OpKind
	Region Region
	ID     string
	Index  int
	// Node is nil for Remove.
	Node Node
}

// Node is a closed set of node kinds: [Lines], [ToolCard], [MarkdownText],
// [Thinking], [Editor], [Selector], [Settings], [Working], [Footer] and
// [Overlay].
type Node interface{ frontendNode() }

// Lines is a component drawn as ANSI lines.
type Lines struct {
	Lines []string
	// Click is the part of the lines that takes a click, such as the
	// header's logo, or nil. A session reports a click there with
	// Env.Click. PiG reports it on [RegionMain] nodes.
	Click *Area
	// View is the structure of the lines when an extension's components
	// drew them (D107), or nil.
	View *View
}

// Area is a rectangle of cells in a node's lines: Rows lines from line Row
// and Columns cells from cell Column, both counted from 0.
type Area struct {
	Row, Column, Rows, Columns int
}

// Editor is the input editor, with id "editor" in the dock. PiG reports it
// while the editor is mounted in the dock and no overlay that takes the keys
// shows, a fullscreen overlay over a [Screen] that ShowsDock aside. When a
// selector or dialog takes the editor's place and PiG models
// it, the dock reports a [Selector] or [Settings] node there instead.
// Otherwise the whole dock arrives as one [Lines] node. The dock is then, in
// order: a [Lines] node "dock" for the components above the editor, a
// [Working] node "working" while a working indicator shows, the editor (or
// the selector), a [Lines] node "dock.below" for its completion list and the
// components below it, and a [Footer] node "footer" while PiG's footer shows.
// The editor's borders are not drawn: the frontend frames the editor itself.
type Editor struct {
	// Text is the editor's text, lines joined by "\n". A large paste
	// appears as its "[paste #N ...]" marker, as PiG draws it.
	Text string
	// Cursor is the cursor's offset in Text in UTF-16 code units.
	Cursor int
	// Placeholder is the hint to show while Text is empty. Pi's editor
	// draws none, so stock PiG reports it empty.
	Placeholder string
	// Sendable reports that Send submits now: PiG's input loop is running
	// and the editor has the keys, with no dialog or external editor
	// holding them and no exit requested. A frontend should send only
	// while it is true.
	Sendable bool
}

// Selector is a list to pick from that takes the input editor's place in the
// dock: a selector such as the model, thinking level, session, tree, fork or
// login provider picker, a submenu of the settings, or an extension's select
// or confirm dialog. Its id starts with "selector." and is new for each
// selector shown; a [Settings] node keeps its id while it shows a submenu as
// a Selector. Keys reach the selector as before, and a session drives it with
// Env.Act. All text is plain.
type Selector struct {
	// Title is the heading, and Description the text under it, such as a
	// confirm dialog's message; either may be "".
	Title, Description string
	// Searchable reports that typed text filters the list, and Query is
	// that text.
	Searchable bool
	Query      string
	// Items are the entries shown now, after filtering, in the order the
	// selection keys step through them.
	Items []SelectorItem
	// Selected is the ID of the highlighted item, or "" when none is.
	Selected string
	// Tabs are the views the tab key steps through, in its order, such as
	// the scopes of the model and session lists or the tree's filters; Tab
	// is the ID of the one showing. A selector without views has none.
	Tabs []SelectorTab
	Tab  string
	// Status is a message about the list, such as an error, a refresh
	// result or the sort order, or "".
	Status string
	// Loading reports that the items are still being loaded.
	Loading bool
	// Confirm is a question the selector asks about the selected item
	// before acting on it, such as deleting a session, or "". While it is
	// asked, the confirm key answers yes and the cancel key no.
	Confirm string
}

// SelectorItem is one entry of a [Selector].
type SelectorItem struct {
	// ID identifies the item while the selector shows, across filtering.
	ID     string
	Label  string
	Detail string
	// Checked marks the item with a check: the current choice, such as the
	// model in use, or an entry a list that toggles entries has enabled.
	Checked bool
	// Depth is the item's nesting in a tree, 0 at the top.
	Depth int
}

// SelectorTab is one view of a [Selector].
type SelectorTab struct {
	ID, Label string
}

// Settings is a list of settings that takes the input editor's place in the
// dock, such as /settings. Choosing a setting cycles its value, or opens its
// submenu, which the node then reports as a [Selector] under the same id
// until it closes. Its id starts with "selector.". All text is plain.
type Settings struct {
	// Searchable reports that typed text filters the list, and Query is
	// that text.
	Searchable bool
	Query      string
	// Items are the settings shown now, after filtering, in the order the
	// selection keys step through them.
	Items []Setting
	// Selected is the ID of the highlighted setting, or "" when none is.
	Selected string
}

// Setting is one row of [Settings].
type Setting struct {
	ID, Label, Description string
	// Value is the value the row shows.
	Value string
	// Values are the values the confirm key cycles through, in order.
	Values []string
	// Submenu reports that choosing the setting opens a selector instead of
	// cycling Values.
	Submenu bool
}

// Overlay is a component PiG draws over the layout, in [RegionOverlay], with
// an id that starts with "overlay." and stays while it shows. A component
// PiG does not model, such as an extension's custom component, arrives as
// its ANSI lines.
type Overlay struct {
	// Lines are the lines the component renders at Width terminal cells.
	Lines []string
	Width int
	// Anchor is where Pi places the overlay: "center", "top-left",
	// "top-center", "top-right", "left-center", "right-center",
	// "bottom-left", "bottom-center" or "bottom-right".
	Anchor string
	// NonCapturing reports an overlay that does not take the keys; the keys
	// of any other overlay reach PiG, which hands them to the overlay.
	NonCapturing bool
	// Fullscreen reports an overlay that covers the whole screen, as PiG's
	// fullscreen mode draws an animation over everything: its Lines are one
	// per row of the screen that [ScreenSession.Screen] reported, Width
	// cells wide, and the session shows them over its whole view, with
	// nothing else visible, until the node is removed. PiG reports one only
	// to a [ScreenSession].
	Fullscreen bool
	// Click is the part of the lines that takes a click, or nil: all of a
	// fullscreen overlay whose component takes clicks. A session reports a
	// click there with Env.Click.
	Click *Area
	// View is the structure of the lines when an extension's components
	// drew them (D107), or nil.
	View *View
}

// Role is who wrote a message.
type Role string

const (
	// RoleUser is a message the user sent.
	RoleUser Role = "user"
	// RoleAssistant is the model's reply text.
	RoleAssistant Role = "assistant"
)

// MarkdownText is message text as its Markdown source, for the frontend to
// draw. One assistant message is a run of nodes in transcript order, with
// ids that share the message's prefix: its text blocks and its other parts,
// such as an error line drawn as [Lines].
type MarkdownText struct {
	Role Role
	// Text is the source, without leading and trailing whitespace for an
	// assistant's text block. While Streaming, a later frame's Text usually
	// starts with this one, so a frontend can append the difference.
	Text string
	// Streaming reports that the text is still arriving: the node is the
	// last part of the assistant message being generated.
	Streaming bool
	// Images are the images a user message carries, such as a pasted
	// screenshot, in order; nil for an assistant's text. PiG's terminal
	// renderer does not draw them, as Pi's UserMessageComponent shows only
	// the text; a frontend may show them with the message. They are shared
	// with PiG and must not be modified.
	Images []ViewImage
}

// Thinking is one run of an assistant message's thinking blocks, a part of
// the message like its [MarkdownText] blocks.
type Thinking struct {
	// Text is the run's Markdown source: its blocks without leading and
	// trailing whitespace, joined by a blank line.
	Text string
	// Hidden reports that the user hid thinking (the thinking toggle, or a
	// click on this run): PiG then draws only a "Thinking..." label.
	Hidden bool
	// Streaming reports that the run is still arriving: it is the last part
	// of the assistant message being generated.
	Streaming bool
}

// ToolStatus is a tool call's lifecycle state.
type ToolStatus string

const (
	// ToolPending is a call whose arguments are still streaming or that has
	// not started executing.
	ToolPending ToolStatus = "pending"
	// ToolRunning is a call that is executing.
	ToolRunning ToolStatus = "running"
	// ToolDone is a call that finished.
	ToolDone ToolStatus = "done"
	// ToolError is a call that failed.
	ToolError ToolStatus = "error"
	// ToolCancelled is a call that ended because the user aborted its turn,
	// including a call that reported the abort as its error.
	ToolCancelled ToolStatus = "cancelled"
)

// ToolCard is one tool call.
type ToolCard struct {
	// Name is the tool name.
	Name string
	// Arguments is the call's argument object, nil while its JSON is still
	// incomplete. It is shared with PiG and must not be modified.
	Arguments map[string]any
	// Header is PiG's one-line call header as plain text, such as
	// "$ ls -la" or "read src/main.go".
	Header string
	Status ToolStatus
	// Output is the result's plain text so far.
	Output string
	// Elapsed is the finished call's duration, or zero.
	Elapsed time.Duration
	// Expanded reports whether the user expanded the tool output.
	Expanded bool
	// Result holds the result as the tool's definition renders it, for a
	// tool drawn through registered renderers; nil otherwise. The call's
	// rendering is not sent: a frontend draws the call from Arguments.
	Result []string
	// ResultView is the structure of Result when the definition's renderer
	// returned an extension's components (D107), or nil.
	ResultView *View
	// Diff is the unified diff a finished call's result carries, such as an
	// edit's patch; nil otherwise. A frontend can draw it in place of Output
	// and Result, which show the same change.
	Diff *Diff
	// Images are the result's images that PiG shows after the output, in
	// order; nil when the result has none or the user turned images off
	// (the terminal.showImages setting). An image's MaxWidthCells is the
	// width PiG draws it at: terminal.imageWidthCells, at most the card's
	// width less 2 and at least 1. PiG's terminal renderer draws them only on a terminal
	// with an image protocol; a frontend draws them itself, and in place of
	// one it cannot draw shows Pi's text, "[Image: [<mime>] <w>x<h>]". They
	// are shared with PiG and must not be modified.
	Images []ViewImage
}

// Diff is a unified diff in a [ToolCard]: optional file headers, then
// "@@ -a,b +c,d @@" hunks of lines that start with "+", "-" or a space.
type Diff struct {
	// Path is the file the call named, which picks the diff's grammar.
	Path string
	Text string
}

// WorkingKind is what a working indicator reports.
type WorkingKind string

const (
	// WorkingAgent is the agent working on a turn.
	WorkingAgent WorkingKind = "working"
	// WorkingCompaction is a manual or automatic compaction.
	WorkingCompaction WorkingKind = "compaction"
	// WorkingBranchSummary is the summary of a branch the user leaves.
	WorkingBranchSummary WorkingKind = "branchSummary"
	// WorkingRetry is the countdown to retrying a failed request.
	WorkingRetry WorkingKind = "retry"
)

// Working is the indicator PiG shows while something runs, which its
// terminal renderer draws in the editor's top border: a spinner and a
// message. The spinner's frame is not reported; the frontend animates it.
type Working struct {
	Kind WorkingKind
	// Message is the text after the spinner, as PiG draws it: "Working" or
	// an extension's working message, a compaction or branch summary label,
	// or the retry countdown with the seconds left. It may carry ANSI styling
	// from an extension.
	Message string
	// Frames are the spinner frames an extension chose, which may carry
	// ANSI styling. Nil means PiG's default spinner, the Braille frames
	// "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"; an empty slice means no spinner.
	Frames []string
	// Interval is how long each frame shows.
	Interval time.Duration
}

// Footer is the session summary that PiG's terminal renderer draws as
// the footer under the editor. PiG reports it while the footer shows: an
// extension's own footer replaces it, and then arrives in "dock.below".
type Footer struct {
	// Cwd is the session's working directory, with the home directory
	// abbreviated to "~".
	Cwd string
	// GitBranch is the branch checked out in Cwd's repository, "detached"
	// for a detached HEAD, or "" outside a repository.
	GitBranch string
	// SessionName is the name the user gave the session, or "".
	SessionName string
	// UsageTotals sums the usage of every entry of the session file.
	UsageTotals UsageTotals
	// LatestCacheHitRate is the percentage, from 0 to 100, of the latest
	// assistant message's prompt tokens that were cache reads, or nil when
	// that message had no prompt tokens.
	LatestCacheHitRate *float64
	// UsingSubscription reports that the model's provider bills a
	// subscription; PiG then shows the cost even when it is zero.
	UsingSubscription bool
	ContextUsage      ContextUsage
	// AutoCompact reports that the session compacts automatically when the
	// context fills up.
	AutoCompact bool
	// Experimental reports that experimental features are on.
	Experimental bool
	// Model is the selected model's id, or "unknown" without one.
	Model string
	// Provider is the model's provider id while more than one provider is
	// available, as PiG then names it; otherwise "".
	Provider string
	// ThinkingLevel is the thinking level of a model that reasons: "off",
	// "minimal", "low", "medium", "high", "xhigh" or "max". It is "" for a
	// model that does not reason.
	ThinkingLevel string
	// Routed is where a virtual model sent the latest request. Its Model is
	// "" for an ordinary model.
	Routed RoutedModel
	// ExtensionStatuses are the texts extensions set with setStatus, in the
	// order of their keys, each on one line with runs of white space
	// collapsed. They may carry ANSI styling.
	ExtensionStatuses []string
}

// UsageTotals counts tokens and cost.
type UsageTotals struct {
	// Input, Output, CacheRead and CacheWrite are token counts.
	Input, Output, CacheRead, CacheWrite int
	// Cost is in US dollars.
	Cost float64
}

// ContextUsage is how full the model's context window is.
type ContextUsage struct {
	// Tokens is the context's size in tokens, and Percent that size as a
	// percentage of ContextWindow, from 0 to 100. Both are zero while
	// Unknown, from a compaction until the next response.
	Tokens  int
	Percent float64
	Unknown bool
	// ContextWindow is the model's context window in tokens.
	ContextWindow int
}

// RoutedModel is the model and thinking level that a virtual model routed
// a request to.
type RoutedModel struct {
	Model         string
	ThinkingLevel string
}

func (Lines) frontendNode()        {}
func (ToolCard) frontendNode()     {}
func (MarkdownText) frontendNode() {}
func (Thinking) frontendNode()     {}
func (Editor) frontendNode()       {}
func (Selector) frontendNode()     {}
func (Settings) frontendNode()     {}
func (Working) frontendNode()      {}
func (Footer) frontendNode()       {}
func (Overlay) frontendNode()      {}
