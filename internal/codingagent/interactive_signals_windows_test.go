//go:build windows

package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Upstream handleCtrlZ reports the win32 case with showStatus, which renders
// a dim status and replaces the previous one when nothing followed it. Pressing
// a bound app.suspend twice therefore leaves one status line, not two.
func TestHandleSuspendShowsTheWindowsStatus(t *testing.T) {
	m := &InteractiveMode{chatContainer: tui.NewContainer()}
	m.handleSuspend()
	m.handleSuspend()

	if got := m.chatContainer.ChildCount(); got != 2 {
		t.Fatalf("chat holds %d children after two suspends, want one spacer and one status", got)
	}
	want := tui.ActiveTheme().FgText("dim", "Suspend to background is not supported on Windows")
	if m.lastStatusText == nil || m.lastStatusText.Content != want {
		t.Fatalf("status = %v, want %q", m.lastStatusText, want)
	}
}
