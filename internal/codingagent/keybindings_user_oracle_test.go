package codingagent

import (
	"bytes"
	"encoding/json"
	"math/rand"
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
	// Random user configs: key ids composed of modifiers, keys, letter case, padding and separators, over real app.* and tui.* ids and an unknown one,
	// so claims collide (conflicts), repeat (duplicates) and miss (unknown or padded ids).
	rng := rand.New(rand.NewSource(20261012))
	pick := func(list []string) string { return list[rng.Intn(len(list))] }
	randomActions := []string{"app.model.select", "app.tools.expand", "app.thinking.cycle", "app.interrupt", "app.exit", "app.clear", "app.session.new", "app.message.followUp",
		"tui.select.confirm", "tui.select.cancel", "tui.editor.cursorLeft", "tui.input.submit", "app.unknown.action"}
	mods := []string{"", "", "ctrl+", "alt+", "shift+", "ctrl+shift+", "ctrl+alt+", "CTRL+", "Alt+", "Shift+", "ctrl+Alt+", "super+", "ctrl+super+", "meta+", "ctrl + ", " ctrl+", "ctrl+ ", "ctrl++"}
	base := []string{"a", "x", "l", "L", "y", "enter", "Enter", "ENTER", "escape", "esc", "ESCAPE", "tab", "Tab", "space", "backspace", "delete", "up", "Down", "left", "pageUp", "home", "end",
		"f1", "F5", "f12", "1", "9", "-", "=", "[", "]", ";", "`", "/", "?", "!", "\u00e9", "unknown", "", "ctrl", "+"}
	randomKey := func() string { return pick(mods) + pick(base) }
	for range 140 {
		config := map[string]any{}
		for range 1 + rng.Intn(4) {
			action := pick(randomActions)
			switch rng.Intn(5) {
			case 0:
				config[action] = []string{}
			case 1, 2:
				config[action] = randomKey()
			default:
				keys := []string{}
				for range 1 + rng.Intn(3) {
					keys = append(keys, randomKey())
				}
				config[action] = keys
			}
		}
		configs = append(configs, config)
	}
	actions = append(actions, randomActions...)
	inputs = append(inputs, "a", "L", "\x01", "\x1ba", "\x1b[1;5A", "\x1b[1;3B", "\x1b[13;2u", "\x1b[27u", "\x1b[108;5u", "\x1b[Z", "\t", "\x7f", "\x1b[3~", "\x1b[5~", "\x1bOP", "\x1b[15~", "\x1b[1;2P", "\x1b[99;9u", "\x00", "\u00e9")
	payload, err := json.Marshal(map[string]any{"configs": configs, "actions": actions, "inputs": inputs})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/keybindings_user.mjs", pigversion.UpstreamPackageVersion)
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
		Resolved  map[string]any      `json:"resolved"`
		Defs      map[string]struct {
			Description string `json:"description"`
			DefaultKeys any    `json:"defaultKeys"`
		} `json:"definitions"`
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
				user[action] = []KeyID{}
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
		// getResolvedBindings lists every keybinding the merged table defines (tui.* and app.*), a single key as a string and several as an array; getDefinition returns each one's description and default keys.
		resolved := km.GetResolvedBindings()
		if len(resolved) != len(want[i].Resolved) {
			t.Errorf("config %v: getResolvedBindings lists %d keybindings, Pi %d", config, len(resolved), len(want[i].Resolved))
		}
		for id, piValue := range want[i].Resolved {
			keys, ok := resolved[id]
			if !ok {
				t.Errorf("config %v: getResolvedBindings has no %s", config, id)
				continue
			}
			var got any = slices.Clone(keys)
			if len(keys) == 1 {
				got = keys[0]
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(piValue)
			if !bytes.Equal(gotJSON, wantJSON) {
				t.Errorf("config %v: getResolvedBindings[%s] = %s, Pi %s", config, id, gotJSON, wantJSON)
			}
			definition, found := km.GetDefinition(id)
			defaults := make([]string, 0, len(definition.DefaultKeys))
			for _, key := range definition.DefaultKeys {
				defaults = append(defaults, string(key))
			}
			var gotDefaults any = defaults
			if len(defaults) == 1 {
				gotDefaults = defaults[0]
			}
			gotDef, _ := json.Marshal(map[string]any{"description": definition.Description, "defaultKeys": gotDefaults})
			wantDef, _ := json.Marshal(map[string]any{"description": want[i].Defs[id].Description, "defaultKeys": want[i].Defs[id].DefaultKeys})
			if !found || !bytes.Equal(gotDef, wantDef) {
				t.Errorf("config %v: getDefinition(%s) = %s (found %v), Pi %s", config, id, gotDef, found, wantDef)
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
