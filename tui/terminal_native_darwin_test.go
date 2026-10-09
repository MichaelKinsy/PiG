//go:build darwin

package tui

import "testing"

// terminal.ts isAppleTerminalSession and the Shift+Enter normalization of ProcessTerminal.forwardInputSequence, with the real
// darwin GOOS and the real modifier bridge instead of the injected goos used by the portable tests.
func TestAppleTerminalSessionOnDarwin(t *testing.T) {
	for program, want := range map[string]bool{"Apple_Terminal": true, "apple_terminal": false, "iTerm.app": false, "": false, "ghostty": false} {
		t.Setenv("TERM_PROGRAM", program)
		if got := IsAppleTerminalSession(); got != want {
			t.Errorf("TERM_PROGRAM=%q: IsAppleTerminalSession() = %v, want %v", program, got, want)
		}
	}
}

func TestNativeShiftEnterNormalizationOnDarwin(t *testing.T) {
	shiftDown := func(ModifierKey) bool { return true }
	shiftUp := func(ModifierKey) bool { return false }
	for _, tc := range []struct {
		program, sequence string
		shift             func(ModifierKey) bool
		want              string
	}{
		{"Apple_Terminal", "\r", shiftDown, "\x1b[13;2u"},
		{"Apple_Terminal", "\r", shiftUp, "\r"},
		{"Apple_Terminal", "a", shiftDown, "a"},
		{"Apple_Terminal", "\x1b[13;2u", shiftDown, "\x1b[13;2u"},
		{"iTerm.app", "\r", shiftDown, "\r"},
		{"", "\r", shiftDown, "\r"},
	} {
		if got := normalizeProcessInputSequenceFor(tc.sequence, "darwin", tc.program, tc.shift); got != tc.want {
			t.Errorf("TERM_PROGRAM=%q %q = %q, want %q", tc.program, tc.sequence, got, tc.want)
		}
	}
	// The production entry point reads the real CoreGraphics key state for every modifier, and leaves other input alone.
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	for _, modifier := range []ModifierKey{ModifierShift, ModifierCommand, ModifierControl, ModifierOption} {
		_ = IsNativeModifierPressed(modifier)
	}
	if got := NormalizeProcessInputSequence("a"); got != "a" {
		t.Errorf("NormalizeProcessInputSequence(a) = %q", got)
	}
}
