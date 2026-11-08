//go:build unix

package tui

import (
	"os"
	"os/signal"
	"syscall"
)

// notifyResize delivers the terminal's resizes (SIGWINCH) to signals.
func notifyResize(signals chan<- os.Signal) { signal.Notify(signals, syscall.SIGWINCH) }

// ignoreInspectorSignal makes SIGUSR1 do nothing: Node opens its inspector on it unless the signal has a listener (a sandbox may
// signal its launcher, and an inspector would give any local process the launcher's token and files: Opus E8). The returned
// function restores the signal. (Go's SIGUSR1 default is to terminate the process: ignored here for the launcher's lifetime.)
func ignoreInspectorSignal() func() {
	signal.Ignore(syscall.SIGUSR1)
	return func() { signal.Reset(syscall.SIGUSR1) }
}
