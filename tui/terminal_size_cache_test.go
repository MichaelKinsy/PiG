//go:build linux || darwin

package tui

import (
	"testing"

	"golang.org/x/sys/unix"
)

// countSizeReads counts the TIOCGWINSZ reads ProcessTerminal makes for the duration of the test.
func countSizeReads(t *testing.T) *int {
	t.Helper()
	reads := 0
	saved := terminalGetSize
	terminalGetSize = func(fd int) (int, int, error) {
		reads++
		return saved(fd)
	}
	t.Cleanup(func() { terminalGetSize = saved })
	return &reads
}

func setPTYSize(t *testing.T, master interface{ Fd() uintptr }, cols, rows uint16) {
	t.Helper()
	if err := unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols}); err != nil {
		t.Fatal(err)
	}
}

// TestProcessTerminalKeepsSizeUntilResize: while a resize handler holds CacheSizeUntilResize, frames read the size the terminal
// last reported without a TIOCGWINSZ each, as Node keeps process.stdout.columns and rows between SIGWINCHs; invalidate (the
// handler's SIGWINCH) makes the next frame read the new size, and after stop every call reads the terminal again.
func TestProcessTerminalKeepsSizeUntilResize(t *testing.T) {
	master, slave := openTestPTY(t)
	setPTYSize(t, master, 97, 31)
	reads := countSizeReads(t)
	terminal := NewProcessTerminal(slave, slave)

	invalidate, stop := terminal.CacheSizeUntilResize()
	for range 100 { // a frame reads both: tuiBase.updateSize
		if terminal.Columns() != 97 || terminal.Rows() != 31 {
			t.Fatalf("size = %dx%d, want 97x31", terminal.Columns(), terminal.Rows())
		}
	}
	if *reads != 1 {
		t.Fatalf("100 frames read the window size %d times, want 1", *reads)
	}

	setPTYSize(t, master, 120, 40)
	if got := terminal.Columns(); got != 97 {
		t.Fatalf("before the SIGWINCH the kept width is %d, want 97 (Node's columns change only on SIGWINCH)", got)
	}
	invalidate()
	if terminal.Columns() != 120 || terminal.Rows() != 40 {
		t.Fatalf("after invalidate the size is %dx%d, want 120x40", terminal.Columns(), terminal.Rows())
	}

	stop()
	stop() // idempotent
	before := *reads
	setPTYSize(t, master, 50, 20)
	if terminal.Columns() != 50 || terminal.Rows() != 20 {
		t.Fatalf("after stop the size is %dx%d, want 50x20", terminal.Columns(), terminal.Rows())
	}
	if *reads-before != 2 {
		t.Fatalf("after stop Columns and Rows read the size %d times, want 2", *reads-before)
	}
}

// TestProcessTerminalWithoutResizeHandlerReadsSizeEveryTime: a terminal no resize handler holds (print and RPC modes, tests
// that swap terminals) never keeps a size.
func TestProcessTerminalWithoutResizeHandlerReadsSizeEveryTime(t *testing.T) {
	master, slave := openTestPTY(t)
	setPTYSize(t, master, 97, 31)
	reads := countSizeReads(t)
	terminal := NewProcessTerminal(slave, slave)
	for range 10 {
		terminal.Columns()
	}
	setPTYSize(t, master, 81, 25)
	if terminal.Columns() != 81 || terminal.Rows() != 25 {
		t.Fatalf("size = %dx%d, want 81x25", terminal.Columns(), terminal.Rows())
	}
	if *reads != 12 {
		t.Fatalf("12 calls read the size %d times, want 12", *reads)
	}
}

// TestProcessTerminalDropsSizeReadAcrossResize: a size read that a SIGWINCH overtakes (the window changed after the ioctl
// returned the old size, and the handler invalidated before the read was kept) is not kept: the next frame reads again.
func TestProcessTerminalDropsSizeReadAcrossResize(t *testing.T) {
	master, slave := openTestPTY(t)
	setPTYSize(t, master, 97, 31)
	terminal := NewProcessTerminal(slave, slave)
	invalidate, stop := terminal.CacheSizeUntilResize()
	defer stop()
	raced := false
	saved := terminalGetSize
	terminalGetSize = func(fd int) (int, int, error) {
		width, height, err := saved(fd)
		if !raced {
			raced = true
			setPTYSize(t, master, 120, 40)
			invalidate()
		}
		return width, height, err
	}
	t.Cleanup(func() { terminalGetSize = saved })
	if got := terminal.Columns(); got != 97 {
		t.Fatalf("the raced read returned %d, want the 97 it read", got)
	}
	if terminal.Columns() != 120 || terminal.Rows() != 40 {
		t.Fatalf("after the raced read the size is %dx%d, want 120x40", terminal.Columns(), terminal.Rows())
	}
}
