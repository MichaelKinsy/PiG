package tui

import (
	"slices"
	"testing"
)

// keybindings.ts TUI_KEYBINDINGS: the default table getKeybindings() resolves until an application installs its own.
func TestTUIKeybindingsTableDefaults(t *testing.T) {
	cases := map[string][]string{
		"tui.editor.cursorUp":       {"up"},
		"tui.editor.cursorLeft":     {"left", "ctrl+b"},
		"tui.editor.jumpForward":    {"ctrl+]"},
		"tui.editor.historyNext":    {},
		"tui.editor.pageUp":         {"pageUp", "ctrl+pageUp"},
		"tui.editor.cursorWordLeft": {"alt+left", "ctrl+left", "alt+b"},
	}
	for id, want := range cases {
		def, ok := TUIKeybindings[id]
		if !ok {
			t.Fatalf("TUIKeybindings has no %q", id)
		}
		if !slices.Equal(def.DefaultKeys, want) {
			t.Errorf("%s default keys = %v, want %v", id, def.DefaultKeys, want)
		}
	}
	if TerminalColorSchemeDark != "dark" || TerminalColorSchemeLight != "light" {
		t.Fatal("TerminalColorScheme constants differ from terminal-colors.ts")
	}
	if ParseTerminalColorSchemeReport("\x1b[?997;2n") != TerminalColorSchemeLight || ParseTerminalColorSchemeReport("\x1b[?997;1n") != TerminalColorSchemeDark {
		t.Fatal("scheme report parse")
	}
}
