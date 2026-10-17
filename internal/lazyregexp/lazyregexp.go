// SPDX-License-Identifier: MIT

// Package lazyregexp compiles a package-level regular expression when it is first used instead of when its package initializes.
//
// Process start pays for every package-level regexp.MustCompile in every linked package, whether or not the run reaches the code that uses it. A Regexp from New exposes the *regexp.Regexp methods its callers use, each returning what the wrapped *regexp.Regexp returns, and Regexp returns the wrapped value for any other use. It compiles the same pattern with regexp.MustCompile, panics with the same message for an invalid pattern, and is safe for concurrent use. Only the moment of compilation and of that panic moves to the first method call. CompileAll compiles every pattern so a test can prove each one is valid.
package lazyregexp

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
)

// Regexp is a lazily compiled *regexp.Regexp.
// Its compilation state lives in the struct, so creating one costs a single allocation: every linked package creates its patterns at start.
type Regexp struct {
	expr     string
	compile  func(string) *regexp.Regexp
	once     sync.Once
	re       *regexp.Regexp
	panicked any
	compiled atomic.Bool
}

var (
	mu       sync.Mutex
	patterns []*Regexp
)

// New returns a Regexp for expr. It does not compile expr.
func New(expr string) *Regexp { return newRegexp(expr, regexp.MustCompile) }

// NewJSIgnoreCase returns a Regexp for expr that matches as a non-unicode JavaScript /i regular expression does. Write expr without a (?i)
// flag. Go's (?i) also folds U+017F onto s, U+212A onto k, U+2126 onto ω and a letter whose uppercase is several letters (ß) onto its
// capital, and folds supplementary letters. JavaScript's case canonicalization does none of these. The compiled expression folds each
// literal and class onto the characters with the same JavaScript canonical form; String returns expr. Go still matches by code point, not
// by UTF-16 code unit. A "." stops at \r, U+2028 and U+2029 as well as \n, as JavaScript's does; \s keeps Go's ASCII meaning, so spell
// JavaScript's whitespace class out.
func NewJSIgnoreCase(expr string) *Regexp { return newRegexp(expr, compileJSIgnoreCase) }

func newRegexp(expr string, compile func(string) *regexp.Regexp) *Regexp {
	r := &Regexp{expr: expr, compile: compile}
	mu.Lock()
	patterns = append(patterns, r)
	mu.Unlock()
	return r
}

// CompileAll compiles every pattern created so far and returns the first panic message of an invalid pattern, or "".
func CompileAll() (invalid string) {
	mu.Lock()
	all := append([]*Regexp(nil), patterns...)
	mu.Unlock()
	for _, r := range all {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil && invalid == "" {
					invalid = fmt.Sprint(recovered)
				}
			}()
			r.get()
		}()
	}
	return invalid
}

// get compiles the pattern on first use. Like sync.OnceValue, it repeats the panic of an invalid pattern on every use, as a package that failed to initialize would have stopped the process.
func (r *Regexp) get() *regexp.Regexp {
	r.once.Do(r.compileOnce)
	if r.panicked != nil {
		panic(r.panicked)
	}
	return r.re
}

func (r *Regexp) compileOnce() {
	defer func() {
		if recovered := recover(); recovered != nil {
			r.panicked = recovered
		}
	}()
	r.compiled.Store(true)
	r.re = r.compile(r.expr)
}

// Regexp returns the compiled expression.
func (r *Regexp) Regexp() *regexp.Regexp { return r.get() }

func (r *Regexp) String() string { return r.expr }

func (r *Regexp) NumSubexp() int { return r.get().NumSubexp() }

func (r *Regexp) SubexpNames() []string { return r.get().SubexpNames() }

func (r *Regexp) SubexpIndex(name string) int { return r.get().SubexpIndex(name) }

func (r *Regexp) Match(b []byte) bool { return r.get().Match(b) }

func (r *Regexp) MatchString(s string) bool { return r.get().MatchString(s) }

func (r *Regexp) Find(b []byte) []byte { return r.get().Find(b) }

func (r *Regexp) FindString(s string) string { return r.get().FindString(s) }

func (r *Regexp) FindStringIndex(s string) []int { return r.get().FindStringIndex(s) }

func (r *Regexp) FindSubmatch(b []byte) [][]byte { return r.get().FindSubmatch(b) }

func (r *Regexp) FindStringSubmatch(s string) []string { return r.get().FindStringSubmatch(s) }

func (r *Regexp) FindStringSubmatchIndex(s string) []int { return r.get().FindStringSubmatchIndex(s) }

func (r *Regexp) FindAllString(s string, n int) []string { return r.get().FindAllString(s, n) }

func (r *Regexp) FindAllStringIndex(s string, n int) [][]int { return r.get().FindAllStringIndex(s, n) }

func (r *Regexp) FindAllStringSubmatch(s string, n int) [][]string {
	return r.get().FindAllStringSubmatch(s, n)
}

func (r *Regexp) FindAllStringSubmatchIndex(s string, n int) [][]int {
	return r.get().FindAllStringSubmatchIndex(s, n)
}

