//go:build unix

package tui

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// DrainInput consumes pending stdin bytes on unix (poll-backed). On Windows it
// is a documented no-op, so this behavioral check is unix-only.
func TestProcessTerminalDrainInput_ConsumesPendingBytes(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()

	term := NewProcessTerminalWithOutput(r, nil, ioDiscard{})
	if _, err := w.WriteString("abc"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := term.DrainInput(200*time.Millisecond, 20*time.Millisecond); err != nil {
		t.Fatalf("DrainInput: %v", err)
	}

	pollfds := []unix.PollFd{{Fd: int32(r.Fd()), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(pollfds, 1)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			t.Fatalf("poll after drain: %v", err)
		}
		if n != 0 {
			t.Fatalf("expected stdin pipe to be drained, poll reported %d ready fds", n)
		}
		break
	}
}

// A drain whose read fails reports the error: a hung-up terminal reads EOF, and Pi's stdin error handler turns the
// failure into emergencyTerminalExit (exit 129). The input is a pipe whose writer closed after writing, so the drain
// reads the pending bytes and then EOF.
func TestProcessTerminalDrainInputReportsAFailedRead(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer func() { _ = r.Close() }()
	if _, err := w.WriteString("abc"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	term := NewProcessTerminalWithOutput(r, nil, ioDiscard{})
	if err := term.DrainInput(time.Second, 200*time.Millisecond); !errors.Is(err, io.EOF) {
		t.Fatalf("DrainInput = %v, want io.EOF", err)
	}
}
