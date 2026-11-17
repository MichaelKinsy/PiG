package tui

import (
	"bytes"
	"testing"
)

// Pi: packages/tui/src/tui.ts:1012 stop(): beforeTerminalStop, terminal.showCursor() (CSI ?25h), terminal.stop() (CSI ?2004l among its writes), then
// afterTerminalStop (the alternate screen is left). A process exit's stop therefore writes `?25h ?2004l` BEFORE `?1049l`.
func TestAltScreenExitStopWritesCursorAndPasteResetBeforeLeavingTheScreen(t *testing.T) {
	resetKeyboardProtocolState(false, false, false)
	for _, tc := range []struct {
		name     string
		preserve bool
	}{{"preserve-screen", true}, {"transcript", false}} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			mouse := false
			alt := NewTuiAltScreenWithOutput(&out, 20, 5, TuiAltScreenOptions{Mouse: &mouse})
			alt.Start()
			out.Reset()
			alt.StopWithOptions(StopOptions{PreserveScreen: tc.preserve, ExitTerminal: true})
			want := altBeginSynchronizedOutput + altEnableAutowrap + altEndSynchronizedOutput +
				"\x1b[?25h\x1b[?2004l" +
				altBeginSynchronizedOutput + altExitAltScreen
			if got := out.String(); len(got) < len(want) || got[:len(want)] != want {
				t.Fatalf("exit stop head = %q, want Pi's %q", got, want)
			}
		})
	}
}

func TestMainScreenExitStopWritesCursorThenPasteReset(t *testing.T) {
	var out bytes.Buffer
	ui := NewWithOutput(&out, 80, 24)
	ui.RestoreRenderState(TuiMainScreenRenderState{PreviousLines: make([]string, 1)})
	ui.StopWithOptions(StopOptions{ExitTerminal: true})
	if got, want := out.String(), " \x1b[1B\r\n\x1b[?25h\x1b[?2004l"; got != want {
		t.Fatalf("exit stop = %q, want Pi's %q", got, want)
	}
}
