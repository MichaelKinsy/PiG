package codingagent

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi interactive-mode.ts:6408-6430 uses theme-dim Text, not Markdown or SGR faint.
func TestNameCommandUsesPaddedThemeText(t *testing.T) {
	m := modelPickerTestMode(t)
	sc := m.buildSlashContext(context.Background())
	sc.CurrentSession = func() *Session { return NewSession("name", t.TempDir()) }
	name := ""
	sc.SetSessionName = func(value string) error { name = value; return nil }
	sc.GetSessionName = func() string { return name }
	sc.OnNameChange = nil
	for _, arg := range []string{"**literal**", ""} {
		sc.Args = arg
		if err := nameHandler(sc); err != nil {
			t.Fatal(err)
		}
	}
	rows := m.chatContainer.Render(80)
	want := []string{"", tui.NewPaddedText(tui.ActiveTheme().Fg("dim", "Session name set: **literal**"), 1, 0, nil).Render(80)[0], "", tui.NewPaddedText(tui.ActiveTheme().Fg("dim", "Session name: **literal**"), 1, 0, nil).Render(80)[0]}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Fatalf("name rows = %q; want %q", rows, want)
	}
}

// Drive the production slash callback and component composition, not just a Markdown table renderer.
func TestHotkeysCommandRendersUpstreamTables(t *testing.T) {
	previous := tui.GetTUIKeybindings()
	t.Cleanup(func() { tui.SetKeybindings(previous) })
	m := modelPickerTestMode(t)
	m.keybindings = DefaultKeybindingsManager()
	m.keybindings.SetUserBindings(map[string][]KeyID{"app.model.select": {"ctrl+r"}, "tui.input.submit": {"ctrl+enter"}})
	m.keybindings.syncToTUI()
	if err := hotkeysHandler(m.buildSlashContext(context.Background())); err != nil {
		t.Fatal(err)
	}
	rows := m.chatContainer.Render(100)
	plain := widthx.StripAnsi(strings.Join(rows, "\n"))
	for _, want := range []string{"Keyboard Shortcuts", "Navigation", "Editing", "Other", "Ctrl+R", "Ctrl+Enter", "Run bash command (excluded from context)", "┌", "└"} {
		if !strings.Contains(plain, want) {
			t.Errorf("hotkeys missing %q:\n%s", want, plain)
		}
	}
	if len(rows) < 2 || rows[0] != "" || !strings.Contains(rows[1], strings.Repeat("─", 100)) {
		t.Fatalf("hotkeys frame = %q", rows)
	}
}

func TestHostSpacerSurvivesEditorReplacementAndWidgetClear(t *testing.T) {
	m := modelPickerTestMode(t)
	m.widgetContainer = tui.NewContainer()
	m.syncWidgets(nil)
	slot := tui.NewContainer(m.editor)
	layout := tui.NewContainer(m.widgetContainer, slot)
	for _, component := range []tui.Component{m.editor, tui.NewStaticModelSelectorComponent("", nil, nil, ""), m.editor} {
		slot.SetChildren(component)
		rows := layout.Render(80)
		if rows[0] != "" || widthx.StripAnsi(rows[1]) != strings.Repeat("─", 80) {
			t.Fatalf("%T lost the host spacer: %q", component, rows[:2])
		}
	}
}

func BenchmarkHotkeysCommandRender(b *testing.B) {
	previous := tui.GetTUIKeybindings()
	DefaultKeybindingsManager().syncToTUI()
	b.Cleanup(func() { tui.SetKeybindings(previous) })
	m := &InteractiveMode{chatContainer: tui.NewContainer()}
	b.ReportAllocs()
	for b.Loop() {
		m.chatContainer.Clear()
		m.handleHotkeysCommand()
		m.chatContainer.Render(100)
	}
}

// interactive-mode.ts:6902-6916: extension shortcuts get an "Extensions" table after the built-in ones, each row the capitalized key
// text and the shortcut's description, or its extension path without one. Without shortcuts there is no such table.
func TestHotkeysCommandListsExtensionShortcuts(t *testing.T) {
	previous := tui.GetTUIKeybindings()
	t.Cleanup(func() { tui.SetKeybindings(previous) })
	m := modelPickerTestMode(t)
	m.keybindings = DefaultKeybindingsManager()
	m.keybindings.syncToTUI()
	m.newRunner = inproc.NewRunner([]extension.Extension{{
		Name: "demo",
		Shortcuts: map[extension.KeyID]extension.ExtensionShortcut{
			"ctrl+shift+x": {Shortcut: "ctrl+shift+x", Description: "Do the demo thing", ExtensionPath: "/ext/demo.ts"},
			"alt+q":        {Shortcut: "alt+q", ExtensionPath: "/ext/demo.ts"},
		},
	}}, t.TempDir())
	plain := widthx.StripAnsi(hotkeysMarkdown(m.extensionShortcutsForHotkeys()))
	for _, want := range []string{"**Extensions**\n| Key | Action |\n|-----|--------|\n", "| `Alt+Q` | /ext/demo.ts |", "| `Ctrl+Shift+X` | Do the demo thing |"} {
		if !strings.Contains(plain, want) {
			t.Errorf("hotkeys missing %q:\n%s", want, plain)
		}
	}
	if !strings.HasSuffix(plain, "| `Ctrl+Shift+X` | Do the demo thing |") {
		t.Errorf("the Extensions table is not last:\n%s", plain)
	}
	if err := hotkeysHandler(m.buildSlashContext(context.Background())); err != nil {
		t.Fatal(err)
	}
	if rendered := widthx.StripAnsi(strings.Join(m.chatContainer.Render(100), "\n")); !strings.Contains(rendered, "Do the demo thing") {
		t.Fatalf("the /hotkeys command did not list the extension shortcut:\n%s", rendered)
	}
	if none := hotkeysMarkdown(nil); strings.Contains(none, "Extensions") {
		t.Fatalf("a table without shortcuts:\n%s", none)
	}
}
