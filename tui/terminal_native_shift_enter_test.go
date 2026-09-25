package tui

import "testing"

// Mirrors terminal.ts normalizeNativeShiftEnterInput and
// normalizeAppleTerminalInput.
func TestNormalizeNativeShiftEnterInput(t *testing.T) {
	cases := []struct {
		data    string
		detect  bool
		shift   bool
		want    string
		comment string
	}{
		{"\r", true, true, "\x1b[13;2u", "plain Return with Shift held becomes Shift+Enter"},
		{"\r", true, false, "\r", "plain Return without Shift stays Enter"},
		{"\r", false, true, "\r", "detection disabled"},
		{"\n", true, true, "\n", "only CR is rewritten"},
		{"\r\r", true, true, "\r\r", "only a lone CR sequence is rewritten"},
		{"\x1b[13;2u", true, true, "\x1b[13;2u", "already enhanced"},
	}
	for _, tc := range cases {
		if got := NormalizeNativeShiftEnterInput(tc.data, tc.detect, tc.shift); got != tc.want {
			t.Errorf("%s: NormalizeNativeShiftEnterInput(%q, %v, %v) = %q, want %q", tc.comment, tc.data, tc.detect, tc.shift, got, tc.want)
		}
		if got := NormalizeAppleTerminalInput(tc.data, tc.detect, tc.shift); got != tc.want {
			t.Errorf("%s: NormalizeAppleTerminalInput(%q, %v, %v) = %q, want %q", tc.comment, tc.data, tc.detect, tc.shift, got, tc.want)
		}
	}
}

// Mirrors terminal.ts isAppleTerminalSession: darwin plus the exact
// TERM_PROGRAM value.
func TestIsAppleTerminalSessionFor(t *testing.T) {
	cases := []struct {
		goos, termProgram string
		want              bool
	}{
		{"darwin", "Apple_Terminal", true},
		{"darwin", "apple_terminal", false},
		{"darwin", "iTerm.app", false},
		{"linux", "Apple_Terminal", false},
	}
	for _, tc := range cases {
		if got := isAppleTerminalSessionFor(tc.goos, tc.termProgram); got != tc.want {
			t.Errorf("isAppleTerminalSessionFor(%q, %q) = %v, want %v", tc.goos, tc.termProgram, got, tc.want)
		}
	}
}

// Mirrors ProcessTerminal.forwardInputSequence: the native Shift probe runs
// only for a lone CR in Apple Terminal on macOS or on Windows.
func TestNormalizeProcessInputSequenceFor(t *testing.T) {
	cases := []struct {
		name, sequence, goos, termProgram string
		shift                             bool
		want                              string
		wantProbe                         bool
	}{
		{"apple terminal shift return", "\r", "darwin", "Apple_Terminal", true, "\x1b[13;2u", true},
		{"apple terminal return", "\r", "darwin", "Apple_Terminal", false, "\r", true},
		{"apple terminal other key never probes", "a", "darwin", "Apple_Terminal", true, "a", false},
		{"iterm2 on macOS never probes", "\r", "darwin", "iTerm.app", true, "\r", false},
		{"windows shift return", "\r", "windows", "", true, "\x1b[13;2u", true},
		{"linux never probes", "\r", "linux", "Apple_Terminal", true, "\r", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probed := false
			probe := func(key ModifierKey) bool {
				if key != ModifierShift {
					t.Fatalf("probed modifier %q, want shift", key)
				}
				probed = true
				return tc.shift
			}
			if got := normalizeProcessInputSequenceFor(tc.sequence, tc.goos, tc.termProgram, probe); got != tc.want {
				t.Fatalf("normalizeProcessInputSequenceFor() = %q, want %q", got, tc.want)
			}
			if probed != tc.wantProbe {
				t.Fatalf("probe called = %v, want %v", probed, tc.wantProbe)
			}
		})
	}
}

// Mirrors native-modifiers.ts: an unknown modifier name or an unavailable
// helper reports not pressed instead of failing.
func TestIsNativeModifierPressedUnknownKey(t *testing.T) {
	if IsNativeModifierPressed(ModifierKey("hyper")) {
		t.Fatal("unknown modifier reported pressed")
	}
}
