package tui

// Ports packages/tui/src/components/input.ts

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type textInputState struct {
	value  string
	cursor int
}

// TextInput is a single-line input with UTF-16 cursor offsets and grapheme-aware editing. Unpaired UTF-16 units use the internal WTF-8 string representation.
type TextInput struct {
	invalidatable
	value               string
	cursor              int
	done, cancel        bool
	Focused             bool
	isInPaste           bool
	pasteBuffer         string
	killRing            KillRing
	lastAction          string
	undoStack           UndoStack[textInputState]
	prompt, placeholder string
	placeholderStyle    func(string) string
	renderedStartColumn int
	callbacks           bool
	OnSubmit            func(string)
	OnEscape            func()
}

// InputOptions mirrors InputOptions. Nil Prompt selects "> "; nil PlaceholderStyle leaves text unstyled.
type InputOptions struct {
	Prompt           *string
	Placeholder      string
	PlaceholderStyle func(string) string
}

// NewTextInput creates a host-owned confirmation input. The title is supplied by its enclosing dialog; Done and Cancelled report completion.
func NewTextInput(_ string) *TextInput {
	return &TextInput{Focused: true, prompt: "> ", placeholderStyle: func(text string) string { return text }}
}

// NewInput creates upstream Input's callback-driven component, initially unfocused.
func NewInput(options InputOptions) *TextInput {
	input := NewTextInput("")
	input.callbacks = true
	input.Focused = false
	if options.Prompt != nil {
		input.prompt = *options.Prompt
	}
	input.placeholder = options.Placeholder
	if options.PlaceholderStyle != nil {
		input.placeholderStyle = options.PlaceholderStyle
	}
	return input
}

// SetFocused records whether the input holds TUI focus; only a focused input emits the hardware-cursor marker.
func (t *TextInput) SetFocused(focused bool) {
	if t.Focused != focused {
		t.Focused = focused
		t.Invalidate()
	}
}

func (t *TextInput) Done() bool      { return t.done }
func (t *TextInput) Cancelled() bool { return t.cancel }
func (t *TextInput) Text() string    { return t.GetValue() }

// GetValue returns the current JavaScript string, retaining unpaired UTF-16 units.
func (t *TextInput) GetValue() string { return t.value }

// SetValue replaces the value and clamps the existing UTF-16 cursor without rounding surrogate-half positions.
func (t *TextInput) SetValue(value string) {
	t.value = jsstring.Canonical(value)
	t.cursor = min(t.cursor, jsstring.Length(t.value))
	t.Invalidate()
}

// SetText sets the component text. Callback-driven Input retains its cursor as setValue does; host confirmation inputs prefill at the end.
func (t *TextInput) SetText(value string) {
	t.SetValue(value)
	if !t.callbacks {
		t.cursor = jsstring.Length(value)
	}
}

func (t *TextInput) pushUndo() { t.undoStack.Push(textInputState{t.value, t.cursor}) }
func (t *TextInput) undo() {
	snapshot, ok := t.undoStack.Pop()
	if !ok {
		return
	}
	t.value, t.cursor = snapshot.value, snapshot.cursor
	t.lastAction = ""
	t.Invalidate()
}

// HandleInput applies one input event. Paste state spans chunks; submission and cancellation use the selected owner/callback contract.
func (t *TextInput) HandleInput(data string) {
	if t.done {
		return
	}
	if strings.Contains(data, "\x1b[200~") {
		t.isInPaste = true
		t.pasteBuffer = ""
		data = strings.Replace(data, "\x1b[200~", "", 1)
	}
	if t.isInPaste {
		t.pasteBuffer += data
		if end := strings.Index(t.pasteBuffer, "\x1b[201~"); end >= 0 {
			t.handlePaste(t.pasteBuffer[:end])
			t.isInPaste = false
			remaining := t.pasteBuffer[end+6:]
			t.pasteBuffer = ""
			if remaining != "" {
				t.HandleInput(remaining)
			}
		}
		return
	}
	kb := GetTUIKeybindings()
	switch {
	case kb.Matches(data, KBSelectCancel):
		if t.callbacks {
			if t.OnEscape != nil {
				t.OnEscape()
			}
		} else {
			t.cancel = true
			t.done = true
			t.Invalidate()
		}
		return
	case kb.Matches(data, KBEditorUndo):
		t.undo()
		return
	case kb.Matches(data, KBInputSubmit) || data == "\n":
		if t.callbacks {
			if t.OnSubmit != nil {
				t.OnSubmit(t.value)
			}
		} else {
			t.done = true
			t.Invalidate()
		}
		return
	case kb.Matches(data, KBEditorDeleteCharBack):
		t.handleBackspace()
		return
	case kb.Matches(data, KBEditorDeleteCharForward):
		t.handleForwardDelete()
		return
	case kb.Matches(data, KBEditorDeleteWordBack):
		t.deleteWordBackward()
		return
	case kb.Matches(data, KBEditorDeleteWordForward):
		t.deleteWordForward()
		return
	case kb.Matches(data, KBEditorDeleteToLineStart):
		t.deleteToLineStart()
		return
	case kb.Matches(data, KBEditorDeleteToLineEnd):
		t.deleteToLineEnd()
		return
	case kb.Matches(data, KBEditorYank):
		t.yank()
		return
	case kb.Matches(data, KBEditorYankPop):
		t.yankPop()
		return
	case kb.Matches(data, KBEditorCursorLeft):
		t.lastAction = ""
		if t.cursor > 0 {
			t.cursor -= inputLastGraphemeLength(jsstring.Slice(t.value, 0, t.cursor))
		}
		t.Invalidate()
		return
	case kb.Matches(data, KBEditorCursorRight):
		t.lastAction = ""
		if t.cursor < jsstring.Length(t.value) {
			t.cursor += inputFirstGraphemeLength(jsstring.Slice(t.value, t.cursor, jsstring.Length(t.value)))
		}
		t.Invalidate()
		return
	case kb.Matches(data, KBEditorCursorLineStart):
		t.lastAction = ""
		t.cursor = 0
		t.Invalidate()
		return
	case kb.Matches(data, KBEditorCursorLineEnd):
		t.lastAction = ""
		t.cursor = jsstring.Length(t.value)
		t.Invalidate()
		return
	case kb.Matches(data, KBEditorCursorWordLeft):
		t.moveWordBackward()
		return
	case kb.Matches(data, KBEditorCursorWordRight):
		t.moveWordForward()
		return
	}
	if printable, ok := DecodePrintableKey(data); ok {
		t.insertCharacter(printable)
		return
	}
	for _, ch := range data {
		if ch < 32 || ch == 0x7f || ch >= 0x80 && ch <= 0x9f {
			return
		}
	}
	t.insertCharacter(data)
}

