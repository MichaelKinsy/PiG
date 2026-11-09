package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// tuiSetCapsForTest forces the cached terminal capabilities to a known
// images-supported / images-unsupported state until the test ends. Test-only.
func tuiSetCapsForTest(t testing.TB, supportsImages bool) {
	t.Helper()
	restoreStartupTheme(t)
	if supportsImages {
		tui.SetCapabilities(tui.TerminalCapabilities{
			Images:     tui.ImageProtocolITerm2,
			TrueColor:  true,
			Hyperlinks: true,
		})
		return
	}
	tui.SetCapabilities(tui.TerminalCapabilities{Images: ""})
}
