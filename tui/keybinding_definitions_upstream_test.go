package tui

import (
	"slices"
	"testing"
)

// upstream: packages/tui/src/keybindings.ts:68 KeybindingDefinitions, :237-262 KeybindingsManager constructor/rebuild: keys come from the definitions table and a user binding replaces its id's default.
func TestKeybindingDefinitionsDriveManager(t *testing.T) {
	definitions := KeybindingDefinitions{
		"x.alpha": {DefaultKeys: []string{"ctrl+a"}, Description: "alpha"},
		"x.beta":  {DefaultKeys: []string{"ctrl+b", "ctrl+c"}},
	}
	m := NewKeybindingsManager(definitions, KeybindingsConfig{"x.alpha": {"ctrl+z"}})
	if got := m.GetKeys("x.alpha"); !slices.Equal(got, []string{"ctrl+z"}) {
		t.Fatalf("overridden keys = %v, want [ctrl+z]", got)
	}
	if got := m.GetKeys("x.beta"); !slices.Equal(got, []string{"ctrl+b", "ctrl+c"}) {
		t.Fatalf("default keys = %v", got)
	}
	if def, ok := m.GetDefinition("x.alpha"); !ok || def.Description != "alpha" {
		t.Fatalf("definition = %+v ok=%v", def, ok)
	}
}
