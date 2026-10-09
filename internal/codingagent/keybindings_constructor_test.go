package codingagent

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// `new KeybindingsManager(userBindings, configPath)` (keybindings.ts:374; interactive-tui.test.ts:85 passes a user binding the same way):
// the given user bindings replace the defaults of their action, and without a configPath Reload keeps them.
func TestNewKeybindingsManagerFromBindingsAppliesUserBindingsAndKeepsThemWithoutAFile(t *testing.T) {
	km := NewKeybindingsManagerFromBindings(map[string][]KeyID{"app.model.cycleForward": {"ctrl+j"}}, "")
	if got := km.GetKeys("app.model.cycleForward"); !slices.Equal(got, []KeyID{"ctrl+j"}) {
		t.Fatalf("app.model.cycleForward keys = %v, want [ctrl+j]", got)
	}
	km.Reload()
	if got := km.GetKeys("app.model.cycleForward"); !slices.Equal(got, []KeyID{"ctrl+j"}) {
		t.Fatalf("Reload without a config file dropped the user bindings: %v", got)
	}
}

// With a configPath Reload reads that file (reload(), keybindings.ts:384), replacing the constructor's bindings.
func TestNewKeybindingsManagerFromBindingsReloadsItsConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keybindings.json")
	if err := os.WriteFile(path, []byte(`{"app.model.cycleForward":"ctrl+k"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	km := NewKeybindingsManagerFromBindings(map[string][]KeyID{"app.model.cycleForward": {"ctrl+j"}}, path)
	if got := km.GetKeys("app.model.cycleForward"); !slices.Equal(got, []KeyID{"ctrl+j"}) {
		t.Fatalf("constructor keys = %v, want the given [ctrl+j] before any reload", got)
	}
	km.Reload()
	if got := km.GetKeys("app.model.cycleForward"); !slices.Equal(got, []KeyID{"ctrl+k"}) {
		t.Fatalf("keys after Reload = %v, want the file's [ctrl+k]", got)
	}
}
