package jsonvalid

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// JSON.parse of Node accepts exactly the texts Valid does, for texts of well-formed UTF-8 that nest no deeper than Node's stack allows: the session loader skips a line when JSON.parse throws.
func TestValidAgreesWithJSONParse(t *testing.T) {
	tokens := []string{"{", "}", "[", "]", ",", ":", `"a"`, `"`, `\"`, `\\`, `\u00e9`, `\ud800`, `\udc00`, `\uZZZZ`, `\u12`, `\x`, "\\/", "0", "00", "-0", "-", "1", "12", "1.5", "1.", ".5", "1e5", "1E+5", "1e", "1e-", "true", "false", "null", "tru", "nul", " ", "\t", "\r", "\n", "\f", "\v", "\u00a0", "\u2028", "\u0085", "\ufeff", "é", "😀", "\x01", "\x1f", "\x7f", "NaN", "Infinity", "undefined", "//", "/*", "\"a\":1", "{\"a\":1}", "[1,2]"}
	rng := rand.New(rand.NewSource(17))
	var texts []string
	for range 30000 {
		var b strings.Builder
		for range rng.Intn(14) {
			b.WriteString(tokens[rng.Intn(len(tokens))])
		}
		texts = append(texts, b.String())
	}
	for _, depth := range []int{1, 1000, 5000} {
		texts = append(texts, strings.Repeat("[", depth)+strings.Repeat("]", depth), strings.Repeat(`{"a":`, depth)+"1"+strings.Repeat("}", depth), strings.Repeat("[", depth)+strings.Repeat("]", depth-1))
	}
	texts = append(texts, "", " ", "\n", "1", "-1", "\"\"", "null", " [] ", "[]\n", "[] []", "{}{}", "\x00")
	for _, text := range texts {
		if !utf8.ValidString(text) {
			t.Fatalf("corpus text %q is not UTF-8", text)
		}
	}
	var want []bool
	pioracle.Run(t, `emit(input.map((text) => { try { JSON.parse(text); return true; } catch { return false; } }));`, texts, &want)
	for i, text := range texts {
		if got := Valid([]byte(text)); got != want[i] {
			t.Errorf("Valid(%q) = %v, JSON.parse %v", text, got, want[i])
		}
	}
}
