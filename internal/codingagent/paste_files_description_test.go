package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pasting copied files became part of the clipboard paste action in 0.99.1:
//   - keybindings.ts:142-145 describes app.clipboard.pasteImage as "Paste files on macOS, images, or text from clipboard";
//   - interactive-mode.ts (handleHotkeysCommand) lists the same words in the /hotkeys table row.
//
// (.upstream/v0.99.1/packages/coding-agent/src/core/keybindings.ts, .../modes/interactive/interactive-mode.ts)
const pasteActionDescription = "Paste files on macOS, images, or text from clipboard"

func TestPasteActionDescriptionNamesFilesOnMacOS(t *testing.T) {
	for _, platform := range []tui.KeybindingPlatform{tui.KeybindingPlatformDarwin, tui.KeybindingPlatformLinux, tui.KeybindingPlatformWin32} {
		if got := appKeybindingDefinitionsFor(platform)["app.clipboard.pasteImage"].Description; got != pasteActionDescription {
			t.Errorf("%v: description = %q, want %q", platform, got, pasteActionDescription)
		}
	}
}

func TestHotkeysTableNamesFilesOnMacOS(t *testing.T) {
	if got := hotkeysMarkdown(); !strings.Contains(got, "| "+pasteActionDescription+" |") {
		t.Fatalf("the /hotkeys table has no %q row:\n%s", pasteActionDescription, got)
	}
}
