package tui

import (
	"fmt"
	"strings"
)

// hostUndoKey is the host platform's first default undo key: ctrl+- except on
// native Windows (ctrl+z) and WSL (alt+z), as upstream coding-agent
// KEYBINDINGS["tui.editor.undo"].
func hostUndoKey() string {
	return TUIKeybindingDefinitionsFor(HostKeybindingPlatform())[KBEditorUndo].DefaultKeys[0]
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
