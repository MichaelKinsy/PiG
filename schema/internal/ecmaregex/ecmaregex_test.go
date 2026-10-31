package ecmaregex

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
)

type compileCase struct {
	source          string
	unicode, annexB bool
}

// TestCompileGrammar covers the grammar points where the u-mode grammar and the Annex B grammar disagree with each other or with RE2.
func TestCompileGrammar(t *testing.T) {
	cases := []compileCase{
		{``, true, true},
		{`abc`, true, true},
		{`a|b|`, true, true},
		{`(`, false, false},
		{`)`, false, false},
		{`a)`, false, false},
		{`^(abc]`, false, false},
		{`[`, false, false},
		{`[abc`, false, false},
		{`\`, false, false},
		{`*a`, false, false},
		{`+`, false, false},
		{`?`, false, false},
		{`a**`, false, false},
		{`a+*`, false, false},
		{`a???`, false, false},
		{`^*`, false, false},
		{`$+`, false, false},
		{`\b+`, false, false},
		{`\B*`, false, false},
		{`{1}`, false, false},
		{`a{2,1}`, false, false},
		{`a{1}{2}`, false, false},
		{`(?`, false, false},
		{`(?<`, false, false},
		{`(?<a`, false, false},
		{`(?<>a)`, false, false},
		{`(?<1a>a)`, false, false},
		{`(?P<a>b)`, false, false},
		{`(?'a'b)`, false, false},
		{`(?#c)`, false, false},
		{`[z-a]`, false, false},
		{`[\u0041-\u0040]`, false, false},
		{`(?<=a)*`, false, false},
		{`(?<!a)+`, false, false},
		{`(?<=a){1}`, false, false},
		{`(?<a>x)(?<a>y)`, false, false},
		{`(?<a>x)\k<b>`, false, false},
		{`(?<a>x)\k`, false, false},
		{`(?<a>x)\k<`, false, false},
		{`(?<a>x)[\k]`, false, false},
		{`\u{110000}`, false, true},
		{`\u{}`, false, true},
		{`a{`, false, true},
		{`a{1`, false, true},
		{`a{1,`, false, true},
		{`a{,2}`, false, true},
		{`a{}`, false, true},
		{`{`, false, true},
		{`}`, false, true},
		{`]`, false, true},
		{`x{1,2}{`, false, true},
		{`\_`, false, true},
		{`\a`, false, true},
		{`\-`, false, true},
		{`\é`, false, true},
		{`\e`, false, true},
		{`\1`, false, true},
		{`(a)\2`, false, true},
		{`\8`, false, true},
		{`\00`, false, true},
		{`\01`, false, true},
		{`\012`, false, true},
		{`\c`, false, true},
		{`\c1`, false, true},
		{`\x4`, false, true},
		{`\u004`, false, true},
		{`\k`, false, true},
		{`\k<a>`, false, true},
		{`[\c]`, false, true},
		{`[\c1]`, false, true},
		{`[\B]`, false, true},
		{`[\1]`, false, true},
		{`[\8]`, false, true},
		{`[\k]`, false, true},
		{`[\d-z]`, false, true},
		{`[a-\d]`, false, true},
		{`[\d-\w]`, false, true},
		{`[\s-a]`, false, true},
		{`[\p{L}-z]`, false, false},
		{`[😀-😎]`, true, false},
		{`(?=a)*`, false, true},
		{`(?!a)+`, false, true},
		{`(?=a){2}`, false, true},
		{`\p`, false, true},
		{`\p{`, false, true},
		{`\p{Foo}`, false, true},
		{`\p{Latin}`, false, true},
		{`\p{Script=Foo}`, false, true},
		{`\p{lowercase}`, false, true},
		{`\p{ASCII=Yes}`, false, true},
		{`\pL`, false, true},
		{`\P{Foo}`, false, true},
		{`\0`, true, true},
		{`\xAF`, true, true},
		{`\u00AF`, true, true},
		{`\u{1F600}`, true, true},
		{`\uD83D\uDE00`, true, true},
		{`\uD83D`, true, true},
		{`\cA`, true, true},
		{`\/`, true, true},
		{`\^\$\\\.\*\+\?\(\)\[\]\{\}\|`, true, true},
		{`[\-]`, true, true},
		{`[\b]`, true, true},
		{`[\0]`, true, true},
		{`[\b-\x7f]`, true, true},
		{`[a-z\d]`, true, true},
		{`[\d\w\s]`, true, true},
		{`[\d\D-]`, true, true},
		{`[-\d]`, true, true},
		{`[\d-]`, true, true},
		{`[a-]`, true, true},
		{`[]`, true, true},
		{`[^]`, true, true},
		{`[\]]`, true, true},
		{`x{1}`, true, true},
		{`x{1,}`, true, true},
		{`x{1,2}?`, true, true},
		{`(a)\1`, true, true},
		{`\1(a)`, true, true},
		{`(a)(?:b)(c)\2`, true, true},
		{`(?:a)(?=b)(?!c)(?<=d)(?<!e)`, true, true},
		{`(?<a>x)\k<a>`, true, true},
		{`\k<a>(?<a>x)`, true, true},
		{`(?<$_é1>x)`, true, true},
		{`(?<\u0061b>x)\k<ab>`, true, true},
		{`(?<\u{1d49c}>x)`, true, true},
		{`(?<a>x)|(?<a>y)`, true, true},
		{`\p{L}`, true, true},
		{`\P{Lu}`, true, true},
		{`\p{Lu}\p{gc=Ll}\p{General_Category=Lt}`, true, true},
		{`\p{Letter}\p{digit}\p{punct}\p{Combining_Mark}\p{cntrl}`, true, true},
		{`\p{Script=Latin}\p{sc=Grek}\p{Script_Extensions=Hira}\p{scx=Kana}`, true, true},
		{`\p{ASCII}\p{Any}\p{Assigned}\p{AHex}\p{Alpha}\p{Lower}\p{Upper}\p{space}\p{IDS}\p{IDC}`, true, true},
		{`[\p{L}\P{N}]`, true, true},
		{`\p{Emoji}\p{Extended_Pictographic}\p{Bidi_Mirrored}\p{CWKCF}\p{XIDS}`, true, true},
	}
	for _, c := range cases {
		if _, err := Compile(c.source); (err == nil) != c.unicode {
			t.Errorf("Compile(%q): err = %v, want accepted = %v", c.source, err, c.unicode)
		}
		if _, err := CompileAnnexB(c.source); (err == nil) != c.annexB {
			t.Errorf("CompileAnnexB(%q): err = %v, want accepted = %v", c.source, err, c.annexB)
		}
		if got := Valid(c.source); got != c.unicode {
			t.Errorf("Valid(%q) = %v, want %v", c.source, got, c.unicode)
		}
	}
}

type matchCase struct {
	pattern string
	input   string
	want    bool
}

func runMatchCases(t *testing.T, mode string, compile func(string) (*Regexp, error), cases []matchCase) {
	t.Helper()
	for _, c := range cases {
		re, err := compile(c.pattern)
		if err != nil {
			t.Errorf("%s %q: %v", mode, c.pattern, err)
			continue
		}
		if got := re.MatchString(c.input); got != c.want {
			t.Errorf("%s %q.test(%q) = %v, want %v", mode, c.pattern, c.input, got, c.want)
		}
	}
}

// sharedMatches are the cases whose result is the same under both grammars.
var sharedMatches = []matchCase{
	// dot, anchors
	{`.`, "\n", false},
	{`.`, "\r", false},
	{`.`, "\u2028", false},
	{`.`, "\u2029", false},
	{`.`, "a", true},
	{`.`, "\u0085", true},
	{`.`, "\v", true},
	{`^.*$`, "a\nb", false},
	{`^a$`, "a", true},
	{`^a$`, "a\n", false},
	{`a$`, "a\n", false},
	{`^a`, "\na", false},
	{`^$`, "", true},
	{`^$`, "\n", false},
	{`a$`, "ba", true},
	{`x`, "abc", false},
	{`b`, "abc", true},
	{`^abc$`, "abc\n", false},
	{`foo`, "a foo b", true},
	// shorthand classes
	{`\d`, "5", true},
	{`\d`, "٣", false},
	{`\D`, "٣", true},
	{`\D`, "5", false},
	{`\w`, "_", true},
	{`\w`, "é", false},
	{`\W`, "é", true},
	{`\w`, "\u212a", false},
	{`\w`, "\u017f", false},
	{`^\s$`, "\u00a0", true},
	{`^\s$`, "\ufeff", true},
	{`^\s$`, "\u2028", true},
	{`^\s$`, "\u3000", true},
	{`^\s$`, "\u1680", true},
	{`^\s$`, "\u2000", true},
	{`^\s$`, "\u200a", true},
	{`^\s$`, "\u200b", false},
	{`^\s$`, "\u180e", false},
	{`^\s$`, "\u0085", false},
	{`^\s$`, "\v", true},
	{`^\S$`, "\u00a0", false},
	{`^\S$`, "a", true},
	// word boundary
	{`\bfoo\b`, "a foo b", true},
	{`\bfoo\b`, "afoo", false},
	{`\bfoo\b`, "éfoo", true},
	{`a\Bb`, "ab", true},
	{`a\B`, "a", false},
	{`\b`, "", false},
	{`\B`, "", true},
	{`\b\b`, "a", true},
	// escapes
	{`^\t\n\v\f\r\0$`, "\t\n\v\f\r\x00", true},
	{`^\cJ$`, "\n", true},
	{`^\cj$`, "\n", true},
	{`^\cC$`, "\\cC", false},
	{`^\x41\u0042$`, "AB", true},
	{`^\xe9$`, "é", true},
	{`^\u00e9$`, "é", true},
	{`^[\b]$`, "\b", true},
	{`^\/$`, "/", true},
	{`^\.$`, ".", true},
	{`^\.$`, "a", false},
	// classes
	{`^[a-c]+$`, "abcabc", true},
	{`^[^a-c]$`, "b", false},
	{`^[^a-c]$`, "d", true},
	{`^[]$`, "a", false},
	{`^[^]$`, "\n", true},
	{`^[^]$`, "x", true},
	{`^[\d\D]$`, "x", true},
	{`^[^\D\s]$`, "5", true},
	{`^[^\D\s]$`, " ", false},
	{`^[^\D\s]$`, "a", false},
	{`^[\D\s]$`, "5", false},
	{`^[\D\s]$`, " ", true},
	{`^[\D\s]$`, "a", true},
	{`^[\S\d]$`, " ", false},
	{`^[\S\d]$`, "5", true},
	{`^[^\S]$`, " ", true},
	{`^[^\S]$`, "a", false},
	{`^[\W\S]$`, "a", true},
	{`^[\w\W]$`, "\n", true},
	{`^[^\w\W]$`, "a", false},
	{`^[\s\S]$`, "\n", true},
	{`^[-a]$`, "-", true},
	{`^[a-]$`, "-", true},
	{`^[a\-c]$`, "b", false},
	{`^[a\-c]$`, "-", true},
	{`^[\]]$`, "]", true},
	{`^[\^a]$`, "^", true},
	{`^[a^]$`, "^", true},
	{`^[\x41-\x43]$`, "B", true},
	{`^[\u0041-\u0043]$`, "D", false},
	{`^[ -~]+$`, "Hello, world", true},
	{`^[\0-\x7f]$`, "é", false},
	{`^[^\0-\x7f]$`, "é", true},
	// groups, alternation, quantifiers
	{`^(a|b)c$`, "bc", true},
	{`^(?:a|b)+$`, "abba", true},
	{`^a|b$`, "ab", true},
	{`^(a|ab)(c|bcd)(d*)$`, "abcd", true},
	{`^a{2}$`, "a", false},
	{`^a{2}$`, "aa", true},
	{`^a{2,3}$`, "aaaa", false},
	{`^a{2,}$`, "aaaaa", true},
	{`^a{0}$`, "", true},
	{`^(?:ab){2}$`, "abab", true},
	{`^a*?$`, "aaa", true},
	{`^(a+?)a$`, "aaa", true},
	{`^a??b$`, "ab", true},
	{`^(a*)*$`, "aaa", true},
	{`^(a*)+$`, "", true},
	{`^(?:a?)*b$`, "aab", true},
	{`^(a|b)*c$`, "abc", true},
	// lookaround
	{`a(?=b)`, "ab", true},
	{`a(?=b)`, "ac", false},
	{`a(?!b)`, "ab", false},
	{`a(?!b)`, "ac", true},
	{`(?<=a)b`, "ab", true},
	{`(?<=a)b`, "b", false},
	{`(?<!a)b`, "ab", false},
	{`(?<!a)b`, "cb", true},
	{`(?<=a|bc)d`, "bcd", true},
	{`(?<=^a)b`, "ab", true},
	{`(?<=\b)a`, "a", true},
	{`(?<=(\d+)(\d+))$`, "1053", true},
	{`^(?=.*\d)(?=.*[a-z]).{4,}$`, "ab12", true},
	{`^(?=.*\d)(?=.*[a-z]).{4,}$`, "abcd", false},
	{`(?<=\1(a))b`, "ab", false},
	{`(?<=\1(a))b`, "aab", true},
	{`(?<=(a)\1)b`, "ab", true},
	{`(?<=(a)\1)b`, "aab", true},
	// backreferences
	{`^(a)\1$`, "aa", true},
	{`^(a)\1$`, "ab", false},
	{`^(a)(b)\2\1$`, "abba", true},
	{`^\1(a)$`, "a", true},
	{`^\1(a)$`, "aa", false},
	{`^(a\1)$`, "a", true},
	{`^(?:(a)|b)\1$`, "b", true},
	{`^(?:(a)|b)\1$`, "ba", false},
	{`^(?:(a)|b)\1$`, "aa", true},
	{`^(a)|\1b$`, "b", true},
	{`^(?:(a)|(b))\1\2$`, "a", false},
	{`^(?:(a)|(b))\1\2$`, "aa", true},
	{`^(?:(a)|(b))\1\2$`, "bb", true},
	{`^(?:(a)|(b))\1\2$`, "ab", false},
	{`^(?=(a))\1$`, "a", true},
	{`^(?!(a))\1$`, "", true},
	{`^(a*)\1$`, "aaaa", true},
	{`^(a*)\1$`, "aaa", false},
	{`^(\w)\1+$`, "aaa", true},
	{`^(\w)\1+$`, "ab", false},
	{`^(a)(b)(c)(d)(e)(f)(g)(h)(i)(j)\10$`, "abcdefghijj", true},
	{`^(a)(b)(c)(d)(e)(f)(g)(h)(i)(j)\10$`, "abcdefghija", false},
	// named groups
	{`^(?<a>x)\k<a>$`, "xx", true},
	{`^(?<a>x)\k<a>$`, "xy", false},
	{`^\k<a>(?<a>x)$`, "x", true},
	{`^(?<a>x)(y)\2$`, "xyy", true},
	{`^(?<a>.)(.)\1$`, "aba", true},
	{`^(?<a>.)(.)\1$`, "abb", false},
	{`^(?<b>b)(?<a>a)\k<b>\k<a>$`, "baba", true},
	{`^(?<b>b)(?<a>a)\k<b>\k<a>$`, "baab", false},
	{`^(?<a>.)\1$`, "xx", true},
	{`^(?<a>x)|(?<a>y)\k<a>$`, "yy", true},
	{`^(?:(?<a>x)|(?<a>y))\k<a>$`, "yy", true},
	{`^(?:(?<a>x)|(?<a>y))\k<a>$`, "xx", true},
	{`^(?:(?<a>x)|(?<a>y))\k<a>$`, "xy", false},
	{`^(?:(?<a>x)|(?<a>y))\k<a>$`, "yx", false},
	{`^(?<$>a)\k<$>$`, "aa", true},
	{`^(?<\u0061b>a)\k<ab>$`, "aa", true},
}

var unicodeMatches = []matchCase{
	{`^.$`, "😀", true},
	{`^..$`, "😀", false},
	{`^\u{1F600}$`, "😀", true},
	{`^\uD83D\uDE00$`, "😀", true},
	{`^[\uD83D\uDE00]$`, "😀", true},
	{`^[^x]$`, "😀", true},
	{`^[😀-😎]$`, "😁", true},
	{`^[😀-😎]$`, "😏", false},
	{`^🐲*$`, "🐲🐲", true},
	{`^🐲*$`, "", true},
	{`^🐲*$`, "🐉", false},
	{`^[\u{1F600}-\u{1F64F}]+$`, "😀😁", true},
	{`^\u{41}$`, "A", true},
	{`^\u{0000041}$`, "A", true},
	{`^\W$`, "😀", true},
	{`^\W+$`, "😀", true},
	{`^\D$`, "😀", true},
	{`^\S$`, "😀", true},
	{`^[^\d]$`, "😀", true},
	{`^[\s\S]$`, "😀", true},
	{`^[^\s\S]$`, "😀", false},
	{`\p{Lu}`, "a", false},
	{`\p{Lu}`, "A", true},
	{`\P{Lu}`, "a", true},
	{`\P{Lu}`, "A", false},
	{`^\p{L}$`, "😀", false},
	{`^\p{L}$`, "𝒜", true},
	{`^\p{Lu}$`, "𝒜", true},
	{`^\P{L}$`, "😀", true},
	{`^\p{Letter}+$`, "Héllo", true},
	{`^\p{Letter}+$`, "H3llo", false},
	{`^\p{digit}+$`, "42৪২", true},
	{`^\p{gc=Nd}$`, "٣", true},
	{`^\p{General_Category=Decimal_Number}$`, "٣", true},
	{`^\p{General_Category=Letter}$`, "x", true},
	{`^\p{Nd}$`, "x", false},
	{`^\p{punct}$`, "!", true},
	{`^\p{cntrl}$`, "\x00", true},
	{`^\p{Cc}$`, "\x7f", true},
	{`^\p{Combining_Mark}$`, "\u0300", true},
	{`^\p{M}$`, "\u0903", true},
	{`^\p{Zs}$`, "\u00a0", true},
	{`^\p{Z}$`, "\u2028", true},
	{`^\p{Cn}$`, "\uffff", true},
	{`^\p{Co}$`, "\ue000", true},
	{`^\p{Script=Latin}+$`, "abcé", true},
	{`^\p{Script=Latin}+$`, "abcα", false},
	{`^\p{sc=Latn}$`, "z", true},
	{`^\p{Script=Greek}$`, "α", true},
	{`^\p{sc=Grek}$`, "α", true},
	{`^\p{Script=Cyrillic}$`, "я", true},
	{`^\p{Script=Han}$`, "漢", true},
	{`^\p{Script=Hiragana}$`, "あ", true},
	{`^\p{Script=Katakana}$`, "ア", true},
	{`^\p{Script=Hangul}$`, "한", true},
	{`^\p{Script=Hebrew}$`, "א", true},
	{`^\p{Script=Arabic}$`, "ا", true},
	{`^\p{Script=Thai}$`, "ก", true},
	{`^\p{Script=Common}$`, "1", true},
	{`^\p{Script=Inherited}$`, "\u0300", true},
	{`^\p{Script=Unknown}$`, "\uffff", true},
	{`^\p{Script=Unknown}$`, "a", false},
	{`^\p{Script=Hira}$`, "ー", false},
	{`^\p{Script=Zyyy}$`, "ー", true},
	{`^\p{scx=Hira}$`, "ー", true},
	{`^\p{Script_Extensions=Katakana}$`, "ー", true},
	{`^\p{scx=Latn}$`, "z", true},
	{`^\p{scx=Grek}$`, "α", true},
	{`^\p{ASCII}$`, "\x7f", true},
	{`^\p{ASCII}$`, "\u0080", false},
	{`^\P{ASCII}$`, "é", true},
	{`^\p{Any}$`, "\x00", true},
	{`^\p{Any}$`, "😀", true},
	{`^\P{Any}$`, "a", false},
	{`^\p{Assigned}$`, "a", true},
	{`^\p{Assigned}$`, "\uffff", false},
	{`^\P{Assigned}$`, "\uffff", true},
	{`^\p{ASCII_Hex_Digit}+$`, "09afAF", true},
	{`^\p{AHex}$`, "g", false},
	{`^\p{Alphabetic}$`, "é", true},
	{`^\p{Alpha}$`, "Ⅷ", true},
	{`^\p{Alpha}$`, "1", false},
	{`^\p{Lowercase}$`, "ª", true},
	{`^\p{Lower}$`, "A", false},
	{`^\p{Uppercase}$`, "Ⅷ", true},
	{`^\p{Upper}$`, "a", false},
	{`^\p{White_Space}$`, "\u0085", true},
	{`^\p{space}$`, "\ufeff", false},
	{`^\p{ID_Start}$`, "a", true},
	{`^\p{IDS}$`, "1", false},
	{`^\p{ID_Continue}$`, "1", true},
	{`^\p{IDC}$`, "-", false},
	{`^\p{XID_Start}$`, "a", true},
	{`^\p{XIDC}$`, "_", true},
	{`^\p{Emoji}$`, "😀", true},
	{`^\p{Emoji}$`, "1", true},
	{`^\p{Emoji}$`, "a", false},
	{`^\p{Extended_Pictographic}$`, "😀", true},
	{`^\p{Emoji_Presentation}$`, "1", false},
	{`^\p{Emoji_Modifier}$`, "\U0001f3fb", true},
	{`^\p{Regional_Indicator}$`, "\U0001f1e6", true},
	{`^\p{Math}$`, "+", true},
	{`^\p{Dash}$`, "-", true},
	{`^\p{Hex_Digit}$`, "F", true},
	{`^\p{Ideographic}$`, "漢", true},
	{`^\p{Cased}$`, "ǅ", true},
	{`^\p{Case_Ignorable}$`, "'", true},
	{`^\p{Changes_When_Uppercased}$`, "a", true},
	{`^\p{CWL}$`, "A", true},
	{`^\p{Bidi_Mirrored}$`, "(", true},
	{`^\p{Default_Ignorable_Code_Point}$`, "\u00ad", true},
	{`^\p{Grapheme_Extend}$`, "\u0300", true},
	{`^\p{Grapheme_Base}$`, "a", true},
	{`^\p{Noncharacter_Code_Point}$`, "\uffff", true},
	{`^\p{Variation_Selector}$`, "\ufe0f", true},
	{`^[\p{L}\d]+$`, "aé1", true},
	{`^[\p{L}\d]+$`, "a-1", false},
	{`^[^\p{L}\d]+$`, "-!", true},
	{`^[\P{L}\p{L}]$`, "x", true},
	{`^[^\P{L}\p{L}]$`, "x", false},
	{`^[\p{Lu}\p{Ll}]+$`, "aB", true},
	{`^[\w-]+$`, "a-b", true},
	{`(?<=\p{L})\p{Lu}`, "aB", true},
	{`(?<n>\p{L})\k<n>`, "éé", true},
	{`^\p{L}{2}$`, "ab", true},
	{`^[\d-]$`, "-", true},
	{`^[\d\w]$`, "é", false},
}

var annexBMatches = []matchCase{
	{`^.$`, "😀", false},
	{`^..$`, "😀", true},
	{`^\uD83D\uDE00$`, "😀", true},
	{`^\uD83D$`, "😀", false},
	{`\uD83D`, "😀", true},
	{`\uDE00`, "😀", true},
	{`^[\uD83D\uDE00]+$`, "😀", true},
	{`^[^x]$`, "😀", false},
	{`^[^x]{2}$`, "😀", true},
	{`^🐲*$`, "🐲", true},
	{`^🐲*$`, "🐲🐲", false},
	{`^🐲*$`, "", false},
	{`^(?:🐲)*$`, "🐲🐲", true},
	{`^\W+$`, "😀", true},
	{`^\W$`, "😀", false},
	{`^\u{41}$`, "A", false},
	{`^\u{2}$`, "uu", true},
	{`^\u{2}$`, "u", false},
	{`^x{`, "x{", true},
	{`^x{1`, "x{1", true},
	{`^x{1,`, "x{1,", true},
	{`^x{,2}$`, "x{,2}", true},
	{`^{$`, "{", true},
	{`^}$`, "}", true},
	{`^]$`, "]", true},
	{`^[]a]$`, "a", false},
	{`^[]a]$`, "]", false},
	{`^a{1}{`, "a{", true},
	{`^\_$`, "_", true},
	{`^\a$`, "a", true},
	{`^\a$`, "\x07", false},
	{`^\-$`, "-", true},
	{`^\é$`, "é", true},
	{`^\p{L}$`, "p{L}", true},
	{`^\p{L}$`, "é", false},
	{`^\p$`, "p", true},
	{`^\1$`, "\x01", true},
	{`^\0$`, "\x00", true},
	{`^\00$`, "\x00", true},
	{`^\01$`, "\x01", true},
	{`^\012$`, "\n", true},
	{`^\0123$`, "\n3", true},
	{`^\377$`, "ÿ", true},
	{`^\400$`, " 0", true},
	{`^\7$`, "\x07", true},
	{`^\8$`, "8", true},
	{`^\9$`, "9", true},
	{`^\08$`, "\x008", true},
	{`^(a)\2$`, "a\x02", true},
	{`^(a)\10$`, "a\x08", true},
	{`^(a)\8$`, "a8", true},
	{`^\c$`, "\\c", true},
	{`^\c1$`, "\\c1", true},
	{`^[\c1]$`, "\x11", true},
	{`^[\c_]$`, "\x1f", true},
	{`^[\c]$`, "c", true},
	{`^[\c]$`, "\\", true},
	{`^\ca$`, "\x01", true},
	{`^\x4$`, "x4", true},
	{`^\u004$`, "u004", true},
	{`^\k<a>$`, "k<a>", true},
	{`^\k$`, "k", true},
	{`^[\d-z]+$`, "5-z", true},
	{`^[\d-z]$`, "y", false},
	{`^[a-\d]+$`, "a-5", true},
	{`^[a-\d]$`, "b", false},
	{`^[\w-.]+$`, "a.-", true},
	{`^[\s-a]$`, "-", true},
	{`^[\B]$`, "B", true},
	{`^[\1]$`, "\x01", true},
	{`^[\8]$`, "8", true},
	{`^(?=a)*a$`, "a", true},
	{`^(?=a){2}a$`, "a", true},
	{`^(?!a)+b$`, "b", true},
	{`^(?:(?=a))*a$`, "a", true},
	{`^[\b]$`, "\b", true},
}

func TestMatchSharedSemantics(t *testing.T) {
	runMatchCases(t, "u", Compile, sharedMatches)
	runMatchCases(t, "annexB", CompileAnnexB, sharedMatches)
}

func TestMatchUnicode(t *testing.T) {
	runMatchCases(t, "u", Compile, unicodeMatches)
}

func TestMatchAnnexB(t *testing.T) {
	runMatchCases(t, "annexB", CompileAnnexB, annexBMatches)
	// A property escape is only an escape with the u flag; without it, \p is the letter p.
	runMatchCases(t, "annexB", CompileAnnexB, []matchCase{{`^\p{2}$`, "pp", true}, {`^\P$`, "P", true}})
}

// TestSharedRegexOrder checks the way Pi's sharedRegex uses the two compilers: the u-mode compile first, then the Annex B compile when that fails.
func TestSharedRegexOrder(t *testing.T) {
	cases := []matchCase{
		{`^\_$`, "_", true},
		{`^a{$`, "a{", true},
		{`^[\d-z]$`, "-", true},
		{`^\p{L}$`, "é", true},
		{`^.$`, "😀", true},
		{`^🐲*$`, "🐲🐲", true},
	}
	for _, c := range cases {
		re, err := Compile(c.pattern)
		if err != nil {
			if re, err = CompileAnnexB(c.pattern); err != nil {
				t.Errorf("%q: neither grammar accepts it", c.pattern)
				continue
			}
		}
		if got := re.MatchString(c.input); got != c.want {
			t.Errorf("%q.test(%q) = %v, want %v", c.pattern, c.input, got, c.want)
		}
	}
}

func TestConcurrentMatch(t *testing.T) {
	for name, compile := range map[string]func(string) (*Regexp, error){"u": Compile, "annexB": CompileAnnexB} {
		re, err := compile(`^(?<w>\w+)-\k<w>(?<=\d)$`)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for worker := range 16 {
			wg.Go(func() {
				for i := range 300 {
					input := strings.Repeat("a", 1+(worker+i)%7)
					if re.MatchString(input + "1-" + input) {
						t.Errorf("%s: unexpected match", name)
						return
					}
					if !re.MatchString(input + "1-" + input + "1") {
						t.Errorf("%s: %q should match", name, input)
						return
					}
				}
			})
		}
		wg.Wait()
	}
}

// TestSuitePatterns replays the patterns of the official JSON-Schema-Test-Suite (draft 2020-12 and draft 7) that Pi's `pattern` and `patternProperties` keywords hand to the regexp engine.
func TestSuitePatterns(t *testing.T) {
	type group struct {
		Schema map[string]any `json:"schema"`
		Tests  []struct {
			Description string `json:"description"`
			Data        any    `json:"data"`
			Valid       bool   `json:"valid"`
		} `json:"tests"`
	}
	matcher := func(pattern string) func(string) bool {
		// Pi's sharedRegex order: the u-mode grammar first, the Annex B grammar when that throws.
		re, err := Compile(pattern)
		if err != nil {
			if re, err = CompileAnnexB(pattern); err != nil {
				t.Fatalf("pattern %q does not compile: %v", pattern, err)
			}
		}
		return re.MatchString
	}
	checked := map[string]bool{}
	for _, draft := range []string{"draft2020-12", "draft7"} {
		for _, file := range []string{"optional/ecmascript-regex.json", "optional/non-bmp-regex.json", "pattern.json", "patternProperties.json"} {
			data, err := os.ReadFile("../../testdata/json-schema-test-suite/tests/" + draft + "/" + file)
			if err != nil {
				t.Fatal(err)
			}
			var groups []group
			if err := json.Unmarshal(data, &groups); err != nil {
				t.Fatalf("%s: %v", file, err)
			}
			for _, g := range groups {
				if pattern, ok := g.Schema["pattern"].(string); ok {
					match := matcher(pattern)
					checked[pattern] = true
					for _, test := range g.Tests {
						if s, ok := test.Data.(string); ok && match(s) != test.Valid {
							t.Errorf("%s/%s pattern %q: %s: match(%q) = %v", draft, file, pattern, test.Description, s, !test.Valid)
						}
					}
				}
				props, ok := g.Schema["patternProperties"].(map[string]any)
				if !ok || len(props) != 1 || g.Schema["additionalProperties"] != false {
					continue
				}
				// A single pattern with additionalProperties false: the object is valid exactly when every property name matches.
				for pattern := range props {
					match := matcher(pattern)
					checked[pattern] = true
					for _, test := range g.Tests {
						object, ok := test.Data.(map[string]any)
						if !ok {
							continue
						}
						all := true
						for key := range object {
							all = all && match(key)
						}
						if all != test.Valid {
							t.Errorf("%s/%s patternProperties %q: %s: all keys match = %v", draft, file, pattern, test.Description, all)
						}
					}
				}
			}
		}
	}
	for _, pattern := range []string{`^abc$`, `^\t$`, `^\cC$`, `^\cc$`, `^\d$`, `^\D$`, `^\w$`, `^\W$`, `^\s$`, `^\S$`, `\p{Letter}cole`, `\wcole`, `[a-z]cole`, `^\d+$`, `^\p{digit}+$`, `^🐲*$`, `^a*$`, `a+`, `^\p{Letter}+$`} {
		if !checked[pattern] {
			t.Errorf("suite pattern %q was not replayed", pattern)
		}
	}
}

// suiteTriples are the pattern, key and verdict triples of the patternProperties fixtures whose schema validates the values too, taken from the object keys of the fixture tests.
var suiteTriples = []matchCase{
	{`f.*o`, "foo", true},
	{`f.*o`, "foooooo", true},
	{`f.*o`, "fooooo", true},
	{`f.*o`, "bar", false},
	{`f.o`, "foo", true},
	{`f.o`, "fo", false},
	{`a*`, "a", true},
	{`a*`, "aaaa", true},
	{`a*`, "", true},
	{`aaa*`, "aaaa", true},
	{`aaa*`, "a", false},
	{`aaa*`, "aa", true},
	{`[0-9]{2,}`, "a31b", true},
	{`[0-9]{2,}`, "answer 1", false},
	{`X_`, "a_X_3", true},
	{`X_`, "a_x_3", false},
	{`f.*`, "foo", true},
	{`f.*`, "foobar", true},
	{`b.*`, "bar", true},
	{`b.*`, "foo", false},
	{`^.*bar$`, "foobar", true},
	{`^.*bar$`, "foobaz", false},
	{`^foo`, "foobar", true},
	{`^foo`, "afoo", false},
	{`^bar`, "bar", true},
	{`^bar$`, "foobar", false},
	{`^v`, "v1", true},
	{`^v`, "av", false},
	{`^á`, "ácido", true},
	{`^á`, "acido", false},
	{`^🐲*$`, "🐲", true},
	{`^🐲*$`, "🐉", false},
	{`^abc$`, "abc", true},
	{`^abc$`, "abc\n", false},
	{`x`, "xyz", true},
	{`^a+$`, "aaa", true},
	{`^a+$`, "", false},
	{`foo`, "afoob", true},
}

func TestSuiteTriples(t *testing.T) {
	runMatchCases(t, "u", Compile, suiteTriples)
	runMatchCases(t, "annexB", CompileAnnexB, suiteTriples)
}

// TestFormatRegex replays optional/format/regex.json: the regex format is `new RegExp(value, "u")` succeeding.
func TestFormatRegex(t *testing.T) {
	type group struct {
		Tests []struct {
			Description string `json:"description"`
			Data        any    `json:"data"`
			Valid       bool   `json:"valid"`
		} `json:"tests"`
	}
	replayed := 0
	for _, draft := range []string{"draft2020-12", "draft7"} {
		data, err := os.ReadFile("../../testdata/json-schema-test-suite/tests/" + draft + "/optional/format/regex.json")
		if err != nil {
			t.Fatal(err)
		}
		var groups []group
		if err := json.Unmarshal(data, &groups); err != nil {
			t.Fatal(err)
		}
		for _, g := range groups {
			for _, test := range g.Tests {
				if s, ok := test.Data.(string); ok {
					replayed++
					if got := Valid(s); got != test.Valid {
						t.Errorf("%s: Valid(%q) = %v: %s", draft, s, got, test.Description)
					}
				}
			}
		}
	}
	if replayed != 24 {
		t.Errorf("replayed %d string cases, want 24", replayed)
	}
	for source, want := range map[string]bool{
		`^\p{L}+$`:              true,
		`(?<a>x)\k<a>`:          true,
		`(?<=a)b(?!c)`:          true,
		`\u{1F600}`:             true,
		`[\d-z]`:                false,
		`a{`:                    false,
		`\_`:                    false,
		`\-`:                    false,
		`\a`:                    false,
		`x{1,2}{`:               false,
		`\1`:                    false,
		`(a)\2`:                 false,
		`\p{Foo}`:               false,
		`(?=a)*`:                false,
		`(?<n>a)(?<n>b)`:        false,
		`(?<n>a)|(?<n>b)`:       true,
		`^[^\s\S]$`:             true,
		`^(?:(a)|b)*\1$`:        true,
		`]`:                     false,
		`}`:                     false,
		`{`:                     false,
		`a{1,2}{3}`:             false,
		`[\u{1F600}-\u{1F64F}]`: true,
	} {
		if got := Valid(source); got != want {
			t.Errorf("Valid(%q) = %v, want %v", source, got, want)
		}
	}
}
