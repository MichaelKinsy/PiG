package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// keybindings.ts getDefinition, getUserBindings and getEffectiveConfig read the merged table: the definition's default keys, a copy of the user's overrides, and each action's resolved key (a lone key as a string, several as a list).
// Pi: packages/coding-agent/src/core/keybindings.ts:390 (KeybindingsManager.getEffectiveConfig); packages/tui/src/keybindings.ts:282 (KeybindingsManager.getDefinition); packages/tui/src/keybindings.ts:295 (KeybindingsManager.getUserBindings).
func TestKeybindingsManagerDefinitionUserBindingsAndEffectiveConfig(t *testing.T) {
	restoreTUIKeybindings(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keybindings.json"), []byte(`{"app.tools.expand":"ctrl+g","app.model.select":["ctrl+p","ctrl+l"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	km := NewKeybindingsManager(dir)

	definition, ok := km.GetDefinition("app.tools.expand")
	if !ok || len(definition.DefaultKeys) == 0 || definition.Description == "" {
		t.Fatalf("GetDefinition(app.tools.expand) = %+v, %v", definition, ok)
	}
	if slices.Contains(definition.DefaultKeys, "ctrl+g") {
		t.Fatalf("definition reports the user's override %v as a default", definition.DefaultKeys)
	}
	if _, ok := km.GetDefinition("app.nonexistent"); ok {
		t.Fatal("GetDefinition found an action that is not in the table")
	}

	user := km.GetUserBindings()
	if got := user["app.model.select"]; !slices.Equal(got, []KeyID{"ctrl+p", "ctrl+l"}) {
		t.Fatalf("GetUserBindings()[app.model.select] = %v", got)
	}
	user["app.model.select"][0] = "mutated"
	delete(user, "app.tools.expand")
	if again := km.GetUserBindings(); again["app.model.select"][0] != "ctrl+p" || len(again["app.tools.expand"]) == 0 {
		t.Fatalf("GetUserBindings returned shared storage: %v", again)
	}

	config := km.GetEffectiveConfig()
	if got := config["app.tools.expand"]; got.Key != "ctrl+g" || got.Keys != nil {
		t.Fatalf("GetEffectiveConfig()[app.tools.expand] = %#v, want the single key as a string", got)
	}
	if got := config["app.model.select"].Keys; !slices.Equal(got, []string{"ctrl+p", "ctrl+l"}) {
		t.Fatalf("GetEffectiveConfig()[app.model.select] = %#v, want the key list", config["app.model.select"])
	}
	if _, ok := config["tui.select.up"]; !ok {
		t.Fatal("GetEffectiveConfig omits the tui.* actions of the merged table")
	}
}

// keybindings.ts: the coding-agent manager's table is KEYBINDINGS, the tui.* and app.* definitions together, so getDefinition answers a tui.* action too; getConflicts copies each conflict's keybinding list.
// Pi: packages/tui/src/keybindings.ts:282 (KeybindingsManager.getDefinition); packages/tui/src/keybindings.ts:286 (KeybindingsManager.getConflicts).
func TestKeybindingsManagerDefinitionCoversTUIActionsAndConflictsAreCopies(t *testing.T) {
	restoreTUIKeybindings(t)
	km := NewKeybindingsManagerFromBindings(nil, "")
	definition, ok := km.GetDefinition("tui.editor.cursorUp")
	if !ok || !slices.Contains(definition.DefaultKeys, "up") || definition.Description == "" {
		t.Fatalf("GetDefinition(tui.editor.cursorUp) = %+v, %v", definition, ok)
	}
	definition.DefaultKeys[0] = "mutated"
	if again, _ := km.GetDefinition("tui.editor.cursorUp"); slices.Contains(again.DefaultKeys, "mutated") {
		t.Fatal("GetDefinition returned the table's own key list")
	}

	km.SetUserBindings(map[string][]KeyID{"app.tools.expand": {"ctrl+g"}, "app.model.select": {"ctrl+g"}})
	conflicts := km.GetConflicts()
	if len(conflicts) != 1 || conflicts[0].Key != "ctrl+g" || len(conflicts[0].Keybindings) != 2 {
		t.Fatalf("GetConflicts() = %+v", conflicts)
	}
	conflicts[0].Keybindings[0] = "mutated"
	if again := km.GetConflicts(); slices.Contains(again[0].Keybindings, "mutated") {
		t.Fatal("GetConflicts returned the manager's own action list")
	}
}

// tui keybindings.ts KeybindingsConfig is `Record<string, KeyId | KeyId[] | undefined>`: a value reads from and writes to the JSON string or array it
// was given, so a config file round-trips as the user wrote it.
func TestKeybindingValueRoundTripsItsJSONKind(t *testing.T) {
	for _, raw := range []string{`"ctrl+x"`, `["ctrl+x","ctrl+y"]`, `[]`} {
		var value KeybindingValue
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		out, err := json.Marshal(value)
		if err != nil || string(out) != raw {
			t.Errorf("%s round-trips as %s (%v)", raw, out, err)
		}
	}
	var value KeybindingValue
	if err := json.Unmarshal([]byte(`7`), &value); err == nil {
		t.Error("a number is not a keybinding")
	}
}
