package tui

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// markdownRandomEmphasis are inline strings of three to fourteen pieces drawn from a seeded generator: runs of the emphasis delimiters ("*", "_", "~"),
// letters, digits, spaces, line breaks, ASCII and CJK punctuation, CJK and accented letters, code spans, escapes and carriage returns. They reach the
// left-flanking, right-flanking, intraword, rule-of-three and CJK-adjacent branches of marked's emStrong and the nesting of the stacked inline writer.
func markdownRandomEmphasis(seed uint64, count int) []string {
	rng := rand.New(rand.NewPCG(seed, seed^0xbf58476d1ce4e5b9))
	pieces := []string{"*", "**", "***", "****", "_", "__", "___", "~~", "~", "a", "b", "ab", "x1", "7", " ", "  ", "\n", "\r\n", "\r", ".", "!", "(", ")", "-", "日", "本語", "。", "、", "「", "」", "é", "`c`", "`", "\\*", "[l](u)", "\x1b[22m", "\x1b[0m"}
	docs := make([]string, 0, count)
	for range count {
		var b strings.Builder
		for range 3 + rng.IntN(12) {
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
		docs = append(docs, b.String())
	}
	return docs
}

// TestMarkdownRandomEmphasisMatchesPi renders markdownRandomEmphasis as a paragraph, a heading, a quote and a list item, in the stock themes and under the default text styles, at a narrow and a wide width.
func TestMarkdownRandomEmphasisMatchesPi(t *testing.T) {
	contexts := []string{"%s", "# %s", "> %s", "- %s", "| h |\n|---|\n| %s |"}
	var probes []markdownContractProbe
	for i, inline := range markdownRandomEmphasis(20260302, 20000) {
		probe := markdownContractProbe{Text: strings.ReplaceAll(contexts[i%len(contexts)], "%s", inline), PaddingX: i / len(contexts) % 2, Theme: "tagged", Ops: markdownContractRenderOps(7, 80)}
		switch i / len(contexts) % 4 {
		case 1:
			probe.Theme = "dark"
		case 2:
			probe.Style = &markdownContractStyle{Color: true, Italic: true}
		case 3:
			probe.Theme, probe.Style = "light", &markdownContractStyle{Bold: true, Strikethrough: true}
		}
		probes = append(probes, probe)
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// markdownOrphanDelimiterDocs are inputs for the closing-delimiter rule's first alternative, which skips an orphan delimiter inside the other delimiter's
// strong span (`^[^_*]*?OO[^_*]*?D[^_*]*?(?=OO)`): the other character twice, the delimiter once, the other character twice again, with only other characters
// between, at the end of the text and followed by more text, with a single other character where two are needed, and for both delimiters.
func markdownOrphanDelimiterDocs() []string {
	var docs []string
	for _, pair := range [][2]string{{"*", "_"}, {"_", "*"}} {
		d, o := pair[0], pair[1]
		for _, form := range []string{
			"%[1]sabc%[2]s%[2]sdef%[1]sghi%[2]s%[2]s",
			"%[1]sabc%[2]s%[2]sdef%[1]sghi%[2]s%[2]s x",
			"%[1]sabc%[2]s%[2]sdef%[1]sghi%[2]s%[2]sx%[1]s",
			"%[1]sabc%[2]s%[2]sdef%[1]sghi%[2]sx",
			"%[1]sabc%[2]s%[2]sdef%[1]sghi%[2]s",
			"%[1]sabc%[2]sdef%[1]sghi%[2]s%[2]s",
			"%[1]sabc%[2]s%[2]sdef%[1]s",
			"%[1]sabc%[2]s%[2]s%[1]sghi%[2]s%[2]s",
			"%[1]sa%[2]s%[2]sb%[1]sc%[2]s%[2]sd%[1]s",
			"%[1]s%[2]s%[2]sb%[1]sc%[2]s%[2]s",
			"%[1]sa b%[2]s%[2]sc d%[1]se f%[2]s%[2]s",
			"%[1]sabc%[2]s%[2]sdef%[1]sghi%[2]s%[2]s%[1]sjkl%[1]s",
			"%[1]s%[1]sabc%[2]s%[2]sdef%[1]sghi%[2]s%[2]s%[1]s",
			"x %[1]sabc%[2]s%[2]sdef%[1]sghi%[2]s%[2]s y%[1]s z",
			"%[1]sabc%[2]s%[2]s%[2]sdef%[1]sghi%[2]s%[2]s",
			"%[1]sabc%[2]s%[2]sd.f%[1]sg.i%[2]s%[2]s",
			"%[1]sabc%[2]s%[2]sdef\n%[1]sghi%[2]s%[2]s",
		} {
			docs = append(docs, fmt.Sprintf(form, d, o))
		}
	}
	return docs
}

// TestMarkdownOrphanDelimiterSkipMatchesPi renders markdownOrphanDelimiterDocs in a paragraph, a heading and a quote, in the stock and the tagged themes, at a narrow and a wide width.
func TestMarkdownOrphanDelimiterSkipMatchesPi(t *testing.T) {
	var probes []markdownContractProbe
	for _, doc := range markdownOrphanDelimiterDocs() {
		for _, format := range []string{"%s", "# %s", "> %s"} {
			for _, theme := range []string{"tagged", "dark"} {
				probes = append(probes, markdownContractProbe{Text: strings.ReplaceAll(format, "%s", doc), Theme: theme, Ops: markdownContractRenderOps(9, 80)})
			}
		}
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
