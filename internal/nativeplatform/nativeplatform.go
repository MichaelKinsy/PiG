// Package nativeplatform provides the operating-system queries that upstream
// Pi loads from its per-platform N-API helpers (packages/tui/src/native-platform.ts).
// Pig builds with CGO_ENABLED=0 and loads no Node addons, so each query calls
// the same system function directly.
package nativeplatform

// IsModifierPressed mirrors the helper's isModifierPressed(name): it reports
// whether the named modifier ("shift", "command", "control", or "option") is
// held right now. It reports false when the platform has no helper, the system
// function cannot be loaded, or the name is unknown.
func IsModifierPressed(name string) bool {
	return isModifierPressed(name)
}
