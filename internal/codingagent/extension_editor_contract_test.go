package codingagent

import (
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// extension-editor.ts handleInput always consumes the app.editor.external key: it starts handleOpenExternalEditor and returns, so the key never reaches the
// Editor. A host that has bound no handoff therefore still swallows the key; it is not typed into the text.
func TestExtensionEditorExternalKeyIsConsumedWithoutAHandoff(t *testing.T) {
	restoreTUIKeybindings(t)
	c := newExtensionEditorProbe(t, "Edit", "draft", map[string][]string{"app.editor.external": {"x"}}, nil)
	c.HandleInput("x")
	if got := c.editor.Text(); got != "draft" || c.done {
		t.Fatalf("text=%q done=%v after the external key with no handoff, want %q and false", got, c.done, "draft")
	}
	c.HandleInput("y")
	if got := c.editor.Text(); got != "drafty" {
		t.Fatalf("text=%q after a following ordinary key, want %q", got, "drafty")
	}
}

// Every press of the external key opens the handoff once with the Editor's current text, and no press reaches the Editor.
func TestExtensionEditorExternalKeyOpensTheHandoffOncePerPress(t *testing.T) {
	restoreTUIKeybindings(t)
	c := newExtensionEditorProbe(t, "Edit", "", map[string][]string{"app.editor.external": {"x"}}, nil)
	var contents []string
	c.SetExternalEditor(func(_, content string, _ func(string), _ func(string)) { contents = append(contents, content) })
	c.HandleInput("a")
	c.HandleInput("x")
	c.HandleInput("b")
	c.HandleInput("x")
	if len(contents) != 2 || contents[0] != "a" || contents[1] != "ab" {
		t.Fatalf("handoff contents = %q, want [a ab]", contents)
	}
	if got := c.editor.Text(); got != "ab" {
		t.Fatalf("text = %q, want ab (the external key must not be typed)", got)
	}
}

// extension-editor.ts handleOpenExternalEditor: a failed edit shows `theme.fg("warning", message)` on the status row and a later completed edit clears it.
func TestExtensionEditorExternalEditorStatusRow(t *testing.T) {
	restoreTUIKeybindings(t)
	c := newExtensionEditorProbe(t, "Edit", "draft", map[string][]string{"app.editor.external": {"ctrl+x"}}, nil)
	var apply func(string)
	var report func(string)
	c.SetExternalEditor(func(_, _ string, a func(string), r func(string)) { apply, report = a, r })
	c.HandleInput("\x18")
	report("editor exited with status 3")
	rendered := strings.Join(c.Render(80), "\n")
	if !strings.Contains(rendered, tui.ActiveTheme().Fg("warning", "editor exited with status 3")) {
		t.Fatalf("the failure is not on the status row in the warning color:\n%q", rendered)
	}
	if got := c.editor.Text(); got != "draft" {
		t.Fatalf("a failed edit changed the text to %q", got)
	}
	c.HandleInput("\x18")
	apply("done")
	if rendered := widthx.StripAnsi(strings.Join(c.Render(80), "\n")); strings.Contains(rendered, "editor exited") || !strings.Contains(rendered, "done") {
		t.Fatalf("a completed edit leaves the failure or loses the text:\n%s", rendered)
	}
}

// The cancel key reaches onCancel once, before the Editor sees it, and the callback is the dialog's only effect: no text is submitted.
func TestExtensionEditorCancelCallsOnCancelOnce(t *testing.T) {
	c := newExtensionEditorProbe(t, "Edit", "kept", nil, nil)
	cancels, submits := 0, 0
	c.onCancel = func() { cancels++ }
	c.onSubmit = func(string) { submits++ }
	c.HandleInput("\x1b")
	c.HandleInput("\x03")
	if cancels != 1 || submits != 0 {
		t.Fatalf("cancels=%d submits=%d, want 1 and 0", cancels, submits)
	}
}

// extension-editor.ts handleInput resolves tui.select.cancel through the process-wide manager (getKeybindings()) and app.editor.external through the manager
// the constructor received (this.keybindings): with two different managers, each action follows its own.
func TestExtensionEditorResolvesCancelGloballyAndTheExternalKeyThroughItsOwnManager(t *testing.T) {
	previous := tui.GetKeybindings()
	t.Cleanup(func() { tui.SetKeybindings(previous) })
	own := NewKeybindingsManagerFromBindings(map[string][]KeyID{"app.editor.external": {"x"}}, "")
	NewKeybindingsManagerFromBindings(map[string][]KeyID{tui.KBSelectCancel: {"ctrl+q"}}, "") // installs the global bindings
	c := &extensionEditorProbe{}
	c.ui = &forcedRenderCounter{TUI: tui.NewWithOutput(io.Discard, 80, 40)}
	c.ExtensionEditorComponent = NewExtensionEditorComponent(c.ui, own, "Edit", "draft",
		func(text string) { c.done, c.value = true, text },
		func() { c.done, c.cancelled = true, true },
		nil, "ed-command")
	opened := 0
	c.SetExternalEditor(func(string, string, func(string), func(string)) { opened++ })
	c.HandleInput("x")
	if opened != 1 || c.editor.Text() != "draft" {
		t.Fatalf("external key from the component's own manager: opened=%d text=%q, want 1 and draft", opened, c.editor.Text())
	}
	c.HandleInput("\x1b")
	if c.done {
		t.Fatal("escape cancelled although the global manager rebinds cancel to ctrl+q")
	}
	c.HandleInput("\x11")
	if !c.done || !c.cancelled {
		t.Fatalf("ctrl+q did not cancel: done=%v cancelled=%v", c.done, c.cancelled)
	}
}
