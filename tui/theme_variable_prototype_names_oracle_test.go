package tui

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// D645, the theme variable row. Pi's resolveVarRefs tests `value in vars` and reads `vars[value]` on the theme's plain `vars` object
// (packages/coding-agent/src/modes/interactive/theme/theme.ts:90-94), so a color that names a member of Object.prototype passes the test, resolves to
// the inherited function or object, and the next step calls startsWith on it: a TypeError where the loader promises "Variable reference not found".
// PiG's vars are a Go map: the name is not a variable and the loader says so. Pi writes one copy of its dark theme per reference with the color `accent`
// set to it, both loaders read the same files. Each case holds Pi's answer, which the pinned Pi must give in this run, and PiG's; the last two are an
// ordinary missing variable and a literal color and must be equal.
func TestAThemeColorNamedLikeAnObjectPrototypeMemberIsAMissingVariable(t *testing.T) {
	cases := []struct{ reference, pi, pig string }{
		{"constructor", "THROWS TypeError: value.startsWith is not a function", "THROWS Error: Variable reference not found: constructor"},
		{"toString", "THROWS TypeError: value.startsWith is not a function", "THROWS Error: Variable reference not found: toString"},
		{"__proto__", "THROWS TypeError: value.startsWith is not a function", "THROWS Error: Variable reference not found: __proto__"},
		{"nosuchvar", "THROWS Error: Variable reference not found: nosuchvar", "THROWS Error: Variable reference not found: nosuchvar"},
		{"#123456", "loaded", "loaded"},
	}
	dir := t.TempDir()
	references := make([]string, len(cases))
	for i, c := range cases {
		references[i] = c.reference
	}
	var pi []string
	pioracle.Run(t, `
const { writeFileSync } = await import("node:fs");
const theme = await load("pi-coding-agent/modes/interactive/theme/theme.js");
const dark = JSON.parse(readFileSync(root + "/pi-coding-agent/modes/interactive/theme/dark.json", "utf8"));
emit(input.references.map((reference, index) => {
	const file = input.dir + "/theme-" + index + ".json";
	writeFileSync(file, JSON.stringify({ ...dark, name: "probe-" + index, colors: { ...dark.colors, accent: reference } }));
	try { theme.loadThemeFromPath(file); return "loaded"; } catch (error) { return "THROWS " + String(error); }
}));`, map[string]any{"dir": dir, "references": references}, &pi)
	if len(pi) != len(cases) {
		t.Fatalf("Pi answered %d of %d themes", len(pi), len(cases))
	}
	for i, c := range cases {
		pig := "loaded"
		if _, err := LoadThemeFromPath(filepath.Join(dir, fmt.Sprintf("theme-%d.json", i)), TerminalColorModeTrueColor); err != nil {
			pig = "THROWS Error: " + err.Error()
		}
		t.Logf("a theme whose color accent is %q: Pi %s, PiG %s", c.reference, pi[i], pig)
		if pi[i] != c.pi {
			t.Errorf("a theme whose color accent is %q: Pi answers %s, the record says %s", c.reference, pi[i], c.pi)
		}
		if pig != c.pig {
			t.Errorf("LoadThemeFromPath of a theme whose color accent is %q: %s, want %s", c.reference, pig, c.pig)
		}
	}
}
