package codingagent

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// builtExtensionKeybindingTable is ExtensionKeybindingTable as it was when it built the definitions on every call: the reference the shared definitions must match.
func builtExtensionKeybindingTable(km *KeybindingsManager) map[string]any {
	definitions := map[string]any{}
	for id, def := range keybindingDefinitionsFor(km.platform) {
		keys := def.DefaultKeys
		if keys == nil {
			keys = []string{}
		}
		definitions[id] = map[string]any{"defaultKeys": keys, "description": def.Description}
	}
	userBindings := make(map[string][]KeyID, len(km.userBindings))
	for action, keys := range km.userBindings {
		userBindings[action] = slices.Clone(keys)
	}
	return map[string]any{"definitions": definitions, "userBindings": userBindings}
}

var extensionKeybindingTableSink map[string]any

// Every platform's table equals and encodes like the one built per call. Tables of one platform share their definitions, and each keeps its own copy of the user's bindings.
func TestExtensionKeybindingTableSharesItsPlatformDefinitions(t *testing.T) {
	for _, platform := range tui.KeybindingPlatforms {
		km := NewKeybindingsManagerFromBindings(map[string][]KeyID{"app.clear": {"ctrl+x"}, "tui.editor.cursorUp": {}}, "")
		km.platform = platform
		got, want := km.ExtensionKeybindingTable(), builtExtensionKeybindingTable(km)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: table = %v, want %v", platform, got, want)
		}
		gotJSON, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		wantJSON, _ := json.Marshal(want)
		if !bytes.Equal(gotJSON, wantJSON) {
			t.Fatalf("%s: encoded table differs:\n%s\nwant\n%s", platform, gotJSON, wantJSON)
		}
		other := NewKeybindingsManagerFromBindings(nil, "")
		other.platform = platform
		again := other.ExtensionKeybindingTable()
		if reflect.ValueOf(again["definitions"]).UnsafePointer() != reflect.ValueOf(got["definitions"]).UnsafePointer() {
			t.Fatalf("%s: two tables built their own definitions", platform)
		}
		got["userBindings"].(map[string][]KeyID)["app.clear"][0] = "changed"
		if km.userBindings["app.clear"][0] != "ctrl+x" {
			t.Fatalf("%s: writing a table's user binding changed the manager's", platform)
		}
		if len(again["userBindings"].(map[string][]KeyID)) != 0 {
			t.Fatalf("%s: a manager without user bindings has %v", platform, again["userBindings"])
		}
	}
	km := NewKeybindingsManagerFromBindings(nil, "")
	km.ExtensionKeybindingTable()
	allocs := testing.AllocsPerRun(20, func() { extensionKeybindingTableSink = km.ExtensionKeybindingTable() })
	built := testing.AllocsPerRun(20, func() { extensionKeybindingTableSink = builtExtensionKeybindingTable(km) })
	if allocs*10 > built {
		t.Fatalf("ExtensionKeybindingTable = %v allocations, building the definitions takes %v; want under a tenth", allocs, built)
	}
}
