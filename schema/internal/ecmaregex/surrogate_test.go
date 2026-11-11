package ecmaregex

import (
	"os"
	"os/exec"
	"slices"
	"testing"
)

// lone returns the WTF-8 string of the code units, which a Go string holds for a JavaScript string that has lone surrogates.
func lone(units ...rune) string {
	var out []byte
	for _, u := range units {
		switch {
		case u >= 0xD800 && u <= 0xDFFF:
			out = append(out, byte(0xE0|u>>12), byte(0x80|u>>6&0x3F), byte(0x80|u&0x3F))
		default:
			out = append(out, string(u)...)
		}
	}
	return string(out)
}

func TestAppendUnits(t *testing.T) {
	cases := []struct {
		in   string
		want []rune
	}{
		{"", nil},
		{"abc", []rune{'a', 'b', 'c'}},
		{"é😀", []rune{'é', 0xD83D, 0xDE00}},
		{lone(0xD800), []rune{0xD800}},
		{lone(0xDFFF, 'a', 0xD83D), []rune{0xDFFF, 'a', 0xD83D}},
		{lone(0xD83D, 0xDE00), []rune{0xD83D, 0xDE00}},
		{"\xff\xed\xa0", []rune{0xFFFD, 0xFFFD, 0xFFFD}},
	}
	for _, c := range cases {
		if got := appendUnits(nil, c.in); !slices.Equal(got, c.want) {
			t.Errorf("appendUnits(%+q) = %x, want %x", c.in, got, c.want)
		}
	}
	if got := codePoints([]rune{0xD83D, 0xDE00, 0xDE00, 0xD83D, 'a', 0xD83D, 0xD83D, 0xDE00}); !slices.Equal(got, []rune{0x1F600, 0xDE00, 0xD83D, 'a', 0xD83D, 0x1F600}) {
		t.Errorf("codePoints = %x", got)
	}
}

type loneCase struct {
	pattern string
	input   string
	want    bool
}

// TestLoneSurrogateSubjects covers subjects that hold lone surrogates as WTF-8: with the u flag a lone surrogate is a character of its own, without it every unit is.
func TestLoneSurrogateSubjects(t *testing.T) {
	lead, trail := lone(0xD83D), lone(0xDE00)
	unicodeCases := []loneCase{
		{`^.$`, lead, true},
		{`^.$`, trail, true},
		{`^.$`, "😀", true},
		{`^..$`, lead + trail, false},
		{`^..$`, lone(0xD83D, 'a'), true},
		{`^.$`, lone(0xD83D, 0xDE00), true},
		{`^..$`, trail + lead, true},
		{`^[\ud800-\udfff]$`, lead, true},
		{`^[\ud800-\udfff]$`, trail, true},
		{`^[\ud800-\udfff]$`, "😀", false},
		{`^[\ud800-\udfff]+$`, trail + lead, true},
		{`^\ud83d$`, lead, true},
		{`^\ud83d$`, "😀", false},
		{`^\ude00$`, trail, true},
		{`^\ude00$`, "😀", false},
		{`\ud83d`, "😀", false},
		{`\ude00`, "😀", false},
		{`^\ud83d\ude00$`, "😀", true},
		{`^\ud83d\ude00$`, lead + trail, true},
		{`^\ud83d.$`, lead + trail, false},
		{`^\p{Cs}$`, lead, true},
		{`^\p{Cs}$`, "😀", false},
		{`^\P{Cs}$`, lead, false},
		{`^[^x]$`, lead, true},
		{`^\W$`, lead, true},
		{`^\S$`, trail, true},
		{`^\D\D$`, trail + lead, true},
		{`(?<=\ud83d)x`, lead + "x", true},
		{`(?<!\ud83d)x`, lead + "x", false},
		{`x(?=\ude00)`, "x" + trail, true},
		{`^(.)\1$`, lead + lead, true},
		{`^(.)\1$`, lead + trail, false},
		{`^.{2}$`, lone(0xD83D, 0xD83D), true},
	}
	for _, c := range unicodeCases {
		re, err := Compile(c.pattern)
		if err != nil {
			t.Errorf("Compile(%+q): %v", c.pattern, err)
			continue
		}
		if got := re.MatchString(c.input); got != c.want {
			t.Errorf("u %+q.test(%+q) = %v, want %v", c.pattern, c.input, got, c.want)
		}
	}
	annexBCases := []loneCase{
		{`^.$`, lead, true},
		{`^..$`, "😀", true},
		{`^..$`, lead + trail, true},
		{`^.$`, lead + trail, false},
		{`^[\ud800-\udfff]{2}$`, lead + trail, true},
		{`^\ud83d$`, lead, true},
		{`^\ud83d`, "😀", true},
		{`\ude00$`, "😀", true},
	}
	for _, c := range annexBCases {
		re, err := CompileAnnexB(c.pattern)
		if err != nil {
			t.Errorf("CompileAnnexB(%+q): %v", c.pattern, err)
			continue
		}
		if got := re.MatchString(c.input); got != c.want {
			t.Errorf("annexB %+q.test(%+q) = %v, want %v", c.pattern, c.input, got, c.want)
		}
	}
}

