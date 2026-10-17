package codingagent

import (
	"encoding/json"
	"fmt"
	"maps"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// pi: packages/coding-agent/src/core/keybindings.ts KeybindingsManager.create against pinned Pi over random keybindings.json files: current, legacy, unknown and integer-like actions, string, array, mixed and non-string bindings, duplicate members, byte order marks and trailing garbage.
func TestRandomKeybindingsFilesMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(20261010))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	actions := []string{"app.interrupt", "app.clear", "tui.select.confirm", "tui.select.up", "tui.editor.cursorLeft", "interrupt", "clear", "selectConfirm", "cursorUp", "expandTools", "x.unknown", "0", "10", "2", "app.interrupt ", "APP.INTERRUPT", ""}
	keys := []string{"ctrl+x", "alt+x", "enter", "escape", "ctrl+shift+a", "CTRL+X", " ctrl+x ", "ctrl+", "", "up", "shift+tab", "ctrl+alt+delete", "f13", "super+k", "a", "ctrl+a ctrl+b", "é", "😀"}
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	value := func() string {
		switch rng.Intn(8) {
		case 0:
			return pick("1", "true", "null", `{"a":1}`, "[]", `[1]`, `["ctrl+x",1]`, `[null]`, "1.5")
		case 1, 2:
			var items []string
			for range rng.Intn(4) {
				items = append(items, quote(keys[rng.Intn(len(keys))]))
			}
			return "[" + strings.Join(items, ",") + "]"
		default:
			return quote(keys[rng.Intn(len(keys))])
		}
	}
	root := t.TempDir()
	paths := map[string]string{}
	for i := range 800 {
		var b strings.Builder
		if rng.Intn(10) == 0 {
			b.WriteString("\ufeff")
		}
		b.WriteString("{")
		for j := range rng.Intn(6) {
			if j > 0 {
				b.WriteString(",")
			}
			b.WriteString(quote(actions[rng.Intn(len(actions))]) + ":" + value())
		}
		b.WriteString("}")
		switch rng.Intn(20) {
		case 0:
			b.WriteString(" x")
		case 1:
			b.Reset()
			b.WriteString(pick("[]", "null", "5", `"s"`, "", "{"))
		}
		name := fmt.Sprintf("k%03d", i)
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		paths[name] = filepath.Join(dir, "keybindings.json")
		if err := os.WriteFile(paths[name], []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	probe := []string{"app.interrupt", "app.clear", "tui.select.confirm", "tui.select.up", "tui.editor.cursorLeft"}
	probeJSON, _ := json.Marshal(probe)
	want := runPiJSONFileOracle(t, paths, `const {KeybindingsManager} = await load('core/keybindings');
const {dirname} = await import('node:path');
const probe = `+string(probeJSON)+`;
for (const [name, path] of Object.entries(files)) {
  const kb = KeybindingsManager.create(dirname(path));
  const norm = c => Object.fromEntries(Object.entries(c).map(([k, v]) => [k, Array.isArray(v) ? v : [v]]));
  out[name] = JSON.stringify({user: norm(kb.getUserBindings()), effective: norm(kb.getEffectiveConfig()), keys: Object.fromEntries(probe.map(a => [a, kb.getKeys(a)]))});
}`)
	failures := 0
	for name, path := range paths {
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
		effective := map[string][]string{}
		for action, keys := range kb.GetEffectiveConfig() {
			if keys.Keys != nil {
				effective[action] = keys.Keys
			} else {
				effective[action] = []string{keys.Key}
			}
		}
		gotKeys := map[string][]string{}
		for _, action := range probe {
			got := []string{}
			for _, key := range kb.GetKeys(action) {
				got = append(got, string(key))
			}
			gotKeys[action] = got
		}
		if !reflect.DeepEqual(user, expected.User) || !reflect.DeepEqual(effective, expected.Effective) || !reflect.DeepEqual(gotKeys, expected.Keys) {
			if failures++; failures <= 6 {
				content, _ := os.ReadFile(path)
				t.Errorf("%s %q:\n  user    Pig %v Pi %v\n  keys    Pig %v Pi %v\n  effective differs: %v", name, content, user, expected.User, gotKeys, expected.Keys, !reflect.DeepEqual(effective, expected.Effective))
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d files differ from Pi", failures)
	}
}
