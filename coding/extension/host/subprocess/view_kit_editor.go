package subprocess

// view_kit_editor.go: the editor kind, pi-tui's Editor(tui, theme, {paddingX,
// autocompleteMaxVisible}) with the setters an author calls on it (D107 §2.2;
// editor.ts). The host owns the text and the cursor and runs Editor's own key
// and mouse handling: the focused node takes every key. The extension
// receives onSubmit as a "submit" event and a "change" with the text and the
// cursor after each key or press that changed either. The visible line count
// is Pi's: max(5, floor(terminal rows * 0.3)), read from the terminal at each
// render and key.

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"slices"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

// viewEditorMaxSlashCommands bounds an editor's autocomplete command list.
// pig additive (D107): Pi's CombinedAutocompleteProvider takes any number of commands; the wire node is validated before the host builds it, so a frontend cannot make the host hold an unbounded list.
const viewEditorMaxSlashCommands = 1000

func init() {
	registerViewWidget(ViewKindEditor, &viewWidget{
		check: func(c *viewChecker, n *ViewNode) {
			if n.ID == "" {
				c.fail("editor needs an id")
			}
			c.fg(n.BorderColor)
			if cur := n.Cursor; cur != nil && (cur.Line < 0 || cur.Col < 0) {
				c.fail("editor cursor must not be negative")
			}
			if len(n.History) > viewMaxItems {
				c.fail("editor history has more than %d entries", viewMaxItems)
			}
			if a := n.Autocomplete; a != nil {
				if len(a.Commands) > viewEditorMaxSlashCommands {
					c.fail("editor autocomplete has more than %d commands", viewEditorMaxSlashCommands)
				}
				for _, command := range a.Commands {
					if command.Name == "" {
						c.fail("editor autocomplete command needs a name")
					}
				}
			}
		},
		build:     buildEditor,
		focusable: true,
	})
}

// viewEditorComponent is the Editor the tree holds. It asks the surface for the terminal's rows before it renders or
// handles a key, as Pi's Editor reads tui.terminal.rows, and reports what a mouse click changed.
type viewEditorComponent struct {
	*tui.Editor
	rows    func() int
	changed func()
}

// editorVisibleLines is editor.ts:536: 30% of the terminal's rows, at least 5.
func editorVisibleLines(rows int) int { return max(5, int(math.Floor(float64(rows)*0.3))) }

func (c *viewEditorComponent) syncRows() {
	rows := 0
	if c.rows != nil {
		rows = c.rows()
	}
	c.SetMaxVisibleLines(editorVisibleLines(rows))
}

func (c *viewEditorComponent) Render(width int) []string {
	c.syncRows()
	return c.Editor.Render(width)
}

func (c *viewEditorComponent) HandleInput(data string) {
	c.syncRows()
	c.Editor.HandleInput(data)
}

func (c *viewEditorComponent) HandleMouse(event tui.TuiMouseEvent) *tui.TuiMouseEventResult {
	c.syncRows()
	result := c.Editor.HandleMouse(event)
	c.changed()
	return result
}

// viewEditorState is the widget state of an editor.
type viewEditorState struct {
	comp   *viewEditorComponent
	b      *viewBuilder
	inst   *viewInstance
	value  string
	cursor ViewCursor
}

func (v *viewEditorState) read() (string, ViewCursor) {
	cur := v.comp.GetCursor()
	return v.comp.Text(), ViewCursor{Line: cur.Line, Col: cur.Col}
}

// report queues a "change" when the text or the cursor moved since the last report.
func (v *viewEditorState) report() {
	value, cursor := v.read()
	if value == v.value && cursor == v.cursor {
		return
	}
	v.value, v.cursor = value, cursor
	v.b.queue(v.inst, ViewEventChange, func(ev *ViewEventPayload) { ev.Value, ev.Cursor = value, &cursor })
}

func (v *viewEditorState) handleInput(data string) bool {
	v.comp.HandleInput(data)
	v.report()
	return true
}
func (v *viewEditorState) setFocused(focused bool) { v.comp.SetFocused(focused) }
func (v *viewEditorState) refresh(node *frontend.ViewNode) {
	value, cursor := v.read()
	node.Value, node.Cursor = value, frontend.ViewCursor{Line: cursor.Line, Col: cursor.Col}
}

