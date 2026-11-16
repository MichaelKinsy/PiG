//go:build linux

package codingagent_test

import (
	"fmt"
	"strings"
	"syscall"
	"testing"
)

// Pi's shutdown() ends the color scheme reports and drains the input before it stops (interactive-mode.ts:4475-4505), and the frame that the exit
// key requested is rendered on the next tick, which falls inside the drain: `?2031l`, the drain's keyboard pop when the protocol is on, the frame,
// the progress clear, the renderer's stop. A frame that pig paints before the exit starts puts it ahead of `?2031l`.
//
// The terminal bytes are read from the moment the exit is sent: nothing may precede `?2031l`, and something (the frame) must lie between it and
// the progress clear.
func TestExitFrameRequestedByTheExitIsPaintedInsideTheDrain(t *testing.T) {
	const (
		colorSchemeOff = "\x1b[?2031l"
		keyboardPop    = "\x1b[<u"
		progressClear  = "\x1b]9;4;0\x07"
	)
	var sentAt int
	signalChild := func(sig syscall.Signal) func(*testing.T, *exitBytesChild) {
		return func(t *testing.T, child *exitBytesChild) {
			sentAt = child.captured.len()
			if err := child.cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
		}
	}
	keys := func(data string) func(*testing.T, *exitBytesChild) {
		return func(_ *testing.T, child *exitBytesChild) {
			sentAt = child.captured.len()
			child.write(data)
		}
	}
	ends := []struct {
		name string
		run  bool
		// frame is whether the exit has a frame to paint: a signal's idle exit in this child has no dispose that requests one.
		frame bool
		end   func(*testing.T, *exitBytesChild)
	}{
		{"ctrl+d", false, true, keys("\x04")},
		{"quit command", false, true, func(_ *testing.T, child *exitBytesChild) {
			from := child.captured.len()
			child.write("/quit")
			child.await("the editor to show /quit", func() bool { return strings.Contains(child.captured.from(from), "/quit") })
			sentAt = child.captured.len()
			child.write("\r")
		}},
		// The first ctrl+c is its own event with its own frame, which Pi paints before the second one exits.
		{"ctrl+c twice", false, true, func(_ *testing.T, child *exitBytesChild) {
			from := child.captured.len()
			child.write("\x03")
			child.await("the first ctrl+c's frame", func() bool {
				frame := child.captured.from(from)
				return strings.HasSuffix(frame, "\x1b[?25l") || strings.HasSuffix(frame, "\x1b[?2026l")
			})
			sentAt = child.captured.len()
			child.write("\x03")
		}},
		{"ctrl+d after a run", true, true, keys("\x04")},
		{"SIGTERM", false, false, signalChild(syscall.SIGTERM)},
	}
	for _, tuiMode := range []string{"regular", "fullscreen"} {
		for _, end := range ends {
			t.Run(fmt.Sprintf("%s in %s", end.name, tuiMode), func(t *testing.T) {
				child := startExitBytesChild(t, true, end.run, exitBytesTuiModeEnv+"="+tuiMode)
				child.await("the mode to be ready for input", child.ready)
				end.end(t, child)
				child.finish()
				// The pseudo-terminal's copy ends with the process: what it holds after the exit was sent is the exit's.
				<-child.copied
				exit := child.captured.from(sentAt)
				off := strings.Index(exit, colorSchemeOff)
				clear := strings.Index(exit, progressClear)
				if off < 0 || clear < off {
					t.Fatalf("the exit wrote the color scheme off at %d and the progress clear at %d, want them in that order\n%q", off, clear, exit)
				}
				if off != 0 {
					t.Fatalf("%q precedes the color scheme off: the frame that the exit requested was painted before the exit started (Pi paints it inside the drain)", exit[:off])
				}
				if between := strings.TrimPrefix(exit[off+len(colorSchemeOff):clear], keyboardPop); end.frame && between == "" {
					t.Fatalf("no frame between the color scheme off and the progress clear (Pi paints the requested frame inside the drain)\n%q", exit)
				}
			})
		}
	}
}
