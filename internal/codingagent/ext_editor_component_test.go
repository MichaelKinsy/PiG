package codingagent

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// factoryEditor is a Pi editor component of the host process (a pi-tui EditorComponent): it keeps its own text, renders it and reports changes and
// submits through the callbacks the host installs.
type factoryEditor struct {
	text     string
	keys     []string
	rendered []int
	onSubmit func(string)
	onChange func(string)
}

func (e *factoryEditor) Render(width int) []string {
	e.rendered = append(e.rendered, width)
	return []string{"factory-editor:" + e.text}
}
func (*factoryEditor) Invalidate()                  {}
func (e *factoryEditor) Text() string               { return e.text }
func (e *factoryEditor) SetText(t string)           { e.text = t }
func (e *factoryEditor) SetOnSubmit(f func(string)) { e.onSubmit = f }
func (e *factoryEditor) SetOnChange(f func(string)) { e.onChange = f }
func (e *factoryEditor) HandleInput(data string) {
	e.keys = append(e.keys, data)
	if data == "\r" {
		if e.onSubmit != nil {
			e.onSubmit(e.text)
		}
		return
	}
	e.text += data
	if e.onChange != nil {
		e.onChange(e.text)
	}
}

// interactive-mode.ts:2875-2890 setCustomEditorComponent: the factory is called with (ui, getEditorTheme(), keybindings) and its component takes the
// editor's place: the editor's text and callbacks follow it, every key goes to the component's handleInput, and getEditorComponent returns the
// factory. A nil factory restores the default editor.
func TestExtUIContextEditorFactoryBuildsTheComponentThatTakesTheEditorsPlace(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	m.uiTaskCh = make(chan func(), 64)
	m.editor.Focused = true
	m.editor.SetText("carried")
	var submitted []string
	m.editor.OnSubmit = func(text string) { submitted = append(submitted, text) }
	ui := &ExtUIContext{m: m}
	if ui.GetEditorComponent() != nil {
		t.Fatal("a default editor has no factory")
	}

	component := &factoryEditor{}
	var gotHost tui.TUI
	var gotTheme tui.EditorTheme
	var gotKeybindings extension.KeybindingsManager
	factory := extension.EditorFactory(func(host tui.TUI, theme tui.EditorTheme, keybindings extension.KeybindingsManager) tui.EditorComponent {
		gotHost, gotTheme, gotKeybindings = host, theme, keybindings
		return component
	})
	ui.SetEditorComponent(factory)
	// interactive-mode.ts:2876 records the factory when it is set, before the editor is rebuilt: getEditorComponent needs no loop turn.
	if got := ui.GetEditorComponent(); got == nil {
		t.Fatal("GetEditorComponent right after SetEditorComponent = nil, want the factory just set")
	}
	runPostedTasks(t, m)

	if gotHost != m.tuiInst || gotKeybindings == nil || gotTheme.BorderColor == nil || gotTheme.BorderColor("x") == "" {
		t.Fatalf("factory got host=%v theme=%+v keybindings=%v, want the TUI, getEditorTheme() and the keybindings", gotHost, gotTheme, gotKeybindings)
	}
	if got := ui.GetEditorComponent(); got == nil || extension.EditorFactory(got) == nil {
		t.Fatalf("GetEditorComponent = %v, want the installed factory", got)
	}
	if component.text != "carried" || !m.editor.IsRemote() {
		t.Fatalf("component text %q remote=%v, want the editor's text carried to the component", component.text, m.editor.IsRemote())
	}

	if err := m.dispatchKey(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	runPostedTasks(t, m)
	if !slices.Equal(component.keys, []string{"x"}) || m.editor.Text() != "carriedx" {
		t.Fatalf("keys %q, host text %q; want the key in the component and its change mirrored", component.keys, m.editor.Text())
	}
	if got := m.editor.Render(40); !slices.Equal(got, []string{"factory-editor:carriedx"}) {
		t.Fatalf("editor rows = %q, want the component's render", got)
	}

	if err := m.dispatchKey(context.Background(), "\r"); err != nil {
		t.Fatal(err)
	}
	runPostedTasks(t, m)
	if !slices.Equal(submitted, []string{"carriedx"}) {
		t.Fatalf("submitted %q, want the component's submit to reach the editor's handler", submitted)
	}

	ui.SetEditorComponent(nil)
	runPostedTasks(t, m)
	if m.editor.IsRemote() || ui.GetEditorComponent() != nil || m.editor.Text() != "carriedx" {
		t.Fatalf("after nil: remote=%v factory=%v text=%q; want the default editor with the text", m.editor.IsRemote(), ui.GetEditorComponent() != nil, m.editor.Text())
	}
}

// interactive-mode.ts:2886-2896: the custom editor is the editor container's child, so the TUI renders it on every pass (a component that calls
// tui.requestRender when a timer changes it shows the change without a key). The host editor renders the installed component at the row's width.
func TestExtUIContextEditorFactoryComponentRendersOnEveryPassAtTheRowsWidth(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	m.uiTaskCh = make(chan func(), 64)
	m.editor.Focused = true
	ui := &ExtUIContext{m: m}
	component := &factoryEditor{}
	ui.SetEditorComponent(func(tui.TUI, tui.EditorTheme, extension.KeybindingsManager) tui.EditorComponent { return component })
	runPostedTasks(t, m)

	component.text = "ticked"
	component.rendered = nil
	if got := m.editor.Render(33); !slices.Equal(got, []string{"factory-editor:ticked"}) || !slices.Equal(component.rendered, []int{33}) {
		t.Fatalf("rows %q rendered at %v; want the component's current render at width 33 with no key", got, component.rendered)
	}
}
