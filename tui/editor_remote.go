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
}

// SetRemote installs remote in place of the editor's own editing, or with
// nil restores it. The mirrored text becomes the editor's text.
func (e *Editor) SetRemote(remote EditorRemote) {
	if remote == nil {
		e.remote = nil
		e.Invalidate()
		return
	}
	e.AutocompleteCancel()
	e.remote = &editorRemoteState{remote: remote, focusedReported: e.Focused, thinkingLevel: e.ThinkingLevel}
	e.Invalidate()
}

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
func (e *Editor) remoteMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	if event.Type != MouseClick || event.Button != MouseButtonLeft || event.Y < 0 {
		return nil
	}
	e.remote.remote.Mouse(event)
	return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Focus: true}}
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

// renderRemote returns the component's frame unchanged at its render width. The layout owns the spacer above the editor. A stale-width frame is clipped until the component renders at the new width.
func (e *Editor) renderRemote(width int) []string {
	if e.Focused != e.remote.focusedReported || e.ThinkingLevel != e.remote.thinkingLevel {
		e.remote.focusedReported = e.Focused
		e.remote.thinkingLevel = e.ThinkingLevel
		e.remote.remote.StateChanged()
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
	if fp, ok := provider.(ForcefulAutocompleteProvider); ok && force {
		query.base = fp.GetSuggestionsForce(lines, cursorLine, cursorCol)
	} else {
		query.base = provider.GetSuggestions(lines, cursorLine, cursorCol)
	}
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
