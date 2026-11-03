package tui

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// markdownRandomDocs are documents of one to five blocks drawn from a seeded generator, so the corpus is the same on every run: paragraphs (with indented
// and hard-break lines), ATX and setext headings, lists (task markers, empty markers, loose items, lazy lines, nested lists, fences, quotes, tables, display
// math and lines of spaces inside items), quotes whose lines hold any block (nested quotes, lists, indented code, headings, fences) with lazy lines between
// them, fences with info strings, indented code, tables, rules, HTML blocks, display math, link definitions and lines of spaces. Blocks follow each other
// with or without a blank line, and any line may be indented by up to four spaces or a tab. They reach the interactions of the block loop (a list ending a
// quote, a fence inside a list, a table after a paragraph, a lazy line after a heading) that no hand-written document names.
func markdownRandomDocs(seed uint64, count int) []string {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	pick := func(items ...string) string { return items[rng.IntN(len(items))] }
	words := []string{"a", "b", "word", "two words", "longer line of text here", "**bold**", "*it*", "~~s~~", "`code`", "[l](http://u.v)", "![i](u)", "http://a.b", "x_y_z", "**a", "b**", "日本語", "🙂", "a|b", "$x^2$", "<b>x</b>", "&amp;", "[ref]", "[a][ref]", "<http://a.b>", "\\*e\\*", "a\\", "&copy; &#35;", "_a_ __b__", "***x***", "``a ` b``", "a\tb", "www.a.b"}
	text := func() string {
		out := pick(words...)
		for rng.IntN(3) == 0 {
			out += " " + pick(words...)
		}
		if rng.IntN(12) == 0 {
			out += "  " // hard break when a line follows
		}
		return out
	}
	// lead is the indent a line may carry: most lines have none, the rest up to a tab.
	lead := func() string {
		if rng.IntN(4) > 0 {
			return ""
		}
		return pick(" ", "  ", "   ", "    ", "\t")
	}
	var block func(depth int) []string
	// quoteLine puts one block line behind a quote marker.
	quoteLine := func(line string) string {
		return pick("> ", "> ", ">", " > ", ">  ", "   > ", ">\t") + line
	}
	// inner are the lines a list item or quote holds after its first line: text, indented text, a lazy-able paragraph line, blank lines, spaces, nested blocks.
	list := func(depth int) []string {
		ordered := rng.IntN(3) == 0
		task := rng.IntN(5) == 0
		var lines []string
		bullet, delimiter := pick("- ", "- ", "* ", "+ ", "-", " - ", "  * "), pick("1. ", "1) ", "7. ", "1.", "10. ", " 2. ")
		for range 1 + rng.IntN(3) {
			marker := bullet
			if ordered {
				marker = delimiter
			}
			item := marker
			if task {
				item += pick("[ ] ", "[x] ")
			}
			if rng.IntN(8) > 0 {
				item += text()
			}
			lines = append(lines, item)
			pad := strings.Repeat(" ", len(strings.TrimRight(marker, " "))+1)
			switch rng.IntN(9) {
			case 0:
				lines = append(lines, pad+text())
			case 1:
				lines = append(lines, text()) // lazy line
			case 3:
				lines = append(lines, "", pad+text())
			case 4:
				lines = append(lines, pad+pick("- ", "* ", "1. ", "- [ ] ")+text())
				if rng.IntN(2) == 0 {
					lines = append(lines, pad+"  "+pick("- ", "1. ", "")+text())
				}
			case 5:
				if depth < 2 {
					for _, line := range block(depth + 1) {
						lines = append(lines, pad+line)
					}
				}
			case 6:
				lines = append(lines, pick("   ", "  ", " ", "\t", ""), pad+lead()+text())
			case 7:
				lines = append(lines, pick("    "+text(), pad+"    "+text(), "", "  ", pad+pick("```", "~~~", "$$", "\\[", "<div>", "> ")+text()))
			}
			if rng.IntN(6) == 0 {
				lines = append(lines, pick("", "", "  ", "   "))
			}
		}
		return lines
	}
	quote := func(depth int) []string {
		var lines []string
		for range 1 + rng.IntN(3) {
			switch rng.IntN(10) {
			case 0:
				lines = append(lines, quoteLine(pick("- ", "* ", "1. ", "- [ ] ")+text()))
			case 1:
				lines = append(lines, quoteLine(pick("", " ", "    ")+text()))
			case 2:
				lines = append(lines, quoteLine(""))
			case 3:
				if depth < 2 {
					lines = append(lines, quoteLine(pick(">", "> ", "> > ", ">>")+text()))
				}
			case 4:
				lines = append(lines, quoteLine(pick("# ", "## ", "---", "===", "```", "$$", "| a | b |", "|---|---|", "<div>", "    ", "  - ")+text()))
			default:
				lines = append(lines, quoteLine(text()))
			}
			for rng.IntN(5) == 0 {
				lines = append(lines, lead()+pick(text(), text(), "- "+text(), "    "+text(), "", "  ", "> "+text(), "```", "# h", "|---|---|", "$$", "  \\[ a"))
			}
		}
		return lines
	}
	block = func(depth int) []string {
		switch rng.IntN(20) {
		case 0, 1:
			lines := []string{lead() + text()}
			for rng.IntN(3) == 0 {
				lines = append(lines, lead()+text())
			}
			return lines
		case 2:
			return []string{lead() + strings.Repeat("#", 1+rng.IntN(6)) + " " + text() + pick("", "", " #", " ##", "#")}
		case 3, 4, 5:
			return list(depth)
		case 6, 7, 8:
			return quote(depth)
		case 9:
			fence := pick("```", "```go", "~~~", "``` go title", "~~~ a`b", "````", "   ```", "  ~~~js")
			lines := []string{fence, text()}
			if rng.IntN(2) == 0 {
				lines = append(lines, pick("", "  ", "    x"), text())
			}
			if rng.IntN(4) > 0 {
				c := strings.TrimLeft(fence, " ")[:1]
				lines = append(lines, pick("", "  ", " ")+c+strings.Repeat(c, 2+rng.IntN(2)-1))
			}
			return lines
		case 10:
			return []string{"    " + text(), pick("    ", "     ", "\t", "") + text()}
		case 11:
			lines := []string{lead() + "| " + text() + " | " + text() + " |", lead() + pick("|---|---|", "|:-|-:|", "| --- | :-: |", "---|---", "|-|")}
			for range rng.IntN(4) {
				lines = append(lines, "| "+text()+" | "+text()+" |")
			}
			return lines
		case 12:
			return []string{lead() + pick("---", "***", "___", "- - -", "-----", "* * *")}
		case 13:
			return []string{lead() + pick("<div>"+text()+"</div>", "<!-- c -->", "<div>\n"+text()+"\n</div>", "<pre>\nx\n\ny\n</pre>", "<br>", "<span>"+text(), "<?php x ?>", "<![CDATA[ a ]]>", "</div>", "<a href=\"u\">", "<script>\nx\n</script>")}
		case 14:
			return []string{lead() + pick("$$\nx^2\n$$", "$$x$$", "\\[\na+b\n\\]", "$$ x $$", "\\[ x \\]", "$$\n$$", "$$x", "\\[x")}
		case 15:
			return []string{pick("[ref]: http://x", "[a]: http://x \"t\"", "  [a]: <http://x> 'u'", "[b]:\nhttp://y", "[c]: http://z\n  \"title\"")}
		case 16:
			return []string{pick("", "   ", "  ", "\t", " ")}
		case 17:
			return []string{lead() + text(), lead() + pick("===", "---", "  ===", " --", "=")}
		case 18:
			return []string{pick("- ", "-", "1. ", "* ", "1)") + pick("", " "), pick("  ", "   ", "    ", "") + text()}
		default:
			return []string{lead() + text(), lead() + pick("$$", "\\[", "$$x$$", "\\[x\\]", "```", "# h", "> q", "- b", "1. b", "<div>", "|a|b|\n|-|-|", "***") + text()}
		}
	}
	docs := make([]string, 0, count)
	for range count {
		var lines []string
		for range 1 + rng.IntN(5) {
			lines = append(lines, block(0)...)
			if rng.IntN(5) > 1 {
				lines = append(lines, "")
			}
		}
		docs = append(docs, strings.Join(lines, "\n"))
	}
	return docs
}

