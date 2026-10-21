package tui

import "github.com/MichaelKinsy/PiG/internal/jsstring"

// view_widget_state.go: read access to the state the extension component
// kit reports to a frontend and compares against the state its sender sent
// (D107, docs/plan/extension-component-kit.md §2.2). Pi's components expose
// this state as public fields or have none; the Go ports keep it private.

// Expanded reports the component's expanded state, which a click toggles.
func (c *BranchSummaryMessageComponent) Expanded() bool { return c.expanded }

// Expanded reports the component's expanded state, which a click toggles.
func (c *CompactionSummaryMessageComponent) Expanded() bool { return c.expanded }

// Expanded reports the component's expanded state, which a click toggles.
func (s *SkillInvocationMessageComponent) Expanded() bool { return s.expanded }

// Expanded reports the component's expanded state (upstream _expanded).
func (c *CustomMessageComponent) Expanded() bool { return c.expanded }

// Cursor is the cursor's UTF-16 offset in the value.
func (t *TextInput) Cursor() int { return t.cursor }

// Loader returns the loader the bordered loader draws.
func (bl *BorderedLoader) Loader() *Loader { return bl.loader }

// SetCursor places the cursor at a UTF-16 offset of the value, clamped to it:
// the cursor of a component whose sender owns the state (Node's Input).
func (t *TextInput) SetCursor(offset int) {
	t.cursor = max(0, min(offset, jsstring.Length(t.value)))
	t.lastAction = ""
	t.Invalidate()
}

// SetCursor places the cursor at a logical line and UTF-16 column, clamped to
// the document: the cursor of a component whose sender owns the state (Node's
// Editor).
func (e *Editor) SetCursor(line, col int) {
	e.cursor[0] = max(0, min(line, len(e.lines)-1))
	e.setCursorCol(max(0, min(col, jsstring.Length(e.lines[e.cursor[0]]))))
	e.lastAction = ""
	e.Invalidate()
}

// SetTheme restyles the editor's border and autocomplete list, as the
// constructor's theme argument does.
func (e *Editor) SetTheme(theme EditorTheme) {
	e.BorderColor = theme.BorderColor
	e.selectListTheme = &theme.SelectList
	e.Invalidate()
}
