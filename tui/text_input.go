package tui

// text_input.go: bare single-line text input.
//
// Mirrors upstream packages/tui/src/components/input.ts for input semantics
// and render surface: a single `> ` prompt line with horizontal scrolling,
// fake cursor, and optional hardware cursor marker.

import (
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type textInputState struct {
	value  string
	cursor int
}

// TextInput is a bare single-line text input component.
type TextInput struct {
	invalidatable
	value  string
	cursor int // byte offset, always kept on grapheme boundary

	done   bool
	cancel bool

	Focused bool

	isInPaste   bool
	pasteBuffer string

	killRing   KillRing
	lastAction string // kill | yank | type-word | ""
	undoStack  UndoStack[textInputState]

	prompt              string
	placeholder         string
	placeholderStyle    func(text string) string
	renderedStartColumn int

	// callbacks selects upstream Input's submit/escape contract: Enter and
	// cancel call OnSubmit/OnEscape instead of latching Done. Set by NewInput.
	callbacks bool
	OnSubmit  func(value string)
	OnEscape  func()
}

// InputOptions mirrors upstream InputOptions. A nil Prompt takes upstream's
// "> " default; nil PlaceholderStyle leaves the placeholder unstyled.
type InputOptions struct {
	Prompt           *string
	Placeholder      string
	PlaceholderStyle func(text string) string
}

// NewTextInput creates a text input with the given title/prompt.
func NewTextInput(title string) *TextInput {
	_ = title // retained for call-site compatibility while Render matches upstream Input.
	return &TextInput{Focused: true, prompt: "> ", placeholderStyle: func(text string) string { return text }}
}

// NewInput creates an input with upstream Input's options and its
// OnSubmit/OnEscape contract. Mirrors upstream new Input(options).
func NewInput(options InputOptions) *TextInput {
	input := NewTextInput("")
	input.callbacks = true
	if options.Prompt != nil {
		input.prompt = *options.Prompt
	}
	input.placeholder = options.Placeholder
	if options.PlaceholderStyle != nil {
		input.placeholderStyle = options.PlaceholderStyle
	}
	return input
}

// Done reports whether the input has been confirmed or cancelled.
func (t *TextInput) Done() bool { return t.done }

// Cancelled reports whether the user pressed Esc.
func (t *TextInput) Cancelled() bool { return t.cancel }

// Text returns the current input text.
func (t *TextInput) Text() string { return t.value }

// SetText pre-fills the input.
func (t *TextInput) SetText(s string) {
	t.value = s
	t.cursor = len(s)
	t.Invalidate()
}

func (t *TextInput) pushUndo() {
	t.undoStack.Push(textInputState{value: t.value, cursor: t.cursor})
}

func (t *TextInput) undo() {
	last, ok := t.undoStack.Pop()
	if !ok {
		return
	}
	t.value = last.value
	t.cursor = last.cursor
	t.lastAction = ""
	t.Invalidate()
}

// HandleInput processes a key event.
func (t *TextInput) HandleInput(data string) {
	if t.done {
		return
	}

	if strings.Contains(data, "\x1b[200~") {
		t.isInPaste = true
		t.pasteBuffer = ""
		data = strings.ReplaceAll(data, "\x1b[200~", "")
	}
	if t.isInPaste {
		t.pasteBuffer += data
		if end := strings.Index(t.pasteBuffer, "\x1b[201~"); end != -1 {
			t.handlePaste(t.pasteBuffer[:end])
			remaining := t.pasteBuffer[end+len("\x1b[201~"):]
			t.isInPaste = false
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
			return
		}
		t.cancel = true
		t.done = true
		t.Invalidate()
		return
	case kb.Matches(data, KBEditorUndo):
		t.undo()
		return
	case kb.Matches(data, KBInputSubmit) || data == "\n":
		if t.callbacks {
			if t.OnSubmit != nil {
				t.OnSubmit(t.value)
			}
			return
		}
		t.done = true
		t.Invalidate()
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
		t.cursor = previousGraphemeStart(t.value, t.cursor)
		t.Invalidate()
		return
	case kb.Matches(data, KBEditorCursorRight):
		t.lastAction = ""
		t.cursor = nextGraphemeEnd(t.value, t.cursor)
		t.Invalidate()
		return
	case kb.Matches(data, KBEditorCursorLineStart):
		t.lastAction = ""
		t.cursor = 0
		t.Invalidate()
		return
	case kb.Matches(data, KBEditorCursorLineEnd):
		t.lastAction = ""
		t.cursor = len(t.value)
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

	hasControlChars := false
	for _, ch := range data {
		code := ch
		if code < 32 || code == 0x7f || (code >= 0x80 && code <= 0x9f) {
			hasControlChars = true
			break
		}
	}
	if !hasControlChars {
		t.insertCharacter(data)
	}
}

func (t *TextInput) insertCharacter(char string) {
	if char == "" {
		return
	}
	if isWhitespaceGrapheme(char) || t.lastAction != "type-word" {
		t.pushUndo()
	}
	t.lastAction = "type-word"
	t.value = t.value[:t.cursor] + char + t.value[t.cursor:]
	t.cursor += len(char)
	t.Invalidate()
}

func (t *TextInput) handleBackspace() {
	if t.cursor == 0 {
		return
	}
	t.lastAction = ""
	t.pushUndo()
	start := previousGraphemeStart(t.value, t.cursor)
	t.value = t.value[:start] + t.value[t.cursor:]
	t.cursor = start
	t.Invalidate()
}

func (t *TextInput) handleForwardDelete() {
	if t.cursor >= len(t.value) {
		return
	}
	t.lastAction = ""
	t.pushUndo()
	end := nextGraphemeEnd(t.value, t.cursor)
	t.value = t.value[:t.cursor] + t.value[end:]
	t.Invalidate()
}

func (t *TextInput) deleteToLineStart() {
	if t.cursor == 0 {
		return
	}
	t.pushUndo()
	deleted := t.value[:t.cursor]
	t.killRing.Push(deleted, true, t.lastAction == "kill")
	t.lastAction = "kill"
	t.value = t.value[t.cursor:]
	t.cursor = 0
	t.Invalidate()
}

func (t *TextInput) deleteToLineEnd() {
	if t.cursor >= len(t.value) {
		return
	}
	t.pushUndo()
	deleted := t.value[t.cursor:]
	t.killRing.Push(deleted, false, t.lastAction == "kill")
	t.lastAction = "kill"
	t.value = t.value[:t.cursor]
	t.Invalidate()
}

func (t *TextInput) deleteWordBackward() {
	if t.cursor == 0 {
		return
	}
	wasKill := t.lastAction == "kill"
	t.pushUndo()
	oldCursor := t.cursor
	t.moveWordBackward()
	deleteFrom := t.cursor
	t.cursor = oldCursor
	deleted := t.value[deleteFrom:t.cursor]
	t.killRing.Push(deleted, true, wasKill)
	t.lastAction = "kill"
	t.value = t.value[:deleteFrom] + t.value[t.cursor:]
	t.cursor = deleteFrom
	t.Invalidate()
}

func (t *TextInput) deleteWordForward() {
	if t.cursor >= len(t.value) {
		return
	}
	wasKill := t.lastAction == "kill"
	t.pushUndo()
	oldCursor := t.cursor
	t.moveWordForward()
	deleteTo := t.cursor
	t.cursor = oldCursor
	deleted := t.value[t.cursor:deleteTo]
	t.killRing.Push(deleted, false, wasKill)
	t.lastAction = "kill"
	t.value = t.value[:t.cursor] + t.value[deleteTo:]
	t.Invalidate()
}

func (t *TextInput) yank() {
	text := t.killRing.Peek()
	if text == "" {
		return
	}
	t.pushUndo()
	t.value = t.value[:t.cursor] + text + t.value[t.cursor:]
	t.cursor += len(text)
	t.lastAction = "yank"
	t.Invalidate()
}

func (t *TextInput) yankPop() {
	if t.lastAction != "yank" || t.killRing.Len() <= 1 {
		return
	}
	t.pushUndo()
	prev := t.killRing.Peek()
	if prev != "" && t.cursor >= len(prev) {
		t.value = t.value[:t.cursor-len(prev)] + t.value[t.cursor:]
		t.cursor -= len(prev)
	}
	t.killRing.Rotate()
	text := t.killRing.Peek()
	t.value = t.value[:t.cursor] + text + t.value[t.cursor:]
	t.cursor += len(text)
	t.lastAction = "yank"
	t.Invalidate()
}

func (t *TextInput) moveWordBackward() {
	if t.cursor == 0 {
		return
	}
	t.lastAction = ""
	for t.cursor > 0 {
		start := previousGraphemeStart(t.value, t.cursor)
		seg := t.value[start:t.cursor]
		if !isWhitespaceGrapheme(seg) {
			break
		}
		t.cursor = start
	}
	if t.cursor == 0 {
		t.Invalidate()
		return
	}
	start := previousGraphemeStart(t.value, t.cursor)
	seg := t.value[start:t.cursor]
	if isPunctuationGrapheme(seg) {
		for t.cursor > 0 {
			start = previousGraphemeStart(t.value, t.cursor)
			seg = t.value[start:t.cursor]
			if !isPunctuationGrapheme(seg) {
				break
			}
			t.cursor = start
		}
	} else {
		for t.cursor > 0 {
			start = previousGraphemeStart(t.value, t.cursor)
			seg = t.value[start:t.cursor]
			if isWhitespaceGrapheme(seg) || isPunctuationGrapheme(seg) {
				break
			}
			t.cursor = start
		}
	}
	t.Invalidate()
}

func (t *TextInput) moveWordForward() {
	if t.cursor >= len(t.value) {
		return
	}
	t.lastAction = ""
	for t.cursor < len(t.value) {
		end := nextGraphemeEnd(t.value, t.cursor)
		seg := t.value[t.cursor:end]
		if !isWhitespaceGrapheme(seg) {
			break
		}
		t.cursor = end
	}
	if t.cursor >= len(t.value) {
		t.Invalidate()
		return
	}
	end := nextGraphemeEnd(t.value, t.cursor)
	seg := t.value[t.cursor:end]
	if isPunctuationGrapheme(seg) {
		for t.cursor < len(t.value) {
			end = nextGraphemeEnd(t.value, t.cursor)
			seg = t.value[t.cursor:end]
			if !isPunctuationGrapheme(seg) {
				break
			}
			t.cursor = end
		}
	} else {
		for t.cursor < len(t.value) {
			end = nextGraphemeEnd(t.value, t.cursor)
			seg = t.value[t.cursor:end]
			if isWhitespaceGrapheme(seg) || isPunctuationGrapheme(seg) {
				break
			}
			t.cursor = end
		}
	}
	t.Invalidate()
}

func (t *TextInput) handlePaste(pastedText string) {
	t.lastAction = ""
	t.pushUndo()
	clean := strings.ReplaceAll(pastedText, "\r\n", "")
	clean = strings.ReplaceAll(clean, "\r", "")
	clean = strings.ReplaceAll(clean, "\n", "")
	clean = strings.ReplaceAll(clean, "\t", "    ")
	t.value = t.value[:t.cursor] + clean + t.value[t.cursor:]
	t.cursor += len(clean)
	t.Invalidate()
}

// Render produces the upstream bare input surface: a single prompt line.
func (t *TextInput) Render(width int) []string {
	return []string{t.renderInputLine(width)}
}

func (t *TextInput) renderInputLine(width int) string {
	prompt := t.prompt
	availableWidth := width - widthx.VisibleWidth(prompt)
	if availableWidth <= 0 {
		return widthx.TruncateToWidth(prompt, width, "", false)
	}

	marker := ""
	if t.Focused {
		marker = widthx.CursorMarker
	}
	if t.value == "" && t.placeholder != "" {
		placeholder := widthx.TruncateToWidth(t.placeholder, availableWidth, "", false)
		atCursor := " "
		if segments := graphemeSegments(placeholder); len(segments) > 0 {
			atCursor = segments[0].Text
		}
		afterCursor := placeholder[min(len(atCursor), len(placeholder)):]
		textWithCursor := marker + "\x1b[7m" + t.placeholderStyle(atCursor) + "\x1b[27m" + t.placeholderStyle(afterCursor)
		padding := strings.Repeat(" ", max(0, availableWidth-widthx.VisibleWidth(textWithCursor)))
		return prompt + textWithCursor + padding
	}

	visibleText := ""
	cursorDisplay := t.cursor
	t.renderedStartColumn = 0
	totalWidth := widthx.VisibleWidth(t.value)
	if totalWidth < availableWidth {
		visibleText = t.value
	} else {
		scrollWidth := availableWidth
		if t.cursor == len(t.value) {
			scrollWidth = availableWidth - 1
		}
		cursorCol := widthx.VisibleWidth(t.value[:t.cursor])
		if scrollWidth > 0 {
			halfWidth := scrollWidth / 2
			startCol := 0
			switch {
			case cursorCol < halfWidth:
				startCol = 0
			case cursorCol > totalWidth-halfWidth:
				startCol = max(0, totalWidth-scrollWidth)
			default:
				startCol = max(0, cursorCol-halfWidth)
			}
			t.renderedStartColumn = startCol
			visibleText = widthx.SliceByColumn(t.value, startCol, scrollWidth, true)
			beforeCursor := widthx.SliceByColumn(t.value, startCol, max(0, cursorCol-startCol), true)
			cursorDisplay = len(beforeCursor)
		} else {
			visibleText = ""
			cursorDisplay = 0
		}
	}

	beforeCursor := visibleText[:cursorDisplay]
	atCursor := " "
	afterCursor := ""
	if cursorDisplay < len(visibleText) {
		seg := graphemeAt(visibleText, cursorDisplay)
		if seg.End > cursorDisplay {
			atCursor = visibleText[cursorDisplay:seg.End]
			afterCursor = visibleText[seg.End:]
		}
	}

	cursorChar := "\x1b[7m" + atCursor + "\x1b[27m"
	textWithCursor := beforeCursor + marker + cursorChar + afterCursor
	padding := strings.Repeat(" ", max(0, availableWidth-widthx.VisibleWidth(textWithCursor)))
	return prompt + textWithCursor + padding
}

// HandleMouse moves the cursor to a left press on the input row and requests
// focus. Mirrors upstream Input.handleMouse.
func (t *TextInput) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	if event.Type != MousePress || event.Button != MouseButtonLeft || event.Y != 0 {
		return nil
	}
	targetColumn := t.renderedStartColumn + max(0, event.X-2)
	currentColumn := 0
	t.cursor = len(t.value)
	for _, grapheme := range graphemeSegments(t.value) {
		nextColumn := currentColumn + widthx.VisibleWidth(grapheme.Text)
		if targetColumn < nextColumn {
			t.cursor = grapheme.Start
			break
		}
		currentColumn = nextColumn
	}
	t.lastAction = ""
	t.Invalidate()
	return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Focus: true}}
}
