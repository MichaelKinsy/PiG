package terminals

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/terminals/ghostty"
)

// Ports test/p2/terminal-keys.test.ts: tmux key names as libghostty key presses: the bytes programs receive, in the
// terminal's current modes.

func TestTmuxKeyNamesEncodeAsATerminalSendsThemOtherWordsAreText(t *testing.T) {
	engine, err := ghostty.Load(context.Background(), ghosttyWasm)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close(context.Background())
	vt, err := engine.Terminal(ghostty.Options{Cols: 20, Rows: 4, Scrollback: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer vt.Free()
	encode := func(name string) (string, bool) {
		key, named := TmuxKey(name)
		if !named {
			return "", false
		}
		bytes, ok := vt.EncodeKey(key)
		return string(bytes), ok
	}
	expected := map[string]string{
		"Enter": "\r", "Escape": "\x1b", "C-[": "\x1b", "Tab": "\t", "BTab": "\x1b[Z", "BSpace": "\x7f", "Space": " ",
		"C-c": "\x03", "C-C": "\x03", "^D": "\x04", "C-Space": "\x00", `C-\`: "\x1c", "C-_": "\x1f",
		"M-x": "\x1bx", "M-X": "\x1bX", "Up": "\x1b[A", "S-Up": "\x1b[1;2A", "Home": "\x1b[H", "PPage": "\x1b[5~",
		"DC": "\x1b[3~", "F1": "\x1bOP", "F5": "\x1b[15~",
	}
	for name, want := range expected {
		if got, ok := encode(name); !ok || got != want {
			t.Errorf("%s = %q (%v), want %q", name, got, ok, want)
		}
	}
	// Not key names: tmux types them.
	for _, word := range []string{"a", "npm test", "enter", "C-", "Ctrl-c", "x-y"} {
		if _, named := TmuxKey(word); named {
			t.Errorf("%q names a key", word)
		}
	}
	// Application cursor mode (a full-screen program asked for it) changes the arrows.
	vt.Write([]byte("\x1b[?1h"))
	if got, _ := encode("Up"); got != "\x1bOA" {
		t.Errorf("Up in application cursor mode = %q", got)
	}
}