// TestLoneSurrogatePatterns reads a pattern that holds lone surrogates as WTF-8.
func TestLoneSurrogatePatterns(t *testing.T) {
	lead, trail := lone(0xD83D), lone(0xDE00)
	cases := []struct {
		mode    string
		pattern string
		input   string
		want    bool
	}{
		// With the u flag a lone surrogate in the pattern is a character, and a lead followed by a trail is one code point.
		{"u", lead, lead, true},
		{"u", lead, "😀", false},
		{"u", lead + trail, "😀", true},
		{"u", "^" + lead + trail + "$", lead + trail, true},
		{"u", "[" + lead + trail + "]", "😀", true},
		{"u", "^[" + lead + trail + "]$", "😀", true},
		{"u", "^[" + lead + "-" + trail + "]$", "😀", false},
		{"u", "^[" + trail + lead + "]+$", trail + lead, true},
		// Without it every unit is a character.
		{"annexB", lead, "😀", true},
		{"annexB", trail, "😀", true},
		{"annexB", lead + trail, "😀", true},
		{"annexB", "^" + lead + trail + "+$", "😀😀", false},
		{"annexB", "^(?:" + lead + trail + ")+$", "😀😀", true},
	}
	for _, c := range cases {
		compile := Compile
		if c.mode == "annexB" {
			compile = CompileAnnexB
		}
		re, err := compile(c.pattern)
		if err != nil {
			t.Errorf("%s %+q: %v", c.mode, c.pattern, err)
			continue
		}
		if got := re.MatchString(c.input); got != c.want {
			t.Errorf("%s %+q.test(%+q) = %v, want %v", c.mode, c.pattern, c.input, got, c.want)
		}
	}
}

// TestDistinctSurrogateSets guards against regexp2 sharing one table entry between sets of surrogates, which it keys by a string where each surrogate is U+FFFD.
func TestDistinctSurrogateSets(t *testing.T) {
	cases := []loneCase{
		{`^(?:\ud83d\ude00|\ud83d\ude01)$`, "😁", true},
		{`^(?:\ud83d\ude00|\ud83e\ude00)$`, "🨀", true},
		{`^[\ud83d\ud83e][\ude00-\ude4f]$`, "🨀", true},
		{`^[\ud83e][\ude00]|^[\ud83d][\ude01]$`, "😁", true},
	}
	for _, c := range cases {
		re, err := CompileAnnexB(c.pattern)
		if err != nil {
			t.Fatal(err)
		}
		if got := re.MatchString(c.input); got != c.want {
			t.Errorf("%+q.test(%+q) = %v, want %v", c.pattern, c.input, got, c.want)
		}
	}
}

