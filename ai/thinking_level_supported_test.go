package ai

import (
	"slices"
	"testing"
)

// TestThinkingLevelSupportedIsMembershipOfTheSupportedLevels pins the list-free membership test to the supported levels list for every model shape and
// every level, a level no model knows included.
func TestThinkingLevelSupportedIsMembershipOfTheSupportedLevels(t *testing.T) {
	for name, model := range thinkingLevelTestModels() {
		for _, level := range thinkingLevelTestLevels() {
			if got, want := thinkingLevelSupported(model, level), slices.Contains(GetSupportedThinkingLevels(model), level); got != want {
				t.Errorf("%s: thinkingLevelSupported(%q) = %v, want %v", name, level, got, want)
			}
		}
	}
}
