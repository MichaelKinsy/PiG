package tui

import (
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
)

// listItemRegexp is the pattern parseMarkdownListItem implements.
var listItemRegexp = regexp.MustCompile(`^( *)([-+*]|\d{1,9}[.)])(?:[ \t]+(.*)|$)`)

func checkListItemAgainstRegexp(t *testing.T, line string) {
	t.Helper()
	indent, marker, body, ok := parseMarkdownListItem(line)
	match := listItemRegexp.FindStringSubmatch(line)
	if ok != (match != nil) || ok && (indent != match[1] || marker != match[2] || body != match[3]) {
		t.Fatalf("parseMarkdownListItem(%q) = %q, %q, %q, %v; the regexp gives %q", line, indent, marker, body, ok, match)
	}
}

func TestParseMarkdownListItemEqualsItsRegexp(t *testing.T) {
	alphabet := []string{" ", "\t", "-", "+", "*", "1", "0", "9", ".", ")", "/", ":", "a", "\n", "\r", "\v", "\f", "\u0085", "\u00a0", "\u2028", "\xff", "é", "#"}
	var all func(prefix string, n int)
	all = func(prefix string, n int) {
		checkListItemAgainstRegexp(t, prefix)
		if n == 0 {
			return
		}
		for _, s := range alphabet {
			all(prefix+s, n-1)
		}
	}
	all("", 4)
	rng := rand.New(rand.NewPCG(3, 5))
	for range 200000 {
		var b strings.Builder
		b.WriteString(strings.Repeat(" ", rng.IntN(6)))
		b.WriteString(strings.Repeat("7", rng.IntN(12)))
		for range rng.IntN(10) {
			b.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		checkListItemAgainstRegexp(t, b.String())
	}
}