// TestErrorText checks the message of a SyntaxError: V8's text with the pattern and the flags.
func TestErrorText(t *testing.T) {
	cases := []struct {
		pattern, unicode, annexB string
	}{
		{"(", "Invalid regular expression: /(/u: Unterminated group", "Invalid regular expression: /(/: Unterminated group"},
		{")", "Invalid regular expression: /)/u: Unmatched ')'", "Invalid regular expression: /)/: Unmatched ')'"},
		{`\`, `Invalid regular expression: /\/u: \ at end of pattern`, `Invalid regular expression: /\/: \ at end of pattern`},
		{"*", "Invalid regular expression: /*/u: Nothing to repeat", "Invalid regular expression: /*/: Nothing to repeat"},
		{"a{", "Invalid regular expression: /a{/u: Incomplete quantifier", ""},
		{"{", "Invalid regular expression: /{/u: Lone quantifier brackets", ""},
		{"a{2,1}", "Invalid regular expression: /a{2,1}/u: numbers out of order in {} quantifier", "Invalid regular expression: /a{2,1}/: numbers out of order in {} quantifier"},
		{"[b-a]", "Invalid regular expression: /[b-a]/u: Range out of order in character class", "Invalid regular expression: /[b-a]/: Range out of order in character class"},
		{"[a", "Invalid regular expression: /[a/u: Unterminated character class", "Invalid regular expression: /[a/: Unterminated character class"},
		{`\p{Foo}`, "Invalid regular expression: /\\p{Foo}/u: Invalid property name", ""},
		{`[\p{Foo}]`, "Invalid regular expression: /[\\p{Foo}]/u: Invalid property name in character class", ""},
		{`\_`, "Invalid regular expression: /\\_/u: Invalid escape", ""},
		{`(?<n>a)\k<m>`, "Invalid regular expression: /(?<n>a)\\k<m>/u: Invalid named capture referenced", "Invalid regular expression: /(?<n>a)\\k<m>/: Invalid named capture referenced"},
		{`(?<n>a)(?<n>b)`, "Invalid regular expression: /(?<n>a)(?<n>b)/u: Duplicate capture group name", "Invalid regular expression: /(?<n>a)(?<n>b)/: Duplicate capture group name"},
		{`(?<1>a)`, "Invalid regular expression: /(?<1>a)/u: Invalid capture group name", "Invalid regular expression: /(?<1>a)/: Invalid capture group name"},
		{`(?`, "Invalid regular expression: /(?/u: Invalid group", "Invalid regular expression: /(?/: Invalid group"},
		{`(?ii:a)`, "Invalid regular expression: /(?ii:a)/u: Repeated flag in flag group", "Invalid regular expression: /(?ii:a)/: Repeated flag in flag group"},
		{`(?-:a)`, "Invalid regular expression: /(?-:a)/u: Invalid flag group", "Invalid regular expression: /(?-:a)/: Invalid flag group"},
		{`(?i--:a)`, "Invalid regular expression: /(?i--:a)/u: Multiple dashes in flag group", "Invalid regular expression: /(?i--:a)/: Multiple dashes in flag group"},
		{`[\d-a]`, "Invalid regular expression: /[\\d-a]/u: Invalid character class", ""},
		{`(?<=a)*`, "Invalid regular expression: /(?<=a)*/u: Invalid quantifier", "Invalid regular expression: /(?<=a)*/: Invalid quantifier"},
		{`\u{110000}`, "Invalid regular expression: /\\u{110000}/u: Invalid Unicode escape", ""},
		{`\1`, "Invalid regular expression: /\\1/u: Invalid escape", ""},
		{`\00`, "Invalid regular expression: /\\00/u: Invalid decimal escape", ""},
	}
	for _, c := range cases {
		if _, err := Compile(c.pattern); err == nil || err.Error() != c.unicode {
			t.Errorf("Compile(%+q) error = %v, want %q", c.pattern, err, c.unicode)
		}
		if c.annexB == "" {
			continue
		}
		if _, err := CompileAnnexB(c.pattern); err == nil || err.Error() != c.annexB {
			t.Errorf("CompileAnnexB(%+q) error = %v, want %q", c.pattern, err, c.annexB)
		}
	}
}

// TestNoPackageInitTables runs a fresh process to see that importing the package builds no lookup table, and that the first \p builds some.
func TestNoPackageInitTables(t *testing.T) {
	if os.Getenv("ECMAREGEX_TABLES_CHILD") == "" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestNoPackageInitTables$")
		cmd.Env = append(os.Environ(), "ECMAREGEX_TABLES_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fresh process: %v\n%s", err, out)
		}
		return
	}
	if n := tablesBuilt.Load(); n != 0 {
		t.Fatalf("%d lookup tables were built before any pattern was compiled", n)
	}
	if _, err := Compile(`^[a-z]+\d*$`); err != nil {
		t.Fatal(err)
	}
	if n := tablesBuilt.Load(); n != 0 {
		t.Fatalf("%d lookup tables were built for a pattern without a property", n)
	}
	if _, err := Compile(`\p{Script=Latn}\p{Emoji}`); err != nil {
		t.Fatal(err)
	}
	if tablesBuilt.Load() == 0 {
		t.Fatal("a property pattern built no table")
	}
}

func BenchmarkMatchString(b *testing.B) {
	long := ""
	for len(long) < 200 {
		long += "the quick brown fox "
	}
	long = long[:199] + "!"
	cases := []struct {
		name    string
		compile func(string) (*Regexp, error)
		pattern string
		input   string
	}{
		{"short ASCII", Compile, `^[a-z]+-\d+$`, "item-42"},
		{"200 byte ASCII", Compile, `fox!$`, long},
		{"non-ASCII", Compile, `^\p{L}+ \p{L}+$`, "héllo wörld"},
		{"AnnexB", CompileAnnexB, `^[a-z]+-\d+$`, "item-42"},
		{"AnnexB non-ASCII", CompileAnnexB, `ö.*d$`, "héllo wörld"},
	}
	for _, c := range cases {
		re, err := c.compile(c.pattern)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				re.MatchString(c.input)
			}
		})
	}
}

// TestModifierGroups covers the syntax and the effect of the (?ims-ims:...) groups of ES2025.
func TestModifierGroups(t *testing.T) {
	for source, valid := range map[string]bool{
		`(?i:a)`: true, `(?-i:a)`: true, `(?m-s:a)`: true, `(?i-m:a)`: true, `(?s:.)`: true, `(?m:^a)`: true, `(?ims:a)`: true,
		`(?ims-:a)`: true, `(?i-:a)`: true, `(?-ims:a)`: true, `(?i:)`: true, `(?i:a)*`: true, `(?i:a){2}`: true,
		`(?i-i:a)`: false, `(?ii:a)`: false, `(?-:a)`: false, `(?i--m:a)`: false, `(?x:a)`: false, `(?i)`: false, `(?i`: false,
		`(?I:a)`: false, `(?u:a)`: false, `(? i:a)`: false, `(?i-s-:a)`: false, `(?i=a)`: false,
	} {
		if got := Valid(source); got != valid {
			t.Errorf("Valid(%q) = %v, want %v", source, got, valid)
		}
		if _, err := CompileAnnexB(source); (err == nil) != valid {
			t.Errorf("CompileAnnexB(%q): err = %v, want accepted = %v", source, err, valid)
		}
	}
	cases := []matchCase{
		{`^(?i:abc)$`, "aBc", true},
		{`^a(?i:b)c$`, "aBc", true},
		{`^a(?i:b)c$`, "ABc", false},
		{`^(?i:a)(?-i:b)$`, "Ab", true},
		{`^(?i:a)(?-i:b)$`, "AB", false},
		{`^(?i:(?-i:a))$`, "A", false},
		{`^(?i:[a-c]+)$`, "aBC", true},
		{`^(?i:[^a-c])$`, "B", false},
		{`^(?i:\u00e9)$`, "É", true},
		{`^(?i:\u03c3)$`, "ς", true},
		{`^(?i:\u03c3)$`, "Σ", true},
		{`^(?i:\u212a)$`, "\u212a", true},
		{`^(?i:(a)\1)$`, "aA", true},
		{`^(?s:.)$`, "\n", true},
		{`^.$`, "\n", false},
		{`^(?-s:.)$`, "\n", false},
		{`^(?m:a$)\nb`, "a\nb", true},
		{`(?m:^b)`, "a\nb", true},
		{`^b`, "a\nb", false},
		{`(?m:a$)`, "a\u2028b", true},
		{`a$`, "a\nb", false},
		// With the u flag the case folding is the simple case folding and \w gains the two characters that fold onto it.
		{`^(?i:\w)$`, "\u017f", true},
		{`^(?i:\W)$`, "\u017f", false},
		{`^(?i:[^k])$`, "\u212a", false},
		{`^(?i:\u{10400})$`, "\U00010428", true},
	}
	for _, c := range cases {
		re, err := Compile(c.pattern)
		if err != nil {
			t.Errorf("Compile(%q): %v", c.pattern, err)
			continue
		}
		if got := re.MatchString(c.input); got != c.want {
			t.Errorf("u %q.test(%q) = %v, want %v", c.pattern, c.input, got, c.want)
		}
	}
	// V8 reads a string of code units up to U+00FF as one-byte and then misses a literal above U+00FF in a case-insensitive group.
	for _, c := range []matchCase{{`(?i:\u017f)`, "s", false}, {`(?i:\u017f)`, "s\u0100", true}, {`(?i:\u017f)`, "\u017f", true}, {`(?i:[\u017f])`, "s", true}} {
		re, err := Compile(c.pattern)
		if err != nil {
			t.Fatal(err)
		}
		if got := re.MatchString(c.input); got != c.want {
			t.Errorf("u %q.test(%q) = %v, want %v", c.pattern, c.input, got, c.want)
		}
	}
}
