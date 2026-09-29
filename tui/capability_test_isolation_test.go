package tui

import (
	"os"
	"testing"
)

func TestDetectCapabilitiesRestoresCachedState(t *testing.T) {
	preserveCapabilityState(t)
	want := TerminalCapabilities{Images: ImageProtocolITerm2, TrueColor: false, Hyperlinks: true}
	SetCapabilities(want)
	t.Run("detection cases", TestDetectCapabilities)
	if got := cachedCapabilities.Load(); got == nil || *got != want {
		t.Fatalf("capability tests changed the surrounding cache: got %+v, want %+v", got, want)
	}
}

func TestTermuxRenderTestRestoresAbsentEnvironment(t *testing.T) {
	unsetEnvForTest(t, "TERMUX_VERSION")
	t.Run("termux render", TestTUIRender_TermuxHeightChangeBypassesFullRedraw)
	if value, exists := os.LookupEnv("TERMUX_VERSION"); exists {
		t.Fatalf("render test left TERMUX_VERSION set to %q; it was absent", value)
	}
}
