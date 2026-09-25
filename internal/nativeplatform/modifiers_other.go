//go:build !darwin && !windows

package nativeplatform

// isModifierPressed mirrors getNativePlatformHelper returning undefined on
// platforms other than macOS and Windows.
func isModifierPressed(string) bool { return false }
