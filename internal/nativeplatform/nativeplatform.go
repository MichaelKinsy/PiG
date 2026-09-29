// Package nativeplatform provides operating-system queries and native clipboard transfers. Pig builds with CGO_ENABLED=0; Go callers use platform APIs or the X11 wire protocol directly.
package nativeplatform

// IsModifierPressed mirrors the helper's isModifierPressed(name): it reports
// whether the named modifier ("shift", "command", "control", or "option") is
// held right now. It reports false when the platform has no helper, the system
// function cannot be loaded, or the name is unknown.
func IsModifierPressed(name string) bool {
	return isModifierPressed(name)
}
