package tui

import "testing"

// Ports packages/tui/test/keys.test.ts, describe("matchesKey") >
// describe("Kitty protocol with alternate keys (non-Latin layouts)"): every
// matchesKey assertion, same inputs and expectations. Kitty flag 4 reports
// CSI codepoint:shifted:base ; modifier:event u, where base is the key's
// position on a standard PC-101 layout.
func TestMatchesKey_KittyAlternateKeys(t *testing.T) {
	type check struct {
		data  string
		keyID string
		want  bool
	}
	tests := []struct {
		name   string
		checks []check
	}{
		{"should match Ctrl+c when pressing Ctrl+С (Cyrillic) with base layout key", []check{
			{"\x1b[1089::99;5u", "ctrl+c", true},
		}},
		{"should match Ctrl+d when pressing Ctrl+В (Cyrillic) with base layout key", []check{
			{"\x1b[1074::100;5u", "ctrl+d", true},
		}},
		{"should match Ctrl+z when pressing Ctrl+Я (Cyrillic) with base layout key", []check{
			{"\x1b[1103::122;5u", "ctrl+z", true},
		}},
		{"should match Ctrl+Shift+p with base layout key", []check{
			{"\x1b[1079::112;6u", "ctrl+shift+p", true},
		}},
		{"should still match direct codepoint when no base layout key", []check{
			{"\x1b[99;5u", "ctrl+c", true},
		}},
		{"should match super-modified Kitty bindings, including combined modifiers", []check{
			{"\x1b[107;9u", "super+k", true},
			{"\x1b[13;9u", "super+enter", true},
			{"\x1b[107;13u", "ctrl+super+k", true}, // Key.ctrlSuper("k")
			{"\x1b[107;13u", "ctrl+super+k", true},
			{"\x1b[107;14u", "ctrl+shift+super+k", true},
			{"\x1b[107;13u", "super+k", false},
		}},
		{"should match digit bindings via Kitty CSI-u", []check{
			{"\x1b[49u", "1", true},
			{"\x1b[49;5u", "ctrl+1", true},
			{"\x1b[49;5u", "ctrl+2", false},
		}},
		{"should normalize Kitty keypad functional keys to logical digits, symbols, and navigation", []check{
			{"\x1b[57400u", "1", true},
			{"\x1b[57410u", "/", true},
			{"\x1b[57417u", "left", true},
			{"\x1b[57426u", "delete", true},
		}},
		{"should handle shifted key in format", []check{
			{"\x1b[99:67:99;2u", "shift+c", true},
		}},
		{"should handle event type in format", []check{
			{"\x1b[1089::99;5:3u", "ctrl+c", true},
		}},
		{"should handle full format with shifted key, base key, and event type", []check{
			{"\x1b[1089:1057:99;6:2u", "ctrl+shift+c", true},
		}},
		{"should prefer codepoint for Latin letters even when base layout differs", []check{
			{"\x1b[107::118;5u", "ctrl+k", true},
			{"\x1b[107::118;5u", "ctrl+v", false},
		}},
		{"should prefer codepoint for symbol keys even when base layout differs", []check{
			{"\x1b[47::91;5u", "ctrl+/", true},
			{"\x1b[47::91;5u", "ctrl+[", false},
		}},
		{"should not match wrong key even with base layout", []check{
			{"\x1b[1089::99;5u", "ctrl+d", false},
		}},
		{"should not match wrong modifiers even with base layout", []check{
			{"\x1b[1089::99;5u", "ctrl+shift+c", false},
		}},
		// From describe("parseKey") > "should parse shifted uppercase CSI-u
		// letters as shift+letter" (the matchesKey half).
		{"should parse shifted uppercase CSI-u letters as shift+letter", []check{
			{"\x1b[69;2u", "shift+e", true},
		}},
	}
	SetKittyProtocolActive(true)
	t.Cleanup(func() { SetKittyProtocolActive(false) })
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, c := range tt.checks {
				if got := MatchesKeyID(c.data, c.keyID); got != c.want {
					t.Errorf("MatchesKeyID(%q, %q) = %v, want %v", c.data, c.keyID, got, c.want)
				}
			}
		})
	}
}

