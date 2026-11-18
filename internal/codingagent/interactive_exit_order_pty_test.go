//go:build linux

package codingagent_test

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// exitModeResets matches the cursor, bracketed paste and alternate screen resets of an exit.
var exitModeResets = regexp.MustCompile("\x1b\\[\\?(?:25h|2004l|1049l)")

// Pi's exit writes, in each ui.stop() (tui.ts stop(): beforeTerminalStop, terminal.showCursor(), terminal.stop(), afterTerminalStop), the cursor show and
// bracketed paste off BEFORE the alternate screen is left. A fullscreen exit that prints the transcript stops twice: the alternate screen's stop
// (`?25h ?2004l ?1049l ?25h`), then the main-screen stop that parks below the transcript (`?25h ?2004l`). An inline exit is the second stop alone.
// Nothing follows the last bracketed paste off (this child negotiates no keyboard protocol): pig's own cursor show after the stop is gone, and no other
// writer of the terminal (title, OSC 52, progress keepalive) may write after it.
// Pi 1.5.2 captured on a pty, ctrl+d, /quit, ctrl+c twice, SIGTERM and SIGHUP alike.
func TestExitModeResetsFollowPiStopOrder(t *testing.T) {
	ends := []struct {
		name string
		run  bool
		end  func(*testing.T, *exitBytesChild)
	}{
		{"ctrl+d", false, func(_ *testing.T, c *exitBytesChild) { c.write("\x04") }},
		{"quit command", false, func(_ *testing.T, c *exitBytesChild) {
			from := c.captured.len()
			c.write("/quit")
			c.await("the editor to show /quit", func() bool { return strings.Contains(c.captured.from(from), "/quit") })
			c.write("\r")
		}},
		{"ctrl+c twice", false, func(_ *testing.T, c *exitBytesChild) { c.write("\x03\x03") }},
		{"ctrl+d after a run", true, func(_ *testing.T, c *exitBytesChild) { c.write("\x04") }},
		{"SIGTERM", false, func(t *testing.T, c *exitBytesChild) {
			if err := c.cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
		}},
		{"SIGHUP", false, func(t *testing.T, c *exitBytesChild) {
			if err := c.cmd.Process.Signal(syscall.SIGHUP); err != nil {
				t.Fatal(err)
			}
		}},
		{"SIGTERM after a run", true, func(t *testing.T, c *exitBytesChild) {
			if err := c.cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tuiMode := range []string{"regular", "fullscreen"} {
		want := []string{"\x1b[?25h", "\x1b[?2004l"}
		if tuiMode == "fullscreen" {
			want = []string{"\x1b[?25h", "\x1b[?2004l", "\x1b[?1049l", "\x1b[?25h", "\x1b[?25h", "\x1b[?2004l"}
		}
		for _, progress := range []bool{false, true} {
			for _, end := range ends {
				t.Run(fmt.Sprintf("%s %s progress %v", tuiMode, end.name, progress), func(t *testing.T) {
					child := startExitBytesChild(t, progress, end.run, exitBytesTuiModeEnv+"="+tuiMode)
					child.awaitReady()
					end.end(t, child)
					written := child.finish()
					got := exitModeResets.FindAllString(written, -1)
					if len(got) < len(want) || !slices.Equal(got[len(got)-len(want):], want) {
						t.Fatalf("%s exit mode resets end %q, want Pi's %q\nlast bytes: %q", tuiMode, got, want, written[max(0, len(written)-300):])
					}
					// Pi's terminal.stop() ends with the bracketed paste off and, only where the keyboard protocols were on, their pops (this child's terminal
					// negotiates none): the exit path writes nothing after it, and the process terminal's other writers (the progress keepalive, a title, an
					// OSC 52 copy) have none after it to make while the mode ends. The title of the dispose is outside this child (no runtime host).
					if tail := written[strings.LastIndex(written, "\x1b[?2004l")+len("\x1b[?2004l"):]; tail != "" {
						t.Fatalf("%s exit wrote %q after the last bracketed paste off; Pi's stop() writes nothing after it", tuiMode, tail)
					}
				})
			}
		}
	}
}