// markdownRandomProbes are the probes of TestMarkdownRandomDocsMatchPi: each document of the seeded corpus at a narrow, two middle and a wide width, with every second one padded, every third under a text
// color and every seventh with preserved list markers.
func markdownRandomProbes() []markdownContractProbe {
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
	return probes
}

// TestMarkdownRandomDocsMatchPi requires the rows of markdownRandomProbes to equal pi-tui's, except for the documents of markdownRandomKnownDifferences, which must still differ: each holds a shape of
// markdownKnownDifferenceProbes that PiG does not render as Pi does.
func TestMarkdownRandomDocsMatchPi(t *testing.T) {
	probes := markdownRandomProbes()
	differences, _ := markdownContractDifferences(t, probes)
	known := map[int]bool{}
	for _, index := range markdownRandomKnownDifferences {
		known[index] = true
	}
	unexpected, fixed := 0, 0
	for i, probe := range probes {
		reason, differs := differences[i]
		switch {
		case differs && !known[i]:
			if unexpected++; unexpected <= 20 {
				t.Errorf("document %d differs from Pi and is not a known difference: %s", i, reason)
			}
		case !differs && known[i]:
			fixed++
			t.Errorf("document %d (%q) is listed in markdownRandomKnownDifferences but matches Pi now: remove it", i, probe.Text)
		}
	}
	t.Logf("%d of %d documents differ from Pi, %d of them known; %d unexpected, %d fixed", len(differences), len(probes), len(markdownRandomKnownDifferences), unexpected, fixed)
}
