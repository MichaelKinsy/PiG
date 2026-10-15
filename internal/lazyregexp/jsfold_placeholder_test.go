// SPDX-License-Identifier: MIT

package lazyregexp

import (
	"math/rand/v2"
	"reflect"
	"regexp"
	"regexp/syntax"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// spelledJSIgnoreCase is compileJSIgnoreCase with every class written into the tree, the form the placeholders replace.
func spelledJSIgnoreCase(expr string) *regexp.Regexp {
	plain, err := syntax.Parse(expr, syntax.Perl)
	if err != nil {
		panic(err)
	}
	return regexp.MustCompile(jsFold(plain, nil).String())
}

// Printing a class from its excluded ranges must accept exactly the code points the class written into the tree accepts, for "." and
// for negated classes, alone and inside quantifiers, alternations, groups and next to literals.
func TestPlaceholderClassesMatchSpelledClasses(t *testing.T) {
	patterns := []string{
		`rate.?limit`, `a.b`, `.`, `^.+$`, `x.*y`, `(?:a.|.b)c`, `[^x]`, `[^s]+`, `^[^\n]*$`, `[^\\/]+\.cmd$`,
		`node_modules[\\/].bin[\\/][^\\/]+\.cmd$`, `[^a-z]`, `[^\x{0}-\x{10FFFF}]|k`, `[^\x{0}-\x{ffff}]`, `[^\x{e000}]`, `.\x{e000}.`,
		`[^\x{e000}-\x{e0ff}].`, `(?:ab|.c)+d?`, `[^k]{2}`, `provider.?returned.?error`, `[\x{e000}.]`,
	}
	texts := []string{
		"", "x", "a-b", "a\nb", "a\rb", "a\u2028b", "a\u2029b", "ab", "rate limit", "RATE_LIMIT", "rate\nlimit", "ratelimit", "ſ", "S", "s", "K", "k", "\u212a",
		"a\x7fb", "\u00e9", "\U0001F600", "\ue000", "\ue000\ue000", "a\ue001b", "\\", "/", "x\\y.cmd", "node_modules/.bin/foo.cmd", "node_modules\\.bin\\x\\y.cmd",
		"provider returned error", "PROVIDER-RETURNED-ERROR", "abcd", "acd", "ccc", "xy", "x\ny", "xzzy", "kk", "kkk", "\u2028", "\r",
	}
	for _, pattern := range patterns {
		got, want := compileJSIgnoreCase(pattern), spelledJSIgnoreCase(pattern)
		for _, text := range texts {
			if g, w := got.FindStringIndex(text), want.FindStringIndex(text); !equalIndex(g, w) {
				t.Errorf("%q on %q: placeholder form finds %v, spelled form finds %v", pattern, text, g, w)
			}
		}
	}
}

func equalIndex(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A pattern that mentions the placeholder range takes the next clear range, and one that leaves no clear range falls back to the spelled classes.
func TestPlaceholderRangeCollisionsStillMatchSpelledClasses(t *testing.T) {
	for _, pattern := range []string{`.\x{e000}`, `[\x{e000}-\x{e03f}].`, `.[^\x{e000}-\x{f8ff}]`, `[^a]\x{e040}`} {
		got, want := compileJSIgnoreCase(pattern), spelledJSIgnoreCase(pattern)
		for _, text := range []string{"x\ue000", "\ue000\ue000", "a\ue040", "ba\ue040", "\ue040", "\r\ue000", "a\uf000", "\uf000\uf000"} {
			if g, w := got.FindStringIndex(text), want.FindStringIndex(text); !equalIndex(g, w) {
				t.Errorf("%q on %q: %v, want %v", pattern, text, g, w)
			}
		}
	}
}

// Twenty "." in one pattern compiled in about 40 ms when String folded each class code point by code point.
func TestCompilingManyDotsDoesNotWalkTheClasses(t *testing.T) {
	expr := ""
	for range 40 {
		expr += "rate.?limit|"
	}
	expr += "x"
	start := time.Now()
	compileJSIgnoreCase(expr)
	elapsed := time.Since(start)
	testenv.TimingGate(t, elapsed > 20*time.Millisecond, "compiling 40 dots took %v; the classes are being walked again", elapsed)
}

// Random patterns built from dots, negated classes, placeholder-range literals and classes, quantifiers, groups and anchors find the same matches and submatches in the placeholder form as in the spelled form.
func TestPlaceholderFormMatchesSpelledFormOnRandomPatterns(t *testing.T) {
	tokens := []string{".", "[^a]", `[^\n]`, "[^a-z0-9]", "a", "K", "s", `\x{e000}`, `[\x{e000}-\x{e0ff}]`, "*", "+", "?", "*?", "{2}", "(", ")", "(?:", "|", "^", "$", `\b`, `[^\x{e040}]`, "x", `\.`, `[^\\/]`}
	texts := []string{"", "a", "aK\nb", "S\u017fs", "\ue000\ue001\ue040", "rate limit", "x.y\r\n", "\u2028a\u2029", "kK\u212a", "zz\U0001F600", "a/b\\c", "abcabc"}
	random := rand.New(rand.NewPCG(3, 4))
	checked := 0
	for checked < 600 {
		var expr strings.Builder
		for range 1 + random.IntN(8) {
			expr.WriteString(tokens[random.IntN(len(tokens))])
		}
		if _, err := syntax.Parse(expr.String(), syntax.Perl); err != nil {
			continue
		}
		checked++
		got, want := compileJSIgnoreCase(expr.String()), spelledJSIgnoreCase(expr.String())
		for _, text := range texts {
			if g, w := got.FindAllStringSubmatchIndex(text, -1), want.FindAllStringSubmatchIndex(text, -1); !reflect.DeepEqual(g, w) {
				t.Fatalf("%q on %q: placeholder form finds %v, spelled form finds %v", expr.String(), text, g, w)
			}
		}
	}
}
