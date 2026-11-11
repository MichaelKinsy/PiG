package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

type titleCapture struct{ strings.Builder }

// Pi's ctx.ui.setTitle is terminal.setTitle, which replaces control characters in the title (terminal.ts:600-603): a BEL or ESC in an
// extension's title must not end the OSC sequence early and send the rest to the terminal as escapes.
func TestExtensionSetTitleReplacesControlCharacters(t *testing.T) {
	var out titleCapture
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: "/work", AppVersion: "9.9.9"})
	m.tuiInst = tui.NewTuiMainScreen(tui.NewProcessTerminalWithOutput(nil, nil, &out), nil, "")
	(&ExtUIContext{m: m}).SetTitle("a\x1b[2Jb\x07c")
	got := out.String()
	if !strings.HasPrefix(got, "\x1b]0;") || !strings.HasSuffix(got, "\x07") {
		t.Fatalf("title write = %q, want an OSC 0 sequence", got)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(got, "\x1b]0;"), "\x07")
	if strings.ContainsAny(body, "\x1b\x07") {
		t.Fatalf("title body = %q, want no ESC or BEL inside the sequence", body)
	}
}
