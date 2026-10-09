//go:build linux || darwin

package tui

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type ptyOutput struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (o *ptyOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.data.String()
}

func collectPTYOutput(master *os.File) *ptyOutput {
	out := &ptyOutput{}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				out.mu.Lock()
				out.data.Write(buf[:n])
				out.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return out
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ProcessTerminal on a real pseudo-terminal (Linux and macOS): raw mode while started, the keyboard-protocol query, input
// forwarding, the live window size, the SIGWINCH-driven resize callback, and the restored line discipline after Stop. These are
// the terminal.ts start/stop/columns/rows/onResize behaviors that need a kernel terminal.
func TestProcessTerminalOnPseudoTerminal(t *testing.T) {
	t.Setenv("PI_PROGRAM_STATUS", "")
	master, slave := openTestPTY(t)
	output := collectPTYOutput(master)
	if err := unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 31, Col: 97}); err != nil {
		t.Fatal(err)
	}
	before, err := unix.IoctlGetTermios(int(slave.Fd()), ttyGetAttributes)
	if err != nil {
		t.Fatal(err)
	}
	if before.Lflag&unix.ICANON == 0 {
		t.Fatalf("a new pseudo-terminal should start in canonical mode, lflag %#x", before.Lflag)
	}

	terminal := NewProcessTerminal(slave, slave)
	if terminal.Columns() != 97 || terminal.Rows() != 31 {
		t.Fatalf("size = %dx%d, want 97x31", terminal.Columns(), terminal.Rows())
	}

	var mu sync.Mutex
	var received []byte
	resizes := 0
	if err := terminal.Start(func(data string) {
		mu.Lock()
		received = append(received, data...)
		mu.Unlock()
	}, func() {
		mu.Lock()
		resizes++
		mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			terminal.Stop()
		}
	})

	raw, err := unix.IoctlGetTermios(int(slave.Fd()), ttyGetAttributes)
	if err != nil {
		t.Fatal(err)
	}
	if raw.Lflag&(unix.ICANON|unix.ECHO) != 0 {
		t.Fatalf("Start left canonical/echo mode on: lflag %#x", raw.Lflag)
	}
	waitFor(t, "the keyboard protocol query", func() bool { return strings.Contains(output.String(), "\x1b[>7u\x1b[?u\x1b]7501;?\x1b\\\x1b[c") })

	// The startup kick reports the first resize; a window change then raises SIGWINCH.
	waitFor(t, "the startup resize", func() bool { mu.Lock(); defer mu.Unlock(); return resizes >= 1 })
	mu.Lock()
	base := resizes
	mu.Unlock()
	if err := unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 40, Col: 120}); err != nil {
		t.Fatal(err)
	}
	// The pseudo-terminal is not this process's controlling terminal, so the kernel does not signal it; deliver what a terminal window change sends.
	if err := unix.Kill(os.Getpid(), unix.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the resize callback", func() bool { mu.Lock(); defer mu.Unlock(); return resizes > base })
	if terminal.Columns() != 120 || terminal.Rows() != 40 {
		t.Fatalf("size after resize = %dx%d, want 120x40", terminal.Columns(), terminal.Rows())
	}

	// A non-ASCII chunk and an escape sequence arrive intact and unechoed.
	if _, err := master.Write([]byte("héllo \x1b[A")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "forwarded input", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(string(received), "héllo ") && strings.Contains(string(received), "\x1b[A")
	})
	if strings.Contains(output.String(), "héllo") {
		t.Fatal("raw mode echoed the input")
	}

	terminal.Stop()
	stopped = true
	after, err := unix.IoctlGetTermios(int(slave.Fd()), ttyGetAttributes)
	if err != nil {
		t.Fatal(err)
	}
	if after.Lflag != before.Lflag || after.Iflag != before.Iflag || after.Oflag != before.Oflag {
		t.Fatalf("Stop did not restore the terminal modes: before %+v after %+v", before, after)
	}
	waitFor(t, "bracketed paste disable on stop", func() bool { return strings.Contains(output.String(), "\x1b[?2004l") })
}
