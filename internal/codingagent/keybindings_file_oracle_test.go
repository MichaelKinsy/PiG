package codingagent

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// keybindingsFileCases are keybindings.json contents run through Pi's KeybindingsManager.create (keybindings.ts:379-397, loadRawConfig,
// migrateKeybindingsConfig, toKeybindingsConfig).
var keybindingsFileCases = map[string]string{
	"missing-object":      `{}`,
	"blank":               ``,
	"invalid-json":        `{ nope`,
	"null":                `null`,
	"array-root":          `["ctrl+a"]`,
	"number-root":         `42`,
	"string-root":         `"ctrl+a"`,
	"bom":                 "\ufeff" + `{"app.interrupt":"ctrl+x"}`,
	"string-binding":      `{"app.interrupt":"ctrl+x"}`,
	"array-binding":       `{"app.interrupt":["ctrl+x","alt+x"]}`,
	"empty-array":         `{"app.interrupt":[]}`,
	"mixed-array":         `{"app.interrupt":["ctrl+x",1]}`,
	"number-binding":      `{"app.interrupt":1}`,
	"null-binding":        `{"app.interrupt":null}`,
	"object-binding":      `{"app.interrupt":{"a":1}}`,
	"bool-binding":        `{"app.interrupt":true}`,
	"legacy-name":         `{"interrupt":"ctrl+x","selectConfirm":["enter","ctrl+j"]}`,
	"legacy-and-current":  `{"interrupt":"ctrl+x","app.interrupt":"ctrl+y"}`,
	"current-then-legacy": `{"app.interrupt":"ctrl+y","interrupt":"ctrl+x"}`,
	"unknown-action":      `{"x.unknown":"ctrl+x","app.interrupt":"ctrl+z"}`,
	"duplicate-keys":      `{"app.interrupt":"ctrl+x","app.interrupt":"ctrl+z"}`,
	"empty-string":        `{"app.interrupt":""}`,
	"uppercase":           `{"app.interrupt":"CTRL+X"}`,
	"spaced":              `{"app.interrupt":" ctrl+x "}`,
	"duplicate-entries":   `{"app.interrupt":["ctrl+x","ctrl+x","alt+x"]}`,
	"empty-entry":         `{"app.interrupt":["","ctrl+x"]}`,
	"number-key-object":   `{"0":"ctrl+a"}`,
	"all-legacy":          `{"cursorUp":"up","cursorDown":"down","selectCancel":"escape","expandTools":"ctrl+o","toggleThinking":"ctrl+t"}`,
}

func TestKeybindingsFileLoadingMatchesPi(t *testing.T) {
	root := t.TempDir()
	paths := map[string]string{}
	for name, content := range keybindingsFileCases {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		paths[name] = filepath.Join(dir, "keybindings.json")
		if err := os.WriteFile(paths[name], []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := runPiJSONFileOracle(t, paths, `const {KeybindingsManager} = await load('core/keybindings');
const {dirname} = await import('node:path');
for (const [name, path] of Object.entries(files)) {
  const kb = KeybindingsManager.create(dirname(path));
  const norm = c => Object.fromEntries(Object.entries(c).map(([k, v]) => [k, Array.isArray(v) ? v : [v]]));
  out[name] = JSON.stringify({user: norm(kb.getUserBindings()), effective: norm(kb.getEffectiveConfig()), keys: Object.fromEntries(['app.interrupt', 'tui.select.confirm', 'app.clear'].map(a => [a, kb.getKeys(a)]))});
}`)
	for name, path := range paths {
		t.Run(name, func(t *testing.T) {
			kb := NewKeybindingsManager(filepath.Dir(path))
			var expected struct {
				User      map[string][]string `json:"user"`
				Effective map[string][]string `json:"effective"`
				Keys      map[string][]string `json:"keys"`
			}
			if err := json.Unmarshal([]byte(want[name]), &expected); err != nil {
				t.Fatal(err)
			}
			user := map[string][]string{}
			maps.Copy(user, kb.GetUserBindings())
			if !reflect.DeepEqual(user, expected.User) {
				t.Errorf("user bindings = %v, want %v", user, expected.User)
			}
			for action, wantKeys := range expected.Keys {
				got := []string{}
				for _, key := range kb.GetKeys(action) {
					got = append(got, string(key))
				}
				if !reflect.DeepEqual(got, wantKeys) {
					t.Errorf("GetKeys(%s) = %v, want %v", action, got, wantKeys)
				}
			}
			effective := map[string][]string{}
			for action, keys := range kb.GetEffectiveConfig() {
				if keys.Keys != nil {
					effective[action] = keys.Keys
				} else {
					effective[action] = []string{keys.Key}
				}
			}
			if !reflect.DeepEqual(effective, expected.Effective) {
				t.Errorf("effective config differs from Pi")
			}
		})
	}
}
