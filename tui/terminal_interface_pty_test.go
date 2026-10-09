//go:build linux

package tui

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func openSizedTestPTY(t *testing.T, rows, cols uint16) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlock pseudo-terminal: %v", err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("pseudo-terminal number: %v", err)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open pseudo-terminal slave: %v", err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	if err := unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols}); err != nil {
		t.Fatalf("set pseudo-terminal size: %v", err)
	}
	return master, slave
}

// Pi's start() enters raw mode and forwards stdin data to onInput; stop() reverses it, and drainInput() discards pending
// bytes. The test drives a real pseudo-terminal through the Terminal interface.
// Pi source: packages/tui/src/terminal.ts
// mutation-checked: zeroing the results of Terminal.Columns, Terminal.Rows fails it
// Pi: packages/tui/src/terminal.ts:91 (rows)
// Pi's start() enters raw mode and forwards stdin data to onInput (packages/tui/src/terminal.ts:174); stop() reverses it
// (terminal.ts:429), drainInput() discards pending bytes (terminal.ts:391), and columns and rows read the terminal's size
// (terminal.ts:488, terminal.ts:492). The test drives a real pseudo-terminal through the Terminal interface.
func TestTerminalInterfaceStartForwardsInputAndStopEndsIt(t *testing.T) {
	master, slave := openSizedTestPTY(t, 31, 101)
	var term Terminal = NewProcessTerminalWithOutput(slave, slave, &ifaceOutput{})
	if term.Columns() != 101 || term.Rows() != 31 {
		t.Fatalf("size = %dx%d, want the pseudo-terminal's 101x31", term.Columns(), term.Rows())
	}
	got := make(chan string, 8)
	if err := term.Start(func(data string) { got <- data }, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := master.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	select {
	case data := <-got:
		if data != "a" {
			t.Fatalf("onInput = %q, want a", data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not forward terminal input")
	}
	term.Stop()
	if _, err := master.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}
	if err := term.DrainInput(500*time.Millisecond, 20*time.Millisecond); err != nil {
		t.Fatalf("DrainInput: %v", err)
	}
	select {
	case data := <-got:
		t.Fatalf("input %q arrived after Stop", data)
	case <-time.After(100 * time.Millisecond):
	}
}
