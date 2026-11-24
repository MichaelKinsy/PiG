//go:build !unix

package tui

// SetContinueSignalHandler does nothing where there is no SIGCONT (for example Windows, which has no SIGTSTP or SIGCONT job control).
func SetContinueSignalHandler(func(owned func() bool)) func() { return func() {} }

func continueOwnersChanged(*continueOwnership, bool) {}
