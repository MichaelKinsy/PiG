package extension

// RemoteEditor is an editor an extension installed with
// ctx.ui.setEditorComponent, running in the extension's process. Pi puts the
// factory's editor (typically a CustomEditor subclass) in place of its own:
// every keystroke goes to that editor's handleInput, its render output is the
// editor on screen, and Pi wires the editor's callbacks to its own handlers.
// The host does the same across the process boundary: it hands the editor
// every keystroke and every text change it makes, shows the editor's frames,
// and receives the editor's callbacks through the bound
// [RemoteEditorHost].
//
// UIContext.SetEditorComponent receives a RemoteEditor; nil restores the
// host's own editor.
type RemoteEditor interface {
	// Input delivers one keystroke to the editor's handleInput.
	Input(data string)
	// SetText, InsertTextAtCursor and AddToHistory call the editor's
	// methods of the same name, as Pi's host calls them on this.editor.
	SetText(text string)
	InsertTextAtCursor(text string)
	AddToHistory(text string)
	// Mouse delivers a left click on the editor rows to the editor's
	// handleMouse, as Pi's fullscreen renderer does.
	Mouse(event RemoteEditorMouseEvent)
	// Configure applies host editor state to the editor: Pi copies the
	// default editor's text, padding and autocomplete size when it installs
	// the editor, and the TUI sets its focus.
	Configure(config RemoteEditorConfig)
	// EmbedWorkingStatus reports whether this editor wants status in its border.
	EmbedWorkingStatus() bool
	// Bind attaches the host that receives the editor's frames and
	// callbacks. Events that arrive before Bind are delivered on binding.
	Bind(host RemoteEditorHost)
	// Close detaches the editor from the host.
	Close()
}

// RemoteEditorMouseEvent is pi-tui's TuiMouseEvent, with the row relative to
// the editor's first row.
type RemoteEditorMouseEvent struct {
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
	ClickCount int    `json:"clickCount,omitempty"`
}

// RemoteEditorConfig is the host editor state an [RemoteEditor] mirrors.
type RemoteEditorConfig struct {
	PaddingX               int  `json:"paddingX"`
	AutocompleteMaxVisible int  `json:"autocompleteMaxVisible"`
	Focused                bool `json:"focused"`
	// ThinkingLevel is the session's thinking level, whose border color Pi
	// gives the editor outside bash mode.
	ThinkingLevel string `json:"thinkingLevel"`
	// ShowHardwareCursor is the TUI's hardware-cursor setting, which the
	// editor's tui.getShowHardwareCursor() reports.
	ShowHardwareCursor bool `json:"showHardwareCursor"`
	// Shortcuts are the key ids of the extension shortcuts Pi's
	// onExtensionShortcut runs from inside the editor.
	Shortcuts        []string `json:"shortcuts"`
	Streaming        bool     `json:"streaming"`
	Compacting       bool     `json:"compacting"`
	BashRunning      bool     `json:"bashRunning"`
	InterruptHandled bool     `json:"interruptHandled"`
	// WorkingStatus is the current border status; nil clears it.
	WorkingStatus *RemoteEditorStatus `json:"workingStatus"`
}

// RemoteEditorStatus is a border-status snapshot.
// pig divergence (D94): the host advances the animation and sends snapshots; Pi's editor-side indicator runs its own timer.
type RemoteEditorStatus struct {
	Kind              string   `json:"kind"`
	Message           string   `json:"message"`
	Frames            []string `json:"frames"`
	Frame             int      `json:"frame"`
	SpinnerColor      string   `json:"spinnerColor"`
	MessageColor      string   `json:"messageColor"`
	IndicatorVerbatim bool     `json:"indicatorVerbatim"`
}

// RemoteEditorAction snapshots the editor at the app-action boundary. Local means the component already applied the synchronous editor mutations; the host must not replay them into that component.
type RemoteEditorAction struct {
	Action   string `json:"action"`
	Text     string `json:"text"`
	Expanded string `json:"expanded"`
	Local    bool   `json:"local"`
}

// RemoteEditorHost receives an [RemoteEditor]'s output. The bridge calls
// it off the host's UI loop, in the order the extension produced the events.
type RemoteEditorHost interface {
	// EditorFrame is the editor's render(width) output for the terminal
	// width. WantsKeyRelease is the editor's wantsKeyRelease flag.
	EditorFrame(lines []string, width int, wantsKeyRelease bool)
	// EditorChanged is the editor's onChange: its text and, for the host's
	// getEditorText, the text with paste markers expanded.
	EditorChanged(text, expanded string)
	// EditorSubmit is the editor's onSubmit. done runs once the host has
	// handled the submission, which settles the promise onSubmit returns.
	EditorSubmit(text string, done func())
	// EditorAction runs Pi's handler for an app action the editor
	// dispatched: app.interrupt (onEscape), app.exit (onCtrlD),
	// app.clipboard.pasteImage (onPasteImage), or an actionHandlers entry.
	EditorAction(action RemoteEditorAction)
	// EditorInputDone releases input backpressure after the key's callbacks have reached the host.
	EditorInputDone()
	// EditorShortcut runs the extension shortcut the editor's
	// onExtensionShortcut matched for data.
	EditorShortcut(data string)
	// TerminalWrite writes raw output to the terminal (tui.terminal.write).
	TerminalWrite(data string)
	// SetShowHardwareCursor is tui.setShowHardwareCursor.
	SetShowHardwareCursor(enabled bool)
	// EditorClosed reports that the extension's editor is gone (its process
	// ended); the host restores its own editor.
	EditorClosed()
}
