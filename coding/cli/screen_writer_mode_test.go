package cli

import (
	"bytes"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Print, JSON and RPC mode show no screen: after applyScreenWriterMode a terminal has no screen writer, with PIG_TUI_SCREEN_WRITER
// asking for one too, so every byte written to it arrives as written. Interactive mode leaves the choice to the environment and the
// setting. A frame written twice tells the two apart: the screen writer does not send the second one.
func TestOnlyInteractiveModeHasAScreenWriter(t *testing.T) {
	const enter = "\x1b[?1049h"
	frame := "\x1b[?2026h\x1b[1;1H\x1b[2Kone\x1b[0m\x1b]8;;\x07\x1b[1;1H\x1b[?25l\x1b[?2026l"
	repaint := "\x1b[?2026h\x1b[2J\x1b[1;1H\x1b[2K\x1b[0m\x1b]8;;\x07\x1b[1;1H\x1b[?25l\x1b[?2026l"
	for _, tc := range []struct {
		mode   appMode
		writer bool
	}{{appModePrint, false}, {appModeJSON, false}, {appModeRPC, false}, {appModeInteractive, true}} {
		t.Run(string(tc.mode), func(t *testing.T) {
			t.Setenv("PIG_TUI_SCREEN_WRITER", "1")
			// The choice is the process's: the next test starts from the setting's "writer", which leaves the environment to decide.
			t.Cleanup(func() { tui.SetScreenWriter(true) })
			tui.SetScreenWriter(true)
			applyScreenWriterMode(tc.mode)
			var out bytes.Buffer
			terminal := tui.NewProcessTerminalWithOutput(nil, nil, &out)
			terminal.Write(enter)
			terminal.Write(repaint)
			terminal.Write(repaint)
			terminal.Write(frame)
			if got := terminal.ScreenWriterActive(); got != tc.writer {
				t.Errorf("a screen writer is active on the alternate screen = %v, want %v", got, tc.writer)
			}
			before := out.Len()
			terminal.Write(frame)
			if sent := out.String()[before:]; (sent == frame) == tc.writer {
				t.Errorf("the second identical frame was sent as %q; screen writer wanted = %v", sent, tc.writer)
			}
		})
	}
}