func inputFirstGraphemeLength(text string) int {
	first, _ := widthx.FirstGrapheme(text)
	if first == "" {
		return 1
	}
	return jsstring.Length(first)
}
func inputLastGraphemeLength(text string) int {
	segments := graphemeSegments(text)
	if len(segments) == 0 {
		return 1
	}
	return jsstring.Length(segments[len(segments)-1].Text)
}
func (t *TextInput) insertCharacter(char string) {
	if isWhitespaceChar(char) || t.lastAction != "type-word" {
		t.pushUndo()
	}
	t.lastAction = "type-word"
	t.value = jsstring.Splice(t.value, t.cursor, t.cursor, char)
	t.cursor += jsstring.Length(char)
	t.Invalidate()
}
func (t *TextInput) handleBackspace() {
	t.lastAction = ""
	if t.cursor <= 0 {
		return
	}
	t.pushUndo()
	length := inputLastGraphemeLength(jsstring.Slice(t.value, 0, t.cursor))
	t.value = jsstring.Splice(t.value, t.cursor-length, t.cursor, "")
	t.cursor -= length
	t.Invalidate()
}
func (t *TextInput) handleForwardDelete() {
	t.lastAction = ""
	if t.cursor >= jsstring.Length(t.value) {
		return
	}
	t.pushUndo()
	length := inputFirstGraphemeLength(jsstring.Slice(t.value, t.cursor, jsstring.Length(t.value)))
	t.value = jsstring.Splice(t.value, t.cursor, t.cursor+length, "")
	t.Invalidate()
}
func (t *TextInput) deleteToLineStart() {
	if t.cursor == 0 {
		return
	}
	t.pushUndo()
	deleted := jsstring.Slice(t.value, 0, t.cursor)
	t.killRing.Push(deleted, true, t.lastAction == "kill")
	t.lastAction = "kill"
	t.value = jsstring.Slice(t.value, t.cursor, jsstring.Length(t.value))
	t.cursor = 0
	t.Invalidate()
}
func (t *TextInput) deleteToLineEnd() {
	if t.cursor >= jsstring.Length(t.value) {
		return
	}
	t.pushUndo()
	deleted := jsstring.Slice(t.value, t.cursor, jsstring.Length(t.value))
	t.killRing.Push(deleted, false, t.lastAction == "kill")
	t.lastAction = "kill"
	t.value = jsstring.Slice(t.value, 0, t.cursor)
	t.Invalidate()
}
func (t *TextInput) deleteWordBackward() {
	if t.cursor == 0 {
		return
	}
	wasKill := t.lastAction == "kill"
	t.pushUndo()
	old := t.cursor
	t.moveWordBackward()
	from := t.cursor
	t.cursor = old
	t.killRing.Push(jsstring.Slice(t.value, from, t.cursor), true, wasKill)
	t.lastAction = "kill"
	t.value = jsstring.Splice(t.value, from, t.cursor, "")
	t.cursor = from
	t.Invalidate()
}
func (t *TextInput) deleteWordForward() {
	if t.cursor >= jsstring.Length(t.value) {
		return
	}
	wasKill := t.lastAction == "kill"
	t.pushUndo()
	old := t.cursor
	t.moveWordForward()
	end := t.cursor
	t.cursor = old
	t.killRing.Push(jsstring.Slice(t.value, t.cursor, end), false, wasKill)
	t.lastAction = "kill"
	t.value = jsstring.Splice(t.value, t.cursor, end, "")
	t.Invalidate()
}
func (t *TextInput) yank() {
	text := t.killRing.Peek()
	if text == "" {
		return
	}
	t.pushUndo()
	t.value = jsstring.Splice(t.value, t.cursor, t.cursor, text)
	t.cursor += jsstring.Length(text)
	t.lastAction = "yank"
	t.Invalidate()
}
func (t *TextInput) yankPop() {
	if t.lastAction != "yank" || t.killRing.Len() <= 1 {
		return
	}
	t.pushUndo()
	previous := t.killRing.Peek()
	length := jsstring.Length(previous)
	t.value = jsstring.Splice(t.value, t.cursor-length, t.cursor, "")
	t.cursor -= length
	t.killRing.Rotate()
	text := t.killRing.Peek()
	t.value = jsstring.Splice(t.value, t.cursor, t.cursor, text)
	t.cursor += jsstring.Length(text)
	t.lastAction = "yank"
	t.Invalidate()
}
func (t *TextInput) moveWordBackward() {
	if t.cursor == 0 {
		return
	}
	t.lastAction = ""
	t.cursor = FindWordBackward(t.value, t.cursor)
	t.Invalidate()
}
func (t *TextInput) moveWordForward() {
	if t.cursor >= jsstring.Length(t.value) {
		return
	}
	t.lastAction = ""
	t.cursor = FindWordForward(t.value, t.cursor)
	t.Invalidate()
}
func (t *TextInput) handlePaste(text string) {
	t.lastAction = ""
	t.pushUndo()
	clean := strings.NewReplacer("\r\n", "", "\r", "", "\n", "", "\t", "    ").Replace(text)
	t.value = jsstring.Splice(t.value, t.cursor, t.cursor, clean)
	t.cursor += jsstring.Length(clean)
	t.Invalidate()
}

