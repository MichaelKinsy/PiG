package codingagent

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// D645, the keybindings.json key row. Pi's isLegacyKeybindingName is `key in KEYBINDING_NAME_MIGRATIONS` on a plain object
// (packages/coding-agent/src/core/keybindings.ts:264), so a key named like a member of Object.prototype counts as a legacy name and is renamed to the
// text of the inherited member (:286), and the file is rewritten with that key. PiG's table holds own entries only: such a key is no legacy name and
// stays as written. Each case holds Pi's answer, which the pinned Pi must give in this run; the legacy name `interrupt` and the unknown name
// `nosuchname` are the ordinary names and must be equal on both sides.
func TestAKeybindingNamedLikeAnObjectPrototypeMemberIsNoLegacyName(t *testing.T) {
	cases := []struct{ key, pi string }{
		{"constructor", `{"config":{"function Object() { [native code] }":"ctrl+a"},"migrated":true}`},
		{"toString", `{"config":{"function toString() { [native code] }":"ctrl+a"},"migrated":true}`},
		{"valueOf", `{"config":{"function valueOf() { [native code] }":"ctrl+a"},"migrated":true}`},
		{"hasOwnProperty", `{"config":{"function hasOwnProperty() { [native code] }":"ctrl+a"},"migrated":true}`},
		{"__proto__", `{"config":{"[object Object]":"ctrl+a"},"migrated":true}`},
		{"interrupt", ""},
		{"nosuchname", ""},
	}
	texts := make([]string, 0, len(cases))
	for _, c := range cases {
		text, err := json.Marshal(map[string]string{c.key: "ctrl+a"})
		if err != nil {
			t.Fatal(err)
		}
		texts = append(texts, string(text))
	}
	var pi []string
	pioracle.Run(t, `
const { migrateKeybindingsConfig } = await load("pi-coding-agent/core/keybindings.js");
emit(input.map((text) => JSON.stringify(migrateKeybindingsConfig(JSON.parse(text)))));`, texts, &pi)
	if len(pi) != len(cases) {
		t.Fatalf("Pi answered %d of %d probes", len(pi), len(cases))
	}
	for i, c := range cases {
		var raw map[string]any
		if err := json.Unmarshal([]byte(texts[i]), &raw); err != nil {
			t.Fatal(err)
		}
		config, migrated := migrateKeybindingsConfig(raw)
		encoded, err := json.Marshal(struct {
			Config   map[string]any `json:"config"`
			Migrated bool           `json:"migrated"`
		}{config, migrated})
		if err != nil {
			t.Fatal(err)
		}
		pig := string(encoded)
		t.Logf("migrateKeybindingsConfig(%s): Pi %s, PiG %s", texts[i], pi[i], pig)
		wantPi, wantPig := c.pi, `{"config":`+texts[i]+`,"migrated":false}`
		if c.pi == "" {
			wantPi, wantPig = pig, pi[i]
		}
		if pi[i] != wantPi {
			t.Errorf("migrateKeybindingsConfig(%s): Pi answers %s, the record says %s", texts[i], pi[i], wantPi)
		}
		if pig != wantPig {
			t.Errorf("migrateKeybindingsConfig(%s) = %s, want %s", texts[i], pig, wantPig)
		}
	}
}