func (r *Regexp) ReplaceAll(src, repl []byte) []byte { return r.get().ReplaceAll(src, repl) }

func (r *Regexp) ReplaceAllString(src, repl string) string {
	return r.get().ReplaceAllString(src, repl)
}

func (r *Regexp) ReplaceAllStringFunc(src string, repl func(string) string) string {
	return r.get().ReplaceAllStringFunc(src, repl)
}

func (r *Regexp) ReplaceAllLiteralString(src, repl string) string {
	return r.get().ReplaceAllLiteralString(src, repl)
}

func (r *Regexp) Split(s string, n int) []string { return r.get().Split(s, n) }

// compileJSIgnoreCase compiles expr case-insensitively as a non-unicode JavaScript /i does. It panics for an invalid pattern, with regexp.MustCompile's message.
func compileJSIgnoreCase(expr string) *regexp.Regexp {
	plain, err := syntax.Parse(expr, syntax.Perl)
	if err != nil {
		// regexp.MustCompile reports the error the way every other pattern does.
		return regexp.MustCompile(expr)
	}
	return regexp.MustCompile(foldedText(plain, expr))
}

// A class that holds most of Unicode ("." and a negated class) makes syntax.Regexp.String walk and fold every one of its code points,
// about 2 ms per class, which made the first use of a pattern with several of them take 40 ms (the provider retry pattern). jsFold
// leaves such a class in the tree as a placeholder literal, and foldedText prints the class from the few ranges it excludes in its place.
type jsFolder struct {
	placeholders []*syntax.Regexp
	excluded     [][]rune // The ranges class i leaves out.
}

// bigClass adds a class that matches every code point except the sorted [lo, hi] pairs in excluded. A nil folder spells the class in the tree.
func (f *jsFolder) bigClass(excluded []rune, flags syntax.Flags) *syntax.Regexp {
	if f == nil {
		return &syntax.Regexp{Op: syntax.OpCharClass, Rune: complement(excluded), Flags: flags}
	}
	node := &syntax.Regexp{Op: syntax.OpLiteral, Rune: []rune{0}, Flags: flags &^ syntax.FoldCase}
	f.placeholders = append(f.placeholders, node)
	f.excluded = append(f.excluded, excluded)
	return node
}

// classText spells the class that matches every code point except excluded.
func classText(excluded []rune) string {
	if len(excluded) == 0 {
		return `[\x{0}-\x{10FFFF}]`
	}
	var text strings.Builder
	text.WriteString("[^")
	for i := 0; i < len(excluded); i += 2 {
		fmt.Fprintf(&text, `\x{%x}`, excluded[i])
		if excluded[i+1] != excluded[i] {
			fmt.Fprintf(&text, `-\x{%x}`, excluded[i+1])
		}
	}
	text.WriteByte(']')
	return text.String()
}

// foldedText is the text of the pattern jsFold builds from plain.
func foldedText(plain *syntax.Regexp, expr string) string {
	folder := &jsFolder{}
	folded := jsFold(plain, folder)
	if len(folder.placeholders) == 0 {
		return folded.String()
	}
	// A placeholder's printed literal must appear exactly once in the text, or a pattern that mentions the rune would be rewritten; move the placeholders up the private-use area until none collides.
	for base := rune(0xe000); base+rune(len(folder.placeholders)) <= 0xf8ff; base += 64 {
		literals := make([]string, len(folder.placeholders))
		for i, node := range folder.placeholders {
			node.Rune[0] = base + rune(i)
			literals[i] = (&syntax.Regexp{Op: syntax.OpLiteral, Rune: []rune{node.Rune[0]}}).String()
		}
		text := folded.String()
		unique := true
		for _, literal := range literals {
			if strings.Count(text, literal) != 1 {
				unique = false
				break
			}
		}
		if !unique {
			continue
		}
		for i, literal := range literals {
			text = strings.Replace(text, literal, classText(folder.excluded[i]), 1)
		}
		return text
	}
	// Every candidate range collided: print each class from the tree.
	spelled, _ := syntax.Parse(expr, syntax.Perl)
	return jsFold(spelled, nil).String()
}

// jsFold rewrites a pattern parsed without case folding so that every literal and class also matches the characters with the same
// JavaScript canonical form (jsCanonicalize). The unfolded parse keeps each class exactly as written, which a folding parse does not.
func jsFold(re *syntax.Regexp, folder *jsFolder) *syntax.Regexp {
	for i, sub := range re.Sub {
		re.Sub[i] = jsFold(sub, folder)
	}
	switch re.Op {
	case syntax.OpLiteral:
		parts := make([]*syntax.Regexp, 0, len(re.Rune))
		for _, r := range re.Rune {
			parts = append(parts, runeSet(jsClose([]rune{r, r}), re.Flags&^syntax.FoldCase))
		}
		if len(parts) == 1 {
			return parts[0]
		}
		return &syntax.Regexp{Op: syntax.OpConcat, Sub: parts, Flags: re.Flags}
	case syntax.OpAnyCharNotNL:
		// JavaScript's "." without the s flag stops at every line terminator, not only at \n.
		return folder.bigClass([]rune{'\n', '\n', '\r', '\r', 0x2028, 0x2029}, re.Flags)
	case syntax.OpCharClass:
		// A negated class matches a character whose canonical form no excluded member shares, so fold the excluded set and complement it.
		if size(re.Rune) > unicode.MaxRune/2 {
			return folder.bigClass(jsClose(complement(re.Rune)), re.Flags)
		}
		re.Rune = jsClose(re.Rune)
	}
	return re
}

