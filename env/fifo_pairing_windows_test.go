//go:build windows

package env

// redeliverFIFOEOF does nothing: Windows has no FIFOs, and the tests that use them skip there.
func redeliverFIFOEOF(string) {}
