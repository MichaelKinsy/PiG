package codingagent

import "github.com/MichaelKinsy/PiG/tui"

type savedCapabilities struct{ caps tui.TerminalCapabilities }

func (s savedCapabilities) restore() {
	tui.SetCapabilities(s.caps)
}

// tuiCapabilitiesForTest snapshots current TUI capabilities so a test can
// restore them with .restore() in a defer. Test-only.
func tuiCapabilitiesForTest() savedCapabilities {
	return savedCapabilities{caps: tui.GetCapabilities()}
}

// tuiSetCapsForTest forces the cached terminal capabilities to a known
// images-supported / images-unsupported state. Test-only.
func tuiSetCapsForTest(supportsImages bool) {
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
