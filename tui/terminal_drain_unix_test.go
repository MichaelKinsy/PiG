//go:build unix

package tui

import (
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
	if _, err := w.Write([]byte("abc")); err != nil {
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
