//go:build linux

package codingagent_test

import (
	"fmt"
	"regexp"
	"strings"
	"syscall"
	"testing"
)

// exitStormWrites matches what the storm's writers write: an extension's title, an OSC 52 copy of the marker and a progress-active update.
var exitStormWrites = regexp.MustCompile("\x1b\\]0;STORM\x07|\x1b\\]52;c;U1RPUk0=\x07|\x1b\\]9;4;3\x07")

// exitStopStart returns where Pi's stop() sequence starts in what a pty received: the synchronized-output frame that the fullscreen stop opens for the
// mouse and autowrap resets when there is one, else the cursor park that the main-screen stop writes before its cursor show.
// written ends at the exit's last bracketed paste off.
func exitStopStart(written string) int {
	reset := regexp.MustCompile("\x1b\\[\\?(?:25h|2004l|1049l)").FindAllStringIndex(written, -1)
	first := reset[len(reset)-2][0] // regular: ?25h ?2004l
	if len(reset) >= 6 && strings.Contains(written[reset[len(reset)-6][0]:], "\x1b[?1049l") {
		first = reset[len(reset)-6][0] // fullscreen: ?25h ?2004l ?1049l ?25h ?25h ?2004l
	}
	if group := strings.LastIndex(written[:first], "\x1b[?2026h"); group >= 0 && strings.Contains(written[group:first], "\x1b[?7h") {
		return group
	}
	park := regexp.MustCompile(" ?(?:\x1b\\[\\d+[AB])?\r*\n$").FindStringIndex(written[:first])
	if park == nil {
		return first
	}
	return park[0]
}

// Pi's stop() runs in one synchronous call: no extension handler, clipboard write or progress update of its event loop can write between its bytes
// (tui.ts :1046 stop(); terminal.ts stop()), and after a signal's stop() process.exit(0) ends the process with nothing more written (interactive-mode.ts
// :4491-4494). PiG's writers are goroutines: with an extension's title, an OSC 52 copy and the progress indicator writing at full pace from the moment
// the mode is ready, none of their bytes may land between the first and the last byte of the stop sequence, and after a signal's stop none at all.
func TestExitStopSequenceHoldsNoWriterOfAnotherGoroutine(t *testing.T) {
	ends := []struct {
		name   string
		run    bool
		signal bool
		end    func(*testing.T, *exitBytesChild)
	}{
		{"ctrl+d", false, false, func(_ *testing.T, c *exitBytesChild) { c.write("\x04") }},
		{"ctrl+c twice", false, false, func(_ *testing.T, c *exitBytesChild) { c.write("\x03\x03") }},
		{"ctrl+d after a run", true, false, func(_ *testing.T, c *exitBytesChild) { c.write("\x04") }},
		{"SIGTERM", false, true, func(t *testing.T, c *exitBytesChild) {
			if err := c.cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
		}},
		{"SIGHUP", false, true, func(t *testing.T, c *exitBytesChild) {
			if err := c.cmd.Process.Signal(syscall.SIGHUP); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tuiMode := range []string{"regular", "fullscreen"} {
		for _, end := range ends {
			t.Run(fmt.Sprintf("%s %s", tuiMode, end.name), func(t *testing.T) {
				// The child's terminal option shows the renderer's bytes; the clipboard and the storm's other writers use the process's stdout, so the
				// pseudo-terminal's capture is the whole stream.
				child := startExitBytesChild(t, true, end.run, exitBytesTuiModeEnv+"="+tuiMode, exitBytesStormEnv+"=true", "PI_CLIPBOARD_OSC52=1")
				child.awaitReady()
				mark := child.captured.len()
				child.await("the storm to write", func() bool { return exitStormWrites.MatchString(child.captured.from(mark)) })
				end.end(t, child)
				child.finish()
				// The child's test binary prints PASS once its main returns, which is after the exit path ended: what follows is outside the exit.
				stream, _, _ := strings.Cut(child.captured.from(0), "PASS\r\n")
				last := strings.LastIndex(stream, "\x1b[?2004l")
				if last < 0 {
					t.Fatalf("no bracketed paste off in %q", stream[max(0, len(stream)-300):])
				}
				start := exitStopStart(stream[:last+len("\x1b[?2004l")])
				stop := stream[start : last+len("\x1b[?2004l")]
				if got := exitStormWrites.FindAllString(stop, -1); len(got) > 0 {
					t.Fatalf("%d writes of other goroutines inside Pi's stop sequence (%q): %q", len(got), got, stop)
				}
				if tail := stream[last+len("\x1b[?2004l"):]; end.signal && exitStormWrites.MatchString(tail) {
					t.Fatalf("after a signal's stop() Pi's process.exit(0) writes nothing more, got %q", tail)
				}
			})
		}
	}
}
