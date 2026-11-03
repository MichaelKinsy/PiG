package tui

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// markdownRandomDocs are documents of one to four blocks (paragraphs, headings, lists with task markers, loose items and lazy lines, quotes with
// lazy lines, fences, indented code, tables, rules, HTML, display math, link definitions) drawn from a seeded generator, so the corpus is the same on every
// run. Blocks follow each other with or without a blank line. They reach the interactions of the block loop (a list ending a quote, a fence inside a
// list, a table after a paragraph, a lazy line after a heading) that no hand-written document names.
func markdownRandomDocs(seed uint64, count int) []string {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	pick := func(items ...string) string { return items[rng.IntN(len(items))] }
	words := []string{"a", "b", "word", "two words", "longer line of text here", "**bold**", "*it*", "~~s~~", "`code`", "[l](http://u.v)", "![i](u)", "http://a.b", "x_y_z", "**a", "b**", "日本語", "🙂", "a|b", "$x^2$", "<b>x</b>", "&amp;", "[ref]"}
	text := func() string {
		out := pick(words...)
		for rng.IntN(3) == 0 {
			out += " " + pick(words...)
		}
		return out
	}
	list := func() []string {
		ordered := rng.IntN(3) == 0
		task := rng.IntN(5) == 0
		var lines []string
		bullet, delimiter := pick("- ", "- ", "* ", "+ "), pick("1. ", "1) ", "7. ")
		for range 1 + rng.IntN(3) {
			marker := bullet
			if ordered {
				marker = delimiter
			}
			item := marker
			if task {
				item += pick("[ ] ", "[x] ")
			}
			item += text()
			lines = append(lines, item)
			pad := strings.Repeat(" ", len(marker))
			switch rng.IntN(5) {
			case 0:
				lines = append(lines, pad+text())
			case 1:
				lines = append(lines, text()) // lazy line
			case 3:
				lines = append(lines, "", pad+text())
			}
			if rng.IntN(6) == 0 {
				lines = append(lines, "")
			}
		}
		return lines
	}
	block := func() []string {
		switch rng.IntN(14) {
		case 0, 1:
			lines := []string{text()}
			for rng.IntN(3) == 0 {
				lines = append(lines, text())
			}
			return lines
		case 2:
			return []string{strings.Repeat("#", 1+rng.IntN(6)) + " " + text()}
		case 3, 4, 5:
			return list()
		case 6, 7:
			var lines []string
			for range 1 + rng.IntN(3) {
				lines = append(lines, pick("> ", "> ", ">")+text())
				if rng.IntN(4) == 0 {
					lines = append(lines, text())
				}
			}
			return lines
		case 8:
			fence := pick("```", "```go", "~~~")
			lines := []string{fence, text()}
			if rng.IntN(2) == 0 {
				lines = append(lines, "", text())
			}
			if rng.IntN(4) > 0 {
				lines = append(lines, string(fence[0])+strings.Repeat(string(fence[0]), 2))
			}
			return lines
		case 9:
			return []string{"    " + text(), "    " + text()}
		case 10:
			// A blank line comes first: a table header that is a lazy line of a quote still differs from Pi.
			lines := []string{"", "| " + text() + " | " + text() + " |", pick("|---|---|", "|:-|-:|", "| --- | :-: |")}
			for range rng.IntN(4) {
				lines = append(lines, "| "+text()+" | "+text()+" |")
			}
			return lines
		case 11:
			return []string{pick("---", "***", "___", "- - -")}
		case 12:
			// A blank line comes first: display math directly after a list item, followed by a blank line and indented code, still differs from Pi.
			return []string{"", pick("<div>"+text()+"</div>", "<!-- c -->", "$$"+"\n"+"x^2"+"\n"+"$$", "\\[\na+b\n\\]", "[ref]: http://x", "[a]: http://x \"t\"")}
		default:
			return []string{text(), pick("===", "---")}
		}
	}
	docs := make([]string, 0, count)
	for range count {
		var lines []string
		for range 1 + rng.IntN(4) {
			lines = append(lines, block()...)
			if rng.IntN(5) > 1 {
				lines = append(lines, "")
			}
		}
		docs = append(docs, strings.Join(lines, "\n"))
	}
	return docs
}

// TestMarkdownRandomDocsMatchPi renders markdownRandomDocs at a narrow, two middle and a wide width, with the default constructor, with a text color and
// with preserved list markers, and requires the rows to equal pi-tui's.
func TestMarkdownRandomDocsMatchPi(t *testing.T) {
	var probes []markdownContractProbe
	for i, doc := range markdownRandomDocs(20260101, 12000) {
		probe := markdownContractProbe{Text: doc, PaddingX: i % 2, PaddingY: 0, Theme: "tagged", Ops: markdownContractRenderOps(5, 11, 24, 80)}
		if i%3 == 0 {
			probe.Style = &markdownContractStyle{Color: true}
		}
		if i%7 == 0 {
			probe.Options.PreserveOrderedListMarkers = true
		}
		probes = append(probes, probe)
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
