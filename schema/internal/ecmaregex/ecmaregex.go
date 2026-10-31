// Package ecmaregex runs ECMAScript regular expressions: the engine behind `new RegExp(source, "u")` and `new RegExp(source)` for patterns without the i, m, s, g, y, d and v flags, which is what packages/schema/src/shared.ts (sharedRegex) and packages/schema/src/formats/typebox/regex.ts construct. It parses a pattern with the grammar V8 uses, translates it once into github.com/dlclark/regexp2 syntax and matches with that engine.
//
// Differences from V8 (Node 24):
//   - Modifier groups such as (?i:...), (?m:...) and (?s:...) are a SyntaxError. They are ES2025 and need case folding that this package leaves out with the i flag.
//   - With the u flag V8 can start a match in the middle of a surrogate pair when the pattern begins with an assertion that holds there (/\B/u on "x😀x" matches). Matching here only ever starts between code points, as the specification says.
//   - A repeated group name, allowed in different alternatives, is accepted and rejected as V8 does for the nestings real patterns use; on contrived nestings of repeated names the two can disagree about which are SyntaxErrors.
//   - The Unicode data comes from Go's unicode package (unicode.Version) except the properties named in property.go, which are generated from V8's own tables; a toolchain on an older Unicode version lags V8 by the characters assigned in between.
package ecmaregex

import (
	"fmt"

	"github.com/dlclark/regexp2"
)

// A Regexp is a compiled regular expression. It is safe for concurrent use by multiple goroutines: regexp2 hands each match its own runner.
type Regexp struct {
	re *regexp2.Regexp
	// units is set for an Annex B expression, which matches over the UTF-16 code units of a string rather than its code points.
	units bool
}

// Compile is `new RegExp(source, "u")`: the pattern is a sequence of code points and the strict grammar applies. The error is the SyntaxError V8 throws.
func Compile(source string) (*Regexp, error) { return compile(source, true) }

// CompileAnnexB is `new RegExp(source)`: the pattern is a sequence of UTF-16 code units and the Annex B grammar applies, which allows a lone "{", "}" or "]", identity escapes such as \_ , legacy octal escapes, \c without a letter and quantified lookaheads.
func CompileAnnexB(source string) (*Regexp, error) { return compile(source, false) }

// Valid reports whether Compile(source) succeeds, which is what the "regex" format checks.
func Valid(source string) bool {
	_, err := Compile(source)
	return err == nil
}

func compile(source string, unicode bool) (*Regexp, error) {
	var src []rune
	if unicode {
		src = []rune(source)
	} else {
		src = utf16Units(source)
	}
	root, err := parse(src, unicode)
	if err != nil {
		return nil, err
	}
	re, err := regexp2.Compile(emit(root), regexp2.ECMAScript)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression: %w", err)
	}
	return &Regexp{re: re, units: !unicode}, nil
}

// MatchString is RegExp.prototype.test for an expression without the g and y flags: it reports whether any part of s matches.
func (r *Regexp) MatchString(s string) bool {
	var matched bool
	var err error
	if r.units {
		matched, err = r.re.MatchRunes(utf16Units(s))
	} else {
		matched, err = r.re.MatchString(s)
	}
	return err == nil && matched
}

// utf16Units is s as UTF-16 code units, one per rune value.
func utf16Units(s string) []rune {
	n := 0
	for _, c := range s {
		n++
		if c >= 0x10000 {
			n++
		}
	}
	out := make([]rune, 0, n)
	for _, c := range s {
		if c >= 0x10000 {
			c -= 0x10000
			out = append(out, 0xD800+c>>10, 0xDC00+c&0x3FF)
		} else {
			out = append(out, c)
		}
	}
	return out
}
