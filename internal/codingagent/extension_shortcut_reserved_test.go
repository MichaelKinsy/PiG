package codingagent

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi: interactive-mode.ts:2242 and :6903 pass keybindings.getEffectiveConfig() to extensionRunner.getShortcuts, whose
// reserved set (runner.ts RESERVED_KEYBINDINGS) holds tui.input.submit, tui.select.confirm and the other tui.* actions
// besides the app.* ones. An extension shortcut on a key a reserved tui.* action owns is skipped with a warning; the app-only
// table the mode used before did not carry the tui.* actions, so such a shortcut was bound and shadowed the editor's Enter.
func TestExtensionShortcutsConflictWithReservedTUIKeybindings(t *testing.T) {
	mode, _ := newCustomEditorDispatchMode(t)
	mode.opts.AgentDir = t.TempDir()
	handler := func(context.Context) error { return nil }
	mode.newRunner = inproc.NewRunner([]extension.Extension{{
		Name: "shortcuts",
		Path: "/ext/shortcuts.ts",
		Shortcuts: map[extension.KeyID]extension.ExtensionShortcut{
			"enter":        {Handler: handler, ExtensionPath: "/ext/shortcuts.ts"},
			"ctrl+shift+x": {Handler: handler, ExtensionPath: "/ext/shortcuts.ts"},
		},
	}}, mode.opts.AgentDir)

	if got, want := mode.extensionShortcutKeys(), []string{"ctrl+shift+x"}; !slices.Equal(got, want) {
		t.Fatalf("extensionShortcutKeys() = %v, want %v: enter is tui.input.submit's reserved key", got, want)
	}
	found := false
	for _, d := range mode.newRunner.ShortcutDiagnostics() {
		found = found || d.Message == "Extension shortcut 'enter' from /ext/shortcuts.ts conflicts with built-in shortcut. Skipping."
	}
	if !found {
		t.Fatalf("diagnostics = %#v, want the reserved-key conflict warning for 'enter'", mode.newRunner.ShortcutDiagnostics())
	}
}

// Pi: interactive-mode.ts:2242 setupExtensionShortcuts binds only the shortcuts getShortcuts(getEffectiveConfig()) keeps, so
// Enter (tui.input.submit) never reaches an extension handler while ctrl+shift+x does.
func TestExtensionShortcutListenerSkipsReservedTUIKeybindings(t *testing.T) {
	mode, _ := newCustomEditorDispatchMode(t)
	mode.opts.AgentDir = t.TempDir()
	fired := make(chan string, 2)
	shortcut := func(key string) extension.ExtensionShortcut {
		return extension.ExtensionShortcut{Handler: func(context.Context) error { fired <- key; return nil }, ExtensionPath: "/ext/shortcuts.ts"}
	}
	mode.newRunner = inproc.NewRunner([]extension.Extension{{
		Name:      "shortcuts",
		Path:      "/ext/shortcuts.ts",
		Shortcuts: map[extension.KeyID]extension.ExtensionShortcut{"enter": shortcut("enter"), "ctrl+shift+x": shortcut("ctrl+shift+x")},
	}}, mode.opts.AgentDir)

	mode.setupExtensionShortcutListener(t.Context())
	if mode.extensionShortcutListener == nil {
		t.Fatal("extensionShortcutListener = nil, want ctrl+shift+x bound")
	}
	if mode.extensionShortcutListener("\r") {
		t.Fatal("Enter was consumed by an extension shortcut; tui.input.submit reserves it")
	}
	if !mode.extensionShortcutListener("\x1b[120;6u") {
		t.Fatal("ctrl+shift+x was not consumed by its extension shortcut")
	}
	if got := <-fired; got != "ctrl+shift+x" {
		t.Fatalf("fired %q, want ctrl+shift+x", got)
	}
}
