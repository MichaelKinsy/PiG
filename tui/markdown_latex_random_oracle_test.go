package tui

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Random paragraphs over the characters the `$`, `\(` and `\[` rules read (the digit, space, backtick, identifier and escape
// rules around a closing `$`, multibyte text on both sides of a span) render as the vendored pi-tui renders them.
func TestMarkdownInlineMathRandomMatchesPiOracle(t *testing.T) {
	pieces := []string{"$", "$", "$$", "\\", "\\$", "\\(", "\\)", "\\[", "\\]", " ", " ", "a", "b", "X", "HOME", "_", "5", "12", "x^2", "+", "-", "=", "`", "é", "ü", "\u00a0", "\u3000", ",", ".", ":", "*", "(", ")", "[", "]", "|", "\n"}
	rng := rand.New(rand.NewPCG(9, 90))
	type probe struct {
		Text  string `json:"text"`
		Width int    `json:"width"`
	}
	var probes []probe
	for range 600 {
		var b strings.Builder
		for range 2 + rng.IntN(22) {
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
		probes = append(probes, probe{b.String(), 40})
	}
	var want [][]string
	pioracle.Run(t, `
const tui = await load("../pi-tui.mjs");
tui.setCapabilities({ images: null, trueColor: true, hyperlinks: false });
const tag = (name) => (text) => "<" + name + ">" + text + "</" + name + ">";
const theme = { heading: tag("h"), link: tag("a"), linkUrl: tag("u"), code: tag("c"), codeBlock: tag("cb"), codeBlockBorder: tag("cbb"), quote: tag("q"), quoteBorder: tag("qb"), hr: tag("hr"), listBullet: tag("lb"), bold: tag("b"), italic: tag("i"), strikethrough: tag("s"), underline: tag("ul") };
emit(input.map((p) => new tui.Markdown(p.text, 0, 0, theme).render(p.width)));`, probes, &want)
	tag := func(name string) func(string) string {
		return func(text string) string { return "<" + name + ">" + text + "</" + name + ">" }
	}
	theme := MarkdownTheme{
		Heading: tag("h"), Link: tag("a"), LinkUrl: tag("u"), Code: tag("c"), CodeBlock: tag("cb"), CodeBlockBorder: tag("cbb"),
		Quote: tag("q"), QuoteBorder: tag("qb"), Hr: tag("hr"), ListBullet: tag("lb"), Bold: tag("b"), Italic: tag("i"), Strikethrough: tag("s"), Underline: tag("ul"),
	}
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	t.Cleanup(ResetCapabilitiesCache)
	failures := 0
	for i, p := range probes {
		got := NewMarkdownWithOptions(p.Text, 0, 0, &theme, nil, nil).Render(p.Width)
		if !reflect.DeepEqual(nonNil(got), nonNil(want[i])) {
			failures++
			if failures <= 10 {
				t.Errorf("markdown %q at width %d:\n Go %q\n Pi %q", p.Text, p.Width, got, want[i])
			}
		}
	}
	if failures > 0 {
		t.Fatalf("%d of %d random paragraphs differ from Pi", failures, len(probes))
	}
}
