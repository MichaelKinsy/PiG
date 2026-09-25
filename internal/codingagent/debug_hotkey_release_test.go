package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// TestDebugHotkeyFiresOncePerPress pins the count of handler runs for one
// keypress, not whether a release is recognizable.
//
// The hotkey is matched with tui.MatchesKeyID, which normalises encodings and
// answers true for the Kitty release of the chord as well as the press, and
// terminal-input listeners see raw input. Registering raw therefore ran the
// debug handler twice per press: the log was written twice and two
// confirmations landed in the chat. Upstream has the same defect
// (tui.ts:850 matches before its only filter at :887); pig diverges here as
// D55.
func TestDebugHotkeyFiresOncePerPress(t *testing.T) {
	// ctrl+shift+d press and its Kitty release (event type 3).
	const (
		press   = "\x1b[100;6u"
		release = "\x1b[100;6:3u"
	)

	// Guard the premise: if MatchesKeyID ever stops matching the release, the
	// filter is no longer what keeps this single-fire and the test would pass
	// for the wrong reason.
	if !tui.MatchesKeyID(release, "ctrl+shift+d") {
		t.Skip("MatchesKeyID no longer matches the release; D55 may be removable")
	}

	m := &InteractiveMode{}
	fired := 0
	m.addKeyPressListener(func(data string) bool {
		if !tui.MatchesKeyID(data, "ctrl+shift+d") {
			return false
		}
		fired++
		return true
	})

	m.notifyTerminalInput(press)
	m.notifyTerminalInput(release)

	if fired != 1 {
		t.Errorf("one keypress ran the debug handler %d times, want 1: the release fired it again", fired)
	}
}