// editorSlashCommands are the wire commands as pi-tui's SlashCommand.
func editorSlashCommands(a *ViewAutocomplete) []tui.SlashCommand {
	commands := make([]tui.SlashCommand, len(a.Commands))
	for i, c := range a.Commands {
		commands[i] = tui.SlashCommand{Name: c.Name, Description: c.Description, ArgumentHint: c.ArgumentHint}
	}
	return commands
}

func buildEditor(b *viewBuilder, n *ViewNode, out *frontend.ViewNode) (tui.Component, error) {
	ctor := widgetCtorKey(n, func(c *ViewNode) {
		c.Value, c.Cursor, c.Focused, c.PaddingX, c.AutocompleteMaxVisible = "", nil, false, nil, nil
		c.BorderColor, c.DisableSubmit, c.History, c.Autocomplete = "", false, nil, nil
	})
	key, _ := json.Marshal(ctor)
	// A history that does not extend the one the sender sent last is another editor's.
	inst, fresh := b.conversationInstance(n, key, func(sent *ViewNode) bool {
		return sent == nil || slices.Equal(sent.History, n.History[:min(len(n.History), len(sent.History))])
	})
	inst.node = out
	if fresh {
		state := &viewEditorState{b: b, inst: inst}
		state.comp = &viewEditorComponent{Editor: tui.NewEditor(), rows: b.s.terminalRows, changed: func() { state.report() }}
		inst.widget = state
	}
	state := inst.widget.(*viewEditorState)
	state.b = b
	ed := state.comp.Editor
	sent := inst.sent

	border := n.BorderColor
	if border == "" {
		border = "borderMuted"
	}
	ed.SetTheme(tui.EditorTheme{BorderColor: b.fgFn(border), SelectList: tui.SelectListThemeFor(b.theme)})
	ed.DisableSubmit = n.DisableSubmit
	if fresh || intOr(sent.PaddingX, 0) != intOr(n.PaddingX, 0) {
		ed.SetPaddingX(intOr(n.PaddingX, 0))
	}
	if fresh || intOr(sent.AutocompleteMaxVisible, 5) != intOr(n.AutocompleteMaxVisible, 5) {
		ed.SetAutocompleteMaxVisible(intOr(n.AutocompleteMaxVisible, 5))
	}
	oldHistory := 0
	if !fresh {
		oldHistory = len(sent.History)
	}
	for _, entry := range n.History[oldHistory:] {
		ed.AddToHistory(entry)
	}
	if fresh || !reflect.DeepEqual(sent.Autocomplete, n.Autocomplete) {
		if a := n.Autocomplete; a != nil {
			base := a.BasePath
			if base == "" {
				base, _ = os.Getwd()
			}
			ed.SetAutocomplete(tui.NewCombinedProvider(editorSlashCommands(a), base, ""))
		} else if !fresh {
			ed.SetAutocomplete(nil)
		}
	}
	// The callbacks are installed after the first setText, which would otherwise call onChange.
	if n.Value != "" && fresh || !fresh && n.Value != sent.Value {
		ed.OnChange = nil
		ed.SetText(n.Value)
	}
	ed.OnSubmit = func(text string) {
		_, cursor := state.read()
		b.queue(inst, ViewEventSubmit, func(ev *ViewEventPayload) { ev.Value, ev.Cursor = text, &cursor })
	}
	// A sender that owns the cursor (Node's) names it with every frame; the host applies it when it moved.
	if n.Cursor != nil && (fresh || sent.Cursor == nil || *sent.Cursor != *n.Cursor) {
		ed.SetCursor(n.Cursor.Line, n.Cursor.Col)
	}
	if fresh {
		state.value, state.cursor = state.read()
	}
	inst.remember(n)
	value, cursor := state.read()
	out.Value, out.Cursor = value, frontend.ViewCursor{Line: cursor.Line, Col: cursor.Col}
	return state.comp, nil
}
