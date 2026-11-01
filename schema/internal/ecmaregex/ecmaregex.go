// Package ecmaregex runs ECMAScript regular expressions: the engine behind `new RegExp(source, "u")` and `new RegExp(source)` for patterns without the d, g, v and y flags, which is what packages/schema/src/shared.ts (sharedRegex) and packages/schema/src/formats/typebox/regex.ts construct. It parses a pattern with the grammar V8 uses, translates it once into github.com/dlclark/regexp2 syntax and matches with that engine over UTF-16 code units. The i, m and s flags exist only as the modifier groups (?i:), (?m:) and (?s:) that V8 13.6 (Node 24) reads.
//
// No match timeout is set, as in V8: a pattern that backtracks catastrophically on a subject runs as long as it does there, so a pattern from an untrusted source can hold a goroutine for as long as it likes.
//
// Differences from V8 (Node 24), each pinned by a test:
//   - A backreference inside a case-insensitive modifier group compares with unicode.ToLower, not Canonicalize, so it differs on pairs such as ς and σ, ſ and s, İ and i (knownGaps in golden_test.go).
//   - V8 answers differently, for reasons its source does not make plain, for a few patterns that combine \W or a class after a case-insensitive modifier group in plain mode, and a long alternation under the m and s modifiers (knownGaps).
//   - Which strings V8 stores with one byte per unit is an implementation detail that a modifier group sees (see parser.oneByte); a string counts as one-byte here when every code unit is at most U+00FF.
//   - The Unicode data comes from Go's unicode package (unicode.Version) except the properties named in property.go, which are generated from V8's own tables; a toolchain on an older Unicode version lags V8 by the characters assigned in between.
package ecmaregex

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/dlclark/regexp2"
)

// A Regexp is a compiled regular expression. It is safe for concurrent use by multiple goroutines: regexp2 hands each match its own runner.
type Regexp struct {
	re *regexp2.Regexp
	// oneByte is the form of the expression that V8 runs on a string of code units up to U+00FF when it differs from re (see parser.oneByte); nil when the two are the same.
	oneByte *regexp2.Regexp
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
	src := appendUnits(nil, source)
	flags := ""
	if unicode {
		src = codePoints(src)
		flags = "u"
	}
	root, latin1Matters, err := parse(src, unicode, false)
	if err != nil {
		return nil, withSource(err, source, flags)
	}
	pattern := emit(root, unicode)
	re, err := regexp2.Compile(pattern, regexp2.ECMAScript)
	if err != nil {
		return nil, fmt.Errorf("ecmaregex: internal: regexp2 rejects the translation %q of /%s/%s: %w", pattern, source, flags, err)
	}
	compiled := &Regexp{re: re}
	if latin1Matters {
		root, _, err := parse(src, unicode, true)
		if err != nil {
			return nil, withSource(err, source, flags)
		}
		if narrow := emit(root, unicode); narrow != pattern {
			if compiled.oneByte, err = regexp2.Compile(narrow, regexp2.ECMAScript); err != nil {
				return nil, fmt.Errorf("ecmaregex: internal: regexp2 rejects the translation %q of /%s/%s: %w", narrow, source, flags, err)
			}
		}
	}
	return compiled, nil
}

// withSource completes the message of a syntax error with the pattern and the flags V8 prints in it.
func withSource(err error, source, flags string) error {
	if syntax, ok := errors.AsType[*syntaxError](err); ok {
		syntax.source, syntax.flags = source, flags
	}
	return err
}

// maxPooledUnits is the largest buffer the pool keeps.
const maxPooledUnits = 1 << 16

// unitBuffers holds the buffers that MatchString converts a subject into, so that a match allocates nothing for it.
var unitBuffers = sync.Pool{New: func() any { return new([]rune) }}

// MatchString is RegExp.prototype.test for an expression without the g and y flags: it reports whether any part of s matches. Like V8, it runs over the UTF-16 code units of s, and s may hold lone surrogates as WTF-8.
func (r *Regexp) MatchString(s string) bool {
	buffer := unitBuffers.Get().(*[]rune)
	units := appendUnits((*buffer)[:0], s)
	re := r.re
	if r.oneByte != nil && !slices.ContainsFunc(units, func(c rune) bool { return c > 0xFF }) {
		re = r.oneByte
	}
	matched, err := re.MatchRunes(units)
	if cap(units) <= maxPooledUnits {
		*buffer = units
		unitBuffers.Put(buffer)
	}
	if err != nil {
		// regexp2 fails a match only when its timeout runs out, and no timeout is set.
		panic(fmt.Sprintf("ecmaregex: matching %q: %v", s, err))
	}
	return matched
}
