package export

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's ansiToHtml (core/export-html/ansi-to-html.js) runs against the vendored Pi module over hand-picked and seeded random SGR sequences: extended colors with missing or oversized parameters, empty parameters, leading zeros, large numbers (up to 65536; parameters beyond int64 are a known gap: Pi keeps a double, Go reads 0), unterminated sequences, HTML-special text and astral characters.
func TestAnsiToHTMLMatchesPiOracle(t *testing.T) {
	inputs := []string{
		"", "plain", "<b>&\"'", "\x1b[0m", "\x1b[m", "\x1b[1mbold\x1b[22m", "\x1b[31mred\x1b[0m tail", "\x1b[38;5;196mx", "\x1b[38;5;256mx", "\x1b[38;5;999mx",
		"\x1b[38;5mx", "\x1b[38;5;mx", "\x1b[38;2;1;2;3mx", "\x1b[38;2;1;2mx", "\x1b[38;2;256;-1;3mx", "\x1b[48;2;300;300;300mx", "\x1b[1;;2mx", "\x1b[;mx",
		"\x1b[007;031mx", "\x1b[1;4;3;2mall\x1b[24;23;22mnone", "\x1b[91;102mx", "\x1b[39;49mx", "\x1b[1", "\x1b[1;", "\x1b[1x", "\x1b[abc", "\x1b[\x1b[1mx", "\x1b[1m\x1b[2m\x1b[0m", "\x1b[31m\x1b[0m",
		"\x1b[38;5;16mx\x1b[38;5;231mx\x1b[38;5;232mx\x1b[38;5;255mx", "\x1b[0;38;5;1mx", "\x1b[38;5;15;1mx", "\x1b[38;5;1;38;5;2mx", "emoji \U0001f600 <\x1b[32m\U0001f600\x1b[0m>", "\x1b]0;title\x07x", "\x1b[?25lx", "\x1b[2Jx",
		"\x1b[+1mx", "\x1b[-1mx", "\x1b[1.5mx", "\x1b[1 mx", "\x1b[０mx", "\x1b[1m\n\x1b[0m", "a\x1b[1mb\x1b[0mc\x1b[1md",
	}
	rng := rand.New(rand.NewSource(3))
	tokens := []string{"0", "1", "2", "3", "4", "22", "23", "24", "30", "37", "38", "39", "40", "47", "48", "49", "90", "97", "100", "107", "5", "2", "255", "256", "16", "231", "232", "", "007", "65536", "9"}
	words := []string{"a", " ", "<", "&", "\"", "'", "é", "\U0001f600", "\n"}
	for range 400 {
		var b strings.Builder
		for range rng.Intn(6) {
			b.WriteString(words[rng.Intn(len(words))])
			b.WriteString("\x1b[")
			for k := range rng.Intn(7) {
				if k > 0 {
					b.WriteByte(';')
				}
				b.WriteString(tokens[rng.Intn(len(tokens))])
			}
			if rng.Intn(10) != 0 {
				b.WriteByte('m')
			}
		}
		b.WriteString(words[rng.Intn(len(words))])
		inputs = append(inputs, b.String())
	}
	var want []string
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/export-html/ansi-to-html.js");
emit(input.map((s) => mod.ansiToHtml(s)));`, inputs, &want)
	mismatches := 0
	for i, input := range inputs {
		if got := ansiToHTML(input); got != want[i] {
			if mismatches++; mismatches <= 8 {
				t.Errorf("%q:\n Go %q\n Pi %q", input, got, want[i])
			}
		}
	}
	if mismatches > 8 {
		t.Errorf("%d mismatches in %d inputs", mismatches, len(inputs))
	}
}
