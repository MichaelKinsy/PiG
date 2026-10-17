package tui

import (
	"context"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// EditorRemote is an extension's editor component standing in for the
// editor (Pi's ctx.ui.setEditorComponent). Pi replaces its editor with the
// component and routes to it everything it routes to the editor; with a
// remote installed the editor does the same: it forwards keystrokes and the
// host's text operations to the remote, shows the remote's frames, and mirrors
// the remote's text so the host reads it as it would its own.
type EditorRemote interface {
	// Input delivers one keystroke to the component's handleInput.
	Input(data string)
	SetText(text string)
	InsertTextAtCursor(text string)
	AddToHistory(text string)
	// Mouse delivers a left click, its row relative to the component's
	// first row, to the component's handleMouse.
	Mouse(event TuiMouseEvent)
	// StateChanged reports that the editor's padding, autocomplete size,
	// focus or thinking level changed, which the component mirrors.
	StateChanged()
}

type editorRemoteState struct {
	remote          EditorRemote
	frame           []string
	frameWidth      int
	wantsKeyRelease bool
	// expanded is the component's getExpandedText for the mirrored text.
	expanded        string
	expandedFor     string
	focusedReported bool
	thinkingLevel   string
	// delegated marks a component whose base editing is this editor: the
	// component sees keys and renders through [Editor.AsBase], and the text
	// stays the editor's own.
	delegated bool
}

// SetRemote installs remote in place of the editor's own editing, or with
// nil restores it. The mirrored text becomes the editor's text.
func (e *Editor) SetRemote(remote EditorRemote) {
	text := e.Text()
	leavingDelegated := e.stockInstance != nil
	if leavingDelegated {
		// The delegated component's editor instance ends; the default editor returns as it was at the install.
		e.loadInstance(*e.stockInstance)
		e.stockInstance = nil
	}
	if remote == nil {
		e.remote = nil
		if leavingDelegated {
			// upstream: interactive-mode.ts setCustomEditorComponent defaultEditor.setText(currentText)
			e.SetText(text)
		}
		e.Invalidate()
		return
	}
	e.AutocompleteCancel()
	e.remote = &editorRemoteState{remote: remote, focusedReported: e.Focused, thinkingLevel: e.ThinkingLevel}
	e.Invalidate()
}

// SetDelegatedRemote installs remote like [Editor.SetRemote] for a component
// that subclasses the editor: it receives every key and renders every frame,
// and reaches this editor's own editing, rendering and app-action handling
// through [Editor.AsBase], as a CustomEditor subclass reaches its super.
//
// Pi builds a new Editor for each factory and copies only the text into it, so the component's base starts as a fresh
// instance: an empty prompt history and kill ring, no pastes, and an undo stack holding the empty document its
// setText(currentText) replaced. The default editor's own instance state returns when the component is removed or
// replaced.
func (e *Editor) SetDelegatedRemote(remote EditorRemote) {
	text := e.Text()
	e.SetRemote(remote)
	if e.remote == nil {
		return
	}
	e.remote.delegated = true
	stock := e.saveInstance()
	e.stockInstance = &stock
	e.loadInstance(editorInstance{lines: []string{""}, inputHistIdx: -1})
	// upstream: interactive-mode.ts setCustomEditorComponent newEditor.setText(currentText) on the new instance
	normalized := normalizeEditorText(text)
	if normalized != "" {
		e.saveHistory()
	}
	e.lines = strings.Split(normalized, "\n")
	e.cursor[0] = len(e.lines) - 1
	e.setCursorCol(jsstring.Length(e.lines[e.cursor[0]]))
	e.Invalidate()
}

// editorInstance is the editing state each pi-tui Editor instance owns, as opposed to its configuration (padding,
// autocomplete provider and size, theme, border colour) and the TUI's focus.
type editorInstance struct {
	lines                []string
	cursor               [2]int
	jumpMode             string
	history              []editorState
	killRing             KillRing
	lastAction           string
	preferredVisualCol   *int
	snappedFromCursorCol *int
	inputHistory         []string
	inputHistIdx         int
	inputHistSaved       *editorHistoryDraft
	isInPaste            bool
	pasteBuffer          string
	pastes               map[int]string
	pasteCounter         int
	scrollOffset         int
}

func (e *Editor) saveInstance() editorInstance {
	return editorInstance{
		lines: e.lines, cursor: e.cursor, jumpMode: e.jumpMode, history: e.history, killRing: e.killRing, lastAction: e.lastAction,
		preferredVisualCol: e.preferredVisualCol, snappedFromCursorCol: e.snappedFromCursorCol, inputHistory: e.inputHistory,
		inputHistIdx: e.inputHistIdx, inputHistSaved: e.inputHistSaved, isInPaste: e.isInPaste, pasteBuffer: e.pasteBuffer,
		pastes: e.pastes, pasteCounter: e.pasteCounter, scrollOffset: e.scrollOffset,
	}
}

func (e *Editor) loadInstance(s editorInstance) {
	e.lines, e.cursor, e.jumpMode, e.history, e.killRing, e.lastAction = s.lines, s.cursor, s.jumpMode, s.history, s.killRing, s.lastAction
	e.preferredVisualCol, e.snappedFromCursorCol, e.inputHistory = s.preferredVisualCol, s.snappedFromCursorCol, s.inputHistory
	e.inputHistIdx, e.inputHistSaved, e.isInPaste, e.pasteBuffer = s.inputHistIdx, s.inputHistSaved, s.isInPaste, s.pasteBuffer
	e.pastes, e.pasteCounter, e.scrollOffset = s.pastes, s.pasteCounter, s.scrollOffset
	e.Invalidate()
}

// IsDelegated reports whether the installed remote reaches this editor's
// editing through [Editor.AsBase].
func (e *Editor) IsDelegated() bool { return e != nil && e.remote != nil && e.remote.delegated }

// AsBase runs fn with the editor's own editing, rendering and completion
// state in place of the remote's, which is Pi's `super` call from a
// CustomEditor subclass. It runs on the owner loop and does not nest.
func (e *Editor) AsBase(fn func()) {
	if e.inBase {
		fn()
		return
	}
	e.inBase = true
	defer func() { e.inBase = false }()
	fn()
}

// forwards reports whether the host's editor operations go to the remote.
func (e *Editor) forwards() bool { return e.remote != nil && !e.inBase }

// Remote returns the installed remote, or nil.
func (e *Editor) Remote() EditorRemote {
	if e.remote == nil {
		return nil
	}
	return e.remote.remote
}

// IsRemote reports whether an extension's editor component stands in for
// the editor.
func (e *Editor) IsRemote() bool { return e != nil && e.remote != nil }

// SetRemoteFrame records the remote's latest render output for the terminal
// width it was laid out for.
func (e *Editor) SetRemoteFrame(lines []string, width int, wantsKeyRelease bool) {
	if e.remote == nil {
		return
	}
	e.remote.frame = append([]string(nil), lines...)
	e.remote.frameWidth = width
	e.remote.wantsKeyRelease = wantsKeyRelease
	e.Invalidate()
}

// ApplyRemoteChange mirrors the remote's text (its onChange) and the same
// text with paste markers expanded.
func (e *Editor) ApplyRemoteChange(text, expanded string) {
	if e.remote == nil {
		return
	}
	text = jsstring.Canonical(text)
	e.lines = strings.Split(text, "\n")
	e.cursor = [2]int{len(e.lines) - 1, jsstring.Length(e.lines[len(e.lines)-1])}
	e.remote.expanded = jsstring.Canonical(expanded)
	e.remote.expandedFor = text
	if e.OnChange != nil {
		e.OnChange(text)
	}
}

// remoteMouse forwards a left click on the component's rows, which Pi's
// Editor handles (positioning the cursor or picking an autocomplete item),
// and leaves press, drag, release and wheel to the renderer, as the Editor
// leaves them unhandled outside its autocomplete list.
func (e *Editor) remoteMouse(event TuiMouseEvent) *TuiMouseEventResult {
	if event.Type != MouseClick || event.Button != MouseButtonLeft || event.Y < 0 {
		return nil
	}
	e.remote.remote.Mouse(event)
	return &TuiMouseEventResult{Handled: true, Focus: true}
}

// WantsKeyRelease reports the remote component's wantsKeyRelease; the
// editor's own editing takes no key releases.
func (e *Editor) WantsKeyRelease() bool {
	return e != nil && e.remote != nil && e.remote.wantsKeyRelease
}

// remoteSetText mirrors text and sends it to the remote.
func (e *Editor) remoteSetText(text string) {
	text = jsstring.Canonical(text)
	e.lines = strings.Split(text, "\n")
	e.cursor = [2]int{len(e.lines) - 1, jsstring.Length(e.lines[len(e.lines)-1])}
	e.remote.expandedFor = ""
	e.remote.remote.SetText(text)
	e.Invalidate()
}

// remoteExpandedText is the remote's getExpandedText for the mirrored text.
func (e *Editor) remoteExpandedText() string {
	text := e.Text()
	if e.remote.expandedFor == text && e.remote.expanded != "" {
		return e.remote.expanded
	}
	return text
}

// EditorRemoteRenderer is an [EditorRemote] that renders its component on demand, for a component that runs in the host's process: the editor
// shows what the component renders at the width it is asked for, so a component that changes on its own (an animation, a timer) shows the change on
// the next render without a key. ok is false when the remote has only the frames it was sent.
type EditorRemoteRenderer interface {
	RenderRemote(width int) (lines []string, ok bool)
}

// renderRemote returns the component's frame unchanged at its render width. The layout owns the spacer above the editor. A stale-width frame is clipped until the component renders at the new width.
func (e *Editor) renderRemote(width int) []string {
	if e.Focused != e.remote.focusedReported || e.ThinkingLevel != e.remote.thinkingLevel {
		e.remote.focusedReported = e.Focused
		e.remote.thinkingLevel = e.ThinkingLevel
		e.remote.remote.StateChanged()
	}
	if renderer, ok := e.remote.remote.(EditorRemoteRenderer); ok {
		if lines, ok := renderer.RenderRemote(width); ok {
			return lines
		}
	}
	out := make([]string, 0, len(e.remote.frame))
	for _, line := range e.remote.frame {
		if e.remote.frameWidth != width && widthx.VisibleWidth(line) > width {
			line = widthx.TruncateToWidth(line, width, "", false)
		}
		out = append(out, line)
	}
	return out
}

// RemoteSuggestionQuery captures a local provider's synchronous answer and its awaited work without reading the editor again.
type RemoteSuggestionQuery struct {
	argumentTask   func(context.Context) ([]AutocompleteItem, error)
	argumentPrefix string
	base           *AutocompleteSuggestions
	fileTask       func(context.Context) []AutocompleteItem
	filePrefix     string
}

// NewAutocompleteQuery captures the local provider's answer and deferred filesystem search on the owner loop. Run awaits filesystem work off-loop.
func NewAutocompleteQuery(provider AutocompleteProvider, lines []string, cursorLine, cursorCol int, force bool) *RemoteSuggestionQuery {
	lines = append([]string(nil), lines...)
	if len(lines) == 0 {
		lines = []string{""}
	}
	query := &RemoteSuggestionQuery{}
	if provider == nil {
		return query
	}
	if planner, ok := provider.(AsyncSuggestionPlanner); ok {
		if prefix, task, ok := planner.SuggestionTask(lines, cursorLine, cursorCol, force); ok {
			query.argumentTask = task
			query.argumentPrefix = prefix
			return query
		}
	}
	if afs, ok := provider.(AsyncFileSearcher); ok {
		if prefix, run, ok := afs.FileSearchTask(lines, cursorLine, cursorCol); ok {
			query.fileTask, query.filePrefix = run, prefix
		}
	}
	query.base = provider.GetSuggestions(context.Background(), lines, cursorLine, cursorCol, AutocompleteSuggestionOptions{Force: force})
	if query.base != nil && len(query.base.Items) == 0 {
		query.base = nil
	}
	return query
}

// RunResult awaits the query and preserves callback errors for its caller.
func (q *RemoteSuggestionQuery) RunResult(ctx context.Context) (*AutocompleteSuggestions, error) {
	if q.argumentTask != nil {
		items, err := q.argumentTask(ctx)
		if err != nil {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if len(items) == 0 {
			return nil, nil
		}
		return &AutocompleteSuggestions{Items: items, Prefix: q.argumentPrefix}, nil
	}
	if q.fileTask != nil {
		items := q.fileTask(ctx)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if len(items) == 0 {
			return nil, nil
		}
		return &AutocompleteSuggestions{Items: items, Prefix: q.filePrefix}, nil
	}
	return q.base, ctx.Err()
}
