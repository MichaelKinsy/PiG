package tui

import (
	"maps"
	"strings"
	"testing"
)

// Pi's requestRender(true) clears the physical buffer after an external program has written to the terminal; treating the next render as a fresh append leaves the old dialog visible.
func TestMainScreenExternalEditorRepaint(t *testing.T) {
	var output strings.Builder
	ui := NewWithOutput(&output, 80, 24)
	ui.Add(&fixedLinesComponent{lines: []string{"dialog"}})
	ui.Render()
	output.Reset()
	ui.Stop()
	ui.Start()
	ui.RepaintAll()
	ui.Stop()
	if !strings.Contains(output.String(), "\x1b[2J") || !strings.Contains(output.String(), "dialog") {
		t.Fatalf("external-editor repaint did not clear and restore the dialog: %q", output.String())
	}
}

func BenchmarkEditorRemoteCachedFrame(b *testing.B) {
	e := NewEditor()
	e.SetRemote(&recordingRemote{})
	e.SetRemoteFrame([]string{strings.Repeat("─", 120), "draft text", strings.Repeat("─", 120)}, 120, false)
	b.ReportAllocs()
	for b.Loop() {
		e.Render(120)
	}
}

// Pi extension-editor.ts:92-99 includes the external-editor app binding in the hint.
func TestExtensionEditorExternalHint(t *testing.T) {
	c := NewExtensionEditorComponent("Edit", "first\nsecond")
	if !strings.Contains(strings.Join(c.Render(100), "\n"), "external editor") {
		t.Fatal("external editor hint missing")
	}
}

// Pi extension-editor.ts:114-137 consumes the app binding, preserves multiline text and keeps the dialog open after completion.
func TestExtensionEditorExternalAction(t *testing.T) {
	old := GetKeybindings()
	t.Cleanup(func() { SetKeybindings(old) })
	defs := maps.Clone(tuiKeybindingDefs)
	defs["app.editor.external"] = TUIKeybindingDef{DefaultKeys: []string{"ctrl+x"}}
	SetKeybindings(NewKeybindingsManager(defs, nil))
	c := NewExtensionEditorComponent("Edit", "first\nsecond")
	var complete func(string)
	c.SetExternalEditor(func(content string, apply func(string)) {
		if content != "first\nsecond" {
			t.Errorf("content = %q", content)
		}
		complete = apply
	})
	c.HandleInput("\x18")
	if complete == nil || c.Done() {
		t.Fatal("external editor did not open")
	}
	complete("changed\ntext")
	c.HandleInput("\r")
	if !c.Done() || c.Value() != "changed\ntext" {
		t.Fatalf("submitted %q", c.Value())
	}
	complete("late")
	if c.Value() != "changed\ntext" {
		t.Fatal("late edit changed completed dialog")
	}
}
