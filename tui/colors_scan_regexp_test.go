package tui

import (
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
)

// The regular expressions PiG used to translate upstream's patterns (colors.ts HEX, OKLCH_PATTERN, OKHSL_PATTERN), kept as the oracle for the scanners that replaced them. The keywords are spelled out because RE2's (?i) also folds U+212A into "k", which a JavaScript /i pattern does not.
const (
	oracleColorNumber  = `[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?`
	oracleColorSpace   = `[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`
	oracleColorDegrees = `(?:[dD][eE][gG])?`
)

var (
	oracleHexPattern   = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)
	oracleOklchPattern = regexp.MustCompile(`^[oO][kK][lL][cC][hH]\(` + oracleColorSpace + `*(` + oracleColorNumber + `)(%)?` + oracleColorSpace + `+(` + oracleColorNumber + `)` + oracleColorSpace + `+(` + oracleColorNumber + `)` + oracleColorDegrees + oracleColorSpace + `*\)$`)
	oracleOkhslPattern = regexp.MustCompile(`^[oO][kK][hH][sS][lL]\(` + oracleColorSpace + `*(` + oracleColorNumber + `)` + oracleColorDegrees + oracleColorSpace + `+(` + oracleColorNumber + `)(%)?` + oracleColorSpace + `+(` + oracleColorNumber + `)(%)?` + oracleColorSpace + `*\)$`)
)

// checkColorScanners compares the scanners with the regular expressions on one string: the same match and the same captured numbers and percent signs.
func checkColorScanners(t *testing.T, value string) {
	t.Helper()
	digits, ok := colorHexDigits(value)
	if hex := oracleHexPattern.FindStringSubmatch(value); (hex != nil) != ok || ok && hex[1] != digits {
		t.Fatalf("hex %q: scanner (%q, %v), pattern %q", value, digits, ok, hex)
	}
	oklch, ok := scanColorFunction(value, "oklch", 2)
	if want := oracleOklchPattern.FindStringSubmatch(value); (want != nil) != ok || ok && (oklch[0].number != want[1] || oklch[0].percent != (want[2] != "") || oklch[1].number != want[3] || oklch[2].number != want[4]) {
		t.Fatalf("oklch %q: scanner (%+v, %v), pattern %q", value, oklch, ok, want)
	}
	okhsl, ok := scanColorFunction(value, "okhsl", 0)
	if want := oracleOkhslPattern.FindStringSubmatch(value); (want != nil) != ok || ok && (okhsl[0].number != want[1] || okhsl[1].number != want[2] || okhsl[1].percent != (want[3] != "") || okhsl[2].number != want[4] || okhsl[2].percent != (want[5] != "")) {
		t.Fatalf("okhsl %q: scanner (%+v, %v), pattern %q", value, okhsl, ok, want)
	}
}

// colorPieces are the fragments the generated strings are assembled from: every token of the three patterns, near misses, whitespace inside and outside JavaScript's \s, and invalid UTF-8.
var colorPieces = []string{
	"oklch(", "OKLCH(", "OkLcH(", "okhsl(", "OKHSL(", "okhsl", "oklch", "ok", "lch(", "\u212alch(", "(", ")", ")", ")", " )",
	"#", "#", "a", "F", "0", "9", "g", "G",
	"0", "1", "12", "0.5", ".5", "5.", ".", "-", "+", "+-", "e", "E", "e5", "E-3", "e+", "1e", "1e+2", "00", "360", "-0",
	"%", "%%", "deg", "DEG", "dEg", "de", "degg", "d",
	" ", " ", "  ", "\t", "\n", "\v", "\f", "\r", "\u00a0", "\u1680", "\u2000", "\u200a", "\u200b", "\u2028", "\u2029", "\u202f", "\u205f", "\u3000", "\ufeff", "\u0085", "\x00", "\xff", "\xe2\x80",
}

