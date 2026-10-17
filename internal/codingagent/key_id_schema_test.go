package codingagent

import "testing"

// packages/coding-agent/test/config-schemas.test.ts "validates keybinding syntax".
func TestIsKeyIDMatchesTheKeyIdSchema(t *testing.T) {
	for _, binding := range []string{"a", "9", "pageUp", "+", "ctrl+shift+x", "alt+ctrl+?", "ctrl+shift+alt+super+f12", "ctrl++", "super+ctrl+esc"} {
		if !isKeyID(binding) {
			t.Errorf("isKeyID(%q) = false, want true", binding)
		}
	}
	for _, binding := range []string{"", "Ctrl+x", "control+x", "ctrl+not-a-key", "ctrl+ctrl+x", "ctrl+shift+alt+super+ctrl+x", "meta+x", "ab", "ctrl+", "ctrl+shift+alt+super+alt+x", "A", "ctrl+X"} {
		if isKeyID(binding) {
			t.Errorf("isKeyID(%q) = true, want false", binding)
		}
	}
}