// The TUI keybinding manager resolves editor and selector actions through
// matchesKeyID, so a Cyrillic layout reaches them via the base-layout key.
func TestTUIKeybindingsManager_MatchesBaseLayoutKey(t *testing.T) {
	kb := NewTUIKeybindingsManager(nil)
	// Ctrl+А (Cyrillic а = 1072) sits on the Latin a key: tui.editor.cursorLineStart.
	if !kb.Matches("\x1b[1072::97;5u", KBEditorCursorLineStart) {
		t.Fatalf("Ctrl+А (base a) did not match %s", KBEditorCursorLineStart)
	}
	// Dvorak Ctrl+E reports base-layout d; the codepoint e stays authoritative.
	if kb.Matches("\x1b[101::100;5u", KBEditorDeleteCharForward) {
		t.Fatalf("Dvorak Ctrl+E (base d) matched %s", KBEditorDeleteCharForward)
	}
}

// keys.ts matchesKey("enter") also accepts CODEPOINTS.kpEnter (57414), the
// Kitty keypad Enter, for every modifier combination.
func TestMatchesKey_KittyKeypadEnter(t *testing.T) {
	cases := []struct {
		data  string
		keyID string
		want  bool
	}{
		{"\x1b[57414u", "enter", true},
		{"\x1b[57414;2u", "shift+enter", true},
		{"\x1b[57414;3u", "alt+enter", true},
		{"\x1b[57414;5u", "ctrl+enter", true},
		{"\x1b[57414;5u", "enter", false},
		{"\x1b[57414u", "tab", false},
	}
	for _, c := range cases {
		if got := MatchesKeyID(c.data, c.keyID); got != c.want {
			t.Errorf("MatchesKeyID(%q, %q) = %v, want %v", c.data, c.keyID, got, c.want)
		}
	}
}

// Ports every parseKey assertion in keys.test.ts's Kitty alternate-key groups.
func TestParseKey_KittyAlternateKeys(t *testing.T) {
	SetKittyProtocolActive(true)
	t.Cleanup(func() { SetKittyProtocolActive(false) })
	cases := []struct {
		data string
		want string
		ok   bool
	}{
		{"\x1b[107;9u", "super+k", true},
		{"\x1b[13;9u", "super+enter", true},
		{"\x1b[107;13u", "ctrl+super+k", true},
		{"\x1b[107;14u", "shift+ctrl+super+k", true},
		{"\x1b[49u", "1", true},
		{"\x1b[49;5u", "ctrl+1", true},
		{"\x1b[57399u", "0", true},
		{"\x1b[57409u", ".", true},
		{"\x1b[57413u", "+", true},
		{"\x1b[57416u", ",", true},
		{"\x1b[57417u", "left", true},
		{"\x1b[57418u", "right", true},
		{"\x1b[57419u", "up", true},
		{"\x1b[57420u", "down", true},
		{"\x1b[57421u", "pageUp", true},
		{"\x1b[57422u", "pageDown", true},
		{"\x1b[57423u", "home", true},
		{"\x1b[57424u", "end", true},
		{"\x1b[57425u", "insert", true},
		{"\x1b[57426u", "delete", true},
		{"\x1b[1089::99;5u", "ctrl+c", true},
		{"\x1b[107::118;5u", "ctrl+k", true},
		{"\x1b[47::91;5u", "ctrl+/", true},
		{"\x1b[99;5u", "ctrl+c", true},
		{"\x1b[69;2u", "shift+e", true},
		{"\x1b[99;17u", "", false},
	}
	for _, tc := range cases {
		got, ok := ParseKey(tc.data)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseKey(%q) = (%q, %v), want (%q, %v)", tc.data, got, ok, tc.want, tc.ok)
		}
	}
}