// colorTokens is the token stream of a color function close to the patterns: a name, the arguments with optional units and JavaScript whitespace around them, and the closing parenthesis.
func colorTokens(random *rand.Rand) []string {
	pick := func(options ...string) string { return options[random.IntN(len(options))] }
	space := func(required bool) string {
		var out strings.Builder
		if required {
			out.WriteString(pick(" ", "\t", "\u3000", "\ufeff", "\u2028", "\u00a0", "\u200a"))
		}
		for random.IntN(3) == 0 {
			out.WriteString(pick(" ", "\n", "\v", "\f", "\r", "\u1680", "\u2000", "\u202f", "\u205f", "\u2029"))
		}
		return out.String()
	}
	number := func() string {
		return pick("", "", "+", "-") + pick("0", "1", "12", "0.5", ".5", "5.", "360") + pick("", "", "", "e5", "E-3", "e+2")
	}
	tokens := []string{pick("oklch", "OKLCH", "okhsl", "OkHsL"), "(", space(false)}
	for i := range 3 {
		if i > 0 {
			tokens = append(tokens, space(true))
		}
		tokens = append(tokens, number(), pick("", "", "%", "deg", "DEG"))
	}
	return append(tokens, space(false), ")")
}

// Strings close to the patterns, and the same strings with one token dropped, doubled or replaced by a fragment, match the scanners exactly as they match the regular expressions. Both matching and rejected strings are produced in bulk.
func TestColorScannersMatchTheirRegularExpressions(t *testing.T) {
	values := []string{
		"", "#", "#abc", "#ABCDEF", "#abcd", "#abcdeg", "#abcdef ", " #abc", "#\u00e9bc",
		"oklch(0.5 0.1 120)", "oklch(50% 0.1 120deg)", "OKLCH( .5\u3000+1e-1\t-2.5E+2DEG\ufeff)", "oklch(1. 2. 3.)", "oklch(1e 2 3)", "oklch(1 2 3)x", "oklch(1 2 3 4)", "oklch(1%2 3)", "oklch(1 2% 3)", "oklch(1 2 3deg%)", "oklch(1 2 3\u0085)",
		"okhsl(120 50% 40%)", "okhsl(120deg 0.5 0.4)", "okhsl(120% 0.5 0.4)", "okhsl(1 2 3deg)", "okhsl(+.5e+2deg\u2028.1%\u00a0.2%)", "\u212aokhsl(1 2 3)", "okhsl(1 2 3", "okhsl 1 2 3)",
	}
	for _, value := range values {
		checkColorScanners(t, value)
	}
	random := rand.New(rand.NewPCG(1, 2))
	var matched, rejected int
	for range 200_000 {
		tokens := colorTokens(random)
		if random.IntN(2) == 0 {
			at := random.IntN(len(tokens))
			switch random.IntN(3) {
			case 0:
				tokens = append(tokens[:at], tokens[at+1:]...)
			case 1:
				tokens = append(tokens[:at+1], tokens[at:]...)
			default:
				tokens[at] = colorPieces[random.IntN(len(colorPieces))]
			}
		}
		value := strings.Join(tokens, "")
		checkColorScanners(t, value)
		if oracleOklchPattern.MatchString(value) || oracleOkhslPattern.MatchString(value) {
			matched++
		} else {
			rejected++
		}
	}
	for range 50_000 {
		var value strings.Builder
		value.WriteString([]string{"#", "", "oklch(", "okhsl("}[random.IntN(4)])
		for range random.IntN(10) {
			value.WriteString(colorPieces[random.IntN(len(colorPieces))])
		}
		checkColorScanners(t, value.String())
	}
	if matched < 20_000 || rejected < 20_000 {
		t.Fatalf("%d generated strings matched and %d did not: the generator no longer exercises both outcomes", matched, rejected)
	}
}

func FuzzColorScannersMatchTheirRegularExpressions(f *testing.F) {
	for _, piece := range []string{"oklch(50% 0.1 120deg)", "okhsl(120 50% 40%)", "#abc", "oklch(1e 2 3)", "okhsl(.5\u3000.1%\t.2%\ufeff)"} {
		f.Add(piece)
	}
	f.Fuzz(checkColorScanners)
}
