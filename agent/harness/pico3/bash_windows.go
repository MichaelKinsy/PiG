//go:build windows

package pico3

import "os"

// signalName reports no signal: Windows processes exit with codes.
func signalName(*os.ProcessState) string { return "" }
