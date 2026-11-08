package codingagent

import (
	"encoding/json"
	"maps"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
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
	// keybindings.ts:302 isLegacyKeybindingName is `key in KEYBINDING_NAME_MIGRATIONS`, which also finds the names Object.prototype inherits.
	"inherited-constructor": `{"constructor":"ctrl+x","app.interrupt":"ctrl+z"}`,
	"inherited-proto":       `{"__proto__":["ctrl+x"],"app.interrupt":"ctrl+z"}`,
	"inherited-tostring":    `{"toString":"ctrl+x","valueOf":["a"],"hasOwnProperty":""}`,
	"all-legacy":            `{"cursorUp":"up","cursorDown":"down","selectCancel":"escape","expandTools":"ctrl+o","toggleThinking":"ctrl+t"}`,
	"schema-url":            `{"$schema":"https://example.test/keybindings.schema.json","app.session.new":"ctrl+n"}`,
	"schema-non-string":     `{"$schema":42,"app.session.new":"ctrl+n"}`,
	"unknown-key-name":      `{"app.interrupt":"ctrl+not-a-key","app.clear":"ctrl+l"}`,
	"duplicate-modifier":    `{"app.interrupt":"ctrl+ctrl+x","app.clear":"alt+ctrl+alt+x"}`,
	"five-modifiers":        `{"app.interrupt":"ctrl+shift+alt+super+ctrl+x"}`,
	"four-modifiers":        `{"app.interrupt":"ctrl+shift+alt+super+f12"}`,
	"control-word":          `{"app.interrupt":"control+x"}`,
	"plus-and-symbols":      `{"app.interrupt":"+","app.clear":"alt+ctrl+?","tui.select.confirm":["9","pageUp","a"]}`,
	"array-one-invalid":     `{"app.interrupt":["ctrl+x","Alt+x"]}`,
	"meta-modifier":         `{"app.interrupt":"meta+x"}`,
	"multi-char-base":       `{"app.interrupt":"ab","app.clear":"ctrl+ab"}`,
}

// randomKeybindingsFileCases composes keybindings.json texts: members drawn from legacy names, current ids, unknown and numeric-looking names (JSON.parse
// orders integer-like keys first), repeated names, and values of every JSON shape, with a BOM or padding now and then.
func randomKeybindingsFileCases(rng *rand.Rand, n int) map[string]string {
	pick := func(list []string) string { return list[rng.Intn(len(list))] }
	names := []string{"interrupt", "selectConfirm", "cursorUp", "expandTools", "toggleThinking", "followUp", "tree", "app.interrupt", "app.clear", "app.exit", "app.tools.expand",
		"app.thinking.cycle", "tui.select.confirm", "tui.select.cancel", "tui.editor.cursorLeft", "tui.input.submit", "x.unknown", "0", "1", "10", "-1", "__proto__", "constructor", "toString", "valueOf", "hasOwnProperty", "isPrototypeOf", "propertyIsEnumerable", "toLocaleString", "__defineGetter__", "__lookupSetter__", "", " app.exit", "APP.EXIT"}
	scalars := []string{`"ctrl+x"`, `"CTRL+X"`, `" ctrl+x "`, `""`, `"enter"`, `"escape"`, `"alt+enter"`, `"shift+tab"`, `"ctrl+shift+p"`, `"a"`}
	value := func() string {
		switch rng.Intn(9) {
		case 0:
			return pick([]string{"null", "1", "true", "false", `{"a":1}`, "[]", `[[]]`, `[null]`, `[1]`, `["ctrl+x",1]`, "1.5e3"})
		case 1, 2, 3:
			var items []string
			for range 1 + rng.Intn(3) {
				items = append(items, pick(scalars))
			}
			return "[" + strings.Join(items, ",") + "]"
		default:
			return pick(scalars)
		}
	}
	out := map[string]string{}
	for i := range n {
		var members []string
		for range rng.Intn(5) {
			members = append(members, `"`+pick(names)+`":`+value())
		}
		text := "{" + strings.Join(members, ",") + "}"
		switch rng.Intn(12) {
		case 0:
			text = "\ufeff" + text
		case 1:
			text = " \n" + text + "\n"
		case 2:
			text = "[" + text + "]"
		}
		out["random-"+strconv.Itoa(i)] = text
	}
	return out
}

func TestKeybindingsFileLoadingMatchesPi(t *testing.T) {
	root := t.TempDir()
	paths := map[string]string{}
	cases := maps.Clone(keybindingsFileCases)
	maps.Copy(cases, randomKeybindingsFileCases(rand.New(rand.NewSource(20261012)), 150))
	for name, content := range cases {
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
