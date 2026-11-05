//go:build unix

package tui

import (
	"bytes"
	"errors"
	"os"
	"syscall"
	"testing"

	"golang.org/x/term"
)

// libuv's uv_tty_set_mode and uv__tcsetattr retry the termios ioctl while it fails with EINTR, so Node's setRawMode(true) and
// setRawMode(false) return after the process was stopped inside the call and continued. A background resume (Ctrl+Z, bg, fg)
// stops the process in the ioctl with SIGTTOU; on macOS the call then fails with EINTR when fg continues it, and the error
// ended the session (#397).
func interruptedRawModeTerminal(t *testing.T) (*ProcessTerminal, *bytes.Buffer) {
	t.Helper()
	stdin, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
	var out bytes.Buffer
	savedState := cookedTerminalState.Load()
	cookedTerminalState.Store(nil)
	resetKeyboardProtocolState(false, false, false)
	t.Cleanup(func() {
		cookedTerminalState.Store(savedState)
		resetKeyboardProtocolState(false, false, false)
	})
	return NewProcessTerminalWithOutput(stdin, nil, &out), &out
}

func TestEnterRawModeRetriesAnInterruptedTermiosIoctl(t *testing.T) {
	terminal, _ := interruptedRawModeTerminal(t)
	state := &term.State{}
	var rawCalls, restoreCalls int
	savedRaw, savedRestore := makeRawTerminal, restoreTerminal
	t.Cleanup(func() { makeRawTerminal, restoreTerminal = savedRaw, savedRestore })
	makeRawTerminal = func(int) (*term.State, error) {
		rawCalls++
		if rawCalls <= 2 {
			return nil, &os.SyscallError{Syscall: "ioctl", Err: syscall.EINTR}
		}
		return state, nil
	}
	restoreTerminal = func(_ int, got *term.State) error {
		restoreCalls++
		if got != state {
			t.Errorf("restored %p, want the state raw mode captured %p", got, state)
		}
		if restoreCalls <= 2 {
			return &os.SyscallError{Syscall: "ioctl", Err: syscall.EINTR}
		}
		return nil
	}

	restore, err := terminal.enterRawMode(false)
	if err != nil {
		t.Fatalf("raw mode failed after the ioctl was interrupted: %v (libuv retries EINTR)", err)
	}
	if rawCalls != 3 {
		t.Fatalf("raw-mode ioctl ran %d times, want 3 (two interrupted, one applied)", rawCalls)
	}
	restore()
	if restoreCalls != 3 {
		t.Fatalf("cooked-mode ioctl ran %d times, want 3 (two interrupted, one applied)", restoreCalls)
	}
}

func TestEnterRawModeStillReturnsAnIoctlErrorThatIsNotEINTR(t *testing.T) {
	terminal, _ := interruptedRawModeTerminal(t)
	calls := 0
	saved := makeRawTerminal
	t.Cleanup(func() { makeRawTerminal = saved })
	makeRawTerminal = func(int) (*term.State, error) {
		calls++
		return nil, &os.SyscallError{Syscall: "ioctl", Err: syscall.EIO}
	}
	_, err := terminal.enterRawMode(false)
	if !errors.Is(err, syscall.EIO) || calls != 1 {
		t.Fatalf("err=%v after %d calls, want EIO from the first", err, calls)
	}
}

func TestRestoreTerminalFromSignalRetriesAnInterruptedIoctl(t *testing.T) {
	var out bytes.Buffer
	savedTerminal := processTerminal
	processTerminal = NewProcessTerminalWithOutput(nil, nil, &out)
	t.Cleanup(func() { processTerminal = savedTerminal })
	savedState := cookedTerminalState.Load()
	cookedTerminalState.Store(&cookedTerminal{fd: 0, state: &term.State{}})
	t.Cleanup(func() { cookedTerminalState.Store(savedState) })
	resetKeyboardProtocolState(false, false, false)
	calls := 0
	savedRestore := restoreTerminal
	t.Cleanup(func() { restoreTerminal = savedRestore })
	restoreTerminal = func(int, *term.State) error {
		calls++
		if calls == 1 {
			return &os.SyscallError{Syscall: "ioctl", Err: syscall.EINTR}
		}
		return nil
	}
	if !RestoreTerminalFromSignal() || calls != 2 {
		t.Fatalf("restore reported failure or ran %d times, want one retry after EINTR", calls)
	}
}
