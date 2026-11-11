//go:build !unix

package testenv

// GrabTerminalIfRequested does nothing where processes have no terminal process groups.
func GrabTerminalIfRequested() {}
