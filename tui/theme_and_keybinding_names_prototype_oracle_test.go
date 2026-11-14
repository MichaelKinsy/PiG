package tui

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// D645, the theme name row. Pi's loadThemeJson tests `name in builtinThemes` on a plain object and returns `builtinThemes[name]`
// (packages/coding-agent/src/modes/interactive/theme/theme.ts:489-490), so a theme named like a member of Object.prototype is taken for a built-in theme
// and setTheme fails with `Cannot convert undefined or null to object`. PiG's built-in themes are own entries only: such a name is a theme that does
// not exist, with the message every unknown name gets. `nosuchname` is the ordinary name and must be equal on both sides.
func TestAThemeNamedLikeAnObjectPrototypeMemberIsNotFound(t *testing.T) {
	const thrown = "Cannot convert undefined or null to object"
	cases := []struct{ name, pi string }{{"constructor", thrown}, {"toString", thrown}, {"__proto__", thrown}, {"nosuchname", ""}}
	names := make([]string, 0, len(cases))
	for _, c := range cases {
		names = append(names, c.name)
	}
	var pi []string
	pioracle.Run(t, `
const { setTheme } = await load("pi-coding-agent/modes/interactive/theme/theme.js");
emit(input.map((name) => { const result = setTheme(name); return result.success ? "loaded" : String(result.error); }));`, names, &pi)
	if len(pi) != len(cases) {
		t.Fatalf("Pi answered %d of %d probes", len(pi), len(cases))
	}
	if previous := activeTheme.Load(); previous != nil {
		t.Cleanup(func() { activeTheme.Store(previous) })
	}
	for i, c := range cases {
		pig := "loaded"
		if err := SetThemeByNameChecked(c.name); err != nil {
			pig = err.Error()
		}
		t.Logf("setTheme(%q): Pi %s, PiG %s", c.name, pi[i], pig)
		want := "Theme not found: " + c.name
		wantPi := c.pi
		if wantPi == "" {
			wantPi = want
		}
		if pi[i] != wantPi {
			t.Errorf("setTheme(%q): Pi answers %s, the record says %s", c.name, pi[i], wantPi)
		}
		if pig != want {
			t.Errorf("SetThemeByNameChecked(%q) = %s, want %s", c.name, pig, want)
		}
	}
}

// D645, the user keybinding id row. Pi's KeybindingsManager.rebuild skips a user binding whose id the definitions do not hold with
// `keybinding in this.definitions` on a plain object (packages/tui/src/keybindings.ts:249), so an id named like a member of Object.prototype passes and
// claims its keys: a conflict is reported between that id and the real keybinding that uses the same key. PiG's definitions are own entries only: the
// id is unknown and claims nothing. `nosuchname` is the ordinary name and must be equal on both sides.
func TestAUserKeybindingNamedLikeAnObjectPrototypeMemberClaimsNoKey(t *testing.T) {
	names := []string{"constructor", "toString", "__proto__", "nosuchname"}
	var pi []string
	pioracle.Run(t, `
const { KeybindingsManager } = await load("pi-tui/index.js");
emit(input.map((name) => {
	const user = JSON.parse('{' + JSON.stringify(name) + ':"ctrl+a","tui.editor.cursorLeft":"ctrl+a"}');
	const manager = new KeybindingsManager({ "tui.editor.cursorLeft": { defaultKeys: "left", description: "x" } }, user);
	return JSON.stringify(manager.getConflicts());
}));`, names, &pi)
	if len(pi) != len(names) {
		t.Fatalf("Pi answered %d of %d probes", len(pi), len(names))
	}
	for i, name := range names {
		manager := NewKeybindingsManager(KeybindingDefinitions{"tui.editor.cursorLeft": {DefaultKeys: []string{"left"}, Description: "x"}},
			KeybindingsConfig{name: {"ctrl+a"}, "tui.editor.cursorLeft": {"ctrl+a"}})
		conflicts := manager.GetConflicts()
		t.Logf("user bindings {%q: ctrl+a, tui.editor.cursorLeft: ctrl+a}: Pi conflicts %s, PiG %v", name, pi[i], conflicts)
		wantPi := "[]"
		if name != "nosuchname" {
			encoded, err := json.Marshal([]map[string]any{{"key": "ctrl+a", "keybindings": []string{name, "tui.editor.cursorLeft"}}})
			if err != nil {
				t.Fatal(err)
			}
			wantPi = string(encoded)
		}
		if pi[i] != wantPi {
			t.Errorf("user binding %q: Pi reports %s, the record says %s", name, pi[i], wantPi)
		}
		if len(conflicts) != 0 {
			t.Errorf("user binding %q: PiG reports the conflicts %v, want none", name, conflicts)
		}
	}
}
