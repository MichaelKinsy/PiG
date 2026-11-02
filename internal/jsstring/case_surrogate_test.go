package jsstring

import (
	"strings"
	"testing"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// Vectors recorded from Node 24: toLowerCase and toUpperCase keep a lone UTF-16 surrogate, spelled in WTF-8 by FromUTF16, and treat it as a
// word boundary that is neither cased nor case-ignorable.
func TestCaseKeepsLoneSurrogates(t *testing.T) {
	wtf := func(units ...uint16) string { return FromUTF16(units) }
	for _, tc := range []struct{ name, in, lower, upper string }{
		{"high between ASCII", wtf('a', 0xD800, 'b'), wtf('a', 0xD800, 'b'), wtf('A', 0xD800, 'B')},
		{"low between ASCII", wtf('a', 0xDC00, 'b'), wtf('a', 0xDC00, 'b'), wtf('A', 0xDC00, 'B')},
		{"high alone", wtf(0xD800), wtf(0xD800), wtf(0xD800)},
		{"low then high", wtf(0xDC00, 0xD800), wtf(0xDC00, 0xD800), wtf(0xDC00, 0xD800)},
		{"env name", "PIG_" + wtf(0xD800), "pig_" + wtf(0xD800), "PIG_" + wtf(0xD800)},
		{"special upper beside surrogate", "ß" + wtf(0xD800), "ß" + wtf(0xD800), "SS" + wtf(0xD800)},
		{"sigma after surrogate", wtf(0xD800) + "Σ", wtf(0xD800) + "σ", wtf(0xD800) + "Σ"},
		{"sigma before surrogate", "Σ" + wtf(0xD800), "σ" + wtf(0xD800), "Σ" + wtf(0xD800)},
		{"letter surrogate sigma", "Α" + wtf(0xD800) + "Σ", "α" + wtf(0xD800) + "σ", "Α" + wtf(0xD800) + "Σ"},
		{"encoded pair is one code point", wtf(0xD801, 0xDC00), "\U00010428", "\U00010400"},
		{"encoded halves joined", string([]byte{0xed, 0xa0, 0x81, 0xed, 0xb0, 0x80}), "\U00010428", "\U00010400"},
	} {
		if got := ToLower(tc.in); got != tc.lower {
			t.Errorf("%s: ToLower(%q) = %q, want %q", tc.name, tc.in, got, tc.lower)
		}
		if got := ToUpper(tc.in); got != tc.upper {
			t.Errorf("%s: ToUpper(%q) = %q, want %q", tc.name, tc.in, got, tc.upper)
		}
	}
	// The Windows environment dedupe in internal/nodespawn keys variables by ToUpper of the name: two names that differ only in a lone surrogate
	// stay two keys, as they are two properties of process.env in Node.
	if a, b := ToUpper("PIG_"+wtf(0xD800)), ToUpper("PIG_"+wtf(0xDC00)); a == b {
		t.Errorf("ToUpper maps PIG_\\uD800 and PIG_\\uDC00 to the same key %q", a)
	}
	if got := UpperRune(0xD800); got != wtf(0xD800) {
		t.Errorf("UpperRune(0xD800) = %q, want the lone surrogate", got)
	}
}

// Final_Sigma looks past any number of case-ignorable characters on both sides, as V8 does. x/text/cases stops after 30 of them, so a sigma
// followed by 31 marks and a letter is lowered to the final form there where Node, and jsstring, give the medial form.
func TestToLowerFinalSigmaPastThirtyMarks(t *testing.T) {
	for _, n := range []int{0, 1, 30, 31, 40} {
		marks := strings.Repeat("\u0301", n)
		for _, tc := range []struct{ in, want string }{
			{"ΑΣ" + marks + "Β", "ασ" + marks + "β"},
			{"Α" + marks + "ΣΒ", "α" + marks + "σβ"},
			{"Α" + marks + "Σ", "α" + marks + "ς"},
			{"ΑΣ" + marks, "ας" + marks},
			{"Α" + marks + "Σ" + marks + "Β", "α" + marks + "σ" + marks + "β"},
		} {
			if got := ToLower(tc.in); got != tc.want {
				t.Errorf("%d marks: ToLower(%q) = %q, Node %q", n, tc.in, got, tc.want)
			}
		}
	}
	in := "ΑΣ" + strings.Repeat("\u0301", 31) + "Β"
	if got := cases.Lower(language.Und).String(in); got == ToLower(in) {
		t.Errorf("x/text/cases now agrees with Node at 31 marks (%q); the comment on ToLower and the changelog claim they differ", got)
	}
}
