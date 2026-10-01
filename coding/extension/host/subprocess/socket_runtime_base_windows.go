//go:build windows

package subprocess

// secureSocketRuntimeBase accepts the Windows per-user temp directory as is.
func secureSocketRuntimeBase(string) error { return nil }
