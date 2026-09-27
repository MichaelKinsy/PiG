//go:build parity && !windows

package runner

import "syscall"

// exitWithSignal re-raises sig under its default disposition, so the process
// dies of the signal and its parent sees 128+signum.
func exitWithSignal(sig syscall.Signal) {
	_ = syscall.Kill(syscall.Getpid(), sig)
}
