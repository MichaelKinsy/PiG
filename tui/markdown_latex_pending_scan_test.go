package tui

import (
	"math/rand"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// The pending-math fallbacks of tokenizeBlockLatex read the rest of the document once per display-math opener, so they
// must not cost a regexp pass and a line split over all of it. These tests keep the scans equal to Pi's source forms.
var (
	pendingDollarOracle  = regexp.MustCompile(`(?s)^ {0,3}\$\$[ \t]*(?:\n)?(.*)$`)
	pendingBracketOracle = regexp.MustCompile(`(?s)^ {0,3}\\\[[ \t]*(?:\n)?(.*)$`)
)

func containsTableStartOracle(source string) bool {
	lines := strings.Split(source, "\n")
	for index := 0; index < len(lines)-1; index++ {
		delimiterRow := lines[index+1]
		if (strings.Contains(delimiterRow, "|") || strings.Contains(lines[index], "|")) && reMdTableDelimiterRow.MatchString(delimiterRow) {
			return true
		}
	}
	return false
}

func TestMarkdownPendingMathScansMatchTheirRegexpForms(t *testing.T) {
	pieces := []string{" ", "  ", "    ", "\t", "\n", "$$", "$", "\\[", "\\", "[", "|", "---", ":---:", "-", " | ", "a", "x_1", "+", "\r"}
	rng := rand.New(rand.NewSource(11159))
	for range 20000 {
		var sb strings.Builder
		for range rng.Intn(14) {
			sb.WriteString(pieces[rng.Intn(len(pieces))])
		}
		source := sb.String()
		for _, c := range []struct {
			opener string
			oracle *regexp.Regexp
		}{{"$$", pendingDollarOracle}, {`\[`, pendingBracketOracle}} {
			m := c.oracle.FindStringSubmatch(source)
			body, ok := mdPendingBody(source, c.opener)
			if (m != nil) != ok || (ok && body != m[1]) {
				t.Fatalf("mdPendingBody(%q, %q) = %q, %t; regexp %q", source, c.opener, body, ok, m)
			}
		}
		if got, want := containsTableStart(source), containsTableStartOracle(source); got != want {
			t.Fatalf("containsTableStart(%q) = %t, split form %t", source, got, want)
		}
	}
}

// tokenizeBlockLatexOracle is tokenizeBlockLatex as markdown.ts writes it: the four regexp forms in order, no opener guard.
func tokenizeBlockLatexOracle(source string) (latexToken, bool) {
	if m := reMdBlockDollar.FindStringSubmatch(source); m != nil && m[1] != "" && !containsTableStartOracle(m[1]) {
		return latexToken{raw: m[0], text: widthx.JSTrim(m[1])}, true
	}
	if m := reMdBlockBracket.FindStringSubmatch(source); m != nil && m[1] != "" && !containsTableStartOracle(m[1]) {
		return latexToken{raw: m[0], text: widthx.JSTrim(m[1])}, true
	}
	if m := pendingBracketOracle.FindStringSubmatch(source); m != nil && m[1] != "" && !containsTableStartOracle(m[1]) {
		return latexToken{raw: m[0], text: m[1], pending: true}, true
	}
	if m := pendingDollarOracle.FindStringSubmatch(source); m != nil && m[1] != "" && looksLikePendingDollarMath(m[1]) && !containsTableStartOracle(m[1]) {
		return latexToken{raw: m[0], text: m[1], pending: true}, true
	}
	return latexToken{}, false
}

func TestTokenizeBlockLatexOpenerGuardMatchesTheFourRegexpForms(t *testing.T) {
	pieces := []string{" ", "   ", "    ", "\n", "\n\n", "$$", "$", "\\[", "\\]", "\\", "|", "---", " | ", "a", "x_1", "=", "+", "\t"}
	rng := rand.New(rand.NewSource(11160))
	for range 30000 {
		var sb strings.Builder
		for range rng.Intn(16) {
			sb.WriteString(pieces[rng.Intn(len(pieces))])
		}
		source := sb.String()
		got, gotOK := tokenizeBlockLatex(source)
		want, wantOK := tokenizeBlockLatexOracle(source)
		if gotOK != wantOK || got != want {
			t.Fatalf("tokenizeBlockLatex(%q) = %+v, %t; the regexp forms give %+v, %t", source, got, gotOK, want, wantOK)
		}
	}
}
