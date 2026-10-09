// SPDX-License-Identifier: MIT

package lazyregexp

import (
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
)

func compiled(r *Regexp) bool { return r.compiled.Load() }

// New must not compile: that is the start-up work the package exists to defer.
func TestNewDoesNotCompileUntilFirstUse(t *testing.T) {
	r := New(`a(b+)c`)
	if compiled(r) {
		t.Fatal("New compiled the pattern")
	}
	if got := r.String(); got != `a(b+)c` {
		t.Fatalf("String() = %q", got)
	}
	if compiled(r) {
		t.Fatal("String compiled the pattern")
	}
	if !r.MatchString("xabbcx") {
		t.Fatal("MatchString missed a match")
	}
	if !compiled(r) {
		t.Fatal("first use did not compile")
	}
}

// Every method returns what the wrapped *regexp.Regexp returns.
func TestMethodsMatchRegexp(t *testing.T) {
	const pattern = `(?P<word>[a-z]+)(\d*)`
	const text = "foo12 bar baz7"
	want, got := regexp.MustCompile(pattern), New(pattern)
	checks := []struct {
		name      string
		want, got any
	}{
		{"MatchString", want.MatchString(text), got.MatchString(text)},
		{"FindString", want.FindString(text), got.FindString(text)},
		{"FindStringIndex", want.FindStringIndex(text), got.FindStringIndex(text)},
		{"FindStringSubmatch", want.FindStringSubmatch(text), got.FindStringSubmatch(text)},
		{"FindStringSubmatchIndex", want.FindStringSubmatchIndex(text), got.FindStringSubmatchIndex(text)},
		{"FindAllString", want.FindAllString(text, -1), got.FindAllString(text, -1)},
		{"FindAllStringSubmatch", want.FindAllStringSubmatch(text, 2), got.FindAllStringSubmatch(text, 2)},
		{"FindAllStringIndex", want.FindAllStringIndex(text, -1), got.FindAllStringIndex(text, -1)},
		{"ReplaceAllString", want.ReplaceAllString(text, "<${word}>"), got.ReplaceAllString(text, "<${word}>")},
		{"ReplaceAllLiteralString", want.ReplaceAllLiteralString(text, "$1"), got.ReplaceAllLiteralString(text, "$1")},
		{"ReplaceAllStringFunc", want.ReplaceAllStringFunc(text, strings.ToUpper), got.ReplaceAllStringFunc(text, strings.ToUpper)},
		{"Split", want.Split(text, -1), got.Split(text, -1)},
		{"NumSubexp", want.NumSubexp(), got.NumSubexp()},
		{"SubexpNames", want.SubexpNames(), got.SubexpNames()},
		{"SubexpIndex", want.SubexpIndex("word"), got.SubexpIndex("word")},
		{"Find", want.Find([]byte(text)), got.Find([]byte(text))},
		{"FindSubmatch", want.FindSubmatch([]byte(text)), got.FindSubmatch([]byte(text))},
		{"ReplaceAll", want.ReplaceAll([]byte(text), []byte("_")), got.ReplaceAll([]byte(text), []byte("_"))},
		{"Match", want.Match([]byte(text)), got.Match([]byte(text))},
	}
	for _, check := range checks {
		if !reflect.DeepEqual(check.want, check.got) {
			t.Errorf("%s = %#v, want %#v", check.name, check.got, check.want)
		}
	}
	if got.Regexp().String() != pattern {
		t.Errorf("Regexp() compiled %q", got.Regexp().String())
	}
}

// An invalid pattern panics with regexp.MustCompile's message, on first use instead of at init.
func TestInvalidPatternPanicsLikeMustCompileOnFirstUse(t *testing.T) {
	// Built at run time so staticcheck does not parse the deliberately invalid pattern.
	pattern := "a(" + strings.TrimSpace(" b")
	var want any
	func() {
		defer func() { want = recover() }()
		regexp.MustCompile(pattern)
	}()
	r := New(pattern)
	var got any
	func() {
		defer func() { got = recover() }()
		r.MatchString("ab")
	}()
	if want == nil || got != want {
		t.Fatalf("panic = %v, want %v", got, want)
	}
	if invalid := CompileAll(); !strings.Contains(invalid, pattern) {
		t.Fatalf("CompileAll() = %q, want it to name %q", invalid, pattern)
	}
}

func TestConcurrentFirstUseCompilesOnce(t *testing.T) {
	r := New(`x+`)
	var wg sync.WaitGroup
	results := make([]bool, 64)
	for i := range results {
		wg.Go(func() { results[i] = r.MatchString("xxx") })
	}
	wg.Wait()
	for i, ok := range results {
		if !ok {
			t.Fatalf("goroutine %d saw no match", i)
		}
	}
}

