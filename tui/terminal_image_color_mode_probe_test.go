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
