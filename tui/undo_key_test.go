package tui

import (
	"fmt"
	"strings"
	"testing"
)

// hostUndoKey is the first undo key of the tui default registry: upstream
// TUI_KEYBINDINGS binds ctrl+- on every platform. The Windows and WSL keys
// belong to the coding agent's table.
func hostUndoKey() string {
	return NewTUIKeybindingsManager(nil).GetKeys(KBEditorUndo)[0]
}

// hostUndoInput is the legacy terminal input for hostUndoKey.
func hostUndoInput() string {
	switch key := hostUndoKey(); key {
	case "alt+z":
		return "\x1bz"
	default:
		return tuiKeyIDInputs[key][0]
	}
}

// hostUndoKittyInput is the Kitty keyboard protocol input for hostUndoKey.
func hostUndoKittyInput() string {
	key := hostUndoKey()
	modifier, char, _ := strings.Cut(key, "+")
	code := map[string]int{"ctrl": 5, "alt": 3}[modifier]
	return fmt.Sprintf("\x1b[%d;%du", char[0], code)
}

// restoreKeybindingsAfterTest returns the global keybinding manager to its current value when the test ends. A test that
// installs a manager built for the host platform (TUIKeybindingDefinitionsFor(HostKeybindingPlatform())) must call it
// first: on Windows and WSL that manager binds undo to ctrl+z or alt+z, and leaving it installed fails every later test
// that sends the default ctrl+- undo key.
func restoreKeybindingsAfterTest(t testing.TB) {
	t.Helper()
	previous := globalTUIKeybindings.Load()
	t.Cleanup(func() { globalTUIKeybindings.Store(previous) })
}
