//go:build linux

package codingagent_test

import (
	"fmt"
	"regexp"
	"strings"
	"syscall"
	"testing"
)

var exitTitleOrReset = regexp.MustCompile("\x1b\\]0;[^\x07\x1b]*\x07|\x1b\\]9;4;0\x07|\x1b\\[\\?(?:2004l|1049l|2031l)|\x1b\\[<u")

// Pi writes the session title once at exit, in the quit's dispose (agent-session-runtime.ts:415-424, beforeSessionInvalidate -> resetExtensionUI ->
// updateTerminalTitle). A quit by keys stops the TUI first (shutdown(): this.stop(), then `await this.runtimeHost.dispose()`), so the title comes after the
// last mode reset; a signal's shutdown disposes first, so the title comes before every reset and before the progress clear.
// Pi 1.5.2 captured on a pty: quit `... ?2004l OSC0`, signal `OSC0 ?2031l <u ... ?2004l`.
func TestExitTitleIsWrittenInTheDisposeAtPisPlace(t *testing.T) {
	quits := []struct {
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
	}
	signals := []struct {
		name string
		run  bool
		sig  syscall.Signal
	}{{"SIGTERM", false, syscall.SIGTERM}, {"SIGHUP", false, syscall.SIGHUP}, {"SIGTERM after a run", true, syscall.SIGTERM}}
	for _, tuiMode := range []string{"regular", "fullscreen"} {
		for _, progress := range []bool{false, true} {
			for _, quit := range quits {
				t.Run(fmt.Sprintf("%s %s progress %v", tuiMode, quit.name, progress), func(t *testing.T) {
					written := runExitWithRuntime(t, tuiMode, progress, quit.run, quit.end)
					tokens := exitTitleOrReset.FindAllString(written, -1)
					if n := len(tokens); n < 2 || !strings.HasPrefix(tokens[n-1], "\x1b]0;") || tokens[n-2] != "\x1b[?2004l" {
						t.Fatalf("quit tokens = %q, want the title last, right after the final bracketed paste off\nlast bytes: %q", tokens, written[max(0, len(written)-300):])
					}
					if count := strings.Count(written, "\x1b]0;"); count != 1 {
						t.Fatalf("the exit wrote %d titles, want 1", count)
					}
				})
			}
			for _, sig := range signals {
				t.Run(fmt.Sprintf("%s %s progress %v", tuiMode, sig.name, progress), func(t *testing.T) {
					written := runExitWithRuntime(t, tuiMode, progress, sig.run, func(t *testing.T, c *exitBytesChild) {
						if err := c.cmd.Process.Signal(sig.sig); err != nil {
							t.Fatal(err)
						}
					})
					tokens := exitTitleOrReset.FindAllString(written, -1)
					if len(tokens) == 0 || !strings.HasPrefix(tokens[0], "\x1b]0;") {
						t.Fatalf("signal tokens = %q, want the title before every reset and the progress clear\nlast bytes: %q", tokens, written[max(0, len(written)-300):])
					}
					if count := strings.Count(written, "\x1b]0;"); count != 1 {
						t.Fatalf("the exit wrote %d titles, want 1", count)
					}
				})
			}
		}
	}
}

func runExitWithRuntime(t *testing.T, tuiMode string, progress, run bool, end func(*testing.T, *exitBytesChild)) string {
	t.Helper()
	child := startExitBytesChild(t, progress, run, exitBytesTuiModeEnv+"="+tuiMode, exitBytesRuntimeEnv+"=true")
	child.awaitReady()
	end(t, child)
	return child.finish()
}
