package tui

import (
	"sync/atomic"

	"golang.org/x/term"
)

// cookedTerminal is the terminal state captured before the first raw-mode
// entry, kept so a signal handler can hand the terminal back.
type cookedTerminal struct {
	fd    int
	state *term.State
}

var cookedTerminalState atomic.Pointer[cookedTerminal]

// rememberSignalRestore records the pre-raw terminal state the first time raw
// mode is entered. Later entries (resuming from suspend, returning from an
// external editor) re-enter raw mode from raw-adjacent states, so only the
// first capture describes the terminal the user started with.
func rememberSignalRestore(fd int, state *term.State) {
	cookedTerminalState.CompareAndSwap(nil, &cookedTerminal{fd: fd, state: state})
}

// RestoreTerminalFromSignal returns the terminal to the mode it had before pig
// took it over, and reports whether there was anything to restore.
//
// This is the subset of raw-mode teardown that is safe to run from a signal
// goroutine: it disables the extended-key protocols in a single write and then
// runs one tcsetattr against a state captured at startup. It deliberately
// skips the rest of the normal teardown, which drains stdin for up to a second,
// because that would race the input reader and the render loop.
//
// Without the tcsetattr, exiting on a signal leaves the terminal raw: ISIG
// stays off, so the user's shell has no working Ctrl+C until they run `reset`.
// Without the protocol disable, the Kitty flags pig pushed stay on the
// terminal's stack after pig is gone, so the shell that inherits the terminal
// receives CSI-u encoded keys it does not understand.
func RestoreTerminalFromSignal() bool {
	// pig divergence (D51): upstream leaves the terminal raw when a signal
	// terminates the session.
	restore := cookedTerminalState.Load()
	if restore == nil {
		return false
	}
	processTerminal.disableKeyboardProtocol()
	return term.Restore(restore.fd, restore.state) == nil
}
