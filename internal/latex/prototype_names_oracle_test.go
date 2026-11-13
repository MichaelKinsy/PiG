package latex

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// D645, the latex row. Pi's parser looks a command up in the plain objects SYMBOLS and NEGATED_SYMBOLS (packages/tui/src/latex.ts:1090, :1075), so a
// command named like a member of Object.prototype finds the inherited function and draws its source text. PiG's tables are Go maps: the command is no
// symbol, the expression is not converted (the caller keeps the source), and \not{...} strikes the first character as it does for any other text.
// Each case holds Pi's answer, which the pinned pi-tui must give in this run, and PiG's; the last three are ordinary names and must be equal.
func TestACommandNamedLikeAnObjectPrototypeMemberIsNoLatexSymbol(t *testing.T) {
	cases := []struct{ source, pi, pig string }{
		{`\constructor`, `"function Object() { [native code] }"`, `undefined`},
		{`x \toString y`, `"x function toString() { [native code] } y"`, `undefined`},
		{`\valueOf`, `"function valueOf() { [native code] }"`, `undefined`},
		{`\hasOwnProperty`, `"function hasOwnProperty() { [native code] }"`, `undefined`},
		{`\isPrototypeOf`, `"function isPrototypeOf() { [native code] }"`, `undefined`},
		{`\not{constructor}`, `"function Object() { [native code] }"`, "\"c\u0338onstructor\""},
		{`\not{toString}`, `"function toString() { [native code] }"`, "\"t\u0338oString\""},
		{`\alpha`, `"α"`, `"α"`},
		{`\not{=}`, `"≠"`, `"≠"`},
		{`\nosuchsymbol`, `undefined`, `undefined`},
	}
	sources := make([]string, len(cases))
	for i, c := range cases {
		sources[i] = c.source
	}
	var pi []string
	pioracle.Run(t, `
const { renderLatex } = await load("pi-tui/index.js");
emit(input.map((source) => {
	try { const value = renderLatex(source); return value === undefined ? "undefined" : JSON.stringify(value); } catch (error) { return "THROWS " + String(error); }
}));`, sources, &pi)
	if len(pi) != len(cases) {
		t.Fatalf("Pi answered %d of %d sources", len(pi), len(cases))
	}
	for i, c := range cases {
		pig := "undefined"
		if out, ok := RenderLatex(c.source, RenderLatexOptions{}); ok {
			encoded, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			pig = string(encoded)
		}
		t.Logf("renderLatex(%q): Pi %s, PiG %s", c.source, pi[i], pig)
		if pi[i] != c.pi {
			t.Errorf("renderLatex(%q): Pi answers %s, the record says %s", c.source, pi[i], c.pi)
		}
		if pig != c.pig {
			t.Errorf("RenderLatex(%q) = %s, want %s", c.source, pig, c.pig)
		}
	}
}
