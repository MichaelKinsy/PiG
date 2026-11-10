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

// A real pseudo-terminal as the process's stdin and stdout: the part of tui_terminal.go that talks to the OS (the size, the TTY
// answer, raw mode and SIGWINCH) as Node's `process.stdin`/`process.stdout` do for packages/chord/src/node/tui/launcher.ts.

// openPTY is a pseudo-terminal: its master and its slave, closed at the test's end.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	return master, slave
}

func setSize(t *testing.T, file *os.File, columns, rows uint16) {
	t.Helper()
	if err := unix.IoctlSetWinsize(int(file.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: columns, Row: rows}); err != nil {
		t.Fatal(err)
	}
}

func modeOf(t *testing.T, file *os.File) *unix.Termios {
	t.Helper()
	termios, err := unix.IoctlGetTermios(int(file.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	return termios
}

func TestTheProcessTerminalOnAPseudoTerminalReportsItsSizeAndThatItIsATTY(t *testing.T) {
	master, slave := openPTY(t)
	swapStdio(t, slave, slave)
	terminal := &ProcessTerminal{}
	if !terminal.IsTTY() {
		t.Fatal("a pseudo-terminal on stdin is no TTY")
	}
	setSize(t, master, 132, 43)
	if terminal.Columns() != 132 || terminal.Rows() != 43 {
		t.Fatalf("size %dx%d, want 132x43", terminal.Columns(), terminal.Rows())
	}
	setSize(t, master, 100, 30)
	if terminal.Columns() != 100 || terminal.Rows() != 30 {
		t.Fatalf("size after a resize %dx%d, want 100x30", terminal.Columns(), terminal.Rows())
	}
	// a pseudo-terminal may report none (a fresh one is 0x0): the launcher defaults it then
	setSize(t, master, 0, 0)
	if terminal.Columns() != 0 || terminal.Rows() != 0 {
		t.Fatalf("size of a terminal that reports none %dx%d, want 0x0", terminal.Columns(), terminal.Rows())
	}
}

func TestTheProcessTerminalSetsRawModeOnStdinAndRestoresTheModeItSaved(t *testing.T) {
	_, slave := openPTY(t)
	swapStdio(t, slave, slave)
	before := *modeOf(t, slave)
	if before.Lflag&(unix.ICANON|unix.ECHO|unix.ISIG) != unix.ICANON|unix.ECHO|unix.ISIG {
		t.Fatalf("fixture: a fresh terminal is not cooked: lflag %#x", before.Lflag)
	}
	terminal := &ProcessTerminal{}
	if err := terminal.SetRaw(true); err != nil {
		t.Fatal(err)
	}
	raw := modeOf(t, slave)
	if raw.Lflag&(unix.ICANON|unix.ECHO|unix.ISIG|unix.IEXTEN) != 0 || raw.Iflag&(unix.ICRNL|unix.IXON|unix.ISTRIP) != 0 || raw.Oflag&unix.OPOST != 0 || raw.Cflag&unix.CSIZE != unix.CS8 {
		t.Fatalf("raw mode not set: iflag %#x oflag %#x lflag %#x cflag %#x", raw.Iflag, raw.Oflag, raw.Lflag, raw.Cflag)
	}
	if raw.Cc[unix.VMIN] != 1 || raw.Cc[unix.VTIME] != 0 {
		t.Fatalf("raw reads wait for VMIN %d VTIME %d, want one byte, no timeout", raw.Cc[unix.VMIN], raw.Cc[unix.VTIME])
	}
	if err := terminal.SetRaw(false); err != nil {
		t.Fatal(err)
	}
	if after := *modeOf(t, slave); after != before {
		t.Fatalf("mode after clearing raw = %+v, want the one before %+v", after, before)
	}
	// clearing again has nothing saved: the mode a program set meanwhile stays
	changed := modeOf(t, slave)
	changed.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(int(slave.Fd()), unix.TCSETS, changed); err != nil {
		t.Fatal(err)
	}
	if err := terminal.SetRaw(false); err != nil {
		t.Fatal(err)
	}
	if got := modeOf(t, slave); got.Lflag&unix.ECHO != 0 {
		t.Fatal("a second clear restored a state that was already restored")
	}
}

func TestTheProcessTerminalCallsTheResizeHandlerOnEachSIGWINCHUntilItIsRemoved(t *testing.T) {
	_, slave := openPTY(t)
	swapStdio(t, slave, slave)
	terminal := &ProcessTerminal{}
	resized := make(chan struct{}, 8)
	stop := terminal.OnResize(func() { resized <- struct{}{} })
	signalSelf := func() {
		t.Helper()
		if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
			t.Fatal(err)
		}
	}
	expect := func(n int) {
		t.Helper()
		for range n {
			select {
			case <-resized:
			case <-time.After(10 * time.Second):
				t.Fatal("the resize handler was not called")
			}
		}
	}
	signalSelf()
	expect(1)
	signalSelf()
	expect(1)
	stop()
	stop() // removing twice is harmless
	signalSelf()
	// a SIGWINCH after the removal is not delivered: give the (stopped) goroutine every chance to run it
	select {
	case <-resized:
		t.Fatal("the handler ran after its removal")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestTwoResizeHandlersEachGetTheSignalAndRemovingOneKeepsTheOther(t *testing.T) {
	_, slave := openPTY(t)
	swapStdio(t, slave, slave)
	terminal := &ProcessTerminal{}
	first, second := make(chan struct{}, 8), make(chan struct{}, 8)
	stopFirst := terminal.OnResize(func() { first <- struct{}{} })
	stopSecond := terminal.OnResize(func() { second <- struct{}{} })
	defer stopSecond()
	wait := func(c chan struct{}) {
		t.Helper()
		select {
		case <-c:
		case <-time.After(10 * time.Second):
			t.Fatal("a resize handler was not called")
		}
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	wait(first)
	wait(second)
	stopFirst()
	if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	wait(second)
	select {
	case <-first:
		t.Fatal("a removed handler ran")
	case <-time.After(300 * time.Millisecond):
	}
}

// Node's `process.stdout.columns` and `rows` are the output's, `process.stdin.isTTY` and `setRawMode` the input's: with the two on
// different terminals each answer comes from its own stream.
func TestTheProcessTerminalTakesItsSizeFromStdoutAndItsTTYAnswerFromStdin(t *testing.T) {
	inputMaster, input := openPTY(t)
	outputMaster, output := openPTY(t)
	setSize(t, inputMaster, 50, 10)
	setSize(t, outputMaster, 132, 43)
	swapStdio(t, input, output)
	terminal := &ProcessTerminal{}
	if !terminal.IsTTY() || terminal.Columns() != 132 || terminal.Rows() != 43 {
		t.Fatalf("tty %v size %dx%d, want true 132x43 (the output's)", terminal.IsTTY(), terminal.Columns(), terminal.Rows())
	}
	// a terminal in, a pipe out: a TTY with no size
	_, pipe := pipeFile(t)
	swapStdio(t, input, pipe)
	if !terminal.IsTTY() || terminal.Columns() != 0 || terminal.Rows() != 0 {
		t.Fatalf("pipe out: tty %v size %dx%d, want true 0x0", terminal.IsTTY(), terminal.Columns(), terminal.Rows())
	}
	// a pipe in, a terminal out: no TTY, the output's size
	reader, _ := pipeFile(t)
	swapStdio(t, reader, output)
	if terminal.IsTTY() || terminal.Columns() != 132 || terminal.Rows() != 43 {
		t.Fatalf("pipe in: tty %v size %dx%d, want false 132x43", terminal.IsTTY(), terminal.Columns(), terminal.Rows())
	}
}