// A non-unicode JavaScript /i folds ASCII letters only: U+017F and U+212A do not match s and k, although Go's (?i) folds them. Expected
// values measured with Node 24 (/too many tokens/i.test("too many tokenſ") === false; /[^x]/i.test("ſ") === true).
func TestNewJSIgnoreCaseFoldsASCIILettersOnly(t *testing.T) {
	for _, tc := range []struct {
		expr, text string
		want       bool
	}{
		{`too many tokens`, "too many tokens", true},
		{`too many tokens`, "TOO MANY TOKENS", true},
		{`too many tokens`, "Too Many ToKeNs", true},
		{`too many tokens`, "too many tokenſ", false},
		{`too many tokens`, "too many to\u212Aens", false},
		{`wsl|microsoft`, "MicroſOft", false},
		{`wsl|microsoft`, "MICROSOFT", true},
		{`(?:a|s)+k`, "SSK", true},
		{`(?:a|s)+k`, "ſſ\u212A", false},
		// A class keeps the members the pattern wrote: ſ is in [^x], is not in [s], and is in [ſ].
		{`^[^x]$`, "ſ", true},
		{`^[^x]$`, "X", false},
		{`^[^s]$`, "ſ", true},
		{`^[^s]$`, "S", false},
		{`^[s]$`, "S", true},
		{`^[s]$`, "ſ", false},
		{`^[a-z]+$`, "ABC", true},
		{`^[a-z]+$`, "ſk", false},
		{`^[ſ]$`, "ſ", true},
		{`^[^\\/]+$`, "node_moduleſ", true},
		{`^[^\\/]+$`, "a/b", false},
		// Non-letters and escapes are unaffected.
		{`\d{2}\.exe$`, "12.EXE", true},
		{`é`, "É", true},
		{`^\s*K$`, "\u212A", false},
		// Non-ASCII letters fold onto the letters with the same JavaScript canonical form (String.prototype.toUpperCase when that yields
		// one unit and does not reach ASCII), the written letter included. Node 24: /é/i.test("é"), /σ/i.test("ς") and /ǅ/i.test("ǆ") are
		// true; /ω/i.test("\u2126"), /ß/i.test("ẞ") and /𐐀/i.test("𐐨") are false.
		{`é`, "é", true},
		{`É`, "é", true},
		{`^naïve$`, "naïve", true},
		{`^naïve$`, "NAÏVE", true},
		{`σ`, "σ", true},
		{`σ`, "ς", true},
		{`µ`, "Μ", true},
		{`ǅ`, "ǆ", true},
		{`ǆ`, "Ǆ", true},
		{`ω`, "\u2126", false},
		{`\x{2126}`, "ω", false},
		{`ß`, "ẞ", false},
		{`ẞ`, "ß", false},
		// U+1F80 and U+1F88 uppercase to two units, so each is its own canonical form although Go's simple mapping pairs them.
		{"\u1f80", "\u1f88", false},
		{"\u1f88", "\u1f80", false},
		{"\U00010400", "\U00010428", false},
		{`^[à-ÿ]$`, "À", true},
		{`^[^é]$`, "É", false},
		{`^[^é]$`, "é", false},
		{`^[^ω]$`, "\u2126", true},
		{`^\W$`, "ſ", true},
		{`^\w$`, "ſ", false},
		// A one-letter class next to a literal: the plain parse merges them into one literal.
		{`[s]t`, "ST", true},
		{`[s]t`, "ſt", false},
		// "." is JavaScript's: no line terminator (Node 24: /rate.?limit/i.test("rate\rlimit") === false).
		{`^rate.?limit$`, "rate-limit", true},
		{`^rate.?limit$`, "rate\rlimit", false},
		{`^rate.?limit$`, "rate\u2028limit", false},
		{`^rate.?limit$`, "rate\u0085limit", true},
		{`(?s)^rate.limit$`, "rate\rlimit", true},
	} {
		if got := NewJSIgnoreCase(tc.expr).MatchString(tc.text); got != tc.want {
			t.Errorf("NewJSIgnoreCase(%q).MatchString(%q) = %v, want %v", tc.expr, tc.text, got, tc.want)
		}
	}
	if got := NewJSIgnoreCase(`a+`).String(); got != `a+` {
		t.Errorf("String() = %q, want the source expression", got)
	}
}

func TestNewJSIgnoreCasePanicsLikeMustCompileOnFirstUse(t *testing.T) {
	r := NewJSIgnoreCase(`a(`)
	defer func() {
		if recover() == nil {
			t.Fatal("an invalid pattern did not panic on first use")
		}
	}()
	r.MatchString("a")
}
