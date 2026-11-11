package tui

import (
	"syscall"
	"testing"
	"time"

	"golang.org/x/term"
)

// Pi's terminalErrorHandler rethrows a write error that is not a dead terminal into uncaughtCrash, which restores the terminal (interactive-mode.ts:4505-4510,
// :4448). The handler runs on the writing goroutine with the output lock held, and the restore pops the Kitty flags under that same lock, so it must not wait
// for it. A bounded wait stands in for the hang.
func TestSignalRestoreFromAWriteErrorHandlerDoesNotWaitForTheOutputLock(t *testing.T) {
	savedTerminal, savedState, savedRestore := processTerminal, cookedTerminalState.Load(), restoreTerminal
	t.Cleanup(func() {
		processTerminal, restoreTerminal = savedTerminal, savedRestore
		cookedTerminalState.Store(savedState)
		resetKeyboardProtocolState(false, false, false)
	})
	processTerminal = NewProcessTerminalWithOutput(nil, nil, failingWriter{syscall.EINVAL})
	restoreTerminal = func(int, *term.State) error { return nil }
	cookedTerminalState.Store(&cookedTerminal{fd: -1})
	resetKeyboardProtocolState(true, false, true)

	restored := make(chan bool, 1)
	remove := OnTerminalWriteError(func(error) { restored <- RestoreTerminalFromSignal() })
	t.Cleanup(remove)

	written := make(chan struct{})
	go func() {
		defer close(written)
		processTerminal.writeUnlogged("\x1b[>7u\x1b[?u")
	}()
	t.Cleanup(func() { <-written })
	select {
	case ok := <-restored:
		if !ok {
			t.Error("RestoreTerminalFromSignal reported nothing restored")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RestoreTerminalFromSignal blocked on the output lock its own write holds")
	}
}
