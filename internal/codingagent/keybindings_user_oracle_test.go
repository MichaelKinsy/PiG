package codingagent

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// Pi's KeybindingsManager keeps each user key id as written: tui keybindings.ts normalizeKeys only drops duplicates, so "CTRL+L" is shown
// as written by keyText, " ctrl+x" (a space) matches nothing, and "Ctrl+L" and "ctrl+l" are two different claims. matchesKey still reads
// letter case loosely. PiG lowercased, trimmed and dropped empty ids. rebuild counts a claim toward a conflict only for an id KEYBINDINGS
// defines, which includes the tui.* ids; PiG reported a conflict for an unknown id.
func TestUserKeybindingsMatchPi(t *testing.T) {
	configs := []map[string]any{
		{"app.model.select": "CTRL+L"},
		{"app.model.select": "Ctrl+L", "app.tools.expand": "ctrl+l"},
		{"app.model.select": []string{" ctrl+x", "ctrl+y"}},
		{"app.model.select": []string{"", "ctrl+y"}},
		{"app.model.select": []string{"ctrl+y", "CTRL+Y", "ctrl+y"}},
		{"app.model.select": "Alt+Enter", "app.thinking.cycle": "SHIFT+TAB"},
		{"app.tools.expand": "Enter", "app.model.select": "ESCAPE"},
		{"app.unknown.action": "ctrl+l", "app.model.select": "ctrl+l"},
		{"tui.select.confirm": "ctrl+y", "app.model.select": "ctrl+y"},
		{},
	}
	actions := []string{"app.model.select", "app.tools.expand", "app.thinking.cycle"}
	inputs := []string{"\x0c", "\x18", "\x19", "\x1b\r", "\x1b[Z", "\r", "\x1b", " "}
	payload, err := json.Marshal(map[string]any{"configs": configs, "actions": actions, "inputs": inputs})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/keybindings_user.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	type conflict struct {
		Key         string   `json:"key"`
		Keybindings []string `json:"keybindings"`
	}
	var want []struct {
		Keys      map[string][]string `json:"keys"`
		Text      map[string]string   `json:"text"`
		Matches   map[string][]bool   `json:"matches"`
		Conflicts []conflict          `json:"conflicts"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	for i, config := range configs {
		user := map[string][]KeyID{}
		for action, value := range config {
			switch v := value.(type) {
			case string:
				user[action] = []KeyID{KeyID(v)}
			case []string:
				for _, key := range v {
					user[action] = append(user[action], KeyID(key))
				}
			}
		}
		km := NewKeybindingsManagerFromBindings(user, "")
		for _, action := range actions {
			keys := []string{}
			for _, key := range km.GetKeys(action) {
				keys = append(keys, string(key))
			}
			if !reflect.DeepEqual(keys, nonNilStrings(want[i].Keys[action])) {
				t.Errorf("config %v: getKeys(%s) = %q, Pi %q", config, action, keys, want[i].Keys[action])
			}
			if got := km.KeyText(action); got != want[i].Text[action] {
				t.Errorf("config %v: keyText(%s) = %q, Pi %q", config, action, got, want[i].Text[action])
			}
			for j, data := range inputs {
				if got := km.Matches(data, action); got != want[i].Matches[action][j] {
					t.Errorf("config %v: matches(%q, %s) = %v, Pi %v", config, data, action, got, want[i].Matches[action][j])
				}
			}
		}
		conflicts := []conflict{}
		for _, c := range km.GetConflicts() {
			bindings := slices.Clone(c.Keybindings)
			slices.Sort(bindings)
			conflicts = append(conflicts, conflict{string(c.Key), bindings})
		}
		if !reflect.DeepEqual(conflicts, append([]conflict{}, want[i].Conflicts...)) {
			t.Errorf("config %v: conflicts = %v, Pi %v", config, conflicts, want[i].Conflicts)
		}
	}
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
