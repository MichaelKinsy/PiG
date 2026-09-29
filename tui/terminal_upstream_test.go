package tui

import (
	"bytes"
	"testing"
)

func TestUpstreamTerminalEscapeTimeout(t *testing.T) {
	// Ports .upstream/v0.87.1/packages/tui/test/terminal.test.ts:12-32, preserving every environment row.
	cases := []struct {
		name string
		envs []map[string]string
		want float64
	}{
		{"uses PI_TUI_ESC_TIMEOUT when configured", []map[string]string{{"PI_TUI_ESC_TIMEOUT": "80"}, {"PI_TUI_ESC_TIMEOUT": "80", "SSH_TTY": "/dev/pts/1"}}, 80},
		{"ignores invalid PI_TUI_ESC_TIMEOUT values", []map[string]string{{"PI_TUI_ESC_TIMEOUT": "abc"}, {"PI_TUI_ESC_TIMEOUT": "0"}, {"PI_TUI_ESC_TIMEOUT": "-5"}, {"PI_TUI_ESC_TIMEOUT": ""}}, 10},
		{"defaults to 100ms over SSH", []map[string]string{{"SSH_CONNECTION": "10.0.0.1 22"}, {"SSH_TTY": "/dev/pts/1"}}, 100},
		{"defaults to 10ms otherwise", []map[string]string{{}}, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, env := range tc.envs {
				if got := ResolveEscapeTimeoutMs(func(key string) string { return env[key] }); got != tc.want {
					t.Fatalf("env=%v: got %v, want %v", env, got, tc.want)
				}
			}
		})
	}
}

func TestUpstreamTerminalNativeShiftEnter(t *testing.T) {
	// Ports .upstream/v0.87.1/packages/tui/test/terminal.test.ts:36-74.
	for _, family := range []struct {
		name      string
		normalize func(string, bool, bool) string
		names     []string
	}{
		{"normalizeNativeShiftEnterInput", NormalizeNativeShiftEnterInput, []string{"rewrites Return to CSI-u Shift+Enter when native Shift detection is enabled and Shift is pressed", "leaves Return unchanged when native Shift detection is disabled", "leaves Return unchanged when Shift is not pressed", "leaves non-Return input unchanged"}},
		{"normalizeAppleTerminalInput", NormalizeAppleTerminalInput, []string{"rewrites Apple Terminal Return to CSI-u Shift+Enter when Shift is pressed", "leaves non-Apple Terminal Return unchanged when Shift is pressed", "leaves Apple Terminal Return unchanged when Shift is not pressed", "leaves non-Return input unchanged"}},
	} {
		t.Run(family.name, func(t *testing.T) {
			for i, tc := range []struct {
				inputs        []string
				detect, shift bool
				expected      []string
			}{
				{[]string{"\r"}, true, true, []string{"\x1b[13;2u"}},
				{[]string{"\r"}, false, true, []string{"\r"}},
				{[]string{"\r"}, true, false, []string{"\r"}},
				{[]string{"\x1b[13;2u", "a"}, true, true, []string{"\x1b[13;2u", "a"}},
			} {
				t.Run(family.names[i], func(t *testing.T) {
					for n, input := range tc.inputs {
						if got := family.normalize(input, tc.detect, tc.shift); got != tc.expected[n] {
							t.Fatalf("input=%q detect=%v shift=%v: %q, want %q", input, tc.detect, tc.shift, got, tc.expected[n])
						}
					}
				})
			}
		})
	}
}

func TestUpstreamTerminalProgressClear(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/terminal.test.ts:241: writes a valid OSC 9;4 clear sequence.
	var out bytes.Buffer
	terminal := NewProcessTerminalWithOutput(nil, nil, &out)
	terminal.SetProgress(false)
	if got := out.String(); got != "\x1b]9;4;0\x07" {
		t.Fatalf("writes=%q, want one OSC 9;4 clear sequence without an extra parameter", got)
	}
}

func TestUpstreamTerminalDimensionFallback(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/terminal.test.ts:261: falls back to COLUMNS and LINES before default dimensions.
	t.Setenv("COLUMNS", "123")
	t.Setenv("LINES", "45")
	terminal := NewProcessTerminal(nil, nil)
	if got := terminal.Columns(); got != 123 {
		t.Fatalf("columns=%d, want 123", got)
	}
	if got := terminal.Rows(); got != 45 {
		t.Fatalf("rows=%d, want 45", got)
	}
}
