package codingagent

import (
	"strings"
	"testing"
)

func TestStartupKeybindHintsShowsCoreBindings(t *testing.T) {
	got := StartupKeybindHints(DefaultKeybindingsManager())
	for _, want := range []string{"interrupt", "clear/exit", "commands", "bash", "more"} {
		if !strings.Contains(got, want) {
			t.Fatalf("startup hints missing %q:\n%s", want, got)
		}
	}
	// The default interrupt/clear/exit/expand keys are surfaced.
	// Upstream keyHint uses non-capitalized keyText (keybinding-hints.ts).
	for _, key := range []string{"escape", "ctrl+c", "ctrl+d", "ctrl+o"} {
		if !strings.Contains(got, key) {
			t.Fatalf("startup hints missing key %q:\n%s", key, got)
		}
	}
}

func TestStartupKeybindHintsNilManagerUsesDefaults(t *testing.T) {
	if StartupKeybindHints(nil) != StartupKeybindHints(DefaultKeybindingsManager()) {
		t.Fatal("nil manager should fall back to defaults")
	}
}
