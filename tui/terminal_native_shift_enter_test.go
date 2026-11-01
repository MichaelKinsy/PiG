package tui

// pi: packages/tui/src/native-modifiers.ts

import (
	"runtime"
	"testing"
)

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

// packages/tui/src/native-modifiers.ts:5-13 and native-platform.ts ModifierKey: the four modifier names are the wire values the helper
// understands, and a platform without a native helper (anything but macOS and Windows) reports every modifier as not pressed.
func TestNativeModifierKeysWithoutAHelper(t *testing.T) {
	for key, want := range map[ModifierKey]string{ModifierShift: "shift", ModifierCommand: "command", ModifierControl: "control", ModifierOption: "option"} {
		if string(key) != want {
			t.Errorf("modifier %q, want %q", key, want)
		}
		if runtime.GOOS != "darwin" && runtime.GOOS != "windows" && IsNativeModifierPressed(key) {
			t.Errorf("%s reported pressed without a native helper", key)
		}
	}
}

// Ports "rewrites ESC+CR to CSI-u Shift+Enter when Shift is pressed" (Pi 86dc686c, #11559; packages/tui/test/terminal.test.ts):
// some Apple Terminal setups send ESC+CR for Shift+Enter, which parses as alt+enter and queues a follow-up.
func TestNormalizeNativeShiftEnterInputRewritesEscapeReturn(t *testing.T) {
	for _, tc := range []struct {
		detect, shift bool
		want          string
	}{
		{true, true, "\x1b[13;2u"},
		{true, false, "\x1b\r"},
		{false, true, "\x1b\r"},
	} {
		if got := NormalizeNativeShiftEnterInput("\x1b\r", tc.detect, tc.shift); got != tc.want {
			t.Errorf("NormalizeNativeShiftEnterInput(ESC CR, %v, %v) = %q, want %q", tc.detect, tc.shift, got, tc.want)
		}
		if got := NormalizeAppleTerminalInput("\x1b\r", tc.detect, tc.shift); got != tc.want {
			t.Errorf("NormalizeAppleTerminalInput(ESC CR, %v, %v) = %q, want %q", tc.detect, tc.shift, got, tc.want)
		}
	}
}

// ProcessTerminal.forwardInputSequence (Pi 86dc686c): ESC+CR probes the native Shift state like a lone CR, in Apple Terminal on
// macOS and on Windows only.
func TestNormalizeProcessInputSequenceForEscapeReturn(t *testing.T) {
	for _, tc := range []struct {
		name, goos, termProgram string
		shift                   bool
		want                    string
		wantProbe               bool
	}{
		{"apple terminal shift", "darwin", "Apple_Terminal", true, "\x1b[13;2u", true},
		{"apple terminal no shift stays alt+enter", "darwin", "Apple_Terminal", false, "\x1b\r", true},
		{"windows shift", "windows", "", true, "\x1b[13;2u", true},
		{"iterm2 never probes", "darwin", "iTerm.app", true, "\x1b\r", false},
		{"linux never probes", "linux", "Apple_Terminal", true, "\x1b\r", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probed := false
			got := normalizeProcessInputSequenceFor("\x1b\r", tc.goos, tc.termProgram, func(key ModifierKey) bool {
				probed = true
				return tc.shift
			})
			if got != tc.want || probed != tc.wantProbe {
				t.Fatalf("got %q probed %v, want %q probed %v", got, probed, tc.want, tc.wantProbe)
			}
		})
	}
}
