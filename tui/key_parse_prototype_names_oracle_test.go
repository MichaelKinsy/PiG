package tui

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// D645, the key id row. Pi's parseKey looks the whole input chunk up in the plain object LEGACY_SEQUENCE_KEY_IDS (packages/tui/src/keys.ts:1376), so a
// chunk that spells a member of Object.prototype returns the inherited function (or the prototype object) where a key id string is promised. PiG's table
// is a Go map: such a chunk is no key, like any other word. Each case holds Pi's answer, which the pinned pi-tui must give in this run, and PiG's; the
// last three are ordinary inputs and must be equal.
func TestAChunkNamedLikeAnObjectPrototypeMemberIsNoKey(t *testing.T) {
	cases := []struct{ data, pi, pig string }{
		{"constructor", "function Object", "undefined"},
		{"toString", "function toString", "undefined"},
		{"valueOf", "function valueOf", "undefined"},
		{"hasOwnProperty", "function hasOwnProperty", "undefined"},
		{"__proto__", "object {}", "undefined"},
		{"a", `"a"`, `"a"`},
		{"\x1b[A", `"up"`, `"up"`},
		{"nosuchkey", "undefined", "undefined"},
	}
	chunks := make([]string, len(cases))
	for i, c := range cases {
		chunks[i] = c.data
	}
	var pi []string
	pioracle.Run(t, `
const { parseKey } = await load("pi-tui/index.js");
emit(input.map((data) => {
	const value = parseKey(data);
	if (typeof value === "function") return "function " + value.name;
	if (value === undefined) return "undefined";
	return typeof value === "string" ? JSON.stringify(value) : typeof value + " " + JSON.stringify(value);
}));`, chunks, &pi)
	if len(pi) != len(cases) {
		t.Fatalf("Pi answered %d of %d chunks", len(pi), len(cases))
	}
	for i, c := range cases {
		pig := "undefined"
		if key, ok := ParseKey(c.data); ok {
			encoded, err := json.Marshal(key)
			if err != nil {
				t.Fatal(err)
			}
			pig = string(encoded)
		}
		t.Logf("parseKey(%q): Pi %s, PiG %s", c.data, pi[i], pig)
		if pi[i] != c.pi {
			t.Errorf("parseKey(%q): Pi answers %s, the record says %s", c.data, pi[i], c.pi)
		}
		if pig != c.pig {
			t.Errorf("ParseKey(%q) = %s, want %s", c.data, pig, c.pig)
		}
	}
}
