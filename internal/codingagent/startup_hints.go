package codingagent

import (
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

// StartupKeybindHints renders the compact keybinding hint line shown under the
// startup banner, mirroring upstream interactive-mode's compact startup
// instructions (interrupt · clear/exit · / commands · ! bash · <expand> more).
// Keys resolve through the supplied manager so user remaps are reflected; a nil
// manager falls back to the built-in defaults.
func StartupKeybindHints(km *KeybindingsManager) string {
	if km == nil {
		km = DefaultKeybindingsManager()
	}
	parts := []string{
		tui.KeyHint(km.KeyText("app.interrupt"), "interrupt"),
		tui.RawKeyHint(km.KeyText("app.clear")+"/"+km.KeyText("app.exit"), "clear/exit"),
		tui.RawKeyHint("/", "commands"),
		tui.RawKeyHint("!", "bash"),
		tui.KeyHint(km.KeyText("app.tools.expand"), "more"),
	}
	t := tui.ActiveTheme()
	sep := t.Muted + " · " + "\x1b[0m"
	return strings.Join(parts, sep)
}
