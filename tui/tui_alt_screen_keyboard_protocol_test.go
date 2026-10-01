package tui

import (
	"bytes"
	"strings"
	"testing"
)

// The Kitty keyboard-protocol stack is per screen. Pi keeps one push on the
// active screen: tui.ts start() enters the alternate screen before
// terminal.start() pushes, and stop() pops in terminal.stop() before
// afterTerminalStop() leaves it. PiG's driver pushes on the main screen first,
// so TuiAltScreen moves an outstanding push across each screen switch and
// writes nothing when no push is outstanding (including for renderers that
// write to a non-terminal output).
func TestTuiAltScreenMovesOutstandingKeyboardPushAcrossScreens(t *testing.T) {
	const (
		pop   = "\x1b[<u"
		push  = "\x1b[>7u"
		enter = "\x1b[?1049h"
		exit  = "\x1b[?1049l"
		rest  = "\x1b[?7l\x1b[2J\x1b[H\x1b[?25l"
	)
	// StopWithOptions writes a teardown block, whose image-deletion content
	// depends on the detected terminal, then the exit block asserted exactly.
	for _, tc := range []struct {
		name      string
		pushed    bool
		preserve  bool
		wantStart string
		wantExit  string
	}{
		{
			name: "pushed preserve", pushed: true, preserve: true,
			wantStart: pop + enter + push + rest,
			wantExit:  "\x1b[?2026h" + pop + exit + push + "\x1b[?25h\x1b[?2026l",
		},
		{
			name: "pushed document", pushed: true, preserve: false,
			wantStart: pop + enter + push + rest,
			wantExit:  "\x1b[?2026h" + pop + exit + push + "\x1b[?7l\x1b[0m\x1b[?7h\r\n\x1b[?25h\x1b[?2026l",
		},
		{
			name: "not pushed preserve", pushed: false, preserve: true,
			wantStart: enter + rest,
			wantExit:  "\x1b[?2026h" + exit + "\x1b[?25h\x1b[?2026l",
		},
		{
			name: "not pushed document", pushed: false, preserve: false,
			wantStart: enter + rest,
			wantExit:  "\x1b[?2026h" + exit + "\x1b[?7l\x1b[0m\x1b[?7h\r\n\x1b[?25h\x1b[?2026l",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetKeyboardProtocolState(tc.pushed, false, false)
			t.Cleanup(func() { resetKeyboardProtocolState(false, false, false) })

			var out bytes.Buffer
			mouse := false
			alt := NewTuiAltScreenWithOutput(&out, 20, 5, TuiAltScreenOptions{Mouse: &mouse})
			alt.Start()
			started := out.String()
			if !strings.HasPrefix(started, tc.wantStart) {
				t.Fatalf("Start wrote\n got %q\nwant prefix %q", started, tc.wantStart)
			}
			// No query or Device Attributes probe: the process terminal's
			// negotiation already ran, and a non-terminal writer must not
			// receive probe traffic.
			for _, probe := range []string{"\x1b[?u", "\x1b[c"} {
				if strings.Contains(started, probe) {
					t.Errorf("Start wrote probe %q: %q", probe, started)
				}
			}

			out.Reset()
			alt.StopWithOptions(StopOptions{PreserveScreen: tc.preserve})
			if got := out.String(); !strings.HasSuffix(got, tc.wantExit) {
				t.Fatalf("StopWithOptions(PreserveScreen=%v) wrote\n got %q\nwant suffix %q", tc.preserve, got, tc.wantExit)
			}
			want := 0
			if tc.pushed {
				want = 1
			}
			if got := strings.Count(out.String(), pop) + strings.Count(out.String(), push); got != 2*want {
				t.Errorf("StopWithOptions wrote %d keyboard-stack operations, want %d", got, 2*want)
			}
		})
	}
}

// DrainInput pops while the alternate screen is active and clears the push
// state, so the later exit writes neither a pop nor a push. A pop here would
// hit the empty alternate stack, and a push would leave the Kitty flags set on
// the main screen after exit.
func TestTuiAltScreenStopAfterDrainWritesNoKeyboardProtocol(t *testing.T) {
	resetKeyboardProtocolState(true, false, true)
	t.Cleanup(func() { resetKeyboardProtocolState(false, false, false) })

	var out bytes.Buffer
	mouse := false
	alt := NewTuiAltScreenWithOutput(&out, 20, 5, TuiAltScreenOptions{Mouse: &mouse})
	alt.Start()
	NewProcessTerminalWithOutput(nil, nil, &out).disableKeyboardProtocol()
	out.Reset()
	alt.StopWithOptions(StopOptions{})
	if got := out.String(); strings.Contains(got, "\x1b[<u") || strings.Contains(got, "\x1b[>7u") {
		t.Fatalf("exit after drain touched the keyboard stack: %q", got)
	}
}
