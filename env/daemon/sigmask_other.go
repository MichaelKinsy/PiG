//go:build !linux && !darwin && !windows

package daemon

// withEmptySignalMask runs start: only Linux and macOS give a program a signal mask of its own.
func withEmptySignalMask(start func() error) error { return start() }
