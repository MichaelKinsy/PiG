package tui

// EditorComponent is what a custom editor (an extension's vim or emacs mode, custom keybindings) provides so the application can host it in place of the built-in [Editor].
//
// Pi's required `onSubmit` and `onChange` callback properties are the SetOnSubmit and SetOnChange methods; the optional members are the small interfaces below, which the application asks for with a type assertion before using them. Pi's `getText` is Text, as on [Editor].
//
// upstream: packages/tui/src/editor-component.ts:11
type EditorComponent interface {
	Component
	// Text returns the current text content.
	Text() string
	// SetText replaces the text content.
	SetText(text string)
	// HandleInput handles raw terminal input (key presses, paste sequences).
	HandleInput(data string)
	// SetOnSubmit installs the callback run when the user submits (Enter).
	SetOnSubmit(callback func(text string))
	// SetOnChange installs the callback run when the text changes.
	SetOnChange(callback func(text string))
}

// EditorWithHistory is an [EditorComponent] with up/down history navigation.
type EditorWithHistory interface {
	EditorComponent
	AddToHistory(text string)
}

// EditorWithTextInsertion is an [EditorComponent] that inserts text at the cursor.
type EditorWithTextInsertion interface {
	EditorComponent
	InsertTextAtCursor(text string)
}

// EditorWithExpandedText is an [EditorComponent] whose text can carry markers (such as paste markers) that expand to their full content. Without it the application falls back to Text.
type EditorWithExpandedText interface {
	EditorComponent
	GetExpandedText() string
}

// EditorWithAutocomplete is an [EditorComponent] that takes an autocomplete provider.
type EditorWithAutocomplete interface {
	EditorComponent
	SetAutocomplete(provider AutocompleteProvider)
}

// EditorWithAppearance is an [EditorComponent] with adjustable padding and autocomplete height.
type EditorWithAppearance interface {
	EditorComponent
	SetPaddingX(padding int)
	SetAutocompleteMaxVisible(maxVisible int)
}

// ExpandedEditorText returns the editor's text with markers expanded, falling back to Text when the editor has no expanded form.
//
// upstream: editor-component.ts:42 (getExpandedText "Falls back to getText() if not implemented")
func ExpandedEditorText(editor EditorComponent) string {
	if expanding, ok := editor.(EditorWithExpandedText); ok {
		return expanding.GetExpandedText()
	}
	return editor.Text()
}

// SetOnSubmit installs the submit callback.
func (e *Editor) SetOnSubmit(callback func(text string)) { e.OnSubmit = callback }

// SetOnChange installs the change callback.
func (e *Editor) SetOnChange(callback func(text string)) { e.OnChange = callback }

var (
	_ EditorWithHistory       = (*Editor)(nil)
	_ EditorWithTextInsertion = (*Editor)(nil)
	_ EditorWithExpandedText  = (*Editor)(nil)
	_ EditorWithAutocomplete  = (*Editor)(nil)
	_ EditorWithAppearance    = (*Editor)(nil)
)
