package tui

import "testing"

// minimalEditor implements only the required members of EditorComponent, as a small extension editor would.
type minimalEditor struct {
	BaseComponent
	text     string
	onSubmit func(string)
	onChange func(string)
}

func (m *minimalEditor) Render(int) []string        { return []string{m.text} }
func (m *minimalEditor) Text() string               { return m.text }
func (m *minimalEditor) SetText(text string)        { m.text = text; m.onChange(text) }
func (m *minimalEditor) SetOnSubmit(f func(string)) { m.onSubmit = f }
func (m *minimalEditor) SetOnChange(f func(string)) { m.onChange = f }
func (m *minimalEditor) HandleInput(data string) {
	if data == "\r" {
		m.onSubmit(m.text)
		return
	}
	m.SetText(m.text + data)
}

// upstream: editor-component.ts:11: the application hosts any editor through the required members, and the optional ones are found by assertion with getText as the fallback for expanded text.
func TestEditorComponentRequiredAndOptionalMembers(t *testing.T) {
	var submitted, changes []string
	var editor EditorComponent = &minimalEditor{}
	editor.SetOnSubmit(func(text string) { submitted = append(submitted, text) })
	editor.SetOnChange(func(text string) { changes = append(changes, text) })
	editor.HandleInput("h")
	editor.HandleInput("i")
	editor.HandleInput("\r")
	if editor.Text() != "hi" || len(submitted) != 1 || submitted[0] != "hi" || len(changes) != 2 {
		t.Fatalf("text=%q submitted=%v changes=%v", editor.Text(), submitted, changes)
	}
	if _, ok := editor.(EditorWithHistory); ok {
		t.Error("a minimal editor has no history")
	}
	if got := ExpandedEditorText(editor); got != "hi" {
		t.Errorf("ExpandedEditorText without an expanded form = %q, want Text", got)
	}
	if lines := editor.Render(10); len(lines) != 1 || lines[0] != "hi" {
		t.Errorf("render = %q", lines)
	}
}

// Pi source: packages/tui/src/editor-component.ts
// mutation-checked: zeroing the results of EditorComponent.SetText fails it
// Pi: packages/tui/src/components/editor.ts:1114 (setText)
// packages/tui/src/editor-component.ts:20 `setText(text)` replaces the content and fires onChange; :47 `insertTextAtCursor?(text)` inserts at the cursor.
// packages/tui/src/components/editor.ts:1114,1133 (Editor.setText, insertTextAtCursor): the built-in editor satisfies EditorComponent with both members.
func TestBuiltInEditorIsAFullEditorComponent(t *testing.T) {
	built := NewEditor()
	var component EditorComponent = built
	var changed []string
	component.SetOnChange(func(text string) { changed = append(changed, text) })
	var submitted string
	component.SetOnSubmit(func(text string) { submitted = text })
	component.SetText("hello")
	if component.Text() != "hello" || len(changed) == 0 || changed[len(changed)-1] != "hello" {
		t.Fatalf("text=%q changes=%v", component.Text(), changed)
	}
	if inserter, ok := component.(EditorWithTextInsertion); ok {
		inserter.InsertTextAtCursor("!")
	} else {
		t.Fatal("the built-in editor inserts text at the cursor")
	}
	if got := ExpandedEditorText(component); got != "hello!" {
		t.Errorf("expanded text = %q, want hello!", got)
	}
	component.HandleInput("\r")
	if submitted != "hello!" {
		t.Errorf("submitted = %q", submitted)
	}
	if _, ok := component.(EditorWithHistory); !ok {
		t.Error("the built-in editor keeps history")
	}
}