// Render returns the prompt, horizontally scrolled value and fake cursor. Cursor slicing follows UTF-16 even when setValue retained a surrogate-half offset.
func (t *TextInput) Render(width int) []string { return []string{t.renderInputLine(width)} }
func (t *TextInput) renderInputLine(width int) string {
	available := width - widthx.VisibleWidth(t.prompt)
	if available <= 0 {
		return widthx.TruncateToWidth(t.prompt, width, "", false)
	}
	marker := ""
	if t.Focused {
		marker = widthx.CursorMarker
	}
	if t.value == "" && t.placeholder != "" {
		placeholder := widthx.TruncateToWidth(t.placeholder, available, "", false)
		at := " "
		if first, _ := widthx.FirstGrapheme(placeholder); first != "" {
			at = first
		}
		after := jsstring.Slice(placeholder, jsstring.Length(at), jsstring.Length(placeholder))
		content := marker + "\x1b[7m" + t.placeholderStyle(at) + "\x1b[27m" + t.placeholderStyle(after)
		return t.prompt + content + strings.Repeat(" ", max(0, available-widthx.VisibleWidth(content)))
	}
	visible := ""
	cursorDisplay := t.cursor
	t.renderedStartColumn = 0
	total := widthx.VisibleWidth(t.value)
	if total < available {
		visible = t.value
	} else {
		scrollWidth := available
		if t.cursor == jsstring.Length(t.value) {
			scrollWidth--
		}
		cursorCol := widthx.VisibleWidth(jsstring.Slice(t.value, 0, t.cursor))
		if scrollWidth > 0 {
			half := scrollWidth / 2
			start := 0
			switch {
			case cursorCol < half:
			case cursorCol > total-half:
				start = max(0, total-scrollWidth)
			default:
				start = max(0, cursorCol-half)
			}
			t.renderedStartColumn = start
			visible = widthx.SliceByColumn(t.value, start, scrollWidth, true)
			cursorDisplay = jsstring.Length(widthx.SliceByColumn(t.value, start, max(0, cursorCol-start), true))
		} else {
			cursorDisplay = 0
		}
	}
	before := jsstring.Slice(visible, 0, cursorDisplay)
	at, _ := widthx.FirstGrapheme(jsstring.Slice(visible, cursorDisplay, jsstring.Length(visible)))
	if at == "" {
		at = " "
	}
	after := jsstring.Slice(visible, cursorDisplay+jsstring.Length(at), jsstring.Length(visible))
	content := before + marker + "\x1b[7m" + at + "\x1b[27m" + after
	return t.prompt + content + strings.Repeat(" ", max(0, available-widthx.VisibleWidth(content)))
}

// HandleMouse places the UTF-16 cursor at the clicked grapheme and requests focus.
func (t *TextInput) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	if event.Type != MousePress || event.Button != MouseButtonLeft || event.Y != 0 {
		return nil
	}
	target := t.renderedStartColumn + max(0, event.X-2)
	column, units := 0, 0
	t.cursor = jsstring.Length(t.value)
	for _, segment := range graphemeSegments(t.value) {
		next := column + segment.Width
		if target < next {
			t.cursor = units
			break
		}
		column = next
		units += jsstring.Length(segment.Text)
	}
	t.lastAction = ""
	t.Invalidate()
	return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Focus: true}}
}