// runeSet is a literal for a one-character set and a class otherwise.
func runeSet(pairs []rune, flags syntax.Flags) *syntax.Regexp {
	if len(pairs) == 2 && pairs[0] == pairs[1] {
		return &syntax.Regexp{Op: syntax.OpLiteral, Rune: []rune{pairs[0]}, Flags: flags}
	}
	return &syntax.Regexp{Op: syntax.OpCharClass, Rune: pairs, Flags: flags}
}

// jsCanonicalize is Canonicalize(ch) of ECMA-262 for a non-unicode /i pattern: the code unit's toUpperCase, unless that is several units
// or maps a non-ASCII unit onto ASCII. A supplementary character is a surrogate pair there, and surrogates have no case. Go's simple
// uppercase mapping equals Node 24's toUpperCase for every BMP code unit whose uppercase is one unit.
func jsCanonicalize(c rune) rune {
	if c > 0xffff || multiUnitUpper(c) {
		return c
	}
	upper := unicode.ToUpper(c)
	if c >= 0x80 && upper < 0x80 {
		return c
	}
	return upper
}

// multiUnitUpperRanges are the BMP code units whose String.prototype.toUpperCase is longer than one unit (SpecialCasing.txt unconditional
// mappings; enumerated with Node 24).
var multiUnitUpperRanges = [][2]rune{
	{0xdf, 0xdf}, {0x149, 0x149}, {0x1f0, 0x1f0}, {0x390, 0x390}, {0x3b0, 0x3b0}, {0x587, 0x587}, {0x1e96, 0x1e9a}, {0x1f50, 0x1f50},
	{0x1f52, 0x1f52}, {0x1f54, 0x1f54}, {0x1f56, 0x1f56}, {0x1f80, 0x1faf}, {0x1fb2, 0x1fb4}, {0x1fb6, 0x1fb7}, {0x1fbc, 0x1fbc},
	{0x1fc2, 0x1fc4}, {0x1fc6, 0x1fc7}, {0x1fcc, 0x1fcc}, {0x1fd2, 0x1fd3}, {0x1fd6, 0x1fd7}, {0x1fe2, 0x1fe4}, {0x1fe6, 0x1fe7},
	{0x1ff2, 0x1ff4}, {0x1ff6, 0x1ff7}, {0x1ffc, 0x1ffc}, {0xfb00, 0xfb06}, {0xfb13, 0xfb17},
}

func multiUnitUpper(c rune) bool {
	for _, r := range multiUnitUpperRanges {
		if r[0] <= c && c <= r[1] {
			return true
		}
	}
	return false
}

// jsClose adds to a class's sorted [lo, hi] pairs every character with the canonical form of a member. Characters with one canonical
// form always share a Go case-folding orbit, so the orbits enumerate them.
func jsClose(pairs []rune) []rune {
	var extra []rune
	for i := 0; i < len(pairs); i += 2 {
		for m := pairs[i]; m <= pairs[i+1]; m++ {
			canonical := jsCanonicalize(m)
			for c := unicode.SimpleFold(m); c != m; c = unicode.SimpleFold(c) {
				if jsCanonicalize(c) == canonical {
					extra = append(extra, c, c)
				}
			}
		}
	}
	if len(extra) == 0 {
		return pairs
	}
	return sortedPairs(append(append([]rune(nil), pairs...), extra...))
}

func size(pairs []rune) int {
	n := 0
	for i := 0; i < len(pairs); i += 2 {
		n += int(pairs[i+1]-pairs[i]) + 1
	}
	return n
}

// complement is the sorted pairs of every rune not in pairs.
func complement(pairs []rune) []rune {
	out := make([]rune, 0, len(pairs)+2)
	next := rune(0)
	for i := 0; i < len(pairs); i += 2 {
		if pairs[i] > next {
			out = append(out, next, pairs[i]-1)
		}
		next = pairs[i+1] + 1
	}
	if next <= unicode.MaxRune {
		out = append(out, next, unicode.MaxRune)
	}
	return out
}

// sortedPairs merges [lo, hi] pairs into the sorted ranges that neither overlap nor touch that the class printer needs.
func sortedPairs(pairs []rune) []rune {
	type span struct{ lo, hi rune }
	spans := make([]span, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		spans = append(spans, span{pairs[i], pairs[i+1]})
	}
	slices.SortFunc(spans, func(a, b span) int { return int(a.lo - b.lo) })
	out := make([]rune, 0, len(pairs))
	for _, s := range spans {
		if n := len(out); n > 0 && s.lo <= out[n-1]+1 {
			out[n-1] = max(out[n-1], s.hi)
			continue
		}
		out = append(out, s.lo, s.hi)
	}
	return out
}
