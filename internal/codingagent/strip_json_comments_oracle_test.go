package codingagent

// pi: packages/coding-agent/src/utils/json.ts

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// utils/json.ts stripJsonComments is two regex passes: string literals and `//` comments (`"(?:\\.|[^"\\])*"|\/\/[^\n]*`), then string literals and trailing commas (`,(\s*[}\]])`) over the first pass's output. A backslash before a
// line terminator or an unterminated string is not a string literal, JS `\s` is the Unicode whitespace set, and a comma followed by a comment is a trailing comma once the comment is gone.
func TestStripJSONCommentsMatchesPi(t *testing.T) {
	fragments := []string{`"`, `"`, `\`, `\"`, "\n", "\r", "\r\n", "\u2028", "\u2029", ",", ",", "}", "]", "{", "[", "//", "/", "/*", " ", "\t", "\u00a0", "\ufeff", "\u000b", "\f", "\u0085", "\u2003", "a", "é", "😀", `"a"`, `"a//b"`, `"a,}"`, `":"`, "1", "true", `"\u00e9"`, "// c\n", "//c"}
	r := rand.New(rand.NewPCG(14, 3))
	inputs := []string{"", `{"a": 1, "b": [1, 2,],}`, "{\"a\": \"x//y\", // c\n \"b\": 2,\n}", `{"u": "http://x/y"}`, "[1, // c\n]", "{\"a\": \"line\\\nbreak\", // c\n}", `{"a": "unterminated // not a comment, }`}
	for range 6000 {
		var b strings.Builder
		for range 1 + r.IntN(14) {
			b.WriteString(fragments[r.IntN(len(fragments))])
		}
		inputs = append(inputs, b.String())
	}
	var want []string
	pioracle.Run(t, `
const { stripJsonComments } = await load("pi-coding-agent/utils/json.js");
emit(input.map((text) => stripJsonComments(text)));
`, inputs, &want)
	bad := 0
	for i, in := range inputs {
		if got := stripJSONComments(in); got != want[i] {
			if bad++; bad <= 8 {
				t.Errorf("stripJSONComments(%q) = %q, Pi %q", in, got, want[i])
			}
		}
	}
	if bad > 8 {
		t.Errorf("%d of %d differ", bad, len(inputs))
	}
}
