package tui

import "testing"

// Ports the non-alternate parseKey assertions from packages/tui/test/keys.test.ts.
func TestParseKey_UpstreamCases(t *testing.T) {
	SetKittyProtocolActive(false)
	t.Cleanup(func() { SetKittyProtocolActive(false) })
	// Raw BS is ctrl+backspace only inside a Windows Terminal session
	// (upstream isWindowsTerminalSession); these cases are outside one.
	t.Setenv("WT_SESSION", "")
	cases := []struct {
		data string
		want string
	}{
		{"\x1b[27;5;99~", "ctrl+c"},
		{"\x1b[27;5;100~", "ctrl+d"},
		{"\x1b[27;5;122~", "ctrl+z"},
		{"\x1b[27;5;13~", "ctrl+enter"},
		{"\x1b[27;2;13~", "shift+enter"},
		{"\x1b[27;3;13~", "alt+enter"},
		{"\x1b[27;2;9~", "shift+tab"},
		{"\x1b[27;5;9~", "ctrl+tab"},
		{"\x1b[27;3;9~", "alt+tab"},
		{"\x1b[27;1;127~", "backspace"},
		{"\x1b[27;5;127~", "ctrl+backspace"},
		{"\x1b[27;3;127~", "alt+backspace"},
		{"\x1b[27;1;27~", "escape"},
		{"\x1b[27;1;32~", "space"},
		{"\x1b[27;5;32~", "ctrl+space"},
		{"\x1b[27;5;47~", "ctrl+/"},
		{"\x1b[27;5;49~", "ctrl+1"},
		{"\x1b[27;2;49~", "shift+1"},
		{"\x1b[27;2;69~", "shift+e"},
		{"\x1b[27;6;69~", "shift+ctrl+e"},
		{"\x1b[104;7u", "ctrl+alt+h"},
		{"\x1b[27;7;104~", "ctrl+alt+h"},
		{"\x03", "ctrl+c"},
		{"\x04", "ctrl+d"},
		{"\x1b", "escape"},
		{"\t", "tab"},
		{"\r", "enter"},
		{"\n", "enter"},
		{"\x00", "ctrl+space"},
		{" ", "space"},
		{"1", "1"},
		{"\x1c", "ctrl+\\"},
		{"\x1d", "ctrl+]"},
		{"\x1f", "ctrl+-"},
		{"\x1b\x1b", "ctrl+alt+["},
		{"\x1b\x1c", "ctrl+alt+\\"},
		{"\x1b\x1d", "ctrl+alt+]"},
		{"\x1b\x1f", "ctrl+alt+-"},
		{"\x7f", "backspace"},
		{"\x08", "backspace"},
		{"\x1b ", "alt+space"},
		{"\x1b\x08", "alt+backspace"},
		{"\x1b\x03", "ctrl+alt+c"},
		{"\x1bB", "alt+left"},
		{"\x1bF", "alt+right"},
		{"\x1ba", "alt+a"},
		{"\x1b1", "alt+1"},
		{"\x1b,", "alt+,"},
		{"\x1b.", "alt+."},
		{"\x1by", "alt+y"},
		{"\x1bz", "alt+z"},
		{"\x1b[A", "up"},
		{"\x1b[B", "down"},
		{"\x1b[C", "right"},
		{"\x1b[D", "left"},
		{"\x1bOA", "up"},
		{"\x1bOB", "down"},
		{"\x1bOC", "right"},
		{"\x1bOD", "left"},
		{"\x1bOH", "home"},
		{"\x1bOF", "end"},
		{"\x1b[7~", "home"},
		{"\x1b[8~", "end"},
		{"\x1b[7:3~", "home"},
		{"\x1b[8;1:2~", "end"},
		{"\x1b[1;5H", "ctrl+home"},
		{"\x1b[1;5F", "ctrl+end"},
		{"\x1b[5;5~", "ctrl+pageUp"},
		{"\x1b[6;5~", "ctrl+pageDown"},
		{"\x1bOP", "f1"},
		{"\x1b[24~", "f12"},
		{"\x1b[E", "clear"},
		{"\x1b[2^", "ctrl+insert"},
		{"\x1bp", "alt+up"},
		{"\x1b[[5~", "pageUp"},
	}
	for _, tc := range cases {
		got, ok := ParseKey(tc.data)
		if !ok || got != tc.want {
			t.Errorf("ParseKey(%q) = (%q, %v), want (%q, true)", tc.data, got, ok, tc.want)
		}
	}
}

func TestParseKey_KittyModeAwareLegacyCases(t *testing.T) {
	SetKittyProtocolActive(true)
	t.Cleanup(func() { SetKittyProtocolActive(false) })
	cases := []struct {
		data string
		want string
		ok   bool
	}{
		{"\n", "shift+enter", true},
		{"\x1b\r", "shift+enter", true},
		{"\x1b ", "", false},
		{"\x1b\x08", "alt+backspace", true},
		{"\x1b\x03", "", false},
		{"\x1bB", "", false},
		{"\x1bF", "", false},
		{"\x1ba", "", false},
		{"\x1b1", "", false},
		{"\x1b,", "", false},
		{"\x1b.", "", false},
		{"\x1by", "", false},
	}
	for _, tc := range cases {
		got, ok := ParseKey(tc.data)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseKey(%q) = (%q, %v), want (%q, %v)", tc.data, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParseKey_RawBackspaceWindowsTerminalHeuristic(t *testing.T) {
	t.Setenv("WT_SESSION", "test-session")
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_CLIENT", "")
	t.Setenv("SSH_TTY", "")
	if got, ok := ParseKey("\x08"); !ok || got != "ctrl+backspace" {
		t.Fatalf("ParseKey(raw BS) = (%q, %v), want (ctrl+backspace, true)", got, ok)
	}
	t.Setenv("SSH_CONNECTION", "1 2 3 4")
	if got, ok := ParseKey("\x08"); !ok || got != "backspace" {
		t.Fatalf("ParseKey(raw BS over SSH) = (%q, %v), want (backspace, true)", got, ok)
	}
}
