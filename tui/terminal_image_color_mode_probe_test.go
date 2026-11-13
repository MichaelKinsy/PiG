package tui

import "testing"

// RT-F6: upstream getTerminalColorMode(capabilities = getCapabilities()) evaluates its default only when the argument is
// absent, so a caller that passes its own capabilities neither runs the tmux probe nor fills the capability cache.
func TestGetTerminalColorModeWithCapabilitiesSkipsDetection(t *testing.T) {
	isolateCapabilityEnv(t)
	setenvs(t, "TMUX", "1")
	restore := tmuxHyperlinkProbe
	probes := 0
	tmuxHyperlinkProbe = func() bool { probes++; return true }
	t.Cleanup(func() { tmuxHyperlinkProbe = restore })
	if got := GetTerminalColorMode(TerminalCapabilities{TrueColor: true}); got != TerminalColorModeTrueColor {
		t.Fatalf("GetTerminalColorMode(truecolor) = %v", got)
	}
	if got := GetTerminalColorMode(TerminalCapabilities{}); got != TerminalColorMode256 {
		t.Fatalf("GetTerminalColorMode(256) = %v", got)
	}
	if probes != 0 {
		t.Fatalf("GetTerminalColorMode(caps) ran the tmux probe %d times, want 0", probes)
	}
	if cached := cachedCapabilities.Load(); cached != nil {
		t.Fatalf("GetTerminalColorMode(caps) cached detected capabilities %+v", *cached)
	}
}

// Inside tmux the default color mode reads TrueColor alone, which the probe does not decide.
func TestDefaultTerminalColorModeSkipsTmuxProbe(t *testing.T) {
	cases := []struct {
		name      string
		env       []string
		overrides CapabilityOverrides
		want      TerminalColorMode
	}{
		{"truecolor hint", []string{"COLORTERM", "truecolor"}, CapabilityOverrides{}, TerminalColorModeTrueColor},
		{"no hint", nil, CapabilityOverrides{}, TerminalColorMode256},
		{"PI_TRUE_COLOR", []string{"PI_TRUE_COLOR", "1"}, CapabilityOverrides{}, TerminalColorModeTrueColor},
		{"settings override", []string{"COLORTERM", "truecolor"}, CapabilityOverrides{TrueColor: new(false)}, TerminalColorMode256},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateCapabilityEnv(t)
			setenvs(t, append([]string{"TMUX", "1", "TERM", "tmux-256color"}, tc.env...)...)
			SetCapabilityOverrides(tc.overrides)
			restore := tmuxHyperlinkProbe
			probes := 0
			tmuxHyperlinkProbe = func() bool { probes++; return true }
			t.Cleanup(func() { tmuxHyperlinkProbe = restore })
			if got := GetTerminalColorMode(); got != tc.want {
				t.Fatalf("GetTerminalColorMode() = %v, want %v", got, tc.want)
			}
			if got := currentColorMode(); got != tc.want {
				t.Fatalf("currentColorMode() = %v, want %v", got, tc.want)
			}
			if probes != 0 {
				t.Fatalf("the color mode ran the tmux probe %d times, want 0", probes)
			}
			if cached := cachedCapabilities.Load(); cached != nil {
				t.Fatalf("the color mode cached capabilities %+v", *cached)
			}
			if got := GetCapabilities().TrueColor; got != (tc.want == TerminalColorModeTrueColor) {
				t.Fatalf("GetCapabilities().TrueColor = %v disagrees with the color mode %v", got, tc.want)
			}
			if probes != 1 {
				t.Fatalf("GetCapabilities() ran the tmux probe %d times, want 1", probes)
			}
		})
	}
}

func TestDefaultTerminalColorModeReadsTheCache(t *testing.T) {
	isolateCapabilityEnv(t)
	setenvs(t, "TMUX", "1", "COLORTERM", "truecolor")
	restore := tmuxHyperlinkProbe
	tmuxHyperlinkProbe = failProbe(t)
	t.Cleanup(func() { tmuxHyperlinkProbe = restore })
	SetCapabilities(TerminalCapabilities{TrueColor: false})
	if got := GetTerminalColorMode(); got != TerminalColorMode256 {
		t.Fatalf("GetTerminalColorMode() = %v, want the cached 256 colors", got)
	}
}
