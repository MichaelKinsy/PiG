//go:build windows

package subprocess

// secureSocketRuntimeBase accepts a Windows per-user base, the temp directory or %LOCALAPPDATA%\pig\s, as is.
func secureSocketRuntimeBase(string) error { return nil }
