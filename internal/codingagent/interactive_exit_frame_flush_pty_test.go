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
// The terminal bytes are read from the moment the exit's keys are sent, and every row sends them in one write, so that no frame of an earlier key
// is in flight when the exit starts: nothing may precede `?2031l` (but the one frame that the first ctrl+c of the ctrl+c row paints at once), and
// something (the frame) must lie between it and the progress clear.
//
// The child is ready only once the start's own repaint has been requested and painted (exitBytesRepaintMark): the grammar load repaints after the start,
// as Pi's does (interactive-mode.ts:1163-1167), and a repaint that came due after the ready marker would fall into the bytes counted here.
func TestExitFrameRequestedByTheExitIsPaintedInsideTheDrain(t *testing.T) {
	const (
		colorSchemeOff = "\x1b[?2031l"
		keyboardPop    = "\x1b[<u"
		progressClear  = "\x1b]9;4;0\x07"
		hideCursor     = "\x1b[?25l"
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
		// before is how many frames the keys before the exit's own paint before the exit starts: the first ctrl+c clears the editor and paints at
		// once, and the exit's key follows it in the same read, so that no frame of the first one is in flight on the renderer when the exit starts.
		before int
		end    func(*testing.T, *exitBytesChild)
	}{
		{"ctrl+d", false, true, 0, keys("\x04")},
		// One write: a typed /quit would paint its own frame, and the completion list for it comes later from a worker, both before the exit and
		// both unordered with the exit's bytes in the pseudo-terminal's copy.
		{"quit command", false, true, 0, keys("/quit\r")},
		// The second ctrl+c within 500ms exits.
		{"ctrl+c twice", false, true, 1, keys("\x03\x03")},
		{"ctrl+d after a run", true, true, 0, keys("\x04")},
		{"SIGTERM", false, false, 0, signalChild(syscall.SIGTERM)},
	}
	for _, tuiMode := range []string{"regular", "fullscreen"} {
		for _, end := range ends {
			t.Run(fmt.Sprintf("%s in %s", end.name, tuiMode), func(t *testing.T) {
				child := startExitBytesChild(t, true, end.run, exitBytesTuiModeEnv+"="+tuiMode)
				child.awaitReady()
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
				if before := exit[:off]; end.before == 0 && before != "" {
					t.Fatalf("%q precedes the color scheme off: the frame that the exit requested was painted before the exit started (Pi paints it inside the drain)", before)
				} else if frames := strings.Count(before, hideCursor); frames > end.before {
					t.Fatalf("%d frames precede the color scheme off, want the %d of the first ctrl+c at most: the frame that the exit requested was painted before the exit started (Pi paints it inside the drain)\n%q", frames, end.before, before)
				}
				if between := strings.TrimPrefix(exit[off+len(colorSchemeOff):clear], keyboardPop); end.frame && between == "" {
					t.Fatalf("no frame between the color scheme off and the progress clear (Pi paints the requested frame inside the drain)\n%q", exit)
				}
			})
		}
	}
}

// A signal's shutdown disposes the runtime before it stops (interactive-mode.ts:4491-4494): the dispose writes the session title and its UI reset
// requests a frame, and the color scheme reports end and the input drains before stop(), so the frame is painted after `?2031l` and the keyboard
// pop (Pi 1.5.2 on a pseudo-terminal: `OSC 0`, `?2031l`, `<u`, the frame, the progress clear). The child of this test has a runtime host whose
// dispose runs the mode's UI reset and then, as a session_shutdown handler's setStatus does, shows a footer status that carries a marker text; it goes
// on for a while after that, as Pi's awaits do, so the frame comes due inside the dispose unless the signal's shutdown holds the frames.
//
// The dispose's frame is the one frame that carries the marker, so the test asserts where the marker is: after `?2031l` and before the progress
// clear, and nowhere before the color scheme off. It does not assert that nothing else is written before: a frame that was requested before the
// signal reached the child is painted before the dispose in Pi's loop too, however late the signal arrives, and carries no marker.
func TestExitSignalDisposeFrameIsPaintedInsideTheDrain(t *testing.T) {
	const (
		colorSchemeOff = "\x1b[?2031l"
		progressClear  = "\x1b]9;4;0\x07"
	)
	for _, tuiMode := range []string{"regular", "fullscreen"} {
		for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
			t.Run(fmt.Sprintf("%v in %s", sig, tuiMode), func(t *testing.T) {
				child := startExitBytesChild(t, true, false, exitBytesTuiModeEnv+"="+tuiMode, exitBytesRuntimeEnv+"=true", exitBytesSlowDisposeEnv+"=true")
				child.awaitReady()
				sentAt := child.captured.len()
				if err := child.cmd.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
				child.finish()
				<-child.copied
				exit := child.captured.from(sentAt)
				off := strings.Index(exit, colorSchemeOff)
				clear := strings.Index(exit, progressClear)
				if off < 0 || clear < off {
					t.Fatalf("the exit wrote the color scheme off at %d and the progress clear at %d, want them in that order\n%q", off, clear, exit)
				}
				at := strings.Index(exit, exitBytesDisposeStatus)
				switch {
				case at < 0:
					t.Fatalf("the dispose's frame (its status %q) was never painted (Pi paints it inside the drain)\n%q", exitBytesDisposeStatus, exit)
				case at < off:
					t.Fatalf("the dispose's frame was painted at %d, before the color scheme off at %d: the frame that the dispose requested was painted before the drain (Pi paints it inside it)\n%q", at, off, exit[:off])
				case at > clear:
					t.Fatalf("the dispose's frame was painted at %d, after the progress clear at %d (Pi paints it inside the drain)\n%q", at, clear, exit)
				}
			})
		}
	}
}
