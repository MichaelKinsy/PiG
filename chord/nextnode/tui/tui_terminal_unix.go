//go:build unix

package tui

import (
	"os"
	"os/signal"
	"syscall"
)

// notifyResize delivers the terminal's resizes (SIGWINCH) to signals.
func notifyResize(signals chan<- os.Signal) { signal.Notify(signals, syscall.SIGWINCH) }

// ignoreInspectorSignal makes SIGUSR1 do nothing for the launcher's lifetime, as Pi's launcher does (Node opens its inspector on it
// unless the signal has a listener: a sandbox may signal its launcher, and an inspector would give any local process the launcher's
// token and files: Opus E8). Go has no inspector, and takes no action on SIGUSR1 anyway, so ignoring it is not observable here (the
// test of Pi's SIGUSR1 leg passes without it); it is kept so that the launcher's disposition of the signal is Pi's, whatever a
// program that embeds it does with signals. The returned function restores the default.
func ignoreInspectorSignal() func() {
	signal.Ignore(syscall.SIGUSR1)
	return func() { signal.Reset(syscall.SIGUSR1) }
}
