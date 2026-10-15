package tui

import "testing"

// Ports packages/tui/test/keys.test.ts (42a3497d): "should match shifted symbols by their produced character", the modifyOtherKeys ctrl++ case, and the unmodified '+' key.
func TestShiftedSymbolsMatchByTheirProducedCharacterUpstream(t *testing.T) {
	parse := func(data string) string {
		key, _ := ParseKey(data)
		return key
	}
	t.Run("kitty", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		for _, tc := range []struct{ data, keyID, parsed string }{
			{"\x1b[61:43;2u", "+", "+"},
			{"\x1b[61:43;2u", "shift+=", ""},
			{"\x1b[61:43;6u", "ctrl++", "ctrl++"},
		} {
			if !MatchesKeyID(tc.data, tc.keyID) {
				t.Errorf("MatchesKeyID(%q, %q) = false", tc.data, tc.keyID)
			}
			if tc.parsed != "" && parse(tc.data) != tc.parsed {
				t.Errorf("ParseKey(%q) = %q, want %q", tc.data, parse(tc.data), tc.parsed)
			}
		}
	})
	t.Run("modifyOtherKeys ctrl+plus", func(t *testing.T) {
		if !MatchesKeyID("\x1b[27;6;43~", "ctrl++") || parse("\x1b[27;6;43~") != "ctrl++" {
			t.Errorf("ctrl++ = %q", parse("\x1b[27;6;43~"))
		}
	})
	t.Run("plain plus", func(t *testing.T) {
		if parse("+") != "+" || !MatchesKeyID("+", "+") {
			t.Errorf("+ = %q", parse("+"))
		}
	})
}
