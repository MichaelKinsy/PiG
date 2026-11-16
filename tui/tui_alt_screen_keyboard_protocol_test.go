package tui

import (
	"bytes"
	"strings"
	"testing"
)

// The Kitty keyboard-protocol stack is per screen. Pi keeps one push on the active screen: tui.ts start() (:956) enters the alternate screen in
// beforeTerminalStart (tui-alt-screen.ts:347) before terminal.start() pushes (terminal.ts:207, queryAndEnableKittyProtocol), and stop() pops in
// terminal.stop() (terminal.ts:481) before afterTerminalStop() leaves it. The renderer therefore writes no pop and no push of its own, whether or
// not a keyboard push is outstanding, at start or at stop.
func TestTuiAltScreenWritesNoKeyboardStackOperations(t *testing.T) {
	const (
		enter = "\x1b[?1049h"
		exit  = "\x1b[?1049l"
		rest  = "\x1b[?7l\x1b[2J\x1b[H\x1b[?25l"
	)
	// StopWithOptions writes a teardown block, whose image-deletion content depends on the detected terminal, then the exit block asserted exactly.
	for _, tc := range []struct {
		name     string
		pushed   bool
		preserve bool
		wantExit string
	}{
		{"pushed preserve", true, true, "\x1b[?2026h" + exit + "\x1b[?25h\x1b[?2026l"},
		{"pushed document", true, false, "\x1b[?2026h" + exit + "\x1b[?7l\x1b[0m\x1b[?7h\r\n\x1b[?25h\x1b[?2026l"},
		{"not pushed preserve", false, true, "\x1b[?2026h" + exit + "\x1b[?25h\x1b[?2026l"},
		{"not pushed document", false, false, "\x1b[?2026h" + exit + "\x1b[?7l\x1b[0m\x1b[?7h\r\n\x1b[?25h\x1b[?2026l"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetKeyboardProtocolState(tc.pushed, false, false)
			t.Cleanup(func() { resetKeyboardProtocolState(false, false, false) })

			var out bytes.Buffer
			mouse := false
			alt := NewTuiAltScreenWithOutput(&out, 20, 5, TuiAltScreenOptions{Mouse: &mouse})
			alt.BeforeTerminalStart()
			if got, want := out.String(), enter+rest; got != want {
				t.Fatalf("BeforeTerminalStart wrote\n got %q\nwant %q", got, want)
			}

			out.Reset()
			alt.StopWithOptions(StopOptions{PreserveScreen: tc.preserve})
			if got := out.String(); !strings.HasSuffix(got, tc.wantExit) {
				t.Fatalf("StopWithOptions(PreserveScreen=%v) wrote\n got %q\nwant suffix %q", tc.preserve, got, tc.wantExit)
			}
			if got := out.String(); strings.Contains(got, "\x1b[<u") || strings.Contains(got, "\x1b[>7u") {
				t.Errorf("StopWithOptions moved the keyboard stack: %q", got)
			}
		})
	}
}

// DrainInput pops while the alternate screen is active and clears the push state, so the later exit writes neither a pop nor a push.
func TestTuiAltScreenStopAfterDrainWritesNoKeyboardProtocol(t *testing.T) {
	resetKeyboardProtocolState(true, false, true)
	t.Cleanup(func() { resetKeyboardProtocolState(false, false, false) })

	var out bytes.Buffer
	mouse := false
	alt := NewTuiAltScreenWithOutput(&out, 20, 5, TuiAltScreenOptions{Mouse: &mouse})
	alt.BeforeTerminalStart()
	NewProcessTerminalWithOutput(nil, nil, &out).disableKeyboardProtocol()
	out.Reset()
	alt.StopWithOptions(StopOptions{})
	if got := out.String(); strings.Contains(got, "\x1b[<u") || strings.Contains(got, "\x1b[>7u") {
		t.Fatalf("exit after drain touched the keyboard stack: %q", got)
	}
}

// Pi's start() (tui.ts:956) writes, up to the first frame: the alternate screen's enter (beforeTerminalStart: tui-alt-screen.ts:347-385), the
// terminal's start (terminal.ts:220 bracketed paste, :237 the keyboard push and query with the Device Attributes sentinel), then hideCursor
// (tui.ts:966) and the colour-scheme reports (:967-969). The bytes of terminal.start() come from a ProcessTerminal on the same output, as the
// driver's raw mode writes them; the fullscreen renderer's contribution is the order around them.
func TestTuiAltScreenStartSequenceFollowsPiStart(t *testing.T) {
	t.Setenv("PI_PROGRAM_STATUS", "0") // no OSC 7501 query: the sequence below is Pi's without it
	resetKeyboardProtocolState(false, false, false)
	t.Cleanup(func() { resetKeyboardProtocolState(false, false, false) })

	var out bytes.Buffer
	mouse := false
	alt := NewTuiAltScreenWithOutput(&out, 20, 5, TuiAltScreenOptions{Mouse: &mouse})
	terminal := NewProcessTerminalWithOutput(nil, nil, &out)
	alt.BeforeTerminalStart()
	terminal.writeUnlogged("\x1b[?2004h")
	terminal.queryAndEnableKittyProtocol()
	alt.HideCursor()
	alt.StartRequestingRender()

	want := "\x1b[?1049h\x1b[?7l\x1b[2J\x1b[H\x1b[?25l" + // beforeTerminalStart
		"\x1b[?2004h" + "\x1b[>7u\x1b[?u" + "\x1b[c" + // terminal.start
		"\x1b[?25l" // hideCursor
	if got := out.String(); got != want {
		t.Fatalf("start wrote\n got %q\nwant %q", got, want)
	}
}

// A renderer's Start after a terminal handoff (the external editor, a suspend, a renderer swap) hides the cursor again, as the main screen's does,
// and writes neither the alternate screen's enter nor any keyboard operation: BeforeTerminalStart and the terminal's start ran before it.
func TestTuiAltScreenStartAfterHandoffWritesOnlyTheRendererTail(t *testing.T) {
	resetKeyboardProtocolState(false, false, false)
	var out bytes.Buffer
	mouse := false
	alt := NewTuiAltScreenWithOutput(&out, 20, 5, TuiAltScreenOptions{Mouse: &mouse})
	alt.BeforeTerminalStart()
	out.Reset()
	alt.Start()
	got := out.String()
	if !strings.HasPrefix(got, "\x1b[?25l") {
		t.Fatalf("Start wrote %q, want the cursor hide first", got)
	}
	for _, forbidden := range []string{"\x1b[?1049h", "\x1b[<u", "\x1b[>7u", "\x1b[?u", "\x1b[c"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("Start wrote %q: %q", forbidden, got)
		}
	}
}
