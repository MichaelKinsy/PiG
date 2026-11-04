package tui

import "testing"

// Ports packages/tui/test/keys.test.ts (deb12d78): "should match Kitty function keys F1-F12 with and without modifiers".
func TestMatchesKittyFunctionKeysF1ToF12WithAndWithoutModifiers(t *testing.T) {
	for _, tc := range []struct {
		data, keyID string
		want        bool
	}{
		{"\x1b[P", "f1", true},
		{"\x1b[Q", "f2", true},
		{"\x1b[13~", "f3", true},
		{"\x1b[S", "f4", true},
		{"\x1b[1;5P", "ctrl+f1", true},
		{"\x1b[1;5P", "f1", false},
		{"\x1b[1;2S", "shift+f4", true},
		{"\x1b[15;5~", "ctrl+f5", true},
		{"\x1b[24;3~", "alt+f12", true},
		{"\x1b[15;1:2~", "f5", true},
	} {
		if got := MatchesKeyID(tc.data, tc.keyID); got != tc.want {
			t.Errorf("MatchesKeyID(%q, %q) = %v, want %v", tc.data, tc.keyID, got, tc.want)
		}
	}
	for _, tc := range []struct {
		data string
		want string
		ok   bool
	}{
		{"\x1b[P", "f1", true},
		{"\x1b[1;5P", "ctrl+f1", true},
		{"\x1b[13~", "f3", true},
		{"\x1b[21;2~", "shift+f10", true},
		{"\x1b[1;5R", "", false},
	} {
		if got, ok := ParseKey(tc.data); got != tc.want || ok != tc.ok {
			t.Errorf("ParseKey(%q) = (%q, %v), want (%q, %v)", tc.data, got, ok, tc.want, tc.ok)
		}
	}
	if !IsKeyRelease("\x1b[1;1:3P") {
		t.Error(`IsKeyRelease("\x1b[1;1:3P") = false, want true`)
	}
	if !IsKeyRepeat("\x1b[1;1:2S") {
		t.Error(`IsKeyRepeat("\x1b[1;1:2S") = false, want true`)
	}
}

// Ports packages/tui/test/keys.test.ts (deb12d78): "ignores unmapped Kitty functional keys such as F13-F35".
func TestDecodeKittyPrintableIgnoresUnmappedKittyFunctionalKeys(t *testing.T) {
	for _, data := range []string{"\x1b[57376u", "\x1b[57398;2u"} {
		if got, ok := DecodeKittyPrintable(data); ok {
			t.Errorf("DecodeKittyPrintable(%q) = (%q, true), want undefined", data, got)
		}
	}
	if got, ok := DecodePrintableKey("\x1b[57382u"); ok {
		t.Errorf("DecodePrintableKey(%q) = (%q, true), want undefined", "\x1b[57382u", got)
	}
}
