package tui

import "testing"

// Ports the mode-dependent and raw-BS matchesKey cases from
// packages/tui/test/keys.test.ts "Legacy key matching".
func TestMatchesKeyID_ModeAwareLegacyInput(t *testing.T) {
	cases := []struct {
		name        string
		kittyActive bool
		data        string
		keyID       string
		want        bool
	}{
		{"legacy LF is enter", false, "\n", "enter", true},
		{"legacy LF is not shift enter", false, "\n", "shift+enter", false},
		{"legacy ESC CR is alt enter", false, "\x1b\r", "alt+enter", true},
		{"legacy ESC CR is not shift enter", false, "\x1b\r", "shift+enter", false},
		{"Kitty ESC CR is shift enter", true, "\x1b\r", "shift+enter", true},
		{"Kitty ESC CR is not alt enter", true, "\x1b\r", "alt+enter", false},
		{"legacy ctrl space", false, "\x00", "ctrl+space", true},
		{"Kitty rejects legacy ctrl space", true, "\x00", "ctrl+space", false},
		{"legacy alt space", false, "\x1b ", "alt+space", true},
		{"Kitty rejects legacy alt space", true, "\x1b ", "alt+space", false},
		{"legacy alt backspace", false, "\x1b\x08", "alt+backspace", true},
		{"Kitty keeps unambiguous alt backspace", true, "\x1b\x08", "alt+backspace", true},
		{"legacy ctrl alt c", false, "\x1b\x03", "ctrl+alt+c", true},
		{"Kitty rejects legacy ctrl alt c", true, "\x1b\x03", "ctrl+alt+c", false},
		{"legacy uppercase B is alt left", false, "\x1bB", "alt+left", true},
		{"Kitty rejects uppercase B mapping", true, "\x1bB", "alt+left", false},
		{"Kitty keeps lowercase b alt left", true, "\x1bb", "alt+left", true},
		{"legacy alt letter", false, "\x1ba", "alt+a", true},
		{"Kitty rejects legacy alt letter", true, "\x1ba", "alt+a", false},
		{"legacy alt digit", false, "\x1b1", "alt+1", true},
		{"Kitty rejects legacy alt digit", true, "\x1b1", "alt+1", false},
		{"legacy alt symbol", false, "\x1b,", "alt+,", true},
		{"Kitty rejects legacy alt symbol", true, "\x1b,", "alt+,", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			SetKittyProtocolActive(tc.kittyActive)
			t.Cleanup(func() { SetKittyProtocolActive(false) })
			if got := MatchesKeyID(tc.data, tc.keyID); got != tc.want {
				t.Fatalf("MatchesKeyID(%q, %q) with Kitty=%v = %v, want %v", tc.data, tc.keyID, tc.kittyActive, got, tc.want)
			}
		})
	}
}

// Pi treats raw LF as the custom Shift+Enter mapping while Kitty mode is
// active. It must not also trigger the plain Enter binding.
func TestMatchesKeyID_KittyRawLFIsNotPlainEnter(t *testing.T) {
	SetKittyProtocolActive(true)
	t.Cleanup(func() { SetKittyProtocolActive(false) })
	if MatchesKeyID("\n", "enter") {
		t.Fatal("raw LF matched plain Enter while Kitty mode was active")
	}
	if !MatchesKeyID("\n", "shift+enter") {
		t.Fatal("raw LF did not match Shift+Enter while Kitty mode was active")
	}
	if !MatchesKeyID("\n", "ctrl+j") {
		t.Fatal("raw LF did not retain its Ctrl+J identity")
	}
}

func TestMatchesKeyID_RawBackspaceWindowsTerminalHeuristic(t *testing.T) {
	SetKittyProtocolActive(false)
	t.Cleanup(func() { SetKittyProtocolActive(false) })
	t.Setenv("WT_SESSION", "")
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_CLIENT", "")
	t.Setenv("SSH_TTY", "")
	assertRawBackspaceMatches(t, true, false)

	t.Setenv("WT_SESSION", "test-session")
	assertRawBackspaceMatches(t, false, true)

	t.Setenv("SSH_CONNECTION", "1 2 3 4")
	assertRawBackspaceMatches(t, true, false)
}

func TestMatchesKeyID_LegacyClearAndFunctionSequences(t *testing.T) {
	cases := []struct {
		data  string
		keyID string
	}{
		{"\x1b[E", "clear"},
		{"\x1bOE", "clear"},
		{"\x1b[e", "shift+clear"},
		{"\x1bOe", "ctrl+clear"},
		{"\x1b[[A", "f1"},
		{"\x1b[[B", "f2"},
		{"\x1b[[C", "f3"},
		{"\x1b[[D", "f4"},
		{"\x1b[[E", "f5"},
	}
	for _, tc := range cases {
		if !MatchesKeyID(tc.data, tc.keyID) {
			t.Errorf("MatchesKeyID(%q, %q) = false, want true", tc.data, tc.keyID)
		}
	}
	if MatchesKeyID("\x0c", "clear") {
		t.Error("ctrl+l byte matched clear")
	}
}

func assertRawBackspaceMatches(t *testing.T, wantPlain, wantCtrl bool) {
	t.Helper()
	if got := MatchesKeyID("\x08", "backspace"); got != wantPlain {
		t.Errorf("raw BS plain match = %v, want %v", got, wantPlain)
	}
	if got := MatchesKeyID("\x08", "ctrl+backspace"); got != wantCtrl {
		t.Errorf("raw BS ctrl match = %v, want %v", got, wantCtrl)
	}
	if !MatchesKeyID("\x08", "ctrl+h") {
		t.Error("raw BS must continue to match ctrl+h")
	}
	if !MatchesKeyID("\x7f", "backspace") || MatchesKeyID("\x7f", "ctrl+backspace") {
		t.Error("DEL must match only plain backspace")
	}
}
