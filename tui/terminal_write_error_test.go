package tui

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

// Pi's InteractiveMode.registerSignalHandlers listens for "error" on process.stdout and process.stderr, and a dead-terminal error (EIO after
// a hang-up, EPIPE) ends the process with emergencyTerminalExit. A failed terminal write must reach the driver's handler instead of vanishing.
func TestFailedTerminalWriteReachesTheWriteErrorHandler(t *testing.T) {
	var got []error
	remove := OnTerminalWriteError(func(err error) { got = append(got, err) })
	t.Cleanup(remove)
	failure := &os.PathError{Op: "write", Path: "/dev/pts/9", Err: syscall.EIO}
	terminal := NewProcessTerminalWithOutput(nil, nil, failingWriter{failure})
	terminal.Write("frame")
	terminal.writeUnlogged("\x1b[?25l")
	if len(got) != 2 || !errors.Is(got[0], syscall.EIO) || !errors.Is(got[1], syscall.EIO) {
		t.Fatalf("handler saw %v, want both failed writes with EIO", got)
	}
	remove()
	terminal.Write("frame")
	if len(got) != 2 {
		t.Fatalf("a removed handler still ran: %v", got)
	}
}

func TestTerminalWriteThatSucceedsNeverReachesTheWriteErrorHandler(t *testing.T) {
	remove := OnTerminalWriteError(func(err error) { t.Errorf("handler ran for a successful write: %v", err) })
	t.Cleanup(remove)
	NewProcessTerminalWithOutput(nil, nil, &discardWriter{}).Write("frame")
}

type discardWriter struct{}

func (*discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// A component's terminal.write goes through WriteRaw (the remote editor's TerminalWrite); in Pi it is process.stdout.write, so its failure
// reaches the same stdout "error" listener.
func TestFailedRawWriteReachesTheWriteErrorHandler(t *testing.T) {
	var got []error
	remove := OnTerminalWriteError(func(err error) { got = append(got, err) })
	t.Cleanup(remove)
	NewWithOutput(failingWriter{syscall.EIO}, 80, 24).WriteRaw("data")
	if len(got) != 1 || !errors.Is(got[0], syscall.EIO) {
		t.Fatalf("handler saw %v, want the failed raw write with EIO", got)
	}
}

// HideCursor, ShowCursor and the renderer's other writes are process.stdout.write in Pi, so a failure reaches the same listener.
func TestFailedCursorWriteReachesTheWriteErrorHandler(t *testing.T) {
	var got []error
	remove := OnTerminalWriteError(func(err error) { got = append(got, err) })
	t.Cleanup(remove)
	screen := NewWithOutput(failingWriter{syscall.EIO}, 80, 24)
	screen.HideCursor()
	screen.ShowCursor()
	if len(got) != 2 || !errors.Is(got[0], syscall.EIO) || !errors.Is(got[1], syscall.EIO) {
		t.Fatalf("handler saw %v, want both failed cursor writes with EIO", got)
	}
}
